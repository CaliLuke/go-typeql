---
name: typeql
description: Write and debug TypeQL queries for TypeDB 3.8+. Use when working with TypeDB schemas, data queries, insertions, deletions, or functions. Covers schema definition, CRUD operations, pattern matching, aggregations, and common pitfalls.
---
# TypeQL Language Reference for TypeDB 3.8+

This skill provides comprehensive guidance for writing TypeQL queries against TypeDB databases.

> **Note (3.8+):** Trailing commas are now allowed in all comma-separated contexts (variable lists, statements, reductions) for easier query composition.
>
> **Note (3.8+):** Unicode identifiers are now supported. Type names, attribute names, and variable names can use any Unicode XID_START character followed by XID_CONTINUE characters (e.g., `名前`, `prénom`, `город`).

## Quick Reference

### Transaction Types

| Type     | Use For                          | Commit           |
| -------- | -------------------------------- | ---------------- |
| `schema` | Define/undefine types, functions | Yes              |
| `write`  | Insert, update, delete data      | Yes              |
| `read`   | Match, fetch, select queries     | No (use `close`) |

### Query Structure

```text
[with ...]                   -- Inline function preamble
[define|undefine|redefine]   -- Schema operations
[match]                      -- Pattern matching
[insert|put|update|delete]   -- Data mutations
[select|sort|offset|limit]   -- Stream operators
[require|distinct]           -- Filtering operators
[reduce ... groupby]         -- Aggregations
[fetch]                      -- JSON output
;                            -- EVERY query MUST end with a semicolon
```

---

## 1. Schema Definition

Schema definitions create types and constraints. Run in `schema` transactions.

### Root Types (TypeDB 3.x)

In TypeDB 3.x, `thing` is no longer a valid root. The three roots are:

- `entity`
- `relation`
- `attribute`

### Attribute Types

```typeql
define
  # Basic value types
  attribute name, value string;
  attribute age, value integer;
  attribute salary, value double;
  attribute is_active, value boolean;
  attribute created_at, value datetime;

  # With constraints
  attribute email, value string @regex("^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$");
  attribute priority, value integer @range(1..5);
  attribute status, value string @values("pending", "active", "completed");
```

### Entity Types

```typeql
define
  # Simple entity
  entity person;

  # Entity with attributes
  entity person,
    owns name,
    owns email @key,        # Unique identifier
    owns age @card(0..1);   # Optional (0 or 1)

  # Abstract entity (cannot be instantiated)
  entity artifact @abstract,
    owns name,
    owns created_at;

  # Entity inheritance
  entity user sub artifact,
    owns email @key;
```

### Relation Types

```typeql
define
  # Basic relation with roles
  relation employment,
    relates employer,
    relates employee;

  # Entities playing roles
  entity company,
    plays employment:employer;

  entity person,
    plays employment:employee;

  # Relation with cardinality on roles
  relation manages,
    relates manager @card(1),      # Exactly one manager
    relates report @card(1..);     # One or more reports

  # Relation owning attributes
  relation employment,
    relates employer,
    relates employee,
    owns start_date,
    owns end_date @card(0..1);

  # Self-referential relation
  relation friendship,
    relates friend;  # Same role played twice

  entity person,
    plays friendship:friend;
```

### Role Overriding (Inheritance)

```typeql
define
  # Base relation
  relation relationship,
    relates partner;

  # Specialized relation with role override
  relation marriage sub relationship,
    relates spouse as partner;  # 'spouse' overrides 'partner'

  entity person,
    plays marriage:spouse;
```

### Type Aliases

```typeql
define
  # Create an alias for an existing type
  attribute username alias name;  # username is an alias for name
```

### Annotations Reference

