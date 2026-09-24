package gotype

import (
	"fmt"
	"unicode"
)

// TypeQLReservedWords is the set of TypeQL reserved keywords that cannot be
// used as type names, attribute names, or role names. It is the keyword list
// of typeql::is_reserved_keyword (typeql 3.13.4), which is the `reserved`
// rule of typeql-reference/typeql.pest. The server rejects these words only
// in lowercase, so "Match" and "MATCH" are valid names. Other TypeQL words,
// such as "label", "count", "string", and "abs", are valid names too.
var TypeQLReservedWords = map[string]bool{
	// Query stages and clauses
	"with": true, "given": true, "match": true, "fetch": true, "update": true,
	"define": true, "undefine": true, "redefine": true, "insert": true, "put": true,
	"delete": true, "end": true, "return": true, "asc": true, "desc": true,
	// Type definitions
	"entity": true, "relation": true, "attribute": true, "role": true, "struct": true, "fun": true,
	// Constraints and statements
	"alias": true, "sub": true, "owns": true, "as": true, "plays": true, "relates": true,
	"iid": true, "isa": true, "links": true, "has": true, "is": true,
	// Pattern logic
	"or": true, "not": true, "try": true, "in": true,
	// Literals
	"true": true, "false": true,
	// Miscellaneous
	"of": true, "from": true, "first": true, "last": true,
}

// IsReservedWord returns true if the given name is a TypeQL reserved keyword.
// The check is case-sensitive, like the TypeDB server: "match" is reserved,
// and "Match" is not.
func IsReservedWord(name string) bool {
	return TypeQLReservedWords[name]
}

// ValidateIdentifier checks that a name is a valid TypeQL identifier.
// Valid identifiers start with a letter or underscore and continue with
// letters, digits, hyphens, or underscores. Returns nil if valid, or an
// error describing the problem.
func ValidateIdentifier(name, context string) error {
	if name == "" {
		return fmt.Errorf("empty %s name", context)
	}
	for i, r := range name {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return &InvalidIdentifierError{
					Name:    name,
					Context: context,
					Reason:  fmt.Sprintf("must start with a letter or underscore, got %q", r),
				}
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return &InvalidIdentifierError{
					Name:    name,
					Context: context,
					Reason:  fmt.Sprintf("invalid character %q at position %d", r, i),
				}
			}
		}
	}
	return nil
}

// validateTypeQLLabel checks the identifier shape and reserved-word set used
// for TypeQL type, relation, and role labels.
func validateTypeQLLabel(name string) error {
	if err := ValidateIdentifier(name, "label"); err != nil {
		return err
	}
	if IsReservedWord(name) {
		return &ReservedWordError{Word: name, Context: "label"}
	}
	return nil
}

// InvalidIdentifierError is returned when a name contains characters
// not allowed in TypeQL identifiers.
type InvalidIdentifierError struct {
	Name    string
	Context string
	Reason  string
}

func (e *InvalidIdentifierError) Error() string {
	return fmt.Sprintf("invalid %s name %q: %s", e.Context, e.Name, e.Reason)
}
