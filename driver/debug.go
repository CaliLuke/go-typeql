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

var txCounter atomic.Uint64
var activeTxOpen atomic.Int64
var activeTxQuery atomic.Int64
var activeTxOpenHighWater atomic.Int64
var activeTxQueryHighWater atomic.Int64

func nextTxID() uint64 {
	return txCounter.Add(1)
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
	case "given", "match", "insert", "delete", "update", "define", "undefine", "fetch", "reduce":
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

func updateHighWater(current int64, highWater *atomic.Int64) int64 {
	for {
		existing := highWater.Load()
		if current <= existing {
			return existing
		}
		if highWater.CompareAndSwap(existing, current) {
			return current
		}
	}
}

func logInFlightLazy(name string, current int64, highWater int64, threshold int64, attrs func() []any) {
	debug := debugEnabled()
	if !debug && current <= threshold {
		return
	}
	base := []any{"active", current, "high_water", highWater, "warn_threshold", threshold}
	base = append(base, attrs()...)
	if debug {
		slog.Info("typedb_go."+name, base...)
	}
	if current > threshold {
		slog.Warn("typedb_go."+name+".high", base...)
	}
}

func incrementInFlightLazy(counter *atomic.Int64, highWater *atomic.Int64, threshold int64, name string, attrs func() []any) {
	current := counter.Add(1)
	high := updateHighWater(current, highWater)
	logInFlightLazy(name, current, high, threshold, attrs)
}

func decrementInFlightLazy(counter *atomic.Int64, highWater *atomic.Int64, threshold int64, name string, attrs func() []any) {
	current := counter.Add(-1)
	if current < 0 {
		counter.Store(0)
		current = 0
	}
	high := highWater.Load()
	logInFlightLazy(name, current, high, threshold, attrs)
}

func incActiveTxOpenLazy(attrs func() []any) {
	incrementInFlightLazy(&activeTxOpen, &activeTxOpenHighWater, txOpenWarnThreshold(), "tx.open_inflight", attrs)
}

func decActiveTxOpenLazy(attrs func() []any) {
	decrementInFlightLazy(&activeTxOpen, &activeTxOpenHighWater, txOpenWarnThreshold(), "tx.open_inflight", attrs)
}

func incActiveTxQueryLazy(attrs func() []any) {
	incrementInFlightLazy(&activeTxQuery, &activeTxQueryHighWater, txQueryWarnThreshold(), "tx.query_inflight", attrs)
}

func decActiveTxQueryLazy(attrs func() []any) {
	decrementInFlightLazy(&activeTxQuery, &activeTxQueryHighWater, txQueryWarnThreshold(), "tx.query_inflight", attrs)
}

func logFFIDebugLazy(event string, attrs func() []any) {
	if debugEnabled() {
		slog.Info("typedb_go."+event, attrs()...)
	}
}

func logFFIDurationLazy(event string, start time.Time, attrs func() []any) {
	elapsed := time.Since(start)
	slow := elapsed >= slowThreshold()
	debug := debugEnabled()
	if !slow && !debug {
		return
	}
	fields := append(attrs(), "elapsed_ms", elapsed.Milliseconds())
	if slow {
		slog.Warn("typedb_go."+event+".slow", fields...)
		return
	}
	slog.Info("typedb_go."+event, fields...)
}
