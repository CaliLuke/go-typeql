// Package gotype provides high-level TypeDB data mapping and CRUD operations.
package gotype

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
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
func NewManager[T any](db *Database) (*Manager[T], error) {
	info, err := lookupManagerInfo[T]()
	if err != nil {
		return nil, err
	}
	return &Manager[T]{
		db:       db,
		info:     info,
		strategy: strategyFor(info.Kind),
	}, nil
}

// MustNewManager creates a new Manager for the model type T and panics if the
// type has not been registered. Prefer NewManager when the caller needs to
// handle registration failures explicitly.
func MustNewManager[T any](db *Database) *Manager[T] {
	mgr, err := NewManager[T](db)
	if err != nil {
		panic(err)
	}
	return mgr
}

// NewManagerWithTx creates a Manager bound to an existing transaction context.
// All operations performed by this manager will use the provided transaction.
func NewManagerWithTx[T any](tc *TransactionContext) (*Manager[T], error) {
	info, err := lookupManagerInfo[T]()
	if err != nil {
		return nil, err
	}
	return &Manager[T]{
		db:       tc.db,
		info:     info,
		strategy: strategyFor(info.Kind),
		tx:       tc.Tx(),
	}, nil
}

// MustNewManagerWithTx creates a Manager bound to an existing transaction
// context and panics if the model type has not been registered.
func MustNewManagerWithTx[T any](tc *TransactionContext) *Manager[T] {
	mgr, err := NewManagerWithTx[T](tc)
	if err != nil {
		panic(err)
	}
	return mgr
}

func lookupManagerInfo[T any]() (*ModelInfo, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	info, ok := LookupType(t)
	if !ok {
		return nil, fmt.Errorf("gotype: type %s is not registered; call Register[%s]() first", t.Name(), t.Name())
	}
	return info, nil
}

