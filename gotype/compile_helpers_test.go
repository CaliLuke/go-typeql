package gotype

import (
	"strings"
	"testing"
)

// compilePatterns compiles f with the query root $e through a fresh
// allocator and returns the top-level patterns, one per line.
func compilePatterns(f Filter) []string {
	return compilePatternsFor("e", f)
}

// compilePatternsFor compiles f with the owner $root. It panics on a compile
// error, like the old ToPatterns did on invalid input; tests of error paths
// use the query builders instead.
func compilePatternsFor(root string, f Filter) []string {
	alloc := newVarAlloc()
	alloc.reserve(root)
	ps, err := f.compile(&compileCtx{alloc: alloc, owner: root})
	if err != nil {
		panic(err)
	}
	var b strings.Builder
	if err := renderPatterns(&b, ps); err != nil {
		panic(err)
	}
	s := strings.TrimPrefix(b.String(), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestCompilePatternsHelper(t *testing.T) {
	got := compilePatterns(Eq("name", "Alice"))
	want := []string{"$e has name $e__name;", `$e__name == "Alice";`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("compilePatterns = %q, want %q", got, want)
	}
}
