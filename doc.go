// Package gotypeql provides a Go ORM for TypeDB 3.x.
//
// Define your graph schema as Go structs with struct tags, and get type-safe
// CRUD operations, a chainable query builder, schema migrations, and code
// generation — all without writing raw TypeQL.
//
// The module is organized into four packages:
//
//   - [github.com/CaliLuke/go-typeql/ast] — TypeQL AST nodes and compiler
//   - [github.com/CaliLuke/go-typeql/gotype] — ORM core: models, CRUD, queries, migrations
//   - [github.com/CaliLuke/go-typeql/tqlgen] — Code generator: TypeQL schema to Go structs
//   - [github.com/CaliLuke/go-typeql/driver] — Rust FFI bindings to typedb-driver 3.x (requires CGo)
//
// The ast, gotype, and tqlgen packages compile and test without CGo or a
// running database. Only the driver package requires the Rust FFI library.
package gotypeql
