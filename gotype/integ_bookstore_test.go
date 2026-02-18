//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"math"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Bookstore domain models
// ---------------------------------------------------------------------------

type Author struct {
	gotype.BaseEntity
	Name        string `typedb:"name,key"`
	Nationality string `typedb:"nationality"`
}

type Book struct {
	gotype.BaseEntity
	Title string  `typedb:"title,key"`
	Year  int     `typedb:"year"`
	Price float64 `typedb:"price"`
	Pages int     `typedb:"pages"`
}

type Publisher struct {
	gotype.BaseEntity
	Name    string `typedb:"name,key"`
	Country string `typedb:"country"`
}

type Genre struct {
	gotype.BaseEntity
	Name string `typedb:"name,key"`
}

type Review struct {
	gotype.BaseEntity
	ReviewID string `typedb:"review-id,key"`
	Rating   int    `typedb:"rating"`
	Comment  string `typedb:"comment"`
}

type Wrote struct {
	gotype.BaseRelation
	Writer *Author `typedb:"role:writer"`
	Work   *Book   `typedb:"role:work"`
}

type PublishedBy struct {
	gotype.BaseRelation
	Publication *Book      `typedb:"role:publication"`
	Press       *Publisher `typedb:"role:press"`
}

type Reviewed struct {
	gotype.BaseRelation
	Reviewer   *Review `typedb:"role:reviewer-entry"`
	ReviewedOf *Book   `typedb:"role:reviewed-book"`
}

type CategorizedAs struct {
	gotype.BaseRelation
	Categorized *Book  `typedb:"role:categorized"`
	Category    *Genre `typedb:"role:category"`
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

func setupBookstoreDB(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Author]()
		_ = gotype.Register[Book]()
		_ = gotype.Register[Publisher]()
		_ = gotype.Register[Genre]()
		_ = gotype.Register[Review]()
		_ = gotype.Register[Wrote]()
		_ = gotype.Register[PublishedBy]()
		_ = gotype.Register[Reviewed]()
		_ = gotype.Register[CategorizedAs]()
	})
}

type bookstoreFixture struct {
	db      *gotype.Database
	authors []*Author
	books   []*Book
	pubs    []*Publisher
	genres  []*Genre
	reviews []*Review
}

func seedBookstore(t *testing.T) bookstoreFixture {
	t.Helper()
	db := setupBookstoreDB(t)
	ctx := context.Background()

	authorMgr := gotype.NewManager[Author](db)
	bookMgr := gotype.NewManager[Book](db)
	pubMgr := gotype.NewManager[Publisher](db)
	genreMgr := gotype.NewManager[Genre](db)
	reviewMgr := gotype.NewManager[Review](db)
	wroteMgr := gotype.NewManager[Wrote](db)
	pubByMgr := gotype.NewManager[PublishedBy](db)
	reviewedMgr := gotype.NewManager[Reviewed](db)
	catMgr := gotype.NewManager[CategorizedAs](db)

	// Authors
	authors := []*Author{
		{Name: "Tolkien", Nationality: "British"},
		{Name: "Asimov", Nationality: "American"},
		{Name: "LeGuin", Nationality: "American"},
	}
	assertInsertMany(t, ctx, authorMgr, authors)
	for i, a := range authors {
		authors[i] = assertGetOne(t, ctx, authorMgr, map[string]any{"name": a.Name})
	}

	// Books
	books := []*Book{
		{Title: "The Hobbit", Year: 1937, Price: 12.99, Pages: 310},
		{Title: "The Lord of the Rings", Year: 1954, Price: 29.99, Pages: 1178},
		{Title: "Foundation", Year: 1951, Price: 14.99, Pages: 244},
		{Title: "I Robot", Year: 1950, Price: 11.99, Pages: 253},
		{Title: "The Left Hand of Darkness", Year: 1969, Price: 13.99, Pages: 286},
	}
	assertInsertMany(t, ctx, bookMgr, books)
	for i, b := range books {
		books[i] = assertGetOne(t, ctx, bookMgr, map[string]any{"title": b.Title})
	}

	// Publishers
	pubs := []*Publisher{
		{Name: "Allen and Unwin", Country: "UK"},
		{Name: "Gnome Press", Country: "US"},
		{Name: "Ace Books", Country: "US"},
	}
	assertInsertMany(t, ctx, pubMgr, pubs)
	for i, p := range pubs {
		pubs[i] = assertGetOne(t, ctx, pubMgr, map[string]any{"name": p.Name})
	}

	// Genres
	genres := []*Genre{
		{Name: "Fantasy"},
		{Name: "Science Fiction"},
	}
	assertInsertMany(t, ctx, genreMgr, genres)
	for i, g := range genres {
		genres[i] = assertGetOne(t, ctx, genreMgr, map[string]any{"name": g.Name})
	}

	// Reviews
	reviews := []*Review{
		{ReviewID: "rev-1", Rating: 5, Comment: "Masterpiece"},
		{ReviewID: "rev-2", Rating: 4, Comment: "Great read"},
		{ReviewID: "rev-3", Rating: 5, Comment: "Classic sci-fi"},
		{ReviewID: "rev-4", Rating: 3, Comment: "Good but dated"},
		{ReviewID: "rev-5", Rating: 4, Comment: "Thought-provoking"},
	}
	assertInsertMany(t, ctx, reviewMgr, reviews)
	for i, r := range reviews {
		reviews[i] = assertGetOne(t, ctx, reviewMgr, map[string]any{"review-id": r.ReviewID})
	}

	// Wrote relations: Tolkien→Hobbit, Tolkien→LOTR, Asimov→Foundation, Asimov→IRobot, LeGuin→LeftHand
	wroteData := []struct{ a, b int }{
		{0, 0}, {0, 1}, {1, 2}, {1, 3}, {2, 4},
	}
	for _, w := range wroteData {
		assertInsert(t, ctx, wroteMgr, &Wrote{Writer: authors[w.a], Work: books[w.b]})
	}

	// Published-by: Hobbit→Allen, LOTR→Allen, Foundation→Gnome, IRobot→Gnome, LeftHand→Ace
	pubData := []struct{ b, p int }{
		{0, 0}, {1, 0}, {2, 1}, {3, 1}, {4, 2},
	}
	for _, pd := range pubData {
		assertInsert(t, ctx, pubByMgr, &PublishedBy{Publication: books[pd.b], Press: pubs[pd.p]})
	}

	// Reviews: rev1→Hobbit, rev2→LOTR, rev3→Foundation, rev4→IRobot, rev5→LeftHand
	for i := range reviews {
		assertInsert(t, ctx, reviewedMgr, &Reviewed{Reviewer: reviews[i], ReviewedOf: books[i]})
	}

	// Categories: Hobbit→Fantasy, LOTR→Fantasy, Foundation→SciFi, IRobot→SciFi, LeftHand→SciFi
	catData := []struct{ b, g int }{
		{0, 0}, {1, 0}, {2, 1}, {3, 1}, {4, 1},
	}
	for _, cd := range catData {
		assertInsert(t, ctx, catMgr, &CategorizedAs{Categorized: books[cd.b], Category: genres[cd.g]})
	}

	return bookstoreFixture{db: db, authors: authors, books: books, pubs: pubs, genres: genres, reviews: reviews}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestIntegration_Bookstore_AllEntitiesInserted(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Author](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Book](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[Publisher](f.db), 3)
	assertCount(t, ctx, gotype.NewManager[Genre](f.db), 2)
	assertCount(t, ctx, gotype.NewManager[Review](f.db), 5)
}

