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
	Fingerprint string `json:"fingerprint,omitempty"`
	StatusCode  int    `json:"status_code,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

// Idempotency replays completed mutating responses for repeated idempotency keys.
func Idempotency(store GatewayStore) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if store == nil || !idempotencyApplies(string(c.Method())) {
			c.Next(ctx)
			return
		}

		key := idempotencyKeyFromRequest(c)
		if key == "" {
			c.Next(ctx)
			return
		}

		// identityKey 取代旧 scope+tenantID：认证请求按身份隔离，公开端点无 identity
		// 时回退到 path 粒度的匿名键，保持既有登录端点 dedupe 行为。
		identityKey, err := RequestIdentityKey(c)
		if err != nil {
			identityKey = "anonymous"
		}
		cacheKey := idempotencyCacheKey(identityKey, string(c.Method()), string(c.Path()), key)
		fingerprint := idempotencyRequestFingerprint(c)
		existing, err := readIdempotencyRecord(ctx, store, cacheKey)
		if err == nil {
			if existing.Fingerprint != "" && existing.Fingerprint != fingerprint {
				respondError(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
				return
			}
			writeIdempotencyRecord(c, existing)
			return
		}
		if !errors.Is(err, ports.ErrNotFound) {
			respondError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE",
				"idempotency store unavailable")
			return
		}
		if isSandboxTokenPath(string(c.Path())) {
			metadata, metadataErr := readIdempotencyRecord(ctx, store, cacheKey+":metadata")
			if metadataErr == nil {
				if metadata.Fingerprint != fingerprint {
					respondError(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
				} else {
					respondError(c, http.StatusConflict, "IdempotencyResultExpired", "idempotency result has expired")
				}
				return
			}
			if !errors.Is(metadataErr, ports.ErrNotFound) {
				respondError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE", "idempotency store unavailable")
				return
			}
		}

		ok, err := store.SetNX(ctx, cacheKey, mustMarshalIdempotencyRecord(idempotencyRecord{State: "processing", Fingerprint: fingerprint}), idempotencyTTL)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE",
				"idempotency store unavailable")
			return
		}
		if !ok {
			existing, err = readIdempotencyRecord(ctx, store, cacheKey)
			if err == nil {
				if existing.Fingerprint != "" && existing.Fingerprint != fingerprint {
					respondError(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used for a different request")
					return
				}
				writeIdempotencyRecord(c, existing)
				return
			}
			respondError(c, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS",
				"idempotent request is already in progress")
			return
		}

		c.Next(ctx)

		completed := idempotencyRecord{
			State:       "completed",
			Fingerprint: fingerprint,
			StatusCode:  c.Response.StatusCode(),
			ContentType: string(c.Response.Header.ContentType()),
			Body:        append([]byte(nil), c.Response.Body()...),
		}
		ttl := idempotencyTTL
		if isSandboxTokenPath(string(c.Path())) && c.Response.StatusCode() >= 200 && c.Response.StatusCode() < 300 {
			if expiresAt, ok := idempotencyResponseExpiry(c.Response.Body()); ok {
				completed.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
				ttl = time.Until(expiresAt)
				metadata := idempotencyRecord{State: "expired", Fingerprint: fingerprint, ExpiresAt: completed.ExpiresAt}
				if err := store.Set(ctx, cacheKey+":metadata", mustMarshalIdempotencyRecord(metadata), idempotencyTTL); err != nil {
					_ = store.Delete(ctx, cacheKey)
					return
				}
			}
		}
		if ttl <= 0 || store.Set(ctx, cacheKey, mustMarshalIdempotencyRecord(completed), ttl) != nil {
			_ = store.Delete(ctx, cacheKey)
		}
	}
}

func idempotencyApplies(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func idempotencyRequestFingerprint(c *app.RequestContext) string {
	body := append([]byte(nil), c.Request.Body()...)
	if len(body) > 0 {
		var value any
		if json.Unmarshal(body, &value) == nil {
			if canonical, err := json.Marshal(value); err == nil {
				body = canonical
			}
		}
	}
	payload := strings.Join([]string{string(c.Method()), string(c.Path()), string(c.URI().QueryString()), string(body)}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func isSandboxTokenPath(path string) bool {
	return strings.HasSuffix(strings.TrimSpace(path), "/sandbox/tokens")
}

func idempotencyResponseExpiry(body []byte) (time.Time, bool) {
	var payload struct {
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.ExpiresAt == "" {
		return time.Time{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, payload.ExpiresAt)
	return expiresAt, err == nil
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

func idempotencyCacheKey(identityKey, method, path, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return "idempotency:" + identityKey + ":" + method + ":" + path + ":" + hex.EncodeToString(digest[:])
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
