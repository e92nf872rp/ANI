package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	idempotencyReplayHeader = "Idempotent-Replay"
	idempotencyTTL          = 24 * time.Hour
)

type idempotencyRecord struct {
	State       string `json:"state"`
	StatusCode  int    `json:"status_code,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// Idempotency replays completed mutating responses for repeated idempotency keys.
func Idempotency(store GatewayStore) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if store == nil || !idempotencyApplies(string(c.Method())) {
			c.Next(ctx)
			return
		}

		tenantID := GetTenantID(c)
		key := idempotencyKeyFromRequest(c)
		if key == "" {
			c.Next(ctx)
			return
		}

		scope := GetScope(c)
		cacheKey := idempotencyCacheKey(scope, tenantID, string(c.Method()), string(c.Path()), key)
		existing, err := readIdempotencyRecord(ctx, store, cacheKey)
		if err == nil {
			writeIdempotencyRecord(c, existing)
			return
		}
		if !errors.Is(err, ports.ErrNotFound) {
			respondError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE",
				"idempotency store unavailable")
			return
		}

		ok, err := store.SetNX(ctx, cacheKey, mustMarshalIdempotencyRecord(idempotencyRecord{State: "processing"}), idempotencyTTL)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE",
				"idempotency store unavailable")
			return
		}
		if !ok {
			existing, err = readIdempotencyRecord(ctx, store, cacheKey)
			if err == nil {
				writeIdempotencyRecord(c, existing)
				return
			}
			respondError(c, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS",
				"idempotent request is already in progress")
			return
		}

		c.Next(ctx)

		if err := store.Set(ctx, cacheKey, mustMarshalIdempotencyRecord(idempotencyRecord{
			State:       "completed",
			StatusCode:  c.Response.StatusCode(),
			ContentType: string(c.Response.Header.ContentType()),
			Body:        append([]byte(nil), c.Response.Body()...),
		}), idempotencyTTL); err != nil {
			_ = store.Delete(ctx, cacheKey)
		}
	}
}

func idempotencyApplies(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func idempotencyKeyFromRequest(c *app.RequestContext) string {
	if key := strings.TrimSpace(string(c.GetHeader("Idempotency-Key"))); key != "" {
		return key
	}
	var payload struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.Unmarshal(c.Request.Body(), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.IdempotencyKey)
}

func idempotencyCacheKey(scope, tenantID, method, path, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return "idempotency:" + scope + ":" + tenantID + ":" + method + ":" + path + ":" + hex.EncodeToString(digest[:])
}

func readIdempotencyRecord(ctx context.Context, store GatewayStore, key string) (idempotencyRecord, error) {
	raw, err := store.Get(ctx, key)
	if err != nil {
		return idempotencyRecord{}, err
	}
	var record idempotencyRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return idempotencyRecord{}, err
	}
	return record, nil
}

func writeIdempotencyRecord(c *app.RequestContext, record idempotencyRecord) {
	if record.State != "completed" {
		respondError(c, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS",
			"idempotent request is already in progress")
		return
	}
	c.Header(idempotencyReplayHeader, "true")
	c.Data(record.StatusCode, record.ContentType, record.Body)
	c.Abort()
}

func mustMarshalIdempotencyRecord(record idempotencyRecord) []byte {
	raw, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	return raw
}
