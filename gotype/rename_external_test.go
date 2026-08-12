package gotype_test

import (
	"reflect"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func TestRenameOperation_HasNoExportedFields(t *testing.T) {
	typeOf := reflect.TypeFor[gotype.RenameOperation]()
	for i := range typeOf.NumField() {
		field := typeOf.Field(i)
		if field.IsExported() {
			t.Errorf("RenameOperation field %q is exported", field.Name)
		}
	}
}
