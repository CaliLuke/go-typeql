package gotype

import (
	"context"
	"fmt"
	"strings"
)

// FunctionQuery builds and executes a TypeDB schema function call.
// TypeDB functions are defined with `fun` in the schema and called via
// match/return patterns.
type FunctionQuery struct {
	db       *Database
	funcName string
	args     []string // TypeQL argument expressions (e.g., "\"Alice\"", "42")
}

// NewFunctionQuery creates a query for a TypeDB schema function.
// funcName is the function name as defined in the schema.
func NewFunctionQuery(db *Database, funcName string) *FunctionQuery {
	return &FunctionQuery{db: db, funcName: funcName}
}

// Arg adds an argument to the function call.
// The value is formatted using FormatValue.
func (fq *FunctionQuery) Arg(value any) *FunctionQuery {
	fq.args = append(fq.args, FormatValue(value))
	return fq
}

// ArgRaw adds a pre-formatted argument string (e.g., a variable reference).
func (fq *FunctionQuery) ArgRaw(expr string) *FunctionQuery {
	fq.args = append(fq.args, expr)
	return fq
}

// Build returns the TypeQL query string for calling the function.
func (fq *FunctionQuery) Build() string {
	return fmt.Sprintf("let $result = %s(%s);\nreturn $result;",
		fq.funcName, strings.Join(fq.args, ", "))
}

// Execute runs the function query and returns the raw results.
func (fq *FunctionQuery) Execute(ctx context.Context) ([]map[string]any, error) {
	query := fq.Build()
	return fq.db.ExecuteRead(ctx, query)
}
