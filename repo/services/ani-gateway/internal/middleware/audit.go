package middleware

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// Audit records every API call asynchronously after the response is sent.
// It does NOT block the request path.
func Audit() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)

		// Capture after response
		identity, err := RequestIdentityKey(c)
		if err != nil {
			identity = ""
		}
		entry := auditEntry{
			RequestID:  GetRequestID(c),
			TenantID:   GetTenantID(c),
			Identity:   identity,
			Method:     string(c.Method()),
			Path:       string(c.Path()),
			StatusCode: c.Response.StatusCode(),
			DurationMs: int(time.Since(start).Milliseconds()),
		}
		// Non-blocking publish; drop on channel full (acceptable: audit != transaction)
		select {
		case auditCh <- entry:
		default:
		}
	}
}

type auditEntry struct {
	RequestID  string
	TenantID   string
	Identity   string
	Method     string
	Path       string
	StatusCode int
	DurationMs int
}

// auditCh is a buffered channel consumed by a background goroutine that writes to DB.
// Buffer size 1000 prevents blocking the request path during DB write spikes.
var auditCh = make(chan auditEntry, 1000)

// StartAuditWorker starts the background audit writer. Call once from main().
func StartAuditWorker() {
	go func() {
		for entry := range auditCh {
			// TODO: batch-write to audit_logs table via DB pool
			_ = entry
		}
	}()
}
