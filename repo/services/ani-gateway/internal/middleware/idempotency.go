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
		// Binary ISO uploads must never enter the idempotency path: looking up
		// idempotency_key from the JSON body would call Request.Body() and
		// materialize multi-GiB payloads in Gateway memory (OOMKilled).
		if isBinaryUploadPath(string(c.Path())) {
			c.Next(ctx)
			return
		}

		tenantID := GetTenantID(c)
		key := idempotencyKeyFromRequest(c)
		if tenantID == "" || key == "" {
			c.Next(ctx)
			return
		}

		cacheKey := idempotencyCacheKey(tenantID, string(c.Method()), string(c.Path()), key)
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
	// Only parse small JSON control-plane bodies. Never touch Request.Body()
	// for octet-stream / multipart / unknown large uploads.
	ct := strings.ToLower(string(c.GetHeader("Content-Type")))
	if !strings.Contains(ct, "application/json") {
		return ""
	}
	if n := c.Request.Header.ContentLength(); n < 0 || n > 1<<20 {
		return ""
	}
	var payload struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.Unmarshal(c.Request.Body(), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.IdempotencyKey)
}

func isBinaryUploadPath(path string) bool {
	switch path {
	case "/api/v1/images/upload-proxy", "/api/v1/objects/upload", "/api/v1/branding/logo":
		return true
	default:
		return strings.HasSuffix(path, "/upload-proxy") || strings.HasSuffix(path, "/upload")
	}
}

func idempotencyCacheKey(tenantID, method, path, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return "idempotency:" + tenantID + ":" + method + ":" + path + ":" + hex.EncodeToString(digest[:])
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
