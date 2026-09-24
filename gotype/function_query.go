package gotype

import (
	"context"
	"fmt"
	"strings"
)

// FunctionQuery builds and executes a call of one function that returns a
// single value: a schema function (defined with `fun`) or a fully qualified
// built-in function. The query is "match let $result = f(args); select
// $result;". Functions that return a stream or a tuple are not supported.
type FunctionQuery struct {
	db       *Database
	funcName string
	args     []string // TypeQL argument expressions (e.g., "\"Alice\"", "42")
}

// NewFunctionQuery creates a query for a TypeDB schema function.
// funcName is the function name as defined in the schema, or a fully
// qualified built-in name such as "std::math::abs".
func NewFunctionQuery(db *Database, funcName string) *FunctionQuery {
	return &FunctionQuery{db: db, funcName: funcName}
}

// Arg adds an argument to the function call.
// The value is formatted using FormatValue.
func (fq *FunctionQuery) Arg(value any) *FunctionQuery {
	fq.args = append(fq.args, FormatValue(value))
	return fq
}

// ArgRaw adds a pre-formatted TypeQL argument, for example an expression of
// constants such as "2 + 3". The query has no match patterns, so a variable
// reference is not bound and the server rejects the query.
func (fq *FunctionQuery) ArgRaw(expr string) *FunctionQuery {
	fq.args = append(fq.args, expr)
	return fq
}

// Build returns the TypeQL query string for calling the function. The
// query binds the function result to $result, so each result row has the
// key "result". funcName can be a schema function or a fully qualified
// built-in function (TypeQL 3.13.4), for example "std::math::log10".
func (fq *FunctionQuery) Build() string {
	return fmt.Sprintf("match\nlet $result = %s(%s);\nselect $result;",
		fq.funcName, strings.Join(fq.args, ", "))
}

// Execute runs the function query and returns the raw results.
func (fq *FunctionQuery) Execute(ctx context.Context) ([]map[string]any, error) {
	query := fq.Build()
	return fq.db.ExecuteRead(ctx, query)
}
