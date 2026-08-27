//go:build cgo && typedb

package driver_test

import (
	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

var _ gotype.Tx = (*driver.Transaction)(nil)
