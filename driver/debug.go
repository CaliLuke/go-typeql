//go:build cgo && typedb

package driver

import (
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var txCounter uint64
var activeTxOpen int64
var activeTxQuery int64
var activeTxOpenHighWater int64
var activeTxQueryHighWater int64

func nextTxID() uint64 {
	return atomic.AddUint64(&txCounter, 1)
}

func debugEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("TYPEDB_GO_DEBUG")))
	switch v {
	case "1", "true", "yes", "on", "debug":
		return true
	default:
		return false
	}
}

func slowThreshold() time.Duration {
	v := strings.TrimSpace(os.Getenv("TYPEDB_GO_DEBUG_SLOW_MS"))
	if v == "" {
		return 2 * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 2 * time.Second
	}
	return time.Duration(n) * time.Millisecond
}

func parseIntEnv(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func txOpenWarnThreshold() int64 {
	return int64(parseIntEnv("TYPEDB_GO_DEBUG_TX_OPEN_WARN", 64))
}

func txQueryWarnThreshold() int64 {
	return int64(parseIntEnv("TYPEDB_GO_DEBUG_TX_QUERY_WARN", 32))
}

func queryOperation(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "unknown"
	}
	first := strings.ToLower(strings.Trim(strings.Fields(trimmed)[0], ";"))
	switch first {
	case "match", "insert", "delete", "update", "define", "undefine", "fetch", "reduce":
		return first
	default:
		return "other"
	}
}

func queryFingerprint(query string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(query))
	return fmt.Sprintf("%016x", h.Sum64())
}

func updateHighWater(current int64, highWater *int64) int64 {
	for {
		existing := atomic.LoadInt64(highWater)
		if current <= existing {
			return existing
		}
		if atomic.CompareAndSwapInt64(highWater, existing, current) {
			return current
		}
	}
}

func logInFlight(name string, current int64, highWater int64, threshold int64, attrs ...any) {
	base := []any{"active", current, "high_water", highWater, "warn_threshold", threshold}
	base = append(base, attrs...)
	if debugEnabled() {
		slog.Info("typedb_go."+name, base...)
	}
	if current > threshold {
		slog.Warn("typedb_go."+name+".high", base...)
	}
}

func incrementInFlight(counter *int64, highWater *int64, threshold int64, name string, attrs ...any) {
	current := atomic.AddInt64(counter, 1)
	high := updateHighWater(current, highWater)
	logInFlight(name, current, high, threshold, attrs...)
}

func decrementInFlight(counter *int64, highWater *int64, threshold int64, name string, attrs ...any) {
	current := atomic.AddInt64(counter, -1)
	if current < 0 {
		atomic.StoreInt64(counter, 0)
		current = 0
	}
	high := atomic.LoadInt64(highWater)
	logInFlight(name, current, high, threshold, attrs...)
}

func incActiveTxOpen(attrs ...any) {
	incrementInFlight(&activeTxOpen, &activeTxOpenHighWater, txOpenWarnThreshold(), "tx.open_inflight", attrs...)
}

func decActiveTxOpen(attrs ...any) {
	decrementInFlight(&activeTxOpen, &activeTxOpenHighWater, txOpenWarnThreshold(), "tx.open_inflight", attrs...)
}

func incActiveTxQuery(attrs ...any) {
	incrementInFlight(&activeTxQuery, &activeTxQueryHighWater, txQueryWarnThreshold(), "tx.query_inflight", attrs...)
}

func decActiveTxQuery(attrs ...any) {
	decrementInFlight(&activeTxQuery, &activeTxQueryHighWater, txQueryWarnThreshold(), "tx.query_inflight", attrs...)
}

func logFFIDebug(event string, attrs ...any) {
	if !debugEnabled() {
		return
	}
	slog.Info("typedb_go."+event, attrs...)
}

func logFFIDuration(event string, start time.Time, attrs ...any) {
	elapsed := time.Since(start)
	attrs = append(attrs, "elapsed_ms", elapsed.Milliseconds())
	if elapsed >= slowThreshold() {
		slog.Warn("typedb_go."+event+".slow", attrs...)
		return
	}
	if debugEnabled() {
		slog.Info("typedb_go."+event, attrs...)
	}
}
