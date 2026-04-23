//go:build cgo && typedb

package driver

import (
	"errors"
	"strings"
	"testing"
)

func TestWithQuery_AttachesQueryToDriverError(t *testing.T) {
	query := "match $x isa thing;"
	err := withQuery(&DriverError{Message: "compile failed"}, query)

	var driverErr *DriverError
	if !errors.As(err, &driverErr) {
		t.Fatalf("expected DriverError, got %T", err)
	}
	if driverErr.Query != query {
		t.Fatalf("expected query %q, got %q", query, driverErr.Query)
	}
	if !strings.Contains(err.Error(), "query:\n"+query) {
		t.Fatalf("expected formatted error to include query, got %q", err.Error())
	}
}

func TestWithQuery_PreservesExistingQuery(t *testing.T) {
	err := withQuery(&DriverError{
		Message: "compile failed",
		Query:   "match $x isa person;",
	}, "match $x isa thing;")

	var driverErr *DriverError
	if !errors.As(err, &driverErr) {
		t.Fatalf("expected DriverError, got %T", err)
	}
	if driverErr.Query != "match $x isa person;" {
		t.Fatalf("expected original query to be preserved, got %q", driverErr.Query)
	}
}
