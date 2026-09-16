package tqlgen

import (
	"bytes"
	"reflect"
	"testing"
)

func TestIndexedRoleResolutionMatchesScan(t *testing.T) {
	schema, _ := roleLookupFixture(64, 16, 4)
	root := schema.Relations[0].Name
	// The first entity wins over a later entity and a relation player.
	schema.Entities[0].Plays = append(schema.Entities[0].Plays, PlaysSpec{Relation: root, Role: "target-1"})
	schema.Relations[0].Plays = append(schema.Relations[0].Plays, PlaysSpec{Relation: root, Role: "target-1"})
	// A relation player is used when no entity plays the role.
	schema.Entities[63].Plays = schema.Entities[63].Plays[:len(schema.Entities[63].Plays)-2]
	schema.Relations[0].Plays = append(schema.Relations[0].Plays, PlaysSpec{Relation: root, Role: "target-14"})
	// The leaf's renamed role resolves the inherited parent role.
	schema.Relations[3].Relates[0] = RelatesSpec{Role: "renamed", AsParent: "target-0"}
	leaf := schema.Relations[3]

	var scanWarnings, indexWarnings bytes.Buffer
	scan := newRenderer(schema, RenderConfig{WarnWriter: &scanWarnings})
	indexed := newRenderer(schema, RenderConfig{WarnWriter: &indexWarnings})
	if !indexed.needsRolePlayerIndex() {
		t.Fatal("large inherited-role schema did not select the index")
	}
	indexed.rolePlayers = buildRolePlayerIndex(schema)
	for _, role := range leaf.Relates {
		if got, want := indexed.findRolePlayer(leaf, role), scan.findRolePlayer(leaf, role); got != want {
			t.Errorf("%s: indexed player %q, scan player %q", role.Role, got, want)
		}
	}
	if got := indexed.findRolePlayer(leaf, leaf.Relates[1]); got != schema.Entities[0].Name {
		t.Errorf("first declaration lost: %q", got)
	}
	if got := indexed.findRolePlayer(leaf, leaf.Relates[14]); got != schema.Relations[0].Name {
		t.Errorf("relation player lost: %q", got)
	}
	if got := indexed.findRolePlayer(leaf, leaf.Relates[15]); got != "" {
		t.Errorf("unresolved role acquired player: %q", got)
	}
	if got := indexed.findRolePlayer(leaf, leaf.Relates[0]); got != schema.Entities[63].Name {
		t.Errorf("renamed inherited role: %q", got)
	}
	if got, want := indexed.buildRelationCtx(leaf), scan.buildRelationCtx(leaf); !reflect.DeepEqual(got, want) {
		t.Errorf("generated relation context differs:\nindexed: %#v\nscan: %#v", got, want)
	}
	if indexWarnings.String() != scanWarnings.String() || indexWarnings.Len() == 0 {
		t.Errorf("warnings differ: indexed %q; scan %q", indexWarnings.String(), scanWarnings.String())
	}
	var indexedGo, scannedGo bytes.Buffer
	indexCfg := DefaultConfig()
	indexCfg.WarnWriter = &indexWarnings
	scanCfg := DefaultConfig()
	scanCfg.WarnWriter = &scanWarnings
	indexWarnings.Reset()
	scanWarnings.Reset()
	if err := Render(&indexedGo, schema, indexCfg); err != nil {
		t.Fatal(err)
	}
	if err := renderWithRolePlayerIndex(&scannedGo, schema, scanCfg, false); err != nil {
		t.Fatal(err)
	}
	if indexedGo.String() != scannedGo.String() || indexWarnings.String() != scanWarnings.String() || indexWarnings.Len() == 0 {
		t.Errorf("complete Render output/warnings differ: indexed warnings %q; scan warnings %q", indexWarnings.String(), scanWarnings.String())
	}
}

func TestSmallRoleResolutionSkipsIndex(t *testing.T) {
	for _, tc := range []struct{ types, roles, depth int }{
		{16, 16, 4}, {64, 4, 4}, {64, 16, 1},
	} {
		schema, _ := roleLookupFixture(tc.types, tc.roles, tc.depth)
		if newRenderer(schema, DefaultConfig()).needsRolePlayerIndex() {
			t.Errorf("indexed small workload %+v", tc)
		}
	}
	schema, _ := roleLookupFixture(64, 16, 4)
	schema.Relations[3].Abstract = true
	if newRenderer(schema, DefaultConfig()).needsRolePlayerIndex() {
		t.Error("indexed a skipped abstract relation")
	}
}
