package ast

import (
	"strings"
	"testing"
)

type alwaysIIDMatcher struct{}

func (alwaysIIDMatcher) IsIID(string) bool { return true }

func TestFluentMatch_SetFetchBuild(t *testing.T) {
	query, err := FluentMatch("n", "user_story").
		Has("display_id", "US-1").
		Set("status", "done").
		Fetch("n", "name", "status").
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}

	if !strings.Contains(query, `$n isa user_story, has display_id "US-1"`) {
		t.Fatalf("expected match clause with identifier, got:\n%s", query)
	}
	if !strings.Contains(query, "delete\n$old_status of $n;") {
		t.Fatalf("expected delete clause, got:\n%s", query)
	}
	if !strings.Contains(query, `insert
$n has status "done";`) {
		t.Fatalf("expected insert clause, got:\n%s", query)
	}
	if !strings.Contains(query, `"name": $n.name`) || !strings.Contains(query, `"status": $n.status`) {
		t.Fatalf("expected fetch fields, got:\n%s", query)
	}
}

func TestMatchFunction_SelectBuild(t *testing.T) {
	query, err := MatchFunction("get_edges_for_node", "$target").
		Select("$did_t", "$rel_label").
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}

	if !strings.Contains(query, "let $result = get_edges_for_node($target);") {
		t.Fatalf("expected match-let function call, got:\n%s", query)
	}
	if !strings.Contains(query, "select $did_t, $rel_label;") {
		t.Fatalf("expected select clause, got:\n%s", query)
	}
}

func TestUpdateAttributeTemplate(t *testing.T) {
	query, err := UpdateAttribute("n", "user_story", "status", "done")
	if err != nil {
		t.Fatalf("update template error: %v", err)
	}

	if !strings.Contains(query, "$n isa user_story") {
		t.Fatalf("expected entity match, got:\n%s", query)
	}
	if !strings.Contains(query, "$old_status of $n") {
		t.Fatalf("expected delete-old statement, got:\n%s", query)
	}
	if !strings.Contains(query, `$n has status "done"`) {
		t.Fatalf("expected insert-new statement, got:\n%s", query)
	}
}

func TestDeleteArtifactTemplate(t *testing.T) {
	iidQuery, err := DeleteArtifact("0x123", "user_story")
	if err != nil {
		t.Fatalf("delete template error: %v", err)
	}
	if !strings.Contains(iidQuery, "$n isa user_story, iid 0x123") {
		t.Fatalf("expected iid matching, got:\n%s", iidQuery)
	}
	if !strings.Contains(iidQuery, "delete\n$n;") {
		t.Fatalf("expected delete thing statement, got:\n%s", iidQuery)
	}

	displayIDQuery, err := DeleteArtifact("US-1", "user_story")
	if err != nil {
		t.Fatalf("delete template error: %v", err)
	}
	if !strings.Contains(displayIDQuery, `$n isa user_story, has display_id "US-1"`) {
		t.Fatalf("expected display_id matching, got:\n%s", displayIDQuery)
	}
}

func TestDeleteArtifactWithOptions(t *testing.T) {
	query, err := DeleteArtifactWithOptions("ticket-1", "task", DeleteArtifactOptions{
		VarName: "t",
		IDAttr:  "external_id",
		Matcher: alwaysIIDMatcher{},
	})
	if err != nil {
		t.Fatalf("delete template error: %v", err)
	}
	if !strings.Contains(query, "$t isa task, iid ticket-1") {
		t.Fatalf("expected custom matcher iid path, got:\n%s", query)
	}
}

func TestPaginatedSearchTemplate(t *testing.T) {
	query, err := PaginatedSearch([]string{"user_story", "task"}, PaginatedSearchOptions{
		Limit:  25,
		Sort:   "-name",
		Offset: 10,
	})
	if err != nil {
		t.Fatalf("paginated search error: %v", err)
	}

	if !strings.Contains(query, "{ $n isa user_story; } or { $n isa task; }") {
		t.Fatalf("expected type alternatives, got:\n%s", query)
	}
	if !strings.Contains(query, "sort $n.name desc;") {
		t.Fatalf("expected descending sort, got:\n%s", query)
	}
	if !strings.Contains(query, "offset 10;") {
		t.Fatalf("expected offset clause, got:\n%s", query)
	}
	if !strings.Contains(query, "limit 25;") {
		t.Fatalf("expected limit clause, got:\n%s", query)
	}
	if !strings.Contains(query, `"_iid": iid($n)`) {
		t.Fatalf("expected iid fetch, got:\n%s", query)
	}
}

func TestFluentMatchByIdentifier(t *testing.T) {
	q, err := FluentMatch("n", "node").
		MatchByIdentifier("display-42", "display_id", nil).
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	if !strings.Contains(q, `$n isa node, has display_id "display-42"`) {
		t.Fatalf("expected attribute fallback, got:\n%s", q)
	}

	iidQ, err := FluentMatch("n", "node").
		MatchByIdentifier("0xabc", "display_id", nil).
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	if !strings.Contains(iidQ, "$n isa node, iid 0xabc") {
		t.Fatalf("expected iid path, got:\n%s", iidQ)
	}
}

func TestFluentBuilders_Immutable(t *testing.T) {
	base := FluentMatch("n", "task")
	changed := base.Has("status", "done")

	baseQuery, err := base.Build()
	if err != nil {
		t.Fatalf("base build error: %v", err)
	}
	changedQuery, err := changed.Build()
	if err != nil {
		t.Fatalf("changed build error: %v", err)
	}

	if strings.Contains(baseQuery, "status") {
		t.Fatalf("base query was unexpectedly mutated: %s", baseQuery)
	}
	if !strings.Contains(changedQuery, `has status "done"`) {
		t.Fatalf("changed query missing constraint: %s", changedQuery)
	}
}

func TestFluentBuilders_Nodes(t *testing.T) {
	nodes := FluentMatch("n", "task").Has("name", "A").Select("n").Limit(5).Nodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes (match + select + limit), got %d", len(nodes))
	}
	if _, ok := nodes[0].(MatchClause); !ok {
		t.Fatalf("first node should be MatchClause, got %T", nodes[0])
	}
	if _, ok := nodes[1].(SelectClause); !ok {
		t.Fatalf("second node should be SelectClause, got %T", nodes[1])
	}
	if _, ok := nodes[2].(LimitClause); !ok {
		t.Fatalf("third node should be LimitClause, got %T", nodes[2])
	}
}
