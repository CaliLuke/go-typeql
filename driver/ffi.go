//go:build cgo && typedb && !typedb_system

package driver

/*
#cgo LDFLAGS: -L${SRCDIR}/rust/target/release -ltypedb_go_ffi -ldl -lpthread -lm
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#include "typedb_ffi.h"
*/
import "C"
