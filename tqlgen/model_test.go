package tqlgen

import (
	"strings"
	"testing"
)

func TestAccumulateInheritance_PreservesInheritedDocAndMeta(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{
				Name: "artifact",
				Owns: []OwnsSpec{
					{
						Attribute: "name",
						Key:       true,
						Doc:       "Inherited name doc.",
						Meta:      []MetaSpec{{Key: "source", Value: "parent"}},
					},
				},
			},
			{
				Name:   "task",
				Parent: "artifact",
			},
		},
	}

	if err := schema.AccumulateInheritance(); err != nil {
		t.Fatalf("AccumulateInheritance failed: %v", err)
	}

	task := schema.Entities[1]
	if len(task.Owns) != 1 {
		t.Fatalf("expected inherited owns, got %#v", task.Owns)
	}
	if task.Owns[0].Doc != "Inherited name doc." {
		t.Fatalf("inherited Doc = %q", task.Owns[0].Doc)
	}
	if got := task.Owns[0].Meta; len(got) != 1 || got[0].Key != "source" || got[0].Value != "parent" {
		t.Fatalf("inherited Meta = %#v", got)
	}
}

func TestAccumulateInheritance_ChildRedeclarationDropsInheritedDocAndMeta(t *testing.T) {
	schema := &ParsedSchema{
		Entities: []EntitySpec{
			{
				Name: "artifact",
				Owns: []OwnsSpec{
					{
						Attribute: "name",
						Key:       true,
						Doc:       "Inherited name doc.",
						Meta:      []MetaSpec{{Key: "source", Value: "parent"}},
					},
				},
			},
			{
				Name:   "task",
				Parent: "artifact",
				Owns: []OwnsSpec{
					{Attribute: "name", Key: true},
				},
			},
		},
	}

	if err := schema.AccumulateInheritance(); err != nil {
		t.Fatalf("AccumulateInheritance failed: %v", err)
	}

	task := schema.Entities[1]
	if len(task.Owns) != 1 {
		t.Fatalf("expected child owns, got %#v", task.Owns)
	}
	if task.Owns[0].Doc != "" {
		t.Fatalf("child redeclaration should drop inherited Doc, got %q", task.Owns[0].Doc)
	}
	if len(task.Owns[0].Meta) != 0 {
		t.Fatalf("child redeclaration should drop inherited Meta, got %#v", task.Owns[0].Meta)
	}
}

// TestAccumulateInheritance_CycleReturnsError is a regression test for issue
// #73: a cyclic sub chain must produce a diagnostic error instead of crashing
// with a stack overflow.
func TestAccumulateInheritance_CycleReturnsError(t *testing.T) {
	t.Run("entity cycle", func(t *testing.T) {
		schema, err := ParseSchema(`define
attribute name, value string;
entity a sub b, owns name;
entity b sub a;
`)
		if err != nil {
			t.Fatalf("ParseSchema failed: %v", err)
		}
		err = schema.AccumulateInheritance()
		if err == nil {
			t.Fatal("expected inheritance cycle error, got nil")
		}
		if !strings.Contains(err.Error(), "inheritance cycle") {
			t.Errorf("expected error to mention the inheritance cycle, got %q", err)
		}
	})

	t.Run("entity self cycle", func(t *testing.T) {
		schema := &ParsedSchema{Entities: []EntitySpec{{Name: "a", Parent: "a"}}}
		err := schema.AccumulateInheritance()
		if err == nil || !strings.Contains(err.Error(), "inheritance cycle: a -> a") {
			t.Errorf("expected self-cycle error, got %v", err)
		}
	})

	t.Run("relation cycle", func(t *testing.T) {
		schema := &ParsedSchema{Relations: []RelationSpec{
			{Name: "r1", Parent: "r2"},
			{Name: "r2", Parent: "r1"},
		}}
		err := schema.AccumulateInheritance()
		if err == nil || !strings.Contains(err.Error(), "inheritance cycle") {
			t.Errorf("expected relation cycle error, got %v", err)
		}
	})

	t.Run("acyclic diamond still succeeds", func(t *testing.T) {
		schema := &ParsedSchema{Entities: []EntitySpec{
			{Name: "base", Owns: []OwnsSpec{{Attribute: "name"}}},
			{Name: "left", Parent: "base"},
			{Name: "right", Parent: "base"},
			{Name: "leaf", Parent: "left"},
		}}
		if err := schema.AccumulateInheritance(); err != nil {
			t.Fatalf("expected no error for acyclic hierarchy, got %v", err)
		}
		leaf := schema.Entities[3]
		if len(leaf.Owns) != 1 || leaf.Owns[0].Attribute != "name" {
			t.Errorf("expected leaf to inherit name through the chain, got %#v", leaf.Owns)
		}
	})
}
