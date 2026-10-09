package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalStatusReconcilerMapsRunningObservation(t *testing.T) {
	request := validReconcileRequest()
	request.Observation.Phase = "Ready"
	request.Observation.Endpoint = "https://app.example.test"
	request.Observation.NodeName = "node-a"
	request.Observation.ObservedAt = time.Unix(300, 0)

	result, err := NewLocalStatusReconciler(WithReconcileClock(func() time.Time {
		return time.Unix(301, 0)
	})).Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("Changed = false, want true")
	}
	if result.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", result.Status.State)
	}
	if result.Status.Endpoint != "https://app.example.test" {
		t.Fatalf("endpoint = %q, want observation endpoint", result.Status.Endpoint)
	}
	if result.Status.NodeName != "node-a" {
		t.Fatalf("node = %q, want node-a", result.Status.NodeName)
	}
}

func TestLocalStatusReconcilerMapsKubeVirtSchedulingObservation(t *testing.T) {
	// KubeVirt reports the VMI phase as "Scheduling" while the virt-launcher pod
	// is still being set up. It must map to provisioning instead of failing the
	// whole reconcile with an unsupported phase.
	request := validReconcileRequest()
	request.Observation.Phase = "Scheduling"

	result, err := NewLocalStatusReconciler().Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile(Scheduling) error = %v", err)
	}
	if result.Status.State != ports.WorkloadStateProvisioning {
		t.Fatalf("state = %s, want provisioning", result.Status.State)
	}
}

func TestLocalStatusReconcilerRejectsMissingAudit(t *testing.T) {
	request := validReconcileRequest()
	request.AuditID = ""
	_, err := NewLocalStatusReconciler().Reconcile(context.Background(), request)
	if err == nil {
		t.Fatalf("Reconcile() error = nil, want missing audit error")
	}
	if !strings.Contains(err.Error(), "audit id") {
		t.Fatalf("error = %q, want audit id", err)
	}
}

func TestLocalStatusReconcilerRejectsMismatchedResourceRefs(t *testing.T) {
	request := validReconcileRequest()
	request.Observation.ResourceRefs = []string{"kubernetes/Deployment/other"}
	_, err := NewLocalStatusReconciler().Reconcile(context.Background(), request)
	if err == nil {
		t.Fatalf("Reconcile() error = nil, want resource ref mismatch")
	}
	if !strings.Contains(err.Error(), "resource refs") {
		t.Fatalf("error = %q, want resource refs", err)
	}
}

func TestLocalStatusReconcilerMapsFailedObservation(t *testing.T) {
	request := validReconcileRequest()
	request.Observation.Phase = "CrashLoopBackOff"
	request.Observation.Reason = "container failed"
	result, err := NewLocalStatusReconciler().Reconcile(context.Background(), request)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.Status.State != ports.WorkloadStateFailed {
		t.Fatalf("state = %s, want failed", result.Status.State)
	}
	if result.Status.Reason != "container failed" {
		t.Fatalf("reason = %q, want provider reason", result.Status.Reason)
	}
}

func validReconcileRequest() ports.WorkloadReconcileRequest {
	ref := "kubernetes/Deployment/app-01"
	return ports.WorkloadReconcileRequest{
		AuditID: "audit-a",
		Current: ports.WorkloadStatus{
			Ref: ports.WorkloadRef{
				TenantID:   "tenant-a",
				InstanceID: "instance-a",
				Kind:       ports.WorkloadKindContainer,
				ProviderID: "planning/container/tenant-a/1",
			},
			State: ports.WorkloadStateProvisioning,
		},
		ApplyResult: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Provider:     "kubernetes",
			Operation:    ports.WorkloadLifecycleCreate,
			ResourceRefs: []string{ref},
		},
		Observation: ports.WorkloadProviderObservation{
			TenantID:     "tenant-a",
			InstanceID:   "instance-a",
			Kind:         ports.WorkloadKindContainer,
			Provider:     "kubernetes",
			ResourceRefs: []string{ref},
			Phase:        "Running",
		},
	}
}
