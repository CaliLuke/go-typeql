//go:build cgo && typedb

package driver_test

import (
	"github.com/CaliLuke/go-typeql/v3/driver"
	"github.com/CaliLuke/go-typeql/v3/gotype"
)

var _ gotype.Tx = (*driver.Transaction)(nil)
