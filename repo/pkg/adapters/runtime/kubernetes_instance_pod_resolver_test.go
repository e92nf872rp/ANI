package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestInstancePodNamesResolverPodNames 验证按 instance label 精确解析 pod 名：
// 请求路径与 labelSelector 正确、结果按名称排序输出（保证 matcher 确定性）。
func TestInstancePodNamesResolverPodNames(t *testing.T) {
	var labelSelector string
	resolver, err := NewInstancePodNamesResolver(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/ani-tenant-tenant-a/pods" {
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
			labelSelector = r.URL.Query().Get("labelSelector")
			return jsonResponse(http.StatusOK, `{"items":[
				{"metadata":{"name":"pod-a-x9k2z"}},
				{"metadata":{"name":"pod-a-abc12"}}
			]}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewInstancePodNamesResolver() error = %v", err)
	}

	names, err := resolver.PodNames(context.Background(), "tenant-a", "pod-a")
	if err != nil {
		t.Fatalf("PodNames() error = %v", err)
	}
	if labelSelector != "ani.kubercloud.io/instance=pod-a" {
		t.Fatalf("labelSelector = %q, want instance label selector", labelSelector)
	}
	if len(names) != 2 || names[0] != "pod-a-abc12" || names[1] != "pod-a-x9k2z" {
		t.Fatalf("names = %v, want sorted exact pod names", names)
	}
}

// TestInstancePodNamesResolverPodNamesCache 验证 TTL 缓存：TTL 内重复解析只打一次 K8s API。
func TestInstancePodNamesResolverPodNamesCache(t *testing.T) {
	var calls int
	resolver, err := NewInstancePodNamesResolver(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return jsonResponse(http.StatusOK, `{"items":[{"metadata":{"name":"pod-a-abc12"}}]}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewInstancePodNamesResolver() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := resolver.PodNames(context.Background(), "tenant-a", "pod-a"); err != nil {
			t.Fatalf("PodNames() call %d error = %v", i+1, err)
		}
	}
	if calls != 1 {
		t.Fatalf("K8s API calls = %d, want 1 (cache hit within TTL)", calls)
	}
}

// TestInstancePodNamesResolverMatcher 验证 Matcher 三种语义：
// 精确列表 / 空列表永不匹配占位符 / K8s API 不可用降级前缀正则。
func TestInstancePodNamesResolverMatcher(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "precise pod list",
			status: http.StatusOK,
			body:   `{"items":[{"metadata":{"name":"pod-a-abc12"}}]}`,
			want:   `^(pod-a-abc12)$`,
		},
		{
			name:   "empty list means no pod",
			status: http.StatusOK,
			body:   `{"items":[]}`,
			want:   podNamesNonePlaceholder,
		},
		{
			name:   "api error falls back to prefix regex",
			status: http.StatusInternalServerError,
			body:   `{"message":"boom"}`,
			want:   "^pod-a(-.*)?$",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver, err := NewInstancePodNamesResolver(KubernetesRESTClientConfig{
				Host: "https://kubernetes.example.test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return jsonResponse(tc.status, tc.body), nil
				})},
			})
			if err != nil {
				t.Fatalf("NewInstancePodNamesResolver() error = %v", err)
			}
			got := resolver.Matcher(context.Background(), "tenant-a", "pod-a")
			if got != tc.want {
				t.Fatalf("Matcher() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInstancePodNamesResolverMatcherNoDoubleEscaping 验证精确列表 matcher 对 pod 名
// 中正则元字符的转义不会破坏锚定结构（pod 名正常不含元字符，此为防御性验证）。
func TestInstancePodNamesResolverMatcherNoDoubleEscaping(t *testing.T) {
	resolver, err := NewInstancePodNamesResolver(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"items":[
				{"metadata":{"name":"pod-a-abc12"}},
				{"metadata":{"name":"pod-a-x9k2z"}}
			]}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewInstancePodNamesResolver() error = %v", err)
	}

	got := resolver.Matcher(context.Background(), "tenant-a", "pod-a")
	if !strings.HasPrefix(got, `^(pod-a-abc12|pod-a-x9k2z)$`) {
		t.Fatalf("Matcher() = %q, want anchored alternation", got)
	}
}

// TestInstancePodNamesResolverMatcherExcludesVMHostPods 验证：同一实例名下 sandbox pod 与
// KubeVirt VM 宿主 pod（virt-launcher-*）并存时，Matcher 排除 VM 宿主 pod，避免 cAdvisor
// 聚合把 VM 宿主指标 sum 进业务实例（2026-09-09 实测 Name=test 场景的回归防护）。
func TestInstancePodNamesResolverMatcherExcludesVMHostPods(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "sandbox pod coexists with VM host pod",
			body: `{"items":[
				{"metadata":{"name":"sandbox-test-abc12"}},
				{"metadata":{"name":"virt-launcher-test-tcjlv"}}
			]}`,
			want: `^(sandbox-test-abc12)$`,
		},
		{
			name: "only VM host pod under this instance label",
			body: `{"items":[{"metadata":{"name":"virt-launcher-test-tcjlv"}}]}`,
			// 过滤后无业务 pod：返回永不匹配占位符，绝不降级前缀正则（否则会再次命中宿主 pod）。
			want: podNamesNonePlaceholder,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver, err := NewInstancePodNamesResolver(KubernetesRESTClientConfig{
				Host: "https://kubernetes.example.test",
				HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return jsonResponse(http.StatusOK, tc.body), nil
				})},
			})
			if err != nil {
				t.Fatalf("NewInstancePodNamesResolver() error = %v", err)
			}
			got := resolver.Matcher(context.Background(), "tenant-a", "sandbox-test")
			if got != tc.want {
				t.Fatalf("Matcher() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFilterOutVMHostPods 验证纯过滤函数对 VM 宿主 pod 前缀的剔除。
func TestFilterOutVMHostPods(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{"keeps business pods and drops vm hosts", []string{"virt-launcher-a-abc", "sandbox-a-xyz", "virt-launcher-b-123", "job-a-9kf5"}, []string{"sandbox-a-xyz", "job-a-9kf5"}},
		{"all vm hosts -> empty", []string{"virt-launcher-a-abc"}, nil},
		{"empty input", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterOutVMHostPods(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("filterOutVMHostPods(%v) = %v, want %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("filterOutVMHostPods(%v) = %v, want %v", tc.input, got, tc.want)
				}
			}
		})
	}
}
