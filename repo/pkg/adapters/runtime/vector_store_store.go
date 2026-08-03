package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

type MetadataVectorStoreStore struct {
	store ports.MetadataStore
	now   func() time.Time
}

type VectorStoreStoreOption func(*MetadataVectorStoreStore)

func WithVectorStoreStoreClock(now func() time.Time) VectorStoreStoreOption {
	return func(store *MetadataVectorStoreStore) {
		if now != nil {
			store.now = now
		}
	}
}

func NewMetadataVectorStoreStore(store ports.MetadataStore, options ...VectorStoreStoreOption) *MetadataVectorStoreStore {
	out := &MetadataVectorStoreStore{store: store, now: time.Now}
	for _, option := range options {
		option(out)
	}
	return out
}

func (s *MetadataVectorStoreStore) Upsert(ctx context.Context, record ports.VectorStoreRecord) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.StoreID) == "" || strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("%w: tenant_id, store_id and name are required", ports.ErrInvalid)
	}
	if record.Dimension <= 0 {
		return fmt.Errorf("%w: dimension must be greater than zero", ports.ErrInvalid)
	}
	if record.State == "" {
		return fmt.Errorf("%w: state is required", ports.ErrInvalid)
	}
	createdAt, updatedAt := networkRecordTimes(s.now, record.CreatedAt, record.UpdatedAt)
	var deletedAt any
	if !record.DeletedAt.IsZero() {
		deletedAt = record.DeletedAt.UTC()
	} else if record.State == ports.VectorStoreDeleted {
		deletedAt = updatedAt
	}
	var lastIndexed any
	if !record.LastIndexedAt.IsZero() {
		lastIndexed = record.LastIndexedAt.UTC()
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO vector_stores (
				tenant_id, vector_store_id, name, dimension, metric, embedding_model,
				vector_count, index_status, last_indexed_at, state, reason,
				deleted_at, create_idempotency_key, create_request_fingerprint, created_at, updated_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5, NULLIF($6, ''),
				$7, NULLIF($8, ''), $9, $10, NULLIF($11, ''),
				$12, NULLIF($13, ''), NULLIF($14, ''), $15, $16
			)
			ON CONFLICT (tenant_id, vector_store_id) DO UPDATE SET
				name = EXCLUDED.name,
				dimension = EXCLUDED.dimension,
				metric = EXCLUDED.metric,
				embedding_model = EXCLUDED.embedding_model,
				vector_count = EXCLUDED.vector_count,
				index_status = EXCLUDED.index_status,
				last_indexed_at = EXCLUDED.last_indexed_at,
				state = EXCLUDED.state,
				reason = EXCLUDED.reason,
				deleted_at = EXCLUDED.deleted_at,
				create_idempotency_key = COALESCE(EXCLUDED.create_idempotency_key, vector_stores.create_idempotency_key),
				create_request_fingerprint = COALESCE(EXCLUDED.create_request_fingerprint, vector_stores.create_request_fingerprint),
				updated_at = EXCLUDED.updated_at
		`, record.TenantID, record.StoreID, record.Name, record.Dimension, record.Metric, record.EmbeddingModel,
			nullInt64(record.VectorCount), record.IndexStatus, lastIndexed, string(record.State), record.Reason,
			deletedAt, record.CreateIdempotencyKey, record.CreateRequestFingerprint, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("upsert vector store: %w", err)
		}
		if record.KnowledgeBaseRef.Name != "" || record.KnowledgeBaseRef.ID != "" {
			return upsertVectorStoreKBLink(ctx, tx, record.TenantID, record.StoreID, record.KnowledgeBaseRef)
		}
		return nil
	})
}

func (s *MetadataVectorStoreStore) Get(ctx context.Context, tenantID string, storeID string) (ports.VectorStoreRecord, error) {
	if s.store == nil {
		return ports.VectorStoreRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(storeID) == "" {
		return ports.VectorStoreRecord{}, fmt.Errorf("%w: tenant_id and store_id are required", ports.ErrInvalid)
	}
	var record ports.VectorStoreRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, vectorStoreSelectSQL+`
			WHERE tenant_id = $1::uuid AND vector_store_id = $2 AND deleted_at IS NULL AND state <> 'deleted'
		`, tenantID, storeID)
		if err := scanVectorStore(row, &record); err != nil {
			return err
		}
		ref, err := loadVectorStoreKBLink(ctx, tx, tenantID, storeID)
		if err != nil {
			return err
		}
		record.KnowledgeBaseRef = ref
		return nil
	})
	if err != nil {
		return ports.VectorStoreRecord{}, err
	}
	return record, nil
}

func (s *MetadataVectorStoreStore) List(ctx context.Context, tenantID string) ([]ports.VectorStoreRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	var records []ports.VectorStoreRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, vectorStoreSelectSQL+`
			WHERE tenant_id = $1::uuid AND deleted_at IS NULL AND state <> 'deleted'
			ORDER BY updated_at DESC
		`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.VectorStoreRecord
			if err := scanVectorStore(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Load KB links after the parent cursor is exhausted; pgx forbids nested
		// queries on the same connection ("conn busy").
		rows.Close()
		for i := range records {
			ref, err := loadVectorStoreKBLink(ctx, tx, tenantID, records[i].StoreID)
			if err != nil {
				return err
			}
			records[i].KnowledgeBaseRef = ref
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (s *MetadataVectorStoreStore) FindByCreateIdempotency(ctx context.Context, tenantID string, idempotencyKey string) (ports.VectorStoreRecord, error) {
	if s.store == nil {
		return ports.VectorStoreRecord{}, ports.ErrNotConfigured
	}
	var record ports.VectorStoreRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, vectorStoreSelectSQL+`
			WHERE tenant_id = $1::uuid AND create_idempotency_key = $2
		`, tenantID, idempotencyKey)
		if err := scanVectorStore(row, &record); err != nil {
			return err
		}
		ref, err := loadVectorStoreKBLink(ctx, tx, tenantID, record.StoreID)
		if err != nil {
			return err
		}
		record.KnowledgeBaseRef = ref
		return nil
	})
	if err != nil {
		return ports.VectorStoreRecord{}, err
	}
	return record, nil
}

func (s *MetadataVectorStoreStore) SetKnowledgeBaseLink(ctx context.Context, tenantID string, storeID string, ref ports.VectorStoreKnowledgeBaseRef) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return upsertVectorStoreKBLink(ctx, tx, tenantID, storeID, ref)
	})
}

func (s *MetadataVectorStoreStore) ClearKnowledgeBaseLink(ctx context.Context, tenantID string, storeID string) error {
	if s.store == nil {
		return ports.ErrNotConfigured
	}
	now := s.now().UTC()
	return s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			UPDATE vector_store_knowledge_base_links
			SET deleted_at = $3, updated_at = $3
			WHERE tenant_id = $1::uuid AND vector_store_id = $2 AND deleted_at IS NULL
		`, tenantID, storeID, now)
		if err != nil {
			return fmt.Errorf("clear vector store knowledge base link: %w", err)
		}
		return nil
	})
}

