package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesInstanceOpsReadsLogs(t *testing.T) {
	var got string
	ops := newTestKubernetesInstanceOps(t, func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.String()
		return jsonResponse(http.StatusOK, "log line"), nil
	})
	result, err := ops.Run(context.Background(), opsRequest(ports.WorkloadInstanceOpsLogs), opsRecord())
	if err != nil {
		t.Fatalf("Run(logs) error = %v", err)
	}
	if result.Output != "log line" {
		t.Fatalf("Output = %q, want log line", result.Output)
	}
	if !strings.Contains(got, "/api/v1/namespaces/ani-tenant-tenant-a/pods/app-01/log") {
		t.Fatalf("request = %q, want pod log path", got)
	}
}

func TestKubernetesInstanceOpsUsesProviderResourceRefName(t *testing.T) {
	var got string
	ops := newTestKubernetesInstanceOps(t, func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.String()
		return jsonResponse(http.StatusOK, "log line"), nil
	})
	record := opsRecord()
	record.Name = "app-01"
	record.ResourceRefs = []string{"kubernetes/Deployment/container-app-01-094ae46b"}

	_, err := ops.Run(context.Background(), opsRequest(ports.WorkloadInstanceOpsLogs), record)
	if err != nil {
		t.Fatalf("Run(logs) error = %v", err)
	}
	if !strings.Contains(got, "/api/v1/namespaces/ani-tenant-tenant-a/pods/container-app-01-094ae46b/log") {
		t.Fatalf("request = %q, want provider resource ref pod log path", got)
	}
	if strings.Contains(got, "/pods/app-01/log") {
		t.Fatalf("request = %q, used display name instead of provider resource ref", got)
	}
}

func TestKubernetesInstanceOpsExecCreatesSession(t *testing.T) {
	var got string
	ops := newTestKubernetesInstanceOps(t, func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.String()
		return jsonResponse(http.StatusOK, "exec accepted"), nil
	})
	req := opsRequest(ports.WorkloadInstanceOpsExec)
	req.Command = []string{"env"}
	result, err := ops.Run(context.Background(), req, opsRecord())
	if err != nil {
		t.Fatalf("Run(exec) error = %v", err)
	}
	if result.SessionID == "" {
		t.Fatalf("SessionID is empty")
	}
	if !strings.Contains(got, "/exec?") || !strings.Contains(got, "command=env") {
		t.Fatalf("request = %q, want exec query", got)
	}
}

func TestKubernetesInstanceOpsVMVNCUsesKubeVirtSubresource(t *testing.T) {
	var got string
	var accept string
	ops := newTestKubernetesInstanceOps(t, func(r *http.Request) (*http.Response, error) {
		got = r.Method + " " + r.URL.String()
		accept = r.Header.Get("Accept")
		return jsonResponse(http.StatusOK, "vnc accepted"), nil
	})
	req := opsRequest(ports.WorkloadInstanceOpsVMVNC)
	result, err := ops.Run(context.Background(), req, opsVMRecord())
	if err != nil {
		t.Fatalf("Run(vm_vnc) error = %v", err)
	}
	if result.Protocol != "vnc" || result.ConnectURL == "" || result.SessionID == "" {
		t.Fatalf("result protocol=%q connect=%q session=%q, want vnc session", result.Protocol, result.ConnectURL, result.SessionID)
	}
	if !strings.Contains(got, "/apis/subresources.kubevirt.io/v1/namespaces/ani-tenant-tenant-a/virtualmachineinstances/vm-01/vnc") {
		t.Fatalf("request = %q, want KubeVirt vnc subresource", got)
	}
	if accept != "*/*" {
		t.Fatalf("Accept = %q, want */*", accept)
	}
}

func TestKubernetesInstanceOpsDisabledDoesNotCallProvider(t *testing.T) {
	called := false
	client := newLifecycleRESTClient(t, func(r *http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(http.StatusOK, "{}"), nil
	})
	ops := NewKubernetesInstanceOps(client)
	result, err := ops.Run(context.Background(), opsRequest(ports.WorkloadInstanceOpsLogs), opsRecord())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Accepted {
		t.Fatalf("Accepted = true, want disabled")
	}
	if called {
		t.Fatalf("provider called while ops disabled")
	}
}

func newTestKubernetesInstanceOps(t *testing.T, roundTrip roundTripFunc) *KubernetesInstanceOps {
	t.Helper()
	return NewKubernetesInstanceOps(
		newLifecycleRESTClient(t, roundTrip),
		WithKubernetesInstanceOpsEnabled(true),
		WithKubernetesInstanceOpsClock(func() time.Time { return time.Unix(1100, 0) }),
	)
}

func opsRecord() ports.WorkloadInstanceRecord {
	return ports.WorkloadInstanceRecord{
		TenantID:   "tenant-a",
		InstanceID: "instance-a",
		Name:       "app-01",
		Kind:       ports.WorkloadKindContainer,
		Status: ports.WorkloadStatus{
			State: ports.WorkloadStateRunning,
		},
	}
}

func opsVMRecord() ports.WorkloadInstanceRecord {
	record := opsRecord()
	record.Name = "vm-01"
	record.Kind = ports.WorkloadKindVM
	return record
}

func opsRequest(action ports.WorkloadInstanceOpsAction) ports.WorkloadInstanceOpsRequest {
	return ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Action:          action,
		UserID:          "user-a",
		PermissionProof: "rbac:ops:workload",
	}
}
