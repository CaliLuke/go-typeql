package gotype

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// --- Mock transaction and connection ---

type mockTx struct {
	queries   []string
	responses [][]map[string]any
	idx       int
	committed bool
	closed    bool
}

func (m *mockTx) Query(query string) ([]map[string]any, error) {
	m.queries = append(m.queries, query)
	if m.idx < len(m.responses) {
		resp := m.responses[m.idx]
		m.idx++
		return resp, nil
	}
	m.idx++
	return nil, nil
}

func (m *mockTx) QueryWithContext(ctx context.Context, query string) ([]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.Query(query)
}

func (m *mockTx) Commit() error {
	m.committed = true
	return nil
}

func (m *mockTx) Rollback() error { return nil }

func (m *mockTx) Close() {
	m.closed = true
}

func (m *mockTx) IsOpen() bool {
	return !m.committed && !m.closed
}

type mockConn struct {
	txs       []*mockTx
	idx       int
	schemaStr string
}

func (m *mockConn) Transaction(dbName string, txType int) (Tx, error) {
	if m.idx < len(m.txs) {
		tx := m.txs[m.idx]
		m.idx++
		return tx, nil
	}
	return nil, fmt.Errorf("no more mock transactions")
}

func (m *mockConn) Schema(dbName string) (string, error)      { return m.schemaStr, nil }
func (m *mockConn) DatabaseCreate(name string) error           { return nil }
func (m *mockConn) DatabaseDelete(name string) error           { return nil }
func (m *mockConn) DatabaseContains(name string) (bool, error) { return true, nil }
func (m *mockConn) DatabaseAll() ([]string, error)             { return []string{"mock"}, nil }
func (m *mockConn) Close()                                     {}
func (m *mockConn) IsOpen() bool                               { return true }

// --- Tests ---

func TestManager_Insert(t *testing.T) {
	registerTestTypes(t)
	// Mock tx: single query = insert+fetch returns IID
	writeTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": "0xABC123"}}, // insert+fetch returns IID in one query
		},
	}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	err := mgr.Insert(context.Background(), p)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Verify IID was set
	if p.GetIID() != "0xABC123" {
		t.Errorf("expected IID 0xABC123, got %q", p.GetIID())
	}

	// Verify insert query was generated
	if len(writeTx.queries) < 1 {
		t.Fatal("no queries executed")
	}
	insertQ := writeTx.queries[0]
	assertContains(t, insertQ, "insert")
	assertContains(t, insertQ, `has name "Alice"`)
	assertContains(t, insertQ, `has email "alice@example.com"`)

	// Verify transaction was committed
	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_Insert_WrappedIID(t *testing.T) {
	registerTestTypes(t)
	// TypeDB may return IID wrapped in {"value": "0x..."}
	writeTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": map[string]any{"value": "0xDEF456"}}}, // insert+fetch in one query
		},
	}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Bob", Email: "bob@example.com"}
	err := mgr.Insert(context.Background(), p)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if p.GetIID() != "0xDEF456" {
		t.Errorf("expected IID 0xDEF456, got %q", p.GetIID())
	}
}

