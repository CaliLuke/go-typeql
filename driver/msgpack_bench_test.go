//go:build cgo && typedb

package driver

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func BenchmarkDecodeMsgpack(b *testing.B) {
	for _, shape := range []string{"flat", "nested", "wide", "keys-only", "string-value"} {
		b.Run(shape, func(b *testing.B) {
			rows := make([]map[string]any, 1000)
			for i := range rows {
				row := map[string]any{"name": fmt.Sprintf("value-%d", i), "score": i, "active": true}
				switch shape {
				case "keys-only":
					row = map[string]any{"key": i}
				case "string-value":
					row = map[string]any{"key": fmt.Sprintf("unique-string-value-%08d-with-long-content", i)}
				case "nested":
					row["details"] = map[string]any{"label": "shared", "items": []any{i, "text"}}
				case "wide":
					for j := range 24 {
						row[fmt.Sprintf("field-%d", j)] = j
					}
				}
				rows[i] = row
			}
			data, err := msgpack.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []string{"generic", "reused-keys"} {
				b.Run(mode, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()
					for range b.N {
						if mode == "generic" {
							var results []map[string]any
							dec := msgpackDecoderPool.Get().(*msgpack.Decoder)
							dec.Reset(bytes.NewReader(data))
							dec.UseLooseInterfaceDecoding(true)
							err := dec.Decode(&results)
							dec.Reset(nil)
							msgpackDecoderPool.Put(dec)
							if err != nil {
								b.Fatal(err)
							}
						} else if _, err := decodeMsgpackBytes(data); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkDecodeMsgpackEachKeys isolates callback-mode key reuse for flat,
// nested, and wide rows. The generic case uses the original DecodeString path.
func BenchmarkDecodeMsgpackEachKeys(b *testing.B) {
	for _, shape := range []string{"flat", "nested", "wide"} {
		b.Run(shape, func(b *testing.B) {
			rows := make([]map[string]any, 1000)
			for i := range rows {
				row := map[string]any{"name": fmt.Sprintf("value-%d", i), "score": i, "active": true}
				if shape == "nested" {
					row["details"] = map[string]any{"label": "shared", "items": []any{i, "text"}}
				}
				if shape == "wide" {
					for j := range 24 {
						row[fmt.Sprintf("field-%d", j)] = j
					}
				}
				rows[i] = row
			}
			data, err := msgpack.Marshal(rows)
			if err != nil {
				b.Fatal(err)
			}
			consume := func(_ int, _ map[string]any) error { return nil }
			for _, mode := range []string{"generic", "reused-keys"} {
				b.Run(mode, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					b.ResetTimer()
					for range b.N {
						if mode == "generic" {
							if err := decodeMsgpackEachGeneric(data, consume); err != nil {
								b.Fatal(err)
							}
						} else if _, err := decodeMsgpackEachBytes(data, consume); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func decodeMsgpackEachGeneric(data []byte, consume func(int, map[string]any) error) error {
	dec := msgpack.NewDecoder(bytes.NewReader(data))
	dec.UseLooseInterfaceDecoding(true)
	count, err := dec.DecodeArrayLen()
	if err != nil {
		return err
	}
	row := make(map[string]any)
	for range count {
		clear(row)
		fields, err := dec.DecodeMapLen()
		if err != nil {
			return err
		}
		for range fields {
			key, err := dec.DecodeString()
			if err != nil {
				return err
			}
			value, err := dec.DecodeInterfaceLoose()
			if err != nil {
				return err
			}
			row[key] = value
		}
		if err := consume(count, row); err != nil {
			return err
		}
	}
	return nil
}
