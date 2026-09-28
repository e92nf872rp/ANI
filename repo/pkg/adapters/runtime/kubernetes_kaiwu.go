package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	// defaultKaiwuNamespace 是 Console 和 BOSS 共用的生产命名空间。
	defaultKaiwuNamespace = "kaiwu"
	// defaultKaiwuConsoleService / defaultKaiwuConsoleSecret 是 Console 默认资源名。
	defaultKaiwuConsoleService = "kaiwu-console"
	// defaultKaiwuBossService / defaultKaiwuBossSecret 是 BOSS 默认资源名。
	defaultKaiwuBossService   = "kaiwu-boss"
	defaultKaiwuConsoleSecret = "kaiwu-console-web-token"
	defaultKaiwuBossSecret    = "kaiwu-boss-web-token"
	// kaiwuServicePort 是两个开物 Service 必须暴露的 HTTP 端口。
	kaiwuServicePort = 3080
)

// KubernetesKaiwuRuntimeConfig 描述每个开物客户端对应的 Kubernetes
// Service 和 Secret。空字段会回退到生产默认资源名。
type KubernetesKaiwuRuntimeConfig struct {
	Namespace      string // 开物资源所在 namespace。
	ConsoleService string // Console 的 ClusterIP Service 名称。
	ConsoleSecret  string // Console 的 DSH webToken Secret 名称。
	BossService    string // BOSS 的 ClusterIP Service 名称。
	BossSecret     string // BOSS 的 DSH webToken Secret 名称。
}

// KubernetesKaiwuRuntimeReader 基于 Kubernetes REST client 实现 Gateway
// 本地定义的 KaiwuRuntimeReader 契约。结构体只保存不可变资源名；每次
// GetKaiwuRuntime 都重新读取 Service 和 Secret，不缓存 ClusterIP 或 token。
type KubernetesKaiwuRuntimeReader struct {
	client         *KubernetesRESTClient
	namespace      string
	consoleService string
	consoleSecret  string
	bossService    string
	bossSecret     string
}

// NewKubernetesKaiwuRuntimeReader 创建 Kubernetes-backed 开物运行时 reader。
// client 允许为 nil，便于启动装配保持简单；此时后续读取统一返回
// ports.ErrUnavailable，并由 handler fail closed。
func NewKubernetesKaiwuRuntimeReader(client *KubernetesRESTClient, config KubernetesKaiwuRuntimeConfig) *KubernetesKaiwuRuntimeReader {
	namespace := strings.TrimSpace(config.Namespace)
	if namespace == "" {
		namespace = defaultKaiwuNamespace
	}
	consoleService := strings.TrimSpace(config.ConsoleService)
	if consoleService == "" {
		consoleService = defaultKaiwuConsoleService
	}
	consoleSecret := strings.TrimSpace(config.ConsoleSecret)
	if consoleSecret == "" {
		consoleSecret = defaultKaiwuConsoleSecret
	}
	bossService := strings.TrimSpace(config.BossService)
	if bossService == "" {
		bossService = defaultKaiwuBossService
	}
	bossSecret := strings.TrimSpace(config.BossSecret)
	if bossSecret == "" {
		bossSecret = defaultKaiwuBossSecret
	}
	return &KubernetesKaiwuRuntimeReader{
		client:         client,
		namespace:      namespace,
		consoleService: consoleService,
		consoleSecret:  consoleSecret,
		bossService:    bossService,
		bossSecret:     bossSecret,
	}
}

// GetKaiwuRuntime 返回指定开物客户端当前可用的 ClusterIP 目标和 DSH
// webToken。每次调用都会实时读取 Kubernetes Service 和 Secret，因此 Kaiwu
// Pod 重启并更新 Secret 后，本方法会自然取得新 token。
func (r *KubernetesKaiwuRuntimeReader) GetKaiwuRuntime(ctx context.Context, client string) (*url.URL, string, error) {
	if r == nil || r.client == nil {
		return nil, "", fmt.Errorf("%w: Kubernetes client is not configured", ports.ErrUnavailable)
	}

	serviceName, secretName, ok := r.kaiwuResourceNames(client)
	if !ok {
		return nil, "", fmt.Errorf("%w: unsupported Kaiwu client %q", ports.ErrInvalid, client)
	}

	target, err := r.readKaiwuService(ctx, serviceName)
	if err != nil {
		return nil, "", err
	}
	webToken, err := r.readKaiwuWebToken(ctx, secretName)
	if err != nil {
		return nil, "", err
	}
	return target, webToken, nil
}

