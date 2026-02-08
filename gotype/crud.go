// Package gotype provides high-level TypeDB data mapping and CRUD operations.
package gotype

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/CaliLuke/go-typeql/ast"
)

// Manager provides high-level, generic CRUD (Create, Read, Update, Delete) operations
// for a registered TypeDB model type T.
type Manager[T any] struct {
	db       *Database
	info     *ModelInfo
	strategy ModelStrategy
	tx       Tx // non-nil when bound to a specific transaction
}

// NewManager creates a new Manager for the model type T.
// T must be a struct that has been registered via Register[T]().
func NewManager[T any](db *Database) *Manager[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		panic(fmt.Sprintf("gotype: type %s is not registered; call Register[%s]() first", t.Name(), t.Name()))
	}

	return &Manager[T]{
		db:       db,
		info:     info,
		strategy: strategyFor(info.Kind),
	}
}

// NewManagerWithTx creates a Manager bound to an existing transaction context.
// All operations performed by this manager will use the provided transaction.
func NewManagerWithTx[T any](tc *TransactionContext) *Manager[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		panic(fmt.Sprintf("gotype: type %s is not registered; call Register[%s]() first", t.Name(), t.Name()))
	}

	return &Manager[T]{
		db:       tc.db,
		info:     info,
		strategy: strategyFor(info.Kind),
		tx:       tc.Tx(),
	}
}

// Insert adds a new instance of T to the database.
// If T has key fields, the instance's internal IID will be populated upon success.
func (m *Manager[T]) Insert(ctx context.Context, instance *T) error {
	if instance == nil {
		return fmt.Errorf("insert %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "insert", m.info.TypeName); err != nil {
		return err
	}
	insertQuery := m.strategy.BuildInsertQuery(m.info, instance, "e")

	tx, autoCommit, err := m.writeTx()
	if err != nil {
		return fmt.Errorf("insert %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	// Execute insert with fetch - single query now returns IID
	results, err := tx.Query(insertQuery)
	if err != nil {
		return fmt.Errorf("insert %s: %w", m.info.TypeName, err)
	}

	// Parse IID from insert result (fetch clause returns it)
	if len(results) == 1 {
		if iid := extractIID(results[0]); iid != "" {
			setIIDOn(instance, iid)
		}
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("insert %s: commit: %w", m.info.TypeName, err)
		}
	}
	return nil
}

// Get retrieves instances of T that match the specified attribute filters.
// filters is a map where keys are TypeDB attribute names and values are the target values.
func (m *Manager[T]) Get(ctx context.Context, filters map[string]any) ([]*T, error) {
	matchQuery := m.buildFilteredMatch("e", filters)
	fetchQuery := m.strategy.BuildFetchAll(m.info, "e")
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", m.info.TypeName, err)
	}

	return m.hydrateResults(results)
}

// All retrieves all instances of the model type T from the database.
func (m *Manager[T]) All(ctx context.Context) ([]*T, error) {
	return m.Get(ctx, nil)
}

// GetWithRoles retrieves instances of T and populates their role players.
// This is primarily used for relation models.
func (m *Manager[T]) GetWithRoles(ctx context.Context, filters map[string]any) ([]*T, error) {
	matchQuery := m.buildFilteredMatch("e", filters)
	matchAdditions, fetchQuery := m.strategy.BuildFetchWithRoles(m.info, "e")
	if matchAdditions != "" {
		matchQuery += "\n" + matchAdditions
	}
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get_with_roles %s: %w", m.info.TypeName, err)
	}

	return m.hydrateResults(results)
}

// GetByIID retrieves a single instance of T by its internal instance ID (IID).
// It returns nil if no instance is found with the given IID.
func (m *Manager[T]) GetByIID(ctx context.Context, iid string) (*T, error) {
	matchQuery := fmt.Sprintf("match\n$e isa %s, iid %s;", m.info.TypeName, iid)
	fetchQuery := m.strategy.BuildFetchAll(m.info, "e")
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get_by_iid %s: %w", m.info.TypeName, err)
	}

	instances, err := m.hydrateResults(results)
	if err != nil {
		return nil, err
	}
	if len(instances) == 0 {
		return nil, nil
	}
	return instances[0], nil
}

