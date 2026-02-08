# Getting Started

This walkthrough connects to TypeDB, creates a database, defines a schema, and does basic CRUD. It assumes you have TypeDB running on port 1729 (e.g. via `podman compose up -d`).

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/CaliLuke/go-typeql/driver"
    "github.com/CaliLuke/go-typeql/gotype"
)

// 1. Define models — struct tags map fields to TypeDB attributes and roles.
//    Go struct names are auto-converted to kebab-case for TypeDB
//    (UserAccount becomes "user-account").

type Person struct {
    gotype.BaseEntity
    Name  string `typedb:"name,key"`    // key = unique identifier
    Email string `typedb:"email,unique"` // unique constraint
    Age   *int   `typedb:"age"`          // pointer = optional
}

type Company struct {
    gotype.BaseEntity
    Name string `typedb:"name,key"`
}

type Employment struct {
    gotype.BaseRelation
    Employee *Person  `typedb:"role:employee"`
    Employer *Company `typedb:"role:employer"`
}

func main() {
    ctx := context.Background()

    // 2. Register types with the global registry.
    gotype.Register[Person]()
    gotype.Register[Company]()
    gotype.Register[Employment]()

    // 3. Connect to TypeDB.
    conn, err := driver.Open("localhost:1729", "admin", "password")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // 4. Create the database if it doesn't exist.
    gotype.EnsureDatabase(ctx, conn, "quickstart")

    // 5. Set up the database handle and apply schema.
    db := gotype.NewDatabase(conn, "quickstart")
    if err := gotype.MigrateFromEmpty(ctx, db); err != nil {
        log.Fatal(err)
    }

    // 6. CRUD via Manager[T].
    persons := gotype.NewManager[Person](db)
    companies := gotype.NewManager[Company](db)
    employments := gotype.NewManager[Employment](db)

    // Insert
    alice := &Person{Name: "Alice", Email: "alice@example.com"}
    persons.Insert(ctx, alice)
    // alice.GetIID() is now populated

    acme := &Company{Name: "Acme"}
    companies.Insert(ctx, acme)

    // Insert a relation (role players must exist in DB)
    emp := &Employment{Employee: alice, Employer: acme}
    employments.Insert(ctx, emp)

    // Query with filters
    results, _ := persons.Query().
        Filter(gotype.Eq("name", "Alice")).
        Execute(ctx)
    fmt.Println(results[0].Name) // "Alice"

    // Update
    age := 30
    alice.Age = &age
    persons.Update(ctx, alice)

    // Upsert (insert or update by key)
    persons.Put(ctx, &Person{Name: "Alice", Email: "new@example.com"})

    // Delete
    persons.Delete(ctx, alice)
}
```

## Next steps

- [API Reference](api/README.md) — full docs for models, CRUD, queries, filters, schema, migration
- [Development Guide](DEVELOPMENT.md) — building the driver, architecture, contributing
- [Testing Guide](TESTING.md) — test strategy and mocks
