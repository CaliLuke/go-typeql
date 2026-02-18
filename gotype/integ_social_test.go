//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Social network domain models
// ---------------------------------------------------------------------------

type SocialUser struct {
	gotype.BaseEntity
	Handle   string `typedb:"handle,key"`
	DisplayName string `typedb:"display-name"`
	Followers *int  `typedb:"follower-count,card=0..1"`
}

type Post struct {
	gotype.BaseEntity
	PostID  string `typedb:"post-id,key"`
	Content string `typedb:"content"`
	Likes   int    `typedb:"like-count"`
}

type Comment struct {
	gotype.BaseEntity
	CommentID string `typedb:"comment-id,key"`
	Body      string `typedb:"body"`
}

type Follows struct {
	gotype.BaseRelation
	Follower *SocialUser `typedb:"role:follower"`
	Followed *SocialUser `typedb:"role:followed"`
}

type SocialFriendship struct {
	gotype.BaseRelation
	SocialFriend1 *SocialUser `typedb:"role:social-friend1"`
	SocialFriend2 *SocialUser `typedb:"role:social-friend2"`
	Since         string      `typedb:"friendship-since"`
}

type Authored struct {
	gotype.BaseRelation
	PostAuthor *SocialUser `typedb:"role:post-author"`
	AuthoredPost *Post     `typedb:"role:authored-post"`
}

