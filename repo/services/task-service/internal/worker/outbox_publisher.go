package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
	sharedrepo "github.com/kubercloud/ani/pkg/repo"
)

type OutboxPublisherConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

type OutboxPublisher struct {
	db     *pgxpool.Pool
	bus    ports.MessageBus
	repo   sharedrepo.OutboxRepo
	cfg    OutboxPublisherConfig
	logger *slog.Logger
}

func NewOutboxPublisher(
	db *pgxpool.Pool,
	bus ports.MessageBus,
	repo sharedrepo.OutboxRepo,
	cfg OutboxPublisherConfig,
	logger *slog.Logger,
) *OutboxPublisher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	return &OutboxPublisher{
		db:     db,
		bus:    bus,
		repo:   repo,
		cfg:    cfg,
		logger: logger,
	}
}

func (p *OutboxPublisher) Run(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	p.logger.InfoContext(ctx, "outbox publisher started",
		"poll_interval", p.cfg.PollInterval.String(),
		"batch_size", p.cfg.BatchSize,
	)

	for {
		if err := p.publishOnce(ctx); err != nil {
			p.logger.ErrorContext(ctx, "outbox publish failed", "err", err)
		}

		select {
		case <-ctx.Done():
			p.logger.InfoContext(ctx, "outbox publisher stopped")
			return
		case <-ticker.C:
		}
	}
}

// injectEventSeq 把 outbox 行 ID 作为 event_seq 注入 payload（uint64，全局
// 单调递增），供 metering consumer 做同实例事件序判定。payload 解析失败时
// 原样返回（consumer 端 event_seq=0 退化为无序事件，Start/Stop 幂等兜底）。
func injectEventSeq(payload []byte, seq int64, logger *slog.Logger) []byte {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		if logger != nil {
			logger.Warn("outbox publisher: inject event_seq failed, publish raw payload", "err", err)
		}
		return payload
	}
	decoded["event_seq"] = seq
	encoded, err := json.Marshal(decoded)
	if err != nil {
		if logger != nil {
			logger.Warn("outbox publisher: re-encode payload failed, publish raw payload", "err", err)
		}
		return payload
	}
	return encoded
}

func (p *OutboxPublisher) publishOnce(ctx context.Context) error {
	tx, err := sharedrepo.BeginOutboxTx(ctx, p.db)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	events, err := p.repo.FetchUnpublished(ctx, tx, p.cfg.BatchSize)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(events))
	for _, event := range events {
		subject := event.EventType
		payload := event.Payload
		// Workload instance lifecycle events target the metering consumer,
		// which subscribes "ani.events.instance.>" (plan-metering-consumer-v2
		// §Subject 契约：上游发布 ani.events.instance.<instance_id>)。The raw
		// event_type (instance.confirmed/...) is kept as envelope metadata in
		// headers. event_seq (outbox row ID, globally monotonic) is injected so
		// the metering consumer's per-instance seenSeq ordering works.
		if event.AggregateType == "workload_instance" {
			subject = "ani.events.instance." + event.AggregateID.String()
			payload = injectEventSeq(event.Payload, event.ID, p.logger)
		}
		p.logger.Info("outbox event dispatching", "event_id", event.ID, "subject", subject)
		if err := p.bus.Publish(ctx, ports.EventEnvelope{
			TenantID:      event.TenantID.String(),
			AggregateID:   event.AggregateID.String(),
			AggregateType: event.AggregateType,
			EventType:     event.EventType,
			Payload:       payload,
			OccurredAt:    event.CreatedAt,
		}, ports.PublishOptions{
			Subject: subject,
			Key:     strconv.FormatInt(event.ID, 10),
		}); err != nil {
			return err
		}
		ids = append(ids, event.ID)
	}

	if err := p.repo.MarkPublished(ctx, tx, ids); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	p.logger.InfoContext(ctx, "outbox events published", "count", len(ids))
	return nil
}
