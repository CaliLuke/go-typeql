// Package gotype provides a fluent query builder for TypeDB.
package gotype

import (
	"context"
	"fmt"
	"strings"
)

// Query provides a chainable, type-safe API for constructing and executing
// TypeDB queries for a specific model type T.
type Query[T any] struct {
	mgr     *Manager[T]
	filters []Filter
	orderBy []OrderClause
	limit   int
	offset  int
}

// OrderClause specifies an attribute name and sort direction for query results.
type OrderClause struct {
	Attr string
	Desc bool
}

// Filter adds one or more filtering conditions to the query.
// Multiple calls to Filter are combined using logical AND.
func (q *Query[T]) Filter(filters ...Filter) *Query[T] {
	q.filters = append(q.filters, filters...)
	return q
}

// OrderAsc adds an ascending sort order on the specified attribute.
func (q *Query[T]) OrderAsc(attr string) *Query[T] {
	q.orderBy = append(q.orderBy, OrderClause{Attr: attr, Desc: false})
	return q
}

// OrderDesc adds a descending sort order on the specified attribute.
func (q *Query[T]) OrderDesc(attr string) *Query[T] {
	q.orderBy = append(q.orderBy, OrderClause{Attr: attr, Desc: true})
	return q
}

// Limit restricts the number of results returned by the query.
func (q *Query[T]) Limit(n int) *Query[T] {
	q.limit = n
	return q
}

// Offset skips the first n results returned by the query.
func (q *Query[T]) Offset(n int) *Query[T] {
	q.offset = n
	return q
}

// Exists returns true if the query matches at least one instance in the database.
func (q *Query[T]) Exists(ctx context.Context) (bool, error) {
	count, err := q.Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// All executes the query and returns all matching instances as a slice of pointers to T.
func (q *Query[T]) All(ctx context.Context) ([]*T, error) {
	return q.Execute(ctx)
}

// Execute performs the query against the database and hydrates the results into Go structs.
func (q *Query[T]) Execute(ctx context.Context) ([]*T, error) {
	query := q.buildQuery()
	results, err := q.mgr.db.ExecuteRead(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.mgr.info.TypeName, err)
	}
	return q.mgr.hydrateResults(results)
}

