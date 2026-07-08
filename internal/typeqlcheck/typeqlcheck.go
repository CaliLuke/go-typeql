// Package typeqlcheck is test support for validating generated TypeQL syntax
// with the official `typeql-check` CLI from typedb/typedb-tools.
//
// The tool is a soft dependency: when the binary is not installed, assertions
// degrade to a one-time warning so contributors are never blocked. Set
// TYPEQL_CHECK_REQUIRED=1 (check.sh does this automatically when the binary is
// present) to turn a missing binary into a test failure instead.
//
// Install with `make install-typeql-check`, or point TYPEQL_CHECK at a binary.
package typeqlcheck

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	lookupOnce sync.Once
	binPath    string
	warnOnce   sync.Once
)

// Path returns the resolved typeql-check binary path and whether it was found.
// Resolution order: $TYPEQL_CHECK, $PATH, then $HOME/go/bin (where
// `make install-typeql-check` places it, matching the repo's staticcheck
// convention).
func Path() (string, bool) {
	lookupOnce.Do(func() {
		if p := os.Getenv("TYPEQL_CHECK"); p != "" {
			if _, err := os.Stat(p); err == nil {
				binPath = p
				return
			}
		}
		if p, err := exec.LookPath("typeql-check"); err == nil {
			binPath = p
			return
		}
		if home, err := os.UserHomeDir(); err == nil {
			p := filepath.Join(home, "go", "bin", "typeql-check")
			if _, err := os.Stat(p); err == nil {
				binPath = p
			}
		}
	})
	return binPath, binPath != ""
}

// Available reports whether the typeql-check binary can be found.
func Available() bool {
	_, ok := Path()
	return ok
}

// Validate runs the query through typeql-check and returns an error describing
// the syntax problem if the query is not valid TypeQL. It returns an error
// only for invalid syntax or a failure to run the tool; callers must check
// Available() first.
func Validate(query string) error {
	bin, ok := Path()
	if !ok {
		return fmt.Errorf("typeql-check binary not found")
	}
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(query)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, isExit := err.(*exec.ExitError); isExit {
			return fmt.Errorf("invalid TypeQL:\n%s\nquery:\n%s", strings.TrimSpace(stderr.String()), query)
		}
		return fmt.Errorf("running typeql-check: %w", err)
	}
	return nil
}

// AssertValid fails the test if query is not syntactically valid TypeQL.
// When the binary is missing it warns once and returns, unless
// TYPEQL_CHECK_REQUIRED=1 is set, in which case it fails the test.
func AssertValid(t testing.TB, label, query string) {
	t.Helper()
	if !Available() {
		if os.Getenv("TYPEQL_CHECK_REQUIRED") == "1" {
			t.Fatalf("%s: typeql-check binary not found but TYPEQL_CHECK_REQUIRED=1 (run: make install-typeql-check)", label)
		}
		warnOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "typeqlcheck: typeql-check binary not found; TypeQL syntax assertions skipped (run: make install-typeql-check)")
		})
		return
	}
	if err := Validate(query); err != nil {
		t.Errorf("%s: %v", label, err)
	}
}