| Annotation     | Usage                           | Description                             |
| -------------- | ------------------------------- | --------------------------------------- |
| `@key`         | `owns name @key`                | Unique identifier for type              |
| `@unique`      | `owns email @unique`            | Unique but not primary key              |
| `@card(n)`     | `owns age @card(0..1)`          | Cardinality constraint                  |
| `@regex(...)`  | `value string @regex(...)`      | Pattern validation                      |
| `@range(...)`  | `value integer @range(1..100)`  | Value range                             |
| `@values(...)` | `value string @values("a","b")` | Enum-like constraint                    |
| `@abstract`    | `entity @abstract`              | Cannot be instantiated                  |
| `@cascade`     | `owns ref @cascade`             | Cascade delete when owner deleted       |
| `@independent` | `owns tag @independent`         | Attribute exists independently of owner |
| `@distinct`    | `owns item @distinct`           | Each value can only be owned once       |
| `@subkey(...)` | `owns code @subkey(region)`     | Composite key with another attribute    |

Note:  `@key` and `@unique` can only be put on `owns`, not directly on an attribute value definition.

---

## 2. Schema Modification

### Undefine (Remove Schema Elements)

```typeql
undefine
  # Remove entire type
  person;

  # Remove capability from type
  owns email from person;
  plays employment:employee from person;
  relates employee from employment;

  # Remove annotation from type
  @abstract from artifact;

  # Remove annotation from capability
  @key from person owns email;

  # Remove role specialization
  as spouse from marriage relates spouse;

  # Remove function
  fun get_active_users;

  # Remove struct
  struct address;
```

### Redefine (Modify Existing Schema)

```typeql
redefine
  # Change annotation on type
  entity person @abstract;

  # Change annotation on capability
  person owns age @card(1..1);  # Make required

  # Redefine function (replaces entirely)
  fun count_users() -> integer:
    match $u isa user, has status "active";
    return count;
```

---

## 3. Data Operations (CRUD)

### Insert

```typeql
# Insert entity with attributes
insert
  $p isa person,
    has name "Alice",
    has email "alice@example.com";

# Insert using anonymous variable (when reference not needed)
insert
  $_ isa person, has name "Bob";

# Insert relation (match entities first)
match
  $p isa person, has email "alice@example.com";
  $c isa company, has name "Acme Inc";
insert
  $_ isa employment (employer: $c, employee: $p),
    has start_date 2024-01-15;
```

### Put (Upsert)

```typeql
# Put creates if not exists, matches if exists
# Useful for idempotent operations
match
  $c isa company, has name "Acme Inc";
put
  $p isa person, has email "alice@example.com", has name "Alice";
insert
  employment (employer: $c, employee: $p);

# Put finds existing person by email (key) or creates new one
# Then insert creates the relation
```

### Update

```typeql
# Update single-cardinality attribute (implicit replacement)
match
  $p isa person, has email "alice@example.com";
update
  $p has email "alice.smith@example.com";

# Update multi-cardinality: delete old, insert new
match
  $p isa person, has name "Alice";
  $p has nickname $old;
delete
  $p has $old;
insert
  $p has nickname "Ally";
```

### Delete

```typeql
# Delete entity
match
  $p isa person, has email "alice@example.com";
delete
  $p;

# Delete attribute from entity
match
  $p isa person, has email "alice@example.com", has nickname $n;
delete
  $p has $n;

# Alternative: delete attribute using 'of'
match
  $p isa person, has email "alice@example.com", has nickname $n;
delete
  has $n of $p;

# Delete relation
match
  $p isa person, has email "alice@example.com";
  $c isa company, has name "Acme Inc";
  $e isa employment (employer: $c, employee: $p) ;
delete
  $e;

# Delete role player from relation (keeps relation)
match
  $rel isa membership (member: $old_member, group: $g) ;
  $old_member has email "alice@example.com";
delete
  links ($old_member) of $rel;

# Optional delete (try) - no error if not found
match
  $p isa person, has email "alice@example.com";
delete
  try {
    $p has nickname $n;
  };
```

---

## 4. Querying Data

### Match + Fetch (Primary Query Pattern)

