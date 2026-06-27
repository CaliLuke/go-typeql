package tqlgen

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderEnums(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "status", ValueType: "string", Values: []string{"proposed", "accepted"}},
			{Name: "anchor_status", ValueType: "string", Values: []string{"anchored", "floating"}},
		},
	}

	var buf bytes.Buffer
	cfg := DefaultConfig()
	cfg.Enums = true

	if err := Render(&buf, schema, cfg); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()

	// Check status constants
	if !strings.Contains(out, `StatusProposed = "proposed"`) {
		t.Errorf("missing StatusProposed constant\n%s", out)
	}
	if !strings.Contains(out, `StatusAccepted = "accepted"`) {
		t.Errorf("missing StatusAccepted constant\n%s", out)
	}

	// Check multi-word attribute
	if !strings.Contains(out, `AnchorStatusAnchored = "anchored"`) {
		t.Errorf("missing AnchorStatusAnchored constant\n%s", out)
	}
	if !strings.Contains(out, `AnchorStatusFloating = "floating"`) {
		t.Errorf("missing AnchorStatusFloating constant\n%s", out)
	}
}

func TestRenderEnumsDisabled(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "status", ValueType: "string", Values: []string{"proposed", "accepted"}},
		},
	}

	var buf bytes.Buffer
	cfg := DefaultConfig()
	cfg.Enums = false

	if err := Render(&buf, schema, cfg); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if strings.Contains(buf.String(), "StatusProposed") {
		t.Errorf("enum constants should be suppressed when Enums=false")
	}
}

func TestRenderDocAnnotationsAsCommentsAndTags(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "email", ValueType: "string", Doc: "Primary contact email."},
		},
		Entities: []EntitySpec{
			{
				Name: "customer",
				Doc:  "A customer account.",
				Owns: []OwnsSpec{
					{Attribute: "email", Key: true, Doc: "Primary contact email."},
				},
			},
		},
	}

	var buf bytes.Buffer
	cfg := DefaultConfig()
	if err := Render(&buf, schema, cfg); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// Customer — A customer account.") {
		t.Fatalf("missing type doc comment\n%s", out)
	}
	if !strings.Contains(out, "// Email — Primary contact email.") {
		t.Fatalf("missing field doc comment\n%s", out)
	}
	if !strings.Contains(out, "Email string `typedb:\"email,key\" typedb_doc:\"Primary contact email.\"`") {
		t.Fatalf("missing typedb_doc tag\n%s", out)
	}
}

