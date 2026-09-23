package service

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// TestScheduleTenantInfraProvision_DisabledBySwitch 开关关闭时不启 goroutine。
func TestScheduleTenantInfraProvision_DisabledBySwitch(t *testing.T) {
	enableTenantInfraProvision = false
	t.Cleanup(func() { enableTenantInfraProvision = true })

	tenants := &fakeTenantClient{}
	scheduleTenantInfraProvision(nil, tenants, uuid.New())
	if tenants.provisionCalls != 0 {
		t.Fatalf("provisionCalls=%d want 0 when switch disabled", tenants.provisionCalls)
	}
}

// TestScheduleTenantInfraProvision_SuccessFirstAttempt 首次成功即停：
// goroutine 退出，不写失败审计。
func TestScheduleTenantInfraProvision_SuccessFirstAttempt(t *testing.T) {
	tenantID := uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
	tenants := &fakeTenantClient{}
	audit := &fakeAuditStore{}

	var mu sync.Mutex
	done := make(chan struct{})
	tenants.provisionFn = func(id uuid.UUID) error {
		mu.Lock()
		defer mu.Unlock()
		if id != tenantID {
			t.Errorf("provision tenant id = %s, want %s", id, tenantID)
		}
		close(done)
		return nil
	}
	scheduleTenantInfraProvision(audit, tenants, tenantID)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("provision was not called within 5s")
	}
	// 留出 goroutine 退出时间
	time.Sleep(200 * time.Millisecond)
	if len(audit.logs) != 0 {
		t.Fatalf("no failure audit expected on success, got %+v", audit.logs)
	}
}

// TestScheduleTenantInfraProvision_RetriesThenFails 3 次失败退避 1s/2s/4s：
// 每次失败写 tenant.infra_provision_failed 审计（含 attempt），最终放弃。
func TestScheduleTenantInfraProvision_RetriesThenFails(t *testing.T) {
	tenantID := uuid.MustParse("ffffffff-ffff-4fff-8fff-ffffffffffff")
	tenants := &fakeTenantClient{provisionErr: errors.New("harbor down")}
	audit := &fakeAuditStore{}

	done := make(chan struct{})
	var mu sync.Mutex
	tenants.provisionFn = func(uuid.UUID) error { return errors.New("harbor down") }
	scheduleTenantInfraProvision(audit, tenants, tenantID)

	// 3 次重试（1s+2s+4s 起始退避），留缓冲
	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		logs := len(audit.logs)
		mu.Unlock()
		if logs >= 3 {
			close(done)
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("want 3 failure audits, got %d in 15s: %+v", logs, audit.logs)
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if tenants.provisionCalls != 3 {
		t.Fatalf("provisionCalls=%d want 3", tenants.provisionCalls)
	}
	for i, log := range audit.logs {
		if log.Action != "tenant.infra_provision_failed" || log.Resource != "tenant" {
			t.Fatalf("audit[%d] = %+v", i, log)
		}
		if log.Result != ports.AuditResultFailure {
			t.Fatalf("audit[%d].Result = %q want failure", i, log.Result)
		}
		if log.TenantID == nil || *log.TenantID != tenantID {
			t.Fatalf("audit[%d].TenantID = %v want %s", i, log.TenantID, tenantID)
		}
		if attempt, ok := log.Details["attempt"].(int); !ok || attempt != i+1 {
			t.Fatalf("audit[%d].Details[attempt] = %v", i, log.Details["attempt"])
		}
	}
}

// TestScheduleTenantInfraProvision_NilAuditIsSafe audit 为 nil（未装配）时
// 失败路径不 panic（writeAuditFailure 对 nil audit 静默）。
func TestScheduleTenantInfraProvision_NilAuditIsSafe(t *testing.T) {
	tenantID := uuid.New()
	tenants := &fakeTenantClient{provisionErr: errors.New("k8s unreachable")}
	scheduleTenantInfraProvision(nil, tenants, tenantID)
	// 只要不 panic 即通过；等待首轮调用发生
	time.Sleep(1500 * time.Millisecond)
}