const vectorStoreSelectSQL = `
	SELECT tenant_id::text, vector_store_id, name, dimension, metric, COALESCE(embedding_model, ''),
		COALESCE(vector_count, 0), COALESCE(index_status, ''), last_indexed_at,
		state, COALESCE(reason, ''), created_at, updated_at,
		COALESCE(create_idempotency_key, ''), COALESCE(create_request_fingerprint, '')
	FROM vector_stores
`

func scanVectorStore(row storageScanner, record *ports.VectorStoreRecord) error {
	var state string
	var lastIndexed *time.Time
	err := row.Scan(
		&record.TenantID,
		&record.StoreID,
		&record.Name,
		&record.Dimension,
		&record.Metric,
		&record.EmbeddingModel,
		&record.VectorCount,
		&record.IndexStatus,
		&lastIndexed,
		&state,
		&record.Reason,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.CreateIdempotencyKey,
		&record.CreateRequestFingerprint,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ports.ErrNotFound) || isNoRows(err) {
			return ports.ErrNotFound
		}
		return err
	}
	if lastIndexed != nil {
		record.LastIndexedAt = lastIndexed.UTC()
	}
	record.State = ports.VectorStoreState(state)
	return nil
}

func loadVectorStoreKBLink(ctx context.Context, tx ports.MetadataTx, tenantID string, storeID string) (ports.VectorStoreKnowledgeBaseRef, error) {
	row := tx.QueryRow(ctx, `
		SELECT COALESCE(knowledge_base_id, ''), COALESCE(knowledge_base_name, ''), COALESCE(source, '')
		FROM vector_store_knowledge_base_links
		WHERE tenant_id = $1::uuid AND vector_store_id = $2 AND deleted_at IS NULL
		LIMIT 1
	`, tenantID, storeID)
	var ref ports.VectorStoreKnowledgeBaseRef
	if err := row.Scan(&ref.ID, &ref.Name, &ref.Source); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isNoRows(err) {
			return ports.VectorStoreKnowledgeBaseRef{}, nil
		}
		return ports.VectorStoreKnowledgeBaseRef{}, err
	}
	return ref, nil
}

func upsertVectorStoreKBLink(ctx context.Context, tx ports.MetadataTx, tenantID string, storeID string, ref ports.VectorStoreKnowledgeBaseRef) error {
	now := time.Now().UTC()
	kbID := firstNetworkNonEmpty(strings.TrimSpace(ref.ID), strings.TrimSpace(ref.Name))
	if kbID == "" {
		return fmt.Errorf("%w: knowledge_base_ref id or name is required", ports.ErrInvalid)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE vector_store_knowledge_base_links
		SET deleted_at = $3, updated_at = $3
		WHERE tenant_id = $1::uuid AND vector_store_id = $2 AND deleted_at IS NULL
	`, tenantID, storeID, now); err != nil {
		return fmt.Errorf("retire previous vector store kb links: %w", err)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO vector_store_knowledge_base_links (
			tenant_id, vector_store_id, knowledge_base_id, knowledge_base_name, source, created_at, updated_at, deleted_at
		) VALUES ($1::uuid, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $6, NULL)
		ON CONFLICT (tenant_id, vector_store_id, knowledge_base_id) DO UPDATE SET
			knowledge_base_name = EXCLUDED.knowledge_base_name,
			source = EXCLUDED.source,
			deleted_at = NULL,
			updated_at = EXCLUDED.updated_at
	`, tenantID, storeID, kbID, ref.Name, ref.Source, now)
	if err != nil {
		return fmt.Errorf("upsert vector store kb link: %w", err)
	}
	return nil
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

var _ ports.VectorStoreResourceStore = (*MetadataVectorStoreStore)(nil)