// readKaiwuService 读取并校验一个开物 Service。只接受 ClusterIP 类型且
// 暴露 HTTP 3080 的 Service，并返回形如 http://<ClusterIP>:3080 的内部地址。
func (r *KubernetesKaiwuRuntimeReader) readKaiwuService(ctx context.Context, serviceName string) (*url.URL, error) {
	endpoint := r.client.Host() + "/api/v1/namespaces/" + url.PathEscape(r.namespace) + "/services/" + url.PathEscape(serviceName)
	body, status, err := r.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || status != http.StatusOK {
		return nil, fmt.Errorf("%w: read Kaiwu service %s/%s failed: status=%d err=%v", ports.ErrUnavailable, r.namespace, serviceName, status, err)
	}

	var document struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
			Type      string `json:"type"`
			Ports     []struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("%w: invalid Kaiwu service %s/%s", ports.ErrUnavailable, r.namespace, serviceName)
	}

	serviceType := strings.TrimSpace(document.Spec.Type)
	if serviceType != "" && serviceType != "ClusterIP" {
		return nil, fmt.Errorf("%w: Kaiwu service %s/%s type must be ClusterIP", ports.ErrUnavailable, r.namespace, serviceName)
	}
	clusterIP := strings.TrimSpace(document.Spec.ClusterIP)
	if clusterIP == "" || clusterIP == "None" {
		return nil, fmt.Errorf("%w: Kaiwu service %s/%s has no ClusterIP", ports.ErrUnavailable, r.namespace, serviceName)
	}
	port := 0
	for _, candidate := range document.Spec.Ports {
		if (candidate.Name == "http" || candidate.Port == kaiwuServicePort) && candidate.Port == kaiwuServicePort {
			port = candidate.Port
			break
		}
	}
	if port == 0 {
		return nil, fmt.Errorf("%w: Kaiwu service %s/%s has no http port %d", ports.ErrUnavailable, r.namespace, serviceName, kaiwuServicePort)
	}

	return &url.URL{Scheme: "http", Host: net.JoinHostPort(clusterIP, strconv.Itoa(port))}, nil
}

// readKaiwuWebToken 读取一个开物 Secret 并解码 data.webToken。返回值只
// 保留在请求内存中，错误信息不包含 token 内容；调用方不得记录或序列化它。
func (r *KubernetesKaiwuRuntimeReader) readKaiwuWebToken(ctx context.Context, secretName string) (string, error) {
	endpoint := r.client.Host() + "/api/v1/namespaces/" + url.PathEscape(r.namespace) + "/secrets/" + url.PathEscape(secretName)
	body, status, err := r.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("%w: read Kaiwu secret %s/%s failed: status=%d err=%v", ports.ErrUnavailable, r.namespace, secretName, status, err)
	}

	var document struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("%w: invalid Kaiwu secret %s/%s", ports.ErrUnavailable, r.namespace, secretName)
	}
	encoded, ok := document.Data["webToken"]
	if !ok {
		return "", fmt.Errorf("%w: Kaiwu secret %s/%s has no webToken", ports.ErrUnavailable, r.namespace, secretName)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: invalid Kaiwu webToken in secret %s/%s", ports.ErrUnavailable, r.namespace, secretName)
	}
	webToken := strings.TrimSpace(string(decoded))
	if webToken == "" {
		return "", fmt.Errorf("%w: Kaiwu webToken in secret %s/%s is empty", ports.ErrUnavailable, r.namespace, secretName)
	}
	return webToken, nil
}

// kaiwuResourceNames 将公开的 client 标识映射为当前配置的 Service 和
// Secret 名称。未知 client 在发起 Kubernetes API 请求前即被拒绝。
func (r *KubernetesKaiwuRuntimeReader) kaiwuResourceNames(client string) (serviceName string, secretName string, ok bool) {
	switch strings.TrimSpace(client) {
	case "console":
		return r.consoleService, r.consoleSecret, true
	case "boss":
		return r.bossService, r.bossSecret, true
	default:
		return "", "", false
	}
}
