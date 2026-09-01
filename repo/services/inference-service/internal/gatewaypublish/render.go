package gatewaypublish

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

const (
	publisherName    = "ani-inference-gateway-publisher"
	gatewayNamespace = "ani-aigw"
	gatewayName      = "ani-aigw"
)

func ResourceName(serviceID uuid.UUID) string {
	return "ani-inf-" + serviceID.String()
}

func Render(target repository.PublicationTarget) (Objects, error) {
	if target.TenantID == uuid.Nil || target.ServiceID == uuid.Nil || target.Generation <= 0 ||
		target.Desired != domain.PublicationPublished || strings.TrimSpace(target.ServedModelName) == "" ||
		target.ServedModelName != strings.TrimSpace(target.ServedModelName) {
		return Objects{}, errors.New("invalid gateway publication target")
	}
	if target.Task != domain.InferenceTaskGenerate && target.Task != domain.InferenceTaskEmbed {
		return Objects{}, errors.New("invalid gateway publication task")
	}
	host, port, err := endpointHostPort(target.RuntimeEndpoint, target)
	if err != nil {
		return Objects{}, err
	}
	name := ResourceName(target.ServiceID)
	labels := ownerLabels(target)
	backend := object(KindBackend, name, target.Generation, labels, map[string]any{
		"type":      "Endpoints",
		"endpoints": []any{map[string]any{"fqdn": map[string]any{"hostname": host, "port": port}}},
	})
	aiBackend := object(KindAIServiceBackend, name, target.Generation, labels, map[string]any{
		"backendRef": map[string]any{"group": "gateway.envoyproxy.io", "kind": "Backend", "name": name},
		"schema":     map[string]any{"name": "OpenAI", "version": "v1"},
	})
	route := object(KindAIGatewayRoute, name, target.Generation, labels, map[string]any{
		"parentRefs": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": gatewayName}},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{"headers": []any{
				map[string]any{"name": "x-ai-eg-model", "type": "Exact", "value": target.ServedModelName},
				map[string]any{"name": "x-ani-tenant-id", "type": "Exact", "value": target.TenantID.String()},
				map[string]any{"name": "x-ani-inference-service-id", "type": "Exact", "value": target.ServiceID.String()},
			}}},
			"backendRefs": []any{map[string]any{
				"name": name, "priority": 0, "weight": 1,
				"headerMutation": map[string]any{"remove": []any{
					"Authorization", "x-api-key", "x-ai-eg-model", "x-ani-tenant-id", "x-ani-inference-service-id", "x-ani-user-id",
				}},
			}},
		}},
	})
	return Objects{Backend: backend, AIServiceBackend: aiBackend, AIGatewayRoute: route}, nil
}

func ownerLabels(target repository.PublicationTarget) map[string]any {
	return map[string]any{
		"app.kubernetes.io/managed-by":             publisherName,
		"ani.kubercloud.io/tenant-id":              target.TenantID.String(),
		"ani.kubercloud.io/inference-service-id":   target.ServiceID.String(),
		"ani.kubercloud.io/publication-generation": strconv.FormatInt(target.Generation, 10),
	}
}

func object(kind Kind, name string, generation int64, labels map[string]any, spec map[string]any) Object {
	apiVersion := ""
	switch kind {
	case KindBackend:
		apiVersion = "gateway.envoyproxy.io/v1alpha1"
	case KindAIServiceBackend, KindAIGatewayRoute:
		apiVersion = "aigateway.envoyproxy.io/v1beta1"
	}
	return Object{
		Kind: kind, Namespace: gatewayNamespace, Name: name, Generation: generation,
		Body: map[string]any{
			"apiVersion": apiVersion,
			"kind":       string(kind),
			"metadata":   map[string]any{"name": name, "namespace": gatewayNamespace, "labels": copyLabels(labels)},
			"spec":       spec,
		},
	}
}

func copyLabels(labels map[string]any) map[string]any {
	copy := make(map[string]any, len(labels))
	for key, value := range labels {
		copy[key] = value
	}
	return copy
}

func endpointHostPort(raw string, target repository.PublicationTarget) (string, int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Path != "" || u.RawPath != "" {
		return "", 0, errors.New("invalid runtime endpoint")
	}
	if u.Port() == "" || strings.Contains(u.Host, "%") {
		return "", 0, errors.New("invalid runtime endpoint")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("invalid runtime endpoint")
	}
	host := u.Hostname()
	expectedHost := runtimeServiceHost(target.ServiceID, target.TenantID)
	if host == "" || host != expectedHost || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil || !strings.HasSuffix(host, ".svc.cluster.local") || !validDNSName(host) {
		return "", 0, errors.New("invalid runtime endpoint")
	}
	return host, port, nil
}

func runtimeServiceHost(serviceID, tenantID uuid.UUID) string {
	return "pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local"
}

func validDNSName(name string) bool {
	if len(name) > 253 || strings.Contains(name, "..") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}