```typeql
# Fetch specific attributes
match
  $p isa person, has name $n, has email $e;
fetch {
  "name": $n,
  "email": $e
}

# Fetch all attributes of entity
match
  $p isa person, has email "alice@example.com";
fetch {
  "person": { $p.* }
}

# Fetch with attribute projection
match
  $p isa person;
fetch {
  "name": $p.name,
  "email": $p.email
}

# Fetch multi-valued attributes as list
match
  $p isa person, has email "alice@example.com";
fetch {
  "name": $p.name,
  "all_nicknames": [ $p.nickname ]
}

# Nested fetch for related data
match
  $c isa company, has name "Acme Inc";
fetch {
  "company": $c.name,
  "employees": [
    match
      employment (employer: $c, employee: $p);
    fetch {
      "name": $p.name
    }
  ]
}

# Fetch with inline function
match
  $c isa company;
fetch {
  "company": $c.name,
  "employee_count": (
    match
      (employer: $c, employee: $e) isa employment;
    reduce $count = count;
  )
}
```

### Filtering Patterns

```typeql
# Filter by type
match $x isa person;

# Filter by exact type (not subtypes)
match $x isa! person;

# Filter by attribute value
match $p isa person, has age > 30;

# Filter by relation
match
  $c isa company, has name "Acme Inc";
  employment (employer: $c, employee: $p);

# Filter by relation with relation variable
match
  $c isa company, has name "Acme Inc";
  $rel isa employment, links (employer: $c, employee: $p);

# Comparison operators: ==, !=, <, <=, >, >=, like, contains
match
  $p isa person, has name $n;
  $n like "^A.*";  # Regex match

match
  $p isa person, has bio $b;
  $b contains "engineer";  # Substring match
```

### Identity Check (is)

```typeql
# Check if two variables refer to the same concept
match
  $p1 isa person;
  $p2 isa person;
  friendship (friend: $p1, friend: $p2);
  not { $p1 is $p2; };  # Exclude self-friendship
fetch { "person": $p1.name }
```

### Conjunction (Grouping with AND)

```typeql
# Explicit grouping with curly braces
match
  $p isa person;
  {
    $p has age > 18;
    $p has status "active";
  };  # Both conditions must be true
```

### Disjunction (OR)

```typeql
match
  $p isa person, has email $e;
  {
    $p has name "Alice";
  } or {
    $p has name "Bob";
  };
fetch { "email": $e }
```

### Negation (NOT)

```typeql
# Find persons not employed by Acme
match
  $p isa person;
  not {
    $c isa company, has name "Acme Inc";
    employment (employer: $c, employee: $p);
  };
fetch { "unemployed": $p.name }
```

### Optional Patterns (TRY)

```typeql
# Match with optional pattern
match
  $p isa person, has name $n;
  try {
    $p has email $e;
  };
fetch {
  "name": $n,
  "email": $e  # May be null if no email
}
```

---

## 5. Stream Operators

Order must be: `sort`, `offset`, `limit`

```typeql
# Sort results
match
  $p isa person, has name $n;
sort $n asc;

# Sort descending
match
  $p isa person, has age $a;
sort $a desc;

# Pagination
match
  $p isa person, has name $n;
sort $n asc;
offset 10;
limit 10;

# Select specific variables (like SQL SELECT)
match
  $p isa person, has name $n, has email $e;
select $n, $e;

# Distinct results
match
  $p isa person, has name $n;
distinct;

# Require variables to be bound (filter nulls from try)
match
  $p isa person, has name $n;
  try { $p has email $e; };
require $e;  # Only return rows where email exists
```

---

## 6. Aggregations

```typeql
# Count
match
  $p isa person;
reduce $count = count;

# Count specific variable
match
  $p isa person, has email $e;
reduce $count = count($e);

# Multiple aggregations
match
  $p isa person, has salary $s;
reduce
  $max = max($s),
  $min = min($s),
  $avg = mean($s);

# Group by
match
  $c isa company;
  employment (employer: $c, employee: $e);
reduce
  $count = count groupby $c;

# Multiple groupby variables
match
  $c isa company, has industry $ind;
  employment (employer: $c, employee: $e);
  $e has department $dept;
reduce
  $count = count groupby $ind, $dept;
```

