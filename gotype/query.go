// Package gotype provides a fluent query builder for TypeDB.
package gotype

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
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
// When the Manager is bound to a transaction, the query runs inside it.
func (q *Query[T]) Execute(ctx context.Context) ([]*T, error) {
	query, err := q.buildQuery()
	if err != nil {
		return nil, fmt.Errorf("query %s: build: %w", q.mgr.info.TypeName, err)
	}
	results, err := q.mgr.readQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", q.mgr.info.TypeName, err)
	}
	return q.mgr.hydrateResults(results)
}

// First executes the query with a limit of 1 and returns the first result, or nil if none found.
// The builder itself is not modified, so a later All on the same query
// returns the full result set.
func (q *Query[T]) First(ctx context.Context) (*T, error) {
	limited := *q
	limited.limit = 1
	results, err := limited.Execute(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Count returns the number of distinct instances matching the query filters.
// Instances matched multiple times (e.g. via several values of a filtered
// multi-valued attribute) are counted once.
func (q *Query[T]) Count(ctx context.Context) (int64, error) {
	query, err := q.buildCountQuery()
	if err != nil {
		return 0, fmt.Errorf("count %s: build: %w", q.mgr.info.TypeName, err)
	}
	results, err := q.mgr.readQuery(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", q.mgr.info.TypeName, err)
	}
	if len(results) == 0 {
		return 0, nil
	}
	count, err := countFromResult(results[0])
	if err != nil {
		return 0, fmt.Errorf("count %s: %w", q.mgr.info.TypeName, err)
	}
	return count, nil
}

// Delete removes all distinct instances that match the query filters and
// returns how many there were. When the Manager is bound to a transaction,
// the delete runs inside it and is committed by the transaction owner.
func (q *Query[T]) Delete(ctx context.Context) (int64, error) {
	countQuery, err := q.buildCountQuery()
	if err != nil {
		return 0, fmt.Errorf("delete %s: build count: %w", q.mgr.info.TypeName, err)
	}
	deleteQuery, err := q.buildDeleteQuery()
	if err != nil {
		return 0, fmt.Errorf("delete %s: build delete: %w", q.mgr.info.TypeName, err)
	}

	return q.countThenWrite(ctx, "delete", countQuery, deleteQuery)
}

// countThenWrite executes a distinct-count query followed by a write query
// inside a (possibly bound) write transaction and returns the count.
func (q *Query[T]) countThenWrite(ctx context.Context, op, countQuery, writeQuery string) (int64, error) {
	var count int64
	err := q.mgr.withWriteTx(ctx, op, q.mgr.writeTx, func(tx Tx) error {
		countResults, err := tx.QueryWithContext(ctx, countQuery)
		if err != nil {
			return fmt.Errorf("%s %s: count: %w", op, q.mgr.info.TypeName, err)
		}
		if len(countResults) > 0 {
			if count, err = countFromResult(countResults[0]); err != nil {
				return fmt.Errorf("%s %s: count: %w", op, q.mgr.info.TypeName, err)
			}
		}

		if _, err := tx.QueryWithContext(ctx, writeQuery); err != nil {
			return fmt.Errorf("%s %s: %w", op, q.mgr.info.TypeName, err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// --- Query building ---

func (q *Query[T]) buildMatchClause() (string, error) {
	// Surface filter construction errors (invalid attribute names, malformed
	// IIDs, non-scalar comparison values) as build errors instead of injected
	// query text or execution-time panics (issues #45, #50).
	if err := validateFilters(q.filters...); err != nil {
		return "", err
	}
	varName := "e"
	var b strings.Builder
	b.WriteString("match\n$")
	b.WriteString(varName)
	b.WriteString(" isa ")
	b.WriteString(q.mgr.info.TypeName)
	b.WriteString(";")

	for _, f := range q.filters {
		for _, pattern := range f.ToPatterns(varName) {
			b.WriteByte('\n')
			b.WriteString(pattern)
		}
	}

	return b.String(), nil
}

func (q *Query[T]) buildQuery() (string, error) {
	match, err := q.buildMatchClause()
	if err != nil {
		return "", err
	}
	fetch, err := q.mgr.strategy.BuildFetchAll(q.mgr.info, "e")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(match)

	// Sort
	if len(q.orderBy) > 0 {
		for _, o := range q.orderBy {
			// Order-by attribute names are interpolated raw (issue #45).
			if err := validateAttrName(o.Attr); err != nil {
				return "", err
			}
			attrVar := sanitizeVar("e__" + o.Attr)
			// Ensure we have a has pattern for the sort attribute
			b.WriteString("\n$e has ")
			b.WriteString(o.Attr)
			b.WriteString(" $")
			b.WriteString(attrVar)
			b.WriteString(";")
		}

		b.WriteString("\nsort ")
		for i, o := range q.orderBy {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('$')
			b.WriteString(sanitizeVar("e__" + o.Attr))
			if o.Desc {
				b.WriteString(" desc")
			} else {
				b.WriteString(" asc")
			}
		}
		b.WriteString(";")
	}

	// Pagination
	if q.offset > 0 {
		b.WriteString("\noffset ")
		b.WriteString(strconv.Itoa(q.offset))
		b.WriteString(";")
	}
	if q.limit > 0 {
		b.WriteString("\nlimit ")
		b.WriteString(strconv.Itoa(q.limit))
		b.WriteString(";")
	}

	b.WriteByte('\n')
	b.WriteString(fetch)
	return b.String(), nil
}

func (q *Query[T]) buildCountQuery() (string, error) {
	match, err := q.buildMatchClause()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(match)
	// Filters bind attribute variables, so an instance can appear in several
	// answer rows (one per matching attribute value). Deduplicate on the
	// instance variable before counting so the result is a distinct-entity count.
	b.WriteString("\nselect $e;")
	b.WriteString("\ndistinct;")
	b.WriteString("\nreduce $count = count($e);")
	return b.String(), nil
}

func (q *Query[T]) buildDeleteQuery() (string, error) {
	match, err := q.buildMatchClause()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(match)
	// Deduplicate answer rows (see buildCountQuery) so each matching
	// instance is deleted exactly once.
	b.WriteString("\nselect $e;")
	b.WriteString("\ndistinct;")
	b.WriteString("\ndelete $e;")
	return b.String(), nil
}

// UpdateWith fetches all matching instances, applies fn to each, then updates them all.
// The fetch and update are performed within a single write transaction for
// atomicity. When the Manager is bound to a transaction, that transaction is
// reused and committed by its owner.
func (q *Query[T]) UpdateWith(ctx context.Context, fn func(*T)) ([]*T, error) {
	query, err := q.buildQuery()
	if err != nil {
		return nil, fmt.Errorf("update_with %s: build: %w", q.mgr.info.TypeName, err)
	}

	var results []*T
	err = q.mgr.withWriteTx(ctx, "update_with", q.mgr.writeTx, func(tx Tx) error {
		// Phase 1: fetch matching instances within the write transaction
		rawResults, err := tx.QueryWithContext(ctx, query)
		if err != nil {
			return fmt.Errorf("update_with %s: fetch: %w", q.mgr.info.TypeName, err)
		}
		results, err = q.mgr.hydrateResults(rawResults)
		if err != nil {
			return fmt.Errorf("update_with %s: hydrate: %w", q.mgr.info.TypeName, err)
		}
		if len(results) == 0 {
			return nil
		}

		// Phase 2: apply function to all instances
		for _, inst := range results {
			fn(inst)
		}

		// Phase 3: persist all updates in the same transaction
		for i, inst := range results {
			if err := q.mgr.updateInstanceInTx(ctx, tx, inst); err != nil {
				return fmt.Errorf("update_with %s[%d]: %w", q.mgr.info.TypeName, i, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

// Update performs a bulk attribute update on all matching instances.
// Keys in the updates map are TypeDB attribute names; values are the new values.
// Returns the number of distinct instances updated. When the Manager is bound
// to a transaction, the update runs inside it and is committed by its owner.
func (q *Query[T]) Update(ctx context.Context, updates map[string]any) (int64, error) {
	if len(updates) == 0 {
		return 0, nil
	}

	// Build match clause from filters
	match, err := q.buildMatchClause()
	if err != nil {
		return 0, fmt.Errorf("bulk_update %s: build: %w", q.mgr.info.TypeName, err)
	}

	countQuery, err := q.buildCountQuery()
	if err != nil {
		return 0, fmt.Errorf("bulk_update %s: build count: %w", q.mgr.info.TypeName, err)
	}

	// Build a single match-delete-insert query for all attributes.
	// Attribute names are sorted so identical updates always produce
	// identical query text.
	var tryMatches []string
	var tryDeletes []string
	var insHas []string
	for i, attr := range slices.Sorted(maps.Keys(updates)) {
		// Update attribute names are interpolated raw (issue #45).
		if err := validateAttrName(attr); err != nil {
			return 0, fmt.Errorf("bulk_update %s: %w", q.mgr.info.TypeName, err)
		}
		tryMatches = append(tryMatches, fmt.Sprintf("try { $e has %s $old%d; };", attr, i))
		tryDeletes = append(tryDeletes, fmt.Sprintf("try { $old%d of $e; };", i))
		insHas = append(insHas, fmt.Sprintf("has %s %s", attr, FormatValue(updates[attr])))
	}
	query := match + "\n" + strings.Join(tryMatches, "\n") +
		"\ndelete\n" + strings.Join(tryDeletes, "\n") +
		fmt.Sprintf("\ninsert $e %s;", strings.Join(insHas, ", "))

	return q.countThenWrite(ctx, "bulk_update", countQuery, query)
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
	if err := validateFilters(aq.filters...); err != nil {
		return 0, fmt.Errorf("%s %s.%s: %w", aq.fn, aq.mgr.info.TypeName, aq.attr, err)
	}
	if err := validateAttrName(aq.attr); err != nil {
		return 0, fmt.Errorf("%s %s: %w", aq.fn, aq.mgr.info.TypeName, err)
	}
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

	results, err := aq.mgr.readQuery(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("%s %s.%s: %w", aq.fn, aq.mgr.info.TypeName, aq.attr, err)
	}
	if len(results) == 0 {
		return 0, nil
	}
	val, err := floatFromResult(results[0], "result")
	if err != nil {
		return 0, fmt.Errorf("%s %s.%s: %w", aq.fn, aq.mgr.info.TypeName, aq.attr, err)
	}
	return val, nil
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
	if err := validateFilters(q.filters...); err != nil {
		return nil, fmt.Errorf("aggregate %s: %w", q.mgr.info.TypeName, err)
	}
	for _, spec := range specs {
		if err := validateAttrName(spec.Attr); err != nil {
			return nil, fmt.Errorf("aggregate %s: %w", q.mgr.info.TypeName, err)
		}
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
		val, err := floatFromResult(flat, resultVar)
		if err != nil {
			return nil, fmt.Errorf("aggregate %s (%s): %w", q.mgr.info.TypeName, key, err)
		}
		results[key] = val
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
	if err := validateFilters(gq.filters...); err != nil {
		return nil, fmt.Errorf("groupby %s: %w", gq.mgr.info.TypeName, err)
	}
	if err := validateAttrName(gq.groupBy); err != nil {
		return nil, fmt.Errorf("groupby %s: %w", gq.mgr.info.TypeName, err)
	}
	for _, spec := range specs {
		if err := validateAttrName(spec.Attr); err != nil {
			return nil, fmt.Errorf("groupby %s: %w", gq.mgr.info.TypeName, err)
		}
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

	rawResults, err := gq.mgr.readQuery(ctx, query)
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
			val, err := floatFromResult(row, sanitizeVar(key))
			if err != nil {
				return nil, fmt.Errorf("groupby %s (%s): %w", gq.mgr.info.TypeName, key, err)
			}
			aggs[key] = val
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

// extractCount leniently reads the "count" key of a reduce result, returning
// 0 when the value is missing or unrecognized. Prefer countFromResult, which
// surfaces those conditions as errors.
func extractCount(result map[string]any) int64 {
	n, _ := countFromResult(result)
	return n
}

// countFromResult reads the "count" key of a reduce result row. A missing key
// or an unrecognized value shape is an error rather than a silent zero, so
// driver/protocol drift cannot masquerade as an empty result.
func countFromResult(result map[string]any) (int64, error) {
	raw, ok := result["count"]
	if !ok {
		return 0, fmt.Errorf("count result has no %q key (keys: %s)", "count", strings.Join(slices.Sorted(maps.Keys(result)), ", "))
	}
	return toInt64(unwrapValue(raw))
}

// floatFromResult reads a numeric aggregate value by key from a result row.
// A missing key or an unrecognized value shape is an error rather than a
// silent zero.
func floatFromResult(result map[string]any, key string) (float64, error) {
	raw, ok := result[key]
	if !ok {
		return 0, fmt.Errorf("aggregate result has no %q key (keys: %s)", key, strings.Join(slices.Sorted(maps.Keys(result)), ", "))
	}
	return toFloat64(unwrapValue(raw))
}

// toInt64 converts a value to int64, handling TypeDB 3.x "Value(integer: N)"
// strings. A nil value (aggregate over an empty set) converts to 0; any other
// unrecognized shape is an error.
func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return int64(n), nil
	case int64:
		return n, nil
	case uint64:
		return int64(n), nil
	case int:
		return int64(n), nil
	case string:
		f, err := parseValueString(n)
		if err != nil {
			return 0, err
		}
		return int64(f), nil
	}
	return 0, fmt.Errorf("cannot convert %T value %v to int64", v, v)
}

// toFloat64 converts a value to float64, handling TypeDB 3.x "Value(type: N)"
// strings. A nil value (aggregate over an empty set) converts to 0; any other
// unrecognized shape is an error.
func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return n, nil
	case int64:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	case int:
		return float64(n), nil
	case string:
		return parseValueString(n)
	}
	return 0, fmt.Errorf("cannot convert %T value %v to float64", v, v)
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

// parseValueString parses TypeDB 3.x result strings like "Value(integer: 55)"
// or "Value(double: 3.14)". Unrecognized shapes are an error so protocol
// drift (e.g. a new value type) cannot silently read as zero.
func parseValueString(s string) (float64, error) {
	for _, prefix := range []string{"Value(integer: ", "Value(double: ", "Value(long: "} {
		if body, ok := strings.CutPrefix(s, prefix); ok {
			if num, ok := strings.CutSuffix(body, ")"); ok {
				val, err := strconv.ParseFloat(num, 64)
				if err == nil {
					return val, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("unrecognized aggregate value string %q", s)
}
