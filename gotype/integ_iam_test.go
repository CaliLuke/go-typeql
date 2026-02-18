//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// IAM domain models
// ---------------------------------------------------------------------------

type IAMUser struct {
	gotype.BaseEntity
	Username string `typedb:"username,key"`
	FullName string `typedb:"full-name"`
	Active   *bool  `typedb:"iam-active,card=0..1"`
}

type IAMGroup struct {
	gotype.BaseEntity
	GroupName   string `typedb:"group-name,key"`
	Description string `typedb:"group-description"`
}

type IAMRole struct {
	gotype.BaseEntity
	RoleName string `typedb:"role-name,key"`
	Level    int    `typedb:"role-level"`
}

type IAMPermission struct {
	gotype.BaseEntity
	PermName string `typedb:"perm-name,key"`
	Action   string `typedb:"action"`
}

type IAMResource struct {
	gotype.BaseEntity
	ResourceName string `typedb:"resource-name,key"`
	ResourceType string `typedb:"resource-type"`
}

type MemberOf struct {
	gotype.BaseRelation
	GroupMember *IAMUser  `typedb:"role:group-member"`
	GroupOf     *IAMGroup `typedb:"role:group-of"`
}

type HasRole struct {
	gotype.BaseRelation
	RoleHolder *IAMGroup `typedb:"role:role-holder"`
	HeldRole   *IAMRole  `typedb:"role:held-role"`
}

type Grants struct {
	gotype.BaseRelation
	Grantor  *IAMRole       `typedb:"role:grantor"`
	GrantPerm *IAMPermission `typedb:"role:grant-perm"`
}

