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
	// Query is the TypeQL statement associated with the error, when available.
	Query string
}

func (e *DriverError) Error() string {
	if e.Query != "" {
		return e.Message + "\nquery:\n" + e.Query
	}
	return e.Message
}

func withQuery(err error, query string) error {
	if err == nil || query == "" {
		return err
	}

	var driverErr *DriverError
	if errors.As(err, &driverErr) {
		if driverErr.Query != "" {
			return err
		}
		clone := *driverErr
		clone.Query = query
		return &clone
	}

	return &DriverError{
		Message: err.Error(),
		Query:   query,
	}
}

var (
	// ErrNotConnected is returned when an operation is attempted on a closed or uninitialized driver.
	ErrNotConnected = errors.New("driver: not connected")
	// ErrNilPointer is returned when an FFI call unexpectedly returns a nil pointer.
	ErrNilPointer = errors.New("driver: nil pointer")
	// ErrTransactionAbandoned is returned when an operation is attempted on a
	// transaction that was abandoned by a cancelled QueryWithContext call. The
	// in-flight query goroutine frees the underlying native handle once the
	// driver call returns; the transaction cannot be reused.
	ErrTransactionAbandoned = errors.New("driver: transaction abandoned after context cancellation")
)
