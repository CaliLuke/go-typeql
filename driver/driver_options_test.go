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

func TestCloseWorkerCount(t *testing.T) {
	for _, tc := range []struct {
		requested, maxNative, want int
	}{
		{0, 16, defaultCloseWorkers},
		{-3, 16, defaultCloseWorkers},
		{4, 16, 4},
		{32, 16, 16}, // more workers than slots cannot run
		{0, 1, 1},    // a limit of one slot needs one worker
		{32, -1, 32}, // admission off: no cap
		{0, -1, defaultCloseWorkers},
	} {
		if got := closeWorkerCount(tc.requested, tc.maxNative); got != tc.want {
			t.Errorf("closeWorkerCount(%d, %d) = %d, want %d", tc.requested, tc.maxNative, got, tc.want)
		}
	}
}
