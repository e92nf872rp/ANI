package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// KubernetesComponentStatusService 平台组件状态 real adapter：
// 按静态注册表逐个读取 K8s 工作负载对象状态；service 组融合
// PlatformServiceHealthReader 的 scrape_status。任一组件读取失败不阻塞
// 200（该组件降级为 unknown + reason，整体 dev_profile.real_provider=false）。
// 结果带进程内 TTL 缓存（默认 15s），避免组件状态页高轮询打爆 API server。
type KubernetesComponentStatusService struct {
	k8sClient *KubernetesRESTClient
	health    ports.PlatformServiceHealthReader
	ttl       time.Duration
	now       func() time.Time

	mu       sync.Mutex
	cached   *ports.ComponentStatusSnapshot
	cachedAt time.Time
}

const (
	componentStatusProviderName   = "kubernetes-component-status"
	componentStatusDefaultTTL     = 15 * time.Second
	componentStatusDegradedPrefix = "degraded components: "
)

// NewKubernetesComponentStatusService 构造 real adapter。
// health 可为 nil（未启用 Prometheus provider 时 service 组 scrape_status
// 全部为 unknown + reason）；ttl 非正时取默认 15s。
func NewKubernetesComponentStatusService(k8sClient *KubernetesRESTClient, health ports.PlatformServiceHealthReader, ttl time.Duration) *KubernetesComponentStatusService {
	if ttl <= 0 {
		ttl = componentStatusDefaultTTL
	}
	return &KubernetesComponentStatusService{
		k8sClient: k8sClient,
		health:    health,
		ttl:       ttl,
		now:       time.Now,
	}
}

// GetComponentStatus 返回组件状态快照（TTL 缓存内复用上次结果）。
func (s *KubernetesComponentStatusService) GetComponentStatus(ctx context.Context) (ports.ComponentStatusSnapshot, error) {
	if s.k8sClient == nil {
		return ports.ComponentStatusSnapshot{}, fmt.Errorf("%w: kubernetes rest client not configured", ports.ErrComponentStatusUnsupported)
	}
	s.mu.Lock()
	if s.cached != nil && s.now().Sub(s.cachedAt) < s.ttl {
		snapshot := *s.cached
		s.mu.Unlock()
		return snapshot, nil
	}
	s.mu.Unlock()

	snapshot := s.collect(ctx)

	s.mu.Lock()
	s.cached = &snapshot
	s.cachedAt = s.now()
	s.mu.Unlock()
	return snapshot, nil
}

// collect 实际执行注册表逐组件读取与聚合。
func (s *KubernetesComponentStatusService) collect(ctx context.Context) ports.ComponentStatusSnapshot {
	scrapes, scrapeErr := s.readScrapeStatuses(ctx)

	componentsByGroup := make(map[string][]ports.ComponentStatus, len(componentStatusGroups))
	var degraded []string
	for _, entry := range componentStatusRegistry {
		component := s.collectOne(ctx, entry)
		if entry.Group == ports.ComponentGroupService {
			if scrapeErr != nil {
				reason := "scrape reader unavailable: " + scrapeErr.Error()
				component.ScrapeStatus = stringPtr(ports.PlatformServiceScrapeUnknown)
				component.Reason = joinReason(component.Reason, reason)
			} else {
				component.ScrapeStatus = stringPtr(scrapes[entry.ServiceName])
			}
		}
		if component.Status == ports.ComponentStatusUnknown && component.Reason != nil {
			degraded = append(degraded, fmt.Sprintf("%s/%s: %s", entry.Namespace, entry.Name, *component.Reason))
		}
		componentsByGroup[entry.Group] = append(componentsByGroup[entry.Group], component)
	}

	groups := make([]ports.ComponentGroupStatus, 0, len(componentStatusGroups))
	for _, group := range componentStatusGroups {
		groups = append(groups, ports.ComponentGroupStatus{
			Name:       group,
			Components: componentsByGroup[group],
		})
	}

	devProfile := ports.DevProfileInfo{
		Mode:         "real",
		Provider:     componentStatusProviderName,
		RealProvider: true,
	}
	if len(degraded) > 0 {
		devProfile.RealProvider = false
		devProfile.Reason = componentStatusDegradedPrefix + strings.Join(degraded, "; ")
	}

	return ports.ComponentStatusSnapshot{
		ObservedAt: s.now().UTC(),
		Groups:     groups,
		DevProfile: devProfile,
	}
}

