// Package gotype provides state tracking for sequential migrations.
package gotype

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// Constants for the sequential migration state entity and attributes.
const (
	seqMigrationEntity       = "seq-migration-record"
	seqMigrationNameAttr     = "seq-migration-name"
	seqMigrationTimeAttr     = "seq-migration-applied-at"
	seqMigrationChecksumAttr = "seq-migration-checksum"
)

// seqMigrationSchemaSQL defines the TypeQL schema for tracking sequential migrations.
const seqMigrationSchemaSQL = `define
attribute seq-migration-name, value string;
attribute seq-migration-applied-at, value datetime;
attribute seq-migration-checksum, value string;
entity seq-migration-record,
    owns seq-migration-name @key,
    owns seq-migration-applied-at,
    owns seq-migration-checksum;`

// seqMigrationState tracks applied sequential migrations in the database.
type seqMigrationState struct {
	db *Database
}

// newSeqMigrationState creates a new state tracker.
func newSeqMigrationState(db *Database) *seqMigrationState {
	return &seqMigrationState{db: db}
}

// EnsureSchema creates the schema for tracking sequential migrations. Idempotent.
func (s *seqMigrationState) EnsureSchema(ctx context.Context) error {
	return s.db.ExecuteSchema(ctx, seqMigrationSchemaSQL)
}

// seqMigrationRecord holds applied migration metadata.
type seqMigrationRecord struct {
	AppliedAt time.Time
	Checksum  string
}

// Applied returns a map of migration name to record (time + checksum).
func (s *seqMigrationState) Applied(ctx context.Context) (map[string]seqMigrationRecord, error) {
	query := fmt.Sprintf(`match
$m isa %s;
fetch {
  "name": $m.%s,
  "applied-at": $m.%s,
  "checksum": $m.%s
};`, seqMigrationEntity, seqMigrationNameAttr, seqMigrationTimeAttr, seqMigrationChecksumAttr)

	results, err := s.db.ExecuteRead(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("seq migration state: query applied: %w", err)
	}

	applied := make(map[string]seqMigrationRecord)
	for _, row := range results {
		flat := unwrapResult(row)
		name, _ := flat["name"].(string)
		if name == "" {
			continue
		}
		var rec seqMigrationRecord
		switch v := flat["applied-at"].(type) {
		case time.Time:
			rec.AppliedAt = v
		case string:
			if parsed, parseErr := time.Parse(time.RFC3339, v); parseErr == nil {
				rec.AppliedAt = parsed
			}
		}
		if cs, ok := flat["checksum"].(string); ok {
			rec.Checksum = cs
		}
		applied[name] = rec
	}
	return applied, nil
}

// Record inserts a new migration record with an optional checksum.
func (s *seqMigrationState) Record(ctx context.Context, name, checksum string) error {
	now := FormatValue(time.Now().UTC())
	checksumClause := ""
	if checksum != "" {
		checksumClause = fmt.Sprintf(",\nhas %s \"%s\"", seqMigrationChecksumAttr, escapeTQL(checksum))
	}
	query := fmt.Sprintf(`insert
$m isa %s,
has %s "%s",
has %s %s%s;`, seqMigrationEntity, seqMigrationNameAttr, escapeTQL(name), seqMigrationTimeAttr, now, checksumClause)

	_, err := s.db.ExecuteWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("seq migration state: record %q: %w", name, err)
	}
	return nil
}

// MigrationChecksum computes a SHA256 checksum for a migration's statements.
func MigrationChecksum(m SequentialMigration) string {
	if m.Statements == nil {
		return ""
	}
	h := sha256.New()
	for _, s := range m.Statements.Up {
		h.Write([]byte(s))
	}
	h.Write([]byte("|"))
	for _, s := range m.Statements.Down {
		h.Write([]byte(s))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ChecksumMismatchError is returned when a migration's checksum doesn't match
// what was recorded when it was first applied.
type ChecksumMismatchError struct {
	Name     string
	Expected string
	Actual   string
}

func (e *ChecksumMismatchError) Error() string {
	return fmt.Sprintf("seq migration %q: checksum mismatch (recorded %s, current %s) — migration file may have been tampered with",
		e.Name, e.Expected[:12]+"...", e.Actual[:12]+"...")
}

// Delete removes a migration record (for rollback).
func (s *seqMigrationState) Delete(ctx context.Context, name string) error {
	query := fmt.Sprintf(`match
$m isa %s, has %s "%s";
delete $m;`, seqMigrationEntity, seqMigrationNameAttr, escapeTQL(name))

	_, err := s.db.ExecuteWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("seq migration state: delete %q: %w", name, err)
	}
	return nil
}
