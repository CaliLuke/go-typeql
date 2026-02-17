//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/driver"
	"github.com/CaliLuke/go-typeql/gotype"
)

// ---------------------------------------------------------------------------
// Test models (additional beyond Person/Company/Employment in integration_test.go)
// ---------------------------------------------------------------------------

// Profile has all optional fields (pointers) except the key.
type Profile struct {
	gotype.BaseEntity
	Username string   `typedb:"username,key"`
	Bio      *string  `typedb:"bio,card=0..1"`
	Score    *float64 `typedb:"score,card=0..1"`
	Active   *bool    `typedb:"active,card=0..1"`
	Level    *int     `typedb:"level,card=0..1"`
}

// TypeTest exercises every supported value type.
type TypeTest struct {
	gotype.BaseEntity
	TagName  string  `typedb:"tag-name,key"`
	IntVal   int     `typedb:"int-val"`
	FloatVal float64 `typedb:"float-val"`
	BoolVal  bool    `typedb:"bool-val"`
	StrVal   string  `typedb:"str-val"`
}

// Friendship is a self-referencing relation (person <-> person).
type Friendship struct {
	gotype.BaseRelation
	Friend1 *Person `typedb:"role:friend1"`
	Friend2 *Person `typedb:"role:friend2"`
	Since   string  `typedb:"since"`
}

// Membership is a relation with multiple attributes.
type Membership struct {
	gotype.BaseRelation
	Member *Person  `typedb:"role:member"`
	Group  *Company `typedb:"role:group"`
	Title  string   `typedb:"title"`
	Active *bool    `typedb:"membership-active,card=0..1"`
}

// ---------------------------------------------------------------------------
// Driver adapter: bridges driver.Driver -> gotype.Conn
// ---------------------------------------------------------------------------

// driverAdapter wraps a *driver.Driver to satisfy gotype.Conn.
type driverAdapter struct {
	drv *driver.Driver
}

func (a *driverAdapter) Transaction(dbName string, txType int) (gotype.Tx, error) {
	tx, err := a.drv.Transaction(dbName, driver.TransactionType(txType))
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (a *driverAdapter) Schema(dbName string) (string, error) {
	return a.drv.Databases().Schema(dbName)
}

func (a *driverAdapter) DatabaseCreate(name string) error {
	return a.drv.Databases().Create(name)
}

func (a *driverAdapter) DatabaseDelete(name string) error {
	return a.drv.Databases().Delete(name)
}

func (a *driverAdapter) DatabaseContains(name string) (bool, error) {
	return a.drv.Databases().Contains(name)
}

func (a *driverAdapter) DatabaseAll() ([]string, error) {
	return a.drv.Databases().All()
}

func (a *driverAdapter) Close() {
	a.drv.Close()
}

func (a *driverAdapter) IsOpen() bool {
	return a.drv.IsOpen()
}

// ---------------------------------------------------------------------------
// Shared constants and helpers
// ---------------------------------------------------------------------------

const testDBName = "go_typeql_integration_test"

func dbAddress() string {
	if addr := os.Getenv("TEST_DB_ADDRESS"); addr != "" {
		return addr
	}
	return "localhost:1729"
}


// ---------------------------------------------------------------------------
// Flexible DB setup helper
// ---------------------------------------------------------------------------

// setupTestDBWith creates a fresh TypeDB database scoped to the test name,
// registers the types from registerFn, applies the generated schema, and
// returns a *gotype.Database. Cleanup (DB deletion + driver close) is
// handled via t.Cleanup.
func setupTestDBWith(t *testing.T, registerFn func()) *gotype.Database {
	t.Helper()

	addr := dbAddress()

	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available at %s: %v", addr, err)
	}

	// Unique DB name per test to avoid collisions.
	sanitized := strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(t.Name())
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	dbName := fmt.Sprintf("go_integ_%s", strings.ToLower(sanitized))

	conn := &driverAdapter{drv: drv}

	// Clean up any leftover database from a previous run.
	exists, err := conn.DatabaseContains(dbName)
	if err != nil {
		drv.Close()
		t.Fatalf("checking database existence: %v", err)
	}
	if exists {
		if err := conn.DatabaseDelete(dbName); err != nil {
			drv.Close()
			t.Fatalf("deleting old test database: %v", err)
		}
	}

	if err := conn.DatabaseCreate(dbName); err != nil {
		drv.Close()
		t.Fatalf("creating test database: %v", err)
	}

	// Clear and re-register types.
	gotype.ClearRegistry()
	registerFn()

	db := gotype.NewDatabase(conn, dbName)

	schema := gotype.GenerateSchema()
	ctx := context.Background()
	if err := db.ExecuteSchema(ctx, schema); err != nil {
		drv.Close()
		t.Fatalf("defining schema: %v\nschema:\n%s", err, schema)
	}

	t.Cleanup(func() {
		_ = conn.DatabaseDelete(dbName)
		drv.Close()
	})

	return db
}

// setupTestDBDefault sets up Person + Company + Employment (the default model set).
func setupTestDBDefault(t *testing.T) *gotype.Database {
	return setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})
}

// ---------------------------------------------------------------------------
// Pointer helpers
// ---------------------------------------------------------------------------


// ---------------------------------------------------------------------------
// Seed data helper: inserts a standard set of Person records.
// ---------------------------------------------------------------------------

func seedPersons(t *testing.T, ctx context.Context, mgr *gotype.Manager[Person]) []*Person {
	t.Helper()
	persons := []*Person{
		{Name: "Alice", Email: "alice@example.com", Age: new(30)},
		{Name: "Bob", Email: "bob@example.com", Age: new(25)},
		{Name: "Charlie", Email: "charlie@example.com", Age: new(35)},
		{Name: "Diana", Email: "diana@example.com", Age: new(28)},
		{Name: "Eve", Email: "eve@example.com", Age: nil},
	}
	if err := mgr.InsertMany(ctx, persons); err != nil {
		t.Fatalf("seedPersons InsertMany failed: %v", err)
	}
	return persons
}

// getSchemaString retrieves the current schema from the database via the driver.
func getSchemaString(t *testing.T, addr, dbName string) string {
	t.Helper()
	drv, err := driver.Open(addr, "admin", "password")
	if err != nil {
		t.Skipf("TypeDB not available: %v", err)
	}
	defer drv.Close()

	conn := &driverAdapter{drv: drv}
	schema, err := conn.Schema(dbName)
	if err != nil {
		t.Fatalf("getting schema: %v", err)
	}
	return schema
}
