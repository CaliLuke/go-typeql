package tqlgen

import "testing"

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

	schema.AccumulateInheritance()

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

	schema.AccumulateInheritance()

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
