package gotype

import "strings"

// TypeQLReservedWords is the set of TypeQL reserved keywords that cannot be
// used as type names, attribute names, or role names.
var TypeQLReservedWords = map[string]bool{
	// Schema queries
	"define": true, "undefine": true, "redefine": true,
	// Data manipulation stages
	"match": true, "fetch": true, "insert": true, "delete": true, "update": true, "put": true,
	// Stream manipulation stages
	"select": true, "require": true, "sort": true, "limit": true, "offset": true, "reduce": true,
	// Special stages
	"with": true,
	// Pattern logic
	"or": true, "not": true, "try": true,
	// Type definition statements
	"entity": true, "relation": true, "attribute": true, "struct": true, "fun": true,
	// Constraint definition statements
	"sub": true, "relates": true, "plays": true, "value": true, "owns": true, "alias": true,
	// Instance statements
	"isa": true, "links": true, "has": true, "is": true, "let": true, "contains": true, "like": true,
	// Identity statements
	"label": true, "iid": true,
	// Annotations (without @)
	"card": true, "cascade": true, "independent": true, "abstract": true,
	"key": true, "subkey": true, "unique": true, "values": true,
	"range": true, "regex": true, "distinct": true,
	// Reductions
	"check": true, "first": true, "count": true, "max": true, "min": true,
	"mean": true, "median": true, "std": true, "sum": true, "list": true,
	// Value types
	"boolean": true, "integer": true, "double": true, "decimal": true,
	"datetime-tz": true, "datetime_tz": true, "datetime": true,
	"date": true, "duration": true, "string": true,
	// Built-in functions
	"round": true, "ceil": true, "floor": true, "abs": true, "length": true,
	// Literals
	"true": true, "false": true,
	// Miscellaneous
	"asc": true, "desc": true, "return": true, "of": true,
	"from": true, "in": true, "as": true,
}

// IsReservedWord returns true if the given name is a TypeQL reserved keyword.
// The check is case-insensitive.
func IsReservedWord(name string) bool {
	return TypeQLReservedWords[strings.ToLower(name)]
}
