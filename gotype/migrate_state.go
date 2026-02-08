// Package gotype handles the persistence and tracking of schema migrations.
package gotype

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"
)

// migrationSchemaSQL defines the TypeQL schema for tracking applied migrations.
const migrationSchemaSQL = `define
attribute migration-hash, value string;
attribute migration-summary, value string;
attribute migration-applied-at, value datetime;
entity migration-record,
    owns migration-hash @key,
    owns migration-summary,
    owns migration-applied-at;`

// MigrationRecord represents a single schema migration that has been successfully
// applied to the database.
type MigrationRecord struct {
	// Hash is the deterministic SHA-256 hash of the migration's TypeQL statements.
	Hash string
	// Summary is a human-readable description of the changes in the migration.
	Summary string
	// AppliedAt is the timestamp when the migration was recorded in the database.
	AppliedAt time.Time
}

// MigrationState provides methods for tracking and managing the history of
// applied migrations within a TypeDB database.
type MigrationState struct {
	db *Database
}

// NewMigrationState initializes a new MigrationState tracker for the given database.
func NewMigrationState(db *Database) *MigrationState {
	return &MigrationState{db: db}
}

// EnsureSchema creates the internal TypeDB schema required for tracking migrations.
// This operation is idempotent and safe to call multiple times.
func (ms *MigrationState) EnsureSchema(ctx context.Context) error {
	return ms.db.ExecuteSchema(ctx, migrationSchemaSQL)
}

// Applied retrieves all migration records stored in the database,
// ordered by their application timestamp.
func (ms *MigrationState) Applied(ctx context.Context) ([]MigrationRecord, error) {
	query := `match
$m isa migration-record;
fetch {
  "hash": $m.migration-hash,
  "summary": $m.migration-summary,
  "applied-at": $m.migration-applied-at
};`

	results, err := ms.db.ExecuteRead(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("migration state: query applied: %w", err)
	}

	var records []MigrationRecord
	for _, row := range results {
		flat := unwrapResult(row)
		r := MigrationRecord{}
		if h, ok := flat["hash"].(string); ok {
			r.Hash = h
		}
		if s, ok := flat["summary"].(string); ok {
			r.Summary = s
		}
		if t, ok := flat["applied-at"]; ok {
			switch v := t.(type) {
			case time.Time:
				r.AppliedAt = v
			case string:
				if parsed, err := time.Parse(time.RFC3339, v); err == nil {
					r.AppliedAt = parsed
				}
			}
		}
		records = append(records, r)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].AppliedAt.Before(records[j].AppliedAt)
	})

	return records, nil
}

// IsApplied checks if a migration with the specified hash has already been applied.
func (ms *MigrationState) IsApplied(ctx context.Context, hash string) (bool, error) {
	query := fmt.Sprintf(`match
$m isa migration-record, has migration-hash "%s";
reduce $count = count($m);`, hash)

	results, err := ms.db.ExecuteRead(ctx, query)
	if err != nil {
		return false, fmt.Errorf("migration state: check applied: %w", err)
	}
	if len(results) == 0 {
		return false, nil
	}
	return extractCount(results[0]) > 0, nil
}

// Record saves a new migration record to the database after it has been applied.
func (ms *MigrationState) Record(ctx context.Context, hash, summary string) error {
	now := FormatValue(time.Now().UTC())
	query := fmt.Sprintf(`insert
$m isa migration-record,
has migration-hash "%s",
has migration-summary "%s",
has migration-applied-at %s;`, hash, escapeTQL(summary), now)

	_, err := ms.db.ExecuteWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("migration state: record: %w", err)
	}
	return nil
}

// HashStatements generates a SHA-256 hash for a slice of migration statements
// to uniquely identify a set of schema changes.
func HashStatements(stmts []string) string {
	h := sha256.New()
	for _, s := range stmts {
		h.Write([]byte(s))
		h.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// MigrateWithState performs a migration while tracking progress in the database.
// It fetches the current schema automatically and ensures that identical
// migrations are not applied more than once.
func MigrateWithState(ctx context.Context, db *Database) (*SchemaDiff, error) {
	schemaStr, err := db.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: fetch schema: %w", err)
	}
	return MigrateWithStateFromSchema(ctx, db, schemaStr)
}

// MigrateWithStateFromSchema performs a migration using the provided schema string
// while tracking progress in the database. It ensures that identical migrations
// are not applied more than once.
func MigrateWithStateFromSchema(ctx context.Context, db *Database, currentSchemaStr string) (*SchemaDiff, error) {
	ms := NewMigrationState(db)

	// Ensure migration tracking schema exists
	if err := ms.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("migrate: ensure state schema: %w", err)
	}

	// Compute diff
	current, err := IntrospectSchemaFromString(currentSchemaStr)
	if err != nil {
		return nil, fmt.Errorf("migrate: parse current schema: %w", err)
	}

	diff := DiffSchemaFromRegistry(current)
	if diff.IsEmpty() {
		return diff, nil
	}

	stmts := diff.GenerateMigration()
	hash := HashStatements(stmts)

	// Check if already applied
	applied, err := ms.IsApplied(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("migrate: check state: %w", err)
	}
	if applied {
		return diff, nil // already applied, skip
	}

	// Apply migration
	for _, stmt := range stmts {
		if err := db.ExecuteSchema(ctx, stmt); err != nil {
			return diff, fmt.Errorf("migrate: execute %q: %w", stmt, err)
		}
	}

	// Record as applied
	if err := ms.Record(ctx, hash, diff.Summary()); err != nil {
		return diff, fmt.Errorf("migrate: record state: %w", err)
	}

	return diff, nil
}

// escapeTQL escapes a string for use inside a TypeQL string literal.
func escapeTQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
