// Package gotype provides a sequential, file-based migration runner for TypeDB.
package gotype

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// TQLStatements holds raw TypeQL statements for introspection.
// Populated automatically by TQLMigration; nil for custom Up/Down functions.
type TQLStatements struct {
	Up   []string
	Down []string // nil if no down statements
}

// SequentialMigration represents a single named migration with Up and optional Down functions.
type SequentialMigration struct {
	// Name is the unique identifier, typically prefixed with a timestamp (e.g. "20240101_create_users").
	Name string
	// Up applies the migration.
	Up func(ctx context.Context, db *Database) error
	// Down reverses the migration. May be nil if rollback is not supported.
	Down func(ctx context.Context, db *Database) error
	// Statements is optionally set by TQLMigration for dry-run introspection.
	// nil for migrations with custom Up/Down functions.
	Statements *TQLStatements
}

// SeqMigrationInfo describes the status of a single migration.
type SeqMigrationInfo struct {
	Name      string
	Applied   bool
	AppliedAt string // RFC3339 or empty
}

// SeqMigrationError is returned when a sequential migration fails.
type SeqMigrationError struct {
	Name  string
	Cause error
}

// Error returns the error message.
func (e *SeqMigrationError) Error() string {
	return fmt.Sprintf("seq migration %q: %v", e.Name, e.Cause)
}

// Unwrap returns the underlying cause.
func (e *SeqMigrationError) Unwrap() error {
	return e.Cause
}

// SeqValidationIssue describes a problem found during migration validation.
type SeqValidationIssue struct {
	Name     string
	Message  string
	Severity string // "error" or "warning"
}

// seqMigrationOptions holds configuration for RunSequentialMigrations.
type seqMigrationOptions struct {
	dryRun bool
	target string
	logger func(string)
}

// SeqMigrationOption configures RunSequentialMigrations.
type SeqMigrationOption func(*seqMigrationOptions)

// WithSeqDryRun enables dry-run mode: validates and returns pending migrations without executing.
func WithSeqDryRun() SeqMigrationOption {
	return func(o *seqMigrationOptions) { o.dryRun = true }
}

// WithSeqTarget stops migration after applying the named migration.
func WithSeqTarget(name string) SeqMigrationOption {
	return func(o *seqMigrationOptions) { o.target = name }
}

// WithSeqLogger sets a callback for migration progress messages.
func WithSeqLogger(fn func(string)) SeqMigrationOption {
	return func(o *seqMigrationOptions) { o.logger = fn }
}

// inferTxType determines whether a TypeQL statement should use a schema or write transaction.
func inferTxType(stmt string) string {
	trimmed := strings.TrimSpace(strings.ToLower(stmt))
	for _, prefix := range []string{"define", "undefine", "redefine"} {
		if strings.HasPrefix(trimmed, prefix) {
			return "schema"
		}
	}
	return "write"
}

// TQLMigration creates a SequentialMigration from raw TypeQL statement slices.
// Each statement is routed to ExecuteSchema or ExecuteWrite based on its prefix.
func TQLMigration(name string, up []string, down []string) SequentialMigration {
	m := SequentialMigration{Name: name}

	// Store raw statements for introspection
	if len(up) > 0 || len(down) > 0 {
		stmts := &TQLStatements{}
		if len(up) > 0 {
			stmts.Up = make([]string, len(up))
			copy(stmts.Up, up)
		}
		if len(down) > 0 {
			stmts.Down = make([]string, len(down))
			copy(stmts.Down, down)
		}
		m.Statements = stmts
	}

	if len(up) > 0 {
		upStmts := make([]string, len(up))
		copy(upStmts, up)
		m.Up = func(ctx context.Context, db *Database) error {
			for _, stmt := range upStmts {
				if err := execStmt(ctx, db, stmt); err != nil {
					return err
				}
			}
			return nil
		}
	}

	if len(down) > 0 {
		downStmts := make([]string, len(down))
		copy(downStmts, down)
		m.Down = func(ctx context.Context, db *Database) error {
			for _, stmt := range downStmts {
				if err := execStmt(ctx, db, stmt); err != nil {
					return err
				}
			}
			return nil
		}
	}

	return m
}

// execStmt executes a single TypeQL statement using the appropriate transaction type.
func execStmt(ctx context.Context, db *Database, stmt string) error {
	if inferTxType(stmt) == "schema" {
		return db.ExecuteSchema(ctx, stmt)
	}
	_, err := db.ExecuteWrite(ctx, stmt)
	return err
}

