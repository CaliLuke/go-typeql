// Package gotype provides state tracking for sequential migrations.
package gotype

import (
	"context"
	"fmt"
	"time"
)

// Constants for the sequential migration state entity and attributes.
const (
	seqMigrationEntity   = "seq-migration-record"
	seqMigrationNameAttr = "seq-migration-name"
	seqMigrationTimeAttr = "seq-migration-applied-at"
)

// seqMigrationSchemaSQL defines the TypeQL schema for tracking sequential migrations.
const seqMigrationSchemaSQL = `define
attribute seq-migration-name, value string;
attribute seq-migration-applied-at, value datetime;
entity seq-migration-record,
    owns seq-migration-name @key,
    owns seq-migration-applied-at;`

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

// Applied returns a map of migration name to applied-at time.
func (s *seqMigrationState) Applied(ctx context.Context) (map[string]time.Time, error) {
	query := fmt.Sprintf(`match
$m isa %s;
fetch {
  "name": $m.%s,
  "applied-at": $m.%s
};`, seqMigrationEntity, seqMigrationNameAttr, seqMigrationTimeAttr)

	results, err := s.db.ExecuteRead(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("seq migration state: query applied: %w", err)
	}

	applied := make(map[string]time.Time)
	for _, row := range results {
		flat := unwrapResult(row)
		name, _ := flat["name"].(string)
		if name == "" {
			continue
		}
		var at time.Time
		switch v := flat["applied-at"].(type) {
		case time.Time:
			at = v
		case string:
			if parsed, parseErr := time.Parse(time.RFC3339, v); parseErr == nil {
				at = parsed
			}
		}
		applied[name] = at
	}
	return applied, nil
}

// Record inserts a new migration record.
func (s *seqMigrationState) Record(ctx context.Context, name string) error {
	now := FormatValue(time.Now().UTC())
	query := fmt.Sprintf(`insert
$m isa %s,
has %s "%s",
has %s %s;`, seqMigrationEntity, seqMigrationNameAttr, escapeTQL(name), seqMigrationTimeAttr, now)

	_, err := s.db.ExecuteWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("seq migration state: record %q: %w", name, err)
	}
	return nil
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
