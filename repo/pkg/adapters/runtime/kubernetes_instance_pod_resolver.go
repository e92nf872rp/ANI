package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// InstancePodNamesResolver 按 ANI 渲染层 workload selector label 精确解析实例当前真实 pod 名。
//
// 背景：实例渲染为 Deployment/Job 后 pod 名带控制器 hash 后缀，Prometheus 查询只能用
// pod=~"^name(-.*)?$" 前缀正则近似归属；当两个实例名互为"前缀+连字符"关系（如 sandbox 与
// sandbox-dongjm）时前缀正则会互相命中，造成监控数据静默污染（内存 sum 翻倍、CPU/网络混入
// 其他实例贡献，2026-09-04 测试环境实测）。
//
// 精确性由渲染契约保证：selectorLabels（dryrun_renderer.go）把 ani.kubercloud.io/instance=<Name>
// 写进 workload selector 与 pod template labels，K8s 强制 selector 必须匹配 pod template
// labels，否则 workload 创建被 API 拒绝，因此只要实例渲染成功，pod 必然携带该 label。
// 按 label 精确 list pods 天然无前缀歧义，且对存量实例立即生效（无需改渲染、无需重建）。
//
// 降级语义（调用方无感知）：K8s API 不可用时 Matcher 降级回旧前缀正则，服务保持可用，
// 容忍前缀重叠歧义（回到精确解析之前的行为）；空列表（实例当前无 pod）不是错误，
// 返回永不匹配占位符使查询为空——此时降级前缀正则反而会命中其他实例的 series。
type InstancePodNamesResolver struct {
	client *KubernetesRESTClient
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]podNamesCacheEntry
}

type podNamesCacheEntry struct {
	names     []string
	expiresAt time.Time
}

const (
	// podNamesCacheTTL 是非空解析结果的缓存 TTL：避免高频 metrics/趋势查询打爆 K8s API，
	// 同时保证 pod 变化（重建/扩缩）后 30s 内收敛。
	podNamesCacheTTL = 30 * time.Second
	// podNamesCacheTTLNoMatch 是空结果的缓存 TTL：pod 起来后尽快恢复精确数据。
	podNamesCacheTTLNoMatch = 10 * time.Second
)

// podNamesNonePlaceholder 是精确解析结果为空时的占位正则。pod 名字符集为 [a-z0-9-]，
// 冒号不在其中，故该正则永不匹配真实 pod 名，语义即"该实例当前无 pod，查询应为空"。
const podNamesNonePlaceholder = `^(::ani-no-pod::)$`

// instancePodLabel 是渲染层写入 workload selector 与 pod template labels 的实例归属 label。
const instancePodLabel = "ani.kubercloud.io/instance"

// NewInstancePodNamesResolver 创建精确 pod 名解析器。K8s client 构造失败（如 API host
// 未配置）时返回错误，调用方应置空 resolver 并走前缀正则降级。
func NewInstancePodNamesResolver(config KubernetesRESTClientConfig) (*InstancePodNamesResolver, error) {
	client, err := NewKubernetesRESTClient(config)
	if err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &InstancePodNamesResolver{
		client: client,
		now:    now,
		cache:  map[string]podNamesCacheEntry{},
	}, nil
}

// PodNames 解析实例当前真实 pod 名列表（按 instance label 精确匹配，带缓存）。
// 空列表是合法结果（实例当前无 pod：未调度/已删除），不视为错误。
func (r *InstancePodNamesResolver) PodNames(ctx context.Context, tenantID, instanceName string) ([]string, error) {
	key := tenantNamespace(tenantID) + "|" + instanceName
	r.mu.Lock()
	if entry, ok := r.cache[key]; ok && r.now().Before(entry.expiresAt) {
		names := entry.names
		r.mu.Unlock()
		return names, nil
	}
	r.mu.Unlock()

	// KubernetesRESTClient.Do 的 endpoint 参数是完整 URL（内部不拼 host，见 doOnce），
	// 必须显式带上 API base URL。
	endpoint := r.client.Host() + fmt.Sprintf("/api/v1/namespaces/%s/pods?labelSelector=%s",
		tenantNamespace(tenantID), url.QueryEscape(instancePodLabel+"="+instanceName))
	body, _, err := r.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode pod list: %w", err)
	}
	names := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		if name := strings.TrimSpace(item.Metadata.Name); name != "" {
			names = append(names, name)
		}
	}
	// 排序保证输出确定性：同一实例的 matcher 不随 API 返回顺序变化。
	sort.Strings(names)

	r.mu.Lock()
	ttl := podNamesCacheTTL
	if len(names) == 0 {
		ttl = podNamesCacheTTLNoMatch
	}
	r.cache[key] = podNamesCacheEntry{names: names, expiresAt: r.now().Add(ttl)}
	r.mu.Unlock()
	return names, nil
}

// Matcher 返回该实例的 PromQL pod/cri_name 匹配器：
//   - 解析成功且非空 → ^(pod1|pod2)$ 锚定精确匹配（无前缀歧义）
//   - 解析成功但为空 → 永不匹配占位符（实例当前无 pod，查询应为空）
//   - K8s API 不可用 → 降级回前缀正则 promQLPodMatcher（保持可用，容忍前缀重叠歧义）
//
// 降级发生时打 Warn 日志：静默降级会让"前缀重叠实例互相污染"这一缺陷在无感知中回归，
// 运维需要从日志知晓当前处于降级状态（该日志按失败场景保留）。
func (r *InstancePodNamesResolver) Matcher(ctx context.Context, tenantID, instanceName string) string {
	names, err := r.PodNames(ctx, tenantID, instanceName)
	if err != nil {
		slog.Warn("instance pod precise resolve failed, falling back to prefix regex matcher",
			"namespace", tenantNamespace(tenantID), "instance", instanceName, "err", err)
		return promQLPodMatcher(instanceName)
	}
	if len(names) == 0 {
		return podNamesNonePlaceholder
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}
