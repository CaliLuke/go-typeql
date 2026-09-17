//go:build cgo && typedb

package driver_test

import (
	"github.com/CaliLuke/go-typeql/v2/driver"
	"github.com/CaliLuke/go-typeql/v2/gotype"
)

var _ gotype.Tx = (*driver.Transaction)(nil)
