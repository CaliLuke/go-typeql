package gotype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type eachConn struct {
	*mockConn
	tx Tx
}

func (c *eachConn) Transaction(string, int) (Tx, error) { return c.tx, nil }

type lateEachTx struct {
	*mockTx
	rows []map[string]any
	err  error
}

func (tx *lateEachTx) QueryEachWithContext(ctx context.Context, query string, fn func(int, map[string]any) error) error {
	tx.queries = append(tx.queries, query)
	for _, row := range tx.rows {
		if err := fn(1, row); err != nil {
			return err
		}
	}
	return tx.err
}

func TestForEachStreamsOwnedModelsAndStops(t *testing.T) {
	registerTestTypes(t)
	tx := &lateEachTx{mockTx: &mockTx{}, rows: []map[string]any{
		{"_iid": "0x01", "name": "Alice", "age": float64(31)},
		{"_iid": "0x02", "name": "Bob"},
		{"_iid": "0x03", "name": "Carol"},
	}}
	mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
	var retained []*testPerson
	err := mgr.ForEach(context.Background(), nil, func(person *testPerson) error {
		retained = append(retained, person)
		if len(retained) == 2 {
			return fmt.Errorf("done: %w", ErrStopIteration)
		}
		return nil
	})
	if err != nil || len(retained) != 2 || !tx.closed {
		t.Fatalf("stop: err=%v retained=%d closed=%v", err, len(retained), tx.closed)
	}
	if retained[0] == retained[1] || retained[0].Name != "Alice" || retained[1].Name != "Bob" || retained[0].Age == nil || retained[1].Age != nil {
		t.Fatalf("models were not independent, or optional field was lost: %+v", retained)
	}
	if retained[0].GetIID() != "0x01" || retained[1].GetIID() != "0x02" {
		t.Fatalf("IIDs were lost: %+v", retained)
	}
}

func TestQueryForEachKeepsBoundTransactionAndHydratesRoles(t *testing.T) {
	registerTestTypes(t)
	tx := &lateEachTx{mockTx: &mockTx{}, rows: []map[string]any{{
		"_iid": "0x10", "employee": map[string]any{"_iid": "0x01", "name": "Alice"},
		"employer": map[string]any{"_iid": "0x02", "name": "Acme"},
	}}}
	db := NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db")
	scope, err := db.Begin(ReadTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	mgr := MustNewManagerWithTx[testEmployment](scope)
	var retained *testEmployment
	err = mgr.Query().Filter(Eq("name", "Alice")).OrderAsc("start-date").Offset(2).Limit(3).ForEach(context.Background(), func(job *testEmployment) error {
		retained = job
		return nil
	})
	if err != nil || retained == nil || retained.Employee == nil || retained.Employer == nil || retained.Employee.Name != "Alice" || retained.Employer.Name != "Acme" {
		t.Fatalf("nested roles: result=%+v err=%v", retained, err)
	}
	if tx.closed || !scope.Tx().IsOpen() {
		t.Fatal("ForEach closed caller-owned transaction")
	}
	for _, part := range []string{"sort $e__start_date asc;", "offset 2;", "limit 3;"} {
		if !strings.Contains(tx.queries[0], part) {
			t.Fatalf("missing fluent stage %q: %s", part, tx.queries[0])
		}
	}
}

func TestForEachFallbackAndErrors(t *testing.T) {
	registerTestTypes(t)
	t.Run("fallback stops after first hydrated row", func(t *testing.T) {
		tx := &mockTx{responses: [][]map[string]any{{
			{"_iid": "0x01", "name": "Alice"}, {"_iid": "0x02", "name": "Bob"},
		}}}
		mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
		seen := 0
		err := mgr.ForEach(context.Background(), nil, func(*testPerson) error {
			seen++
			return ErrStopIteration
		})
		if err != nil || seen != 1 || !tx.closed {
			t.Fatalf("fallback: err=%v seen=%d closed=%v", err, seen, tx.closed)
		}
	})
	t.Run("callback error", func(t *testing.T) {
		want := errors.New("consumer failed")
		tx := &lateEachTx{mockTx: &mockTx{}, rows: []map[string]any{{"name": "Alice"}}}
		mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
		err := mgr.ForEach(context.Background(), nil, func(*testPerson) error { return want })
		if !errors.Is(err, want) || !tx.closed {
			t.Fatalf("callback error=%v closed=%v", err, tx.closed)
		}
	})
	t.Run("later chunk error", func(t *testing.T) {
		want := errors.New("later chunk failed")
		tx := &lateEachTx{mockTx: &mockTx{}, rows: []map[string]any{{"name": "Alice"}, {"name": "Bob"}}, err: want}
		mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
		seen := 0
		err := mgr.ForEach(context.Background(), nil, func(*testPerson) error { seen++; return nil })
		if !errors.Is(err, want) || seen != 2 || !tx.closed {
			t.Fatalf("later error=%v seen=%d closed=%v", err, seen, tx.closed)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		tx := &lateEachTx{mockTx: &mockTx{}, rows: []map[string]any{{"name": "Alice"}, {"name": "Bob"}}}
		mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
		seen := 0
		err := mgr.ForEach(ctx, nil, func(*testPerson) error { seen++; cancel(); return nil })
		if !errors.Is(err, context.Canceled) || seen != 1 || !tx.closed {
			t.Fatalf("cancellation=%v seen=%d closed=%v", err, seen, tx.closed)
		}
	})
	t.Run("cancellation in final callback", func(t *testing.T) {
		for _, streamed := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			base := &mockTx{responses: [][]map[string]any{{{"name": "Alice"}}}}
			var tx Tx = base
			if streamed {
				tx = &lateEachTx{mockTx: base, rows: []map[string]any{{"name": "Alice"}}}
			}
			mgr := MustNewManager[testPerson](NewDatabase(&eachConn{mockConn: &mockConn{}, tx: tx}, "test_db"))
			seen := 0
			err := mgr.ForEach(ctx, nil, func(*testPerson) error { seen++; cancel(); return nil })
			if !errors.Is(err, context.Canceled) || seen != 1 || !base.closed {
				t.Fatalf("streamed=%v cancellation=%v seen=%d closed=%v", streamed, err, seen, base.closed)
			}
		}
	})
}
