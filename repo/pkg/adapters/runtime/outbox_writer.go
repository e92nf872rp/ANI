package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
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

// encodeInstanceLifecyclePayload builds the metering InstanceLifecycleEvent
// payload (pkg/ports/instance_events.go) from a workload instance record. The
// consumer routes on new_status and extracts GPU count from gpu_spec;
// event_seq is injected at publish time by the outbox publisher.
func encodeInstanceLifecyclePayload(record ports.WorkloadInstanceRecord) ([]byte, error) {
	var gpuSpec *ports.GPUEventSpec
	if record.GPU != nil && record.GPU.Count > 0 {
		gpuSpec = &ports.GPUEventSpec{Count: record.GPU.Count}
	}
	return encodeOutboxPayload(map[string]any{
		"instance_id":   record.InstanceID,
		"tenant_id":     record.TenantID,
		"name":          record.Name,
		"workload_kind": string(record.Kind),
		"new_status":    string(record.Status.State),
		"gpu_spec":      gpuSpec,
	})
}

// writeInstanceOutboxTx emits an instance lifecycle outbox event inside the
// given tenant transaction. Best-effort: invalid aggregate/tenant UUIDs,
// payload encoding failures and write errors are logged and skipped so the
// business state change (quota + status) still commits. No-op when the writer
// is nil (outbox disabled).
func writeInstanceOutboxTx(ctx context.Context, tx ports.MetadataTx, w OutboxWriter, eventType string, record ports.WorkloadInstanceRecord) {
	if w == nil || tx == nil {
		return
	}
	// The outbox_events table casts aggregate_id and tenant_id to UUID.
	// instance_id is "inst_<uuid>" which is NOT a valid UUID, so the INSERT
	// would fail with SQLSTATE 22P02 and abort the entire PostgreSQL
	// transaction. Extract the UUID part from "inst_<uuid>" before writing;
	// skip the outbox write entirely when no valid UUID can be extracted.
	aggregateID := extractUUIDFromInstanceID(record.InstanceID)
	if aggregateID == "" {
		slog.Warn("writeInstanceOutboxTx: instance_id has no valid UUID, skipping outbox",
			"instance_id", record.InstanceID,
			"event_type", eventType,
		)
		return
	}
	if _, err := uuid.Parse(record.TenantID); err != nil {
		slog.Warn("writeInstanceOutboxTx: tenant_id is not a valid UUID, skipping outbox",
			"tenant_id", record.TenantID,
			"event_type", eventType,
		)
		return
	}
	payload, err := encodeInstanceLifecyclePayload(record)
	if err != nil {
		slog.Warn("writeInstanceOutboxTx: encode payload failed, skipping outbox",
			"instance_id", record.InstanceID,
			"event_type", eventType,
			"err", err,
		)
		return
	}
	if err := w.WriteTx(ctx, tx, OutboxEvent{
		AggregateType: "workload_instance",
		AggregateID:   aggregateID,
		EventType:     eventType,
		TenantID:      record.TenantID,
		Payload:       payload,
	}); err != nil {
		slog.Warn("writeInstanceOutboxTx: outbox write failed, skipping (business state still commits)",
			"instance_id", record.InstanceID,
			"event_type", eventType,
			"err", err,
		)
	}
}
