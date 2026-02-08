# API Guides

Usage guides for the go-typeql packages. For an introduction to the library, see the [project README](../../README.md). For complete API signatures, see [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql) or the [reference docs](reference/).

## Guides

| Guide                     | What you'll learn                                               |
| ------------------------- | --------------------------------------------------------------- |
| [Models](models.md)       | Defining entities and relations, struct tags, type registration |
| [CRUD](crud.md)           | Insert, Get, Update, Delete, Put, batch ops, transactions       |
| [Queries](queries.md)     | Filters, sorting, pagination, aggregations, group-by            |
| [Schema](schema.md)       | Schema generation, migration workflows, state tracking          |
| [Generator](generator.md) | tqlgen: generate Go structs from TypeQL schemas                 |
| [AST](ast.md)             | Low-level TypeQL AST for programmatic query building            |
| [Driver](driver.md)       | Rust FFI driver setup and connection                            |

## API Reference

Generated from source via `gomarkdoc`. Regenerate with:

```bash
~/go/bin/gomarkdoc ./ast/ > docs/api/reference/ast.md
~/go/bin/gomarkdoc ./gotype/ > docs/api/reference/gotype.md
~/go/bin/gomarkdoc ./tqlgen/ > docs/api/reference/tqlgen.md
```

| Package | Reference                                  |
| ------- | ------------------------------------------ |
| ast     | [reference/ast.md](reference/ast.md)       |
| gotype  | [reference/gotype.md](reference/gotype.md) |
| tqlgen  | [reference/tqlgen.md](reference/tqlgen.md) |
