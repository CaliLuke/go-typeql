package naming

import "testing"

func TestKebabCase(t *testing.T) {
	for in, want := range map[string]string{
		"":            "",
		"UserAccount": "user-account",
		"HTTPServer":  "http-server",
		"User2FA":     "user2-fa",
		"UserID":      "user-id",
		// Go names that tqlgen emits for labels which do not round-trip; tqlgen
		// adds a type: tag for these (see formal/lean/Naming.lean).
		"Tag2fa":  "tag2fa",  // tag-2fa
		"AB":      "ab",      // a-b
		"HTTPAPI": "httpapi", // http-api
	} {
		if got := KebabCase(in); got != want {
			t.Errorf("KebabCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVarLabel(t *testing.T) {
	for in, want := range map[string]string{
		"name":        "name",
		"first-name":  "first_name", // common case unchanged
		"ipv4-addr":   "ipv4_addr",
		"first_name":  "_first_uname",
		"a-b_c":       "_a_hb_uc",
		"_x":          "__ux",
		"-x":          "__hx",
		"person:name": "_person_x3aname",
		"":            "_",
	} {
		if got := VarLabel(in); got != want {
			t.Errorf("VarLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVarLabelInjective checks every label up to length 5 over an alphabet
// that exercises each branch: distinct labels must get distinct variables.
func TestVarLabelInjective(t *testing.T) {
	alphabet := []byte{'a', 'u', 'h', 'x', '0', '_', '-', ':'}
	seen := make(map[string]string)
	var walk func(prefix []byte)
	walk = func(prefix []byte) {
		label := string(prefix)
		v := VarLabel(label)
		for i := range len(v) {
			if c := v[i]; c != '_' && !isAlnum(c) {
				t.Fatalf("VarLabel(%q) = %q contains %q", label, v, c)
			}
		}
		if prev, ok := seen[v]; ok {
			t.Fatalf("VarLabel(%q) == VarLabel(%q) == %q", prev, label, v)
		}
		seen[v] = label
		if len(prefix) == 5 {
			return
		}
		for _, c := range alphabet {
			walk(append(prefix, c))
		}
	}
	walk(nil)
}
