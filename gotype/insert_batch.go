package gotype

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/CaliLuke/go-typeql/v3/given"
)

// batchInsertTx is optional: pure-Go transaction implementations continue to
// work with the per-instance InsertMany path without implementing it.
type batchInsertTx interface {
	QueryWithGivenRows(context.Context, string, *given.TypedRows) ([]map[string]any, error)
}

const insertBatchSize = 32

func (m *Manager[T]) insertManyBatched(ctx context.Context, tx Tx, instances []*T, pendingIIDs []string) (bool, error) {
	batchTx, ok := tx.(batchInsertTx)
	if !ok || len(instances) < 2 || m.info.Kind != ModelKindEntity || len(m.info.KeyFields) != 1 || m.info.KeyFields[0].ValueType != "string" {
		return false, nil
	}
	keyIndex, supported := m.batchKeyIndex()
	if !supported {
		return false, nil
	}
	values, seen, supported, err := m.prepareBatchValues(instances, keyIndex)
	if !supported || err != nil {
		return supported, err
	}
	query, variables := buildBatchInsertQuery(m.info, keyIndex)
	return true, m.executeBatchRows(ctx, batchTx, query, variables, values, seen, pendingIIDs, "insert_many")
}

func (m *Manager[T]) batchKeyIndex() (int, bool) {
	keyIndex := -1
	for i, fi := range m.info.Fields {
		if fi.IsSlice || (fi.ValueType != "string" && fi.ValueType != "integer" && fi.ValueType != "boolean" && fi.ValueType != "double") {
			return 0, false
		}
		if fi.Tag.Name == m.info.KeyFields[0].Tag.Name {
			keyIndex = i
		}
	}
	return keyIndex, keyIndex >= 0
}

func (m *Manager[T]) prepareBatchValues(instances []*T, keyIndex int) ([][]given.Value, map[string]int, bool, error) {
	// Check the complete input before sending a query. An unsupported optional
	// value falls back for the entire call, preserving one atomic transaction.
	valuesByInstance := make([][]given.Value, len(instances))
	seen := make(map[string]int, len(instances))
	for i, inst := range instances {
		if inst == nil {
			return nil, nil, true, fmt.Errorf("insert_many %s[%d]: instance must not be nil", m.info.TypeName, i)
		}
		if err := m.validateKeyAttributes("insert", inst); err != nil {
			return nil, nil, true, fmt.Errorf("insert_many %s[%d]: %w", m.info.TypeName, i, err)
		}
		v := reflectValue(inst)
		values := make([]given.Value, len(m.info.Fields))
		for j, fi := range m.info.Fields {
			raw := extractSingleFieldValue(v, fi)
			if raw == nil && optionalBatchField(fi, j, keyIndex) {
				// Insert omits a nil optional field; the batch query skips it
				// through the optional variable and its try block.
				values[j] = given.Value{Type: "empty"}
				continue
			}
			value, supported := batchValue(fi, raw)
			if !supported {
				return nil, nil, false, nil
			}
			values[j] = value
		}
		key := values[keyIndex].Value.(string)
		if previous, duplicate := seen[key]; duplicate {
			return nil, nil, true, fmt.Errorf("insert_many %s[%d]: duplicate key %q (also at index %d)", m.info.TypeName, i, key, previous)
		}
		seen[key] = i
		valuesByInstance[i] = values
	}
	return valuesByInstance, seen, true, nil
}

