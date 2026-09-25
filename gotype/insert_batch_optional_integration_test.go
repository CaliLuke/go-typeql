//go:build cgo && typedb && integration

package gotype_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/gotype"
)

// TestIntegration_InsertManyNilOptionalFields checks the typed-row batch for
// rows where an optional attribute is nil: the entity is inserted without the
// attribute, like Insert, and every row gets its IID.
func TestIntegration_InsertManyNilOptionalFields(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()
	mgr, err := gotype.NewManager[Person](db)
	if err != nil {
		t.Fatal(err)
	}
	persons := make([]*Person, 40) // more than one batch of 32
	for i := range persons {
		persons[i] = &Person{Name: fmt.Sprintf("p%02d", i), Email: fmt.Sprintf("p%02d@example.com", i)}
		if i%3 == 0 {
			persons[i].Age = new(i)
		}
	}
	if err := mgr.InsertMany(ctx, persons); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
	for i, p := range persons {
		if p.GetIID() == "" {
			t.Fatalf("person %d has no IID", i)
		}
		got, err := mgr.GetByIID(ctx, p.GetIID())
		if err != nil || got == nil {
			t.Fatalf("GetByIID(%s): %v, %v", p.GetIID(), got, err)
		}
		if got.Name != p.Name || got.Email != p.Email {
			t.Fatalf("person %d read back %+v, want %+v", i, got, p)
		}
		switch {
		case p.Age == nil && got.Age != nil:
			t.Fatalf("person %d: age %d, want none", i, *got.Age)
		case p.Age != nil && (got.Age == nil || *got.Age != *p.Age):
			t.Fatalf("person %d: age %v, want %d", i, got.Age, *p.Age)
		}
	}
}