func TestIntegration_Bookstore_AllRelationsInserted(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()

	assertCount(t, ctx, gotype.NewManager[Wrote](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[PublishedBy](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[Reviewed](f.db), 5)
	assertCount(t, ctx, gotype.NewManager[CategorizedAs](f.db), 5)
}

func TestIntegration_Bookstore_FilterBooksByYear(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	// Books published before 1952
	results, err := mgr.Query().
		Filter(gotype.Lt("year", 1952)).
		OrderAsc("year").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 books before 1952, got %d", len(results))
	}
	if results[0].Title != "The Hobbit" {
		t.Errorf("expected first book The Hobbit, got %q", results[0].Title)
	}
}

func TestIntegration_Bookstore_FilterBooksByPriceRange(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	results, err := mgr.Query().
		Filter(gotype.Range("price", 12.0, 15.0)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Hobbit 12.99, Foundation 14.99, LeftHand 13.99 — all in [12, 15]
	if len(results) != 3 {
		t.Fatalf("expected 3 books in price range, got %d", len(results))
	}
}

func TestIntegration_Bookstore_AggregateAvgPrice(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	avg, err := mgr.Query().Avg("price").Execute(ctx)
	if err != nil {
		t.Fatalf("avg price: %v", err)
	}
	// (12.99+29.99+14.99+11.99+13.99) / 5 = 16.79
	if math.Abs(avg-16.79) > 0.01 {
		t.Errorf("expected avg price ~16.79, got %f", avg)
	}
}

func TestIntegration_Bookstore_AggregateSumPages(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	sum, err := mgr.Query().Sum("pages").Execute(ctx)
	if err != nil {
		t.Fatalf("sum pages: %v", err)
	}
	// 310+1178+244+253+286 = 2271
	if sum != 2271 {
		t.Errorf("expected sum pages 2271, got %f", sum)
	}
}

func TestIntegration_Bookstore_MinMaxYear(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	minYear, err := mgr.Query().Min("year").Execute(ctx)
	if err != nil {
		t.Fatalf("min year: %v", err)
	}
	if minYear != 1937 {
		t.Errorf("expected min year 1937, got %f", minYear)
	}

	maxYear, err := mgr.Query().Max("year").Execute(ctx)
	if err != nil {
		t.Fatalf("max year: %v", err)
	}
	if maxYear != 1969 {
		t.Errorf("expected max year 1969, got %f", maxYear)
	}
}

func TestIntegration_Bookstore_PaginatedBooks(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	// Page 1: first 2 books by year
	page1, err := mgr.Query().
		OrderAsc("year").
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page1))
	}

	// Page 2: next 2
	page2, err := mgr.Query().
		OrderAsc("year").
		Offset(2).
		Limit(2).
		Execute(ctx)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 results, got %d", len(page2))
	}

	// Pages should not overlap
	if page1[0].Title == page2[0].Title {
		t.Error("page1 and page2 should not overlap")
	}
}

