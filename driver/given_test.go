//go:build cgo && typedb

package driver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGivenRowsAddValidatesWidth(t *testing.T) {
	rows := NewGivenRows("name", "age")
	if err := rows.Add(StringGiven("Alice")); err == nil {
		t.Fatal("expected row width error")
	}

	if err := rows.Add(StringGiven("Alice"), IntGiven(30)); err != nil {
		t.Fatalf("unexpected row add error: %v", err)
	}
	if len(rows.Rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows.Rows))
	}
}

func TestGivenRowsJSONRequiresVariables(t *testing.T) {
	rows := &GivenRows{}
	_, err := rows.json()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "at least one variable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConceptGivenEncodesOpaqueConceptHandle(t *testing.T) {
	concept := Concept{
		Handle: "concept-123",
		Kind:   "entity",
		Type:   "person",
		IID:    "0x1",
	}
	rows := NewGivenRows("p").MustAdd(ConceptGiven(concept))

	data, err := rows.json()
	if err != nil {
		t.Fatalf("json: %v", err)
	}

	var decoded struct {
		Rows [][]GivenValue `json:"rows"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode rows json: %v", err)
	}
	if got := decoded.Rows[0][0].Type; got != GivenConcept {
		t.Fatalf("expected concept type, got %q", got)
	}
	if got := decoded.Rows[0][0].Value; got != "concept-123" {
		t.Fatalf("expected concept handle, got %#v", got)
	}
}

func TestAsConceptExtractsOpaqueConceptMetadata(t *testing.T) {
	input := map[string]any{
		"_kind":           "entity",
		"_type":           "person",
		"_iid":            "0x1",
		"_concept_handle": "concept-123",
	}

	concept, ok := AsConcept(input)
	if !ok {
		t.Fatal("expected concept")
	}
	if concept.Handle != "concept-123" {
		t.Fatalf("expected handle concept-123, got %q", concept.Handle)
	}
	if concept.Kind != "entity" || concept.Type != "person" || concept.IID != "0x1" {
		t.Fatalf("unexpected concept metadata: %#v", concept)
	}
}

func TestConceptGivenRequiresHandle(t *testing.T) {
	rows := NewGivenRows("p").MustAdd(ConceptGiven(Concept{}))
	_, err := rows.json()
	if err == nil {
		t.Fatal("expected empty handle error")
	}
	if !strings.Contains(err.Error(), "non-empty opaque handle") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAsConceptRejectsFetchDocumentShape(t *testing.T) {
	input := map[string]any{
		"_kind": "entity",
		"_type": "person",
		"_iid":  "0x1",
	}

	if concept, ok := AsConcept(input); ok {
		t.Fatalf("expected no concept without opaque handle, got %#v", concept)
	}
}
