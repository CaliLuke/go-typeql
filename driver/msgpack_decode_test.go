//go:build cgo && typedb

package driver

import (
	"bytes"
	"errors"
	"maps"
	"reflect"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestDecodeMsgpackKeyReuseMatchesGenericDecoder(t *testing.T) {
	wide := make(map[string]any)
	for i := range 300 {
		wide[string(bytes.Repeat([]byte{byte(i%26 + 'a')}, i+1))] = i
	}
	cases := []any{
		[]map[string]any(nil),
		[]map[string]any{},
		[]map[string]any{{"name": "Alice", "n": int64(12), "nil": nil}, {"other": true, "name": "Bob"}},
		[]map[string]any{{"nested": map[string]any{"name": "child", "list": []any{map[string]any{"name": "leaf"}, nil, 1.5}}}},
		[]map[string]any{wide, wide},
	}
	for _, input := range cases {
		data, err := msgpack.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var generic []map[string]any
		dec := msgpack.NewDecoder(bytes.NewReader(data))
		dec.UseLooseInterfaceDecoding(true)
		if err := dec.Decode(&generic); err != nil {
			t.Fatal(err)
		}
		got, err := decodeMsgpackBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, generic) {
			t.Fatalf("decode differs: got %#v; generic %#v", got, generic)
		}
		for i := range data {
			data[i] = 0
		}
		if !reflect.DeepEqual(got, generic) {
			t.Fatal("decoded keys or values retained input buffer")
		}
	}
}

func TestDecodeMsgpackKeyReuseErrors(t *testing.T) {
	for _, data := range [][]byte{
		{0xc1},                               // invalid array header
		{0x91, 0x81, 0xa4, 'n', 'a'},         // truncated key
		{0x91, 0x81, 0xa1, 'k'},              // missing value
		{0x91, 0x81, 0x01, 0x02},             // non-string key
		{0x91, 0xdf, 0xff, 0xff, 0xff, 0xff}, // huge declared map, no entries
		{0xdd, 0xff, 0xff, 0xff, 0xff},       // huge declared array, no rows
	} {
		if _, err := decodeMsgpackBytes(data); err == nil {
			t.Fatalf("expected decoding error for %x", data)
		}
	}
}

func TestDecodeMsgpackEachKeyReuseAndCallbackError(t *testing.T) {
	input := []map[string]any{
		{"name": "Alice", "details": map[string]any{"items": []any{"value", int64(2)}}},
		{"name": "Bob", "other": true},
	}
	data, err := msgpack.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	count, err := decodeMsgpackEachBytes(data, func(_ int, row map[string]any) error {
		got = append(got, maps.Clone(row))
		return nil
	})
	if err != nil || count != len(input) || !reflect.DeepEqual(got, input) {
		t.Fatalf("decoded %d rows: %#v, %v", count, got, err)
	}
	for i := range data {
		data[i] = 0
	}
	if !reflect.DeepEqual(got, input) {
		t.Fatal("callback keys or values retained input buffer")
	}
	data, _ = msgpack.Marshal(input)
	want := errors.New("stop callback")
	if _, err := decodeMsgpackEachBytes(data, func(_ int, _ map[string]any) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback error = %v, want %v", err, want)
	}
	if _, err := decodeMsgpackEachBytes(data, func(_ int, _ map[string]any) error { return nil }); err != nil {
		t.Fatalf("decoder reuse after callback error: %v", err)
	}
	for _, malformed := range [][]byte{{0x91, 0x81, 0xa4, 'n', 'a'}, {0x91, 0x81, 0x01, 0x02}} {
		if _, err := decodeMsgpackEachBytes(malformed, func(_ int, _ map[string]any) error { return nil }); err == nil {
			t.Fatalf("expected callback decoding error for %x", malformed)
		}
	}
}
