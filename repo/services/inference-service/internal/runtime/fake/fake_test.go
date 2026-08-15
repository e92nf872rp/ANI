package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

func TestFakeLogsNewestFirstAndOmitMissingRuntime(t *testing.T) {
	rt := New()
	tenantID := uuid.New()
	serviceID := uuid.New()
	observed, err := rt.Ensure(context.Background(), runtimeport.EnsureRequest{
		TenantID: tenantID, ServiceID: serviceID, Generation: 1, IdempotencyKey: uuid.New(),
		Spec: domain.Spec{Replicas: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := rt.Logs(context.Background(), runtimeport.LogQuery{
		TenantID: tenantID, ServiceID: serviceID, RuntimeRef: observed.RuntimeRef, Limit: 10,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].Message != "runtime accepted" {
		t.Fatalf("page = %+v err=%v", page, err)
	}
	if _, err := rt.Logs(context.Background(), runtimeport.LogQuery{TenantID: tenantID, ServiceID: uuid.New()}); !errors.Is(err, runtimeport.ErrRuntimeNotFound) {
		t.Fatalf("missing runtime err = %v", err)
	}
}
