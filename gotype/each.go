package gotype

import (
	"context"
	"errors"
	"fmt"
)

// ErrStopIteration stops ForEach without returning an error. A callback may
// return this value directly or wrap it with fmt.Errorf and %w.
var ErrStopIteration = errors.New("gotype: stop iteration")

// ForEach reads matching models and calls fn once per answer row. The callback
// may retain its *T: each row is hydrated into a separate model. Returning
// ErrStopIteration ends the read successfully; other errors stop and return.
// When the transaction supports row streaming, no result slice is retained.
// Otherwise the transaction first materializes all raw rows, but the ORM
// still hydrates and delivers one model at a time.
func (m *Manager[T]) ForEach(ctx context.Context, filters map[string]any, fn func(*T) error) error {
	ctx, span := m.startOp(ctx, "gotype.Manager.ForEach")
	defer span.End(nil)
	if fn == nil {
		return fmt.Errorf("for_each %s: callback must not be nil", m.info.TypeName)
	}
	match, err := m.buildFilteredMatch("e", filters)
	if err != nil {
		return fmt.Errorf("for_each %s: build match: %w", m.info.TypeName, err)
	}
	fetch, err := m.strategy.BuildFetchAll(m.info, "e")
	if err != nil {
		return fmt.Errorf("for_each %s: build fetch: %w", m.info.TypeName, err)
	}
	return m.readEach(ctx, match+"\n"+fetch, fn)
}

// ForEachWithRoles streams models with their role players, like GetWithRoles.
// Nested role-player models are separately hydrated and safe to retain.
func (m *Manager[T]) ForEachWithRoles(ctx context.Context, filters map[string]any, fn func(*T) error) error {
	ctx, span := m.startOp(ctx, "gotype.Manager.ForEachWithRoles")
	defer span.End(nil)
	if fn == nil {
		return fmt.Errorf("for_each_with_roles %s: callback must not be nil", m.info.TypeName)
	}
	match, err := m.buildFilteredMatch("e", filters)
	if err != nil {
		return fmt.Errorf("for_each_with_roles %s: build match: %w", m.info.TypeName, err)
	}
	additions, fetch, err := m.strategy.BuildFetchWithRoles(m.info, "e")
	if err != nil {
		return fmt.Errorf("for_each_with_roles %s: build fetch: %w", m.info.TypeName, err)
	}
	if additions != "" {
		match += "\n" + additions
	}
	return m.readEach(ctx, match+"\n"+fetch, fn)
}

// ForEach runs the fluent query and delivers one owned model per answer row.
// Filters, sorting, offset, and limit match Execute. Returning
// ErrStopIteration from fn ends the read without an error.
func (q *Query[T]) ForEach(ctx context.Context, fn func(*T) error) error {
	ctx, span := q.mgr.startOp(ctx, "gotype.Query.ForEach")
	defer span.End(nil)
	if fn == nil {
		return fmt.Errorf("query %s: callback must not be nil", q.mgr.info.TypeName)
	}
	query, err := q.buildQuery()
	if err != nil {
		return fmt.Errorf("query %s: build: %w", q.mgr.info.TypeName, err)
	}
	if err := q.mgr.readEach(ctx, query, fn); err != nil {
		return fmt.Errorf("query %s: %w", q.mgr.info.TypeName, err)
	}
	return nil
}

func (m *Manager[T]) readEach(ctx context.Context, query string, fn func(*T) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read: context cancelled: %w", err)
	}
	if m.tx != nil {
		return m.eachQueryRows(ctx, m.tx, query, fn)
	}
	tx, err := m.db.openTransaction(ctx, ReadTransaction)
	if err != nil {
		return fmt.Errorf("open read transaction: %w", err)
	}
	defer tx.Close()
	return m.eachQueryRows(ctx, tx, query, fn)
}

func (m *Manager[T]) eachQueryRows(ctx context.Context, tx Tx, query string, fn func(*T) error) error {
	stopped := false
	consume := func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		instance, err := hydrateNewWithInfo[T](m.info, row)
		if err != nil {
			return fmt.Errorf("hydrate %s: %w", m.info.TypeName, err)
		}
		if err := fn(instance); err != nil {
			if errors.Is(err, ErrStopIteration) {
				stopped = true
			}
			return err
		}
		return nil
	}
	if rowTx, ok := tx.(rowQueryTx); ok {
		err := rowTx.QueryEachWithContext(ctx, query, func(_ int, row map[string]any) error {
			return consume(row)
		})
		if stopped && errors.Is(err, ErrStopIteration) {
			return nil
		}
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	rows, err := tx.QueryWithContext(ctx, query)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := consume(row); err != nil {
			if stopped && errors.Is(err, ErrStopIteration) {
				return nil
			}
			return err
		}
	}
	return ctx.Err()
}
