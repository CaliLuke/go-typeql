//go:build cgo && typedb && integration

package gotype_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

type ProjectionBase struct {
	gotype.BaseEntity
	Code string `typedb:"projection-code,key"`
}

func TestIntegration_ProjectionMissingScalarAndEmptySlice(t *testing.T) {
	t.Run("missing scalar", func(t *testing.T) {
		db := setupTestDBWith(t, func() { _ = gotype.Register[Profile]() })
		mgr := gotype.MustNewManager[Profile](db)
		if err := mgr.Insert(context.Background(), &Profile{Username: "alice"}); err != nil {
			t.Fatal(err)
		}
		rows, err := mgr.GetProjected(context.Background(), nil, gotype.Projection{Fields: []string{"bio"}})
		if err != nil || len(rows) != 1 {
			t.Fatalf("projected read: rows=%d err=%v", len(rows), err)
		}
		if got := rows[0].Fields["bio"]; got.Present || got.Value != nil {
			t.Fatalf("missing bio = %+v", got)
		}
	})
	t.Run("empty slice", func(t *testing.T) {
		db := setupTestDBWith(t, func() { _ = gotype.Register[TaggedDoc]() })
		mgr := gotype.MustNewManager[TaggedDoc](db)
		if err := mgr.Insert(context.Background(), &TaggedDoc{DocID: "doc-1"}); err != nil {
			t.Fatal(err)
		}
		rows, err := mgr.GetProjected(context.Background(), nil, gotype.Projection{Fields: []string{"doc-tag"}})
		if err != nil || len(rows) != 1 {
			t.Fatalf("projected read: rows=%d err=%v", len(rows), err)
		}
		if got := rows[0].Fields["doc-tag"]; !got.Present || reflect.TypeOf(got.Value) == nil || reflect.TypeOf(got.Value).Kind() != reflect.Slice || reflect.ValueOf(got.Value).Len() != 0 {
			t.Fatalf("empty attribute slice has wrong shape: %+v (%T)", got, got.Value)
		}
	})
}

func TestIntegration_ProjectionSelectedRoleRequiresLink(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
	ctx := context.Background()
	people := gotype.MustNewManager[Person](db)
	alice := &Person{Name: "Alice", Email: "alice@example.test"}
	if err := people.Insert(ctx, alice); err != nil {
		t.Fatal(err)
	}
	jobs := gotype.MustNewManager[Employment](db)
	if err := jobs.Insert(ctx, &Employment{Employee: alice, StartDate: "2026"}); err != nil {
		t.Fatal(err)
	}
	all, err := jobs.GetProjected(ctx, nil, gotype.Projection{Fields: []string{"start-date"}})
	if err != nil || len(all) != 1 {
		t.Fatalf("unrestricted projection: rows=%d err=%v", len(all), err)
	}
	withEmployee, err := jobs.GetProjected(ctx, nil, gotype.Projection{Roles: map[string][]string{"employee": {"name"}}})
	if err != nil || len(withEmployee) != 1 {
		t.Fatalf("employee projection: rows=%d err=%v", len(withEmployee), err)
	}
	withEmployer, err := jobs.GetProjected(ctx, nil, gotype.Projection{Roles: map[string][]string{"employer": nil}})
	if err != nil || len(withEmployer) != 0 {
		t.Fatalf("absent employer did not filter relation: rows=%d err=%v", len(withEmployer), err)
	}
}

func TestIntegration_ProjectionMultipleRolePlayers(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Squad]()
	})
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, "define relation squad relates squad-member @card(0..);"); err != nil {
		t.Fatal(err)
	}
	people := gotype.MustNewManager[Person](db)
	alice := &Person{Name: "Alice", Email: "alice@example.test"}
	bob := &Person{Name: "Bob", Email: "bob@example.test"}
	for _, person := range []*Person{alice, bob} {
		if err := people.Insert(ctx, person); err != nil {
			t.Fatal(err)
		}
	}
	squads := gotype.MustNewManager[Squad](db)
	if err := squads.Insert(ctx, &Squad{Members: []*Person{alice, bob}, SquadName: "alpha"}); err != nil {
		t.Fatal(err)
	}
	rows, err := squads.GetProjected(ctx, map[string]any{"squad-name": "alpha"}, gotype.Projection{
		Roles: map[string][]string{"squad-member": {"name"}},
	})
	if err != nil || len(rows) != 2 {
		t.Fatalf("projected players: rows=%d err=%v", len(rows), err)
	}
	if rows[0].IID == "" || rows[0].IID != rows[1].IID {
		t.Fatalf("expected two answer rows for one relation: %+v", rows)
	}
	want := map[string]string{"Alice": alice.GetIID(), "Bob": bob.GetIID()}
	for _, row := range rows {
		member := row.Roles["squad-member"]
		if member == nil || member.TypeName != "person" {
			t.Fatalf("missing member identity: %+v", row)
		}
		name, ok := member.Fields["name"].Value.(string)
		if !ok || want[name] != member.IID {
			t.Fatalf("wrong member: %+v", member)
		}
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing projected members: %v", want)
	}
}

func TestIntegration_ProjectionPreservesConcreteSubtype(t *testing.T) {
	db := setupTestDBWith(t, func() { _ = gotype.Register[ProjectionBase]() })
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, "define entity projection-child, sub projection-base;"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecuteWrite(ctx, `insert $e isa projection-child, has projection-code "child-1";`); err != nil {
		t.Fatal(err)
	}
	mgr := gotype.MustNewManager[ProjectionBase](db)
	rows, err := mgr.GetProjected(ctx, map[string]any{"projection-code": "child-1"}, gotype.Projection{Fields: []string{"projection-code"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("projected child: rows=%d err=%v", len(rows), err)
	}
	if rows[0].TypeName != "projection-child" || rows[0].IID == "" || rows[0].Fields["projection-code"].Value != "child-1" {
		t.Fatalf("concrete child identity lost: %+v", rows[0])
	}
}