func (m *Manager[T]) executeBatchRows(ctx context.Context, tx batchInsertTx, query string, variables []string, values [][]given.Value, seen map[string]int, pendingIIDs []string, op string) error {
	for start := 0; start < len(values); start += insertBatchSize {
		end := min(start+insertBatchSize, len(values))
		rows := given.NewRows(variables...)
		for i := start; i < end; i++ {
			if err := rows.Add(values[i]...); err != nil {
				return fmt.Errorf("%s %s[%d]: %w", op, m.info.TypeName, i, err)
			}
		}
		results, err := tx.QueryWithGivenRows(ctx, query, rows)
		if err != nil {
			return fmt.Errorf("%s %s[%d:%d]: %w", op, m.info.TypeName, start, end, err)
		}
		if len(results) != end-start {
			return fmt.Errorf("%s %s[%d:%d]: expected %d IID results, got %d", op, m.info.TypeName, start, end, end-start, len(results))
		}
		if err := m.mapBatchIIDs(results, seen, pendingIIDs, start, end, op); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager[T]) mapBatchIIDs(results []map[string]any, seen map[string]int, pendingIIDs []string, start, end int, op string) error {
	for _, result := range results {
		keyValue, ok := lookupResultValue(result, "_key")
		key, keyOK := keyValue.(string)
		i, expected := seen[key]
		iid := extractIID(result)
		if !ok || !keyOK || !expected || i < start || i >= end || iid == "" || pendingIIDs[i] != "" {
			return fmt.Errorf("%s %s[%d:%d]: invalid IID mapping in batch result", op, m.info.TypeName, start, end)
		}
		pendingIIDs[i] = iid
	}
	return nil
}

// buildBatchInsertQuery builds the typed-row insert. Pointer fields are
// optional variables whose attribute is inserted in a try block, so a row
// with an empty value inserts the entity without that attribute, like Insert.
func buildBatchInsertQuery(info *ModelInfo, keyIndex int) (string, []string) {
	variables := make([]string, len(info.Fields))
	var b strings.Builder
	b.WriteString("given ")
	for i, fi := range info.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		variables[i] = fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "$%s: %s", variables[i], fi.ValueType)
		if optionalBatchField(fi, i, keyIndex) {
			b.WriteByte('?')
		}
	}
	fmt.Fprintf(&b, ";\ninsert $e isa %s", info.TypeName)
	for i, fi := range info.Fields {
		if !optionalBatchField(fi, i, keyIndex) {
			fmt.Fprintf(&b, ", has %s == $%s", fi.Tag.Name, variables[i])
		}
	}
	b.WriteByte(';')
	for i, fi := range info.Fields {
		if optionalBatchField(fi, i, keyIndex) {
			fmt.Fprintf(&b, "\ntry { $e has %s == $%s; };", fi.Tag.Name, variables[i])
		}
	}
	fmt.Fprintf(&b, "\nfetch {"+`"_iid": iid($e), "_key": $%s`+"};", variables[keyIndex])
	return b.String(), variables
}

// optionalBatchField reports whether a batch insert treats field i as
// optional. The key is never optional.
func optionalBatchField(fi FieldInfo, i, keyIndex int) bool {
	return fi.IsPointer && i != keyIndex
}

func buildBatchWriteQuery(info *ModelInfo, keyIndex int, keyword string) (string, []string) {
	variables := make([]string, len(info.Fields))
	var b strings.Builder
	b.WriteString("given ")
	for i, fi := range info.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		variables[i] = fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "$%s: %s", variables[i], fi.ValueType)
	}
	fmt.Fprintf(&b, ";\n%s $e isa %s", keyword, info.TypeName)
	for i, fi := range info.Fields {
		fmt.Fprintf(&b, ", has %s == $%s", fi.Tag.Name, variables[i])
	}
	fmt.Fprintf(&b, ";\nfetch {"+`"_iid": iid($e), "_key": $%s`+"};", variables[keyIndex])
	return b.String(), variables
}

func batchValue(fi FieldInfo, value any) (given.Value, bool) {
	if value == nil {
		return given.Value{}, false
	}
	v := reflect.ValueOf(value)
	switch fi.ValueType {
	case "string":
		return given.Value{Type: "string", Value: v.String()}, true
	case "boolean":
		return given.Value{Type: "boolean", Value: v.Bool()}, true
	case "integer":
		if v.CanInt() {
			return given.Value{Type: "integer", Value: v.Int()}, true
		}
		if v.CanUint() && v.Uint() <= math.MaxInt64 {
			return given.Value{Type: "integer", Value: int64(v.Uint())}, true
		}
	case "double":
		return given.Value{Type: "double", Value: v.Convert(reflect.TypeFor[float64]()).Float()}, true
	}
	return given.Value{}, false
}
