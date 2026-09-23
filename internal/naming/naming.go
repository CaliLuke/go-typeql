// Package naming holds label conversions shared across packages: the
// Go-name to TypeDB-label conversion used by gotype (which derives default
// type names) and tqlgen (which must know when a generated struct needs an
// explicit type: override), and the injective label to TypeQL-variable
// encoding used by gotype and ast.
package naming

import "strings"

// KebabCase converts a PascalCase Go struct name to kebab-case, treating
// consecutive uppercase runs as initialisms.
// e.g. "UserAccount" → "user-account", "HTTPServer" → "http-server",
// "User2FA" → "user2-fa".
//
// The conversion is not a left inverse of tqlgen's label → Go name mapping:
// "user_account" and "user-account" both become "UserAccount". Callers that
// start from a TypeDB label must carry the label explicitly.
func KebabCase(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	var b strings.Builder
	for i, r := range runes {
		if isUpper(r) {
			// Start a new word when the previous rune is not uppercase (end of a
			// lowercase/digit run), or when this uppercase rune starts a new word
			// after an initialism run (next rune is lowercase).
			startsWord := i > 0 && (!isUpper(runes[i-1]) ||
				(i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'))
			if startsWord {
				b.WriteByte('-')
			}
			b.WriteByte(byte(r - 'A' + 'a'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// VarLabel encodes a TypeDB label as TypeQL variable-name characters
// ([A-Za-z0-9_]). The encoding is injective, so distinct labels never share
// a generated variable (which TypeQL would treat as an implicit equality):
//
//   - a label that starts with a letter or digit and contains only letters,
//     digits, and hyphens maps hyphens to underscores ("first-name" →
//     "first_name"), keeping the common case readable;
//   - any other label gets a leading underscore, which the first form never
//     produces, followed by the label with "_" → "_u", "-" → "_h", and other
//     bytes → "_x" plus two hex digits ("first_name" → "_first_uname").
//
// formal/lean/Naming.lean proves the encoding injective.
func VarLabel(label string) string {
	if isPlainLabel(label) {
		return strings.ReplaceAll(label, "-", "_")
	}
	var b strings.Builder
	b.Grow(len(label) + 8)
	b.WriteByte('_')
	for i := range len(label) {
		switch c := label[i]; {
		case c == '_':
			b.WriteString("_u")
		case c == '-':
			b.WriteString("_h")
		case isAlnum(c):
			b.WriteByte(c)
		default:
			b.WriteString("_x")
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

const hexDigits = "0123456789abcdef"

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isPlainLabel(label string) bool {
	if label == "" || !isAlnum(label[0]) {
		return false
	}
	for i := range len(label) {
		if c := label[i]; c != '-' && !isAlnum(c) {
			return false
		}
	}
	return true
}
