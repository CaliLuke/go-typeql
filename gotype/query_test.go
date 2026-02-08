package gotype

import (
	"context"
	"strings"
	"testing"
)

func TestQuery_Execute(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{
				{"_iid": "0x001", "name": "Alice", "email": "alice@example.com", "age": float64(30)},
			},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().
		Filter(Eq("name", "Alice")).
		Execute(context.Background())

	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Alice" {
		t.Errorf("expected Alice, got %q", results[0].Name)
	}

	// Verify query structure
	q := readTx.queries[0]
	assertContains(t, q, "match")
	assertContains(t, q, "$e isa test-person;")
	assertContains(t, q, "$e has name $e__name;")
	assertContains(t, q, `$e__name == "Alice";`)
	assertContains(t, q, "fetch")
}

func TestQuery_MultipleFilters(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		Filter(Gt("age", 25), Lt("age", 50)).
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "$e has age $e__age;")
	assertContains(t, q, "$e__age > 25;")
	assertContains(t, q, "$e__age < 50;")
}

func TestQuery_OrFilter(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		Filter(Or(Eq("name", "Alice"), Eq("name", "Bob"))).
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "or")
}

func TestQuery_Limit(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		Limit(10).
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "limit 10;")
}

func TestQuery_Offset(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		Offset(5).
		Limit(10).
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "offset 5;")
	assertContains(t, q, "limit 10;")
	// Verify offset comes before limit
	offsetIdx := strings.Index(q, "offset")
	limitIdx := strings.Index(q, "limit")
	if offsetIdx >= limitIdx {
		t.Error("offset should come before limit")
	}
}

func TestQuery_OrderBy(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		OrderAsc("name").
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "sort $e__name asc;")
	assertContains(t, q, "$e has name $e__name;")
}

func TestQuery_OrderByDesc(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, _ = mgr.Query().
		OrderDesc("age").
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "sort $e__age desc;")
}

func TestQuery_First(t *testing.T) {
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

	result, err := mgr.Query().First(context.Background())
	if err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result, got nil")
	}
	if result.Name != "Alice" {
		t.Errorf("expected Alice, got %q", result.Name)
	}
	assertContains(t, readTx.queries[0], "limit 1;")
}

func TestQuery_First_NoResults(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, err := mgr.Query().
		Filter(Eq("name", "NonExistent")).
		First(context.Background())
	if err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestQuery_Count(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(42)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	count, err := mgr.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 42 {
		t.Errorf("expected 42, got %d", count)
	}
	assertContains(t, readTx.queries[0], "reduce $count = count($e);")
}

func TestQuery_Count_WithFilter(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(5)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	count, err := mgr.Query().
		Filter(Gt("age", 25)).
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}

	q := readTx.queries[0]
	assertContains(t, q, "$e has age $e__age;")
	assertContains(t, q, "$e__age > 25;")
	assertContains(t, q, "reduce $count = count($e);")
}

func TestQuery_Delete(t *testing.T) {
	registerTestTypes(t)

	writeTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, err := mgr.Query().
		Filter(Eq("name", "Alice")).
		Delete(context.Background())
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	q := writeTx.queries[0]
	assertContains(t, q, "match")
	assertContains(t, q, `"Alice"`)
	assertContains(t, q, "delete $e;")
}

func TestQuery_Sum(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result": float64(150)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	sum, err := mgr.Query().Sum("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Sum failed: %v", err)
	}
	if sum != 150 {
		t.Errorf("expected 150, got %f", sum)
	}
	assertContains(t, readTx.queries[0], "reduce $result = sum($e__age);")
}

func TestQuery_Avg(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result": float64(30.5)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	avg, err := mgr.Query().Avg("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Avg failed: %v", err)
	}
	if avg != 30.5 {
		t.Errorf("expected 30.5, got %f", avg)
	}
	assertContains(t, readTx.queries[0], "reduce $result = mean($e__age);")
}

