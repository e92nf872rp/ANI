package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	// kube-system namespace hosting the Volcano vGPU device plugin and its
	// node-level ConfigMap (GPU-PARTITION-A).
	kubernetesKubeSystemNamespace = "kube-system"
	// Node-level ConfigMap consumed by the volcano-device-plugin DaemonSet
	// (Projected Volume). data["config.json"] holds a nodeconfig array.
	kubernetesVolcanoVGPUNodeConfigMap = "volcano-vgpu-node-config"
	// DaemonSet match label of the volcano-device-plugin pods.
	kubernetesVolcanoVGPUPluginPodLabel = "name=volcano-device-plugin"
)

// Registration wait defaults: the device plugin re-registers within seconds
// after restart; 120s covers slow device discovery. Vars so tests can
// shorten the wait.
var (
	gpuPartitionRegisterTimeout  = 120 * time.Second
	gpuPartitionRegisterInterval = 2 * time.Second
)

// gpuSpecLabelPattern extracts the model prefix and per-card total MiB from
// an ani.kubercloud.io/gpu-spec label value, e.g.
// "NVIDIA-RTX-4090-49140MiB" → ("NVIDIA-RTX-4090", 49140).
var gpuSpecLabelPattern = regexp.MustCompile(`^(.+)-(\d+)MiB$`)

// gpuNodeConfigEntry mirrors one nodeconfig array element inside the
// volcano-vgpu-node-config ConfigMap data["config.json"].
type gpuNodeConfigEntry struct {
	Name                string                 `json:"name"`
	OperatingMode       string                 `json:"operatingmode"`
	DeviceMemoryScaling float64                `json:"devicememoryscaling"`
	DeviceSplitCount    int                    `json:"devicesplitcount"`
	MIGStrategy         string                 `json:"migstrategy"`
	FilterDevices       gpuNodeConfigFilterDev `json:"filterdevices"`
}

type gpuNodeConfigFilterDev struct {
	UUID  []string `json:"uuid"`
	Index []string `json:"index"`
}

type gpuNodeConfigDocument struct {
	NodeConfig []gpuNodeConfigEntry `json:"nodeconfig"`
}

