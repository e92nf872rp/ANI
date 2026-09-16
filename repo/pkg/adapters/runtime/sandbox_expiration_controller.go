package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// SandboxExpirationController is the background control-plane loop that fires
// the configured OnTimeout action for expired sandboxes and persists the
// terminal "expired" state (Bug-7). It runs alongside the workload reconcile
// controller inside the gateway.
//
// Scope (MVP): session expiration via config.ExpiresAt and idle expiration via
// config.LastActivityAt + config.IdleTimeout. OnTimeout "pause" scales the
// sandbox workload replicas to 0; OnTimeout "kill" maps to delete. After the
// action fires, the sandbox state is persisted as "expired" so the target
// stops being enumerated on subsequent ticks (idempotent per instance).
type SandboxExpirationController struct {
	lister   ports.ExpirableSandboxLister
	store    ports.SandboxExpirationStatusWriter
	sandbox  ports.SandboxRuntime
	interval time.Duration
	limit    int
	now      func() time.Time
}

type SandboxExpirationControllerOption func(*SandboxExpirationController)

// WithSandboxExpirationClock overrides the time source (for tests).
func WithSandboxExpirationClock(now func() time.Time) SandboxExpirationControllerOption {
	return func(controller *SandboxExpirationController) {
		if now != nil {
			controller.now = now
		}
	}
}

// WithSandboxExpirationInterval overrides the scan cadence. Default 30s.
func WithSandboxExpirationInterval(interval time.Duration) SandboxExpirationControllerOption {
	return func(controller *SandboxExpirationController) {
		if interval > 0 {
			controller.interval = interval
		}
	}
}

// WithSandboxExpirationLimit overrides the per-tick enumeration limit.
func WithSandboxExpirationLimit(limit int) SandboxExpirationControllerOption {
	return func(controller *SandboxExpirationController) {
		if limit > 0 {
			controller.limit = limit
		}
	}
}

// NewSandboxExpirationController builds the expiration scanner. When any of
// lister/store/sandbox is nil the controller reports itself as not ready and
// Start returns immediately (safe to leave unstarted when the provider stack
// is incomplete, e.g. local-only profile without a cross-tenant lister).
func NewSandboxExpirationController(
	lister ports.ExpirableSandboxLister,
	store ports.SandboxExpirationStatusWriter,
	sandbox ports.SandboxRuntime,
	options ...SandboxExpirationControllerOption,
) *SandboxExpirationController {
	controller := &SandboxExpirationController{
		lister:   lister,
		store:    store,
		sandbox:  sandbox,
		interval: 30 * time.Second,
		limit:    100,
		now:      time.Now,
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

func (c *SandboxExpirationController) ready() bool {
	return c.lister != nil && c.store != nil && c.sandbox != nil
}

// Start runs the periodic scan until the context is cancelled.
func (c *SandboxExpirationController) Start(ctx context.Context) error {
	if !c.ready() {
		slog.Warn("sandbox expiration controller not started: missing lister/store/sandbox dependency")
		return nil
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.reconcileExpired(ctx); err != nil {
				slog.Error("sandbox expiration scan failed", "err", err)
			}
		}
	}
}

func (c *SandboxExpirationController) reconcileExpired(ctx context.Context) error {
	records, err := c.lister.ListRunningSandboxes(ctx, c.limit)
	if err != nil {
		slog.Error("sandbox expiration list targets failed", "err", err)
		return err
	}
	now := c.now().UTC()
	for _, record := range records {
		if record.Sandbox == nil {
			continue
		}
		if !sandboxConfigExpired(record.Sandbox.Config, now) {
			continue
		}
		if err := c.handleExpiration(ctx, record, now); err != nil {
			slog.Error("sandbox expiration handling failed",
				"tenant_id", record.TenantID,
				"instance_id", record.InstanceID,
				"err", err,
			)
		}
	}
	return nil
}

// sandboxConfigExpired reports whether now has passed the session deadline or
// the idle deadline. The zero ExpiresAt / LastActivityAt values are treated as
// "not set" so legacy records are never spuriously expired.
func sandboxConfigExpired(config ports.SandboxConfig, now time.Time) bool {
	if !config.ExpiresAt.IsZero() && !now.Before(config.ExpiresAt) {
		return true
	}
	if config.IdleTimeout > 0 && !config.LastActivityAt.IsZero() &&
		!now.Before(config.LastActivityAt.Add(config.IdleTimeout)) {
		return true
	}
	return false
}

func (c *SandboxExpirationController) handleExpiration(ctx context.Context, record ports.WorkloadInstanceRecord, now time.Time) error {
	slog.Info("sandbox expired, firing on_timeout",
		"tenant_id", record.TenantID,
		"instance_id", record.InstanceID,
		"on_timeout", record.Sandbox.Config.OnTimeout,
	)
	action := onTimeoutAction(record.Sandbox.Config.OnTimeout)
	execution, err := SandboxExecutionContextFromRecord(record)
	if err != nil {
		return err
	}
	updated, err := c.sandbox.ApplyLifecycle(ctx, ports.SandboxLifecycleRequest{
		TenantID:    record.TenantID,
		InstanceID:  record.InstanceID,
		Execution:   &execution,
		Action:      action,
		RequestedAt: now,
	})
	if err != nil {
		return err
	}
	// Persist the terminal expired state regardless of whether pause or kill
	// ran, so the record drops out of the running-sandbox enumeration. The write
	// goes through the platform bypass because this background goroutine holds no
	// per-tenant context (tenant-scoped UpsertStatus would panic).
	updated.State = ports.SandboxStateExpired
	updated.SessionState = string(updated.State)
	updated.UpdatedAt = now
	record.Sandbox = &updated
	record.Status.State = workloadStateFromSandboxState(updated.State)
	record.Status.Reason = "SandboxExpired"
	record.Status.UpdatedAt = now
	record.UpdatedAt = now
	if err := c.store.UpsertStatusAsPlatform(ctx, record); err != nil {
		return err
	}
	slog.Info("sandbox marked expired",
		"tenant_id", record.TenantID,
		"instance_id", record.InstanceID,
		"action", action,
	)
	return nil
}

// onTimeoutAction maps the contract on_timeout value to a lifecycle action.
// on_timeout supports [pause, kill]; "kill" maps to delete (WorkloadLifecycleAction
// has no standalone kill), anything else defaults to pause.
func onTimeoutAction(onTimeout string) ports.WorkloadLifecycleAction {
	switch onTimeout {
	case "kill":
		return ports.WorkloadLifecycleDelete
	case "pause":
		return ports.WorkloadLifecyclePause
	default:
		return ports.WorkloadLifecyclePause
	}
}

var _ ports.SandboxExpirationController = (*SandboxExpirationController)(nil)
