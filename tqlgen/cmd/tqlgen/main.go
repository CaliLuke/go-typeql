// tqlgen generates Go code from TypeQL schema files.
//
// Usage:
//
//	tqlgen -schema schema.tql [-out models_gen.go] [-pkg models] [-acronyms]
//	tqlgen -schema schema.tql -registry [-out registry_gen.go] [-pkg graph]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/CaliLuke/go-typeql/tqlgen"
)

const version = "0.4.0"

func main() {
	schemaFile := flag.String("schema", "", "Path to TypeQL schema file (required)")
	outFile := flag.String("out", "", "Output Go file (default: stdout)")
	pkg := flag.String("pkg", "models", "Package name for generated code")
	acronyms := flag.Bool("acronyms", true, "Apply Go naming conventions for acronyms (ID, URL, etc.)")
	skipAbstract := flag.Bool("skip-abstract", true, "Skip abstract types in output")
	inherit := flag.Bool("inherit", true, "Accumulate inherited owns from parent types")
	showVersion := flag.Bool("version", false, "Print version and exit")
	enums := flag.Bool("enums", true, "Generate string constants from @values constraints")
	versionStr := flag.String("schema-version", "", "Schema version string (included in generated header)")
	registry := flag.Bool("registry", false, "Generate schema registry instead of Go structs")
	dto := flag.Bool("dto", false, "Generate DTO structs (Out/Create/Patch) for HTTP APIs")
	idField := flag.String("id-field", "ID", "ID field name in Out DTOs (default: ID)")
	strictOut := flag.Bool("strict-out", false, "Make required fields non-pointer in Out structs")
	skipRelOut := flag.Bool("skip-relation-out", false, "Skip generating relation Out structs")
	typedConsts := flag.Bool("typed-constants", false, "Generate typed string constants (EntityType, RelationType)")
	jsonSchema := flag.Bool("json-schema", false, "Generate JSON schema fragment maps for OpenAPI/LLM use")

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

	if *dto {
		dtoCfg := tqlgen.DTOConfig{
			PackageName:     *pkg,
			UseAcronyms:     *acronyms,
			SkipAbstract:    *skipAbstract,
			IDFieldName:     *idField,
			StrictOut:       *strictOut,
			SkipRelationOut: *skipRelOut,
		}
		data := tqlgen.BuildDTOData(schema, dtoCfg)
		if err := tqlgen.RenderDTO(w, data); err != nil {
			fmt.Fprintf(os.Stderr, "error rendering DTOs: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *registry {
		schemaBytes, readErr := os.ReadFile(*schemaFile)
		schemaText := ""
		if readErr == nil {
			schemaText = string(schemaBytes)
		}
		regCfg := tqlgen.RegistryConfig{
			PackageName:    *pkg,
			UseAcronyms:    *acronyms,
			SkipAbstract:   *skipAbstract,
			Enums:          *enums,
			SchemaText:     schemaText,
			SchemaVersion:  *versionStr,
			TypedConstants: *typedConsts,
			JSONSchema:     *jsonSchema,
		}
		data := tqlgen.BuildRegistryData(schema, regCfg)
		if err := tqlgen.RenderRegistry(w, data); err != nil {
			fmt.Fprintf(os.Stderr, "error rendering registry: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg := tqlgen.RenderConfig{
			PackageName:   *pkg,
			UseAcronyms:   *acronyms,
			SkipAbstract:  *skipAbstract,
			SchemaVersion: *versionStr,
			Enums:         *enums,
		}
		if err := tqlgen.Render(w, schema, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "error rendering: %v\n", err)
			os.Exit(1)
		}
	}
}
