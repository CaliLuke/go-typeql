package given

import "testing"

func TestRows_AddCopiesInputAndChecksWidth(t *testing.T) {
	rows := NewRows("name", "age")
	values := []Value{{Type: "string", Value: "Alice"}, {Type: "integer", Value: int64(30)}}
	if err := rows.Add(values...); err != nil {
		t.Fatal(err)
	}
	values[0] = Value{Type: "string", Value: "Bob"}
	if rows.Rows[0][0].Value != "Alice" {
		t.Fatal("row retained caller's mutable slice")
	}
	if err := rows.Add(Value{Type: "string", Value: "short"}); err == nil {
		t.Fatal("expected width error")
	}
}