// First executes the query with a limit of 1 and returns the first result, or nil if none found.
func (q *Query[T]) First(ctx context.Context) (*T, error) {
	q.limit = 1
	results, err := q.Execute(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Count returns the number of instances matching the query filters.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	query := q.buildCountQuery()
	results, err := q.mgr.db.ExecuteRead(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", q.mgr.info.TypeName, err)
	}
	if len(results) == 0 {
		return 0, nil
	}
	return extractCount(results[0]), nil
}

// Delete removes all instances that match the query filters.
func (q *Query[T]) Delete(ctx context.Context) (int64, error) {
	query := q.buildDeleteQuery()
	_, err := q.mgr.db.ExecuteWrite(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("delete %s: %w", q.mgr.info.TypeName, err)
	}
	return -1, nil
}

// --- Query building ---

func (q *Query[T]) buildMatchClause() string {
	varName := "e"
	var patterns []string
	patterns = append(patterns, fmt.Sprintf("$%s isa %s;", varName, q.mgr.info.TypeName))

	for _, f := range q.filters {
		patterns = append(patterns, f.ToPatterns(varName)...)
	}

	return "match\n" + strings.Join(patterns, "\n")
}

func (q *Query[T]) buildQuery() string {
	match := q.buildMatchClause()
	fetch := q.mgr.strategy.BuildFetchAll(q.mgr.info, "e")

	var parts []string
	parts = append(parts, match)

	// Sort
	if len(q.orderBy) > 0 {
		var sorts []string
		for _, o := range q.orderBy {
			attrVar := sanitizeVar("e__" + o.Attr)
			// Ensure we have a has pattern for the sort attribute
			match += fmt.Sprintf("\n$e has %s $%s;", o.Attr, attrVar)
			dir := "asc"
			if o.Desc {
				dir = "desc"
			}
			sorts = append(sorts, fmt.Sprintf("$%s %s", attrVar, dir))
		}
		parts = []string{match} // rebuild with added has patterns
		parts = append(parts, "sort "+strings.Join(sorts, ", ")+";")
	}

	// Pagination
	if q.offset > 0 {
		parts = append(parts, fmt.Sprintf("offset %d;", q.offset))
	}
	if q.limit > 0 {
		parts = append(parts, fmt.Sprintf("limit %d;", q.limit))
	}

	parts = append(parts, fetch)
	return strings.Join(parts, "\n")
}

func (q *Query[T]) buildCountQuery() string {
	match := q.buildMatchClause()
	return match + "\nreduce $count = count($e);"
}

func (q *Query[T]) buildDeleteQuery() string {
	match := q.buildMatchClause()
	return match + "\ndelete $e;"
}

// UpdateWith fetches all matching instances, applies fn to each, then updates them all.
// The fetch and update are performed within a single write transaction for atomicity.
func (q *Query[T]) UpdateWith(ctx context.Context, fn func(*T)) ([]*T, error) {
	// Use a single write transaction for both fetch and update to prevent race conditions.
	tx, err := q.mgr.db.Transaction(WriteTransaction)
	if err != nil {
		return nil, fmt.Errorf("update_with %s: %w", q.mgr.info.TypeName, err)
	}
	defer tx.Close()

	// Phase 1: fetch matching instances within the write transaction
	query := q.buildQuery()
	rawResults, err := tx.Query(query)
	if err != nil {
		return nil, fmt.Errorf("update_with %s: fetch: %w", q.mgr.info.TypeName, err)
	}
	results, err := q.mgr.hydrateResults(rawResults)
	if err != nil {
		return nil, fmt.Errorf("update_with %s: hydrate: %w", q.mgr.info.TypeName, err)
	}
	if len(results) == 0 {
		return nil, nil
	}

	// Phase 2: apply function to all instances
	for _, inst := range results {
		fn(inst)
	}

	// Phase 3: persist all updates in the same transaction
	for i, inst := range results {
		if err := q.mgr.updateInstanceInTx(tx, inst); err != nil {
			return nil, fmt.Errorf("update_with %s[%d]: %w", q.mgr.info.TypeName, i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update_with %s: commit: %w", q.mgr.info.TypeName, err)
	}
	return results, nil
}

// Update performs a bulk attribute update on all matching instances.
// Keys in the updates map are TypeDB attribute names; values are the new values.
// Returns the number of instances updated, or -1 if the count is unknown.
func (q *Query[T]) Update(ctx context.Context, updates map[string]any) (int64, error) {
	if len(updates) == 0 {
		return 0, nil
	}

	// Build match clause from filters
	match := q.buildMatchClause()

	tx, err := q.mgr.db.Transaction(WriteTransaction)
	if err != nil {
		return 0, fmt.Errorf("bulk_update %s: %w", q.mgr.info.TypeName, err)
	}
	defer tx.Close()

	// Build a single match-delete-insert query for all attributes
	var tryMatches []string
	var tryDeletes []string
	var insHas []string
	i := 0
	for attr, val := range updates {
		tryMatches = append(tryMatches, fmt.Sprintf("try { $e has %s $old%d; };", attr, i))
		tryDeletes = append(tryDeletes, fmt.Sprintf("try { $old%d of $e; };", i))
		insHas = append(insHas, fmt.Sprintf("has %s %s", attr, FormatValue(val)))
		i++
	}

	query := match + "\n" + strings.Join(tryMatches, "\n") +
		"\ndelete\n" + strings.Join(tryDeletes, "\n") +
		fmt.Sprintf("\ninsert $e %s;", strings.Join(insHas, ", "))
	_, err = tx.Query(query)
	if err != nil {
		return 0, fmt.Errorf("bulk_update %s: %w", q.mgr.info.TypeName, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("bulk_update %s: commit: %w", q.mgr.info.TypeName, err)
	}
	return -1, nil
}

// --- Aggregate queries ---

// AggregateQuery runs a reduce query and returns a single numeric result.
type AggregateQuery[T any] struct {
	mgr     *Manager[T]
	filters []Filter
	attr    string
	fn      string // sum, mean, min, max, std, median
}

// Sum creates an aggregate query for the sum of an attribute.
func (q *Query[T]) Sum(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "sum"}
}

// Avg creates an aggregate query for the mean of an attribute.
func (q *Query[T]) Avg(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "mean"}
}

// Min creates an aggregate query for the minimum of an attribute.
func (q *Query[T]) Min(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "min"}
}

// Max creates an aggregate query for the maximum of an attribute.
func (q *Query[T]) Max(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "max"}
}