### Available Reducers

| Reducer        | Usage                    | Description        |
| -------------- | ------------------------ | ------------------ |
| `count`        | `count` or `count($var)` | Count results      |
| `sum($var)`    | `sum($salary)`           | Sum numeric values |
| `min($var)`    | `min($age)`              | Minimum value      |
| `max($var)`    | `max($age)`              | Maximum value      |
| `mean($var)`   | `mean($score)`           | Average            |
| `median($var)` | `median($score)`         | Median             |
| `std($var)`    | `std($score)`            | Standard deviation |
| `list($var)`   | `list($name)`            | Collect into list  |

---

## 7. Expressions and Computed Values

### Arithmetic Operators

```typeql
match
  $p isa product, has price $price, has quantity $qty;
let $subtotal = $price * $qty;           # Multiplication
let $with_tax = $subtotal * 1.1;         # More multiplication
let $discount = $subtotal / 10;          # Division
let $final = $subtotal - $discount;      # Subtraction
let $total = $final + 5.00;              # Addition (shipping)
let $squared = $price ^ 2;               # Power/exponent
let $remainder = $qty % 3;               # Modulo
fetch {
  "product": $p.name,
  "total": $total
}
```

### Assignment and Literals

```typeql
match
  $p isa product, has price $price;
let $vat_rate = 0.2;                     # Assign literal
let $price_with_vat = $price * (1 + $vat_rate);
fetch {
  "price_with_vat": $price_with_vat
}
```

### Built-in Functions

```typeql
match
  $p isa product, has price $price, has name $name;
# NOTE: ceil, floor, round only work on double/decimal, not integers
let $rounded = round($price);            # Round to nearest integer
let $ceiling = ceil($price);             # Round up
let $floored = floor($price);            # Round down
let $absolute = abs($price - 100);       # Absolute value
let $name_len = len($name);              # String length (note: len, not length)
let $higher = max($price, 10.0);         # Maximum of two values
let $lower = min($price, 100.0);         # Minimum of two values
fetch { "stats": { $rounded, $ceiling, $floored } }

# String concatenation
match
  $u isa user, has first_name $fn, has last_name $ln;
let $full = concat($fn, " ", $ln);
fetch { "full_name": $full }

# Get IID of a concept (3.8+)
match
  $p isa person, has email "alice@example.com";
fetch {
  "iid": iid($p)                         # Get internal identifier
}

# Get type label (3.8+) - NOTE: label() works on TYPE variables, not instances!
# Must bind the exact type first using isa! and a type variable
match
  $p isa! $t, has email "alice@example.com";
  $t sub person;                         # Bind $t to exact type of $p
fetch {
  "iid": iid($p),
  "type": label($t)                      # label() on TYPE variable $t
}
```

---

## 8. List Operations

### List Literals

```typeql
match
  $p isa person, has name $name;
let $tags = ["active", "verified", "premium"];  # List literal
fetch { "name": $name, "tags": $tags }
```

### List Indexing

```typeql
match
  $p isa person, has score $scores;  # Multi-valued attribute
let $first = $scores[0];             # First element (0-indexed)
let $second = $scores[1];            # Second element
fetch { "first_score": $first }
```

### List Slicing

```typeql
match
  $p isa person, has score $scores;
let $top_three = $scores[0..3];      # Elements 0, 1, 2
let $rest = $scores[3..10];          # Elements 3 through 9
fetch { "top_scores": $top_three }
```

---

## 9. Structs

### Struct Definition

```typeql
define
  # Define a struct type
  struct address:
    street value string,
    city value string,
    zip value string?,      # Optional field (nullable)
    country value string;
```

WARNING: structs are not yet implemented as of TypeDB 3.8.0, so should not be used.

### Using Structs

```typeql
# Insert with struct value
insert
  $p isa person,
    has name "Alice",
    has home_address { street: "123 Main St", city: "Boston", zip: "02101", country: "USA" };

# Match struct fields
match
  $p isa person, has home_address $addr;
  $addr isa address { city: "Boston" };
fetch { "person": $p.name }
```

