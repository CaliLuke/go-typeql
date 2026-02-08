//go:build integration && cgo && typedb

package gotype_test

import (
	"context"
	"strings"
	"testing"

	"github.com/CaliLuke/go-typeql/gotype"
)

func TestIntegration_SchemaAcceptedByTypeDB(t *testing.T) {
	db := setupTestDBDefault(t)
	ctx := context.Background()

	mgr := gotype.NewManager[Person](db)
	assertInsert(t, ctx, mgr, &Person{Name: "SchemaTest", Email: "st@example.com"})
	assertCount(t, ctx, mgr, 1)
}

func TestIntegration_SchemaHasKeyAnnotation(t *testing.T) {
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()

	schema := gotype.GenerateSchema()
	if !strings.Contains(schema, "@key") {
		t.Error("expected @key in generated schema")
	}
}

func TestIntegration_SchemaHasUniqueAnnotation(t *testing.T) {
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()

	schema := gotype.GenerateSchema()
	if !strings.Contains(schema, "@unique") {
		t.Error("expected @unique in generated schema")
	}
}

func TestIntegration_SchemaHasCardAnnotation(t *testing.T) {
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()

	schema := gotype.GenerateSchema()
	if !strings.Contains(schema, "@card(0..1)") {
		t.Error("expected @card(0..1) in generated schema for optional Age field")
	}
}

func TestIntegration_SchemaAttributeValueTypes(t *testing.T) {
	gotype.ClearRegistry()
	_ = gotype.Register[TypeTest]()

	schema := gotype.GenerateSchema()
	for _, want := range []string{"value string", "value integer", "value double", "value boolean"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q", want)
		}
	}
}

func TestIntegration_GenerateSchemaForSingleType(t *testing.T) {
	gotype.ClearRegistry()
	_ = gotype.Register[Person]()
	_ = gotype.Register[Company]()

	info, ok := gotype.Lookup("person")
	if !ok {
		t.Fatal("person not registered")
	}

	schema := gotype.GenerateSchemaFor(info)
	if !strings.Contains(schema, "entity person") {
		t.Error("expected entity person in single-type schema")
	}
	if strings.Contains(schema, "entity company") {
		t.Error("single-type schema should not contain company")
	}
}

func TestIntegration_SchemaRoundTrip(t *testing.T) {
	db := setupTestDBWith(t, func() {
		_ = gotype.Register[Person]()
		_ = gotype.Register[Company]()
		_ = gotype.Register[Employment]()
	})

	addr := dbAddress()
	schemaStr := getSchemaString(t, addr, db.Name())
	if schemaStr == "" {
		t.Fatal("expected non-empty schema from database")
	}

	parsed, err := gotype.IntrospectSchemaFromString(schemaStr)
	if err != nil {
		t.Fatalf("IntrospectSchemaFromString: %v", err)
	}

	entityNames := make(map[string]bool)
	for _, e := range parsed.Entities {
		entityNames[e.Name] = true
	}
	for _, want := range []string{"person", "company"} {
		if !entityNames[want] {
			t.Errorf("parsed schema missing entity %q", want)
		}
	}

	relNames := make(map[string]bool)
	for _, r := range parsed.Relations {
		relNames[r.Name] = true
	}
	if !relNames["employment"] {
		t.Error("parsed schema missing relation employment")
	}
}