// Median creates an aggregate query for the median of an attribute.
func (q *Query[T]) Median(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "median"}
}

// Std creates an aggregate query for the standard deviation of an attribute.
func (q *Query[T]) Std(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "std"}
}

// Variance creates an aggregate query for the variance of an attribute.
func (q *Query[T]) Variance(attr string) *AggregateQuery[T] {
	return &AggregateQuery[T]{mgr: q.mgr, filters: q.filters, attr: attr, fn: "variance"}
}

// Execute runs the aggregate query and returns the result as float64.
func (aq *AggregateQuery[T]) Execute(ctx context.Context) (float64, error) {
	varName := "e"
	var patterns []string
	patterns = append(patterns, fmt.Sprintf("$%s isa %s;", varName, aq.mgr.info.TypeName))
	for _, f := range aq.filters {
		patterns = append(patterns, f.ToPatterns(varName)...)
	}

	attrVar := sanitizeVar(varName + "__" + aq.attr)
	patterns = append(patterns, fmt.Sprintf("$%s has %s $%s;", varName, aq.attr, attrVar))

	match := "match\n" + strings.Join(patterns, "\n")
	query := match + fmt.Sprintf("\nreduce $result = %s($%s);", aq.fn, attrVar)

	results, err := aq.mgr.db.ExecuteRead(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("%s %s.%s: %w", aq.fn, aq.mgr.info.TypeName, aq.attr, err)
	}
	if len(results) == 0 {
		return 0, nil
	}
	return extractFloat(results[0], "result"), nil
}

// --- Multi-aggregate ---

// AggregateSpec describes a single aggregation to compute.
type AggregateSpec struct {
	Attr string
	Fn   string // sum, mean, min, max, std, median, variance, count
}

// Aggregate runs multiple aggregations in one call and returns named results.
// Each spec produces a result keyed by "fn_attr" (e.g., "sum_age", "mean_score").
// All aggregations are computed in a single query using multiple reduce assignments.
func (q *Query[T]) Aggregate(ctx context.Context, specs ...AggregateSpec) (map[string]float64, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	// Build match patterns
	varName := "e"
	var patterns []string
	patterns = append(patterns, fmt.Sprintf("$%s isa %s;", varName, q.mgr.info.TypeName))
	for _, f := range q.filters {
		patterns = append(patterns, f.ToPatterns(varName)...)
	}

	// Build reduce assignments - one per spec
	var assignments []string
	resultKeys := make([]string, len(specs))
	for i, spec := range specs {
		attrVar := sanitizeVar(varName + "__" + spec.Attr)
		resultVar := fmt.Sprintf("result%d", i)
		resultKeys[i] = spec.Fn + "_" + spec.Attr

		patterns = append(patterns, fmt.Sprintf("$%s has %s $%s;", varName, spec.Attr, attrVar))

		// Map fn to TypeDB aggregation function (TypeDB uses "mean" not "avg")
		fn := spec.Fn
		if fn == "avg" {
			fn = "mean"
		}
		assignments = append(assignments, fmt.Sprintf("$%s = %s($%s)", resultVar, fn, attrVar))
	}

	// Build complete query: match ... reduce ...
	matchClause := "match\n" + strings.Join(patterns, "\n")
	reduceClause := "reduce " + strings.Join(assignments, ", ") + ";"
	query := matchClause + "\n" + reduceClause

	// Execute query
	rawResults, err := q.mgr.readQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(rawResults) == 0 {
		return nil, nil
	}

	// Parse results - reduce returns single row with multiple values
	flat := unwrapResult(rawResults[0])
	results := make(map[string]float64, len(specs))
	for i, key := range resultKeys {
		resultVar := fmt.Sprintf("result%d", i)
		if val, ok := flat[resultVar]; ok {
			results[key] = toFloat64(val)
		}
	}

	return results, nil
}

// --- GroupBy ---

// GroupByQuery groups results by an attribute and supports aggregate operations.
type GroupByQuery[T any] struct {
	mgr     *Manager[T]
	filters []Filter
	groupBy string
}