func TestManager_All(t *testing.T) {
	registerTestTypes(t)
	readTx := &mockTx{
		responses: [][]map[string]any{
			{
				{"_iid": "0x001", "name": "Alice", "email": "alice@example.com", "age": float64(30)},
				{"_iid": "0x002", "name": "Bob", "email": "bob@example.com"},
			},
		},
	}

	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.All(context.Background())
	if err != nil {
		t.Fatalf("All failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify first result
	if results[0].Name != "Alice" {
		t.Errorf("expected Name=Alice, got %q", results[0].Name)
	}
	if results[0].Email != "alice@example.com" {
		t.Errorf("expected Email=alice@example.com, got %q", results[0].Email)
	}
	if results[0].GetIID() != "0x001" {
		t.Errorf("expected IID=0x001, got %q", results[0].GetIID())
	}
	if results[0].Age == nil || *results[0].Age != 30 {
		t.Errorf("expected Age=30, got %v", results[0].Age)
	}

	// Verify query was a match+fetch
	if len(readTx.queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(readTx.queries))
	}
	assertContains(t, readTx.queries[0], "match")
	assertContains(t, readTx.queries[0], "fetch")
}

func TestManager_Get_WithFilters(t *testing.T) {
	registerTestTypes(t)
	readTx := &mockTx{
		responses: [][]map[string]any{
			{
				{"_iid": "0x001", "name": "Alice", "email": "alice@example.com"},
			},
		},
	}

	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Get(context.Background(), map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Alice" {
		t.Errorf("expected Name=Alice, got %q", results[0].Name)
	}

	// Verify the query includes the filter
	assertContains(t, readTx.queries[0], `has name "Alice"`)
}

func TestManager_Update(t *testing.T) {
	registerTestTypes(t)
	// Write tx for update — single batched query
	writeTx := &mockTx{
		responses: [][]map[string]any{
			nil, // batch delete+insert
		},
	}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	age := 31
	p := &testPerson{Name: "Alice", Email: "alice-new@example.com", Age: &age}
	p.SetIID("0xABC123")

	err := mgr.Update(context.Background(), p)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Should have generated 1 batched query with try-delete + insert
	if len(writeTx.queries) != 1 {
		t.Fatalf("expected 1 batched query, got %d", len(writeTx.queries))
	}

	q := writeTx.queries[0]
	assertContains(t, q, "0xABC123")
	assertContains(t, q, "try")
	assertContains(t, q, "delete")
	assertContains(t, q, "insert")

	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_Update_NilOptionalDeletesOnly(t *testing.T) {
	registerTestTypes(t)
	// When a pointer field is nil, Update should emit a delete query
	// but NOT an insert query for that attribute.
	writeTx := &mockTx{}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	// Age is nil — should trigger delete-old but skip insert-new.
	p := &testPerson{Name: "Alice", Email: "alice@example.com", Age: nil}
	p.SetIID("0xABC123")

	err := mgr.Update(context.Background(), p)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Single batched query: try-delete both attrs, insert only email (age is nil)
	if len(writeTx.queries) != 1 {
		t.Fatalf("expected 1 batched query, got %d:\n%s",
			len(writeTx.queries), strings.Join(writeTx.queries, "\n---\n"))
	}

	q := writeTx.queries[0]
	// Both attributes should have try-delete blocks
	assertContains(t, q, "has email")
	assertContains(t, q, "has age")
	assertContains(t, q, "delete")
	// Only email should be in the insert (age is nil)
	assertContains(t, q, "insert")
	assertContains(t, q, `has email "alice@example.com"`)
	if strings.Contains(q, "insert") && strings.Contains(q, "has age") {
		// age should appear in try-match/delete but NOT in insert
		parts := strings.SplitN(q, "insert", 2)
		if len(parts) == 2 && strings.Contains(parts[1], "has age") {
			t.Error("expected NO insert for nil age attribute")
		}
	}

	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_Update_NoIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	err := mgr.Update(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for Update without IID")
	}
	assertContains(t, err.Error(), "no IID")
}

func TestManager_Delete(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{responses: [][]map[string]any{nil}}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	p.SetIID("0xABC123")

	err := mgr.Delete(context.Background(), p)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if len(writeTx.queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(writeTx.queries))
	}
	assertContains(t, writeTx.queries[0], "0xABC123")
	assertContains(t, writeTx.queries[0], "delete $e;")
}

func TestManager_Delete_NoIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	err := mgr.Delete(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for Delete without IID")
	}
	assertContains(t, err.Error(), "no IID")
}

