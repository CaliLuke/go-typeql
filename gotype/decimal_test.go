package gotype

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
)

// --- Test models ---

// decimalProduct exercises the supported Go carriers for value:decimal.
type decimalProduct struct {
	BaseEntity
	SKU     string    `typedb:"sku,key"`
	Price   float64   `typedb:"price,value:decimal"`
	Exact   string    `typedb:"exact-price,value:decimal"`
	Tier    *float64  `typedb:"tier-price,value:decimal,card=0..1"`
	History []float64 `typedb:"price-history,value:decimal,card=0.."`
}

// decimalKeyed exercises decimal literals in key-match clauses.
type decimalKeyed struct {
	BaseEntity
	Rate float64 `typedb:"rate,key,value:decimal"`
}

// --- Tag parsing ---

func TestParseTag_ValueDecimal(t *testing.T) {
	ft, err := ParseTag("price,value:decimal")
	if err != nil {
		t.Fatalf("ParseTag failed: %v", err)
	}
	if ft.Name != "price" || ft.ValueType != "decimal" {
		t.Errorf("got Name=%q ValueType=%q, want price/decimal", ft.Name, ft.ValueType)
	}
}

func TestParseTag_ValueDecimal_Invalid(t *testing.T) {
	for _, tag := range []string{
		"price,value:float",
		"price,value:double",
		"price,value:",
	} {
		if _, err := ParseTag(tag); err == nil {
			t.Errorf("ParseTag(%q): expected error, got nil", tag)
		}
	}
}

// --- Field extraction / registration validation ---

func TestExtractModelInfo_DecimalOverride(t *testing.T) {
	info, err := ExtractModelInfo(reflect.TypeOf(decimalProduct{}))
	if err != nil {
		t.Fatalf("ExtractModelInfo failed: %v", err)
	}
	for _, attr := range []string{"price", "exact-price", "tier-price", "price-history"} {
		fi, ok := info.FieldByAttrName(attr)
		if !ok {
			t.Fatalf("field %s not found", attr)
		}
		if fi.ValueType != "decimal" {
			t.Errorf("field %s: ValueType = %q, want decimal", attr, fi.ValueType)
		}
	}
	if fi, _ := info.FieldByAttrName("sku"); fi.ValueType != "string" {
		t.Errorf("sku ValueType = %q, want string (no override)", fi.ValueType)
	}
}

func TestExtractModelInfo_DecimalOverride_BadKinds(t *testing.T) {
	type decimalOnInt struct {
		BaseEntity
		N int `typedb:"n,value:decimal"`
	}
	type decimalOnBool struct {
		BaseEntity
		B bool `typedb:"b,value:decimal"`
	}
	type decimalOnRole struct {
		BaseRelation
		P *decimalProduct `typedb:"role:item,value:decimal"`
	}
	type decimalNoName struct {
		BaseEntity
		X float64 `typedb:"value:decimal"`
	}

	for name, typ := range map[string]reflect.Type{
		"int field":    reflect.TypeOf(decimalOnInt{}),
		"bool field":   reflect.TypeOf(decimalOnBool{}),
		"role field":   reflect.TypeOf(decimalOnRole{}),
		"nameless tag": reflect.TypeOf(decimalNoName{}),
	} {
		if _, err := ExtractModelInfo(typ); err == nil {
			t.Errorf("%s: expected registration error, got nil", name)
		}
	}
}

// --- Literal formatting ---

