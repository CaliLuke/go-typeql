//go:build cgo && typedb

package driver

// #include "typedb_ffi.h"
import "C"

// getError extracts error message from an FFI error out-parameter.
// If the error pointer is nil (no error occurred), returns nil.
// Otherwise returns a DriverError with the error message and frees the C string.
func getError(cErr *C.char) error {
	if cErr == nil {
		return nil
	}
	defer C.typedb_free_string(cErr)
	return &DriverError{Message: C.GoString(cErr)}
}
