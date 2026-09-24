//go:build cgo && typedb && integration

package gotype

import (
	"context"
	"testing"
)

// BenchmarkLiveR5_OneBindingPerScope measures R5 of issue #138: several
// comparisons on one attribute bind it once. The "duplicated" case is the
// same query with one binding for each comparison, as the compiler emitted
// before issue #138. TypeDB evaluates each identical binding.
func BenchmarkLiveR5_OneBindingPerScope(b *testing.B) {
	f := liveBenchSetup(b)
	ctx := context.Background()
	mgr := mustLiveBenchManager[liveBenchPerson](f.db)
	filters := []Filter{Gt("age", 10), Lt("age", 200), Neq("age", 20)}
	q, key, err := mgr.Query().Filter(filters...).buildCountQuery()
	if err != nil {
		b.Fatal(err)
	}
	duplicated := "match\n$e isa live-bench-person;\n" +
		"$e has age $e__age;\n$e__age > 10;\n" +
		"$e has age $e__age;\n$e__age < 200;\n" +
		"$e has age $e__age;\n$e__age != 20;\n" +
		"select $e;\ndistinct;\nreduce $" + key + " = count($e);"
	for _, bc := range []struct{ name, query string }{{"compiled", q}, {"duplicated", duplicated}} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				rows, err := f.db.ExecuteRead(ctx, bc.query)
				if err != nil {
					b.Fatal(err)
				}
				if n, err := countFromKey(rows[0], key); err != nil || n == 0 {
					b.Fatalf("count = %d, %v", n, err)
				}
			}
		})
	}
}
