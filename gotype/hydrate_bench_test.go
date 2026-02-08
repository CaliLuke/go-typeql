package gotype

import (
	"testing"
)

// Benchmark entity for hydration tests
type benchEntity struct {
	BaseEntity
	Name  string `typedb:"name,key"`
	Email string `typedb:"email"`
	Age   int    `typedb:"age"`
	City  string `typedb:"city"`
	Score int    `typedb:"score"`
}

func BenchmarkHydrate_100Rows(b *testing.B) {
	ClearRegistry()
	if err := Register[benchEntity](); err != nil {
		b.Fatal(err)
	}

	// Create 100 result rows
	results := make([]map[string]any, 100)
	for i := 0; i < 100; i++ {
		results[i] = map[string]any{
			"_iid":  "0xABC123",
			"name":  "Person Name",
			"email": "person@example.com",
			"age":   30,
			"city":  "San Francisco",
			"score": 95,
		}
	}

	info, _ := LookupType(typeOf[benchEntity]())
	mgr := &Manager[benchEntity]{info: info}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mgr.hydrateResults(results)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHydrate_1000Rows(b *testing.B) {
	ClearRegistry()
	if err := Register[benchEntity](); err != nil {
		b.Fatal(err)
	}

	// Create 1000 result rows
	results := make([]map[string]any, 1000)
	for i := 0; i < 1000; i++ {
		results[i] = map[string]any{
			"_iid":  "0xABC123",
			"name":  "Person Name",
			"email": "person@example.com",
			"age":   30,
			"city":  "San Francisco",
			"score": 95,
		}
	}

	info, _ := LookupType(typeOf[benchEntity]())
	mgr := &Manager[benchEntity]{info: info}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mgr.hydrateResults(results)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHydrate_10000Rows(b *testing.B) {
	ClearRegistry()
	if err := Register[benchEntity](); err != nil {
		b.Fatal(err)
	}

	// Create 10000 result rows
	results := make([]map[string]any, 10000)
	for i := 0; i < 10000; i++ {
		results[i] = map[string]any{
			"_iid":  "0xABC123",
			"name":  "Person Name",
			"email": "person@example.com",
			"age":   30,
			"city":  "San Francisco",
			"score": 95,
		}
	}

	info, _ := LookupType(typeOf[benchEntity]())
	mgr := &Manager[benchEntity]{info: info}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mgr.hydrateResults(results)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHydrate_SingleRow(b *testing.B) {
	ClearRegistry()
	if err := Register[benchEntity](); err != nil {
		b.Fatal(err)
	}

	result := map[string]any{
		"_iid":  "0xABC123",
		"name":  "Person Name",
		"email": "person@example.com",
		"age":   30,
		"city":  "San Francisco",
		"score": 95,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var entity benchEntity
		err := Hydrate(&entity, result)
		if err != nil {
			b.Fatal(err)
		}
	}
}
