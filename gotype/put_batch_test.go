package gotype

import (
	"context"
	"fmt"
	"testing"
)

func TestPutMany_BatchesAndMapsReorderedIIDs(t *testing.T) {
	tx := &batchTestTx{}
	mgr := batchTestManager(t, tx)
	instances := make([]*testCompany, 65)
	for i := range instances {
		instances[i] = &testCompany{Name: fmt.Sprintf("company-%d", i), Industry: "tools"}
	}
	if err := mgr.PutMany(context.Background(), instances); err != nil {
		t.Fatal(err)
	}
	if len(tx.rows) != 3 || len(tx.rows[0].Rows) != 32 || len(tx.rows[1].Rows) != 32 || len(tx.rows[2].Rows) != 1 {
		t.Fatalf("unexpected batch sizes: %d", len(tx.rows))
	}
	if len(tx.queries) != 0 || !tx.committed {
		t.Fatalf("fallback queries=%d committed=%v", len(tx.queries), tx.committed)
	}
	if instances[0].GetIID() != "0x0020" || instances[1].GetIID() != "0x0021" {
		t.Fatalf("incorrect reordered IID mapping: %s, %s", instances[0].GetIID(), instances[1].GetIID())
	}
	for _, query := range tx.queriesWithRows {
		assertContains(t, query, "put $e")
		assertContains(t, query, `"_key": $v0`)
	}
}

func TestPutMany_BatchCommitFailureLeavesIIDsUnset(t *testing.T) {
	tx := &batchTestTx{mockTx: mockTx{commitErr: fmt.Errorf("commit failed")}}
	mgr := batchTestManager(t, tx)
	instances := []*testCompany{{Name: "A", Industry: "tools"}, {Name: "B", Industry: "tools"}}
	if err := mgr.PutMany(context.Background(), instances); err == nil {
		t.Fatal("expected commit failure")
	}
	if instances[0].GetIID() != "" || instances[1].GetIID() != "" {
		t.Fatal("IIDs assigned before commit")
	}
}

func TestPutMany_BatchLaterChunkFailureRollsBackIIDs(t *testing.T) {
	tx := &batchTestTx{failAt: 2}
	mgr := batchTestManager(t, tx)
	instances := make([]*testCompany, 33)
	for i := range instances {
		instances[i] = &testCompany{Name: fmt.Sprintf("company-%d", i), Industry: "tools"}
	}
	if err := mgr.PutMany(context.Background(), instances); err == nil {
		t.Fatal("expected second chunk failure")
	}
	if tx.committed || len(tx.rows) != 2 {
		t.Fatalf("committed=%v batches=%d", tx.committed, len(tx.rows))
	}
	for i, instance := range instances {
		if instance.GetIID() != "" {
			t.Fatalf("instance %d assigned IID before failed commit", i)
		}
	}
}

func TestPutMany_BatchFallsBackForDuplicateAndOptional(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func() ([]*testCompany, [][]map[string]any)
	}{
		{"duplicate", func() ([]*testCompany, [][]map[string]any) {
			return []*testCompany{{Name: "A"}, {Name: "A"}}, [][]map[string]any{{{"_iid": "0x01"}}, {{"_iid": "0x01"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instances, responses := tc.setup()
			tx := &batchTestTx{mockTx: mockTx{responses: responses}}
			mgr := batchTestManager(t, tx)
			if err := mgr.PutMany(context.Background(), instances); err != nil {
				t.Fatal(err)
			}
			if len(tx.rows) != 0 || len(tx.queries) != 2 || instances[0].GetIID() != instances[1].GetIID() {
				t.Fatalf("duplicate fallback: batch=%d legacy=%d IIDs=%s/%s", len(tx.rows), len(tx.queries), instances[0].GetIID(), instances[1].GetIID())
			}
		})
	}
	tx := &batchTestTx{mockTx: mockTx{responses: [][]map[string]any{{{"_iid": "0x01"}}, {{"_iid": "0x02"}}}}}
	ClearRegistry()
	MustRegister[testPerson]()
	mgr := MustNewManager[testPerson](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
	if err := mgr.PutMany(context.Background(), []*testPerson{{Name: "A", Email: "a@test.com"}, {Name: "B", Email: "b@test.com"}}); err != nil {
		t.Fatal(err)
	}
	if len(tx.rows) != 0 || len(tx.queries) != 2 {
		t.Fatalf("optional field fallback: batch=%d legacy=%d", len(tx.rows), len(tx.queries))
	}
}

func TestPutMany_BatchUnsupportedModelShapesUseLegacyPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		try  func(*batchTestTx) bool
	}{
		{"slice", func(tx *batchTestTx) bool {
			ClearRegistry()
			MustRegister[testTagged]()
			mgr := MustNewManager[testTagged](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.putManyBatched(context.Background(), tx, []*testTagged{{}, {}}, make([]string, 2))
			return used
		}},
		{"decimal", func(tx *batchTestTx) bool {
			ClearRegistry()
			MustRegister[decimalProduct]()
			mgr := MustNewManager[decimalProduct](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.putManyBatched(context.Background(), tx, []*decimalProduct{{}, {}}, make([]string, 2))
			return used
		}},
		{"relation", func(tx *batchTestTx) bool {
			ClearRegistry()
			MustRegister[testEmployment]()
			mgr := MustNewManager[testEmployment](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
			used, _ := mgr.putManyBatched(context.Background(), tx, []*testEmployment{{}, {}}, make([]string, 2))
			return used
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &batchTestTx{}
			if tc.try(tx) {
				t.Fatal("unsupported model used typed batch")
			}
		})
	}
}

func TestPutMany_FallbackExecutesSliceDecimalAndRelation(t *testing.T) {
	newTx := func() *batchTestTx {
		return &batchTestTx{mockTx: mockTx{responses: [][]map[string]any{{{"_iid": "0x01"}}, {{"_iid": "0x02"}}}}}
	}
	t.Run("slice", func(t *testing.T) {
		ClearRegistry()
		MustRegister[testTagged]()
		tx := newTx()
		mgr := MustNewManager[testTagged](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
		rows := []*testTagged{{Label: "A", Tags: []string{"first"}}, {Label: "B", Tags: []string{"second"}}}
		if err := mgr.PutMany(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
		if len(tx.rows) != 0 || len(tx.queries) != 2 || rows[1].GetIID() != "0x02" {
			t.Fatal("slice did not follow the per-instance path")
		}
	})
	t.Run("decimal", func(t *testing.T) {
		ClearRegistry()
		MustRegister[decimalProduct]()
		tx := newTx()
		mgr := MustNewManager[decimalProduct](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
		rows := []*decimalProduct{{SKU: "A", Price: 1, Exact: "1.0"}, {SKU: "B", Price: 2, Exact: "2.0"}}
		if err := mgr.PutMany(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
		if len(tx.rows) != 0 || len(tx.queries) != 2 || rows[1].GetIID() != "0x02" {
			t.Fatal("decimal did not follow the per-instance path")
		}
	})
	t.Run("relation", func(t *testing.T) {
		registerTestTypes(t)
		tx := newTx()
		mgr := MustNewManager[testEmployment](NewDatabase(&batchTestConn{tx: tx}, "test_db"))
		p := &testPerson{Name: "Alice"}
		p.SetIID("0x123")
		rows := []*testEmployment{{Employee: p}, {Employee: p}}
		if err := mgr.PutMany(context.Background(), rows); err != nil {
			t.Fatal(err)
		}
		if len(tx.rows) != 0 || len(tx.queries) != 2 || rows[0].GetIID() != "" {
			t.Fatal("keyless relation did not follow the per-instance path")
		}
	})
}