// collectOne 读取单个注册表条目对应的工作负载对象。
func (s *KubernetesComponentStatusService) collectOne(ctx context.Context, entry componentRegistration) ports.ComponentStatus {
	component := ports.ComponentStatus{
		Name:      entry.Name,
		Kind:      entry.Kind,
		Namespace: entry.Namespace,
		Group:     entry.Group,
		Status:    ports.ComponentStatusUnknown,
	}
	endpoint := s.k8sClient.Host() + componentStatusObjectPath(entry)
	body, statusCode, err := s.k8sClient.Do(ctx, http.MethodGet, endpoint, "", nil)
	// Do 对非 2xx 返回 (data, statusCode, StatusError)；404 属预期缺失，
	// 优先按 statusCode 识别，再按 err 处理真实读失败。
	if statusCode == http.StatusNotFound {
		component.Reason = stringPtr("workload not found")
		return component
	}
	if err != nil {
		component.Reason = stringPtr(fmt.Sprintf("workload read failed: %v", err))
		return component
	}
	if statusCode != http.StatusOK {
		component.Reason = stringPtr(fmt.Sprintf("workload read status %d", statusCode))
		return component
	}
	var object k8sWorkloadObject
	if err := json.Unmarshal(body, &object); err != nil {
		component.Reason = stringPtr(fmt.Sprintf("workload decode failed: %v", err))
		return component
	}
	return componentStatusFromObject(component, entry.Kind, object)
}

// componentStatusFromObject 把工作负载对象 JSON 归一为组件状态。
func componentStatusFromObject(component ports.ComponentStatus, kind ports.ComponentKind, object k8sWorkloadObject) ports.ComponentStatus {
	var desired, ready int32
	switch kind {
	case ports.ComponentKindDaemonSet:
		desired = object.Status.DesiredNumberScheduled
		ready = object.Status.NumberReady
	default:
		if object.Spec.Replicas != nil {
			desired = *object.Spec.Replicas
		} else {
			desired = 1
		}
		ready = object.Status.ReadyReplicas
	}

	component.DesiredReplicas = int32Ptr(desired)
	component.ReadyReplicas = int32Ptr(ready)
	if version := componentImageTag(object); version != "" {
		component.Version = stringPtr(version)
	}

	if desired == 0 {
		component.Status = ports.ComponentStatusStopped
		return component
	}
	available := true
	for _, condition := range object.Status.Conditions {
		if condition.Type == "Available" {
			available = condition.Status == "True"
			break
		}
	}
	switch {
	case ready >= desired && available:
		component.Status = ports.ComponentStatusRunning
	case ready > 0:
		component.Status = ports.ComponentStatusDegraded
	case available:
		// Available=True 但 ready=0：罕见中间态，按 degraded 上报。
		component.Status = ports.ComponentStatusDegraded
	default:
		component.Status = ports.ComponentStatusDegraded
	}
	return component
}

// readScrapeStatuses 读取七服务 scrape_status，按 canonical 服务名建索引。
func (s *KubernetesComponentStatusService) readScrapeStatuses(ctx context.Context) (map[string]string, error) {
	if s.health == nil {
		return nil, fmt.Errorf("platform service health reader not configured")
	}
	result, err := s.health.ReadPlatformServiceHealth(ctx)
	if err != nil {
		return nil, err
	}
	scrapes := make(map[string]string, len(result.Components))
	for _, component := range result.Components {
		scrapes[component.ServiceName] = component.ScrapeStatus
	}
	return scrapes, nil
}

func componentStatusObjectPath(entry componentRegistration) string {
	switch entry.Kind {
	case ports.ComponentKindStatefulSet:
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/statefulsets/%s", entry.Namespace, entry.Name)
	case ports.ComponentKindDaemonSet:
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/daemonsets/%s", entry.Namespace, entry.Name)
	default:
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", entry.Namespace, entry.Name)
	}
}

// componentImageTag 取首个容器镜像的 tag 段：
// 先切掉 registry host（最后一个 '/' 之前），再取最后一个 ':' 之后段；
// 无 ':'（如裸镜像名）返回空。
func componentImageTag(object k8sWorkloadObject) string {
	if len(object.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	image := object.Spec.Template.Spec.Containers[0].Image
	short := image
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		short = image[idx+1:]
	}
	idx := strings.LastIndex(short, ":")
	if idx < 0 {
		return ""
	}
	return short[idx+1:]
}

// joinReason 合并组件级原因（如 scrape 不可用叠加对象读取失败）。
func joinReason(existing *string, extra string) *string {
	if existing == nil || strings.TrimSpace(*existing) == "" {
		return stringPtr(extra)
	}
	return stringPtr(*existing + "; " + extra)
}

func stringPtr(v string) *string { return &v }

func int32Ptr(v int32) *int32 { return &v }

// k8sWorkloadObject 组件状态读取所需的 K8s 对象字段裁剪。
type k8sWorkloadObject struct {
	Spec struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		Replicas               int32 `json:"replicas"`
		ReadyReplicas          int32 `json:"readyReplicas"`
		DesiredNumberScheduled int32 `json:"desiredNumberScheduled"`
		NumberReady            int32 `json:"numberReady"`
		Conditions             []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}
