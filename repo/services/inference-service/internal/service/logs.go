package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

// LogQuery 是产品日志查询。ClusterIP / runtime_ref 会在 redactLogMessage 里抹掉。
type LogQuery struct {
	Limit  int
	Cursor string
	Level  string
}

type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Container string
	Stream    string
}

type LogPage struct {
	Items      []LogEntry
	NextCursor string
}

type serviceLookup interface {
	GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error)
}

type runtimeLogs interface {
	Logs(context.Context, runtime.LogQuery) (runtime.LogPage, error)
}

// LogReader 经 Core GET /platform-workloads/{id}/logs 读容器日志。
type LogReader struct {
	store   serviceLookup
	runtime runtimeLogs
}

func NewLogReader(store serviceLookup, runtime runtimeLogs) *LogReader {
	return &LogReader{store: store, runtime: runtime}
}

// List 返回脱敏后的引擎日志。尚未绑定 runtime 时给空列表。
func (r *LogReader) List(ctx context.Context, tenantID, serviceID uuid.UUID, query LogQuery) (LogPage, error) {
	if tenantID == uuid.Nil || serviceID == uuid.Nil {
		return LogPage{}, ErrInvalidInput
	}
	limit, level, err := normalizeLogQuery(query)
	if err != nil {
		return LogPage{}, err
	}
	resource, err := r.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return LogPage{}, err
	}
	if resource.DeletedAt != nil {
		return LogPage{}, repository.ErrNotFound
	}
	if resource.RuntimeRef == uuid.Nil {
		return LogPage{Items: []LogEntry{}}, nil
	}
	page, err := r.runtime.Logs(ctx, runtime.LogQuery{
		TenantID: tenantID, ServiceID: serviceID, RuntimeRef: resource.RuntimeRef,
		Limit: limit, Cursor: strings.TrimSpace(query.Cursor), Level: level,
	})
	if err != nil {
		if errors.Is(err, runtime.ErrRuntimeNotFound) {
			return LogPage{Items: []LogEntry{}}, nil
		}
		return LogPage{}, err
	}
	items := make([]LogEntry, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, LogEntry{
			Timestamp: item.Timestamp.UTC(),
			Level:     item.Level,
			Message:   redactLogMessage(item.Message, resource),
			Container: item.Container,
			Stream:    item.Stream,
		})
	}
	return LogPage{Items: items, NextCursor: page.NextCursor}, nil
}

func normalizeLogQuery(query LogQuery) (int, string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return 0, "", ErrInvalidInput
	}
	level := strings.ToLower(strings.TrimSpace(query.Level))
	switch level {
	case "", "debug", "info", "warn", "error":
		return limit, level, nil
	default:
		return 0, "", ErrInvalidInput
	}
}

// redactLogMessage 去掉 token、ClusterIP、runtime_ref，避免内部地址泄漏到 Console。
func redactLogMessage(message string, resource domain.Service) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "pre-signed") ||
		strings.Contains(lower, "presigned") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "runtime_ref") ||
		strings.Contains(lower, "runtime_endpoint") ||
		strings.Contains(lower, "internal.svc") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://") {
		return "[redacted]"
	}
	if resource.RuntimeEndpoint != "" && strings.Contains(message, resource.RuntimeEndpoint) {
		return "[redacted]"
	}
	if resource.RuntimeRef != uuid.Nil && strings.Contains(message, resource.RuntimeRef.String()) {
		return "[redacted]"
	}
	return message
}
