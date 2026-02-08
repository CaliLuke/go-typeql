.PHONY: build-rust clean test test-unit test-integration lint

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
