// Package ast defines the Abstract Syntax Tree (AST) for TypeQL queries.
//
// It decouples query construction from string formatting, providing a
// structured way to build TypeQL queries programmatically.
package ast

// QueryNode is the marker interface for all AST nodes.
type QueryNode interface {
	queryNode()
}

// --- Values ---

// Value is the marker interface for value nodes that can be used in expressions.
type Value interface {
	QueryNode
	value()
}

// FunctionCallValue represents a function call in TypeQL, such as iid($x).
type FunctionCallValue struct {
	// Function is the name of the function to call.
	Function string
	// Args contains the arguments for the function, which can be Value nodes or variable strings.
	Args []any
}

func (FunctionCallValue) queryNode() {}
func (FunctionCallValue) value()     {}

// LiteralValue represents a literal value such as a string, number, or boolean.
type LiteralValue struct {
	// Val is the actual value.
	Val any
	// ValueType specifies the TypeQL type of the value (e.g., "string", "long", "boolean").
	ValueType string
}

func (LiteralValue) queryNode() {}
func (LiteralValue) value()     {}

// ArithmeticValue represents a binary arithmetic operation between two values.
// It supports TypeQL infix operators like +, -, *, /, %, and ^.
type ArithmeticValue struct {
	// Left is the left operand (Value or variable string).
	Left any
	// Operator is the infix operator (+, -, *, /, %, ^).
	Operator string
	// Right is the right operand (Value or variable string).
	Right any
}

func (ArithmeticValue) queryNode() {}
func (ArithmeticValue) value()     {}

// --- Role Players ---

// RolePlayer represents a role player in a relation, mapping a role name to a player variable.
type RolePlayer struct {
	// Role is the name of the role being played.
	Role string
	// PlayerVar is the variable name of the entity or relation playing the role (e.g., "$p").
	PlayerVar string
}

func (RolePlayer) queryNode() {}

// --- Constraints ---

// Constraint is the marker interface for constraints applied to variables in a pattern.
type Constraint interface {
	QueryNode
	constraint()
}

// IidConstraint matches a thing by its internal instance ID (IID).
type IidConstraint struct {
	// IID is the unique instance identifier.
	IID string
}

func (IidConstraint) queryNode()  {}
func (IidConstraint) constraint() {}

// HasConstraint checks if a thing has a specific attribute with a given value.
type HasConstraint struct {
	// AttrName is the name of the attribute type.
	AttrName string
	// Value is the value of the attribute (Value or variable string).
	Value any
}

func (HasConstraint) queryNode()  {}
func (HasConstraint) constraint() {}

// IsaConstraint checks if a thing is an instance of a specific type.
type IsaConstraint struct {
	// TypeName is the name of the type.
	TypeName string
	// Strict indicates whether to use strict type checking (isa!).
	Strict bool
}

func (IsaConstraint) queryNode()  {}
func (IsaConstraint) constraint() {}

// --- Patterns ---

// Pattern is the marker interface for patterns used in a match clause.
type Pattern interface {
	QueryNode
	pattern()
}

// EntityPattern matches an entity with an optional type and constraints.
type EntityPattern struct {
	// Variable is the variable name for the matched entity.
	Variable string
	// TypeName is the name of the entity type.
	TypeName string
	// Constraints are additional constraints applied to the entity.
	Constraints []Constraint
	// IsStrict indicates whether to use strict type checking (isa!).
	IsStrict bool
}

func (EntityPattern) queryNode() {}
func (EntityPattern) pattern()   {}

// RelationPattern matches a relation with its role players and optional constraints.
type RelationPattern struct {
	// Variable is the variable name for the matched relation.
	Variable string
	// TypeName is the name of the relation type.
	TypeName string
	// RolePlayers defines the participants in the relation.
	RolePlayers []RolePlayer
	// Constraints are additional constraints applied to the relation.
	Constraints []Constraint
}

func (RelationPattern) queryNode() {}
func (RelationPattern) pattern()   {}

// SubTypePattern matches types that are subtypes of a parent type ($t sub type).
type SubTypePattern struct {
	// Variable is the variable representing the subtype.
	Variable string
	// ParentType is the name of the parent type.
	ParentType string
}

