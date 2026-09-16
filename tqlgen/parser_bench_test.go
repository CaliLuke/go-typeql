package tqlgen

import (
	"fmt"
	"testing"

	"github.com/alecthomas/participle/v2"
)

const parserBenchSchema = `define
attribute name, value string @doc("person name") @meta("source", "benchmark");
entity person, owns name @key;
struct profile: display-name value string, alias value string?;
fun named($n: string) -> { person }:
    match $p isa person, has name $n;
    return { $p };
`

var parserBenchResult *ParsedSchema
var parserBenchGrammar *participle.Parser[TQLFileSimple]

// BenchmarkParseSchema_ColdGrammar measures grammar construction on its own.
func BenchmarkParseSchema_ColdGrammar(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		grammar, err := participle.Build[TQLFileSimple](
			participle.Lexer(simpleLexer),
			participle.Elide("Comment", "Whitespace"),
			participle.UseLookahead(3),
		)
		if err != nil {
			b.Fatal(err)
		}
		parserBenchGrammar = grammar
	}
}

// BenchmarkParseSchema_Repeated includes parsing and AST conversion.
func BenchmarkParseSchema_Repeated(b *testing.B) {
	fresh := func(input string) (*ParsedSchema, error) {
		grammar, err := participle.Build[TQLFileSimple](
			participle.Lexer(simpleLexer),
			participle.Elide("Comment", "Whitespace"),
			participle.UseLookahead(3),
		)
		if err != nil {
			return nil, err
		}
		ast, err := grammar.ParseString("schema.tql", input)
		if err != nil {
			return nil, err
		}
		return convertAST(ast), nil
	}
	large := "define\n"
	for i := range 20 {
		large += fmt.Sprintf(`attribute name-%d, value string @doc("person name");
entity person-%d, owns name-%d @key;
struct profile-%d: display-name value string, alias value string?;
fun named-%d($n: string) -> { person-%d }:
    match $p isa person-%d, has name-%d $n;
    return { $p };
`, i, i, i, i, i, i, i, i)
	}
	for _, tc := range []struct {
		name, schema string
	}{
		{"small", parserBenchSchema},
		{"large_20_blocks", large},
	} {
		for _, method := range []struct {
			name  string
			parse func(string) (*ParsedSchema, error)
		}{
			{"fresh", fresh},
			{"cached", ParseSchema},
		} {
			b.Run(tc.name+"/"+method.name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					parsed, err := method.parse(tc.schema)
					if err != nil {
						b.Fatal(err)
					}
					parserBenchResult = parsed
				}
			})
		}
	}
}
