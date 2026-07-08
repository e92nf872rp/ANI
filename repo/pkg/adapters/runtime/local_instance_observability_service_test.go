package runtime

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalInstanceObservabilityReturnsDevProfileData(t *testing.T) {
	service := NewLocalInstanceObservabilityService(WithInstanceObservabilityClock(func() time.Time {
		return time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	}))

	logs, err := service.ListLogs(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "11111111-1111-1111-1111-111111111111",
		Limit:      1,
		Level:      "info",
	})
	if err != nil {
		t.Fatalf("ListLogs error = %v", err)
	}
	if len(logs.Items) != 1 || logs.Total != 1 || logs.Items[0].Level != "info" {
		t.Fatalf("logs = %+v, want one info entry", logs)
	}
	if logs.DevProfile.Mode != "local" || logs.DevProfile.Provider != "local-instance-observability" || logs.DevProfile.RealProvider {
		t.Fatalf("logs dev profile = %+v, want local non-real marker", logs.DevProfile)
	}

	metrics, err := service.GetMetrics(context.Background(), ports.InstanceObservationGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("GetMetrics error = %v", err)
	}
	if metrics.InstanceID == "" || metrics.CPUUtilizationPct == nil || metrics.MemoryUsedMB == nil {
		t.Fatalf("metrics = %+v, want synthetic utilization data", metrics)
	}
	if metrics.DevProfile.Mode != "local" || metrics.DevProfile.RealProvider {
		t.Fatalf("metrics dev profile = %+v, want local non-real marker", metrics.DevProfile)
	}

	security, err := service.ListSecurityEvents(context.Background(), ports.InstanceObservationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "11111111-1111-1111-1111-111111111111",
		Limit:      10,
		Severity:   "warning",
	})
	if err != nil {
		t.Fatalf("ListSecurityEvents error = %v", err)
	}
	if len(security.Items) != 1 || security.Items[0].Severity != "warning" {
		t.Fatalf("security events = %+v, want one warning event", security)
	}

	var streamed []ports.InstanceLogEntry
	err = service.StreamLogs(context.Background(), ports.InstanceLogStreamRequest{
		TenantID:   "tenant-a",
		InstanceID: "11111111-1111-1111-1111-111111111111",
		TailLines:  2,
	}, func(entry ports.InstanceLogEntry) error {
		streamed = append(streamed, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamLogs error = %v", err)
	}
	if len(streamed) != 2 {
		t.Fatalf("streamed logs = %+v, want two entries", streamed)
	}
}

func TestLocalInstanceObservabilityExecSessionIsIdempotentAndShortLived(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 30, 0, 0, time.UTC)
	service := NewLocalInstanceObservabilityService(WithInstanceObservabilityClock(func() time.Time {
		return now
	}))
	req := ports.InstanceExecSessionCreateRequest{
		TenantID:       "tenant-a",
		InstanceID:     "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "exec-once",
		Command:        []string{"/bin/sh"},
		TTY:            true,
		Rows:           24,
	}

	first, err := service.CreateExecSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateExecSession first error = %v", err)
	}
	second, err := service.CreateExecSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateExecSession replay error = %v", err)
	}
	if second.ID != first.ID || second.WSURL != first.WSURL {
		t.Fatalf("idempotent replay = %+v, want same session as %+v", second, first)
	}
	if first.Token == "" {
		t.Fatalf("token is empty, want short-lived websocket token")
	}
	if !strings.Contains(first.WSURL, "token="+url.QueryEscape(first.Token)) {
		t.Fatalf("ws_url = %q, want embedded websocket token", first.WSURL)
	}
	if !first.ExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires_at = %s, want 15 minute TTL", first.ExpiresAt)
	}
	resolved, err := service.GetExecSession(context.Background(), ports.InstanceExecSessionGetRequest{
		TenantID:   req.TenantID,
		InstanceID: req.InstanceID,
		SessionID:  first.ID,
		Token:      first.Token,
	})
	if err != nil {
		t.Fatalf("GetExecSession error = %v", err)
	}
	if resolved.ID != first.ID {
		t.Fatalf("resolved session = %+v, want %+v", resolved, first)
	}
}

func TestLocalInstanceObservabilityExecSessionLookupAllowsTokenOnlyHandshake(t *testing.T) {
	service := NewLocalInstanceObservabilityService()
	req := ports.InstanceExecSessionCreateRequest{
		TenantID:       "tenant-a",
		InstanceID:     "instance-a",
		IdempotencyKey: "exec-token-only",
		Command:        []string{"/bin/sh"},
		TTY:            true,
	}
	session, err := service.CreateExecSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateExecSession error = %v", err)
	}

	resolved, err := service.GetExecSession(context.Background(), ports.InstanceExecSessionGetRequest{
		InstanceID: session.InstanceID,
		SessionID:  session.ID,
		Token:      session.Token,
	})
	if err != nil {
		t.Fatalf("GetExecSession without tenant error = %v", err)
	}
	if resolved.TenantID != req.TenantID || resolved.ID != session.ID {
		t.Fatalf("resolved session = %+v, want tenant %q session %q", resolved, req.TenantID, session.ID)
	}
	if _, err := service.GetExecSession(context.Background(), ports.InstanceExecSessionGetRequest{
		InstanceID: session.InstanceID,
		SessionID:  session.ID,
		Token:      "wrong-token",
	}); err == nil {
		t.Fatalf("GetExecSession with wrong token succeeded, want error")
	}
}

func TestInstanceExecWSURLNormalizesHTTPBaseURL(t *testing.T) {
	got := instanceExecWSURL("http://gateway.example.test/api/v1", "inst-a", "session-a", "token-a")
	if !strings.HasPrefix(got, "ws://gateway.example.test/api/v1/instances/inst-a/exec/session-a?") {
		t.Fatalf("ws_url = %q, want ws scheme", got)
	}
}

func TestInstanceExecWSURLAddsCoreAPIBasePath(t *testing.T) {
	got := instanceExecWSURL("http://gateway.example.test", "inst-a", "session-a", "token-a")
	if !strings.HasPrefix(got, "ws://gateway.example.test/api/v1/instances/inst-a/exec/session-a?") {
		t.Fatalf("ws_url = %q, want /api/v1 base path", got)
	}
}

func TestLocalInstanceObservabilityRejectsInvalidRequests(t *testing.T) {
	service := NewLocalInstanceObservabilityService()
	if _, err := service.ListEvents(context.Background(), ports.InstanceObservationListRequest{TenantID: "tenant-a"}); err == nil {
		t.Fatalf("ListEvents without instance id succeeded, want error")
	}
	if _, err := service.CreateExecSession(context.Background(), ports.InstanceExecSessionCreateRequest{
		TenantID:   "tenant-a",
		InstanceID: "11111111-1111-1111-1111-111111111111",
	}); err == nil {
		t.Fatalf("CreateExecSession without idempotency key succeeded, want error")
	}
}
