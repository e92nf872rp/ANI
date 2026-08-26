package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kubercloud/ani/pkg/ports"
)

// OutboxEvent is the payload written into the outbox within the same
// transaction as the business state change (SPEC §3.2). The outbox
// publisher reads outbox_events rows and publishes them to NATS.
type OutboxEvent struct {
	AggregateType string
	AggregateID   string
	EventType     string
	TenantID      string
	Payload       []byte
}

// OutboxWriter is a small interface that lets the reconciler and orchestrator
// emit outbox events inside an externally-owned MetadataTx (SPEC §3.2/§5.1).
// The production implementation reuses the existing outbox_events table via
// OutboxRepo; tests use a mock implementation.
type OutboxWriter interface {
	WriteTx(ctx context.Context, tx ports.MetadataTx, event OutboxEvent) error
}

// metadataOutboxWriter is the production OutboxWriter. It inserts a row into
// the outbox_events table using the caller's MetadataTx so the event is
// committed atomically with the business state change.
type metadataOutboxWriter struct{}

// NewMetadataOutboxWriter builds an OutboxWriter that writes into outbox_events
// using the caller-supplied MetadataTx.
func NewMetadataOutboxWriter() OutboxWriter {
	return metadataOutboxWriter{}
}

func (metadataOutboxWriter) WriteTx(ctx context.Context, tx ports.MetadataTx, event OutboxEvent) error {
	if tx == nil {
		return ports.ErrNotConfigured
	}
	if event.AggregateType == "" || event.AggregateID == "" || event.EventType == "" || event.TenantID == "" {
		return fmt.Errorf("%w: aggregate_type, aggregate_id, event_type and tenant_id are required for outbox event", ports.ErrInvalid)
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, tenant_id, payload)
		VALUES ($1, NULLIF($2, '')::uuid, $3, NULLIF($4, '')::uuid, $5::jsonb)
	`, event.AggregateType, event.AggregateID, event.EventType, event.TenantID, string(payload))
	if err != nil {
		return fmt.Errorf("write outbox event: %w", err)
	}
	return nil
}

// MockOutboxWriter is a test-only OutboxWriter that records events in memory.
type MockOutboxWriter struct {
	events []OutboxEvent
	err    error
}

func (w *MockOutboxWriter) WriteTx(_ context.Context, _ ports.MetadataTx, event OutboxEvent) error {
	if w.err != nil {
		return w.err
	}
	w.events = append(w.events, event)
	return nil
}

// encodeOutboxPayload marshals the payload to JSON for outbox storage.
func encodeOutboxPayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode outbox payload: %w", err)
	}
	return raw, nil
}
