package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeGPUPartitionCluster emulates the subset of the Kubernetes REST API the
// GPU partition adapter touches: node list/get/patch, pod list/delete and the
// kube-system volcano-vgpu-node-config ConfigMap.
type fakeGPUPartitionCluster struct {
	nodes        []map[string]interface{}
	configMap    map[string]string
	nodePatches  []string // raw patch bodies per node
	cmPatches    []string // raw patch bodies on the node config CM
	podDeletes   []string // "<namespace>/<name>"
	pluginPods   []map[string]interface{}
	registerGETs map[string]int // per node: how many single-node GETs served
	registerHits map[string]int // slices reported by the register annotation
}

func (f *fakeGPUPartitionCluster) transport(t *testing.T) roundTripFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		t.Helper()
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/api/v1/nodes":
			return jsonResponse(http.StatusOK, fakeJSON(map[string]interface{}{"items": f.nodes})), nil
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/nodes/"):
			name := strings.TrimPrefix(path, "/api/v1/nodes/")
			f.registerGETs[name]++
			node := f.nodeByName(name)
			if f.registerHits[name] > 0 && f.registerGETs[name] > 1 {
				meta, _ := node["metadata"].(map[string]interface{})
				ann, _ := meta["annotations"].(map[string]interface{})
				if ann == nil {
					ann = map[string]interface{}{}
				}
				ann["volcano.sh/node-vgpu-register"] = fmt.Sprintf("GPU-abc,%d,12285,NVIDIA-RTX4090,true,hami-core", f.registerHits[name])
				meta["annotations"] = ann
			}
			return jsonResponse(http.StatusOK, fakeJSON(node)), nil
		case r.Method == http.MethodPatch && strings.HasPrefix(path, "/api/v1/nodes/"):
			f.nodePatches = append(f.nodePatches, string(readBody(r)))
			return jsonResponse(http.StatusOK, `{}`), nil
		case r.Method == http.MethodGet && path == "/api/v1/pods":
			return jsonResponse(http.StatusOK, `{"items":[]}`), nil
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/kube-system/configmaps/volcano-vgpu-node-config":
			return jsonResponse(http.StatusOK, fakeJSON(map[string]interface{}{"data": map[string]interface{}{"config.json": f.configMap["config.json"]}})), nil
		case r.Method == http.MethodPatch && path == "/api/v1/namespaces/kube-system/configmaps/volcano-vgpu-node-config":
			patchBody := readBody(r)
			f.cmPatches = append(f.cmPatches, string(patchBody))
			var patch struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(patchBody, &patch); err != nil {
				t.Fatalf("cm patch unmarshal: %v", err)
			}
			f.configMap = patch.Data
			return jsonResponse(http.StatusOK, `{}`), nil
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/kube-system/pods":
			return jsonResponse(http.StatusOK, fakeJSON(map[string]interface{}{"items": f.pluginPods})), nil
		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/v1/namespaces/kube-system/pods/"):
			f.podDeletes = append(f.podDeletes, strings.TrimPrefix(path, "/api/v1/namespaces/kube-system/pods/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			return nil, nil
		}
	}
}

func (f *fakeGPUPartitionCluster) nodeByName(name string) map[string]interface{} {
	for _, node := range f.nodes {
		if meta, _ := node["metadata"].(map[string]interface{}); meta != nil && meta["name"] == name {
			return node
		}
	}
	return map[string]interface{}{"metadata": map[string]interface{}{"name": name}}
}

