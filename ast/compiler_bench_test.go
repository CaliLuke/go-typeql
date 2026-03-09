package ast

import (
	"testing"
	"time"
)

func BenchmarkCompiler_CompileBatch(b *testing.B) {
	compiler := &Compiler{}
	nodes := []QueryNode{
		Match(
			Entity("$person", "person",
				Has("name", Str("Alice Example")),
				Has("email", Str("alice@example.com")),
				Has("age", Long(42)),
			),
			Relation("$employment", "employment", []RolePlayer{
				Role("employee", "$person"),
				Role("employer", "$company"),
			}),
			Entity("$company", "company",
				Has("name", Str("Acme Corp")),
				Has("founded", Lit(time.Date(2024, time.March, 15, 12, 30, 0, 0, time.FixedZone("PDT", -7*60*60)), "datetime-tz")),
			),
		),
		Fetch(
			FetchFunc("_iid", "iid", "$person"),
			FetchAttr("name", "$person", "name"),
			FetchAttr("email", "$person", "email"),
			FetchAttr("company", "$company", "name"),
		),
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := compiler.CompileBatch(nodes, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompiler_FormatGoValue(b *testing.B) {
	values := []any{
		"Alice Example",
		true,
		int64(42),
		3.1415926535,
		time.Date(2024, time.March, 15, 12, 30, 0, 0, time.UTC),
		time.Date(2024, time.March, 15, 12, 30, 0, 0, time.FixedZone("PDT", -7*60*60)),
	}

	b.ReportAllocs()
	for b.Loop() {
		for _, value := range values {
			_ = FormatGoValue(value)
		}
	}
}
