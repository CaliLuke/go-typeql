package gotype

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/v3/given"
)

type batchTestTx struct {
	mockTx
	queriesWithRows []string
	rows            []*given.TypedRows
	failAt          int
	events          []string
}

func (t *batchTestTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	t.events = append(t.events, "legacy")
	return t.mockTx.QueryWithContext(ctx, query)
}

func (t *batchTestTx) QueryWithGivenRows(ctx context.Context, query string, rows *given.TypedRows) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.queriesWithRows = append(t.queriesWithRows, query)
	t.rows = append(t.rows, rows)
	t.events = append(t.events, "batch")
	if t.failAt == len(t.rows) {
		return nil, fmt.Errorf("batch failed")
	}
	results := make([]map[string]any, len(rows.Rows))
	for i, row := range rows.Rows {
		key := row[0].Value.(string)
		results[len(results)-1-i] = map[string]any{"_key": key, "_iid": fmt.Sprintf("0x%04x", i+len(t.rows)*insertBatchSize)}
	}
	return results, nil
}

type batchTestConn struct {
	Conn
	tx *batchTestTx
}

func (c *batchTestConn) Transaction(string, int) (Tx, error) { return c.tx, nil }

func batchTestManager(t *testing.T, tx *batchTestTx) *Manager[testCompany] {
	t.Helper()
	ClearRegistry()
	MustRegister[testCompany]()
	return MustNewManager[testCompany](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
}

func TestInsertMany_BatchesAndMapsReorderedIIDs(t *testing.T) {
	tx := &batchTestTx{}
	mgr := batchTestManager(t, tx)
	instances := make([]*testCompany, 65)
	for i := range instances {
		instances[i] = &testCompany{Name: fmt.Sprintf("company-%d", i), Industry: "tools"}
	}
	if err := mgr.InsertMany(context.Background(), instances); err != nil {
		t.Fatal(err)
	}
	if len(tx.rows) != 3 || len(tx.rows[0].Rows) != 32 || len(tx.rows[1].Rows) != 32 || len(tx.rows[2].Rows) != 1 {
		t.Fatalf("batch sizes: %d, %d, %d", len(tx.rows), len(tx.rows[0].Rows), len(tx.rows[1].Rows))
	}
	if len(tx.queries) != 0 || !tx.committed {
		t.Fatalf("fallback queries=%d committed=%v", len(tx.queries), tx.committed)
	}
	for i, instance := range instances {
		if instance.GetIID() == "" {
			t.Fatalf("instance %d missing IID", i)
		}
	}
	if instances[0].GetIID() != "0x0020" || instances[1].GetIID() != "0x0021" {
		t.Fatalf("unexpected IID mapping: %s, %s", instances[0].GetIID(), instances[1].GetIID())
	}
}

func TestInsertMany_BatchDuplicateInputAndFailedCommit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		duplicate bool
	}{
		{"duplicate", true},
		{"commit failure", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &batchTestTx{}
			mgr := batchTestManager(t, tx)
			instances := []*testCompany{{Name: "Acme", Industry: "Tools"}, {Name: "Other", Industry: "Tools"}}
			if tc.duplicate {
				instances[1].Name = "Acme"
			} else {
				tx.commitErr = fmt.Errorf("commit failed")
			}
			err := mgr.InsertMany(context.Background(), instances)
			if err == nil {
				t.Fatal("expected failure")
			}
			if tc.duplicate && (!strings.Contains(err.Error(), "duplicate key") || len(tx.rows) != 0) {
				t.Fatalf("duplicate: %v, queries=%d", err, len(tx.rows))
			}
			for _, instance := range instances {
				if instance.GetIID() != "" {
					t.Fatalf("IID assigned on failure: %s", instance.GetIID())
				}
			}
		})
	}
}

func TestInsertMany_BatchesNilOptionalFields(t *testing.T) {
	tx := &batchTestTx{}
	ClearRegistry()
	MustRegister[testPerson]()
	mgr := MustNewManager[testPerson](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
	age := 30
	instances := []*testPerson{{Name: "Alice", Email: "a@example.com", Age: &age}, {Name: "Bob", Email: "b@example.com"}}
	if err := mgr.InsertMany(context.Background(), instances); err != nil {
		t.Fatal(err)
	}
	if len(tx.queriesWithRows) != 1 || len(tx.queries) != 0 {
		t.Fatalf("expected one batch query, got batch=%d legacy=%d", len(tx.queriesWithRows), len(tx.queries))
	}
	query := tx.queriesWithRows[0]
	for _, want := range []string{"$v2: integer?", "try { $e has age == $v2; };"} {
		if !strings.Contains(query, want) {
			t.Fatalf("batch query lacks %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, ", has age ==") {
		t.Fatalf("optional age must only be inside the try block:\n%s", query)
	}
	rows := tx.rows[0].Rows
	if rows[0][2] != (given.Value{Type: "integer", Value: int64(30)}) || rows[1][2] != (given.Value{Type: "empty"}) {
		t.Fatalf("age values = %+v, %+v; want integer 30 and empty", rows[0][2], rows[1][2])
	}
	for i, instance := range instances {
		if instance.GetIID() == "" {
			t.Fatalf("instance %d missing IID", i)
		}
	}
}

func TestInsertMany_BatchEmptyAndCancellation(t *testing.T) {
	tx := &batchTestTx{}
	mgr := batchTestManager(t, tx)
	if err := mgr.InsertMany(context.Background(), nil); err != nil || tx.committed {
		t.Fatalf("empty insert = %v, committed=%v", err, tx.committed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.InsertMany(ctx, []*testCompany{{Name: "A"}, {Name: "B"}}); err == nil {
		t.Fatal("expected context cancellation")
	}
}

func TestInsertMany_BatchUnsupportedModelShapesUseLegacyPath(t *testing.T) {
	tx := &batchTestTx{}
	for _, tc := range []struct {
		name string
		try  func() bool
	}{
		{"slice", func() bool {
			ClearRegistry()
			MustRegister[testTagged]()
			mgr := MustNewManager[testTagged](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.insertManyBatched(context.Background(), tx, []*testTagged{{}, {}}, make([]string, 2))
			return used
		}},
		{"decimal", func() bool {
			ClearRegistry()
			MustRegister[decimalProduct]()
			mgr := MustNewManager[decimalProduct](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.insertManyBatched(context.Background(), tx, []*decimalProduct{{}, {}}, make([]string, 2))
			return used
		}},
		{"relation", func() bool {
			ClearRegistry()
			MustRegister[testEmployment]()
			mgr := MustNewManager[testEmployment](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.insertManyBatched(context.Background(), tx, []*testEmployment{{}, {}}, make([]string, 2))
			return used
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.try() {
				t.Fatal("unsupported model used typed batch")
			}
		})
	}
}
