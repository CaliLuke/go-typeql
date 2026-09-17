package gotype

import (
	"context"
	"fmt"
	"strings"

	"github.com/CaliLuke/go-typeql/v2/given"
)

// The IID disjunction is indexed, but large disjunctions are costly to plan.
// Eight rows outperformed larger chunks on the live heterogeneous benchmark.
const updateBatchSize = 8

func (m *Manager[T]) persistUpdatesInTx(ctx context.Context, tx Tx, instances []*T, op string) error {
	if used, err := m.updateManyBatched(ctx, tx, instances, op); used {
		return err
	}
	for i, inst := range instances {
		if err := m.updateInstanceInTx(ctx, tx, inst); err != nil {
			return fmt.Errorf("%s %s[%d]: %w", op, m.info.TypeName, i, err)
		}
	}
	return nil
}

// updateManyBatched handles present scalar non-key fields and distinct IIDs.
// Exceptional rows use the per-instance path in input order between batches.
func (m *Manager[T]) updateManyBatched(ctx context.Context, tx Tx, instances []*T, op string) (bool, error) {
	batchTx, ok := tx.(batchInsertTx)
	if !ok || len(instances) < 2 || m.info.Kind != ModelKindEntity {
		return false, nil
	}
	fields, supported := m.batchUpdateFields()
	if !supported {
		return false, nil
	}
	seen := make(map[string]bool, len(instances))
	run := make([][]given.Value, 0, updateBatchSize)
	runStart := 0
	flush := func() error {
		err := m.persistUpdateRun(ctx, tx, batchTx, fields, instances[runStart:runStart+len(run)], run, op, runStart)
		run = run[:0]
		return err
	}
	for i, inst := range instances {
		iid := getIIDOfInfo(inst, m.info)
		row, eligible := m.batchUpdateRow(inst, iid, fields)
		canonicalIID := strings.ToLower(iid)
		if !eligible || seen[canonicalIID] {
			if err := flush(); err != nil {
				return true, err
			}
			if err := m.updateInstanceInTx(ctx, tx, inst); err != nil {
				return true, fmt.Errorf("%s %s[%d]: %w", op, m.info.TypeName, i, err)
			}
			continue
		}
		seen[canonicalIID] = true
		if len(run) == 0 {
			runStart = i
		}
		run = append(run, row)
		if len(run) == updateBatchSize {
			if err := flush(); err != nil {
				return true, err
			}
		}
	}
	return true, flush()
}

func (m *Manager[T]) batchUpdateFields() ([]FieldInfo, bool) {
	fields := make([]FieldInfo, 0, len(m.info.Fields))
	for _, fi := range m.info.Fields {
		if fi.Tag.Key {
			continue
		}
		if fi.IsSlice || (fi.ValueType != "string" && fi.ValueType != "integer" && fi.ValueType != "boolean" && fi.ValueType != "double") {
			return nil, false
		}
		fields = append(fields, fi)
	}
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

func (m *Manager[T]) batchUpdateRow(inst *T, iid string, fields []FieldInfo) ([]given.Value, bool) {
	if validateIID(iid) != nil {
		return nil, false
	}
	v := reflectValue(inst)
	row := make([]given.Value, 1, len(fields)+1)
	// Given string equality compares the IID's canonical representation, unlike
	// a hexadecimal IID literal, which accepts upper-case digits.
	row[0] = given.Value{Type: "string", Value: strings.ToLower(iid)}
	for _, fi := range fields {
		value, supported := batchValue(fi, extractSingleFieldValue(v, fi))
		if !supported {
			return nil, false
		}
		row = append(row, value)
	}
	return row, true
}

func (m *Manager[T]) persistUpdateRun(ctx context.Context, tx Tx, batchTx batchInsertTx, fields []FieldInfo, instances []*T, rows [][]given.Value, op string, startIndex int) error {
	if len(rows) == 1 {
		if err := m.updateInstanceInTx(ctx, tx, instances[0]); err != nil {
			return fmt.Errorf("%s %s[%d]: %w", op, m.info.TypeName, startIndex, err)
		}
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	return m.executeBatchUpdates(ctx, batchTx, fields, rows, op, startIndex)
}

func (m *Manager[T]) executeBatchUpdates(ctx context.Context, batchTx batchInsertTx, fields []FieldInfo, rowsByInstance [][]given.Value, op string, offset int) error {
	variables := batchUpdateVariables(fields)
	for start := 0; start < len(rowsByInstance); start += updateBatchSize {
		end := min(start+updateBatchSize, len(rowsByInstance))
		iids := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			iids = append(iids, rowsByInstance[i][0].Value.(string))
		}
		query := buildBatchUpdateQuery(m.info.TypeName, fields, iids)
		rows := given.NewRows(variables...)
		for i := start; i < end; i++ {
			if err := rows.Add(rowsByInstance[i]...); err != nil {
				return fmt.Errorf("%s %s[%d]: %w", op, m.info.TypeName, offset+i, err)
			}
		}
		if _, err := batchTx.QueryWithGivenRows(ctx, query, rows); err != nil {
			return fmt.Errorf("%s %s[%d:%d]: %w", op, m.info.TypeName, offset+start, offset+end, err)
		}
	}
	return nil
}

func batchUpdateVariables(fields []FieldInfo) []string {
	variables := make([]string, 1, len(fields)+1)
	variables[0] = "id"
	for i := range fields {
		variables = append(variables, fmt.Sprintf("v%d", i))
	}
	return variables
}

func buildBatchUpdateQuery(typeName string, fields []FieldInfo, iids []string) string {
	var b strings.Builder
	b.WriteString("given $id: string")
	for i, fi := range fields {
		fmt.Fprintf(&b, ", $v%d: %s", i, fi.ValueType)
	}
	fmt.Fprintf(&b, ";\nmatch $e isa %s; %s\niid($e) == $id;\n", typeName, IIDIn(iids...).ToPatterns("e")[0])
	for i, fi := range fields {
		fmt.Fprintf(&b, "try { $e has %s $old%d; };\n", fi.Tag.Name, i)
	}
	b.WriteString("delete\n")
	for i := range fields {
		fmt.Fprintf(&b, "try { $old%d of $e; };\n", i)
	}
	b.WriteString("insert $e ")
	for i, fi := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "has %s == $v%d", fi.Tag.Name, i)
	}
	b.WriteString(";")
	return b.String()
}
