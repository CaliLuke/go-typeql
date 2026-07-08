//go:build cgo && typedb

package driver

import (
	"strings"
	"testing"
)

// These tests exercise FFI error paths that fail before any network dial, so
// they run without a TypeDB server.

func TestOpenRejectsInvalidUTF8Username(t *testing.T) {
	conn, err := Open("localhost:1729", "user\xff\xfe", "password")
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("expected an error for invalid UTF-8 username")
	}
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("expected invalid UTF-8 error, got %v", err)
	}
}

func TestOpenRejectsInvalidUTF8Address(t *testing.T) {
	conn, err := Open("local\xffhost:1729", "admin", "password")
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("expected an error for invalid UTF-8 address")
	}
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Fatalf("expected invalid UTF-8 error, got %v", err)
	}
}

func TestReleaseConceptHelpersAreSafeWithoutRegistrations(t *testing.T) {
	// Unknown and empty handles must be no-ops, not crashes.
	(Concept{}).Release()
	(Concept{Handle: "concept-does-not-exist"}).Release()
	ReleaseAllConcepts()
}

func TestResolveAddressTranslation(t *testing.T) {
	t.Run("explicit map wins", func(t *testing.T) {
		translation, err := resolveAddressTranslation("localhost:1730", DriverOptions{
			AddressTranslation: map[string]string{"localhost:1730": "127.0.0.1:1729"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if translation["localhost:1730"] != "127.0.0.1:1729" {
			t.Fatalf("unexpected translation: %#v", translation)
		}
	})

	t.Run("explicit map must contain dialed address", func(t *testing.T) {
		_, err := resolveAddressTranslation("localhost:1730", DriverOptions{
			AddressTranslation: map[string]string{"other:1730": "127.0.0.1:1729"},
		})
		if err == nil {
			t.Fatal("expected error for missing dialed address")
		}
		if !strings.Contains(err.Error(), "no entry for dialed address") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no implicit rewrite by default", func(t *testing.T) {
		t.Setenv("TYPEDB_GO_COMPOSE_PORT_MAP", "")
		translation, err := resolveAddressTranslation("localhost:1730", DriverOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if translation != nil {
			t.Fatalf("expected no translation, got %#v", translation)
		}
	})

	t.Run("compose env var opts into localhost mapping", func(t *testing.T) {
		t.Setenv("TYPEDB_GO_COMPOSE_PORT_MAP", "1")
		translation, err := resolveAddressTranslation("localhost:1730", DriverOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if translation["localhost:1730"] != "127.0.0.1:1729" {
			t.Fatalf("unexpected translation: %#v", translation)
		}
	})

	t.Run("compose env var leaves default port alone", func(t *testing.T) {
		t.Setenv("TYPEDB_GO_COMPOSE_PORT_MAP", "1")
		translation, err := resolveAddressTranslation("localhost:1729", DriverOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if translation != nil {
			t.Fatalf("expected no translation for default port, got %#v", translation)
		}
	})

	t.Run("compose env var leaves remote hosts alone", func(t *testing.T) {
		t.Setenv("TYPEDB_GO_COMPOSE_PORT_MAP", "1")
		translation, err := resolveAddressTranslation("typedb.example.com:1730", DriverOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if translation != nil {
			t.Fatalf("expected no translation for remote host, got %#v", translation)
		}
	})
}

func TestQueryOptionsSetConceptHandles(t *testing.T) {
	opts := NewQueryOptions()
	defer opts.Close()
	if opts.conceptHandles {
		t.Fatal("concept handles must default to off")
	}
	if opts.SetConceptHandles(true) != opts {
		t.Fatal("expected fluent return")
	}
	if !opts.conceptHandles {
		t.Fatal("expected concept handles enabled")
	}
	opts.SetConceptHandles(false)
	if opts.conceptHandles {
		t.Fatal("expected concept handles disabled again")
	}
}
