package gotype

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestUpdateMany_BatchesHeterogeneousRows(t *testing.T) {
	tx := &batchTestTx{}
	mgr := batchTestManager(t, tx)
	rows := make([]*testCompany, 65)
	for i := range rows {
		rows[i] = &testCompany{Name: fmt.Sprintf("Company-%d", i), Industry: fmt.Sprintf("Industry-%d", i)}
		rows[i].SetIID(fmt.Sprintf("0x%04x", i+1))
	}
	if err := mgr.UpdateMany(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	if len(tx.rows) != 8 || len(tx.queries) != 1 || !tx.committed {
		t.Fatalf("batches=%d fallback=%d committed=%v", len(tx.rows), len(tx.queries), tx.committed)
	}
	if tx.rows[0].Rows[1][0].Value != "0x0002" || tx.rows[0].Rows[1][1].Value != "Industry-1" {
		t.Fatal("row values not aligned with IID")
	}
}

func TestUpdateMany_BatchFailureDoesNotCommit(t *testing.T) {
	for _, failure := range []struct {
		name string
		tx   *batchTestTx
	}{
		{"later chunk", &batchTestTx{failAt: 2}},
		{"commit", &batchTestTx{mockTx: mockTx{commitErr: fmt.Errorf("commit failed")}}},
	} {
		t.Run(failure.name, func(t *testing.T) {
			mgr := batchTestManager(t, failure.tx)
			rows := make([]*testCompany, 16)
			for i := range rows {
				rows[i] = &testCompany{Industry: "New"}
				rows[i].SetIID(fmt.Sprintf("0x%04x", i+1))
			}
			if err := mgr.UpdateMany(context.Background(), rows); err == nil || failure.tx.committed {
				t.Fatalf("error=%v committed=%v", err, failure.tx.committed)
			}
		})
	}
}

func TestUpdateMany_BatchBoundTransactionOwnership(t *testing.T) {
	tx := &batchTestTx{}
	_ = batchTestManager(t, tx)
	db := NewDatabase(&batchTestConn{tx: tx}, "test_db")
	scope, err := db.Begin(WriteTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	mgr := MustNewManagerWithTx[testCompany](scope)
	first := &testCompany{Industry: "First"}
	first.SetIID("0x01")
	second := &testCompany{Industry: "Second"}
	second.SetIID("0x02")
	if err := mgr.UpdateMany(context.Background(), []*testCompany{first, second}); err != nil {
		t.Fatal(err)
	}
	if tx.committed || len(tx.rows) != 1 {
		t.Fatalf("bound transaction committed=%v batches=%d", tx.committed, len(tx.rows))
	}
	if err := scope.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateMany_BatchFallsBackForDuplicateIIDAndMissingOptional(t *testing.T) {
	t.Run("duplicate IID", func(t *testing.T) {
		tx := &batchTestTx{}
		mgr := batchTestManager(t, tx)
		first := &testCompany{Industry: "First"}
		first.SetIID("0x0a")
		second := &testCompany{Industry: "Second"}
		second.SetIID("0x0A")
		if err := mgr.UpdateMany(context.Background(), []*testCompany{first, second}); err != nil {
			t.Fatal(err)
		}
		if len(tx.rows) != 0 || len(tx.queries) != 2 {
			t.Fatalf("batch=%d fallback=%d", len(tx.rows), len(tx.queries))
		}
	})
	t.Run("missing optional", func(t *testing.T) {
		ClearRegistry()
		MustRegister[testPerson]()
		tx := &batchTestTx{}
		mgr := MustNewManager[testPerson](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
		first := &testPerson{Email: "a@test.com"}
		first.SetIID("0x01")
		second := &testPerson{Email: "b@test.com"}
		second.SetIID("0x02")
		if err := mgr.UpdateMany(context.Background(), []*testPerson{first, second}); err != nil {
			t.Fatal(err)
		}
		if len(tx.rows) != 0 || len(tx.queries) != 2 {
			t.Fatalf("batch=%d fallback=%d", len(tx.rows), len(tx.queries))
		}
	})
}

func TestUpdateMany_BatchesCompatibleRunsAroundOptionalDeletion(t *testing.T) {
	ClearRegistry()
	MustRegister[testPerson]()
	tx := &batchTestTx{}
	mgr := MustNewManager[testPerson](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
	rows := make([]*testPerson, 5)
	for i := range rows {
		rows[i] = &testPerson{Email: fmt.Sprintf("changed-%d@test.com", i)}
		rows[i].SetIID(fmt.Sprintf("0x%02x", i+1))
		if i != 2 {
			rows[i].Age = new(i + 30)
		}
	}
	if err := mgr.UpdateMany(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tx.events, []string{"batch", "legacy", "batch"}) {
		t.Fatalf("query order=%v", tx.events)
	}
	if len(tx.rows[0].Rows) != 2 || len(tx.rows[1].Rows) != 2 || !tx.committed {
		t.Fatalf("batches=%d committed=%v", len(tx.rows), tx.committed)
	}
}

func TestUpdateMany_BatchFallsBackForSliceDecimalAndRelation(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*batchTestTx) error
	}{
		{"slice", func(tx *batchTestTx) error {
			ClearRegistry()
			MustRegister[testTagged]()
			mgr := MustNewManager[testTagged](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			first := &testTagged{Tags: []string{"A"}}
			first.SetIID("0x01")
			second := &testTagged{Tags: []string{"B"}}
			second.SetIID("0x02")
			return mgr.UpdateMany(context.Background(), []*testTagged{first, second})
		}},
		{"decimal", func(tx *batchTestTx) error {
			ClearRegistry()
			MustRegister[decimalProduct]()
			mgr := MustNewManager[decimalProduct](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			first := &decimalProduct{Price: 1, Exact: "1.0"}
			first.SetIID("0x01")
			second := &decimalProduct{Price: 2, Exact: "2.0"}
			second.SetIID("0x02")
			return mgr.UpdateMany(context.Background(), []*decimalProduct{first, second})
		}},
		{"relation", func(tx *batchTestTx) error {
			registerTestTypes(t)
			mgr := MustNewManager[testEmployment](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			first := &testEmployment{}
			first.SetIID("0x01")
			second := &testEmployment{}
			second.SetIID("0x02")
			return mgr.UpdateMany(context.Background(), []*testEmployment{first, second})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &batchTestTx{}
			if err := tc.run(tx); err != nil {
				t.Fatal(err)
			}
			if len(tx.rows) != 0 || len(tx.queries) != 2 {
				t.Fatalf("batch=%d fallback=%d", len(tx.rows), len(tx.queries))
			}
		})
	}
}
