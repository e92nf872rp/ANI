package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

type MetadataStore struct {
	pool *pgxpool.Pool
}

var _ ports.MetadataStore = (*MetadataStore)(nil)

func NewMetadataStore(pool *pgxpool.Pool) *MetadataStore {
	return &MetadataStore{pool: pool}
}

func (s *MetadataStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *MetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ports.ErrMetadataTenantTxBegin, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, txWrapper{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", ports.ErrMetadataTenantTxCommit, err)
	}
	return nil
}

func (s *MetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ports.ErrMetadataPlatformTxBegin, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, txWrapper{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", ports.ErrMetadataPlatformTxCommit, err)
	}
	return nil
}

type txWrapper struct {
	tx pgx.Tx
}

func (w txWrapper) Exec(ctx context.Context, sql string, args ...any) (ports.CommandTag, error) {
	tag, err := w.tx.Exec(ctx, sql, args...)
	if err != nil {
		return ports.CommandTag{}, err
	}
	return ports.CommandTag{RowsAffected: tag.RowsAffected()}, nil
}

func (w txWrapper) Query(ctx context.Context, sql string, args ...any) (ports.Rows, error) {
	rows, err := w.tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return rowsWrapper{rows: rows}, nil
}

func (w txWrapper) QueryRow(ctx context.Context, sql string, args ...any) ports.Row {
	return w.tx.QueryRow(ctx, sql, args...)
}

type rowsWrapper struct {
	rows pgx.Rows
}

func (w rowsWrapper) Close() {
	w.rows.Close()
}

func (w rowsWrapper) Err() error {
	return w.rows.Err()
}

func (w rowsWrapper) Next() bool {
	return w.rows.Next()
}

func (w rowsWrapper) Scan(dest ...any) error {
	return w.rows.Scan(dest...)
}