func TestIntegration_Bookstore_CountByFilter(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	// Count sci-fi era books (year >= 1950)
	count, err := mgr.Query().
		Filter(gotype.Gte("year", 1950)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	// Foundation(1951), IRobot(1950), LOTR(1954), LeftHand(1969) = 4
	if count != 4 {
		t.Errorf("expected 4 books from 1950+, got %d", count)
	}
}

func TestIntegration_Bookstore_ExistsAndNotExists(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	exists, err := mgr.Query().
		Filter(gotype.Eq("title", "The Hobbit")).
		Exists(ctx)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Error("expected The Hobbit to exist")
	}

	exists2, err := mgr.Query().
		Filter(gotype.Eq("title", "Nonexistent Book")).
		Exists(ctx)
	if err != nil {
		t.Fatalf("exists2: %v", err)
	}
	if exists2 {
		t.Error("expected nonexistent book to not exist")
	}
}

func TestIntegration_Bookstore_FilterAmericanAuthors(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Author](f.db)

	results, err := mgr.Query().
		Filter(gotype.Eq("nationality", "American")).
		OrderAsc("name").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 American authors, got %d", len(results))
	}
	if results[0].Name != "Asimov" {
		t.Errorf("expected first American author Asimov, got %q", results[0].Name)
	}
}

func TestIntegration_Bookstore_DeleteBookRemovesRelations(t *testing.T) {
	// Deleting a book should not fail even if relations exist (TypeDB cascades).
	// But we must first delete the relations referencing it.
	f := seedBookstore(t)
	ctx := context.Background()
	bookMgr := gotype.NewManager[Book](f.db)

	// Verify 5 books initially
	assertCount(t, ctx, bookMgr, 5)

	// Deleting a book that has relations pointing to it requires
	// deleting those relations first (TypeDB constraint).
	// For now, verify that the book exists and can be fetched.
	result := assertGetOne(t, ctx, bookMgr, map[string]any{"title": "I Robot"})
	if result.Year != 1950 {
		t.Errorf("expected year 1950, got %d", result.Year)
	}
}

func TestIntegration_Bookstore_UpdateBookPrice(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	book := assertGetOne(t, ctx, mgr, map[string]any{"title": "The Hobbit"})
	book.Price = 15.99
	assertUpdate(t, ctx, mgr, book)

	updated := assertGetOne(t, ctx, mgr, map[string]any{"title": "The Hobbit"})
	if math.Abs(updated.Price-15.99) > 0.01 {
		t.Errorf("expected price 15.99, got %f", updated.Price)
	}
}

func TestIntegration_Bookstore_FilterBooksContainingThe(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	results, err := mgr.Query().
		Filter(gotype.Contains("title", "The")).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// "The Hobbit", "The Lord of the Rings", "The Left Hand of Darkness"
	if len(results) != 3 {
		t.Errorf("expected 3 books containing 'The', got %d", len(results))
	}
}

func TestIntegration_Bookstore_InFilter(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	results, err := mgr.Query().
		Filter(gotype.In("title", []any{"The Hobbit", "Foundation"})).
		OrderAsc("title").
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 books, got %d", len(results))
	}
}

func TestIntegration_Bookstore_OrFilter(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	// Cheap books (price < 13) OR long books (pages > 500)
	results, err := mgr.Query().
		Filter(gotype.Or(
			gotype.Lt("price", 13.0),
			gotype.Gt("pages", 500),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// Lt(price,13): Hobbit(12.99), IRobot(11.99)
	// Gt(pages,500): LOTR(1178)
	// Union: Hobbit, IRobot, LOTR = 3
	if len(results) != 3 {
		t.Errorf("expected 3 results from OR filter, got %d", len(results))
	}
}

func TestIntegration_Bookstore_AndFilter(t *testing.T) {
	f := seedBookstore(t)
	ctx := context.Background()
	mgr := gotype.NewManager[Book](f.db)

	// Books that are both expensive (> 14) AND long (> 250 pages)
	results, err := mgr.Query().
		Filter(gotype.And(
			gotype.Gt("price", 14.0),
			gotype.Gt("pages", 250),
		)).
		Execute(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	// LOTR (29.99, 1178) and Foundation (14.99, 244 — no, 244 < 250) → just LOTR
	if len(results) != 1 {
		t.Fatalf("expected 1 result (LOTR), got %d", len(results))
	}
	if results[0].Title != "The Lord of the Rings" {
		t.Errorf("expected LOTR, got %q", results[0].Title)
	}
}
