package gotype

import (
	"testing"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		want    FieldTag
		wantErr bool
	}{
		{
			name: "simple name",
			tag:  "name",
			want: FieldTag{Name: "name"},
		},
		{
			name: "name with key",
			tag:  "name,key",
			want: FieldTag{Name: "name", Key: true},
		},
		{
			name: "name with unique",
			tag:  "email,unique",
			want: FieldTag{Name: "email", Unique: true},
		},
		{
			name: "name with key and unique",
			tag:  "email,key,unique",
			want: FieldTag{Name: "email", Key: true, Unique: true},
		},
		{
			name: "role player",
			tag:  "role:employee",
			want: FieldTag{RoleName: "employee"},
		},
		{
			name: "cardinality range",
			tag:  "tags,card=0..5",
			want: FieldTag{Name: "tags", CardMin: intPtr(0), CardMax: intPtr(5)},
		},
		{
			name: "cardinality unbounded",
			tag:  "tags,card=2..",
			want: FieldTag{Name: "tags", CardMin: intPtr(2)},
		},
		{
			name: "cardinality shorthand",
			tag:  "tags,card=0+",
			want: FieldTag{Name: "tags", CardMin: intPtr(0)},
		},
		{
			name: "abstract",
			tag:  "abstract",
			want: FieldTag{Abstract: true},
		},
		{
			name: "type override",
			tag:  "type:custom-name",
			want: FieldTag{TypeName: "custom-name"},
		},
		{
			name: "skip",
			tag:  "-",
			want: FieldTag{Skip: true},
		},
		{
			name: "empty",
			tag:  "",
			want: FieldTag{},
		},
		{
			name: "kebab-case name",
			tag:  "start-date",
			want: FieldTag{Name: "start-date"},
		},
		{
			name:    "invalid cardinality",
			tag:     "x,card=abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTag(tt.tag)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.Name != tt.want.Name {
				t.Errorf("Name: got %q, want %q", got.Name, tt.want.Name)
			}
			if got.Key != tt.want.Key {
				t.Errorf("Key: got %v, want %v", got.Key, tt.want.Key)
			}
			if got.Unique != tt.want.Unique {
				t.Errorf("Unique: got %v, want %v", got.Unique, tt.want.Unique)
			}
			if got.RoleName != tt.want.RoleName {
				t.Errorf("RoleName: got %q, want %q", got.RoleName, tt.want.RoleName)
			}
			if got.Abstract != tt.want.Abstract {
				t.Errorf("Abstract: got %v, want %v", got.Abstract, tt.want.Abstract)
			}
			if got.TypeName != tt.want.TypeName {
				t.Errorf("TypeName: got %q, want %q", got.TypeName, tt.want.TypeName)
			}
			if got.Skip != tt.want.Skip {
				t.Errorf("Skip: got %v, want %v", got.Skip, tt.want.Skip)
			}
			if !intPtrEqual(got.CardMin, tt.want.CardMin) {
				t.Errorf("CardMin: got %v, want %v", derefIntPtr(got.CardMin), derefIntPtr(tt.want.CardMin))
			}
			if !intPtrEqual(got.CardMax, tt.want.CardMax) {
				t.Errorf("CardMax: got %v, want %v", derefIntPtr(got.CardMax), derefIntPtr(tt.want.CardMax))
			}
		})
	}
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func derefIntPtr(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return string(rune('0' + *p))
}