func TestRenderMetaAnnotationsAsCommentsOnly(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "email", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{
				Name: "customer",
				Meta: []MetaSpec{
					{Key: "icon", Value: "user"},
					{Key: "owner", Value: "crm"},
				},
				Owns: []OwnsSpec{
					{
						Attribute: "email",
						Key:       true,
						Meta:      []MetaSpec{{Key: "pii", Value: "true"}},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// @meta icon=user") {
		t.Fatalf("missing type @meta comment\n%s", out)
	}
	if !strings.Contains(out, "// @meta owner=crm") {
		t.Fatalf("missing second type @meta comment\n%s", out)
	}
	if !strings.Contains(out, "// @meta pii=true") {
		t.Fatalf("missing field @meta comment\n%s", out)
	}
	if strings.Contains(out, "typedb_meta") {
		t.Fatalf("should not emit typedb_meta tag\n%s", out)
	}
	if !strings.Contains(out, "func (Customer) SchemaMeta() map[string]string") {
		t.Fatalf("missing SchemaMeta method\n%s", out)
	}
	if !strings.Contains(out, `"icon": "user"`) {
		t.Fatalf("missing icon metadata entry\n%s", out)
	}
	if !strings.Contains(out, `"owner": "crm"`) {
		t.Fatalf("missing owner metadata entry\n%s", out)
	}
}

func TestRenderTypeMetaAnnotationsAsSchemaMetaMethod(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{
				Name: "customer",
				Meta: []MetaSpec{
					{Key: "owner", Value: "crm"},
					{Key: "ui", Value: "account"},
				},
			},
		},
		Relations: []RelationSpec{
			{
				Name: "employment",
				Meta: []MetaSpec{
					{Key: "owner", Value: "hr"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "func (Customer) SchemaMeta() map[string]string") {
		t.Fatalf("missing entity SchemaMeta method\n%s", out)
	}
	if !strings.Contains(out, "func (Employment) SchemaMeta() map[string]string") {
		t.Fatalf("missing relation SchemaMeta method\n%s", out)
	}
	if !strings.Contains(out, `"owner": "crm"`) || !strings.Contains(out, `"ui": "account"`) || !strings.Contains(out, `"owner": "hr"`) {
		t.Fatalf("missing metadata map entries\n%s", out)
	}
}

func TestRenderRelationRoleDocAndMetaAnnotationsAsComments(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{Name: "person", Owns: []OwnsSpec{{Attribute: "name", Key: true}}, Plays: []PlaysSpec{{Relation: "employment", Role: "employee"}}},
		},
		Relations: []RelationSpec{
			{
				Name: "employment",
				Relates: []RelatesSpec{
					{
						Role: "employee",
						Doc:  "Employee side.",
						Meta: []MetaSpec{{Key: "required", Value: "true"}},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// Employee — Employee side.") {
		t.Fatalf("missing role doc comment\n%s", out)
	}
	if !strings.Contains(out, "// @meta required=true") {
		t.Fatalf("missing role @meta comment\n%s", out)
	}
}

func TestRenderTypeDocKeepsInheritanceComment(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{
				Name:   "task",
				Parent: "artifact",
				Doc:    "A work item.",
				Owns:   []OwnsSpec{{Attribute: "name", Key: true}},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// Task — A work item.") {
		t.Fatalf("missing type doc comment\n%s", out)
	}
	if !strings.Contains(out, "// Task inherits from artifact.") {
		t.Fatalf("missing inheritance comment with type doc\n%s", out)
	}
}

func TestRenderAttributeDocAndMetaAnnotationsOnEnums(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{
				Name:      "status",
				ValueType: "string",
				Values:    []string{"active"},
				Doc:       "Lifecycle state.",
				Meta:      []MetaSpec{{Key: "ui", Value: "badge"}},
			},
		},
	}

	var buf bytes.Buffer
	cfg := DefaultConfig()
	cfg.Enums = true
	if err := Render(&buf, schema, cfg); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// Status — Lifecycle state.") {
		t.Fatalf("missing enum attribute doc comment\n%s", out)
	}
	if !strings.Contains(out, "// @meta ui=badge") {
		t.Fatalf("missing enum attribute meta comment\n%s", out)
	}
}

func TestRenderDocAnnotationTagEscapesQuotes(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{
				Name: "person",
				Owns: []OwnsSpec{
					{Attribute: "name", Key: true, Doc: `Legal "display" name.`},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Name string `typedb:\"name,key\" typedb_doc:\"Legal \\\"display\\\" name.\"`") {
		t.Fatalf("missing escaped typedb_doc tag\n%s", out)
	}
}

func TestRenderDocAnnotationTagWithBacktickUsesInterpretedLiteral(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{
				Name: "person",
				Owns: []OwnsSpec{
					{Attribute: "name", Key: true, Doc: "Name with `code`."},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Name string \"typedb:\\\"name,key\\\" typedb_doc:\\\"Name with `code`.\\\"\"") {
		t.Fatalf("missing interpreted struct tag literal\n%s", out)
	}
}

func TestRenderDocAnnotationCommentsAreSingleLine(t *testing.T) {
	schema := &ParsedSchema{
		Attributes: []AttributeSpec{
			{Name: "name", ValueType: "string"},
		},
		Entities: []EntitySpec{
			{
				Name: "person",
				Doc:  "Person\nrecord",
				Owns: []OwnsSpec{
					{Attribute: "name", Key: true, Doc: "Display\nname"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := Render(&buf, schema, DefaultConfig()); err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "// Person — Person record") {
		t.Fatalf("missing single-line type comment\n%s", out)
	}
	if !strings.Contains(out, "// Name — Display name") {
		t.Fatalf("missing single-line field comment\n%s", out)
	}
}

func TestBuildEnumCtxAcronyms(t *testing.T) {
	attr := AttributeSpec{Name: "display_id", ValueType: "string", Values: []string{"auto", "manual"}}
	cfg := RenderConfig{UseAcronyms: true}

	ctx := buildEnumCtx(attr, cfg)

	if ctx.GoPrefix != "DisplayID" {
		t.Errorf("GoPrefix = %q, want %q", ctx.GoPrefix, "DisplayID")
	}
	if ctx.Values[0].GoName != "DisplayIDAuto" {
		t.Errorf("Values[0].GoName = %q, want %q", ctx.Values[0].GoName, "DisplayIDAuto")
	}
}