// Update modifies an existing instance of T in the database.
// The instance must have its IID populated (typically from a prior Get or Insert).
func (m *Manager[T]) Update(ctx context.Context, instance *T) error {
	if instance == nil {
		return fmt.Errorf("update %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "update", m.info.TypeName); err != nil {
		return err
	}
	iid := getIIDOf(instance)
	if iid == "" {
		return fmt.Errorf("update %s: instance has no IID", m.info.TypeName)
	}

	tx, autoCommit, err := m.writeTx()
	if err != nil {
		return fmt.Errorf("update %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	if err := m.updateInstanceInTx(tx, instance); err != nil {
		return err
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("update %s: commit: %w", m.info.TypeName, err)
		}
	}
	return nil
}

// updateInstanceInTx performs a batched update within an existing transaction.
// It issues one delete query to remove all non-key attribute values, then one
// insert query to set the new values, minimizing round-trips.
func (m *Manager[T]) updateInstanceInTx(tx Tx, instance *T) error {
	iid := getIIDOf(instance)
	if iid == "" {
		return fmt.Errorf("update %s: instance has no IID", m.info.TypeName)
	}

	v := reflectValue(instance)

	// Collect non-key attribute names for deletion, and new values for insertion.
	var delAttrs []string
	var insHas []string

	for _, fi := range m.info.Fields {
		if fi.Tag.Key {
			continue
		}
		delAttrs = append(delAttrs, fi.Tag.Name)

		field := v.Field(fi.FieldIndex)
		if fi.IsPointer && field.IsNil() {
			continue // nil optional: delete only, no insert
		}

		val := field.Interface()
		if fi.IsPointer {
			val = field.Elem().Interface()
		}
		insHas = append(insHas, fmt.Sprintf("has %s %s", fi.Tag.Name, FormatValue(val)))
	}

	// Single query: match entity + try-match old attrs, delete old, insert new.
	// Uses TypeQL try { } blocks so missing optional attributes don't fail the match.
	if len(delAttrs) == 0 && len(insHas) == 0 {
		return nil
	}

	query := buildBatchUpdate(m.info.TypeName, iid, delAttrs, insHas)
	_, err := tx.Query(query)
	if err != nil {
		return fmt.Errorf("update %s: %w", m.info.TypeName, err)
	}
	return nil
}

// buildBatchUpdate builds a single match-delete-insert query that updates
// all non-key attributes in one round-trip. Uses try { } blocks in both
// the match and delete clauses so missing optional attributes are skipped.
func buildBatchUpdate(typeName, iid string, delAttrs, insHas []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("match\n$e isa %s, iid %s;\n", typeName, iid))

	// Try-match each old attribute (try block needs inner ; and outer ;)
	for i, attr := range delAttrs {
		fmt.Fprintf(&b, "try { $e has %s $old%d; };\n", attr, i)
	}

	// Delete old values using try blocks
	if len(delAttrs) > 0 {
		b.WriteString("delete\n")
		for i := range delAttrs {
			fmt.Fprintf(&b, "try { $old%d of $e; };\n", i)
		}
	}

	// Insert new values
	if len(insHas) > 0 {
		b.WriteString(fmt.Sprintf("insert $e %s;", strings.Join(insHas, ", ")))
	}

	return b.String()
}

// DeleteOption configures delete behavior.
type DeleteOption func(*deleteConfig)

type deleteConfig struct {
	strict bool
}

// WithStrict enables strict mode: delete returns an error if the instance doesn't exist.
func WithStrict() DeleteOption {
	return func(c *deleteConfig) { c.strict = true }
}

