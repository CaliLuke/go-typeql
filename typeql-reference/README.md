# TypeQL Grammar Reference

This directory vendors a copy of the upstream TypeQL grammar used as a reference when reviewing parser compatibility and language changes.

## Source

The grammar file in this directory:

- [typeql.pest](./typeql.pest)

is copied from the `typedb/typeql` repository at:

- `rust/parser/typeql.pest`

The current vendored copy matches the upstream `typedb/typeql` tag `3.10.0`.

## Why This Exists

`go-typeql` does not consume the upstream Pest grammar directly. The project has its own Go-side parser and compiler logic in `tqlgen/` and `ast/`.

This vendored grammar is kept here so changes in the official TypeQL grammar can be reviewed against:

- `tqlgen` schema parsing behavior
- AST/query generation expectations
- reserved-word and identifier rules
- release-to-release language changes

## Refreshing The File

To update this reference:

1. Clone or fetch the desired `typedb/typeql` tag.
2. Copy `rust/parser/typeql.pest` into this directory as `typeql.pest`.
3. Diff the new grammar against the previous vendored copy.
4. Review whether any `tqlgen/`, `ast/`, tests, or docs need updating.

Example:

```bash
git clone --depth 1 --branch <tag> https://github.com/typedb/typeql /tmp/typeql-<tag>
cp /tmp/typeql-<tag>/rust/parser/typeql.pest typeql-reference/typeql.pest
git diff -- typeql-reference/typeql.pest
```
