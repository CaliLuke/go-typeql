//go:build cgo && typedb && typedb_prebuilt

package driver

/*
#cgo LDFLAGS: -ltypedb_go_ffi -ldl -lpthread -lm
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#cgo LDFLAGS: -L/usr/local/lib
#include "typedb_ffi.h"
*/
import "C"
