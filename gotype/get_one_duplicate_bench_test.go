package gotype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type duplicateAnswerConn struct {
	Conn
	rows []map[string]any
}

func (c *duplicateAnswerConn) Transaction(_ string, _ int) (Tx, error) {
	return &duplicateAnswerTx{rows: c.rows}, nil
}

type duplicateAnswerTx struct {
	Tx
	rows []map[string]any
}

func (t *duplicateAnswerTx) QueryWithContext(_ context.Context, query string) ([]map[string]any, error) {
	if strings.Contains(query, "reduce $count") {
		return []map[string]any{{"count": int64(1)}}, nil
	}
	if strings.Contains(query, "limit 2;") {
		return t.rows[:min(2, len(t.rows))], nil
	}
	return t.rows, nil
}

func (t *duplicateAnswerTx) Close() {}

// benchmarkBoundedGetOne is an experimental different-contract API: duplicate
// errors report at most two answer rows, never the exact count above two.
func benchmarkBoundedGetOne[T any](ctx context.Context, mgr *Manager[T], filter Filter) (*T, error) {
	rows, err := mgr.Query().Filter(filter).Limit(2).Execute(ctx)
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, &NotFoundError{TypeName: mgr.info.TypeName}
	case 1:
		return rows[0], nil
	default:
		return nil, &NotUniqueError{TypeName: mgr.info.TypeName, Count: 2}
	}
}

func TestBenchmarkBoundedGetOneContract(t *testing.T) {
	registerTestTypes(t)
	row := map[string]any{"_iid": "0x1", "name": "Alice", "email": "a@example.test"}
	for _, tc := range []struct {
		name string
		rows []map[string]any
	}{
		{"zero", nil},
		{"one", []map[string]any{row}},
		{"repeated answer rows", []map[string]any{row, row, row}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := MustNewManager[testPerson](NewDatabase(&duplicateAnswerConn{rows: tc.rows}, "mock"))
			model, err := benchmarkBoundedGetOne(context.Background(), mgr, Eq("name", "Alice"))
			switch len(tc.rows) {
			case 0:
				var missing *NotFoundError
				if model != nil || !errors.As(err, &missing) {
					t.Fatalf("zero: model=%+v err=%v", model, err)
				}
			case 1:
				if err != nil || model == nil || model.Name != "Alice" || model.Email != "a@example.test" {
					t.Fatalf("one: model=%+v err=%v", model, err)
				}
			default:
				var duplicate *NotUniqueError
				if model != nil || !errors.As(err, &duplicate) || duplicate.Count != 2 {
					t.Fatalf("repeated rows: model=%+v err=%v", model, err)
				}
			}
		})
	}
}

// BenchmarkGetOneRepeatedRows isolates ORM hydration when 25 answer rows all
// refer to one IID. It is synthetic, not a server/transfer measurement.
func BenchmarkGetOneRepeatedRows(b *testing.B) {
	ClearRegistry()
	if err := Register[testPerson](); err != nil {
		b.Fatal(err)
	}
	row := map[string]any{"_iid": "0x1", "name": "Alice", "email": "a@example.test"}
	rows := make([]map[string]any, 25)
	for i := range rows {
		rows[i] = row
	}
	mgr := MustNewManager[testPerson](NewDatabase(&duplicateAnswerConn{rows: rows}, "mock"))
	ctx := context.Background()
	for _, method := range []string{"current", "count-first", "bounded-two"} {
		b.Run(method, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				switch method {
				case "current":
					_, err := mgr.GetOne(ctx, map[string]any{"name": "Alice"})
					var nonUnique *NotUniqueError
					if !errors.As(err, &nonUnique) || nonUnique.Count != 25 {
						b.Fatalf("answer-row count: %v", err)
					}
				case "count-first":
					count, err := mgr.Query().Filter(Eq("name", "Alice")).Count(ctx)
					if err != nil || count != 1 {
						b.Fatalf("distinct count: %d, %v", count, err)
					}
					_, err = mgr.GetOne(ctx, map[string]any{"name": "Alice"})
					var nonUnique *NotUniqueError
					if !errors.As(err, &nonUnique) || nonUnique.Count != 25 {
						b.Fatalf("count-first cannot preserve answer-row uniqueness: %v", err)
					}
				case "bounded-two":
					_, err := benchmarkBoundedGetOne(ctx, mgr, Eq("name", "Alice"))
					var nonUnique *NotUniqueError
					if !errors.As(err, &nonUnique) || nonUnique.Count != 2 {
						b.Fatalf("bounded duplicate error: %v", err)
					}
				}
			}
			b.ReportMetric(25, "answer-rows")
			b.ReportMetric(1, "distinct-iids")
			if method == "count-first" {
				b.ReportMetric(2, "queries/op")
			} else {
				b.ReportMetric(1, "queries/op")
			}
		})
	}
}