// Delete deletes an instance by IID.
func (m *Manager[T]) Delete(ctx context.Context, instance *T, opts ...DeleteOption) error {
	if instance == nil {
		return fmt.Errorf("delete %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "delete", m.info.TypeName); err != nil {
		return err
	}
	iid := getIIDOf(instance)
	if iid == "" {
		return fmt.Errorf("delete %s: instance has no IID", m.info.TypeName)
	}

	cfg := deleteConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.strict {
		count, err := m.countByIID(ctx, iid)
		if err != nil {
			return fmt.Errorf("delete %s: strict check: %w", m.info.TypeName, err)
		}
		if count == 0 {
			return fmt.Errorf("delete %s: instance not found (strict mode)", m.info.TypeName)
		}
	}

	query := fmt.Sprintf("match\n$e isa %s, iid %s;\ndelete $e;", m.info.TypeName, iid)
	if m.tx != nil {
		_, err := m.tx.Query(query)
		if err != nil {
			return fmt.Errorf("delete %s: %w", m.info.TypeName, err)
		}
		return nil
	}
	_, err := m.db.ExecuteWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("delete %s: %w", m.info.TypeName, err)
	}
	return nil
}

// DeleteMany deletes multiple instances in a single transaction.
func (m *Manager[T]) DeleteMany(ctx context.Context, instances []*T, opts ...DeleteOption) error {
	if len(instances) == 0 {
		return nil
	}

	cfg := deleteConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	// Validate all instances are non-nil and have IIDs
	for i, inst := range instances {
		if inst == nil {
			return fmt.Errorf("delete_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		if getIIDOf(inst) == "" {
			return fmt.Errorf("delete_many %s[%d]: instance has no IID", m.info.TypeName, i)
		}
	}

	// Strict mode: pre-check existence of all instances
	if cfg.strict {
		for i, inst := range instances {
			iid := getIIDOf(inst)
			count, err := m.countByIID(ctx, iid)
			if err != nil {
				return fmt.Errorf("delete_many %s[%d]: strict check: %w", m.info.TypeName, i, err)
			}
			if count == 0 {
				return fmt.Errorf("delete_many %s[%d]: instance not found (strict mode)", m.info.TypeName, i)
			}
		}
	}

	tx, autoCommit, err := m.writeTx()
	if err != nil {
		return fmt.Errorf("delete_many %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	for i, inst := range instances {
		iid := getIIDOf(inst)
		query := fmt.Sprintf("match\n$e isa %s, iid %s;\ndelete $e;", m.info.TypeName, iid)
		_, err := tx.Query(query)
		if err != nil {
			return fmt.Errorf("delete_many %s[%d]: %w", m.info.TypeName, i, err)
		}
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("delete_many %s: commit: %w", m.info.TypeName, err)
		}
	}
	return nil
}

// UpdateMany updates multiple instances in a single transaction.
func (m *Manager[T]) UpdateMany(ctx context.Context, instances []*T) error {
	if len(instances) == 0 {
		return nil
	}

	// Validate all instances are non-nil and have IIDs
	for i, inst := range instances {
		if inst == nil {
			return fmt.Errorf("update_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		if getIIDOf(inst) == "" {
			return fmt.Errorf("update_many %s[%d]: instance has no IID", m.info.TypeName, i)
		}
	}

	tx, autoCommit, err := m.writeTx()
	if err != nil {
		return fmt.Errorf("update_many %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	for i, inst := range instances {
		if err := m.updateInstanceInTx(tx, inst); err != nil {
			return fmt.Errorf("update_many %s[%d]: %w", m.info.TypeName, i, err)
		}
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("update_many %s: commit: %w", m.info.TypeName, err)
		}
	}
	return nil
}

// Put upserts an instance (insert or update).
// After a successful put, the instance's IID is populated (if it has key fields).
func (m *Manager[T]) Put(ctx context.Context, instance *T) error {
	if instance == nil {
		return fmt.Errorf("put %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "put", m.info.TypeName); err != nil {
		return err
	}
	putQuery := m.strategy.BuildPutQuery(m.info, instance, "e")

	tx, autoCommit, err := m.writeTx()
	if err != nil {
		return fmt.Errorf("put %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	_, err = tx.Query(putQuery)
	if err != nil {
		return fmt.Errorf("put %s: %w", m.info.TypeName, err)
	}

	// Fetch IID in the same transaction via key match
	if len(m.info.KeyFields) > 0 {
		matchQuery := m.strategy.BuildMatchByKey(m.info, instance, "e")
		iidQuery := matchQuery + "\n" + `fetch { "_iid": iid($e) };`

		results, err := tx.Query(iidQuery)
		if err == nil && len(results) == 1 {
			if iid := extractIID(results[0]); iid != "" {
				setIIDOn(instance, iid)
			}
		}
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("put %s: commit: %w", m.info.TypeName, err)
		}
	}
	return nil
}

// PutMany upserts multiple instances in a single transaction.
func (m *Manager[T]) PutMany(ctx context.Context, instances []*T) error {
	if len(instances) == 0 {
		return nil
	}

	tx, err := m.db.Transaction(WriteTransaction)
	if err != nil {
		return fmt.Errorf("put_many %s: %w", m.info.TypeName, err)
	}
	defer tx.Close()

	for i, inst := range instances {
		if inst == nil {
			return fmt.Errorf("put_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		varName := fmt.Sprintf("e%d", i)
		putQuery := m.strategy.BuildPutQuery(m.info, inst, varName)

		_, err := tx.Query(putQuery)
		if err != nil {
			return fmt.Errorf("put_many %s[%d]: %w", m.info.TypeName, i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put_many %s: commit: %w", m.info.TypeName, err)
	}

	// Fetch IIDs in a read transaction
	for _, inst := range instances {
		if len(m.info.KeyFields) > 0 {
			matchQuery := m.strategy.BuildMatchByKey(m.info, inst, "e")
			iidQuery := matchQuery + "\n" + `fetch { "_iid": iid($e) };`

			results, err := m.db.ExecuteRead(ctx, iidQuery)
			if err == nil && len(results) == 1 {
				if iid := extractIID(results[0]); iid != "" {
					setIIDOn(inst, iid)
				}
			}
		}
	}

	return nil
}

// countByIID checks if an instance with the given IID exists.
func (m *Manager[T]) countByIID(ctx context.Context, iid string) (int64, error) {
	query := fmt.Sprintf("match\n$e isa %s, iid %s;\nreduce $count = count($e);", m.info.TypeName, iid)
	results, err := m.readQuery(ctx, query)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return extractCount(results[0]), nil
}

// InsertMany inserts multiple instances in a single transaction.
func (m *Manager[T]) InsertMany(ctx context.Context, instances []*T) error {
	if len(instances) == 0 {
		return nil
	}

	tx, err := m.db.Transaction(WriteTransaction)
	if err != nil {
		return fmt.Errorf("insert_many %s: %w", m.info.TypeName, err)
	}
	defer tx.Close()

	for i, inst := range instances {
		if inst == nil {
			return fmt.Errorf("insert_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		varName := fmt.Sprintf("e%d", i)
		insertQuery := m.strategy.BuildInsertQuery(m.info, inst, varName)

		// Execute insert with fetch - get IID in same query
		results, err := tx.Query(insertQuery)
		if err != nil {
			return fmt.Errorf("insert_many %s[%d]: %w", m.info.TypeName, i, err)
		}

		// Parse IID from insert result (fetch clause returns it)
		if len(results) == 1 {
			if iid := extractIID(results[0]); iid != "" {
				setIIDOn(inst, iid)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert_many %s: commit: %w", m.info.TypeName, err)
	}

	return nil
}

// GetByIIDPolymorphic fetches a single instance by IID with polymorphic type resolution.
// It resolves the actual stored type and fetches all of that type's attributes,
// so subtype-specific fields are preserved when the concrete type is registered.
// Returns the instance hydrated as *T (base type fields only), the type label,
// and an error if any. Use GetByIIDPolymorphicAny for full subtype hydration.
// Returns nil, "", nil if not found.
func (m *Manager[T]) GetByIIDPolymorphic(ctx context.Context, iid string) (*T, string, error) {
	if err := checkCtx(ctx, "get_by_iid_polymorphic", m.info.TypeName); err != nil {
		return nil, "", err
	}

	// Single query fetches type label + union of all subtype fields
	matchQuery := fmt.Sprintf("match\n$e isa! $t, iid %s;\n$t sub %s;", iid, m.info.TypeName)
	fetchQuery := buildPolymorphicFetch(m.info, "e")
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic %s: %w", m.info.TypeName, err)
	}
	if len(results) == 0 {
		return nil, "", nil
	}

	flat := unwrapResult(results[0])
	typeLabel := ""
	if tl, ok := flat["_type"]; ok {
		if s, ok := tl.(string); ok {
			typeLabel = s
		}
	}
	delete(flat, "_type")

	instance, err := HydrateNew[T](flat)
	if err != nil {
		return nil, "", fmt.Errorf("hydrate %s: %w", m.info.TypeName, err)
	}
	return instance, typeLabel, nil
}

// GetByIIDPolymorphicAny fetches a single instance by IID and hydrates it as
// the actual concrete subtype. Unlike GetByIIDPolymorphic which always returns *T,
// this returns any (the concrete type pointer) so subtype-specific fields are preserved.
// The concrete subtype must be registered via Register[ConcreteType]().
// Returns nil, "", nil if not found.
func (m *Manager[T]) GetByIIDPolymorphicAny(ctx context.Context, iid string) (any, string, error) {
	if err := checkCtx(ctx, "get_by_iid_polymorphic_any", m.info.TypeName); err != nil {
		return nil, "", err
	}

	// Single query fetches type label + union of all subtype fields
	matchQuery := fmt.Sprintf("match\n$e isa! $t, iid %s;\n$t sub %s;", iid, m.info.TypeName)
	fetchQuery := buildPolymorphicFetch(m.info, "e")
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic_any %s: %w", m.info.TypeName, err)
	}
	if len(results) == 0 {
		return nil, "", nil
	}

	flat := unwrapResult(results[0])
	typeLabel := ""
	if tl, ok := flat["_type"]; ok {
		if s, ok := tl.(string); ok {
			typeLabel = s
		}
	}

	instance, err := HydrateAny(flat)
	if err != nil {
		return nil, "", fmt.Errorf("hydrate_any %s: %w", typeLabel, err)
	}
	return instance, typeLabel, nil
}

// --- Transaction helpers ---

// checkCtx returns an error if the context is already cancelled.
func checkCtx(ctx context.Context, op, typeName string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s %s: context cancelled: %w", op, typeName, err)
	}
	return nil
}

// writeTx returns the bound transaction or creates a new write transaction.
// If a bound tx is used, autoCommit is false (caller manages lifecycle).
func (m *Manager[T]) writeTx() (tx Tx, autoCommit bool, err error) {
	if m.tx != nil {
		return m.tx, false, nil
	}
	tx, err = m.db.Transaction(WriteTransaction)
	if err != nil {
		return nil, false, err
	}
	return tx, true, nil
}

// readQuery executes a read query using the bound tx or a new read transaction.
func (m *Manager[T]) readQuery(ctx context.Context, query string) ([]map[string]any, error) {
	if m.tx != nil {
		return m.tx.Query(query)
	}
	return m.db.ExecuteRead(ctx, query)
}

// --- Internal helpers ---

func (m *Manager[T]) buildFilteredMatch(varName string, filters map[string]any) string {
	if len(filters) == 0 {
		return m.strategy.BuildMatchAll(m.info, varName)
	}

	var constraints []string
	constraints = append(constraints, fmt.Sprintf("$%s isa %s", varName, m.info.TypeName))
	for attr, val := range filters {
		constraints = append(constraints, fmt.Sprintf("has %s %s", attr, FormatValue(val)))
	}
	return "match\n" + strings.Join(constraints, ",\n") + ";"
}

func (m *Manager[T]) hydrateResults(results []map[string]any) ([]*T, error) {
	if len(results) == 0 {
		return nil, nil
	}

	var instances []*T
	for _, row := range results {
		flat := unwrapResult(row)
		instance, err := HydrateNew[T](flat)
		if err != nil {
			return nil, fmt.Errorf("hydrate %s: %w", m.info.TypeName, err)
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

// getIIDOf extracts the IID from any entity or relation pointer.
func getIIDOf[T any](instance *T) string {
	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	return getIIDFromValue(v)
}

// setIIDOn sets the IID on an entity or relation instance.
func setIIDOn[T any](instance *T, iid string) {
	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if !field.CanAddr() {
			continue
		}
		if e, ok := field.Addr().Interface().(*BaseEntity); ok {
			e.SetIID(iid)
			return
		}
		if r, ok := field.Addr().Interface().(*BaseRelation); ok {
			r.SetIID(iid)
			return
		}
	}
}

// extractIID extracts the IID string from a fetch result.
// Handles both direct string and wrapped {"value": "0x..."} formats.
func extractIID(result map[string]any) string {
	v, ok := result["_iid"]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		if inner, ok := val["value"]; ok {
			if s, ok := inner.(string); ok {
				return s
			}
		}
	}
	return ""
}

// unwrapResult flattens nested TypeDB result structures.
// TypeDB fetch results may wrap values as {"value": X, "type": {...}}.
func unwrapResult(result map[string]any) map[string]any {
	flat := make(map[string]any)
	for key, val := range result {
		flat[key] = unwrapValue(val)
	}
	return flat
}

func unwrapValue(val any) any {
	if val == nil {
		return nil
	}
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	if v, ok := m["value"]; ok {
		return v
	}
	return val
}

// buildPolymorphicFetch builds a fetch clause that includes the type label
// plus the union of all attribute fields from the base type and all registered
// subtypes. This allows polymorphic retrieval in a single query.
func buildPolymorphicFetch(info *ModelInfo, varName string) string {
	var items []ast.FetchItem
	items = append(items, ast.FetchFunc("_iid", "iid", "$"+varName))
	items = append(items, ast.FetchFunc("_type", "label", "$t"))

	for _, fi := range info.Fields {
		items = appendFetchField(items, fi, varName)
	}
	// Add subtype-only fields
	for _, sub := range SubtypesOf(info.TypeName) {
		for _, fi := range sub.Fields {
			if _, exists := fieldByName(info.Fields, fi.Tag.Name); !exists {
				items = appendFetchField(items, fi, varName)
			}
		}
	}

	fetch := ast.Fetch(items...)
	compiler := &ast.Compiler{}
	result, _ := compiler.Compile(fetch)
	return result
}

func appendFetchField(items []ast.FetchItem, fi FieldInfo, varName string) []ast.FetchItem {
	if fi.IsSlice {
		return append(items, ast.FetchAttributeList{
			Key:      fi.Tag.Name,
			Var:      "$" + varName,
			AttrName: fi.Tag.Name,
		})
	}
	return append(items, ast.FetchAttr(fi.Tag.Name, "$"+varName, fi.Tag.Name))
}

func fieldByName(fields []FieldInfo, name string) (FieldInfo, bool) {
	for _, f := range fields {
		if f.Tag.Name == name {
			return f, true
		}
	}
	return FieldInfo{}, false
}
