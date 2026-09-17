package gotype

import (
	"context"

	"github.com/CaliLuke/go-typeql/v2/given"
)

// putManyBatched selects only the scalar, single-string-key entity subset.
// Duplicate keys and unsupported shapes use the per-instance path, preserving
// their existing semantics inside the same transaction.
func (m *Manager[T]) putManyBatched(ctx context.Context, tx Tx, instances []*T, pendingIIDs []string) (bool, error) {
	batchTx, ok := tx.(batchInsertTx)
	if !ok || len(instances) < 2 || m.info.Kind != ModelKindEntity || len(m.info.KeyFields) != 1 || m.info.KeyFields[0].ValueType != "string" {
		return false, nil
	}
	keyIndex, supported := m.batchKeyIndex()
	if !supported {
		return false, nil
	}
	values, seen, supported, err := m.preparePutBatchValues(instances, keyIndex)
	if !supported || err != nil {
		return supported, err
	}
	query, variables := buildBatchWriteQuery(m.info, keyIndex, "put")
	return true, m.executeBatchRows(ctx, batchTx, query, variables, values, seen, pendingIIDs, "put_many")
}

func (m *Manager[T]) preparePutBatchValues(instances []*T, keyIndex int) ([][]given.Value, map[string]int, bool, error) {
	valuesByInstance := make([][]given.Value, len(instances))
	seen := make(map[string]int, len(instances))
	for i, inst := range instances {
		v := reflectValue(inst) // PutMany validates non-nil instances and keys first.
		values := make([]given.Value, len(m.info.Fields))
		for j, fi := range m.info.Fields {
			value, supported := batchValue(fi, extractSingleFieldValue(v, fi))
			if !supported {
				return nil, nil, false, nil
			}
			values[j] = value
		}
		key := values[keyIndex].Value.(string)
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, false, nil
		}
		seen[key] = i
		valuesByInstance[i] = values
	}
	return valuesByInstance, seen, true, nil
}
