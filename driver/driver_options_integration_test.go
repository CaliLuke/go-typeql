//go:build cgo && typedb && integration

package driver

import (
	"strings"
	"testing"
)

func TestServerVersion(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	version, err := conn.ServerVersion()
	if err != nil {
		t.Fatalf("server version: %v", err)
	}
	if version.Distribution == "" {
		t.Fatalf("expected distribution, got %#v", version)
	}
	if !strings.Contains(version.Version, "3.12.0-rc2") {
		t.Fatalf("expected 3.12.0-rc2 server, got %#v", version)
	}
}

func TestOpenWithOptions(t *testing.T) {
	conn, err := OpenWithOptions(testAddr(), "admin", "password", DriverOptions{
		RequestTimeoutMillis:   5_000,
		PrimaryFailoverRetries: 1,
	})
	if err != nil {
		t.Fatalf("connect with options: %v", err)
	}
	defer conn.Close()

	if !conn.IsOpen() {
		t.Fatal("expected connection to be open")
	}
}

func TestOpenWithAddresses(t *testing.T) {
	conn, err := OpenWithAddresses([]string{testAddr()}, "admin", "password", DriverOptions{})
	if err != nil {
		t.Fatalf("connect with addresses: %v", err)
	}
	defer conn.Close()

	if !conn.IsOpen() {
		t.Fatal("expected connection to be open")
	}
}

func TestOpenWithAddressTranslation(t *testing.T) {
	conn, err := OpenWithAddressTranslation(
		map[string]string{testAddr(): "127.0.0.1:1729"},
		"admin",
		"password",
		DriverOptions{RequestTimeoutMillis: 5_000},
	)
	if err != nil {
		t.Fatalf("connect with address translation: %v", err)
	}
	defer conn.Close()

	version, err := conn.ServerVersion()
	if err != nil {
		t.Fatalf("server version: %v", err)
	}
	if version.Version == "" {
		t.Fatalf("expected server version, got %#v", version)
	}
}
