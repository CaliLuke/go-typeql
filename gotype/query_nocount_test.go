package gotype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type cancelMutationTx struct {
	*mockTx
	cancel context.CancelFunc
}

func (tx *cancelMutationTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	rows, err := tx.mockTx.QueryWithContext(ctx, query)
	tx.cancel()
	return rows, err
}

func TestQueryNoCountUsesSameMutationWithoutCountQuery(t *testing.T) {
	registerTestTypes(t)
	t.Run("update", func(t *testing.T) {
		counted := &mockTx{responses: [][]map[string]any{{{"count": float64(2)}}, nil}}
		countedMgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{counted}}, "test_db"))
		updates := map[string]any{"email": "new@example.test", "age": 40}
		if _, err := countedMgr.Query().Filter(Eq("name", "Alice")).Update(context.Background(), updates); err != nil {
			t.Fatal(err)
		}
		tx := &mockTx{}
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
		if err := mgr.Query().Filter(Eq("name", "Alice")).UpdateNoCount(context.Background(), updates); err != nil {
			t.Fatal(err)
		}
		if len(tx.queries) != 1 || tx.queries[0] != counted.queries[1] || strings.Contains(tx.queries[0], "reduce $count") || !tx.committed || !tx.closed {
			t.Fatalf("count-free update diverged: queries=%v committed=%v closed=%v", tx.queries, tx.committed, tx.closed)
		}
	})
	t.Run("delete", func(t *testing.T) {
		counted := &mockTx{responses: [][]map[string]any{{{"count": float64(2)}}, nil}}
		countedMgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{counted}}, "test_db"))
		if _, err := countedMgr.Query().Filter(Contains("email", "example")).Delete(context.Background()); err != nil {
			t.Fatal(err)
		}
		tx := &mockTx{}
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
		if err := mgr.Query().Filter(Contains("email", "example")).DeleteNoCount(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(tx.queries) != 1 || tx.queries[0] != counted.queries[1] || !strings.Contains(tx.queries[0], "distinct;") || !tx.committed || !tx.closed {
			t.Fatalf("count-free delete diverged: queries=%v committed=%v closed=%v", tx.queries, tx.committed, tx.closed)
		}
	})
}

func TestQueryNoCountTransactionAndErrorSemantics(t *testing.T) {
	registerTestTypes(t)
	t.Run("empty update avoids transaction", func(t *testing.T) {
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{}, "test_db"))
		if err := mgr.Query().UpdateNoCount(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("bound transaction remains caller owned", func(t *testing.T) {
		tx := &mockTx{}
		db := NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db")
		scope, err := db.Begin(WriteTransaction)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Close()
		mgr := MustNewManagerWithTx[testPerson](scope)
		if err := mgr.Query().DeleteNoCount(context.Background()); err != nil {
			t.Fatal(err)
		}
		if tx.closed || tx.committed || len(tx.queries) != 1 {
			t.Fatalf("bound tx ownership changed: %+v", tx)
		}
	})
	t.Run("invalid filter and attribute", func(t *testing.T) {
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{}, "test_db"))
		if err := mgr.Query().Filter(Eq("bad;name", "x")).DeleteNoCount(context.Background()); err == nil {
			t.Fatal("invalid filter was accepted")
		}
		if err := mgr.Query().UpdateNoCount(context.Background(), map[string]any{"bad;name": "x"}); err == nil {
			t.Fatal("invalid update attribute was accepted")
		}
	})
	t.Run("write error does not commit", func(t *testing.T) {
		tx := &mockTx{queryErrAt: 1}
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
		if err := mgr.Query().DeleteNoCount(context.Background()); err == nil || tx.committed || !tx.closed || len(tx.queries) != 1 {
			t.Fatalf("write failure: err=%v tx=%+v", err, tx)
		}
	})
	t.Run("cancelled before write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{}, "test_db"))
		if err := mgr.Query().DeleteNoCount(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled write error=%v", err)
		}
	})
	t.Run("cancelled after query does not commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := &mockTx{}
		tx := &cancelMutationTx{mockTx: base, cancel: cancel}
		mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
		if err := mgr.Query().UpdateNoCount(ctx, map[string]any{"email": "new@example.test"}); !errors.Is(err, context.Canceled) || base.committed || !base.closed {
			t.Fatalf("cancelled query: err=%v tx=%+v", err, base)
		}
	})
}