// GroupBy creates a grouped query for computing per-group aggregates.
func (q *Query[T]) GroupBy(attr string) *GroupByQuery[T] {
	return &GroupByQuery[T]{mgr: q.mgr, filters: q.filters, groupBy: attr}
}

// Aggregate runs aggregations per group and returns results keyed by group value.
// Returns map[groupValue]map[aggKey]float64, where aggKey is "fn_attr".
func (gq *GroupByQuery[T]) Aggregate(ctx context.Context, specs ...AggregateSpec) (map[string]map[string]float64, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	varName := "e"
	var patterns []string
	patterns = append(patterns, fmt.Sprintf("$%s isa %s;", varName, gq.mgr.info.TypeName))
	for _, f := range gq.filters {
		patterns = append(patterns, f.ToPatterns(varName)...)
	}

	// Add has clause for the group-by attribute
	groupVar := sanitizeVar(varName + "__" + gq.groupBy)
	patterns = append(patterns, fmt.Sprintf("$%s has %s $%s;", varName, gq.groupBy, groupVar))

	// Add has clauses for each aggregate attribute (if not already the group-by attr)
	attrVars := make(map[string]string)
	for _, spec := range specs {
		if spec.Attr == gq.groupBy {
			attrVars[spec.Attr] = groupVar
			continue
		}
		if _, exists := attrVars[spec.Attr]; !exists {
			av := sanitizeVar(varName + "__" + spec.Attr)
			patterns = append(patterns, fmt.Sprintf("$%s has %s $%s;", varName, spec.Attr, av))
			attrVars[spec.Attr] = av
		}
	}

	match := "match\n" + strings.Join(patterns, "\n")

	// Build reduce clauses
	var reduces []string
	for _, spec := range specs {
		av := attrVars[spec.Attr]
		key := spec.Fn + "_" + spec.Attr
		reduces = append(reduces, fmt.Sprintf("$%s = %s($%s)", sanitizeVar(key), spec.Fn, av))
	}

	query := match + fmt.Sprintf("\nreduce %s, group $%s;", strings.Join(reduces, ", "), groupVar)

	rawResults, err := gq.mgr.db.ExecuteRead(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("groupby %s: %w", gq.mgr.info.TypeName, err)
	}

	// Parse results: each row has the group value and aggregate results
	results := make(map[string]map[string]float64)
	for _, row := range rawResults {
		groupVal := fmt.Sprintf("%v", unwrapValue(row[gq.groupBy]))
		aggs := make(map[string]float64)
		for _, spec := range specs {
			key := spec.Fn + "_" + spec.Attr
			aggs[key] = toFloat64(unwrapValue(row[sanitizeVar(key)]))
		}
		results[groupVal] = aggs
	}
	return results, nil
}

// --- Manager integration ---

// Query returns a new chainable query builder for this model.
func (m *Manager[T]) Query() *Query[T] {
	return &Query[T]{mgr: m}
}

// --- Helpers ---

func extractCount(result map[string]any) int64 {
	v := unwrapValue(result["count"])
	return toInt64(v)
}

func extractFloat(result map[string]any, key string) float64 {
	v := unwrapValue(result[key])
	return toFloat64(v)
}

// toInt64 converts a value to int64, handling TypeDB 3.x "Value(integer: N)" strings.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case uint64:
		return int64(n)
	case int:
		return int64(n)
	case string:
		return int64(parseValueString(n))
	}
	return 0
}

// toFloat64 converts a value to float64, handling TypeDB 3.x "Value(type: N)" strings.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		return parseValueString(n)
	}
	return 0
}

// --- FunctionQuery ---

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

// parseValueString parses TypeDB 3.x result strings like "Value(integer: 55)" or "Value(double: 3.14)".
func parseValueString(s string) float64 {
	var val float64
	// Try "Value(integer: N)" format
	if _, err := fmt.Sscanf(s, "Value(integer: %f)", &val); err == nil {
		return val
	}
	// Try "Value(double: N)" format
	if _, err := fmt.Sscanf(s, "Value(double: %f)", &val); err == nil {
		return val
	}
	// Try "Value(long: N)" format (legacy)
	if _, err := fmt.Sscanf(s, "Value(long: %f)", &val); err == nil {
		return val
	}
	return 0
}
