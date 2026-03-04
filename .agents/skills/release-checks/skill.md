---
name: release-checks
description: Codex release checklist for publishing a go-typeql version safely and repeatably.
argument-hint: "<version, e.g. v1.5.0>"
---

# Codex Release Checks for go-typeql

Run this checklist in order. Stop on first failure and report the exact failing step.

Target version: `$ARGUMENTS`

## 1. Confirm version

- Require an explicit semver version (e.g. `v1.5.0`).
- If breaking changes exist, release as major and confirm module-path policy.
- Verify tag does not already exist:

```bash
git tag --list | rg "^<version>$"
```

## 2. Validate working tree

- Check for unrelated/uncommitted changes before release prep:

```bash
git status --short
```

- If dirty, confirm with user what should be included.

## 3. Run unit tests

```bash
go test ./ast/... ./gotype/... ./tqlgen/...
```

## 4. Run integration tests

```bash
podman compose up -d
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## 5. Run static checks

```bash
go vet ./...
golangci-lint run ./...
```

## 6. Check coverage for changed surfaces

```bash
go test -coverprofile=coverage.out ./ast/... ./gotype/... ./tqlgen/...
go tool cover -func=coverage.out | tail -1
```

Review any newly introduced public API paths with weak/no coverage.

## 7. Verify module hygiene

```bash
go mod tidy
git diff -- go.mod go.sum
```

Expected: no diff unless intentionally changing dependencies.

## 8. Regenerate docs when exported API changes

```bash
~/go/bin/gomarkdoc ./ast/ > docs/api/reference/ast.md
~/go/bin/gomarkdoc ./gotype/ > docs/api/reference/gotype.md
~/go/bin/gomarkdoc ./tqlgen/ > docs/api/reference/tqlgen.md
```

## 9. Update release-facing docs

- Update version-pinned install line in `README.md`.
- If command counts/quality gates changed, update `CLAUDE.md` notes accordingly.

## 10. Final verification sweep

```bash
go test ./ast/... ./gotype/... ./tqlgen/...
go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

## 11. Commit release prep

```bash
git add -A
git commit -m "release: prepare <version>"
```

## 12. Tag and push

```bash
git tag <version>
git push origin main
git push origin <version>
```

## 13. Create and refine GitHub release

```bash
gh release create <version> --generate-notes --title "<version>"
```

Then edit notes with a concise human changelog focused on user-visible changes.

## 14. Verify public availability

- Check package page: `https://pkg.go.dev/github.com/CaliLuke/go-typeql@<version>`
- Trigger proxy fetch if needed:

```bash
GOPROXY=https://proxy.golang.org GO111MODULE=on go get github.com/CaliLuke/go-typeql@<version>
```

## 15. Post-release sanity

- Confirm tag + release artifact visibility on GitHub.
- Confirm CI for the release commit/tag is green.
- Capture follow-up items in issues, not in release notes.
