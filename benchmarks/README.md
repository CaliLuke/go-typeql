# Benchmarks

This directory stores benchmark history for the repository.

- `benchmarks.sqlite` is the canonical benchmark database and is committed with the repo.
- Each `make bench` run appends a new benchmark run with git metadata, machine metadata, and the raw `go test` output.
- The benchmark suite currently records the audit hotspots in `ast/` and `gotype/`.

Run benchmarks with:

```bash
make bench
```

The benchmark recorder prints the current numbers and compares them to the previous recorded run for each benchmark.
