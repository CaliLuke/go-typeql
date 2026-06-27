//go:build cgo && typedb && integration

package driver

import (
	"strings"
	"testing"
)

func TestQueryWithRows_InsertAndReadScalars(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_rows_scalars"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query(`
define
  attribute name, value string;
  attribute age, value integer;
  entity person,
    owns name @key,
    owns age;
`); err != nil {
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	insertRows := NewGivenRows("n", "a").
		MustAdd(StringGiven("Alice"), IntGiven(30)).
		MustAdd(StringGiven("Bob"), IntGiven(41))

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	if _, err := writeTx.QueryWithRows(`
given $n: string, $a: integer;
insert $p isa person, has name == $n, has age == $a;
`, insertRows); err != nil {
		t.Fatalf("insert with given rows: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit write: %v", err)
	}

	readRows := NewGivenRows("n").
		MustAdd(StringGiven("Alice")).
		MustAdd(StringGiven("Bob"))

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	defer readTx.Close()

	results, err := readTx.QueryWithRows(`
given $n: string;
match
  $p isa person, has name == $n, has age $a;
fetch {
  "name": $n,
  "age": $a
};
`, readRows)
	if err != nil {
		t.Fatalf("read with given rows: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %#v", len(results), results)
	}

	seen := map[string]bool{}
	for _, result := range results {
		name, ok := result["name"].(string)
		if !ok {
			t.Fatalf("result missing string name: %#v", result)
		}
		seen[name] = true
	}
	if !seen["Alice"] || !seen["Bob"] {
		t.Fatalf("missing expected names in results: %#v", results)
	}
}

func TestQueryWithRows_UsesOpaqueConceptsFromQueryResults(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_rows_concepts"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query(`
define
  attribute name, value string;
  attribute age, value integer;
  entity person,
    owns name @key,
    owns age;
`); err != nil {
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	insertTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open insert tx: %v", err)
	}
	if _, err := insertTx.Query(`
insert
  $alice isa person, has name "Alice";
  $bob isa person, has name "Bob";
`); err != nil {
		t.Fatalf("insert people: %v", err)
	}
	if err := insertTx.Commit(); err != nil {
		t.Fatalf("commit insert: %v", err)
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	conceptRows, err := readTx.Query(`
match
  $p isa person, has name $n;
select $p, $n;
sort $n asc;
`)
	if err != nil {
		t.Fatalf("select concepts: %v", err)
	}
	readTx.Close()
	if len(conceptRows) != 2 {
		t.Fatalf("expected two concept rows, got %d: %#v", len(conceptRows), conceptRows)
	}

	alice, ok := AsConcept(conceptRows[0]["p"])
	if !ok {
		t.Fatalf("expected opaque concept for first person, got %#v", conceptRows[0]["p"])
	}
	bob, ok := AsConcept(conceptRows[1]["p"])
	if !ok {
		t.Fatalf("expected opaque concept for second person, got %#v", conceptRows[1]["p"])
	}
	if alice.Handle == "" || bob.Handle == "" || alice.Handle == bob.Handle {
		t.Fatalf("expected distinct non-empty handles: alice=%#v bob=%#v", alice, bob)
	}

	givenRows := NewGivenRows("p", "a").
		MustAdd(ConceptGiven(alice), IntGiven(30)).
		MustAdd(ConceptGiven(bob), IntGiven(41))

	updateTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open update tx: %v", err)
	}
	if _, err := updateTx.QueryWithRows(`
given $p: person, $a: integer;
insert $p has age == $a;
`, givenRows); err != nil {
		t.Fatalf("insert ages with concept given rows: %v", err)
	}
	if err := updateTx.Commit(); err != nil {
		t.Fatalf("commit update: %v", err)
	}

	verifyTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open verify tx: %v", err)
	}
	defer verifyTx.Close()

	results, err := verifyTx.Query(`
match
  $p isa person, has name $n, has age $a;
fetch {
  "name": $n,
  "age": $a
};
`)
	if err != nil {
		t.Fatalf("verify ages: %v", err)
	}
	ages := map[string]int64{}
	for _, result := range results {
		name, ok := result["name"].(string)
		if !ok {
			t.Fatalf("missing string name: %#v", result)
		}
		age, ok := integerResult(result["age"])
		if !ok {
			t.Fatalf("missing integer age: value=%#v type=%T result=%#v", result["age"], result["age"], result)
		}
		ages[name] = age
	}
	if ages["Alice"] != 30 || ages["Bob"] != 41 {
		t.Fatalf("unexpected ages: %#v", ages)
	}
}

func TestQueryWithRows_UnknownConceptHandleReturnsError(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_rows_unknown_handle"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	tx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open tx: %v", err)
	}
	defer tx.Close()

	rows := NewGivenRows("p").MustAdd(ConceptGiven(Concept{Handle: "concept-does-not-exist"}))
	_, err = tx.QueryWithRows(`
given $p: person;
match $p isa person;
`, rows)
	if err == nil {
		t.Fatal("expected unknown concept handle error")
	}
	if !strings.Contains(err.Error(), "unknown concept handle") {
		t.Fatalf("expected unknown handle error, got %v", err)
	}
}

func TestQueryWithOptionsAndRows_UsesRowsAndOptions(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_rows_options"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query(`
define
  attribute name, value string;
  entity person,
    owns name @key;
`); err != nil {
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	opts := NewQueryOptions().SetPrefetchSize(1)
	defer opts.Close()
	rows := NewGivenRows("n").
		MustAdd(StringGiven("Ada")).
		MustAdd(StringGiven("Grace"))

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	if _, err := writeTx.QueryWithOptionsAndRows(`
given $n: string;
insert $p isa person, has name == $n;
`, opts, rows); err != nil {
		t.Fatalf("insert with options and rows: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit write: %v", err)
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	defer readTx.Close()
	results, err := readTx.Query(`
match $p isa person, has name $n;
fetch { "name": $n };
`)
	if err != nil {
		t.Fatalf("read names: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two inserted people, got %d: %#v", len(results), results)
	}
}

func TestQueryFetchResultsDoNotExposeOpaqueConcepts(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_fetch_no_concepts"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query(`
define
  attribute name, value string;
  entity person,
    owns name @key;
`); err != nil {
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	if _, err := writeTx.Query(`insert $p isa person, has name "Alice";`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit write: %v", err)
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	defer readTx.Close()
	results, err := readTx.Query(`
match $p isa person, has name "Alice";
fetch { "person": { $p.* } };
`)
	if err != nil {
		t.Fatalf("fetch document: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one fetch result, got %d: %#v", len(results), results)
	}
	if concept, ok := AsConcept(results[0]["person"]); ok {
		t.Fatalf("fetch document should not expose opaque concept, got %#v", concept)
	}
}

func TestQueryWithRows_UsesOpaqueRelationConcepts(t *testing.T) {
	conn, err := OpenWithTLS(testAddr(), "admin", "password", false, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	dbName := "test_given_rows_relation_concepts"
	dm := conn.Databases()
	_ = dm.Delete(dbName)
	if err := dm.Create(dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	defer dm.Delete(dbName)

	schemaTx, err := conn.Transaction(dbName, Schema)
	if err != nil {
		t.Fatalf("open schema tx: %v", err)
	}
	if _, err := schemaTx.Query(`
define
  attribute name, value string;
  attribute since, value integer;
  entity person,
    owns name @key,
    plays friendship:friend;
  relation friendship,
    relates friend @card(2),
    owns since;
`); err != nil {
		t.Fatalf("define schema: %v", err)
	}
	if err := schemaTx.Commit(); err != nil {
		t.Fatalf("commit schema: %v", err)
	}

	writeTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open write tx: %v", err)
	}
	if _, err := writeTx.Query(`
insert
  $alice isa person, has name "Alice";
  $bob isa person, has name "Bob";
  $f isa friendship, links (friend: $alice, friend: $bob);
`); err != nil {
		t.Fatalf("insert friendship: %v", err)
	}
	if err := writeTx.Commit(); err != nil {
		t.Fatalf("commit write: %v", err)
	}

	readTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open read tx: %v", err)
	}
	relationRows, err := readTx.Query(`
match $f isa friendship;
select $f;
`)
	if err != nil {
		t.Fatalf("select relation: %v", err)
	}
	readTx.Close()
	if len(relationRows) != 1 {
		t.Fatalf("expected one relation row, got %d: %#v", len(relationRows), relationRows)
	}
	relation, ok := AsConcept(relationRows[0]["f"])
	if !ok {
		t.Fatalf("expected relation concept, got %#v", relationRows[0]["f"])
	}
	if relation.Kind != "relation" {
		t.Fatalf("expected relation kind, got %#v", relation)
	}

	updateTx, err := conn.Transaction(dbName, Write)
	if err != nil {
		t.Fatalf("open update tx: %v", err)
	}
	if _, err := updateTx.QueryWithRows(`
given $f: friendship, $s: integer;
insert $f has since == $s;
`, NewGivenRows("f", "s").MustAdd(ConceptGiven(relation), IntGiven(2026))); err != nil {
		t.Fatalf("insert relation attribute with concept row: %v", err)
	}
	if err := updateTx.Commit(); err != nil {
		t.Fatalf("commit update: %v", err)
	}

	verifyTx, err := conn.Transaction(dbName, Read)
	if err != nil {
		t.Fatalf("open verify tx: %v", err)
	}
	defer verifyTx.Close()
	results, err := verifyTx.Query(`
match $f isa friendship, has since $s;
fetch { "since": $s };
`)
	if err != nil {
		t.Fatalf("verify relation attribute: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one relation attribute result, got %d: %#v", len(results), results)
	}
	since, ok := integerResult(results[0]["since"])
	if !ok || since != 2026 {
		t.Fatalf("expected since 2026, got %#v", results[0])
	}
}

func integerResult(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(v), true
	case float64:
		i := int64(v)
		return i, float64(i) == v
	default:
		return 0, false
	}
}
