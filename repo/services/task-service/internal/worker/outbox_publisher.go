package worker

import (
	"context"
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
		p.logger.Info("outbox event dispatching", "event_id", event.ID, "subject", event.EventType)
		if err := p.bus.Publish(ctx, ports.EventEnvelope{
			TenantID:      event.TenantID.String(),
			AggregateID:   event.AggregateID.String(),
			AggregateType: event.AggregateType,
			EventType:     event.EventType,
			Payload:       event.Payload,
			OccurredAt:    event.CreatedAt,
		}, ports.PublishOptions{
			Subject: event.EventType,
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
