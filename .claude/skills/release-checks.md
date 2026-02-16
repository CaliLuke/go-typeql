---
name: release-checks
description: Run the full pre-release checklist for publishing a new go-typeql version to the Go module registry. Use when the user says "release", "publish", "tag a version", or "prepare a release".
argument-hint: "<version, e.g. v1.2.0>"
---

# Release Checks for go-typeql

Follow every step in order. Stop and report if any step fails.

The target version is: $ARGUMENTS

## 1. Decide the version

Follow semver:

- **Patch** (`v1.0.2`): bug fixes, doc updates, no API changes
- **Minor** (`v1.1.0`): new features, backward-compatible API additions
- **Major** (`v2.0.0`): breaking changes (requires module path change to `github.com/CaliLuke/go-typeql/v2`)

If `$ARGUMENTS` is empty, ask the user what version to release.

## 2. Run the full test suite

```bash
go test ./ast/... ./gotype/... ./tqlgen/...
podman compose up -d
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## 3. Check test coverage

```bash
go test -coverprofile=coverage.out ./ast/... ./gotype/... ./tqlgen/...
go tool cover -func=coverage.out | tail -1
```

Review any significant uncovered paths in new/changed code. No hard threshold, but don't ship untested public APIs.

## 4. Run linters

```bash
go vet ./...
golangci-lint run ./...
```

## 5. Verify go.mod is tidy

```bash
go mod tidy
git diff go.mod go.sum
```

Should produce no diff.

## 6. Regenerate reference docs

If any exported symbols changed:

```bash
~/go/bin/gomarkdoc ./ast/ > docs/api/reference/ast.md
~/go/bin/gomarkdoc ./gotype/ > docs/api/reference/gotype.md
~/go/bin/gomarkdoc ./tqlgen/ > docs/api/reference/tqlgen.md
```

## 7. Update CLAUDE.md test count

Count tests with:

```bash
go test ./ast/... ./gotype/... ./tqlgen/... -v 2>&1 | grep -c "^--- PASS"
```

Update the number in the comment at the top of the Commands section in CLAUDE.md.

## 8. Update version in README.md

The `go get` install command pins a version — update it to the new version:

```bash
go get github.com/CaliLuke/go-typeql@<version>
```

## 9. Commit outstanding changes

Commit everything from steps 5-8.

## 10. Tag the release

```bash
git tag <version>
git push origin main <version>
```

## 11. Create a GitHub release

```bash
gh release create <version> --generate-notes --title "<version>"
```

## 12. Write a changelog

Edit the release notes with a human-written summary: new features, new types/functions, options, documentation changes. Omit internal-only changes (CLAUDE.md edits, memory updates).

```bash
gh release edit <version> --notes "..."
```

## 13. Verify on pkg.go.dev

Visit `https://pkg.go.dev/github.com/CaliLuke/go-typeql@<version>`. Force indexing if needed:

```bash
GOPROXY=https://proxy.golang.org GO111MODULE=on go get github.com/CaliLuke/go-typeql@<version>
```
