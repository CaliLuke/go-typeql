# Changelog

## v1.15.0-alpha.1 - 2026-08-12

- Added native entity, relation, attribute, and role rename operations.
- Added atomic tracked rename migrations with reverse-order rollback.
- Deprecated the create-only `RenameAttribute` operation without changing its output.
- Updated the TypeDB server to `3.12.2` and the Rust driver to `3.12.3`.
- Updated the build to Go `1.27rc2`, Rust `1.97.1`, and macOS `13` or later.
- Reduced allocations for result decoding, row encoding, and query-result streaming.
