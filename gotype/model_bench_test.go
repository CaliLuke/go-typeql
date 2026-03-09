package gotype

import (
	"reflect"
	"testing"
	"time"
)

type benchCompany struct {
	BaseEntity
	Name    string    `typedb:"name,key"`
	Country string    `typedb:"country"`
	Founded time.Time `typedb:"founded"`
}

type benchEmployment struct {
	BaseRelation
	Employee  *benchEntity  `typedb:"role:employee"`
	Employer  *benchCompany `typedb:"role:employer"`
	StartedAt time.Time     `typedb:"started-at"`
	Title     string        `typedb:"title"`
}

func BenchmarkExtractModelInfo_Entity(b *testing.B) {
	t := reflect.TypeOf(benchEntity{})

	b.ReportAllocs()
	for b.Loop() {
		info, err := ExtractModelInfo(t)
		if err != nil {
			b.Fatal(err)
		}
		if info.Kind != ModelKindEntity {
			b.Fatalf("unexpected kind: %v", info.Kind)
		}
	}
}

func BenchmarkExtractModelInfo_Relation(b *testing.B) {
	t := reflect.TypeOf(benchEmployment{})

	b.ReportAllocs()
	for b.Loop() {
		info, err := ExtractModelInfo(t)
		if err != nil {
			b.Fatal(err)
		}
		if info.Kind != ModelKindRelation {
			b.Fatalf("unexpected kind: %v", info.Kind)
		}
	}
}

func BenchmarkUnwrapResult(b *testing.B) {
	row := map[string]any{
		"_iid": map[string]any{"value": "0xABC123"},
		"name": map[string]any{"value": "Person Name"},
		"email": map[string]any{
			"value": "person@example.com",
			"type":  map[string]any{"label": "email"},
		},
		"age":   map[string]any{"value": float64(30)},
		"city":  map[string]any{"value": "San Francisco"},
		"score": map[string]any{"value": float64(95)},
	}

	b.ReportAllocs()
	for b.Loop() {
		flat := unwrapResult(row)
		if flat["_iid"] == nil {
			b.Fatal("expected _iid")
		}
	}
}