func (SubTypePattern) queryNode() {}
func (SubTypePattern) pattern()   {}

// AttributePattern matches an attribute of a specific type and optionally its value.
type AttributePattern struct {
	// Variable is the variable name for the attribute.
	Variable string
	// TypeName is the name of the attribute type.
	TypeName string
	// Value is the optional value of the attribute.
	Value Value
}

func (AttributePattern) queryNode() {}
func (AttributePattern) pattern()   {}

// HasPattern represents a pattern where a thing has an attribute assignment ($x has Type $v).
type HasPattern struct {
	// ThingVar is the variable representing the thing (entity or relation).
	ThingVar string
	// AttrType is the type of the attribute.
	AttrType string
	// AttrVar is the variable representing the attribute instance.
	AttrVar string
}

func (HasPattern) queryNode() {}
func (HasPattern) pattern()   {}

// ValueComparisonPattern represents a comparison between a variable and a value ($v > 10).
type ValueComparisonPattern struct {
	// Var is the variable being compared.
	Var string
	// Operator is the comparison operator (e.g., >, <, ==).
	Operator string
	// Value is the value to compare against (Value or variable string).
	Value any
}

func (ValueComparisonPattern) queryNode() {}
func (ValueComparisonPattern) pattern()   {}

// NotPattern represents a negation of one or more patterns (not { ... }).
type NotPattern struct {
	// Patterns are the patterns to negate.
	Patterns []Pattern
}

func (NotPattern) queryNode() {}
func (NotPattern) pattern()   {}

// OrPattern represents a disjunction of multiple pattern alternatives ({ ... } or { ... }).
type OrPattern struct {
	// Alternatives defines the multiple sets of patterns that satisfy the disjunction.
	Alternatives [][]Pattern
}

func (OrPattern) queryNode() {}
func (OrPattern) pattern()   {}

// IidPattern represents a pattern matching a thing by its IID ($x iid 0x...).
type IidPattern struct {
	// Variable is the variable representing the thing.
	Variable string
	// IID is the instance ID string.
	IID string
}

func (IidPattern) queryNode() {}
func (IidPattern) pattern()   {}

// RawPattern represents a raw TypeQL string pattern, typically for legacy support.
type RawPattern struct {
	// Content is the raw TypeQL string.
	Content string
}

func (RawPattern) queryNode() {}
func (RawPattern) pattern()   {}

// --- Statements ---

// Statement is the marker interface for statements used in insert, delete, or update clauses.
type Statement interface {
	QueryNode
	statement()
}

// RawStatement represents a raw TypeQL string statement.
type RawStatement struct {
	// Content is the raw TypeQL string.
	Content string
}

func (RawStatement) queryNode() {}
func (RawStatement) statement() {}

// HasStatement assigns an attribute value to a subject variable.
type HasStatement struct {
	// SubjectVar is the variable representing the thing being assigned an attribute.
	SubjectVar string
	// AttrName is the name of the attribute type.
	AttrName string
	// Value is the value being assigned.
	Value Value
}

func (HasStatement) queryNode() {}
func (HasStatement) statement() {}

// IsaStatement defines the type of a variable in an insert statement.
type IsaStatement struct {
	// Variable is the variable name.
	Variable string
	// TypeName is the name of the type.
	TypeName string
}

func (IsaStatement) queryNode() {}
func (IsaStatement) statement() {}

// RelationStatement defines a relation and its participants for an insert statement.
// In TypeDB 3.x, relations in insert statements often don't use a variable prefix.
type RelationStatement struct {
	// Variable is the optional variable name for the relation.
	Variable string
	// TypeName is the name of the relation type.
	TypeName string
	// RolePlayers defines the participants in the relation.
	RolePlayers []RolePlayer
	// IncludeVariable indicates whether to include the variable prefix in the compiled query.
	IncludeVariable bool
	// Attributes are inline attribute assignments for the relation.
	Attributes []HasStatement
}

func (RelationStatement) queryNode() {}
func (RelationStatement) statement() {}