// Insert adds a new instance of T to the database.
// If T has key fields, the instance's internal IID will be populated upon success.
// Key attributes must be set to non-zero values; a missing key returns a
// *KeyAttributeError instead of silently inserting a zero-value key.
func (m *Manager[T]) Insert(ctx context.Context, instance *T) error {
	if instance == nil {
		return fmt.Errorf("insert %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "insert", m.info.TypeName); err != nil {
		return err
	}
	if err := m.validateKeyAttributes("insert", instance); err != nil {
		return err
	}
	insertQuery, err := m.strategy.BuildInsertQuery(m.info, instance, "e")
	if err != nil {
		return fmt.Errorf("insert %s: build query: %w", m.info.TypeName, err)
	}

	tx, autoCommit, err := m.writeTx(ctx)
	if err != nil {
		return fmt.Errorf("insert %s: %w", m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	// Execute insert with fetch - single query now returns IID
	results, err := tx.QueryWithContext(ctx, insertQuery)
	if err != nil {
		return fmt.Errorf("insert %s: %w", m.info.TypeName, err)
	}

	// Parse IID from insert result (fetch clause returns it)
	if len(results) == 1 {
		if iid := extractIID(results[0]); iid != "" {
			setIIDOnInfo(instance, m.info, iid)
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
	matchQuery, err := m.buildFilteredMatch("e", filters)
	if err != nil {
		return nil, fmt.Errorf("get %s: build match: %w", m.info.TypeName, err)
	}
	fetchQuery, err := m.strategy.BuildFetchAll(m.info, "e")
	if err != nil {
		return nil, fmt.Errorf("get %s: build fetch: %w", m.info.TypeName, err)
	}
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", m.info.TypeName, err)
	}

	return m.hydrateResults(results)
}

// GetOne retrieves exactly one instance of T matching the specified attribute
// filters. It returns a *NotFoundError when no instance matches and a
// *NotUniqueError when more than one instance matches, so callers can
// distinguish those cases with errors.As.
func (m *Manager[T]) GetOne(ctx context.Context, filters map[string]any) (*T, error) {
	results, err := m.Get(ctx, filters)
	if err != nil {
		return nil, err
	}
	switch len(results) {
	case 0:
		return nil, &NotFoundError{TypeName: m.info.TypeName}
	case 1:
		return results[0], nil
	default:
		return nil, &NotUniqueError{TypeName: m.info.TypeName, Count: len(results)}
	}
}

// All retrieves all instances of the model type T from the database.
func (m *Manager[T]) All(ctx context.Context) ([]*T, error) {
	return m.Get(ctx, nil)
}

// GetWithRoles retrieves instances of T and populates their role players.
// This is primarily used for relation models.
func (m *Manager[T]) GetWithRoles(ctx context.Context, filters map[string]any) ([]*T, error) {
	matchQuery, err := m.buildFilteredMatch("e", filters)
	if err != nil {
		return nil, fmt.Errorf("get_with_roles %s: build match: %w", m.info.TypeName, err)
	}
	matchAdditions, fetchQuery, err := m.strategy.BuildFetchWithRoles(m.info, "e")
	if err != nil {
		return nil, fmt.Errorf("get_with_roles %s: build fetch: %w", m.info.TypeName, err)
	}
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
// It returns nil if no instance is found with the given IID. The IID must
// match 0x[0-9a-fA-F]+; anything else is rejected with an error before any
// query is sent.
func (m *Manager[T]) GetByIID(ctx context.Context, iid string) (*T, error) {
	if err := validateIID(iid); err != nil {
		return nil, fmt.Errorf("get_by_iid %s: %w", m.info.TypeName, err)
	}
	matchQuery := fmt.Sprintf("match\n$e isa %s, iid %s;", m.info.TypeName, iid)
	fetchQuery, err := m.strategy.BuildFetchAll(m.info, "e")
	if err != nil {
		return nil, fmt.Errorf("get_by_iid %s: build fetch: %w", m.info.TypeName, err)
	}
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
	iid := getIIDOfInfo(instance, m.info)
	if iid == "" {
		return fmt.Errorf("update %s: instance has no IID", m.info.TypeName)
	}

	return m.withWriteTx(ctx, "update", m.writeTx, func(tx Tx) error {
		return m.updateInstanceInTx(ctx, tx, instance)
	})
}

// updateInstanceInTx performs a batched update within an existing transaction.
// It issues one delete query to remove all non-key attribute values, then one
// insert query to set the new values, minimizing round-trips.
func (m *Manager[T]) updateInstanceInTx(ctx context.Context, tx Tx, instance *T) error {
	iid := getIIDOfInfo(instance, m.info)
	if iid == "" {
		return fmt.Errorf("update %s: instance has no IID", m.info.TypeName)
	}

	v := reflectValue(instance)

	// Collect non-key attribute names for deletion, and new values for insertion.
	var delAttrs []string
	nonKeyFields := make([]FieldInfo, 0, len(m.info.Fields))
	for _, fi := range m.info.Fields {
		if fi.Tag.Key {
			continue
		}
		delAttrs = append(delAttrs, fi.Tag.Name)
		nonKeyFields = append(nonKeyFields, fi)
	}

	// buildHasClauseParts handles pointers (skip nil: delete only), slices
	// (one has-clause per element), and decimal literals exactly like the
	// insert path, so multi-valued attributes round-trip instead of being
	// stringified.
	insHas, err := buildHasClauseParts(v, nonKeyFields)
	if err != nil {
		return fmt.Errorf("update %s: %w", m.info.TypeName, err)
	}

	// Single query: match entity + try-match old attrs, delete old, insert new.
	// Uses TypeQL try { } blocks so missing optional attributes don't fail the match.
	if len(delAttrs) == 0 && len(insHas) == 0 {
		return nil
	}

	query := buildBatchUpdate(m.info.TypeName, iid, delAttrs, insHas)
	if _, err := tx.QueryWithContext(ctx, query); err != nil {
		return fmt.Errorf("update %s: %w", m.info.TypeName, err)
	}
	return nil
}

// buildBatchUpdate builds a single match-delete-insert query that updates
// all non-key attributes in one round-trip. Uses try { } blocks in both
// the match and delete clauses so missing optional attributes are skipped.
func buildBatchUpdate(typeName, iid string, delAttrs, insHas []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "match\n$e isa %s, iid %s;\n", typeName, iid)

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
		fmt.Fprintf(&b, "insert $e %s;", strings.Join(insHas, ", "))
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
	iid := getIIDOfInfo(instance, m.info)
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
		_, err := m.tx.QueryWithContext(ctx, query)
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
		if getIIDOfInfo(inst, m.info) == "" {
			return fmt.Errorf("delete_many %s[%d]: instance has no IID", m.info.TypeName, i)
		}
	}

	// Strict mode: pre-check existence of all instances
	if cfg.strict {
		for i, inst := range instances {
			iid := getIIDOfInfo(inst, m.info)
			count, err := m.countByIID(ctx, iid)
			if err != nil {
				return fmt.Errorf("delete_many %s[%d]: strict check: %w", m.info.TypeName, i, err)
			}
			if count == 0 {
				return fmt.Errorf("delete_many %s[%d]: instance not found (strict mode)", m.info.TypeName, i)
			}
		}
	}

	return m.withWriteTx(ctx, "delete_many", m.writeTx, func(tx Tx) error {
		for i, inst := range instances {
			iid := getIIDOfInfo(inst, m.info)
			query := fmt.Sprintf("match\n$e isa %s, iid %s;\ndelete $e;", m.info.TypeName, iid)
			_, err := tx.QueryWithContext(ctx, query)
			if err != nil {
				return fmt.Errorf("delete_many %s[%d]: %w", m.info.TypeName, i, err)
			}
		}
		return nil
	})
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
		if getIIDOfInfo(inst, m.info) == "" {
			return fmt.Errorf("update_many %s[%d]: instance has no IID", m.info.TypeName, i)
		}
	}

	return m.withWriteTx(ctx, "update_many", m.writeTx, func(tx Tx) error {
		for i, inst := range instances {
			if err := m.updateInstanceInTx(ctx, tx, inst); err != nil {
				return fmt.Errorf("update_many %s[%d]: %w", m.info.TypeName, i, err)
			}
		}
		return nil
	})
}

// Put upserts an instance (insert or update).
// After a successful put, the instance's IID is populated (if it has key fields).
// Key attributes must be set to non-zero values; a missing key returns a
// *KeyAttributeError since the upsert match is meaningless without it.
func (m *Manager[T]) Put(ctx context.Context, instance *T) error {
	if instance == nil {
		return fmt.Errorf("put %s: instance must not be nil", m.info.TypeName)
	}
	if err := checkCtx(ctx, "put", m.info.TypeName); err != nil {
		return err
	}
	if err := m.validateKeyAttributes("put", instance); err != nil {
		return err
	}
	putQuery, err := m.strategy.BuildPutQuery(m.info, instance, "e")
	if err != nil {
		return fmt.Errorf("put %s: build query: %w", m.info.TypeName, err)
	}

	return m.withWriteTx(ctx, "put", m.writeTx, func(tx Tx) error {
		_, err = tx.QueryWithContext(ctx, putQuery)
		if err != nil {
			return fmt.Errorf("put %s: %w", m.info.TypeName, err)
		}

		// Fetch IID in the same transaction via key match
		if len(m.info.KeyFields) > 0 {
			matchQuery, err := m.strategy.BuildMatchByKey(m.info, instance, "e")
			if err != nil {
				return fmt.Errorf("put %s: build iid query: %w", m.info.TypeName, err)
			}
			iidQuery := matchQuery + "\n" + `fetch { "_iid": iid($e) };`

			results, err := tx.QueryWithContext(ctx, iidQuery)
			if err != nil {
				return fmt.Errorf("put %s: fetch iid: %w", m.info.TypeName, err)
			}
			if len(results) == 1 {
				if iid := extractIID(results[0]); iid != "" {
					setIIDOnInfo(instance, m.info, iid)
				}
			}
		}
		return nil
	})
}

// PutMany upserts multiple instances in a single transaction.
// IIDs are fetched inside the same write transaction (one key-match query per
// instance) instead of opening a read transaction per instance afterwards.
func (m *Manager[T]) PutMany(ctx context.Context, instances []*T) error {
	if len(instances) == 0 {
		return nil
	}

	for i, inst := range instances {
		if inst == nil {
			return fmt.Errorf("put_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		if err := m.validateKeyAttributes("put", inst); err != nil {
			return fmt.Errorf("put_many %s[%d]: %w", m.info.TypeName, i, err)
		}
	}

	hasKeys := len(m.info.KeyFields) > 0
	pendingIIDs := make([]string, len(instances))
	err := m.withWriteTx(ctx, "put_many", m.newWriteTx, func(tx Tx) error {
		for i, inst := range instances {
			varName := fmt.Sprintf("e%d", i)
			putQuery, err := m.strategy.BuildPutQuery(m.info, inst, varName)
			if err != nil {
				return fmt.Errorf("put_many %s[%d]: build query: %w", m.info.TypeName, i, err)
			}

			if _, err := tx.QueryWithContext(ctx, putQuery); err != nil {
				return fmt.Errorf("put_many %s[%d]: %w", m.info.TypeName, i, err)
			}

			if !hasKeys {
				continue
			}
			// Fetch the IID in the same transaction via key match.
			matchQuery, err := m.strategy.BuildMatchByKey(m.info, inst, "e")
			if err != nil {
				return fmt.Errorf("put_many %s[%d]: build iid query: %w", m.info.TypeName, i, err)
			}
			iidQuery := matchQuery + "\n" + `fetch { "_iid": iid($e) };`

			results, err := tx.QueryWithContext(ctx, iidQuery)
			if err != nil {
				return fmt.Errorf("put_many %s[%d]: fetch iid: %w", m.info.TypeName, i, err)
			}
			if len(results) == 1 {
				if iid := extractIID(results[0]); iid != "" {
					pendingIIDs[i] = iid
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for i, iid := range pendingIIDs {
		if iid != "" {
			setIIDOnInfo(instances[i], m.info, iid)
		}
	}

	return nil
}

// countByIID checks if an instance with the given IID exists.
func (m *Manager[T]) countByIID(ctx context.Context, iid string) (int64, error) {
	if err := validateIID(iid); err != nil {
		return 0, err
	}
	query := fmt.Sprintf("match\n$e isa %s, iid %s;\nreduce $count = count($e);", m.info.TypeName, iid)
	results, err := m.readQuery(ctx, query)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return countFromResult(results[0])
}

// InsertMany inserts multiple instances in a single transaction.
func (m *Manager[T]) InsertMany(ctx context.Context, instances []*T) error {
	if len(instances) == 0 {
		return nil
	}

	pendingIIDs := make([]string, len(instances))
	err := m.withWriteTx(ctx, "insert_many", m.newWriteTx, func(tx Tx) error {
		for i, inst := range instances {
			if inst == nil {
				return fmt.Errorf("insert_many %s[%d]: instance must not be nil", m.info.TypeName, i)
			}
			if err := m.validateKeyAttributes("insert", inst); err != nil {
				return fmt.Errorf("insert_many %s[%d]: %w", m.info.TypeName, i, err)
			}
			varName := fmt.Sprintf("e%d", i)
			insertQuery, err := m.strategy.BuildInsertQuery(m.info, inst, varName)
			if err != nil {
				return fmt.Errorf("insert_many %s[%d]: build query: %w", m.info.TypeName, i, err)
			}

			// Execute insert with fetch - get IID in same query
			results, err := tx.QueryWithContext(ctx, insertQuery)
			if err != nil {
				return fmt.Errorf("insert_many %s[%d]: %w", m.info.TypeName, i, err)
			}

			// Parse IID from insert result (fetch clause returns it)
			if len(results) == 1 {
				if iid := extractIID(results[0]); iid != "" {
					pendingIIDs[i] = iid
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	for i, iid := range pendingIIDs {
		if iid != "" {
			setIIDOnInfo(instances[i], m.info, iid)
		}
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
	if err := validateIID(iid); err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic %s: %w", m.info.TypeName, err)
	}

	// Single query fetches type label + union of all subtype fields
	matchQuery := fmt.Sprintf("match\n$e isa! $t, iid %s;\n$t sub %s;", iid, m.info.TypeName)
	fetchQuery, err := buildPolymorphicFetch(m.info, "e")
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic %s: build fetch: %w", m.info.TypeName, err)
	}
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic %s: %w", m.info.TypeName, err)
	}
	if len(results) == 0 {
		return nil, "", nil
	}

	typeLabel := ""
	if tl, ok := lookupResultValue(results[0], "_type"); ok {
		if s, ok := tl.(string); ok {
			typeLabel = s
		}
	}

	instance, err := HydrateNew[T](results[0])
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
	if err := validateIID(iid); err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic_any %s: %w", m.info.TypeName, err)
	}

	// Single query fetches type label + union of all subtype fields
	matchQuery := fmt.Sprintf("match\n$e isa! $t, iid %s;\n$t sub %s;", iid, m.info.TypeName)
	fetchQuery, err := buildPolymorphicFetch(m.info, "e")
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic_any %s: build fetch: %w", m.info.TypeName, err)
	}
	query := matchQuery + "\n" + fetchQuery

	results, err := m.readQuery(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("get_by_iid_polymorphic_any %s: %w", m.info.TypeName, err)
	}
	if len(results) == 0 {
		return nil, "", nil
	}

	typeLabel := ""
	if tl, ok := lookupResultValue(results[0], "_type"); ok {
		if s, ok := tl.(string); ok {
			typeLabel = s
		}
	}

	instance, err := HydrateAny(results[0])
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

// writeTx returns the bound transaction or creates a new write transaction,
// honoring context cancellation while acquiring it.
// If a bound tx is used, autoCommit is false (caller manages lifecycle).
func (m *Manager[T]) writeTx(ctx context.Context) (tx Tx, autoCommit bool, err error) {
	if m.tx != nil {
		return m.tx, false, nil
	}
	return m.newWriteTx(ctx)
}

// newWriteTx always opens a fresh write transaction, ignoring any bound tx.
func (m *Manager[T]) newWriteTx(ctx context.Context) (Tx, bool, error) {
	tx, err := m.db.TransactionContext(ctx, WriteTransaction)
	if err != nil {
		return nil, false, err
	}
	return tx, true, nil
}

func (m *Manager[T]) withWriteTx(ctx context.Context, op string, open func(context.Context) (Tx, bool, error), fn func(Tx) error) error {
	tx, autoCommit, err := open(ctx)
	if err != nil {
		return fmt.Errorf("%s %s: %w", op, m.info.TypeName, err)
	}
	if autoCommit {
		defer tx.Close()
	}

	if err := fn(tx); err != nil {
		return err
	}

	if autoCommit {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%s %s: commit: %w", op, m.info.TypeName, err)
		}
	}
	return nil
}

// readQuery executes a read query using the bound tx or a new read transaction.
func (m *Manager[T]) readQuery(ctx context.Context, query string) ([]map[string]any, error) {
	if m.tx != nil {
		return m.tx.QueryWithContext(ctx, query)
	}
	return m.db.ExecuteRead(ctx, query)
}

// --- Internal helpers ---

func (m *Manager[T]) buildFilteredMatch(varName string, filters map[string]any) (string, error) {
	if len(filters) == 0 {
		return m.strategy.BuildMatchAll(m.info, varName)
	}

	var b strings.Builder
	b.WriteString("match\n$")
	b.WriteString(varName)
	b.WriteString(" isa ")
	b.WriteString(m.info.TypeName)
	// Sort attribute names so identical logical filters always produce
	// identical query text (map iteration order is randomized).
	for _, attr := range slices.Sorted(maps.Keys(filters)) {
		// Attribute names are interpolated raw into the query, so reject
		// anything that is not a plain TypeQL identifier (issue #45).
		if err := validateAttrName(attr); err != nil {
			return "", err
		}
		lit, err := m.formatAttrValue(attr, filters[attr])
		if err != nil {
			return "", err
		}
		b.WriteString(",\nhas ")
		b.WriteString(attr)
		b.WriteByte(' ')
		b.WriteString(lit)
	}
	b.WriteString(";")
	return b.String(), nil
}

// formatAttrValue formats a value supplied by attribute name (Get filters,
// bulk updates), using the model's field metadata when the attribute maps to
// a registered field so decimal attributes get `dec`-suffixed literals.
func (m *Manager[T]) formatAttrValue(attr string, val any) (string, error) {
	if fi, ok := m.info.FieldByAttrName(attr); ok {
		lit, err := formatFieldValue(&fi, val)
		if err != nil {
			return "", fmt.Errorf("attribute %s: %w", attr, err)
		}
		return lit, nil
	}
	return FormatValue(val), nil
}

// validateKeyAttributes ensures every key attribute carries a non-zero value
// before a write that must be able to identify the instance by key.
// Returns a *KeyAttributeError naming the first missing key attribute.
func (m *Manager[T]) validateKeyAttributes(op string, instance *T) error {
	if len(m.info.KeyFields) == 0 {
		return nil
	}
	v := reflectValue(instance)
	for _, kf := range m.info.KeyFields {
		val := extractSingleFieldValue(v, kf)
		if val == nil || reflect.ValueOf(val).IsZero() {
			return &KeyAttributeError{
				EntityType: m.info.TypeName,
				FieldName:  kf.Tag.Name,
				Operation:  op,
			}
		}
	}
	return nil
}

func (m *Manager[T]) hydrateResults(results []map[string]any) ([]*T, error) {
	if len(results) == 0 {
		return nil, nil
	}

	instances := make([]*T, 0, len(results))
	for _, row := range results {
		instance, err := hydrateNewWithInfo[T](m.info, row)
		if err != nil {
			return nil, fmt.Errorf("hydrate %s: %w", m.info.TypeName, err)
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

// getIIDOfInfo extracts the IID from any entity or relation pointer using
// the already-resolved model info when available.
func getIIDOfInfo[T any](instance *T, info *ModelInfo) string {
	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	return getIIDFromValueInfo(v, info)
}

// setIIDOnInfo sets the IID on an entity or relation instance using the
// already-resolved model info when available.
func setIIDOnInfo[T any](instance *T, info *ModelInfo, iid string) {
	v := reflect.ValueOf(instance)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	setIIDWithInfo(v, info, iid)
}

// extractIID extracts the IID string from a fetch result.
// Handles both direct string and wrapped {"value": "0x..."} formats.
func extractIID(result map[string]any) string {
	v, ok := lookupResultValue(result, "_iid")
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// buildPolymorphicFetch builds a fetch clause that includes the type label
// plus the union of all attribute fields from the base type and all registered
// subtypes. This allows polymorphic retrieval in a single query.
func buildPolymorphicFetch(info *ModelInfo, varName string) (string, error) {
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
	return compileNode(fetch)
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
