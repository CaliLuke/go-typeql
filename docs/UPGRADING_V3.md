# Upgrade to go-typeql v3

Version 3 changes the Go module path and the filter API. The filter compiler now allocates every TypeQL variable (issue #138). For the full list of changes, see the [changelog](../CHANGELOG.md).

## Required changes

If you use the built-in expressions (`Abs`, `Length`, and the others) or call a built-in function with `FunctionQuery`, first upgrade the TypeDB server to 3.13.6 or later. From v3.1.0, these emit fully qualified names such as `std::math::abs`, and TypeDB 3.13.0 rejects them.

1. Update the dependency.

   ```bash
   go get github.com/CaliLuke/go-typeql/v3@v3.2.0
   ```

2. Replace `/v2` with `/v3` in every import.

   ```go
   import (
       "github.com/CaliLuke/go-typeql/v3/driver"
       "github.com/CaliLuke/go-typeql/v3/given"
       "github.com/CaliLuke/go-typeql/v3/gotype"
   )
   ```

3. Generate the models again with the v3 generator.

   ```bash
   go run github.com/CaliLuke/go-typeql/v3/tqlgen/cmd/tqlgen -schema schema.tql -out models_gen.go -pkg models
   ```

4. Rewrite `Computed` filters with typed expressions. The string form, `ArithmeticExpr`, and `BuiltinFuncExpr` are removed.

   | v2 | v3 |
   |---|---|
   | `Computed("total", ArithmeticExpr("e", "price", "*", "quantity"), ">", 100)` | `Computed(Mul(Attr("price"), Attr("quantity")), ">", 100)` |
   | `Computed("abs_bal", BuiltinFuncExpr("abs", "$e__balance"), ">", 1000)` | `Computed(Abs(Attr("balance")), ">", 1000)` |
   | `BuiltinFuncExpr("length", ...)` | `Length(...)` (TypeQL `len` in v3.0.0, `std::string::len` from v3.1.0) |

   `Attr` names an attribute, not a variable. The compiler binds the attribute, so a separate filter on it is not necessary.

5. Remove custom `Filter` types. The `Filter` interface is sealed, and `ToPatterns` is removed. Compose the built-in filters (`And`, `Or`, `Not`, `RolePlayer`, and the comparison and set filters). For TypeQL that the filters cannot express, use `Database.ExecuteRead`, `Database.ExecuteWrite`, or the `ast` package.

6. Run `go mod tidy` and the application tests.

   `go get` downloads source only. For the driver, install the v3 release archive or run `make build-rust` in the source checkout. The [installation guide](../README.md#prebuilt-ffi-library) lists the archive names and build tags.

## Behavior changes

- A filter inside `Or` or `Not` binds its own variables, and so do `NotIn` and `NotHasAttr`. For a multi-valued attribute, `And(Gt("age", 1), Not(Eq("age", 5)))` means "no age value is 5".
- A query binds each attribute once in each scope. Filters on the same attribute in the same scope still apply to the same value.
- Generated variable names are an internal detail. Do not parse them, and do not write them in raw TypeQL that you combine with filter queries. In particular:
  - Attribute variables in `or` and `not` blocks use `_s<i>` (for example `$e_s1__name`).
  - Count and aggregate queries use `$result0` for their output, not `$count` or `$result`.