func fakeJSON(v interface{}) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return []byte("{}")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// gpuPartitionTestNode returns a wholecard node document matching the real
// lab shape (dev-phys-02): 1 card, nvidia.com/gpu.memory label, ani gpu-spec.
func gpuPartitionTestNode(name string, cardCount int, ready bool, gpuMode string) map[string]interface{} {
	status := "True"
	if !ready {
		status = "False"
	}
	labels := map[string]interface{}{
		"kubernetes.io/hostname":     name,
		"nvidia.com/gpu.product":     "NVIDIA-GeForce-RTX-4090",
		"nvidia.com/gpu.memory":      "49140",
		"ani.kubercloud.io/gpu-spec": "NVIDIA-RTX-4090-49140MiB",
		"ani.kubercloud.io/gpu-mode": gpuMode,
	}
	return map[string]interface{}{
		"metadata": map[string]interface{}{"name": name, "labels": labels},
		"status": map[string]interface{}{
			"capacity":    map[string]string{"nvidia.com/gpu": fmt.Sprintf("%d", cardCount)},
			"allocatable": map[string]string{"nvidia.com/gpu": fmt.Sprintf("%d", cardCount)},
			"conditions":  []map[string]string{{"type": "Ready", "status": status}},
		},
	}
}

func newGPUPartitionTestClient(t *testing.T, cluster *fakeGPUPartitionCluster) *KubernetesGPUInventory {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:        "https://kubernetes.example",
		BearerToken: "token-a",
		HTTPClient:  &http.Client{Transport: cluster.transport(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewKubernetesGPUInventory(client)
}

func TestPlanGPUPartitionClassifiesNodes(t *testing.T) {
	cluster := &fakeGPUPartitionCluster{nodes: []map[string]interface{}{
		gpuPartitionTestNode("gpu-idle", 2, true, "wholecard"),
		gpuPartitionTestNode("gpu-busy", 1, true, "wholecard"),
		gpuPartitionTestNode("gpu-vgpu", 1, true, "vgpu"),
		gpuPartitionTestNode("gpu-dead", 1, false, "wholecard"),
	}}
	// gpu-busy runs one wholecard pod; the busy-check pod list endpoint is
	// served separately from the plugin pod list, so swap the transport for
	// the planning call by pre-seeding via a custom roundTripFunc.
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:        "https://kubernetes.example",
		BearerToken: "token-a",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/pods" {
				return jsonResponse(http.StatusOK, `{"items":[{
					"metadata": {"namespace": "ani-tenant-x", "name": "train-0"},
					"spec": {"nodeName": "gpu-busy", "containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]},
					"status": {"phase": "Running"}
				}]}`), nil
			}
			return cluster.transport(t)(r)
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	inventory := NewKubernetesGPUInventory(client)

	plan, err := inventory.PlanGPUPartition(context.Background(), 4)
	if err != nil {
		t.Fatalf("PlanGPUPartition() error = %v", err)
	}
	if len(plan.EligibleNodes) != 1 || plan.EligibleNodes[0].NodeName != "gpu-idle" {
		t.Fatalf("eligible = %+v, want only gpu-idle", plan.EligibleNodes)
	}
	candidate := plan.EligibleNodes[0]
	if candidate.CardCount != 2 || candidate.TotalMemoryMiB != 49140 || candidate.MBPerShare != 12285 {
		t.Fatalf("candidate = %+v, want 2 cards × 49140MiB → 12285MiB", candidate)
	}
	if candidate.Model != "NVIDIA-RTX-4090" || candidate.SharingSpec != "NVIDIA-RTX-4090-12285MiB" || candidate.SharingPolicy != "quarter" {
		t.Fatalf("candidate naming = %+v, want NVIDIA-RTX-4090-12285MiB quarter", candidate)
	}
	reasons := map[string]string{}
	for _, skipped := range plan.SkippedNodes {
		reasons[skipped.NodeName] = skipped.Reason
	}
	if reasons["gpu-busy"] != ports.GPUPartitionSkipBusy || reasons["gpu-vgpu"] != ports.GPUPartitionSkipVGPU || reasons["gpu-dead"] != ports.GPUPartitionSkipNotReady {
		t.Fatalf("skip reasons = %v, want busy/vgpu/not_ready", reasons)
	}
	if len(plan.SkippedNodes[0].Pods) != 1 || plan.SkippedNodes[0].Pods[0].Name != "train-0" {
		t.Fatalf("busy pods = %+v, want train-0", plan.SkippedNodes)
	}
}

func TestPlanGPUPartitionRejectsUnsupportedShares(t *testing.T) {
	inventory := NewKubernetesGPUInventory(nil)
	if _, err := inventory.PlanGPUPartition(context.Background(), 3); err == nil {
		t.Fatal("shares=3 must be rejected")
	}
}

// TestStripJSONCommentsRealClusterConfig guards the real ani-test2/live
// cluster config.json shape: operators comment out filterdevices UUIDs with
// a leading #, which plain json.Unmarshal rejects.
func TestStripJSONCommentsRealClusterConfig(t *testing.T) {
	raw := "{\n \"nodeconfig\": [{\n  \"name\": \"dev-phys-03\",\n  \"devicesplitcount\": 4,\n" +
		"  \"filterdevices\": {\n   \"uuid\": [\n    #\"GPU-81a7a1b7-3671-2da0-cd94-9ae5e92700da\",\n" +
		"    \"GPU-4e7a0d18-9e71-50ac-2de3-05245d7d89b5\"\n   ],\n   \"index\": []\n  } }] }\n"
	stripped := stripJSONComments(raw)
	var doc gpuNodeConfigDocument
	if err := json.Unmarshal([]byte(stripped), &doc); err != nil {
		t.Fatalf("unmarshal stripped config: %v\nstripped=%s", err, stripped)
	}
	if len(doc.NodeConfig) != 1 || doc.NodeConfig[0].Name != "dev-phys-03" {
		t.Fatalf("doc = %+v, want 1 dev-phys-03 entry", doc.NodeConfig)
	}
	uuids := doc.NodeConfig[0].FilterDevices.UUID
	if len(uuids) != 1 || uuids[0] != "GPU-4e7a0d18-9e71-50ac-2de3-05245d7d89b5" {
		t.Fatalf("uuids = %v, want only the active UUID", uuids)
	}
	// String literals must survive: a "uuid" containing // or # is data.
	withHash := stripJSONComments(`{"a":"GPU-x//y#z", "b": 1} // tail`)
	var probe map[string]any
	if err := json.Unmarshal([]byte(withHash), &probe); err != nil {
		t.Fatalf("unmarshal string-literal case: %v (%s)", err, withHash)
	}
	if probe["a"] != "GPU-x//y#z" || probe["b"] != float64(1) {
		t.Fatalf("probe = %v, string literal mangled", probe)
	}
}

func TestApplyGPUPartitionExecutesAllSteps(t *testing.T) {
	oldInterval, oldTimeout := gpuPartitionRegisterInterval, gpuPartitionRegisterTimeout
	gpuPartitionRegisterInterval = time.Millisecond
	gpuPartitionRegisterTimeout = 200 * time.Millisecond
	defer func() { gpuPartitionRegisterInterval, gpuPartitionRegisterTimeout = oldInterval, oldTimeout }()

	cluster := &fakeGPUPartitionCluster{
		nodes: []map[string]interface{}{gpuPartitionTestNode("gpu-a", 2, true, "wholecard")},
		configMap: map[string]string{"config.json": `{"nodeconfig":[{
			"name": "gpu-existing", "operatingmode": "hami-core", "devicememoryscaling": 1,
			"devicesplitcount": 4, "migstrategy": "none",
			"filterdevices": {"uuid": ["GPU-excluded"], "index": []}}]}`},
		pluginPods:   []map[string]interface{}{{"metadata": map[string]interface{}{"name": "volcano-device-plugin-xyz", "namespace": "kube-system"}}},
		registerGETs: map[string]int{},
		registerHits: map[string]int{"gpu-a": 8},
	}
	inventory := newGPUPartitionTestClient(t, cluster)

	plan, err := inventory.PlanGPUPartition(context.Background(), 4)
	if err != nil || len(plan.EligibleNodes) != 1 {
		t.Fatalf("plan = %+v err=%v, want gpu-idle eligible", plan, err)
	}
	var progressCalls []string
	result, err := inventory.ApplyGPUPartition(context.Background(), plan, func(step, total int, message string) {
		progressCalls = append(progressCalls, fmt.Sprintf("%d/%d %s", step, total, message))
	})
	if err != nil {
		t.Fatalf("ApplyGPUPartition() error = %v", err)
	}
	if len(result.AppliedNodes) != 1 || result.AppliedNodes[0] != "gpu-a" {
		t.Fatalf("applied = %+v failed=%+v skipped=%+v, want [gpu-a]", result.AppliedNodes, result.FailedNodes, result.SkippedNodes)
	}
	if len(result.FailedNodes) != 0 {
		t.Fatalf("failed = %+v, want none", result.FailedNodes)
	}
	// CM patch: existing entry preserved with its filter, new entry added.
	if len(cluster.cmPatches) != 1 {
		t.Fatalf("cm patches = %d, want 1", len(cluster.cmPatches))
	}
	var patchedDoc gpuNodeConfigDocument
	if err := json.Unmarshal([]byte(cluster.configMap["config.json"]), &patchedDoc); err != nil {
		t.Fatalf("patched config.json invalid: %v (cmPatch=%q)", err, cluster.cmPatches[0])
	}
	if len(patchedDoc.NodeConfig) != 2 {
		t.Fatalf("nodeconfig = %+v, want 2 entries", patchedDoc.NodeConfig)
	}
	sawExisting, sawNew := false, false
	for _, entry := range patchedDoc.NodeConfig {
		switch entry.Name {
		case "gpu-existing":
			sawExisting = entry.DeviceSplitCount == 4 && len(entry.FilterDevices.UUID) == 1
		case "gpu-a":
			sawNew = entry.DeviceSplitCount == 4 && entry.OperatingMode == "hami-core"
		}
	}
	if !sawExisting || !sawNew {
		t.Fatalf("nodeconfig entries wrong: existing=%v new=%v (%+v)", sawExisting, sawNew, patchedDoc.NodeConfig)
	}
	// Node label patch: vgpu mode + sharing labels, gpu-spec removed.
	if len(cluster.nodePatches) != 1 {
		t.Fatalf("node patches = %d, want 1", len(cluster.nodePatches))
	}
	patchBody := string(cluster.nodePatches[0])
	for _, want := range []string{`"ani.kubercloud.io/gpu-mode":"vgpu"`, `"ani.kubercloud.io/gpu-sharing-policy":"quarter"`, `"ani.kubercloud.io/gpu-sharing-spec":"NVIDIA-RTX-4090-12285MiB"`, `"ani.kubercloud.io/gpu-spec":null`} {
		if !strings.Contains(patchBody, want) {
			t.Fatalf("node patch missing %s: %s", want, patchBody)
		}
	}
	// Plugin pod restart + registration wait observed.
	if len(cluster.podDeletes) != 1 || cluster.podDeletes[0] != "volcano-device-plugin-xyz" {
		t.Fatalf("pod deletes = %v, want plugin pod", cluster.podDeletes)
	}
	if len(progressCalls) == 0 {
		t.Fatal("progress callback never invoked")
	}
}

func TestApplyGPUPartitionSkipsNewlyBusyNode(t *testing.T) {
	oldInterval, oldTimeout := gpuPartitionRegisterInterval, gpuPartitionRegisterTimeout
	gpuPartitionRegisterInterval = time.Millisecond
	gpuPartitionRegisterTimeout = 50 * time.Millisecond
	defer func() { gpuPartitionRegisterInterval, gpuPartitionRegisterTimeout = oldInterval, oldTimeout }()

	cluster := &fakeGPUPartitionCluster{
		nodes:        []map[string]interface{}{gpuPartitionTestNode("gpu-a", 1, true, "wholecard")},
		configMap:    map[string]string{"config.json": `{"nodeconfig":[]}`},
		registerGETs: map[string]int{},
	}
	// The workload lands between Plan and Apply: the busy-check pod endpoint
	// returns nothing during Plan, then the occupying pod afterwards.
	podListCalls := 0
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:        "https://kubernetes.example",
		BearerToken: "token-a",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/pods" {
				podListCalls++
				if podListCalls == 1 {
					return jsonResponse(http.StatusOK, `{"items":[]}`), nil
				}
				return jsonResponse(http.StatusOK, `{"items":[{
					"metadata": {"namespace": "ani-tenant-y", "name": "sprint-1"},
					"spec": {"nodeName": "gpu-a", "containers": [{"resources": {"limits": {"volcano.sh/vgpu-number": "4"}}}]},
					"status": {"phase": "Pending"}
				}]}`), nil
			}
			return cluster.transport(t)(r)
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	inventory := NewKubernetesGPUInventory(client)

	plan, err := inventory.PlanGPUPartition(context.Background(), 4)
	if err != nil || len(plan.EligibleNodes) != 1 {
		t.Fatalf("precondition failed: plan=%+v err=%v", plan, err)
	}
	result, err := inventory.ApplyGPUPartition(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("ApplyGPUPartition() error = %v", err)
	}
	if len(result.AppliedNodes) != 0 {
		t.Fatalf("applied = %+v, want none", result.AppliedNodes)
	}
	if len(cluster.cmPatches) != 0 || len(cluster.nodePatches) != 0 {
		t.Fatal("busy node must not be mutated")
	}
	if len(result.SkippedNodes) != 1 || result.SkippedNodes[0].Reason != ports.GPUPartitionSkipBusy {
		t.Fatalf("skipped = %+v, want node_busy", result.SkippedNodes)
	}
}

