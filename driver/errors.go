//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"
import (
	"errors"
)

// DriverError represents an error returned by the underlying TypeDB Rust driver.
type DriverError struct {
	// Message is the error message returned from the driver.
	Message string
}

func (e *DriverError) Error() string {
	return e.Message
}

var (
	// ErrNotConnected is returned when an operation is attempted on a closed or uninitialized driver.
	ErrNotConnected = errors.New("driver: not connected")
	// ErrNilPointer is returned when an FFI call unexpectedly returns a nil pointer.
	ErrNilPointer = errors.New("driver: nil pointer")
)
