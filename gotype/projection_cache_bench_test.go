package gotype

import "testing"

// BenchmarkProjectionConstruction compares repeated cached projection lookup
// with fresh AST compilation. Run with -benchtime=2000x -count=5.
func BenchmarkProjectionConstruction(b *testing.B) {
	ClearRegistry()
	MustRegister[testPerson]()
	info, _ := LookupType(typeOf[testPerson]())
	for _, tc := range []struct {
		name  string
		build func() (string, error)
	}{
		{"fresh", func() (string, error) { return compileFetchAll(info, "e") }},
		{"cached", func() (string, error) { return buildFetchAll(info, "e") }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if _, err := tc.build(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for range b.N {
				if _, err := tc.build(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
