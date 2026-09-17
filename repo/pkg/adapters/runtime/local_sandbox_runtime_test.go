package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
)

func TestLocalSandboxRuntimeCreatesRunningSessionWithDevProfile(t *testing.T) {
	runtime := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time {
		return time.Unix(2100, 0).UTC()
	}))

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID: "tenant-a",
		Name:     "agent-session",
		Config: ports.SandboxConfig{
			TemplateID:          "sandbox-template-python",
			RuntimeClass:        "sandbox-kata",
			SessionTimeout:      45 * time.Minute,
			NetworkEgressPolicy: ports.SandboxNetworkEgressDenyAll,
		},
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if instance.Kind != ports.WorkloadKindSandbox || instance.State != ports.SandboxStateRunning {
		t.Fatalf("kind/state = %s/%s, want sandbox/running", instance.Kind, instance.State)
	}
	if instance.TemplateID != "sandbox-template-python" || instance.SessionState != "running" {
		t.Fatalf("template/session = %q/%q, want sandbox-template-python/running", instance.TemplateID, instance.SessionState)
	}
	if instance.Config.RuntimeClass != "sandbox-kata" || instance.Config.SessionTimeout != 45*time.Minute || instance.Config.NetworkEgressPolicy != ports.SandboxNetworkEgressDenyAll {
		t.Fatalf("config = %+v, want request config", instance.Config)
	}
	if instance.DevProfile.Mode != "local" || instance.DevProfile.Provider != "local-sandbox-runtime" || instance.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", instance.DevProfile)
	}

	got, err := runtime.Get(context.Background(), ports.SandboxGetRequest{
		TenantID:   "tenant-a",
		InstanceID: instance.InstanceID,
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.InstanceID != instance.InstanceID || got.State != ports.SandboxStateRunning {
		t.Fatalf("got = %+v, want stored running instance", got)
	}
}

func TestLocalSandboxRuntimeDefaultsToKataAndPendingWhenNotAutoStarted(t *testing.T) {
	runtime := NewLocalSandboxRuntime()

	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID: "tenant-a",
		Name:     "agent-session",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if instance.Config.RuntimeClass != "sandbox-kata" {
		t.Fatalf("runtime class = %q, want sandbox-kata", instance.Config.RuntimeClass)
	}
	if instance.Config.NetworkEgressPolicy != ports.SandboxNetworkEgressDenyAll {
		t.Fatalf("egress = %s, want deny_all", instance.Config.NetworkEgressPolicy)
	}
	if instance.Config.SessionTimeout != 30*time.Minute {
		t.Fatalf("timeout = %s, want 30m", instance.Config.SessionTimeout)
	}
	if instance.State != ports.SandboxStatePending {
		t.Fatalf("state = %s, want pending", instance.State)
	}
}

func TestLocalSandboxRuntimeCreateTokenIssuesSignedToken(t *testing.T) {
	now := time.Unix(2_200, 0).UTC()
	key := []byte("local-sandbox-token-test-key")
	runtime := NewLocalSandboxRuntime(
		WithSandboxRuntimeClock(func() time.Time { return now }),
		WithSandboxTokenSigningKey(key),
	)
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "token-session",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	first, err := runtime.CreateToken(context.Background(), ports.SandboxTokenRequest{
		TenantID:       "tenant-a",
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "token-idem-1",
		ExpiresIn:      15 * time.Minute,
		Scopes:         []string{"files", "exec"},
		RequestedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}
	if !strings.HasPrefix(first.Token, sandboxtoken.Prefix) {
		t.Fatalf("token = %q, want signed prefix", first.Token)
	}
	claims, err := sandboxtoken.Parse(first.Token, key, now)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.InstanceID != instance.InstanceID || !sandboxtoken.HasScope(claims, "files") {
		t.Fatalf("claims = %+v", claims)
	}

	second, err := runtime.CreateToken(context.Background(), ports.SandboxTokenRequest{
		TenantID:       "tenant-a",
		InstanceID:     instance.InstanceID,
		IdempotencyKey: "token-idem-1",
		ExpiresIn:      15 * time.Minute,
		Scopes:         []string{"files", "exec"},
		RequestedAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateToken() replay error = %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("idempotent token mismatch")
	}
}

func TestLocalSandboxRuntimeDeleteRemovesSession(t *testing.T) {
	runtime := NewLocalSandboxRuntime()
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "delete-session",
		AutoStart: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	deleted, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:   "tenant-a",
		InstanceID: instance.InstanceID,
		Action:     ports.WorkloadLifecycleDelete,
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(delete) error = %v", err)
	}
	if deleted.State != ports.SandboxStateStopped {
		t.Fatalf("deleted state = %s, want stopped tombstone", deleted.State)
	}
	if _, err := runtime.Get(context.Background(), ports.SandboxGetRequest{
		TenantID:   "tenant-a",
		InstanceID: instance.InstanceID,
	}); err != ports.ErrNotFound {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestLocalSandboxRuntimeInitializesExpirationFieldsOnCreate(t *testing.T) {
	createdAt := time.Unix(2100, 0).UTC()
	runtime := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time {
		return time.Unix(2200, 0).UTC()
	}))
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "expirable",
		AutoStart: true,
		CreatedAt: createdAt,
		Config: ports.SandboxConfig{
			SessionTimeout: 45 * time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantExpiresAt := createdAt.Add(45 * time.Minute)
	if !instance.Config.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("expires_at = %v, want %v", instance.Config.ExpiresAt, wantExpiresAt)
	}
	if !instance.Config.LastActivityAt.Equal(createdAt) {
		t.Fatalf("last_activity_at = %v, want %v", instance.Config.LastActivityAt, createdAt)
	}
}

func TestLocalSandboxRuntimeExtendAdvancesDeadlineAndTouchIdleRefreshesActivity(t *testing.T) {
	now := time.Unix(3000, 0).UTC()
	runtime := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time { return now }))
	instance, err := runtime.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID:  "tenant-a",
		Name:      "renewable",
		AutoStart: true,
		CreatedAt: now,
		Config:    ports.SandboxConfig{SessionTimeout: 30 * time.Minute},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// extend pushes the absolute deadline forward without mutating SessionTimeout.
	extended, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:    "tenant-a",
		InstanceID:  instance.InstanceID,
		Action:      ports.WorkloadLifecycleExtend,
		Duration:    10 * time.Minute,
		RequestedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(extend) error = %v", err)
	}
	wantExpiresAt := now.Add(30 * time.Minute).Add(10 * time.Minute)
	if !extended.Config.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("extended expires_at = %v, want %v", extended.Config.ExpiresAt, wantExpiresAt)
	}
	if extended.Config.SessionTimeout != 30*time.Minute {
		t.Fatalf("extended session_timeout = %s, want baseline 30m", extended.Config.SessionTimeout)
	}

	// touch_idle refreshes the last-activity marker only.
	touched, err := runtime.ApplyLifecycle(context.Background(), ports.SandboxLifecycleRequest{
		TenantID:    "tenant-a",
		InstanceID:  instance.InstanceID,
		Action:      ports.WorkloadLifecycleTouchIdle,
		RequestedAt: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(touch_idle) error = %v", err)
	}
	if !touched.Config.LastActivityAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("touched last_activity_at = %v, want %v", touched.Config.LastActivityAt, now.Add(2*time.Minute))
	}
	if !touched.Config.ExpiresAt.Equal(wantExpiresAt) {
		t.Fatalf("touched expires_at changed = %v, want unchanged %v", touched.Config.ExpiresAt, wantExpiresAt)
	}
}