func TestExtractIID(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{"direct string", map[string]any{"_iid": "0x123"}, "0x123"},
		{"wrapped value", map[string]any{"_iid": map[string]any{"value": "0x456"}}, "0x456"},
		{"missing", map[string]any{"name": "Alice"}, ""},
		{"nil value", map[string]any{"_iid": nil}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractIID(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestUnwrapResult(t *testing.T) {
	input := map[string]any{
		"_iid":  map[string]any{"value": "0x123"},
		"name":  map[string]any{"value": "Alice", "type": map[string]any{"label": "name"}},
		"email": "direct@example.com",
		"age":   float64(30),
	}

	flat := unwrapResult(input)

	if flat["_iid"] != "0x123" {
		t.Errorf("expected _iid=0x123, got %v", flat["_iid"])
	}
	if flat["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", flat["name"])
	}
	if flat["email"] != "direct@example.com" {
		t.Errorf("expected email=direct@example.com, got %v", flat["email"])
	}
	if flat["age"] != float64(30) {
		t.Errorf("expected age=30, got %v", flat["age"])
	}
}

func TestManager_DeleteMany(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p1 := &testPerson{Name: "Alice", Email: "a@example.com"}
	p1.SetIID("0x001")
	p2 := &testPerson{Name: "Bob", Email: "b@example.com"}
	p2.SetIID("0x002")

	err := mgr.DeleteMany(context.Background(), []*testPerson{p1, p2})
	if err != nil {
		t.Fatalf("DeleteMany failed: %v", err)
	}

	if len(writeTx.queries) != 2 {
		t.Fatalf("expected 2 delete queries, got %d", len(writeTx.queries))
	}
	assertContains(t, writeTx.queries[0], "0x001")
	assertContains(t, writeTx.queries[0], "delete $e;")
	assertContains(t, writeTx.queries[1], "0x002")
	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_DeleteMany_Empty(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.DeleteMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("DeleteMany with empty slice should succeed, got: %v", err)
	}
}

func TestManager_DeleteMany_NoIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	err := mgr.DeleteMany(context.Background(), []*testPerson{p})
	if err == nil {
		t.Fatal("expected error for DeleteMany without IID")
	}
	assertContains(t, err.Error(), "no IID")
}

func TestManager_UpdateMany(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p1 := &testPerson{Name: "Alice", Email: "new-a@example.com"}
	p1.SetIID("0x001")
	p2 := &testPerson{Name: "Bob", Email: "new-b@example.com"}
	p2.SetIID("0x002")

	err := mgr.UpdateMany(context.Background(), []*testPerson{p1, p2})
	if err != nil {
		t.Fatalf("UpdateMany failed: %v", err)
	}

	// Each person has 1 batched update query → 2 queries total for 2 persons
	if len(writeTx.queries) != 2 {
		t.Fatalf("expected 2 batched queries (1 per person), got %d", len(writeTx.queries))
	}
	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_UpdateMany_Empty(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.UpdateMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("UpdateMany with empty slice should succeed, got: %v", err)
	}
}

func TestManager_UpdateMany_NoIID(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	err := mgr.UpdateMany(context.Background(), []*testPerson{p})
	if err == nil {
		t.Fatal("expected error for UpdateMany without IID")
	}
	assertContains(t, err.Error(), "no IID")
}

func TestManager_Delete_Strict_NotFound(t *testing.T) {
	registerTestTypes(t)
	// Read tx for strict check returns count 0
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(0)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x001")

	err := mgr.Delete(context.Background(), p, WithStrict())
	if err == nil {
		t.Fatal("expected error for strict delete of non-existent instance")
	}
	assertContains(t, err.Error(), "not found")
}

func TestManager_Delete_Strict_Found(t *testing.T) {
	registerTestTypes(t)
	// Read tx for strict check returns count 1
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(1)}},
		},
	}
	// Write tx for the actual delete
	writeTx := &mockTx{}

	conn := &mockConn{txs: []*mockTx{readTx, writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x001")

	err := mgr.Delete(context.Background(), p, WithStrict())
	if err != nil {
		t.Fatalf("Delete strict (found) failed: %v", err)
	}
}

func TestManager_Delete_BackwardCompat(t *testing.T) {
	registerTestTypes(t)
	// Verify the old Delete(ctx, instance) signature still works
	writeTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x001")

	err := mgr.Delete(context.Background(), p) // no opts — backward compatible
	if err != nil {
		t.Fatalf("Delete (backward compat) failed: %v", err)
	}
}

func TestManager_DeleteMany_Strict(t *testing.T) {
	registerTestTypes(t)
	// First instance found, second not found
	readTx1 := &mockTx{responses: [][]map[string]any{{{"count": float64(1)}}}}
	readTx2 := &mockTx{responses: [][]map[string]any{{{"count": float64(0)}}}}
	conn := &mockConn{txs: []*mockTx{readTx1, readTx2}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p1 := &testPerson{Name: "Alice", Email: "a@example.com"}
	p1.SetIID("0x001")
	p2 := &testPerson{Name: "Bob", Email: "b@example.com"}
	p2.SetIID("0x002")

	err := mgr.DeleteMany(context.Background(), []*testPerson{p1, p2}, WithStrict())
	if err == nil {
		t.Fatal("expected error for strict DeleteMany with missing instance")
	}
	assertContains(t, err.Error(), "not found")
}

func TestManager_Put(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{
		responses: [][]map[string]any{
			nil,                          // put query
			{{"_iid": "0xPUT_IID_001"}}, // IID fetch
		},
	}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	err := mgr.Put(context.Background(), p)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Verify put keyword used
	if len(writeTx.queries) < 1 {
		t.Fatal("no queries executed")
	}
	assertContains(t, writeTx.queries[0], "put")
	assertNotContains(t, writeTx.queries[0], "insert")
}

func TestManager_Put_SetsIID(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{
		responses: [][]map[string]any{
			nil,
			{{"_iid": "0xPUT123"}},
		},
	}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p := &testPerson{Name: "Alice", Email: "alice@example.com"}
	_ = mgr.Put(context.Background(), p)

	if p.GetIID() != "0xPUT123" {
		t.Errorf("expected IID 0xPUT123, got %q", p.GetIID())
	}
}

func TestManager_PutMany(t *testing.T) {
	registerTestTypes(t)
	writeTx := &mockTx{}
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": "0xP1"}},
			{{"_iid": "0xP2"}},
		},
	}
	conn := &mockConn{txs: []*mockTx{writeTx, readTx, readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	p1 := &testPerson{Name: "Alice", Email: "a@example.com"}
	p2 := &testPerson{Name: "Bob", Email: "b@example.com"}

	err := mgr.PutMany(context.Background(), []*testPerson{p1, p2})
	if err != nil {
		t.Fatalf("PutMany failed: %v", err)
	}

	if len(writeTx.queries) != 2 {
		t.Fatalf("expected 2 put queries, got %d", len(writeTx.queries))
	}
	assertContains(t, writeTx.queries[0], "put")
	assertContains(t, writeTx.queries[1], "put")
	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestManager_PutMany_Empty(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.PutMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("PutMany with empty slice should succeed, got: %v", err)
	}
}

func TestNewManager_Panics_Unregistered(t *testing.T) {
	type unregistered struct {
		BaseEntity
		X string `typedb:"x"`
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unregistered type")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "not registered") {
			t.Errorf("unexpected panic message: %s", msg)
		}
	}()

	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	NewManager[unregistered](db)
}

func TestNewManagerWithTx(t *testing.T) {
	registerTestTypes(t)

	writeTx := &mockTx{
		responses: [][]map[string]any{
			nil,                                                                     // insert
			{{"_iid": map[string]any{"value": "0x999"}}},                            // iid fetch
		},
	}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")

	tc, err := db.Begin(WriteTransaction)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tc.Close()

	mgr := NewManagerWithTx[testPerson](tc)

	err = mgr.Insert(context.Background(), &testPerson{Name: "TxAlice", Email: "tx@example.com"})
	if err != nil {
		t.Fatalf("Insert in tx: %v", err)
	}

	// The insert should NOT have auto-committed
	if writeTx.committed {
		t.Error("expected transaction NOT to be auto-committed when using NewManagerWithTx")
	}

	// Now commit manually
	if err := tc.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !writeTx.committed {
		t.Error("expected transaction to be committed after Commit()")
	}
}

func TestManager_GetByIIDPolymorphic(t *testing.T) {
	registerTestTypes(t)

	// Single query fetches type label + attributes
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": "0xABC", "_type": "test-person", "name": "Alice", "email": "alice@example.com"}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, typeLabel, err := mgr.GetByIIDPolymorphic(context.Background(), "0xABC")
	if err != nil {
		t.Fatalf("GetByIIDPolymorphic failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Name != "Alice" {
		t.Errorf("expected Alice, got %q", result.Name)
	}
	if typeLabel != "test-person" {
		t.Errorf("expected typeLabel 'test-person', got %q", typeLabel)
	}

	// Single query fetches type label + attributes
	assertContains(t, readTx.queries[0], "isa! $t")
	assertContains(t, readTx.queries[0], "$t sub test-person")
	assertContains(t, readTx.queries[0], "label($t)")
}

func TestManager_GetByIIDPolymorphic_NotFound(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, typeLabel, err := mgr.GetByIIDPolymorphic(context.Background(), "0xDEAD")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
	if typeLabel != "" {
		t.Errorf("expected empty typeLabel, got %q", typeLabel)
	}
}

func TestManager_GetByIIDPolymorphicAny(t *testing.T) {
	registerTestTypes(t)

	// Single query fetches type label + attributes
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": "0xABC", "_type": "test-person", "name": "Alice", "email": "alice@example.com"}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, typeLabel, err := mgr.GetByIIDPolymorphicAny(context.Background(), "0xABC")
	if err != nil {
		t.Fatalf("GetByIIDPolymorphicAny failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if typeLabel != "test-person" {
		t.Errorf("expected typeLabel 'test-person', got %q", typeLabel)
	}

	person, ok := result.(*testPerson)
	if !ok {
		t.Fatalf("expected *testPerson, got %T", result)
	}
	if person.Name != "Alice" {
		t.Errorf("expected Alice, got %q", person.Name)
	}
}

func TestTransactionContext_Rollback(t *testing.T) {
	registerTestTypes(t)

	writeTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")

	tc, err := db.Begin(WriteTransaction)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := tc.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	tc.Close()
}

// --- Nil pointer safety tests ---

func TestManager_Insert_NilInstance(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.Insert(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil instance")
	}
	assertContains(t, err.Error(), "must not be nil")
}

func TestManager_Update_NilInstance(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.Update(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil instance")
	}
	assertContains(t, err.Error(), "must not be nil")
}

func TestManager_Delete_NilInstance(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.Delete(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil instance")
	}
	assertContains(t, err.Error(), "must not be nil")
}

func TestManager_Put_NilInstance(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.Put(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil instance")
	}
	assertContains(t, err.Error(), "must not be nil")
}

func TestManager_DeleteMany_NilElement(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.DeleteMany(context.Background(), []*testPerson{nil})
	if err == nil {
		t.Fatal("expected error for nil element in slice")
	}
	assertContains(t, err.Error(), "must not be nil")
}

func TestManager_UpdateMany_NilElement(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	err := mgr.UpdateMany(context.Background(), []*testPerson{nil})
	if err == nil {
		t.Fatal("expected error for nil element in slice")
	}
	assertContains(t, err.Error(), "must not be nil")
}

// --- Context cancellation tests ---

func TestManager_Insert_CancelledContext(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := mgr.Insert(ctx, &testPerson{Name: "Alice", Email: "a@example.com"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	assertContains(t, err.Error(), "context cancelled")
}

func TestManager_Update_CancelledContext(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x001")
	err := mgr.Update(ctx, p)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	assertContains(t, err.Error(), "context cancelled")
}

func TestManager_Delete_CancelledContext(t *testing.T) {
	registerTestTypes(t)
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &testPerson{Name: "Alice", Email: "a@example.com"}
	p.SetIID("0x001")
	err := mgr.Delete(ctx, p)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	assertContains(t, err.Error(), "context cancelled")
}

func TestDatabase_ExecuteRead_CancelledContext(t *testing.T) {
	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.ExecuteRead(ctx, "match $x isa thing;")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	assertContains(t, err.Error(), "context cancelled")
}

// --- EnsureDatabase tests ---

type ensureDBMockConn struct {
	mockConn
	exists  bool
	created bool
}

func (m *ensureDBMockConn) DatabaseContains(name string) (bool, error) { return m.exists, nil }
func (m *ensureDBMockConn) DatabaseCreate(name string) error {
	m.created = true
	return nil
}

func TestEnsureDatabase_Creates(t *testing.T) {
	conn := &ensureDBMockConn{exists: false}
	created, err := EnsureDatabase(context.Background(), conn, "newdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true")
	}
	if !conn.created {
		t.Error("expected DatabaseCreate to be called")
	}
}

func TestEnsureDatabase_AlreadyExists(t *testing.T) {
	conn := &ensureDBMockConn{exists: true}
	created, err := EnsureDatabase(context.Background(), conn, "existingdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Error("expected created=false")
	}
	if conn.created {
		t.Error("expected DatabaseCreate NOT to be called")
	}
}

func TestEnsureDatabase_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &ensureDBMockConn{}
	_, err := EnsureDatabase(ctx, conn, "db")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