// DeleteThingStatement represents a statement to delete a thing instance identified by a variable.
type DeleteThingStatement struct {
	// Variable is the variable representing the thing to delete.
	Variable string
}

func (DeleteThingStatement) queryNode() {}
func (DeleteThingStatement) statement() {}

// DeleteHasStatement represents a statement to delete an attribute from its owner.
// Compiles to: $attrVar of $ownerVar
type DeleteHasStatement struct {
	// AttrVar is the variable representing the attribute to delete (e.g., "$old").
	AttrVar string
	// OwnerVar is the variable representing the owner entity/relation (e.g., "$e").
	OwnerVar string
}

func (DeleteHasStatement) queryNode() {}
func (DeleteHasStatement) statement() {}

// --- Clauses ---

// Clause is the marker interface for top-level TypeQL clauses.
type Clause interface {
	QueryNode
	clause()
}

// MatchClause represents a 'match' clause containing one or more patterns.
type MatchClause struct {
	// Patterns are the patterns to match.
	Patterns []Pattern
}

func (MatchClause) queryNode() {}
func (MatchClause) clause()    {}

// LetAssignment represents an assignment in a 'match let' clause.
type LetAssignment struct {
	// Variables are the variables being assigned values.
	Variables []string
	// Expression is the value or expression being assigned (Value or variable string).
	Expression any
	// IsStream indicates whether to use stream assignment ('in') or scalar assignment ('=').
	IsStream bool
}

func (LetAssignment) queryNode() {}

// MatchLetClause represents a 'match' clause using 'let' assignments.
type MatchLetClause struct {
	// Assignments are the let assignments in the clause.
	Assignments []LetAssignment
}

func (MatchLetClause) queryNode() {}
func (MatchLetClause) clause()    {}

// InsertClause represents an 'insert' clause containing one or more statements.
type InsertClause struct {
	// Statements are the statements to insert.
	Statements []Statement
}

func (InsertClause) queryNode() {}
func (InsertClause) clause()    {}

// DeleteClause represents a 'delete' clause containing one or more statements.
type DeleteClause struct {
	// Statements are the statements defining what to delete.
	Statements []Statement
}

func (DeleteClause) queryNode() {}
func (DeleteClause) clause()    {}

// UpdateClause represents an 'update' clause containing one or more statements.
type UpdateClause struct {
	// Statements are the statements defining what to update.
	Statements []Statement
}

func (UpdateClause) queryNode() {}
func (UpdateClause) clause()    {}

// PutClause represents a 'put' clause (upsert) containing one or more statements.
// Put inserts if not exists, or skips if already exists (based on key attributes).
type PutClause struct {
	// Statements are the statements defining what to put.
	Statements []Statement
}

func (PutClause) queryNode() {}
func (PutClause) clause()    {}

// SelectClause represents a 'select' clause for variable projection.
// Compiles to: select $var1, $var2, ...;
type SelectClause struct {
	// Variables are the variable names to project (e.g., ["$did", "$name"]).
	Variables []string
}

func (SelectClause) queryNode() {}
func (SelectClause) clause()    {}

// SortClause represents a 'sort' clause for ordering query results.
type SortClause struct {
	// Variable is the variable name to sort by (e.g., "$name").
	Variable string
	// Direction is "asc" or "desc".
	Direction string
}

func (SortClause) queryNode() {}
func (SortClause) clause()    {}

// OffsetClause represents an 'offset' clause for skipping results.
type OffsetClause struct {
	// Count is the number of results to skip.
	Count int
}

func (OffsetClause) queryNode() {}
func (OffsetClause) clause()    {}

// LimitClause represents a 'limit' clause for restricting result count.
type LimitClause struct {
	// Count is the maximum number of results to return.
	Count int
}

func (LimitClause) queryNode() {}
func (LimitClause) clause()    {}

// --- Fetch Items ---

// FetchItem is the marker interface for items in a 'fetch' clause.
type FetchItem interface {
	QueryNode
	fetchItem()
	// FetchKey returns the key under which the item will be returned in the result JSON.
	FetchKey() string
}

