package runtimeadmin

import (
	"context"
	"errors"
	"strings"
)

func sanitizeError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errNotServing):
		return "unavailable"
	case errors.Is(err, context.Canceled):
		return "internal"
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(normalized, "not configured") || strings.Contains(normalized, "not_configured") {
		return "not_configured"
	}
	return "unavailable"
}