### Struct Destructuring

```typeql
# Destructure struct in let
match
  $p isa person, has home_address $addr;
let { city: $city, zip: $zip } = $addr;
fetch {
  "person": $p.name,
  "city": $city
}
```

---

## 10. Functions

Define reusable query logic in schema.

### Function Definition

```typeql
define

# Return stream of values
fun get_active_users() -> { string }:
  match
    $u isa user, has status == "active", has email $e;
  return { $e };

# Return single value with aggregation
fun count_users() -> integer:
  match
    $u isa user;
  return count;

# Function with parameters
fun get_user_projects($user_email: string) -> { string }:
  match
    $u isa user, has email == $user_email;
    membership (member: $u, project: $p);
    $p has name $name;
  return { $name };

# Return multiple values in stream
fun get_user_details($email: string) -> { string, string }:
  match
    $u isa user, has email == $email, has name $n, has role $r;
  return { $n, $r };

# Return first match only
fun get_oldest_user() -> string:
  match
    $u isa user, has name $n, has age $a;
  sort $a desc;
  return first $n;

# Return last match only
fun get_youngest_user() -> string:
  match
    $u isa user, has name $n, has age $a;
  sort $a desc;
  return last $n;

# Return boolean (existence check)
fun user_exists($email: string) -> boolean:
  match
    $u isa user, has email == $email;
  return check;  # Returns true if match found, false otherwise
```

### Using Functions

```typeql
# Call function in match with 'let ... in'
match
  let $emails in get_active_users();
fetch { "active_emails": $emails }

# Call function with parameter
match
  let $projects in get_user_projects("alice@example.com");
fetch { "projects": $projects }

# Call function returning single value
match
  let $count in count_users();
fetch { "total_users": $count }

# Use function in expression
match
  $u isa user;
  let $exists in user_exists($u.email);
fetch { "user": $u.name, "verified": $exists }
```

### Inline Functions with WITH

```typeql
# Define function inline for single query
with
  fun active_in_dept($dept: string) -> { user }:
    match
      $u isa user, has department == $dept, has status "active";
    return { $u };

match
  let $user in active_in_dept("Engineering");
fetch { "engineer": $user.name }
```

---

## 11. IID (Internal Identifier) Operations

```typeql
# Match by IID (for direct lookups)
match
  $entity iid 0x1f0005000000000000012f;
fetch { "data": { $entity.* } }

# Get IID using iid() function (3.8+)
match
  $p isa person, has email "alice@example.com";
fetch {
  "person": $p.name,
  "iid": iid($p)
}

# Get IID and exact type label together (3.8+)
# NOTE: label() requires a TYPE variable, use isa! to bind it
match
  $p isa! $t, has email "alice@example.com";
  $t sub person;
fetch {
  "iid": iid($p),
  "type": label($t)
}
```

---

## 12. Rules (Inference)
TypeDB 3.0 and on no longer uses rules, and uses only functions instead.

---

## 13. Common Patterns

Note: each clause is not itself terminated with a trailing semicolon, only each statement within a clause is.

### Upsert (Insert if not exists)

```typeql
# Use 'put' for upsert behavior
match
  $c isa company, has name "Acme Inc";
put
  $p isa person, has email "new@example.com", has name "New Person";
insert
  employment (employer: $c, employee: $p);
```

### Polymorphic Queries

```typeql
# Query abstract supertype to get all subtypes
match
  $artifact isa artifact, has name $n;  # Gets all subtypes
fetch { "name": $n }

# Query exact type only (not subtypes)
match
  $artifact isa! artifact, has name $n;  # Only direct instances
fetch { "name": $n }
```

### Graph Traversal

```typeql
# Find all connected nodes (1-hop)
match
  $center isa entity, has id == "target-id";
  $rel links ($center), links ($neighbor);
  not { $neighbor is $center; };
fetch {
  "center": $center.id,
  "neighbor": $neighbor.id
}
```

