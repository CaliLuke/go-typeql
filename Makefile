.PHONY: build-rust test-rust clean-rust clean test test-all test-unit test-integration bench lint check diagnose-startup-hang install-typeql-check

# Version of the official TypeQL syntax checker (typedb/typedb-tools).
# Use the newest published checker that is compatible with the TypeDB server.
TYPEQL_CHECK_VERSION ?= 3.12.0

# Build the Rust FFI static library
# MACOSX_DEPLOYMENT_TARGET=13.0 matches Go 1.27's minimum supported macOS.
build-rust:
	cd driver/rust && MACOSX_DEPLOYMENT_TARGET=13.0 cargo build --release

# Run Rust FFI and direct TypeQL parser tests.
test-rust:
	cargo test --manifest-path driver/rust/Cargo.toml

# Install the official typeql-check CLI into ~/go/bin (used by the TypeQL
# syntax test gates; see internal/typeqlcheck and docs/TESTING.md).
install-typeql-check:
	@set -e; \
	OS=$$(uname -s); ARCH=$$(uname -m); \
	case "$$OS-$$ARCH" in \
		Darwin-arm64)              NAME=typeql-check-mac-arm64;    EXT=zip ;; \
		Darwin-x86_64)             NAME=typeql-check-mac-x86_64;   EXT=zip ;; \
		Linux-aarch64|Linux-arm64) NAME=typeql-check-linux-arm64;  EXT=tar.gz ;; \
		Linux-x86_64)              NAME=typeql-check-linux-x86_64; EXT=tar.gz ;; \
		*) echo "unsupported platform: $$OS $$ARCH"; exit 1 ;; \
	esac; \
	URL="https://repo.typedb.com/public/public-release/raw/names/$$NAME/versions/$(TYPEQL_CHECK_VERSION)/$$NAME-$(TYPEQL_CHECK_VERSION).$$EXT"; \
	TMP=$$(mktemp -d); trap 'rm -rf "$$TMP"' EXIT; \
	echo "Downloading $$URL"; \
	curl -sfL "$$URL" -o "$$TMP/pkg.$$EXT"; \
	if [ "$$EXT" = "zip" ]; then unzip -q "$$TMP/pkg.$$EXT" -d "$$TMP"; else tar -xzf "$$TMP/pkg.$$EXT" -C "$$TMP"; fi; \
	BIN=$$(find "$$TMP" -name typeql-check -type f | head -1); \
	[ -n "$$BIN" ] || { echo "typeql-check binary not found in archive"; exit 1; }; \
	mkdir -p "$$HOME/go/bin"; \
	install -m 0755 "$$BIN" "$$HOME/go/bin/typeql-check"; \
	"$$HOME/go/bin/typeql-check" 'match $$x isa person;' && \
	echo "Installed $$HOME/go/bin/typeql-check ($(TYPEQL_CHECK_VERSION))"

# Clean Rust build artifacts
clean-rust:
	cd driver/rust && cargo clean

# Run all unit tests (no DB required)
test-unit:
	go test ./ast/... ./gotype/...

# Run integration tests (requires TypeDB + built Rust library).
# The repo compose maps host port 1730 -> container port 1729;
# TYPEDB_GO_COMPOSE_PORT_MAP=1 opts into the localhost address translation
# that makes driver.Open("localhost:1730") reach the compose server.
test-integration:
	TEST_DB_ADDRESS=$${TEST_DB_ADDRESS:-localhost:1730} \
	TYPEDB_GO_COMPOSE_PORT_MAP=$${TYPEDB_GO_COMPOSE_PORT_MAP:-1} \
	go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...

# Run all tests
test: test-unit

# Run unit tests plus the benchmark recorder
test-all: test-unit bench

# Run benchmarks and append the results to benchmarks/benchmarks.sqlite
bench:
	go run ./cmd/benchdb

# Lint (fast — just vet)
lint:
	go vet ./ast/... ./gotype/...

# Full quality gates (unit scope): build, vet, goimports, tidy drift,
# golangci-lint, staticcheck, tests + dupl/gocyclo reports.
# Use `./check.sh --fix` to auto-format.
check:
	./check.sh

# Full clean
clean: clean-rust
	go clean ./...

# Generate and serve documentation locally
# Installs pkgsite if needed, then serves docs at http://localhost:8080
docs:
	@command -v pkgsite >/dev/null 2>&1 || { echo "Installing pkgsite..."; go install golang.org/x/pkgsite/cmd/pkgsite@latest; }
	@echo "Starting pkgsite on http://localhost:8080/github.com/CaliLuke/go-typeql"
	pkgsite -http=:8080

# Open docs in browser (macOS)
docs-open: docs &
	@sleep 2
	open http://localhost:8080/github.com/CaliLuke/go-typeql

# Diagnose startup hangs in cgo/typedb test binary initialisation.
# Usage:
#   make diagnose-startup-hang
#   TIMEOUT_SEC=45 make diagnose-startup-hang
diagnose-startup-hang:
	@set -e; \
	TIMEOUT_SEC=$${TIMEOUT_SEC:-30}; \
	TEST_TIMEOUT=$${TEST_TIMEOUT:-20s}; \
	LOG_OUT=/tmp/go-typeql-startup-hang-$$(date +%Y%m%d-%H%M%S).log; \
	SAMPLE_OUT=""; \
	echo "Running startup-only smoke (timeout=$$TIMEOUT_SEC s, go test -timeout=$$TEST_TIMEOUT)"; \
	( go test -tags "cgo,typedb" ./driver -run '^$$' -count=1 -timeout $$TEST_TIMEOUT -v > "$$LOG_OUT" 2>&1 ) & \
	TEST_PID=$$!; \
	START=$$(date +%s); \
	while kill -0 $$TEST_PID 2>/dev/null; do \
		NOW=$$(date +%s); \
		ELAPSED=$$((NOW - START)); \
		if [ $$ELAPSED -ge $$TIMEOUT_SEC ]; then \
			echo "Detected startup hang after $$ELAPSED s"; \
			DRIVER_PID=$$(ps -ef | awk '/go-build.*\/driver\.test/ && !/awk/ { print $$2; exit }'); \
			if [ -n "$$DRIVER_PID" ]; then \
				SAMPLE_OUT=/tmp/driver.test_$$(date +%Y%m%d-%H%M%S).sample.txt; \
				echo "Sampling stuck driver.test pid=$$DRIVER_PID -> $$SAMPLE_OUT"; \
				sample $$DRIVER_PID 1 1 > "$$SAMPLE_OUT" 2>&1 || true; \
			else \
				echo "No driver.test pid found for sampling"; \
			fi; \
			kill -TERM $$TEST_PID 2>/dev/null || true; \
			sleep 1; \
			kill -KILL $$TEST_PID 2>/dev/null || true; \
			echo "go test log: $$LOG_OUT"; \
			if [ -n "$$SAMPLE_OUT" ]; then echo "sample log: $$SAMPLE_OUT"; fi; \
			exit 1; \
		fi; \
		sleep 1; \
	done; \
	wait $$TEST_PID; \
	STATUS=$$?; \
	echo "go test log: $$LOG_OUT"; \
	if [ $$STATUS -ne 0 ]; then \
		echo "go test exited with status $$STATUS"; \
		exit $$STATUS; \
	fi; \
	echo "startup-only smoke completed without hang"
