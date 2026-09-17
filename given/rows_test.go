package given

import (
	"testing"
)

func TestRows_PreservesScalarZeroValues(t *testing.T) {
	rows := NewRows("text", "flag", "integer", "double", "empty")
	if err := rows.Add(Value{Type: "string", Value: ""}, Value{Type: "boolean", Value: false},
		Value{Type: "integer", Value: int64(0)}, Value{Type: "double", Value: float64(0)}, Value{Type: "empty"}); err != nil {
		t.Fatal(err)
	}
	data, err := rows.MarshalGivenRows()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"variables":["text","flag","integer","double","empty"],"rows":[[{"type":"string","value":""},{"type":"boolean","value":false},{"type":"integer","value":0},{"type":"double","value":0},{"type":"empty"}]]}`
	if string(data) != want {
		t.Fatalf("encoded rows = %s, want %s", data, want)
	}
}

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
