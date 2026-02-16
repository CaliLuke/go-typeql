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
