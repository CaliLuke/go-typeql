package gotype

import (
	"testing"
	"time"
)

func TestFormatValue(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"string", "hello", `"hello"`},
		{"string with quotes", `say "hi"`, `"say \"hi\""`},
		{"string with newline", "line1\nline2", `"line1\nline2"`},
		{"string with tab", "a\tb", `"a\tb"`},
		{"string with backslash", `a\b`, `"a\\b"`},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"int", 42, "42"},
		{"int64", int64(100), "100"},
		{"float64", 3.14, "3.14"},
		{"float32", float32(2.5), "2.5"},
		{"nil", nil, "null"},
		{"pointer to string", strPtr("test"), `"test"`},
		{"nil pointer", (*string)(nil), "null"},
		{"time date only", time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), "2024-01-15"},
		{"time datetime", time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC), "2024-01-15T10:30:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatValue(tt.value)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