// ValidateSequentialMigrations checks migrations for structural issues without touching the database.
func ValidateSequentialMigrations(migrations []SequentialMigration) []SeqValidationIssue {
	var issues []SeqValidationIssue
	seen := make(map[string]bool)

	for i, m := range migrations {
		if m.Name == "" {
			issues = append(issues, SeqValidationIssue{
				Name:     fmt.Sprintf("[index %d]", i),
				Message:  "migration name is empty",
				Severity: "error",
			})
			continue
		}
		if seen[m.Name] {
			issues = append(issues, SeqValidationIssue{
				Name:     m.Name,
				Message:  "duplicate migration name",
				Severity: "error",
			})
		}
		seen[m.Name] = true
		if m.Up == nil {
			issues = append(issues, SeqValidationIssue{
				Name:     m.Name,
				Message:  "Up function is nil",
				Severity: "error",
			})
		}
	}

	// Check if already sorted
	sorted := sort.SliceIsSorted(migrations, func(i, j int) bool {
		return migrations[i].Name < migrations[j].Name
	})
	if !sorted && len(migrations) > 1 {
		issues = append(issues, SeqValidationIssue{
			Message:  "migrations are not in sorted order; they will be sorted automatically",
			Severity: "warning",
		})
	}

	return issues
}

// hasValidationErrors returns true if any issue has severity "error".
func hasValidationErrors(issues []SeqValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

// RunSequentialMigrations validates, sorts, and applies pending migrations.
// Returns the names of migrations that were applied (or would be applied in dry-run mode).
func RunSequentialMigrations(ctx context.Context, db *Database, migrations []SequentialMigration, opts ...SeqMigrationOption) ([]string, error) {
	cfg := &seqMigrationOptions{}
	for _, opt := range opts {
		opt(cfg)
	}
	logFn := cfg.logger
	if logFn == nil {
		logFn = func(string) {}
	}

	// Validate
	issues := ValidateSequentialMigrations(migrations)
	if hasValidationErrors(issues) {
		return nil, fmt.Errorf("seq migration validation failed: %s", formatIssues(issues))
	}

	// Sort by name
	sorted := make([]SequentialMigration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	// Ensure state schema and query applied
	state := newSeqMigrationState(db)
	if err := state.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("seq migration: ensure state schema: %w", err)
	}

	applied, err := state.Applied(ctx)
	if err != nil {
		return nil, fmt.Errorf("seq migration: query applied: %w", err)
	}

	// Validate checksums of already-applied migrations
	for _, m := range sorted {
		rec, ok := applied[m.Name]
		if !ok {
			continue
		}
		if rec.Checksum != "" {
			current := MigrationChecksum(m)
			if current != "" && current != rec.Checksum {
				return nil, &ChecksumMismatchError{
					Name:     m.Name,
					Expected: rec.Checksum,
					Actual:   current,
				}
			}
		}
	}

	// Determine pending
	var pending []SequentialMigration
	for _, m := range sorted {
		if _, ok := applied[m.Name]; ok {
			continue
		}
		pending = append(pending, m)
		if cfg.target != "" && m.Name == cfg.target {
			break
		}
	}

	if cfg.dryRun {
		names := make([]string, len(pending))
		for i, m := range pending {
			names[i] = m.Name
			logFn(fmt.Sprintf("[dry-run] pending: %s", m.Name))
			if m.Statements != nil {
				for _, s := range m.Statements.Up {
					logFn(fmt.Sprintf("[dry-run]   %s", s))
				}
			}
		}
		return names, nil
	}

	// Apply pending migrations
	var appliedNames []string
	for _, m := range pending {
		logFn(fmt.Sprintf("applying: %s", m.Name))
		if err := m.Up(ctx, db); err != nil {
			return appliedNames, &SeqMigrationError{Name: m.Name, Cause: err}
		}
		checksum := MigrationChecksum(m)
		if err := state.Record(ctx, m.Name, checksum); err != nil {
			return appliedNames, fmt.Errorf("seq migration: record %q: %w", m.Name, err)
		}
		appliedNames = append(appliedNames, m.Name)
		logFn(fmt.Sprintf("applied: %s", m.Name))
	}

	return appliedNames, nil
}

// StampSequentialMigrations marks the specified migrations as applied without
// executing their Up functions. Migrations that are already applied are skipped.
// Returns the names of newly stamped migrations.
//
// This is useful when a database's schema was applied in bulk (e.g., via ExecuteSchema)
// and the migration records need to catch up to reflect the current state.
//
// Supports WithSeqDryRun (report without stamping), WithSeqTarget (stamp up to
// a named migration), and WithSeqLogger (progress callback).
func StampSequentialMigrations(ctx context.Context, db *Database, migrations []SequentialMigration, opts ...SeqMigrationOption) ([]string, error) {
	if len(migrations) == 0 {
		return nil, nil
	}

	cfg := &seqMigrationOptions{}
	for _, opt := range opts {
		opt(cfg)
	}
	logFn := cfg.logger
	if logFn == nil {
		logFn = func(string) {}
	}

	// Sort by name
	sorted := make([]SequentialMigration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	// Ensure state schema and query applied
	state := newSeqMigrationState(db)
	if err := state.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("seq stamp: ensure state schema: %w", err)
	}

	applied, err := state.Applied(ctx)
	if err != nil {
		return nil, fmt.Errorf("seq stamp: query applied: %w", err)
	}

	// Determine pending
	var pending []SequentialMigration
	for _, m := range sorted {
		if _, ok := applied[m.Name]; ok {
			continue
		}
		pending = append(pending, m)
		if cfg.target != "" && m.Name == cfg.target {
			break
		}
	}

	if cfg.dryRun {
		names := make([]string, len(pending))
		for i, m := range pending {
			names[i] = m.Name
			logFn(fmt.Sprintf("[dry-run] stamp: %s", m.Name))
			if m.Statements != nil {
				for _, s := range m.Statements.Up {
					logFn(fmt.Sprintf("[dry-run]   %s", s))
				}
			}
		}
		return names, nil
	}

	var stampedNames []string
	for _, m := range pending {
		checksum := MigrationChecksum(m)
		if err := state.Record(ctx, m.Name, checksum); err != nil {
			return stampedNames, fmt.Errorf("seq stamp: record %q: %w", m.Name, err)
		}
		stampedNames = append(stampedNames, m.Name)
		logFn(fmt.Sprintf("stamped: %s", m.Name))
	}

	return stampedNames, nil
}

// SeqMigrationStatus returns the status of all provided migrations.
func SeqMigrationStatus(ctx context.Context, db *Database, migrations []SequentialMigration) ([]SeqMigrationInfo, error) {
	state := newSeqMigrationState(db)
	if err := state.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("seq migration status: ensure schema: %w", err)
	}

	applied, err := state.Applied(ctx)
	if err != nil {
		return nil, fmt.Errorf("seq migration status: query applied: %w", err)
	}

	sorted := make([]SequentialMigration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	infos := make([]SeqMigrationInfo, len(sorted))
	for i, m := range sorted {
		info := SeqMigrationInfo{Name: m.Name}
		if rec, ok := applied[m.Name]; ok {
			info.Applied = true
			if !rec.AppliedAt.IsZero() {
				info.AppliedAt = rec.AppliedAt.Format("2006-01-02T15:04:05Z")
			}
		}
		infos[i] = info
	}
	return infos, nil
}

// RollbackSequentialMigration rolls back the last N applied migrations in reverse order.
// Returns the names of rolled-back migrations.
func RollbackSequentialMigration(ctx context.Context, db *Database, migrations []SequentialMigration, steps int) ([]string, error) {
	if steps <= 0 {
		return nil, nil
	}

	state := newSeqMigrationState(db)
	if err := state.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("seq rollback: ensure schema: %w", err)
	}

	applied, err := state.Applied(ctx)
	if err != nil {
		return nil, fmt.Errorf("seq rollback: query applied: %w", err)
	}

	// Build lookup of migrations by name
	byName := make(map[string]SequentialMigration, len(migrations))
	for _, m := range migrations {
		byName[m.Name] = m
	}

	// Sort applied names descending (most recent first by name)
	appliedNames := make([]string, 0, len(applied))
	for name := range applied {
		appliedNames = append(appliedNames, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(appliedNames)))

	if steps > len(appliedNames) {
		steps = len(appliedNames)
	}

	var rolledBack []string
	for _, name := range appliedNames[:steps] {
		m, ok := byName[name]
		if !ok {
			return rolledBack, fmt.Errorf("seq rollback: migration %q not found in provided migrations", name)
		}
		if m.Down == nil {
			return rolledBack, fmt.Errorf("seq rollback: migration %q has no Down function", name)
		}
		if err := m.Down(ctx, db); err != nil {
			return rolledBack, &SeqMigrationError{Name: name, Cause: err}
		}
		if err := state.Delete(ctx, name); err != nil {
			return rolledBack, fmt.Errorf("seq rollback: delete record %q: %w", name, err)
		}
		rolledBack = append(rolledBack, name)
	}

	return rolledBack, nil
}

// formatIssues formats validation issues into a single error string.
func formatIssues(issues []SeqValidationIssue) string {
	var parts []string
	for _, issue := range issues {
		if issue.Severity == "error" {
			label := issue.Name
			if label == "" {
				label = "(global)"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", label, issue.Message))
		}
	}
	return strings.Join(parts, "; ")
}