### Existence Check

```typeql
# Check if pattern exists
match
  $u isa user, has email "alice@example.com";
  not { (member: $u) isa team_membership; };
# Returns results only if user exists but has no team
```

---

## 14. Critical Pitfalls

### TypeDB 3 relation syntax

Relations in TypeDB 3.x use the follow syntax only:

Anonyous relation syntax:
**always use this if you don't need a relation instance variable**
```
<rel-type> (<role-type>: <player var>, <role-type-2>, <player var 2>, ...);
```

Variabilized relation syntax (with a variable for the relation instance):
**only use this if you need a variable for the relation instance**
```
$rel-var isa <rel-type>,
  links (<role-type>: <player var>, <role-type-2>, <player var 2>, ...);
```

### Reserved keywords

These are reserved identifiers. **Never use them as user-defined identifier like type labels or variables, in any capitalization. If required, append _ instead**:

with, match, fetch, update, define, undefine, redefine, insert, put, delete, end , entity, relation, attribute, role , asc, desc, struct, fun, return, alias, sub, owns, as, plays, relates, iid, isa, links, has, is, or, not, try, in, true, false, of, from, first, last

### Match Clauses are Simultaneous (AND)

All statements in `match` must be satisfied together. Not sequential.

```typeql
# WRONG assumption: "get all books, then find promotions"
# ACTUAL: Only returns books that ARE in promotions
match
  $b isa book;
  promotion (book: $b, promo: $p);
```

### Unconstrained Variables = Cross Join

```typeql
# PROBLEM: Returns every book x every promotion (Cartesian product)
match
  $b isa book;
  $p isa promotion;  # Not linked to $b!

# FIX: Link variables through relations
match
  $b isa book;
  book_promotion (book: $b, promotion: $p);
```

### Symmetric Relations Return Duplicates

```typeql
# PROBLEM: Returns (A,B) and (B,A)
match
  friendship (friend: $p1, friend: $p2);

# FIX: Add asymmetric constraint
match
  $p1 has email $e1;
  $p2 has email $e2;
  friendship (friend: $p1, friend: $p2);
  $e1 < $e2;  # Each pair only once
```

### Cardinality Validated at Commit

Insert violations don't fail immediately - only on commit.

### Sorting Loads All Results

`sort` requires loading all matching data into memory before pagination.

### Variable Scoping in OR/NOT

Variables inside `or` blocks are not guaranteed bound outside:

```typeql
# INVALID: $company might not be bound
match
  $p isa person;
  {
    employment (employee: $p, employer: $company);
  } or {
    $p has status "freelancer";
  };
  not { $company has name "BadCorp"; };  # $company may be unbound!

# VALID: Bind variable in parent scope first
match
  $p isa person;
  employment (employee: $p, employer: $company);
  not { $company has name "BadCorp"; };
```

### Variable Reuse in OR Branches

```typeql
# INVALID: $x means different things in each branch
match {
    editing (person: $p, document: $x);
} or {
    messaging (person: $p, message: $x);
};

# VALID: Use different variable names
match {
    editing (person: $p, document: $doc);
} or {
    messaging (person: $p, message: $msg) ;
};
```

---

## 15. CLI Notes

### Command Execution

```bash
# Server commands: NO semicolon
typedb console --command "database list"

# TypeQL in transactions: WITH semicolons
typedb console --command "transaction mydb schema" --command "define entity person;"
```

### Script Files (.tqls)

```text
# Console commands without semicolons
transaction mydb schema
define
  entity person;
commit
```

### Transaction Lifecycle

- `schema`, `write` transactions: use `commit`
- `read` transactions: use `close`

---

## 16. Value Types Reference