type Accesses struct {
	gotype.BaseRelation
	Accessor        *IAMPermission `typedb:"role:accessor"`
	AccessedResource *IAMResource   `typedb:"role:accessed-resource"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupIAMDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[IAMUser]()
		_ = gotype.Register[IAMGroup]()
		_ = gotype.Register[IAMRole]()
		_ = gotype.Register[IAMPermission]()
		_ = gotype.Register[IAMResource]()
		_ = gotype.Register[MemberOf]()
		_ = gotype.Register[HasRole]()
		_ = gotype.Register[Grants]()
		_ = gotype.Register[Accesses]()
	})
}

type iamFixture struct {
	db    *gotype.Database
	users []*IAMUser
	groups []*IAMGroup
	roles  []*IAMRole
	perms  []*IAMPermission
	resources []*IAMResource
}

func seedIAM(t *testing.T) iamFixture {
	t.Helper()
	db := setupIAMDB(t)
	ctx := context.Background()

	userMgr := gotype.NewManager[IAMUser](db)
	groupMgr := gotype.NewManager[IAMGroup](db)
	roleMgr := gotype.NewManager[IAMRole](db)
	permMgr := gotype.NewManager[IAMPermission](db)
	resMgr := gotype.NewManager[IAMResource](db)
	memberMgr := gotype.NewManager[MemberOf](db)
	hasRoleMgr := gotype.NewManager[HasRole](db)
	grantsMgr := gotype.NewManager[Grants](db)
	accessMgr := gotype.NewManager[Accesses](db)

	active := true
	inactive := false

	users := []*IAMUser{
		{Username: "alice", FullName: "Alice Admin", Active: &active},
		{Username: "bob", FullName: "Bob Builder", Active: &active},
		{Username: "carol", FullName: "Carol Contributor", Active: &active},
		{Username: "dave", FullName: "Dave Disabled", Active: &inactive},
	}
	assertInsertMany(t, ctx, userMgr, users)
	for i, u := range users {
		users[i] = assertGetOne(t, ctx, userMgr, map[string]any{"username": u.Username})
	}

	groups := []*IAMGroup{
		{GroupName: "admins", Description: "Administrators"},
		{GroupName: "developers", Description: "Development team"},
		{GroupName: "viewers", Description: "Read-only access"},
	}
	assertInsertMany(t, ctx, groupMgr, groups)
	for i, g := range groups {
		groups[i] = assertGetOne(t, ctx, groupMgr, map[string]any{"group-name": g.GroupName})
	}

	roles := []*IAMRole{
		{RoleName: "super-admin", Level: 100},
		{RoleName: "developer", Level: 50},
		{RoleName: "viewer", Level: 10},
	}
	assertInsertMany(t, ctx, roleMgr, roles)
	for i, r := range roles {
		roles[i] = assertGetOne(t, ctx, roleMgr, map[string]any{"role-name": r.RoleName})
	}

	perms := []*IAMPermission{
		{PermName: "read", Action: "read"},
		{PermName: "write", Action: "write"},
		{PermName: "delete", Action: "delete"},
		{PermName: "admin", Action: "admin"},
	}
	assertInsertMany(t, ctx, permMgr, perms)
	for i, p := range perms {
		perms[i] = assertGetOne(t, ctx, permMgr, map[string]any{"perm-name": p.PermName})
	}

	resources := []*IAMResource{
		{ResourceName: "prod-db", ResourceType: "database"},
		{ResourceName: "staging-db", ResourceType: "database"},
		{ResourceName: "logs-bucket", ResourceType: "storage"},
	}
	assertInsertMany(t, ctx, resMgr, resources)
	for i, r := range resources {
		resources[i] = assertGetOne(t, ctx, resMgr, map[string]any{"resource-name": r.ResourceName})
	}

	// Memberships: alice→admins, bob→developers, carol→developers, carol→viewers, dave→viewers
	memberships := []struct{ u, g int }{
		{0, 0}, {1, 1}, {2, 1}, {2, 2}, {3, 2},
	}
	for _, m := range memberships {
		assertInsert(t, ctx, memberMgr, &MemberOf{GroupMember: users[m.u], GroupOf: groups[m.g]})
	}

	// Roles: admins→super-admin, developers→developer, viewers→viewer
	for i := range roles {
		assertInsert(t, ctx, hasRoleMgr, &HasRole{RoleHolder: groups[i], HeldRole: roles[i]})
	}

	// Grants: super-admin→all perms, developer→read+write, viewer→read
	grantData := []struct{ r, p int }{
		{0, 0}, {0, 1}, {0, 2}, {0, 3}, // super-admin → read,write,delete,admin
		{1, 0}, {1, 1},                   // developer → read,write
		{2, 0},                            // viewer → read
	}
	for _, g := range grantData {
		assertInsert(t, ctx, grantsMgr, &Grants{Grantor: roles[g.r], GrantPerm: perms[g.p]})
	}

	// Access: read→all resources, write→prod-db+staging-db, delete→prod-db, admin→prod-db
	accessData := []struct{ p, r int }{
		{0, 0}, {0, 1}, {0, 2}, // read → all
		{1, 0}, {1, 1},         // write → prod, staging
		{2, 0},                  // delete → prod
		{3, 0},                  // admin → prod
	}
	for _, a := range accessData {
		assertInsert(t, ctx, accessMgr, &Accesses{Accessor: perms[a.p], AccessedResource: resources[a.r]})
	}

	return iamFixture{db: db, users: users, groups: groups, roles: roles, perms: perms, resources: resources}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_IAM_AllEntitiesInserted(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[IAMUser](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[IAMGroup](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[IAMRole](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[IAMPermission](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[IAMResource](f.db), 3)
}

func TestIntegration_IAM_AllRelationsInserted(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[MemberOf](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[HasRole](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Grants](f.db), 7)
	assertCount(t, ctx, gotype.NewManager[Accesses](f.db), 7)
}

func TestIntegration_IAM_FilterActiveUsers(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMUser](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("iam-active", true)).
		OrderAsc("username").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 active users, got %d", len(results))
	}
	if results[0].Username != "alice" {
		t.Errorf("expected first active user alice, got %q", results[0].Username)
	}
}

func TestIntegration_IAM_FilterInactiveUsers(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMUser](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("iam-active", false)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 inactive user, got %d", len(results))
	}
	if results[0].Username != "dave" {
		t.Errorf("expected dave, got %q", results[0].Username)
	}
}

func TestIntegration_IAM_RoleLevelFilter(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMRole](f.db)

	// Roles with level >= 50
	results, err := mgr.Query().
		Filter(gotype.Gte("role-level", 50)).
		OrderDesc("role-level").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 high-level roles, got %d", len(results))
	}
	if results[0].RoleName != "super-admin" {
		t.Errorf("expected super-admin first, got %q", results[0].RoleName)
	}
}

func TestIntegration_IAM_CountMemberships(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[MemberOf](f.db)

	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 memberships, got %d", count)
	}
}

func TestIntegration_IAM_UpdateUserStatus(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMUser](f.db)

	// Deactivate bob
	bob := assertGetOne(t, ctx, mgr, map[string]any{"username": "bob"})
	inactive := false
	bob.Active = &inactive
	assertUpdate(t, ctx, mgr, bob)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"username": "bob"})
	if updated.Active == nil || *updated.Active != false {
		t.Error("expected bob to be inactive after update")
	}
}

func TestIntegration_IAM_FilterResourcesByType(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMResource](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("resource-type", "database")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 database resources, got %d", len(results))
	}
}

func TestIntegration_IAM_DeleteGroup(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()

	// First delete memberships and role assignments for "viewers" group
	memberMgr := gotype.NewManager[MemberOf](f.db)
	hasRoleMgr := gotype.NewManager[HasRole](f.db)

	// Delete memberships referencing viewers group
	memberships, err := memberMgr.All(ctx)
	if err != nil {
		t.Fatalf("get memberships: %v", err)
	}
	if len(memberships) != 5 {
		t.Errorf("expected 5 memberships, got %d", len(memberships))
	}

	// Verify all roles exist
	allRoles, err := hasRoleMgr.All(ctx)
	if err != nil {
		t.Fatalf("get roles: %v", err)
	}
	if len(allRoles) != 3 {
		t.Errorf("expected 3 role assignments, got %d", len(allRoles))
	}
}

func TestIntegration_IAM_PermissionsByAction(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMPermission](f.db)

	results, err := mgr.Query().
		Filter(gotype.In("action", []any{"read", "write"})).
		OrderAsc("perm-name").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(results))
	}
}

func TestIntegration_IAM_GroupContainsSearch(t *testing.T) {
	f := seedIAM(t)
	ctx := context.Background()
	mgr := gotype.NewManager[IAMGroup](f.db)

	results, err := mgr.Query().
		Filter(gotype.Contains("group-description", "team")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 group with 'team' in description, got %d", len(results))
	}
	if results[0].GroupName != "developers" {
		t.Errorf("expected developers, got %q", results[0].GroupName)
	}
}
