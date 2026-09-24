//go:build cgo && typedb && integration

package driver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/go-typeql/v3/internal/typeqlcheck"
)

func projectionTransferSchema() string {
	var schema strings.Builder
	schema.WriteString("define attribute name, value string;")
	for _, letter := range "abcdefgh" {
		fmt.Fprintf(&schema, " attribute attr-%c, value string;", letter)
	}
	schema.WriteString(" entity projection-bench-narrow, owns name @key;")
	schema.WriteString(" entity projection-bench-wide, owns name @key")
	for _, letter := range "abcdefgh" {
		fmt.Fprintf(&schema, ", owns attr-%c", letter)
	}
	schema.WriteByte(';')
	return schema.String()
}

func projectionTransferQuery(shape, mode string) string {
	var query strings.Builder
	fmt.Fprintf(&query, "match $e isa projection-bench-%s;", shape)
	if mode == "projected" {
		query.WriteString(" $e isa! $projection_type;")
	}
	query.WriteString(` fetch { "_iid": iid($e),`)
	if mode == "projected" {
		query.WriteString(` "_type": label($projection_type),`)
	}
	query.WriteString(` "name": $e.name`)
	if shape == "wide" && mode == "full" {
		for _, letter := range "abcdefgh" {
			fmt.Fprintf(&query, `, "attr-%c": $e.attr-%c`, letter, letter)
		}
	}
	query.WriteString(" };")
	return query.String()
}

func TestProjectionTransferTypeQLSyntax(t *testing.T) {
	typeqlcheck.AssertValid(t, "projection transfer schema", projectionTransferSchema())
	for _, shape := range []string{"narrow", "wide"} {
		for _, mode := range []string{"full", "projected"} {
			typeqlcheck.AssertValid(t, shape+" "+mode, projectionTransferQuery(shape, mode))
		}
	}
}

// BenchmarkLiveProjectionTransfer counts encoded FFI buffers for the same
// 64-row narrow and wide query shapes as the ORM projection benchmark.
func BenchmarkLiveProjectionTransfer(b *testing.B) {
	conn, err := Open(testAddr(), "admin", "password")
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	dbName := fmt.Sprintf("projection_transfer_%d", time.Now().UnixNano())
	if err := conn.Databases().Create(dbName); err != nil {
		b.Fatal(err)
	}
	defer conn.Databases().Delete(dbName)
	schema, err := conn.Transaction(dbName, Schema)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := schema.Query(projectionTransferSchema()); err != nil {
		schema.Close()
		b.Fatal(err)
	}
	if err := schema.Commit(); err != nil {
		b.Fatal(err)
	}
	write, err := conn.Transaction(dbName, Write)
	if err != nil {
		b.Fatal(err)
	}
	var inserts strings.Builder
	inserts.WriteString("insert\n")
	payload := strings.Repeat("x", 256)
	for i := range 64 {
		fmt.Fprintf(&inserts, "$n%d isa projection-bench-narrow, has name \"item-%02d\";\n", i, i)
		fmt.Fprintf(&inserts, "$w%d isa projection-bench-wide, has name \"item-%02d\"", i, i)
		for _, letter := range "abcdefgh" {
			fmt.Fprintf(&inserts, ", has attr-%c \"%s\"", letter, payload)
		}
		inserts.WriteString(";\n")
	}
	if _, err := write.Query(inserts.String()); err != nil {
		write.Close()
		b.Fatal(err)
	}
	if err := write.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, shape := range []string{"narrow", "wide"} {
		for _, mode := range []string{"full", "projected"} {
			b.Run(shape+"/"+mode, func(b *testing.B) {
				query := projectionTransferQuery(shape, mode)
				var encodedBytes int64
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					tx, err := conn.Transaction(dbName, Read)
					if err != nil {
						b.Fatal(err)
					}
					rows := 0
					err = tx.queryEachConfigured(&queryMetadata{query: query}, nil, queryStreamChunkRows,
						func(_ int, bytes int) { encodedBytes += int64(bytes) },
						func(_ int, _ map[string]any) error { rows++; return nil })
					closeErr := tx.CloseChecked()
					if err != nil || closeErr != nil || rows != 64 {
						b.Fatalf("query=%v close=%v rows=%d", err, closeErr, rows)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(encodedBytes)/float64(b.N), "ffi-bytes/op")
				b.ReportMetric(64, "rows/op")
				b.ReportMetric(float64(len(query)), "query-bytes")
			})
		}
	}
}