func TestQuery_MinMax(t *testing.T) {
	registerTestTypes(t)

	minTx := &mockTx{responses: [][]map[string]any{{{"result": float64(20)}}}}
	maxTx := &mockTx{responses: [][]map[string]any{{{"result": float64(50)}}}}
	conn := &mockConn{txs: []*mockTx{minTx, maxTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	minVal, err := mgr.Query().Min("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Min failed: %v", err)
	}
	if minVal != 20 {
		t.Errorf("expected 20, got %f", minVal)
	}

	maxVal, err := mgr.Query().Max("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Max failed: %v", err)
	}
	if maxVal != 50 {
		t.Errorf("expected 50, got %f", maxVal)
	}
}

func TestQuery_UpdateWith(t *testing.T) {
	registerTestTypes(t)

	// Single write tx for both fetch and updates (atomic)
	writeTx := &mockTx{
		responses: [][]map[string]any{
			// First query: the fetch query returns results
			{
				{"_iid": "0x001", "name": "Alice", "email": "old-a@example.com"},
				{"_iid": "0x002", "name": "Bob", "email": "old-b@example.com"},
			},
			// Subsequent queries: delete/insert for updates (return nil)
		},
	}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().UpdateWith(context.Background(), func(p *testPerson) {
		p.Email = "updated@example.com"
	})
	if err != nil {
		t.Fatalf("UpdateWith failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify the function was applied
	for _, r := range results {
		if r.Email != "updated@example.com" {
			t.Errorf("expected email=updated@example.com, got %q", r.Email)
		}
	}

	// Verify single write tx was committed
	if !writeTx.committed {
		t.Error("write transaction was not committed")
	}
}

func TestQuery_UpdateWith_NoResults(t *testing.T) {
	registerTestTypes(t)

	// Uses a write tx now (fetch happens inside write tx)
	writeTx := &mockTx{
		responses: [][]map[string]any{nil},
	}

	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().UpdateWith(context.Background(), func(p *testPerson) {
		p.Email = "should-not-matter"
	})
	if err != nil {
		t.Fatalf("UpdateWith with no results should not error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
}

func TestQuery_Update_BulkMap(t *testing.T) {
	registerTestTypes(t)

	writeTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	_, err := mgr.Query().Update(context.Background(), map[string]any{
		"email": "bulk@example.com",
	})
	if err != nil {
		t.Fatalf("Update bulk failed: %v", err)
	}

	// Should have a single batched query with try-delete + insert
	if len(writeTx.queries) != 1 {
		t.Fatalf("expected 1 batched query, got %d:\n%s",
			len(writeTx.queries), strings.Join(writeTx.queries, "\n---\n"))
	}

	q := writeTx.queries[0]
	assertContains(t, q, "delete")
	assertContains(t, q, "try")
	assertContains(t, q, "insert")
	assertContains(t, q, `"bulk@example.com"`)

	if !writeTx.committed {
		t.Error("transaction was not committed")
	}
}

func TestQuery_Update_EmptyMap(t *testing.T) {
	registerTestTypes(t)

	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	count, err := mgr.Query().Update(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Update with empty map should succeed, got: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0 for empty update, got %d", count)
	}
}

func TestQuery_Exists_True(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(3)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	exists, err := mgr.Query().Filter(Eq("name", "Alice")).Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("expected exists=true, got false")
	}
}

func TestQuery_Exists_False(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"count": float64(0)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	exists, err := mgr.Query().Filter(Eq("name", "Nobody")).Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected exists=false, got true")
	}
}

func TestQuery_All(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{
				{"_iid": "0x001", "name": "Alice", "email": "alice@example.com"},
				{"_iid": "0x002", "name": "Bob", "email": "bob@example.com"},
			},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().All(context.Background())
	if err != nil {
		t.Fatalf("All failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestQuery_Median(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result": float64(30)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	val, err := mgr.Query().Median("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Median failed: %v", err)
	}
	if val != 30 {
		t.Errorf("expected 30, got %f", val)
	}
	assertContains(t, readTx.queries[0], "reduce $result = median($e__age);")
}

func TestQuery_Std(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result": float64(5.5)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	val, err := mgr.Query().Std("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Std failed: %v", err)
	}
	if val != 5.5 {
		t.Errorf("expected 5.5, got %f", val)
	}
	assertContains(t, readTx.queries[0], "reduce $result = std($e__age);")
}

func TestQuery_Variance(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result": float64(30.25)}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	val, err := mgr.Query().Variance("age").Execute(context.Background())
	if err != nil {
		t.Fatalf("Variance failed: %v", err)
	}
	if val != 30.25 {
		t.Errorf("expected 30.25, got %f", val)
	}
	assertContains(t, readTx.queries[0], "reduce $result = variance($e__age);")
}

func TestQuery_Aggregate_Multi(t *testing.T) {
	registerTestTypes(t)

	// Single query with multiple reduce assignments
	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"result0": float64(150), "result1": float64(30)}}, // reduce returns both values
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().Aggregate(context.Background(),
		AggregateSpec{Attr: "age", Fn: "sum"},
		AggregateSpec{Attr: "age", Fn: "mean"},
	)
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}
	if results["sum_age"] != 150 {
		t.Errorf("expected sum_age=150, got %f", results["sum_age"])
	}
	if results["mean_age"] != 30 {
		t.Errorf("expected mean_age=30, got %f", results["mean_age"])
	}

	// Verify single query with multiple reduce assignments
	if len(readTx.queries) != 1 {
		t.Errorf("expected 1 query, got %d", len(readTx.queries))
	}
	assertContains(t, readTx.queries[0], "reduce $result0 = sum($e__age), $result1 = mean($e__age);")
}

