// tqlgen generates Go struct definitions from TypeQL schema files.
//
// Usage:
//
//	tqlgen -schema schema.tql [-out models_gen.go] [-pkg models] [-acronyms]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/CaliLuke/go-typeql/tqlgen"
)

const version = "0.2.0"

func main() {
	schemaFile := flag.String("schema", "", "Path to TypeQL schema file (required)")
	outFile := flag.String("out", "", "Output Go file (default: stdout)")
	pkg := flag.String("pkg", "models", "Package name for generated code")
	acronyms := flag.Bool("acronyms", true, "Apply Go naming conventions for acronyms (ID, URL, etc.)")
	skipAbstract := flag.Bool("skip-abstract", true, "Skip abstract types in output")
	inherit := flag.Bool("inherit", true, "Accumulate inherited owns from parent types")
	showVersion := flag.Bool("version", false, "Print version and exit")
	versionStr := flag.String("schema-version", "", "Schema version string (included in generated header)")

	flag.Parse()

	if *showVersion {
		fmt.Printf("tqlgen %s\n", version)
		os.Exit(0)
	}

	if *schemaFile == "" {
		fmt.Fprintln(os.Stderr, "error: -schema flag is required")
		flag.Usage()
		os.Exit(1)
	}

	schema, err := tqlgen.ParseSchemaFile(*schemaFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *inherit {
		schema.AccumulateInheritance()
	}

	cfg := tqlgen.RenderConfig{
		PackageName:   *pkg,
		UseAcronyms:   *acronyms,
		SkipAbstract:  *skipAbstract,
		SchemaVersion: *versionStr,
	}

	var w *os.File
	if *outFile != "" {
		w, err = os.Create(*outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating output: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = w.Close() }()
	} else {
		w = os.Stdout
	}

	if err := tqlgen.Render(w, schema, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error rendering: %v\n", err)
		os.Exit(1)
	}
}