| TypeQL Type   | Description            | Example Literal                        |
| ------------- | ---------------------- | -------------------------------------- |
| `string`      | UTF-8 text             | `"hello"`                              |
| `boolean`     | true/false             | `true`, `false`                        |
| `integer`     | 64-bit signed int      | `42`, `-17`                            |
| `double`      | Double-precision float | `3.14159`                              |
| `decimal`     | Precise decimal        | `99.99dec`                             |
| `date`        | Date only              | `2024-01-15`                           |
| `datetime`    | Date and time          | `2024-01-15T10:30:00`                  |
| `datetime-tz` | With timezone          | `2024-01-15T10:30:00 America/New_York` |
| `duration`    | Time duration          | `P1Y2M3D`, `PT4H30M`                   |

Note: `0dec` is not valid syntax in some versions of TypeDB. Always use `0.0dec`.


### Duration Format (ISO 8601)

```text
P[n]Y[n]M[n]DT[n]H[n]M[n]S
P1Y2M3D     = 1 year, 2 months, 3 days
PT4H30M     = 4 hours, 30 minutes
P1W         = 1 week
PT1H30M45S  = 1 hour, 30 minutes, 45 seconds
```

---

## 17. Debugging Queries

### Test Match Before Write

```typeql
# Before running delete:
match
  $u isa user, has status "inactive";
fetch { "will_delete": $u.email }

# Then execute:
match
  $u isa user, has status "inactive";
delete $u;
```

### Check Schema

```typeql
# In read transaction - list entity types
match
  $type sub entity;
fetch { "entity_types": $type }

# List relation types
match
  $rel sub relation;
fetch { "relation_types": $rel }

# List attribute types
match
  $attr sub attribute;
fetch { "attribute_types": $attr }
```

### Verify Cardinality

```typeql
# Count instances per type
match
  $type sub entity;
  $instance isa! $type;
reduce $count = count groupby $type;
```

---

## 18. Complete Operator Reference

### Comparison Operators

| Operator   | Description           | Example               |
| ---------- | --------------------- | --------------------- |
| `==`       | Equal                 | `$age == 30`          |
| `!=`       | Not equal             | `$status != "done"`   |
| `<`        | Less than             | `$age < 18`           |
| `<=`       | Less than or equal    | `$age <= 65`          |
| `>`        | Greater than          | `$salary > 50000`     |
| `>=`       | Greater than or equal | `$score >= 90`        |
| `like`     | Regex match           | `$name like "^A.*"`   |
| `contains` | Substring match       | `$bio contains "dev"` |

### Math Operators

| Operator | Description    | Example   |
| -------- | -------------- | --------- |
| `+`      | Addition       | `$a + $b` |
| `-`      | Subtraction    | `$a - $b` |
| `*`      | Multiplication | `$a * $b` |
| `/`      | Division       | `$a / $b` |
| `%`      | Modulo         | `$a % $b` |
| `^`      | Power          | `$a ^ 2`  |

### Expression Functions

| Function     | Description                      | Example             |
| ------------ | -------------------------------- | ------------------- |
| `abs($x)`    | Absolute value                   | `abs($n - 10)`      |
| `ceil($x)`   | Round up (double/decimal only)   | `ceil($price)`      |
| `floor($x)`  | Round down (double/decimal only) | `floor($price)`     |
| `round($x)`  | Round nearest (double/decimal)   | `round($price)`     |
| `len($s)`    | String length                    | `len($name)`        |
| `max($a,$b)` | Maximum of two values            | `max($x, 0)`        |
| `min($a,$b)` | Minimum of two values            | `min($x, 100)`      |
| `iid($var)`  | Get internal identifier (3.8+)   | `iid($p)`           |
| `label($t)`  | Get type label (TYPE var only!)  | `label($t)`         |
| `concat(…)`  | Concatenate strings              | `concat($a," ",$b)` |

### Pattern Keywords

| Keyword | Description                          |
| ------- | ------------------------------------ |
| `isa`   | Type check (includes subtypes)       |
| `isa!`  | Exact type check (excludes subtypes) |
| `has`   | Attribute ownership                  |
| `links` | Role player in relation              |
| `is`    | Identity comparison                  |
| `or`    | Disjunction                          |
| `not`   | Negation                             |
| `try`   | Optional pattern                     |
