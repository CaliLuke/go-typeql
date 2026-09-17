package gotype

import (
	"context"
	"fmt"
	"testing"
)

func TestPooledBulkOperationsPreserveBatching(t *testing.T) {
	for _, op := range []string{"insert", "put", "update", "update_with"} {
		t.Run(op, func(t *testing.T) {
			ClearRegistry()
			MustRegister[testCompany]()
			tx := &batchTestTx{}
			conn := &fixedTxConn{poolMockConn: newPoolMockConn(1), tx: tx}
			db, err := NewDatabaseWithPool(PoolConfig{MaxSize: 1}, "test_db", func() (Conn, error) { return conn, nil })
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mgr := MustNewManager[testCompany](db)
			instances := make([]*testCompany, 65)
			results := make([]map[string]any, len(instances))
			for i := range instances {
				instances[i] = &testCompany{Name: fmt.Sprintf("company-%d", i), Industry: "tools"}
				instances[i].SetIID(fmt.Sprintf("0x%04x", i+1))
				results[i] = map[string]any{"_iid": instances[i].GetIID(), "name": instances[i].Name, "industry": "tools"}
			}
			wantBatch, wantLegacy := 3, 0
			switch op {
			case "insert":
				err = mgr.InsertMany(context.Background(), instances)
			case "put":
				err = mgr.PutMany(context.Background(), instances)
			case "update":
				wantBatch, wantLegacy = 8, 1 // Final single row uses the legacy path.
				err = mgr.UpdateMany(context.Background(), instances)
			case "update_with":
				wantBatch, wantLegacy = 8, 2 // Initial read plus the final single row.
				tx.responses = [][]map[string]any{results}
				_, err = mgr.Query().UpdateWith(context.Background(), func(v *testCompany) { v.Industry = "changed" })
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(tx.rows) != wantBatch || len(tx.queries) != wantLegacy || !tx.committed {
				t.Fatalf("batch=%d legacy=%d committed=%v, want %d/%d/true", len(tx.rows), len(tx.queries), tx.committed, wantBatch, wantLegacy)
			}
		})
	}
}

func TestPooledLegacyTransactionDoesNotAdvertiseBatching(t *testing.T) {
	conn := newPoolMockConn(1)
	db, err := NewDatabaseWithPool(PoolConfig{MaxSize: 1}, "test_db", func() (Conn, error) { return conn, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Transaction(WriteTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if _, ok := tx.(batchInsertTx); ok {
		t.Fatal("pool advertised an unsupported batch capability")
	}
}
