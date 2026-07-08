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
	if !strings.Contains(query, "$n has name $sort_name;") {
		t.Fatalf("expected sort attribute binding, got:\n%s", query)
	}
	if !strings.Contains(query, "sort $sort_name desc;") {
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

// Regression for issue #21: clone() copied the matchPatterns slice but the
// first pattern's Constraints backing array was shared, so two branches forked
// from the same builder could write into the same array slot.
func TestFluentBuilders_SiblingBranchesIndependent(t *testing.T) {
	// Three Has calls leave the shared Constraints slice with spare capacity
	// (len 3, cap 4), so both branch appends target the same array slot.
	base := FluentMatch("p", "person").Has("a", 1).Has("b", 2).Has("c", 3)
	branchA := base.Has("y", 20)
	branchB := base.Has("z", 30)

	queryA, err := branchA.Build()
	if err != nil {
		t.Fatalf("branch A build error: %v", err)
	}
	queryB, err := branchB.Build()
	if err != nil {
		t.Fatalf("branch B build error: %v", err)
	}

	if !strings.Contains(queryA, "has y 20") {
		t.Fatalf("branch A lost its own constraint:\n%s", queryA)
	}
	if strings.Contains(queryA, "has z") {
		t.Fatalf("branch A was corrupted by sibling branch B:\n%s", queryA)
	}
	if !strings.Contains(queryB, "has z 30") {
		t.Fatalf("branch B lost its own constraint:\n%s", queryB)
	}
	if strings.Contains(queryB, "has y") {
		t.Fatalf("branch B was corrupted by sibling branch A:\n%s", queryB)
	}
}

// Regression for issue #22: Has/Iid/MatchByIdentifier used to silently no-op
// when the first match pattern was not an EntityPattern, which could turn a
// targeted delete into a mass delete. They must now surface an error at Build.
func TestFluentConstraintOnNonEntityPattern_ErrorsAtBuild(t *testing.T) {
	relationFirst := func() MatchStage {
		return FluentPatterns(
			Relation("$r", "employment", []RolePlayer{Role("employee", "$p")}),
		)
	}

	t.Run("Has on relation-first builder", func(t *testing.T) {
		_, err := relationFirst().Has("start-date", "2020-01-01").DeleteThing().Build()
		if err == nil {
			t.Fatal("expected build error when Has cannot attach, got nil")
		}
		if !strings.Contains(err.Error(), "EntityPattern") {
			t.Fatalf("expected error to explain the entity-first requirement, got: %v", err)
		}
	})

	t.Run("Iid on relation-first builder", func(t *testing.T) {
		_, err := relationFirst().Iid("0x123").Build()
		if err == nil {
			t.Fatal("expected build error when Iid cannot attach, got nil")
		}
	})

	t.Run("MatchByIdentifier IID arm on relation-first builder", func(t *testing.T) {
		_, err := relationFirst().MatchByIdentifier("0x123", "display_id", nil).Build()
		if err == nil {
			t.Fatal("expected build error when MatchByIdentifier cannot attach, got nil")
		}
	})

	t.Run("Has with no match patterns", func(t *testing.T) {
		_, err := FluentPatterns().Has("name", "x").Build()
		if err == nil {
			t.Fatal("expected build error when there are no match patterns, got nil")
		}
	})

	t.Run("error survives later stages", func(t *testing.T) {
		_, err := relationFirst().Has("start-date", "2020-01-01").Fetch("r", "start-date").Limit(5).Build()
		if err == nil {
			t.Fatal("expected deferred error to survive output-stage transitions, got nil")
		}
	})

	t.Run("valid entity-first builders still build", func(t *testing.T) {
		q, err := FluentPatterns(Entity("$p", "person")).Has("name", "Alice").Build()
		if err != nil {
			t.Fatalf("unexpected build error: %v", err)
		}
		if !strings.Contains(q, `$p isa person, has name "Alice"`) {
			t.Fatalf("expected attached constraint, got:\n%s", q)
		}
	})
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

func TestFluentPatterns_LetStream_SelectBuild(t *testing.T) {
	query, err := FluentPatterns(
		Entity("$target", "artifact", Has("display_id", Str("A-123"))),
	).Let(LetAssignment{
		Variables:  []string{"$iid_t", "$iid_o", "$did_t", "$did_o", "$rel_label", "$role_t", "$role_o"},
		Expression: FuncCall("get_edges_for_node", "$target"),
		IsStream:   true,
	}).
		Select("$iid_t", "$iid_o", "$did_t", "$did_o", "$rel_label", "$role_t", "$role_o").
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}

	if !strings.Contains(query, `$target isa artifact, has display_id "A-123"`) {
		t.Fatalf("expected pattern in match-let clause, got:\n%s", query)
	}
	if !strings.Contains(query, "let $iid_t, $iid_o, $did_t, $did_o, $rel_label, $role_t, $role_o in get_edges_for_node($target);") {
		t.Fatalf("expected stream let assignment, got:\n%s", query)
	}
	if !strings.Contains(query, "select $iid_t, $iid_o, $did_t, $did_o, $rel_label, $role_t, $role_o;") {
		t.Fatalf("expected select clause, got:\n%s", query)
	}
}

func TestFluentPatterns_WhereOrAndExplicitMutationStatements(t *testing.T) {
	query, err := FluentPatterns(
		Entity("$n", "artifact"),
	).Where(
		Relation("$r", "edge", []RolePlayer{
			Role("from", "$n"),
			Role("to", "$other"),
		}),
	).Or(
		[]Pattern{Entity("$other", "task")},
		[]Pattern{Entity("$other", "user-story")},
	).
		DeleteHas("$old_status", "$n").
		InsertHas("$n", "status", "done").
		Build()
	if err != nil {
		t.Fatalf("build error: %v", err)
	}

	if !strings.Contains(query, "$r isa edge (from: $n, to: $other)") {
		t.Fatalf("expected relation pattern in where clause, got:\n%s", query)
	}
	if !strings.Contains(query, "{ $other isa task; } or { $other isa user-story; }") {
		t.Fatalf("expected or alternatives, got:\n%s", query)
	}
	if !strings.Contains(query, "delete\n$old_status of $n;") {
		t.Fatalf("expected explicit delete-has statement, got:\n%s", query)
	}
	if !strings.Contains(query, "insert\n$n has status \"done\";") {
		t.Fatalf("expected explicit insert has statement, got:\n%s", query)
	}
}

func TestFluentBuilders_BuildNodesAlias(t *testing.T) {
	nodes := FluentPatterns(Entity("$n", "task")).Select("n").Limit(2).BuildNodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes (match + select + limit), got %d", len(nodes))
	}
	if _, ok := nodes[0].(MatchClause); !ok {
		t.Fatalf("first node should be MatchClause, got %T", nodes[0])
	}
}