// stripJSONComments removes #- and //-style line comments from a JSON
// document while preserving string literals. Operators annotate the real
// volcano-vgpu-node-config config.json with comments (e.g. commenting out a
// filterdevices UUID with #); the device plugin tolerates them via a
// JSON5-style reader but plain json.Unmarshal does not. Each comment is
// replaced by a single space so adjacent tokens stay separated.
func stripJSONComments(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	inString, escaped := false, false
	for idx := 0; idx < len(raw); idx++ {
		ch := raw[idx]
		if inString {
			b.WriteByte(ch)
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
			b.WriteByte(ch)
		case '#':
			b.WriteByte(' ')
			for idx+1 < len(raw) && raw[idx+1] != '\n' {
				idx++
			}
		case '/':
			if idx+1 < len(raw) && raw[idx+1] == '/' {
				b.WriteByte(' ')
				for idx+1 < len(raw) && raw[idx+1] != '\n' {
					idx++
				}
			} else {
				b.WriteByte(ch)
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// SharesToPartitionPolicy maps an equal-split count to the
// ani.kubercloud.io/gpu-sharing-policy label value. Supported: 2=half,
// 4=quarter, 8=eighth.
func SharesToPartitionPolicy(shares int) string {
	switch shares {
	case 2:
		return ports.GPUSharingPolicyHalf
	case 4:
		return ports.GPUSharingPolicyQuarter
	case 8:
		return ports.GPUSharingPolicyEighth
	default:
		return ""
	}
}

// PlanGPUPartition computes the cluster partition snapshot (GPU-PARTITION-A):
// idle wholecard nodes become candidates; busy / not-ready / already-vgpu
// nodes are skipped with reasons. shares must be 2, 4 or 8.
func (i *KubernetesGPUInventory) PlanGPUPartition(ctx context.Context, shares int) (ports.GPUPartitionPlan, error) {
	policy := SharesToPartitionPolicy(shares)
	if policy == "" {
		return ports.GPUPartitionPlan{}, fmt.Errorf("%w: shares must be one of 2, 4, 8", ports.ErrInvalid)
	}
	if i.client == nil {
		return ports.GPUPartitionPlan{}, fmt.Errorf("%w: Kubernetes REST client is required for GPU partition", ports.ErrNotConfigured)
	}
	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		return ports.GPUPartitionPlan{}, err
	}
	busy, err := i.fetchGPUBusyPods(ctx)
	if err != nil {
		return ports.GPUPartitionPlan{}, err
	}
	plan := ports.GPUPartitionPlan{Shares: shares}
	for _, node := range nodes {
		if pods, isBusy := busy[node.NodeName]; isBusy {
			plan.SkippedNodes = append(plan.SkippedNodes, ports.GPUPartitionSkippedNode{
				NodeName: node.NodeName, Reason: ports.GPUPartitionSkipBusy, Pods: pods,
			})
			continue
		}
		if !node.Ready {
			plan.SkippedNodes = append(plan.SkippedNodes, ports.GPUPartitionSkippedNode{
				NodeName: node.NodeName, Reason: ports.GPUPartitionSkipNotReady,
			})
			continue
		}
		if node.GPUMode == "vgpu" {
			plan.SkippedNodes = append(plan.SkippedNodes, ports.GPUPartitionSkippedNode{
				NodeName: node.NodeName, Reason: ports.GPUPartitionSkipVGPU,
			})
			continue
		}
		totalMiB, model, ok := deriveWholecardMemoryAndModel(node)
		if !ok {
			plan.SkippedNodes = append(plan.SkippedNodes, ports.GPUPartitionSkippedNode{
				NodeName: node.NodeName, Reason: ports.GPUPartitionSkipNoMemory,
			})
			continue
		}
		plan.EligibleNodes = append(plan.EligibleNodes, ports.GPUPartitionCandidateNode{
			NodeName:       node.NodeName,
			Model:          model,
			CardCount:      gpuAllocatableCount(node, kubernetesNVIDIAGPUResource),
			TotalMemoryMiB: totalMiB,
			MBPerShare:     totalMiB / int64(shares),
			SharingSpec:    fmt.Sprintf("%s-%dMiB", model, totalMiB/int64(shares)),
			SharingPolicy:  policy,
		})
	}
	return plan, nil
}

// deriveWholecardMemoryAndModel resolves the per-card total memory and the
// model prefix used to build the gpu-sharing-spec label. Primary source is
// the nvidia.com/gpu.memory node label (device plugin); the model prefix
// comes from the wholecard gpu-spec label ("NVIDIA-RTX-4090-49140MiB") so the
// derived vGPU spec ("NVIDIA-RTX-4090-12285MiB") matches the operator's
// existing naming. Falls back to nvidia.com/gpu.product for the model.
func deriveWholecardMemoryAndModel(node ports.GPUNodeClass) (int64, string, bool) {
	totalMiB, _ := strconv.ParseInt(strings.TrimSpace(node.Labels[kubernetesNVIDIAGPUMemoryLabel]), 10, 64)
	if totalMiB <= 0 {
		return 0, "", false
	}
	if match := gpuSpecLabelPattern.FindStringSubmatch(strings.TrimSpace(node.GPUSpec)); match != nil {
		if specTotal, err := strconv.ParseInt(match[2], 10, 64); err == nil && specTotal == totalMiB {
			return totalMiB, match[1], true
		}
	}
	model := strings.TrimSpace(node.Model)
	if model == "" || model == "nvidia-gpu" {
		return totalMiB, "", true
	}
	return totalMiB, model, true
}

// fetchGPUBusyPods returns, per node, the pods that hold GPU resources.
// A pod counts as busy when it is bound to a node (spec.nodeName non-empty),
// is not Succeeded/Failed, and any container or init container requests or
// limits more than zero nvidia.com/gpu or volcano.sh/vgpu-number. Pending
// pods that are already bound (e.g. ImagePullBackOff) hold device
// allocations, so phase is deliberately NOT restricted to Running.
func (i *KubernetesGPUInventory) fetchGPUBusyPods(ctx context.Context) (map[string][]ports.GPUPartitionBusyPod, error) {
	endpoint := i.client.Host() + "/api/v1/pods?fieldSelector=" + url.QueryEscape("spec.nodeName!=")
	body, status, err := i.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || status != 200 {
		return nil, fmt.Errorf("fetch pods for busy check: status=%d err=%v", status, err)
	}
	var podList struct {
		Items []struct {
			Metadata struct {
				Namespace         string  `json:"namespace"`
				Name              string  `json:"name"`
				DeletionTimestamp *string `json:"deletionTimestamp"`
			} `json:"metadata"`
			Spec struct {
				NodeName       string                     `json:"nodeName"`
				Containers     []gpuPodContainerResources `json:"containers"`
				InitContainers []gpuPodContainerResources `json:"initContainers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &podList) != nil {
		return nil, fmt.Errorf("unmarshal pod list for busy check")
	}
	busy := make(map[string][]ports.GPUPartitionBusyPod)
	for _, pod := range podList.Items {
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		phase := strings.ToUpper(pod.Status.Phase)
		if phase == "SUCCEEDED" || phase == "FAILED" {
			continue
		}
		if !gpuPodHoldsAnyGPU(pod.Spec.Containers) && !gpuPodHoldsAnyGPU(pod.Spec.InitContainers) {
			continue
		}
		_ = pod.Metadata.DeletionTimestamp // presence is irrelevant once GPU is held; kept for clarity
		busy[nodeName] = append(busy[nodeName], ports.GPUPartitionBusyPod{
			Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name,
		})
	}
	return busy, nil
}

type gpuPodContainerResources struct {
	Resources struct {
		Requests map[string]string `json:"requests"`
		Limits   map[string]string `json:"limits"`
	} `json:"resources"`
}

// gpuPodHoldsAnyGPU reports whether any container requests or limits a
// positive GPU resource count (wholecard or Volcano vGPU slices).
func gpuPodHoldsAnyGPU(containers []gpuPodContainerResources) bool {
	for _, c := range containers {
		for _, values := range []map[string]string{c.Resources.Requests, c.Resources.Limits} {
			for name, value := range values {
				if name != kubernetesNVIDIAGPUResource && name != kubernetesVolcanoVGPUNumberResource {
					continue
				}
				if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
					return true
				}
			}
		}
	}
	return false
}

// ApplyGPUPartition executes the planned split node by node (GPU-PARTITION-A):
//
//  1. one ConfigMap patch upserting a nodeconfig entry (devicesplitcount)
//     per eligible node in kube-system/volcano-vgpu-node-config;
//  2. per node: relabel (gpu-mode=vgpu + gpu-sharing-* , drop gpu-spec),
//     delete the node's volcano-device-plugin pod when present (fresh
//     wholecard nodes gain the pod automatically via the DaemonSet
//     nodeSelector once relabeled), then wait for the
//     volcano.sh/node-vgpu-register annotation to report
//     cardCount×shares slices.
//
// Before mutating, every node is re-checked for busy pods so a workload that
// landed between Plan and Apply is skipped instead of disrupted. Nodes are
// processed sequentially; a node failure does not stop the remaining nodes.
func (i *KubernetesGPUInventory) ApplyGPUPartition(ctx context.Context, plan ports.GPUPartitionPlan, progress ports.GPUPartitionProgressFunc) (ports.GPUPartitionApplyResult, error) {
	if plan.Shares == 0 || len(plan.EligibleNodes) == 0 {
		return ports.GPUPartitionApplyResult{}, fmt.Errorf("%w: partition plan has no eligible nodes", ports.ErrInvalid)
	}
	if i.client == nil {
		return ports.GPUPartitionApplyResult{}, fmt.Errorf("%w: Kubernetes REST client is required for GPU partition", ports.ErrNotConfigured)
	}
	result := ports.GPUPartitionApplyResult{AppliedNodes: []string{}, SkippedNodes: []ports.GPUPartitionSkippedNode{}, FailedNodes: []ports.GPUPartitionNodeResult{}}
	result.SkippedNodes = append(result.SkippedNodes, plan.SkippedNodes...)

	busy, err := i.fetchGPUBusyPods(ctx)
	if err != nil {
		return ports.GPUPartitionApplyResult{}, fmt.Errorf("re-check busy pods before apply: %w", err)
	}
	pending := make([]ports.GPUPartitionCandidateNode, 0, len(plan.EligibleNodes))
	for _, node := range plan.EligibleNodes {
		if pods, isBusy := busy[node.NodeName]; isBusy {
			result.SkippedNodes = append(result.SkippedNodes, ports.GPUPartitionSkippedNode{
				NodeName: node.NodeName, Reason: ports.GPUPartitionSkipBusy, Pods: pods,
			})
			continue
		}
		pending = append(pending, node)
	}
	if len(pending) == 0 {
		return result, nil
	}
	emit := func(step int, message string) {
		if progress != nil {
			progress(step, len(pending), message)
		}
	}

	if err := i.patchGPUNodeConfigMap(ctx, pending, plan.Shares); err != nil {
		return ports.GPUPartitionApplyResult{}, fmt.Errorf("patch %s: %w", kubernetesVolcanoVGPUNodeConfigMap, err)
	}
	emit(0, fmt.Sprintf("node config updated for %d node(s), devicesplitcount=%d", len(pending), plan.Shares))

	for idx, node := range pending {
		if err := i.partitionNodeLabels(ctx, node); err != nil {
			result.FailedNodes = append(result.FailedNodes, ports.GPUPartitionNodeResult{NodeName: node.NodeName, Reason: err.Error()})
			continue
		}
		if err := i.restartGPUPluginPod(ctx, node.NodeName); err != nil {
			result.FailedNodes = append(result.FailedNodes, ports.GPUPartitionNodeResult{NodeName: node.NodeName, Reason: err.Error()})
			continue
		}
		emit(idx+1, fmt.Sprintf("node %s relabeled, waiting for %d vGPU slices", node.NodeName, node.CardCount*plan.Shares))
		if err := i.waitForGPURegistration(ctx, node, plan.Shares); err != nil {
			result.FailedNodes = append(result.FailedNodes, ports.GPUPartitionNodeResult{NodeName: node.NodeName, Reason: err.Error()})
			continue
		}
		result.AppliedNodes = append(result.AppliedNodes, node.NodeName)
		emit(idx+1, fmt.Sprintf("node %s registered %d vGPU slices", node.NodeName, node.CardCount*plan.Shares))
	}
	return result, nil
}

// patchGPUNodeConfigMap upserts one nodeconfig entry per candidate node in
// kube-system/volcano-vgpu-node-config with the requested devicesplitcount.
// Existing entries are preserved; an entry for the same node is replaced.
// Existing filterdevices on a replaced entry are kept so operators' excluded
// GPU lists survive a re-split.
func (i *KubernetesGPUInventory) patchGPUNodeConfigMap(ctx context.Context, nodes []ports.GPUPartitionCandidateNode, shares int) error {
	getEndpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps/%s",
		i.client.Host(), kubernetesKubeSystemNamespace, kubernetesVolcanoVGPUNodeConfigMap)
	body, status, err := i.client.Do(ctx, http.MethodGet, getEndpoint, "", nil)
	if err != nil || status != 200 {
		return fmt.Errorf("get status=%d err=%v", status, err)
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(body, &cm) != nil {
		return fmt.Errorf("unmarshal configmap")
	}
	var doc gpuNodeConfigDocument
	if raw := strings.TrimSpace(cm.Data["config.json"]); raw != "" {
		// Real-cluster config.json may carry # / // comments (the volcano
		// device plugin strips them with a JSON5-style reader); plain
		// json.Unmarshal cannot. Strip before parsing and write back clean.
		if json.Unmarshal([]byte(stripJSONComments(raw)), &doc) != nil {
			return fmt.Errorf("unmarshal existing config.json")
		}
	}
	kept := make([]gpuNodeConfigEntry, 0, len(doc.NodeConfig)+len(nodes))
	byNode := make(map[string]ports.GPUPartitionCandidateNode, len(nodes))
	for _, node := range nodes {
		byNode[node.NodeName] = node
	}
	for _, entry := range doc.NodeConfig {
		if node, replaced := byNode[entry.Name]; replaced {
			// Preserve the operator's filter lists on the replaced entry.
			entry.DeviceSplitCount = shares
			entry.FilterDevices = gpuNodeConfigFilterDev{UUID: entry.FilterDevices.UUID, Index: entry.FilterDevices.Index}
			kept = append(kept, entry)
			delete(byNode, node.NodeName)
			continue
		}
		kept = append(kept, entry)
	}
	for _, node := range nodes {
		if _, stillPending := byNode[node.NodeName]; !stillPending {
			continue
		}
		kept = append(kept, gpuNodeConfigEntry{
			Name:                node.NodeName,
			OperatingMode:       "hami-core",
			DeviceMemoryScaling: 1,
			DeviceSplitCount:    shares,
			MIGStrategy:         "none",
			FilterDevices:       gpuNodeConfigFilterDev{UUID: []string{}, Index: []string{}},
		})
	}
	updated, err := json.MarshalIndent(gpuNodeConfigDocument{NodeConfig: kept}, "", "    ")
	if err != nil {
		return fmt.Errorf("marshal config.json: %w", err)
	}
	// Merge patch under "data" so nested keys merge instead of replacing
	// the whole ConfigMap data map.
	patch, err := json.Marshal(map[string]map[string]string{
		"data": {"config.json": string(updated)},
	})
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	_, err = i.client.do(ctx, http.MethodPatch, getEndpoint, "application/merge-patch+json", patch)
	if err != nil {
		return err
	}
	return nil
}

// partitionNodeLabels relabels a wholecard node to vgpu mode and drops the
// wholecard gpu-spec label (null in a merge patch removes the key).
func (i *KubernetesGPUInventory) partitionNodeLabels(ctx context.Context, node ports.GPUPartitionCandidateNode) error {
	labels := map[string]interface{}{
		kubernetesANIGPUModeLabel:          "vgpu",
		kubernetesANIGPUSharingPolicyLabel: node.SharingPolicy,
		kubernetesANIGPUSharingSpecLabel:   node.SharingSpec,
		kubernetesANIGPUSpecLabel:          nil,
	}
	patch, err := json.Marshal(map[string]interface{}{"metadata": map[string]interface{}{"labels": labels}})
	if err != nil {
		return err
	}
	endpoint := i.client.Host() + "/api/v1/nodes/" + url.PathEscape(node.NodeName)
	_, err = i.client.do(ctx, http.MethodPatch, endpoint, "application/strategic-merge-patch+json", patch)
	return err
}

// restartGPUPluginPod deletes the volcano-device-plugin pod bound to the
// node so the DaemonSet recreates it with the updated node config. Missing
// pods are not an error: a freshly relabeled wholecard node has no plugin pod
// yet and gains one through the DaemonSet nodeSelector.
func (i *KubernetesGPUInventory) restartGPUPluginPod(ctx context.Context, nodeName string) error {
	endpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/pods?labelSelector=%s&fieldSelector=%s",
		i.client.Host(), kubernetesKubeSystemNamespace,
		url.QueryEscape(kubernetesVolcanoVGPUPluginPodLabel),
		url.QueryEscape("spec.nodeName="+nodeName))
	body, status, err := i.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || status != 200 {
		return fmt.Errorf("list device plugin pods: status=%d err=%v", status, err)
	}
	var podList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &podList) != nil {
		return fmt.Errorf("unmarshal device plugin pod list")
	}
	for _, pod := range podList.Items {
		deleteEndpoint := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s",
			i.client.Host(), url.PathEscape(pod.Metadata.Namespace), url.PathEscape(pod.Metadata.Name))
		if _, err := i.client.do(ctx, http.MethodDelete, deleteEndpoint, "", nil); err != nil {
			return fmt.Errorf("delete device plugin pod %s: %w", pod.Metadata.Name, err)
		}
	}
	return nil
}

// waitForGPURegistration polls the node until the
// volcano.sh/node-vgpu-register annotation reports the expected slice count
// (cardCount×shares) or the timeout elapses.
func (i *KubernetesGPUInventory) waitForGPURegistration(ctx context.Context, node ports.GPUPartitionCandidateNode, shares int) error {
	expected := node.CardCount * shares
	deadline := time.Now().Add(gpuPartitionRegisterTimeout)
	endpoint := i.client.Host() + "/api/v1/nodes/" + url.PathEscape(node.NodeName)
	for {
		body, status, err := i.client.Do(ctx, http.MethodGet, endpoint, "", nil)
		if err == nil && status == 200 {
			var nodeDoc struct {
				Metadata struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"metadata"`
			}
			if json.Unmarshal(body, &nodeDoc) == nil {
				if got := parseVolcanoVGPUAnnotation(nodeDoc.Metadata.Annotations); got >= expected {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %d vGPU slices on %s", expected, node.NodeName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gpuPartitionRegisterInterval):
		}
	}
}
