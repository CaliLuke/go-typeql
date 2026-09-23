//go:build integration && cgo && typedb

package gotype_test

// End-to-end coverage of label handling.
//
// Labels that do not round-trip through Go names: the models below are the
// shape tqlgen renders for
//
//	attribute gt-name, value string;
//	entity gt_account, owns gt-name @key, plays gt_member_of:member;
//	relation gt_member_of, relates member, owns gt-name;
//
// (pinned by tqlgen's TestRenderCompile_TypeNameRoundTrip): without the type:
// tags these would register as gt-account / gt-member-of, and the relation's
// role player would resolve to gt-account even with the player's tag.

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/v2/gotype"
)

type GtAccount struct {
	gotype.BaseEntity `typedb:"type:gt_account"`
	GtName            string `typedb:"gt-name,key"`
}

type GtMemberOf struct {
	gotype.BaseRelation `typedb:"type:gt_member_of"`
	Member              *GtAccount `typedb:"role:member"`
	GtName              string     `typedb:"gt-name"`
}

func TestIntegration_GeneratedSnakeCaseLabels(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[GtAccount]()
		_ = gotype.Register[GtMemberOf]()
	})
	ctx := context.Background()

	accounts := gotype.MustNewManager[GtAccount](db)
	ann := insertAndGet(t, ctx, accounts, &GtAccount{GtName: "ann"}, "gt-name", "ann")

	memberships := gotype.MustNewManager[GtMemberOf](db)
	assertInsert(t, ctx, memberships, &GtMemberOf{Member: ann, GtName: "m1"})

	got, err := memberships.GetWithRoles(ctx, map[string]any{"gt-name": "m1"})
	if err != nil {
		t.Fatalf("GetWithRoles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d memberships, want 1", len(got))
	}
	if got[0].Member == nil || got[0].Member.GtName != "ann" {
		t.Fatalf("role player not hydrated from gt_account: %+v", got[0].Member)
	}

	// The schema on the server uses the snake_case labels themselves.
	rows, err := db.ExecuteRead(ctx, `match $a isa gt_account; $m isa gt_member_of, links (member: $a);`)
	if err != nil {
		t.Fatalf("query by schema labels: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows matching the schema labels, want 1", len(rows))
	}
}

type LnContact struct {
	gotype.BaseEntity
	LnID      string `typedb:"ln-id,key"`
	FirstName string `typedb:"first-name"`
	FirstSnak string `typedb:"first_name"`
}

// Filters on first-name and first_name used to bind one shared variable,
// which TypeQL treats as an equality between the two values, so the query
// matched nothing.
func TestIntegration_FiltersOnHyphenAndUnderscoreLabels(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[LnContact]()
	})
	ctx := context.Background()
	contacts := gotype.MustNewManager[LnContact](db)
	assertInsert(t, ctx, contacts, &LnContact{LnID: "c1", FirstName: "Ann", FirstSnak: "Bob"})

	got, err := contacts.Query().Filter(gotype.Eq("first-name", "Ann"), gotype.Eq("first_name", "Bob")).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].FirstName != "Ann" || got[0].FirstSnak != "Bob" {
		t.Fatalf("got %+v, want the c1 contact", got)
	}
}