func TestFetchGPUBusyPodsIgnoresTerminatedAndUnbound(t *testing.T) {
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:        "https://kubernetes.example",
		BearerToken: "token-a",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/pods" {
				if !strings.Contains(r.URL.RawQuery, "spec.nodeName%21%3D") && !strings.Contains(r.URL.RawQuery, "spec.nodeName!=") {
					t.Fatalf("busy check must filter bound pods server-side, query = %s", r.URL.RawQuery)
				}
				return jsonResponse(http.StatusOK, `{"items":[
					{"metadata": {"namespace": "ns", "name": "running"}, "spec": {"nodeName": "n1", "containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]}, "status": {"phase": "Running"}},
					{"metadata": {"namespace": "ns", "name": "pullbackoff"}, "spec": {"nodeName": "n1", "containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]}, "status": {"phase": "Pending"}},
					{"metadata": {"namespace": "ns", "name": "terminating"}, "spec": {"nodeName": "n2", "containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]}, "status": {"phase": "Running"}},
					{"metadata": {"namespace": "ns", "name": "succeeded"}, "spec": {"nodeName": "n2", "containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]}, "status": {"phase": "Succeeded"}},
					{"metadata": {"namespace": "ns", "name": "unbound"}, "spec": {"containers": [{"resources": {"requests": {"nvidia.com/gpu": "1"}}}]}, "status": {"phase": "Pending"}},
					{"metadata": {"namespace": "ns", "name": "nogpu"}, "spec": {"nodeName": "n3", "containers": [{"resources": {"requests": {"cpu": "1"}}}]}, "status": {"phase": "Running"}},
					{"metadata": {"namespace": "ns", "name": "initgpu"}, "spec": {"nodeName": "n4", "initContainers": [{"resources": {"limits": {"volcano.sh/vgpu-number": "4"}}}], "containers": [{"resources": {"requests": {"cpu": "1"}}}]}, "status": {"phase": "Running"}}
				]}`), nil
			}
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := (&KubernetesGPUInventory{client: client}).fetchGPUBusyPods(context.Background())
	if err != nil {
		t.Fatalf("fetchGPUBusyPods() error = %v", err)
	}
	if len(busy["n1"]) != 2 || len(busy["n2"]) != 1 || len(busy["n4"]) != 1 {
		t.Fatalf("busy = %+v, want n1=2 n2=1 n4=1", busy)
	}
	if _, exists := busy["n3"]; exists {
		t.Fatalf("nogpu pod must not mark n3 busy: %+v", busy)
	}
	// Terminating pod stays busy; Succeeded and unbound pods are excluded.
	names := map[string]bool{}
	for _, pod := range busy["n2"] {
		names[pod.Name] = true
	}
	if !names["terminating"] || names["succeeded"] {
		t.Fatalf("n2 pods = %v, want only terminating", names)
	}
}
