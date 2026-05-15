//go:build cgo && typedb

package driver

import (
	"strings"
	"testing"
)

func TestOpenWithAddressesRequiresAddress(t *testing.T) {
	conn, err := OpenWithAddresses(nil, "admin", "password", DriverOptions{})
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "at least one address") {
		t.Fatalf("expected address validation error, got %v", err)
	}
}
