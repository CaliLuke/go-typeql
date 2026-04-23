---
name: release-checks
description: This skill should be used when preparing and publishing a new go-typeql version to the Go module registry and GitHub. Triggers include "/release-checks", "release", "publish", "tag a version", "cut a release", "ship vX.Y.Z", "bump the version", or "prepare a release". Runs the 14-step checklist (tests, coverage, vet, golangci-lint, staticcheck, docs regen, tagging, changelog, pkg.go.dev verification).
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
make build-rust
podman compose up -d
TEST_DB_ADDRESS=localhost:1730 go test -tags "cgo,typedb,integration" ./driver/... ./gotype/...
```

This step must prove both release surfaces still work:

- The pure-Go packages (`ast/`, `gotype/`, `tqlgen/`) still pass from a clean checkout.
- The CGo driver still links after producing `driver/rust/target/release/libtypedb_go_ffi.a`.

The TypeDB container can be left running for subsequent steps; no teardown needed unless the user wants a clean environment (`podman compose down`).

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
~/go/bin/staticcheck ./...
```

## 5. Verify go.mod is tidy

```bash
go mod tidy
git diff go.mod go.sum
```

Should produce no diff. If it does, commit the tidied result as part of step 9 and continue.

## 6. Regenerate reference docs

If any exported symbols changed:

```bash
~/go/bin/gomarkdoc ./ast/ > docs/api/reference/ast.md
~/go/bin/gomarkdoc ./gotype/ > docs/api/reference/gotype.md
~/go/bin/gomarkdoc ./tqlgen/ > docs/api/reference/tqlgen.md
```

## 7. Update AGENTS.md test count

Count tests with:

```bash
go test ./ast/... ./gotype/... ./tqlgen/... -v 2>&1 | grep -c "^--- PASS"
```

Update the number in the comment at the top of the Commands section in `AGENTS.md`. `CLAUDE.md` is a symlink to `AGENTS.md`. If the comment is no longer at that location, grep for the prior count to find it.

## 8. Verify upstream dependency versions

Check that all upstream version pins are consistent with each other and with the target release. The canonical locations are:

| Dependency                        | File                           | Field                                     |
| --------------------------------- | ------------------------------ | ----------------------------------------- |
| `typedb-driver` (Rust crate)      | `driver/rust/Cargo.toml`       | `typedb-driver = { version = "..." }`     |
| `typeql` (Rust crate)             | `driver/rust/Cargo.toml`       | `typeql = "..."`                          |
| TypeDB server (integration tests) | `docker-compose.yml`           | `image: typedb/typedb:...`                |
| TypeQL grammar reference          | `typeql-reference/README.md`   | tag mention in prose                      |
| TypeQL grammar file               | `typeql-reference/typeql.pest` | vendored copy — diff against upstream tag |
| Driver version mention            | `docs/DEVELOPMENT.md`          | prose reference                           |
| Driver version mention            | `docs/api/driver.md`           | prose reference                           |

Version sources to check:

- `typedb-driver` crate: <https://crates.io/crates/typedb-driver>
- `typeql` crate: <https://crates.io/crates/typeql>
- TypeDB server releases: <https://github.com/typedb/typedb/releases>
- TypeQL grammar releases: <https://github.com/typedb/typeql/releases>

After updating `Cargo.toml`, always run `cargo update` inside `driver/rust/` to regenerate `Cargo.lock` — the lock file is what actually determines what gets downloaded during `make build-rust`.

## 9. Update installation and release docs

Update every user-facing version reference and artifact instruction:

```bash
go get github.com/CaliLuke/go-typeql@$ARGUMENTS
```

Specifically verify:

- `README.md` uses the new version in both `go get` and `gh release download` examples.
- Build/install docs reference the generic archive name `libtypedb_go_ffi.a` (the name after download/rename).
- Release-download docs reference the per-platform names (`libtypedb_go_ffi-<os>-<arch>.a`, see step 14).
- Docs clearly state that `go get` downloads source only; it does not build the Rust archive automatically.

## 10. Commit outstanding changes

Commit everything from steps 5–9.

## 11. Tag the release

Push `main` first so CI can verify before the tag goes out:

```bash
git push origin main
git tag $ARGUMENTS
git push origin $ARGUMENTS
```

## 12. Create a GitHub release

Use `--generate-notes` to seed an initial changelog from commits — you'll replace it in step 13.

```bash
gh release create $ARGUMENTS --generate-notes --title "$ARGUMENTS"
```

## 13. Write a changelog

Edit the release notes with a human-written summary: new features, new types/functions, options, documentation changes. Use the auto-generated notes from step 12 as a reference. Omit internal-only changes (instruction-file edits, benchmark DB churn, local housekeeping).

```bash
gh release edit $ARGUMENTS --notes "..."
```

## 14. Verify published assets and pkg.go.dev

Visit `https://pkg.go.dev/github.com/CaliLuke/go-typeql@$ARGUMENTS`. Force indexing if needed:

```bash
GOPROXY=https://proxy.golang.org go get github.com/CaliLuke/go-typeql@$ARGUMENTS
```

Also verify the GitHub release contains the expected Rust static libraries and that their names match the documented install flow:

- `libtypedb_go_ffi-linux-amd64.a`
- `libtypedb_go_ffi-darwin-amd64.a`
- `libtypedb_go_ffi-darwin-arm64.a`

## 15. Report completion

Report back to the user with:

- The release URL (`https://github.com/CaliLuke/go-typeql/releases/tag/$ARGUMENTS`)
- The pkg.go.dev URL
- A one-line summary of test count, coverage, and any skipped/deferred steps
