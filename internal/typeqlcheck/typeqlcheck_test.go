package typeqlcheck

import "testing"

func TestValidateBasics(t *testing.T) {
	if !Available() {
		t.Skip("typeql-check binary not installed (run: make install-typeql-check)")
	}
	if err := Validate("match $x isa person;"); err != nil {
		t.Errorf("valid query rejected: %v", err)
	}
	if err := Validate("match $x isa person; select $x;"); err != nil {
		t.Errorf("valid pipeline rejected: %v", err)
	}
	if err := Validate("undefine entity person;"); err == nil {
		t.Error("invalid 2.x undefine form accepted")
	}
	if err := Validate("insert\n;"); err == nil {
		t.Error("empty insert clause accepted")
	}
}
