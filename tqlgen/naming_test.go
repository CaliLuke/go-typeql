package tqlgen

import "testing"

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user_story", "UserStory"},
		{"isbn-13", "Isbn13"},
		{"name", "Name"},
		{"display_id", "DisplayId"},
		{"birth-date", "BirthDate"},
		{"nf_category", "NfCategory"},
		{"a", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ToPascalCase(tt.input)
			if got != tt.expected {
				t.Errorf("ToPascalCase(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestToPascalCaseAcronyms(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"display_id", "DisplayID"},
		{"external_url", "ExternalURL"},
		{"name", "Name"},
		{"user_story", "UserStory"},
		{"postgres_id", "PostgresID"},
		{"github_issue_id", "GithubIssueID"},
		{"nf_category", "NFCategory"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ToPascalCaseAcronyms(tt.input)
			if got != tt.expected {
				t.Errorf("ToPascalCaseAcronyms(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"start-date", "start_date"},
		{"isbn-13", "isbn_13"},
		{"name", "name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ToSnakeCase(tt.input)
			if got != tt.expected {
				t.Errorf("ToSnakeCase(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
