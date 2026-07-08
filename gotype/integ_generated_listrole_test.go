//go:build integration && cgo && typedb

package gotype_test

// End-to-end coverage of the tqlgen list-role contract: models below are the
// verbatim shape tqlgen renders for the schema in defineListRoleSchema (the
// emission shape itself is pinned by tqlgen's TestRenderCompile_ListRoles).
// The flow mirrors real tqlgen usage — the .tql schema is the source of
// truth on the server, the generated structs are registered for the ORM only.
//
// Live-server facts these tests rely on (verified against TypeDB 3.12.0):
// scalar `links (member: $a, member: $b)` entries insert into a list role
// (`relates member[]`), and scalar `links (member: $m)` matching reads every
// player back — which is exactly what gotype emits and consumes. The
// list-literal insert form (`links (member[]: [$a, $b])`) is NOT accepted by
// 3.12.0, and a scalar role cannot be redefined as a list role after the
// fact, which is why the relation types are defined from the .tql text
// instead of being synced from the registry.

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// LrPerson mirrors tqlgen output for: entity lr-person, owns lr-name @key, plays ...
type LrPerson struct {
	gotype.BaseEntity
	LrName string `typedb:"lr-name,key"`
}

// LrTeam mirrors tqlgen output for: relation lr-team, relates member[], relates leader;
type LrTeam struct {
	gotype.BaseRelation
	Member []*LrPerson `typedb:"role:member"`
	Leader *LrPerson   `typedb:"role:leader"`
}

// LrGuild mirrors tqlgen output for: relation lr-guild, relates participant @card(0..);
type LrGuild struct {
	gotype.BaseRelation
	Participant []*LrPerson `typedb:"role:participant"`
}

// defineListRoleSchema applies the .tql source of truth on top of the synced
// lr-person entity: the relation types with their list / many-cardinality
// roles, and the plays clauses (define is additive).
func defineListRoleSchema(t *testing.T, ctx context.Context, db *gotype.Database) {
	t.Helper()
	schema := `define
relation lr-team, relates member[], relates leader;
relation lr-guild, relates participant @card(0..);
entity lr-person, plays lr-team:member, plays lr-team:leader, plays lr-guild:participant;`
	if err := db.ExecuteSchema(ctx, schema); err != nil {
		t.Fatalf("defining list-role schema: %v", err)
	}
}

func linkedNames(t *testing.T, ctx context.Context, db *gotype.Database, query string) []string {
	t.Helper()
	rows, err := db.ExecuteRead(ctx, query)
	if err != nil {
		t.Fatalf("reading linked players: %v", err)
	}
	var names []string
	for _, row := range rows {
		if name, ok := row["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// TestIntegration_GeneratedListRole exercises a true TypeQL list role
// (relates member[]) through the generated []*Player field shape: insert a
// relation with several players of the list role plus a scalar role, and
// verify every player is linked.
func TestIntegration_GeneratedListRole(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[LrPerson]()
	})
	ctx := context.Background()
	defineListRoleSchema(t, ctx, db)

	// Registration is ORM-side only; the relation types already exist on the
	// server in their list-role form.
	if err := gotype.Register[LrTeam](); err != nil {
		t.Fatalf("registering LrTeam: %v", err)
	}

	personMgr := gotype.MustNewManager[LrPerson](db)
	ada := &LrPerson{LrName: "Ada"}
	brin := &LrPerson{LrName: "Brin"}
	cody := &LrPerson{LrName: "Cody"}
	for _, p := range []*LrPerson{ada, brin, cody} {
		assertInsert(t, ctx, personMgr, p)
	}

	teamMgr := gotype.MustNewManager[LrTeam](db)
	team := &LrTeam{Member: []*LrPerson{ada, brin}, Leader: cody}
	if err := teamMgr.Insert(ctx, team); err != nil {
		t.Fatalf("inserting team with list-role players: %v", err)
	}

	members := linkedNames(t, ctx, db,
		"match\n$r isa lr-team, links (member: $p);\n$p has lr-name $n;\nfetch { \"name\": $n };")
	assertSameStringSet(t, "list-role members", []string{"Ada", "Brin"}, members)

	leaders := linkedNames(t, ctx, db,
		"match\n$r isa lr-team, links (leader: $p);\n$p has lr-name $n;\nfetch { \"name\": $n };")
	assertSameStringSet(t, "scalar-role leader", []string{"Cody"}, leaders)
}

// TestIntegration_GeneratedManyCardRole exercises the @card(0..) variant of
// the generated slice-role shape against its real schema definition.
func TestIntegration_GeneratedManyCardRole(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[LrPerson]()
	})
	ctx := context.Background()
	defineListRoleSchema(t, ctx, db)

	if err := gotype.Register[LrGuild](); err != nil {
		t.Fatalf("registering LrGuild: %v", err)
	}

	personMgr := gotype.MustNewManager[LrPerson](db)
	dara := &LrPerson{LrName: "Dara"}
	eryn := &LrPerson{LrName: "Eryn"}
	fionn := &LrPerson{LrName: "Fionn"}
	for _, p := range []*LrPerson{dara, eryn, fionn} {
		assertInsert(t, ctx, personMgr, p)
	}

	guildMgr := gotype.MustNewManager[LrGuild](db)
	guild := &LrGuild{Participant: []*LrPerson{dara, eryn, fionn}}
	if err := guildMgr.Insert(ctx, guild); err != nil {
		t.Fatalf("inserting guild with many-card role players: %v", err)
	}

	participants := linkedNames(t, ctx, db,
		"match\n$r isa lr-guild, links (participant: $p);\n$p has lr-name $n;\nfetch { \"name\": $n };")
	assertSameStringSet(t, "many-card participants", []string{"Dara", "Eryn", "Fionn"}, participants)
}
