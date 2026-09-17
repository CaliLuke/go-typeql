package gotype

import (
	"context"
	"strings"
	"testing"
)

func TestProjectedReadDistinguishesOmittedAndMissingFields(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{responses: [][]map[string]any{{{
		"_iid": "0x01", "_type": "test-person", "name": map[string]any{"value": "Alice"}, "age": nil,
	}}}}
	mgr := MustNewManager[testPerson](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
	results, err := mgr.GetProjected(context.Background(), map[string]any{"name": "Alice"}, Projection{Fields: []string{"name", "age"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].IID != "0x01" || results[0].TypeName != "test-person" {
		t.Fatalf("wrong projected identity: %+v", results)
	}
	if got := results[0].Fields["name"]; !got.Present || got.Value != "Alice" {
		t.Fatalf("name = %+v", got)
	}
	if got := results[0].Fields["age"]; got.Present || got.Value != nil {
		t.Fatalf("missing age = %+v", got)
	}
	if _, ok := results[0].Fields["email"]; ok {
		t.Fatal("omitted email appeared in projected result")
	}
	if strings.Contains(tx.queries[0], `"email":`) || !strings.Contains(tx.queries[0], `"_type": label($projection_type)`) {
		t.Fatalf("wrong projection query: %s", tx.queries[0])
	}
	if !tx.closed {
		t.Fatal("internally opened transaction was not closed")
	}
}

func TestProjectedRelationSelectsOnlyRequestedRoleFields(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{responses: [][]map[string]any{{{
		"_iid": "0x10", "_type": "test-employment",
		"employee": map[string]any{"_iid": "0x01", "_type": "test-person", "name": "Alice"},
	}}}}
	mgr := MustNewManager[testEmployment](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
	results, err := mgr.GetProjected(context.Background(), nil, Projection{Roles: map[string][]string{"employee": {"name"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Roles["employee"] == nil {
		t.Fatalf("missing projected role: %+v", results)
	}
	player := results[0].Roles["employee"]
	if player.IID != "0x01" || player.TypeName != "test-person" || player.Fields["name"].Value != "Alice" {
		t.Fatalf("wrong role player: %+v", player)
	}
	if _, ok := player.Fields["email"]; ok {
		t.Fatal("omitted role-player email appeared")
	}
	if strings.Contains(tx.queries[0], `"employer":`) || strings.Contains(tx.queries[0], `"email":`) {
		t.Fatalf("unrequested data in query: %s", tx.queries[0])
	}
}

func TestProjectedFluentReadPreservesBoundTransactionAndPaging(t *testing.T) {
	registerTestTypes(t)
	tx := &mockTx{responses: [][]map[string]any{{{"_iid": "0x02", "_type": "test-person", "name": "Bob"}}}}
	db := NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db")
	scope, err := db.Begin(ReadTransaction)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	mgr := MustNewManagerWithTx[testPerson](scope)
	results, err := mgr.Query().Filter(Eq("name", "Bob")).OrderAsc("name").Offset(1).Limit(2).ExecuteProjected(context.Background(), Projection{Fields: []string{"name"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Fields["name"].Value != "Bob" {
		t.Fatalf("wrong result: %+v", results)
	}
	query := tx.queries[0]
	for _, want := range []string{"sort $e__name asc;", "offset 1;", "limit 2;", `"name": $e.name`} {
		if !strings.Contains(query, want) {
			t.Fatalf("query lacks %q: %s", want, query)
		}
	}
	if tx.closed {
		t.Fatal("bound transaction was closed by projected read")
	}
}

func TestProjectedReadRejectsUnknownAndDuplicateNames(t *testing.T) {
	registerTestTypes(t)
	mgr := MustNewManager[testEmployment](NewDatabase(&mockConn{}, "test_db"))
	for _, spec := range []Projection{
		{Fields: []string{"unknown"}},
		{Fields: []string{"start-date", "start-date"}},
		{Roles: map[string][]string{"missing": nil}},
		{Roles: map[string][]string{"employee": {"unknown"}}},
	} {
		if _, err := mgr.GetProjected(context.Background(), nil, spec); err == nil {
			t.Fatalf("accepted invalid projection: %+v", spec)
		}
	}
}

func TestProjectedReadPreservesConcreteTypeAndSliceValue(t *testing.T) {
	ClearRegistry()
	MustRegister[TestPersonWithTags]()
	t.Cleanup(func() { ClearRegistry() })
	tx := &mockTx{responses: [][]map[string]any{{{
		"_iid": "0x03", "_type": "special-person-with-tags", "nickname": []any{"red", "blue"},
	}}}}
	mgr := MustNewManager[TestPersonWithTags](NewDatabase(&mockConn{txs: []*mockTx{tx}}, "test_db"))
	results, err := mgr.GetProjected(context.Background(), nil, Projection{Fields: []string{"nickname"}})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].TypeName != "special-person-with-tags" || !results[0].Fields["nickname"].Present {
		t.Fatalf("lost concrete type or slice: %+v", results[0])
	}
	values, ok := results[0].Fields["nickname"].Value.([]any)
	if !ok || len(values) != 2 || values[0] != "red" || values[1] != "blue" {
		t.Fatalf("wrong slice value: %#v", results[0].Fields["nickname"].Value)
	}
}
