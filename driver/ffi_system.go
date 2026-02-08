//go:build cgo && typedb && typedb_system

package driver

/*
#cgo pkg-config: typedb-go-ffi
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#include "typedb_ffi.h"
*/
import "C"
