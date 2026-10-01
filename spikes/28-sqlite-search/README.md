# Spike #28: SQLite driver, FTS5, vectors, rqlite

Validates ADR-0019 and ADR-0021. Findings are recorded in the
*Validation* sections of those ADRs.

- `main.go` — modernc.org/sqlite (pure Go, `CGO_ENABLED=0`), FTS5, Go
  implementations of sqlite-vec's `vec_distance_cosine`, hybrid query with
  reciprocal rank fusion, atomic multi-statement insert.
  Run: `CGO_ENABLED=0 go run .`
- `rqlite.sh` — the same schema, batch and query on rqlite 10.4.0 with the
  sqlite-vec 0.1.9 loadable extension.

Both produce identical rankings and scores. This directory is reference
material, not part of the Go workspace or the build.
