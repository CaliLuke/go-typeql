.PHONY: build-rust clean-rust clean test test-all test-unit test-integration bench lint diagnose-startup-hang

# Build the Rust FFI static library
# MACOSX_DEPLOYMENT_TARGET=11.0 matches Go's -mmacosx-version-min=11.0
build-rust:
	cd driver/rust && MACOSX_DEPLOYMENT_TARGET=11.0 cargo build --release

# Clean Rust build artifacts
clean-rust:
	cd driver/rust && cargo clean

# Run all unit tests (no DB required)
test-unit:
	go test ./ast/... ./gotype/...

# Run integration tests (requires TypeDB + built Rust library)
test-integration:
	go test -tags integration ./driver/... ./gotype/...

# Run all tests
test: test-unit

# Run unit tests plus the benchmark recorder
test-all: test-unit bench

# Run benchmarks and append the results to benchmarks/benchmarks.sqlite
bench:
	go run ./cmd/benchdb

# Lint
lint:
	go vet ./ast/... ./gotype/...

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