// FetchAttribute fetches a single attribute value of a variable.
type FetchAttribute struct {
	// Key is the output key in the result JSON.
	Key string
	// Var is the variable whose attribute is being fetched.
	Var string
	// AttrName is the name of the attribute type to fetch.
	AttrName string
}

func (FetchAttribute) queryNode()       {}
func (FetchAttribute) fetchItem()       {}

// FetchKey returns the output key for the attribute.
func (f FetchAttribute) FetchKey() string { return f.Key }

// FetchVariable fetches a variable directly.
type FetchVariable struct {
	// Key is the output key in the result JSON.
	Key string
	// Var is the variable being fetched.
	Var string
}

func (FetchVariable) queryNode()       {}
func (FetchVariable) fetchItem()       {}

// FetchKey returns the output key for the variable.
func (f FetchVariable) FetchKey() string { return f.Key }

// FetchAttributeList fetches all values of a multi-value attribute as a list.
type FetchAttributeList struct {
	// Key is the output key in the result JSON.
	Key string
	// Var is the variable whose attributes are being fetched.
	Var string
	// AttrName is the name of the attribute type.
	AttrName string
}

func (FetchAttributeList) queryNode()       {}
func (FetchAttributeList) fetchItem()       {}

// FetchKey returns the output key for the attribute list.
func (f FetchAttributeList) FetchKey() string { return f.Key }

// FetchFunction fetches the result of a function applied to a variable.
type FetchFunction struct {
	// Key is the output key in the result JSON.
	Key string
	// FuncName is the name of the function (e.g., "iid").
	FuncName string
	// Var is the variable the function is applied to.
	Var string
}

func (FetchFunction) queryNode()       {}
func (FetchFunction) fetchItem()       {}

// FetchKey returns the output key for the function result.
func (f FetchFunction) FetchKey() string { return f.Key }

// FetchWildcard fetches all attributes of a variable.
type FetchWildcard struct {
	// Key is the output key in the result JSON.
	Key string
	// Var is the variable whose attributes are all fetched.
	Var string
}

func (FetchWildcard) queryNode()       {}
func (FetchWildcard) fetchItem()       {}

// FetchKey returns the output key for the wildcard.
func (f FetchWildcard) FetchKey() string { return f.Key }

// FetchNestedWildcard retrieves all attributes and their values recursively in a nested structure.
type FetchNestedWildcard struct {
	// Key is the output key in the result JSON.
	Key string
	// Var is the variable being fetched.
	Var string
}

func (FetchNestedWildcard) queryNode()       {}
func (FetchNestedWildcard) fetchItem()       {}

// FetchKey returns the output key for the nested wildcard.
func (f FetchNestedWildcard) FetchKey() string { return f.Key }

// FetchClause defines the output structure of a query.
type FetchClause struct {
	// Items are the items to fetch, which can be FetchItem nodes or raw strings.
	Items []any
}

func (FetchClause) queryNode() {}
func (FetchClause) clause()    {}

// --- Reduce/Aggregate ---

// AggregateExpr represents an aggregate expression like count($var) or sum($attr).
type AggregateExpr struct {
	// FuncName is the name of the aggregate function (count, sum, min, max, mean, std, median).
	FuncName string
	// Var is the variable being aggregated.
	Var string
	// AttrName is the optional attribute name to aggregate on if the variable is a thing.
	AttrName string
}

func (AggregateExpr) queryNode() {}

// ReduceAssignment represents an assignment in a 'reduce' clause.
type ReduceAssignment struct {
	// Variable is the variable receiving the aggregated value.
	Variable string
	// Expression is the aggregation expression (AggregateExpr or variable string).
	Expression any
}

func (ReduceAssignment) queryNode() {}

// ReduceClause represents a 'reduce' clause for performing aggregations in TypeQL.
type ReduceClause struct {
	// Assignments are the aggregate assignments.
	Assignments []ReduceAssignment
	// GroupBy is the optional variable to group the results by.
	GroupBy string
}

func (ReduceClause) queryNode() {}
func (ReduceClause) clause()    {}
