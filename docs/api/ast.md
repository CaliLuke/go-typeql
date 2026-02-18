# AST Package

`import "github.com/CaliLuke/go-typeql/ast"` -- [pkg.go.dev](https://pkg.go.dev/github.com/CaliLuke/go-typeql/ast)

The `ast` package provides TypeQL Abstract Syntax Tree nodes and a compiler for programmatic query building. It operates at a lower level than the ORM layer, giving full control over TypeQL query construction.

## When to Use

- **Use the ORM layer** (`Manager[T]`, `Query[T]`) for standard CRUD and queries on registered models.
- **Use the AST** when you need queries that the ORM doesn't support: custom fetch structures, raw match-let-reduce pipelines, multi-variable patterns, or schema queries.

## Node Type Hierarchy

All AST nodes implement the `QueryNode` marker interface. Specialized interfaces organize them:

```text
QueryNode
├── Value          — literal values, function calls, arithmetic
├── Constraint     — has, isa, iid constraints
├── Pattern        — entity, relation, has, comparison, not, or patterns
├── Statement      — has, isa, relation, delete statements
├── Clause         — match, insert, delete, update, fetch, reduce clauses
└── FetchItem      — fetch attribute, variable, list, function, wildcard
```

## Builder Helpers

The package provides builder functions so you can construct AST nodes ergonomically instead of using verbose struct literals:

```go
// Instead of verbose struct initialization:
match := ast.MatchClause{
    Patterns: []ast.Pattern{
        ast.EntityPattern{
            Variable: "$p", TypeName: "person",
            Constraints: []ast.Constraint{
                ast.HasConstraint{AttrName: "name", Value: ast.LiteralValue{Val: "Alice", ValueType: "string"}},
            },
        },
    },
}

// Use builders:
match := ast.Match(
    ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
)
```

The builders are organized by category:

- **Clauses**: `Match`, `Insert`, `Put`, `Delete`, `Update`, `Fetch`, `Select`, `Sort`, `Offset`, `Limit`
- **Patterns**: `Entity`, `Relation`, `Role`, `Cmp`, `Or`
- **Constraints**: `Has`, `Isa`, `IsaExact`, `Iid`
- **Values**: `Str`, `Long`, `Double`, `Bool`, `Lit`, `FuncCall`, `ValueFromGo`
- **Statements**: `IsaStmt`, `HasStmt`, `RelationStmt`, `DeleteHas`
- **Fetch Items**: `FetchAttr`, `FetchAttrPath`, `FetchVar`, `FetchFunc`

## Compiling to TypeQL

The `Compiler` converts AST nodes to TypeQL strings. It accepts any `QueryNode` and dispatches by type:

```go
c := &ast.Compiler{}
typeql, err := c.Compile(node)
```

`CompileBatch` compiles multiple nodes joined by newlines, optionally wrapped with an operation keyword.

## Value Formatting

Two functions handle converting Go values to TypeQL literals:

- `FormatGoValue(value any) string` -- converts a Go value using reflection. This is the canonical formatting function; other packages delegate to it.
- `FormatLiteral(val any, valueType string) string` -- formats using an explicit TypeQL value type (`"string"`, `"long"`, `"double"`, `"boolean"`, `"datetime"`).

## Examples

### Match-Fetch Query

```go
import "github.com/CaliLuke/go-typeql/ast"

// Build: match $p isa person, has name "Alice";
//        fetch { "name": $p.name, "email": $p.email };
match := ast.Match(
    ast.Entity("$p", "person", ast.Has("name", ast.Str("Alice"))),
)

fetch := ast.Fetch(
    ast.FetchAttr("name", "$p", "name"),
    ast.FetchAttr("email", "$p", "email"),
)

c := &ast.Compiler{}
matchStr, _ := c.Compile(match)
fetchStr, _ := c.Compile(fetch)
query := matchStr + "\n" + fetchStr
```

### Insert Entity

```go
insert := ast.Insert(
    ast.IsaStmt("$p", "person"),
    ast.HasStmt("$p", "name", ast.Str("Bob")),
    ast.HasStmt("$p", "email", ast.Str("bob@example.com")),
)

c := &ast.Compiler{}
typeql, _ := c.Compile(insert)
// insert
// $p isa person;
// $p has name "Bob";
// $p has email "bob@example.com";
```

### Delete Attribute

```go
// Build: match $p isa person, has email "bob@example.com", has age $old;
//        delete $old of $p;
match := ast.Match(
    ast.Entity("$p", "person", ast.Has("email", ast.Str("bob@example.com"))),
    ast.HasPattern{ThingVar: "$p", AttrType: "age", AttrVar: "$old"},
)

delete := ast.Delete(
    ast.DeleteHas("$old", "$p"),
)

c := &ast.Compiler{}
matchStr, _ := c.Compile(match)
deleteStr, _ := c.Compile(delete)
query := matchStr + "\n" + deleteStr
```

### Aggregation

Note that TypeDB uses `mean` (not `avg`) for average aggregation.

```go
match := ast.Match(
    ast.Entity("$p", "person"),
    ast.HasPattern{ThingVar: "$p", AttrType: "age", AttrVar: "$age"},
)

reduce := ast.ReduceClause{
    Assignments: []ast.ReduceAssignment{
        {
            Variable:   "$avg",
            Expression: ast.FuncCall("mean", "$age"),
        },
    },
}

c := &ast.Compiler{}
matchStr, _ := c.Compile(match)
reduceStr, _ := c.Compile(reduce)
// match
// $p isa person;
// $p has age $age;
// reduce $avg = mean($age);
```

### Arithmetic Expressions

```go
// Build: ($price * $quantity)
arith := ast.ArithmeticValue{
    Left:     "$price",
    Operator: "*",
    Right:    "$quantity",
}
```

Supported operators: `+`, `-`, `*`, `/`, `%`, `^`.

### Function Calls

```go
// Build: abs($balance)
funcCall := ast.FunctionCallValue{
    Function: "abs",
    Args:     []any{"$balance"},
}

// Build: round($score, 2)
round := ast.FuncCall("round", "$score", ast.Long(2))
```

### Let Assignments

Computed variable bindings in match clauses:

```go
matchLet := ast.MatchLetClause{
    Assignments: []ast.LetAssignment{
        {
            Variables:  []string{"$total"},
            Expression: ast.ArithmeticValue{Left: "$price", Operator: "*", Right: "$qty"},
        },
    },
}
```

### Insert Relation

```go
insert := ast.Insert(
    ast.RelationStmt("employment",
        ast.Role("employee", "$p"),
        ast.Role("employer", "$c"),
    ),
)

c := &ast.Compiler{}
typeql, _ := c.Compile(insert)
```
