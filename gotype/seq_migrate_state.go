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

// seqRecordQuery builds the insert query that records an applied migration.
// It is shared by Record (standalone transaction) and the atomic apply path,
// which executes it inside the same transaction as the migration statements.
func seqRecordQuery(name, checksum string) string {
	now := FormatValue(time.Now().UTC())
	checksumClause := ""
	if checksum != "" {
		checksumClause = fmt.Sprintf(",\nhas %s \"%s\"", seqMigrationChecksumAttr, escapeTQL(checksum))
	}
	return fmt.Sprintf(`insert
$m isa %s,
has %s "%s",
has %s %s%s;`, seqMigrationEntity, seqMigrationNameAttr, escapeTQL(name), seqMigrationTimeAttr, now, checksumClause)
}

// seqDeleteQuery builds the match-delete query that removes a migration record.
// It is shared by Delete (standalone transaction) and the atomic rollback path.
func seqDeleteQuery(name string) string {
	return fmt.Sprintf(`match
$m isa %s, has %s "%s";
delete $m;`, seqMigrationEntity, seqMigrationNameAttr, escapeTQL(name))
}

// Record inserts a new migration record with an optional checksum.
func (s *seqMigrationState) Record(ctx context.Context, name, checksum string) error {
	_, err := s.db.ExecuteWrite(ctx, seqRecordQuery(name, checksum))
	if err != nil {
		return fmt.Errorf("seq migration state: record %q: %w", name, err)
	}
	return nil
}

// MigrationChecksum computes a SHA256 checksum for a migration's statements.
//
// Each statement is length-prefixed and the Up/Down groups are framed with
// their statement counts, so moving text across statement boundaries — or
// between the Up and Down groups — always produces a different checksum.
//
// Compatibility: go-typeql v1.12.x and earlier concatenated statements with
// no delimiter. Checksums recorded by those versions are still accepted
// during verification (see legacyMigrationChecksum), but new records are
// always written in the delimited format returned here.
func MigrationChecksum(m SequentialMigration) string {
	if m.Statements == nil {
		return ""
	}
	h := sha256.New()
	writeGroup := func(stmts []string) {
		_, _ = fmt.Fprintf(h, "%d\n", len(stmts)) // hash.Hash.Write never fails
		for _, s := range stmts {
			_, _ = fmt.Fprintf(h, "%d:%s\n", len(s), s)
		}
	}
	writeGroup(m.Statements.Up)
	writeGroup(m.Statements.Down)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// legacyMigrationChecksum reproduces the undelimited checksum format used by
// go-typeql v1.12.x and earlier. It exists only so verification can accept
// checksums recorded before the delimited format was introduced; new records
// always use MigrationChecksum.
func legacyMigrationChecksum(m SequentialMigration) string {
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
		e.Name, truncateChecksum(e.Expected), truncateChecksum(e.Actual))
}

// truncateChecksum shortens a checksum for display. Recorded checksums come
// from the database and may be arbitrarily short (hand-stamped or corrupted
// records), so it must never slice past the end of the string.
func truncateChecksum(s string) string {
	const displayLen = 12
	if len(s) <= displayLen {
		return s
	}
	return s[:displayLen] + "..."
}

// Delete removes a migration record (for rollback).
func (s *seqMigrationState) Delete(ctx context.Context, name string) error {
	_, err := s.db.ExecuteWrite(ctx, seqDeleteQuery(name))
	if err != nil {
		return fmt.Errorf("seq migration state: delete %q: %w", name, err)
	}
	return nil
}