type CommentedOn struct {
	gotype.BaseRelation
	Commenter    *SocialUser `typedb:"role:commenter"`
	CommentEntry *Comment    `typedb:"role:comment-entry"`
	CommentedPost *Post      `typedb:"role:commented-post"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupSocialDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[SocialUser]()
		_ = gotype.Register[Post]()
		_ = gotype.Register[Comment]()
		_ = gotype.Register[Follows]()
		_ = gotype.Register[SocialFriendship]()
		_ = gotype.Register[Authored]()
		_ = gotype.Register[CommentedOn]()
	})
}

type socialFixture struct {
	db       *gotype.Database
	users    []*SocialUser
	posts    []*Post
	comments []*Comment
}

func seedSocial(t *testing.T) socialFixture {
	t.Helper()
	db := setupSocialDB(t)
	ctx := context.Background()

	userMgr := gotype.NewManager[SocialUser](db)
	postMgr := gotype.NewManager[Post](db)
	commentMgr := gotype.NewManager[Comment](db)
	followMgr := gotype.NewManager[Follows](db)
	friendMgr := gotype.NewManager[SocialFriendship](db)
	authorMgr := gotype.NewManager[Authored](db)
	commentOnMgr := gotype.NewManager[CommentedOn](db)

	users := []*SocialUser{
		{Handle: "alice", DisplayName: "Alice A", Followers: new(100)},
		{Handle: "bob", DisplayName: "Bob B", Followers: new(50)},
		{Handle: "carol", DisplayName: "Carol C", Followers: new(200)},
		{Handle: "dave", DisplayName: "Dave D", Followers: new(10)},
		{Handle: "eve", DisplayName: "Eve E", Followers: nil},
	}
	assertInsertMany(t, ctx, userMgr, users)
	for i, u := range users {
		users[i] = assertGetOne(t, ctx, userMgr, map[string]any{"handle": u.Handle})
	}

	posts := []*Post{
		{PostID: "p1", Content: "Hello world", Likes: 42},
		{PostID: "p2", Content: "Go is great", Likes: 100},
		{PostID: "p3", Content: "TypeDB rocks", Likes: 75},
		{PostID: "p4", Content: "Good morning", Likes: 5},
	}
	assertInsertMany(t, ctx, postMgr, posts)
	for i, p := range posts {
		posts[i] = assertGetOne(t, ctx, postMgr, map[string]any{"post-id": p.PostID})
	}

	comments := []*Comment{
		{CommentID: "c1", Body: "Nice post!"},
		{CommentID: "c2", Body: "I agree"},
		{CommentID: "c3", Body: "Interesting"},
	}
	assertInsertMany(t, ctx, commentMgr, comments)
	for i, c := range comments {
		comments[i] = assertGetOne(t, ctx, commentMgr, map[string]any{"comment-id": c.CommentID})
	}

	// Follows: alice→bob, alice→carol, bob→carol, carol→alice, dave→alice
	followData := []struct{ from, to int }{
		{0, 1}, {0, 2}, {1, 2}, {2, 0}, {3, 0},
	}
	for _, f := range followData {
		assertInsert(t, ctx, followMgr, &Follows{Follower: users[f.from], Followed: users[f.to]})
	}

	// Friendships (symmetric): alice-bob, alice-carol, bob-carol
	friendData := []struct{ a, b int }{
		{0, 1}, {0, 2}, {1, 2},
	}
	for _, f := range friendData {
		assertInsert(t, ctx, friendMgr, &SocialFriendship{
			SocialFriend1: users[f.a],
			SocialFriend2: users[f.b],
			Since:         "2024-01-01",
		})
	}

	// Authored: alice→p1, alice→p2, bob→p3, carol→p4
	authorData := []struct{ u, p int }{
		{0, 0}, {0, 1}, {1, 2}, {2, 3},
	}
	for _, a := range authorData {
		assertInsert(t, ctx, authorMgr, &Authored{PostAuthor: users[a.u], AuthoredPost: posts[a.p]})
	}

	// Comments: bob→c1→p1, carol→c2→p1, dave→c3→p2
	commentOnData := []struct{ u, c, p int }{
		{1, 0, 0}, {2, 1, 0}, {3, 2, 1},
	}
	for _, co := range commentOnData {
		assertInsert(t, ctx, commentOnMgr, &CommentedOn{
			Commenter:     users[co.u],
			CommentEntry:  comments[co.c],
			CommentedPost: posts[co.p],
		})
	}

	return socialFixture{db: db, users: users, posts: posts, comments: comments}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Social_AllEntitiesInserted(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[SocialUser](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[Post](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[Comment](f.db), 3)
}

func TestIntegration_Social_AllRelationsInserted(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Follows](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[SocialFriendship](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Authored](f.db), 4)
	assertCount(t, ctx, gotype.NewManager[CommentedOn](f.db), 3)
}

func TestIntegration_Social_SelfReferentialFollows(t *testing.T) {
	// Follows is self-referential (SocialUser → SocialUser).
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Follows](f.db)

	count, err := mgr.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 follow relations, got %d", count)
	}
}

func TestIntegration_Social_FilterPopularUsers(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[SocialUser](f.db)

	// Users with > 50 followers
	results, err := mgr.Query().
		Filter(gotype.Gt("follower-count", 50)).
		OrderDesc("follower-count").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 popular users, got %d", len(results))
	}
	if results[0].Handle != "carol" {
		t.Errorf("expected carol first (200 followers), got %q", results[0].Handle)
	}
}

func TestIntegration_Social_FilterUserWithoutFollowers(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[SocialUser](f.db)

	// Eve has no follower-count attribute
	results, err := mgr.Query().
		Filter(gotype.NotHasAttr("follower-count")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 user without follower-count, got %d", len(results))
	}
	if results[0].Handle != "eve" {
		t.Errorf("expected eve, got %q", results[0].Handle)
	}
}

func TestIntegration_Social_FilterPostsByLikes(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	results, err := mgr.Query().
		Filter(gotype.Gte("like-count", 50)).
		OrderDesc("like-count").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// p2(100), p3(75)
	if len(results) != 2 {
		t.Fatalf("expected 2 popular posts, got %d", len(results))
	}
	if results[0].Likes != 100 {
		t.Errorf("expected most liked post with 100, got %d", results[0].Likes)
	}
}

func TestIntegration_Social_SumPostLikes(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	sum, err := mgr.Query().Sum("like-count").Execute(ctx)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	// 42 + 100 + 75 + 5 = 222
	if sum != 222 {
		t.Errorf("expected sum likes 222, got %f", sum)
	}
}

func TestIntegration_Social_AvgPostLikes(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	avg, err := mgr.Query().Avg("like-count").Execute(ctx)
	if err != nil {
		t.Fatalf("avg: %v", err)
	}
	// (42+100+75+5)/4 = 55.5
	expected := 55.5
	if avg < expected-0.1 || avg > expected+0.1 {
		t.Errorf("expected avg likes ~55.5, got %f", avg)
	}
}

func TestIntegration_Social_ContainsFilter(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	results, err := mgr.Query().
		Filter(gotype.Contains("content", "great")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 post containing 'great', got %d", len(results))
	}
	if results[0].PostID != "p2" {
		t.Errorf("expected p2, got %q", results[0].PostID)
	}
}

func TestIntegration_Social_UpdatePostLikes(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	post := assertGetOne(t, ctx, mgr, map[string]any{"post-id": "p1"})
	post.Likes = 43
	assertUpdate(t, ctx, mgr, post)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"post-id": "p1"})
	if updated.Likes != 43 {
		t.Errorf("expected likes 43 after update, got %d", updated.Likes)
	}
}

func TestIntegration_Social_PaginatedPosts(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	page1, err := mgr.Query().
		OrderDesc("like-count").
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page1))
	}

	page2, err := mgr.Query().
		OrderDesc("like-count").
		Offset(2).
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page2))
	}

	// Top post should be p2 (100 likes)
	if page1[0].PostID != "p2" {
		t.Errorf("expected p2 as top post, got %q", page1[0].PostID)
	}
}

func TestIntegration_Social_ExistsPost(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	exists, err := mgr.Query().
		Filter(gotype.Eq("post-id", "p1")).
		Exists(ctx)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Error("expected p1 to exist")
	}

	exists2, err := mgr.Query().
		Filter(gotype.Eq("post-id", "p999")).
		Exists(ctx)
	if err != nil {
		t.Fatalf("exists2: %v", err)
	}
	if exists2 {
		t.Error("expected p999 to not exist")
	}
}

func TestIntegration_Social_OrFilterPosts(t *testing.T) {
	f := seedSocial(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Post](f.db)

	// Posts with > 90 likes OR content containing "world"
	results, err := mgr.Query().
		Filter(gotype.Or(
			gotype.Gt("like-count", 90),
			gotype.Contains("content", "world"),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// p1 ("Hello world", 42) and p2 ("Go is great", 100)
	if len(results) != 2 {
		t.Errorf("expected 2 results from OR filter, got %d", len(results))
	}
}