func TestFormatDecimalLiteral(t *testing.T) {
	type namedFloat float64
	type namedString string
	f := 4.25

	tests := []struct {
		name string
		val  any
		want string
	}{
		{"float64", 12.5, "12.5dec"},
		{"float64 negative", -12.5, "-12.5dec"},
		{"float64 integral gets fraction", 3.0, "3.0dec"},
		{"float64 zero", 0.0, "0.0dec"},
		{"float32", float32(2.5), "2.5dec"},
		{"string", "12.5", "12.5dec"},
		{"string integral gets fraction", "3", "3.0dec"},
		{"string negative", "-0.5", "-0.5dec"},
		{"string plus sign", "+7.25", "+7.25dec"},
		{"string zero", "0", "0.0dec"},
		{"pointer to float64", &f, "4.25dec"},
		{"named float", namedFloat(1.5), "1.5dec"},
		{"named string", namedString("9.75"), "9.75dec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatDecimalLiteral(tt.val)
			if err != nil {
				t.Fatalf("formatDecimalLiteral(%v) failed: %v", tt.val, err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDecimalLiteral_Invalid(t *testing.T) {
	invalid := []any{
		"",       // empty
		"abc",    // not a numeral
		"1e5",    // exponent
		"1.",     // missing fraction digits
		".5",     // missing integer digits
		"1.2.3",  // two dots
		" 1.5",   // whitespace
		"1.5dec", // suffix already present
		math.NaN(),
		math.Inf(1),
		42,   // int is not a decimal carrier
		true, // nor bool
		(*float64)(nil),
	}
	for _, val := range invalid {
		if got, err := formatDecimalLiteral(val); err == nil {
			t.Errorf("formatDecimalLiteral(%#v): expected error, got %q", val, got)
		}
	}
}

func TestFormatFieldValue_NonDecimalPassthrough(t *testing.T) {
	fi := &FieldInfo{ValueType: "double"}
	got, err := formatFieldValue(fi, 12.5)
	if err != nil {
		t.Fatalf("formatFieldValue failed: %v", err)
	}
	if got != "12.5" {
		t.Errorf("got %q, want %q (plain double literal)", got, "12.5")
	}
	got, err = formatFieldValue(nil, "x")
	if err != nil || got != `"x"` {
		t.Errorf("nil FieldInfo: got %q, %v; want %q, nil", got, err, `"x"`)
	}
}

// --- Query generation ---

func TestToInsertQuery_Decimal(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	tier := 5.0
	p := &decimalProduct{
		SKU:     "widget-1",
		Price:   12.5,
		Exact:   "10.99",
		Tier:    &tier,
		History: []float64{1.25, 3.0},
	}
	query, err := ToInsertQuery(p)
	if err != nil {
		t.Fatalf("ToInsertQuery failed: %v", err)
	}
	for _, want := range []string{
		"has price 12.5dec",
		"has exact-price 10.99dec",
		"has tier-price 5.0dec",
		"has price-history 1.25dec",
		"has price-history 3.0dec",
		`has sku "widget-1"`,
	} {
		if !strings.Contains(query, want) {
			t.Errorf("insert query missing %q:\n%s", want, query)
		}
	}
}

func TestToInsertQuery_Decimal_InvalidString(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	p := &decimalProduct{SKU: "bad", Price: 1.0, Exact: "not-a-number"}
	if _, err := ToInsertQuery(p); err == nil {
		t.Fatal("expected error for invalid decimal string, got nil")
	}
}

func TestToMatchQuery_DecimalKey(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalKeyed]()

	query, err := ToMatchQuery(&decimalKeyed{Rate: 9.75})
	if err != nil {
		t.Fatalf("ToMatchQuery failed: %v", err)
	}
	if !strings.Contains(query, "has rate 9.75dec") {
		t.Errorf("match query missing decimal key literal:\n%s", query)
	}
}

func TestManagerUpdate_DecimalLiterals(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	writeTx := &mockTx{responses: [][]map[string]any{nil}}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

	p := &decimalProduct{SKU: "widget-1", Price: 42.0, Exact: "0.1"}
	p.SetIID("0xABC123")
	if err := mgr.Update(context.Background(), p); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if len(writeTx.queries) == 0 {
		t.Fatal("no update query issued")
	}
	q := writeTx.queries[0]
	for _, want := range []string{"has price 42.0dec", "has exact-price 0.1dec"} {
		if !strings.Contains(q, want) {
			t.Errorf("update query missing %q:\n%s", want, q)
		}
	}
}

func TestManagerGet_DecimalFilterLiteral(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	readTx := &mockTx{}
	conn := &mockConn{txs: []*mockTx{readTx}}
	mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

	if _, err := mgr.Get(context.Background(), map[string]any{"price": 12.5, "sku": "widget-1"}); err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(readTx.queries) == 0 {
		t.Fatal("no read query issued")
	}
	q := readTx.queries[0]
	if !strings.Contains(q, "has price 12.5dec") {
		t.Errorf("filtered match missing decimal literal:\n%s", q)
	}
	if !strings.Contains(q, `has sku "widget-1"`) {
		t.Errorf("filtered match missing plain string literal:\n%s", q)
	}
}

func TestQueryBulkUpdate_DecimalLiteral(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	writeTx := &mockTx{responses: [][]map[string]any{{{"count": int64(1)}}, nil}}
	conn := &mockConn{txs: []*mockTx{writeTx}}
	mgr := MustNewManager[decimalProduct](NewDatabase(conn, "test_db"))

	if _, err := mgr.Query().Filter(Eq("sku", "widget-1")).Update(context.Background(), map[string]any{
		"price": 19.99,
	}); err != nil {
		t.Fatalf("bulk update failed: %v", err)
	}
	found := false
	for _, q := range writeTx.queries {
		if strings.Contains(q, "has price 19.99dec") {
			found = true
		}
	}
	if !found {
		t.Errorf("bulk update queries missing decimal literal: %v", writeTx.queries)
	}
}

// --- Schema emission ---

func TestGenerateSchema_DecimalValueType(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	info, ok := Lookup("decimal-product")
	if !ok {
		t.Fatal("decimal-product not registered")
	}
	schema := GenerateSchemaFor(info)
	for _, want := range []string{
		"attribute price, value decimal;",
		"attribute exact-price, value decimal;",
		"attribute tier-price, value decimal;",
		"attribute price-history, value decimal;",
		"attribute sku, value string;",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q:\n%s", want, schema)
		}
	}
}

// --- Hydration ---

func TestHydrate_DecimalFields(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	data := map[string]any{
		"sku":           "widget-1",
		"price":         "12.5",                       // driver sends decimals as strings
		"exact-price":   "0.1",                        // exact string target
		"tier-price":    "5.25",                       // pointer target
		"price-history": []any{"1.25", "3.0", "-0.5"}, // slice target
	}
	p, err := HydrateNew[decimalProduct](data)
	if err != nil {
		t.Fatalf("HydrateNew failed: %v", err)
	}
	if p.Price != 12.5 {
		t.Errorf("Price = %v, want 12.5", p.Price)
	}
	if p.Exact != "0.1" {
		t.Errorf("Exact = %q, want \"0.1\"", p.Exact)
	}
	if p.Tier == nil || *p.Tier != 5.25 {
		t.Errorf("Tier = %v, want pointer to 5.25", p.Tier)
	}
	if len(p.History) != 3 || p.History[0] != 1.25 || p.History[1] != 3.0 || p.History[2] != -0.5 {
		t.Errorf("History = %v, want [1.25 3 -0.5]", p.History)
	}
}

func TestHydrate_DecimalDefensiveInputs(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	// Numeric inputs (e.g. from JSON transports that parse eagerly) still
	// hydrate float and string targets.
	p, err := HydrateNew[decimalProduct](map[string]any{
		"sku":         "widget-2",
		"price":       12.5,  // float64 into float64 target
		"exact-price": 10.99, // float64 into string target
	})
	if err != nil {
		t.Fatalf("HydrateNew failed: %v", err)
	}
	if p.Price != 12.5 {
		t.Errorf("Price = %v, want 12.5", p.Price)
	}
	if p.Exact != "10.99" {
		t.Errorf("Exact = %q, want \"10.99\"", p.Exact)
	}

	// int64 is accepted defensively.
	p, err = HydrateNew[decimalProduct](map[string]any{"sku": "widget-3", "price": int64(7)})
	if err != nil {
		t.Fatalf("HydrateNew failed: %v", err)
	}
	if p.Price != 7.0 {
		t.Errorf("Price = %v, want 7", p.Price)
	}
}

func TestHydrate_DecimalErrors(t *testing.T) {
	ClearRegistry()
	MustRegister[decimalProduct]()

	if _, err := HydrateNew[decimalProduct](map[string]any{"sku": "x", "price": "abc"}); err == nil {
		t.Error("expected error hydrating unparseable decimal string, got nil")
	}
	if _, err := HydrateNew[decimalProduct](map[string]any{"sku": "x", "price": true}); err == nil {
		t.Error("expected error hydrating bool into decimal field, got nil")
	}
}

func TestCoerceDecimal_NamedTypes(t *testing.T) {
	type price float64
	got, err := coerceDecimal("2.5", reflect.TypeOf(price(0)))
	if err != nil {
		t.Fatalf("coerceDecimal failed: %v", err)
	}
	if v, ok := got.(price); !ok || v != 2.5 {
		t.Errorf("got %v (%T), want price(2.5)", got, got)
	}

	type code string
	got, err = coerceDecimal("2.5", reflect.TypeOf(code("")))
	if err != nil {
		t.Fatalf("coerceDecimal failed: %v", err)
	}
	if v, ok := got.(code); !ok || v != "2.5" {
		t.Errorf("got %v (%T), want code(\"2.5\")", got, got)
	}

	if _, err := coerceDecimal("2.5", reflect.TypeOf(0)); err == nil {
		t.Error("expected error coercing decimal into int target, got nil")
	}
}