func TestQuery_Aggregate_Empty(t *testing.T) {
	registerTestTypes(t)

	conn := &mockConn{}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().Aggregate(context.Background())
	if err != nil {
		t.Fatalf("Aggregate with no specs should not error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
}

func TestQuery_GroupBy(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{
				{"name": "Engineering", "sum_age": float64(120)},
				{"name": "Marketing", "sum_age": float64(90)},
			},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	results, err := mgr.Query().GroupBy("name").Aggregate(context.Background(),
		AggregateSpec{Attr: "age", Fn: "sum"},
	)
	if err != nil {
		t.Fatalf("GroupBy.Aggregate failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(results))
	}

	q := readTx.queries[0]
	assertContains(t, q, "group $e__name")
	assertContains(t, q, "sum($e__age)")
}

func TestManager_GetByIID(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{
		responses: [][]map[string]any{
			{{"_iid": "0xABC", "name": "Alice", "email": "alice@example.com"}},
		},
	}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, err := mgr.GetByIID(context.Background(), "0xABC")
	if err != nil {
		t.Fatalf("GetByIID failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Name != "Alice" {
		t.Errorf("expected Alice, got %q", result.Name)
	}

	q := readTx.queries[0]
	assertContains(t, q, "iid 0xABC")
	assertContains(t, q, "fetch")
}

func TestManager_GetByIID_NotFound(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	result, err := mgr.GetByIID(context.Background(), "0xDEAD")
	if err != nil {
		t.Fatalf("GetByIID not found should not error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestQuery_Chaining(t *testing.T) {
	registerTestTypes(t)

	readTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{readTx}}
	db := NewDatabase(conn, "test_db")
	mgr := NewManager[testPerson](db)

	// Test full chaining
	_, _ = mgr.Query().
		Filter(Gt("age", 18)).
		Filter(Contains("email", "@example.com")).
		OrderAsc("name").
		Limit(10).
		Offset(5).
		Execute(context.Background())

	q := readTx.queries[0]
	assertContains(t, q, "match")
	assertContains(t, q, "$e__age > 18;")
	assertContains(t, q, "contains")
	assertContains(t, q, "sort")
	assertContains(t, q, "offset 5;")
	assertContains(t, q, "limit 10;")
	assertContains(t, q, "fetch")
}
