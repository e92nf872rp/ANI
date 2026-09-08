package gatewaypublish

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

func TestRenderProducesInstalledEnvoySchemas(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	serviceID := uuid.MustParse("182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	target := publicationTarget(tenantID, serviceID)

	objects, err := Render(target)
	if err != nil {
		t.Fatal(err)
	}
	name := "ani-inf-" + serviceID.String()
	if objects.Backend.Name != name || objects.AIServiceBackend.Name != name || objects.AIGatewayRoute.Name != name {
		t.Fatalf("resource names = %q, %q, %q", objects.Backend.Name, objects.AIServiceBackend.Name, objects.AIGatewayRoute.Name)
	}
	if objects.Backend.Namespace != "ani-aigw" || objects.Backend.Generation != 7 {
		t.Fatalf("backend metadata = %#v", objects.Backend)
	}
	if objects.Backend.Body["apiVersion"] != "gateway.envoyproxy.io/v1alpha1" || objects.Backend.Body["kind"] != "Backend" {
		t.Fatalf("backend schema = %#v", objects.Backend.Body)
	}
	backendSpec := objectMap(t, objects.Backend.Body["spec"])
	if backendSpec["type"] != "Endpoints" {
		t.Fatalf("backend type = %#v", backendSpec["type"])
	}
	endpoint := objectMap(t, objectList(t, backendSpec["endpoints"])[0])
	fqdn := objectMap(t, endpoint["fqdn"])
	if fqdn["hostname"] != "pw-"+serviceID.String()+".ani-tenant-"+tenantID.String()+".svc.cluster.local" || fqdn["port"] != 8000 {
		t.Fatalf("backend endpoint = %#v", fqdn)
	}
	if objects.AIServiceBackend.Body["apiVersion"] != "aigateway.envoyproxy.io/v1beta1" || objects.AIServiceBackend.Body["kind"] != "AIServiceBackend" {
		t.Fatalf("AIServiceBackend schema = %#v", objects.AIServiceBackend.Body)
	}
	aiSpec := objectMap(t, objects.AIServiceBackend.Body["spec"])
	if objectMap(t, aiSpec["schema"])["name"] != "OpenAI" || objectMap(t, aiSpec["schema"])["version"] != "v1" {
		t.Fatalf("AIServiceBackend schema config = %#v", aiSpec["schema"])
	}
	if objects.AIGatewayRoute.Body["apiVersion"] != "aigateway.envoyproxy.io/v1beta1" || objects.AIGatewayRoute.Body["kind"] != "AIGatewayRoute" {
		t.Fatalf("route schema = %#v", objects.AIGatewayRoute.Body)
	}
	routeSpec := objectMap(t, objects.AIGatewayRoute.Body["spec"])
	if _, found := routeSpec["path"]; found {
		t.Fatal("route must not contain an unsupported path match")
	}
	parent := objectMap(t, objectList(t, routeSpec["parentRefs"])[0])
	if parent["group"] != "gateway.networking.k8s.io" || parent["kind"] != "Gateway" || parent["name"] != "ani-aigw" {
		t.Fatalf("route parent = %#v", parent)
	}
	if got := routeHeader(t, objects.AIGatewayRoute, "x-ai-eg-model"); got != "ani-qwen3" {
		t.Fatalf("model header = %q", got)
	}
	if got := routeHeader(t, objects.AIGatewayRoute, "x-ani-tenant-id"); got != tenantID.String() {
		t.Fatalf("tenant header = %q", got)
	}
	if got := routeHeader(t, objects.AIGatewayRoute, "x-ani-inference-service-id"); got != serviceID.String() {
		t.Fatalf("service header = %q", got)
	}
	for _, header := range []string{"Authorization", "x-api-key", "x-ai-eg-model", "x-ani-tenant-id", "x-ani-inference-service-id", "x-ani-user-id"} {
		if !routeRemoves(t, objects.AIGatewayRoute, header) {
			t.Fatalf("route does not remove %q", header)
		}
	}
	labels := objectMap(t, objectMap(t, objects.Backend.Body["metadata"])["labels"])
	if labels["app.kubernetes.io/managed-by"] != "ani-inference-gateway-publisher" ||
		labels["ani.kubercloud.io/tenant-id"] != tenantID.String() ||
		labels["ani.kubercloud.io/inference-service-id"] != serviceID.String() ||
		labels["ani.kubercloud.io/publication-generation"] != "7" {
		t.Fatalf("owner labels = %#v", labels)
	}
}

func TestRenderRejectsUnsafePublicationTargets(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	serviceID := uuid.MustParse("182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	for name, mutate := range map[string]func(*repository.PublicationTarget){
		"IP endpoint":     func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://127.0.0.1:8000" },
		"IPv6 endpoint":   func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://[::1]:8000" },
		"missing port":    func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://pw.example.svc" },
		"non HTTP scheme": func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "https://pw.example.svc:8000" },
		"userinfo": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://user:password@pw.example.svc:8000"
		},
		"query": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw.example.svc:8000?token=secret"
		},
		"empty query": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw.example.svc.cluster.local:8000?"
		},
		"fragment": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw.example.svc:8000#secret"
		},
		"empty fragment": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000#"
		},
		"non service host":         func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://example.com:8000" },
		"uppercase service suffix": func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://pw.example.SVC:8000" },
		"trailing dot":             func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://pw.example.svc.:8000" },
		"escaped host":             func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://pw%2eexample.svc:8000" },
		"localhost":                func(target *repository.PublicationTarget) { target.RuntimeEndpoint = "http://localhost.svc:8000" },
		"empty model":              func(target *repository.PublicationTarget) { target.ServedModelName = "" },
		"unknown task":             func(target *repository.PublicationTarget) { target.Task = "unknown" },
		"zero generation":          func(target *repository.PublicationTarget) { target.Generation = 0 },
		"cross tenant endpoint": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw-" + serviceID.String() + ".ani-tenant-00000000-0000-0000-0000-000000000002.svc.cluster.local:8000"
		},
		"cross service endpoint": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw-6ae6f951-415d-454f-8459-cd38d32dc58f.ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000"
		},
		"mixed case target endpoint": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://PW-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000"
		},
		"trailing target endpoint dot": func(target *repository.PublicationTarget) {
			target.RuntimeEndpoint = "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local.:8000"
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := publicationTarget(tenantID, serviceID)
			mutate(&target)
			_, err := Render(target)
			if err == nil {
				t.Fatal("Render accepted an unsafe target")
			}
			if strings.Contains(strings.ToLower(err.Error()), "password") || strings.Contains(strings.ToLower(err.Error()), "token") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatalf("Render leaked endpoint content: %v", err)
			}
		})
	}
}

func TestRenderDoesNotShareOwnerLabelsAcrossObjects(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	serviceID := uuid.MustParse("182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	objects, err := Render(publicationTarget(tenantID, serviceID))
	if err != nil {
		t.Fatal(err)
	}
	backendLabels := objectMap(t, objectMap(t, objects.Backend.Body["metadata"])["labels"])
	backendLabels["ani.kubercloud.io/publication-generation"] = "mutated"
	for name, object := range map[string]Object{
		"AIServiceBackend": objects.AIServiceBackend,
		"AIGatewayRoute":   objects.AIGatewayRoute,
	} {
		labels := objectMap(t, objectMap(t, object.Body["metadata"])["labels"])
		if labels["ani.kubercloud.io/publication-generation"] != "7" {
			t.Fatalf("%s labels were aliased: %#v", name, labels)
		}
	}
}

func publicationTarget(tenantID, serviceID uuid.UUID) repository.PublicationTarget {
	return repository.PublicationTarget{
		TenantID:        tenantID,
		ServiceID:       serviceID,
		Generation:      7,
		Desired:         domain.PublicationPublished,
		ServedModelName: "ani-qwen3",
		Task:            domain.InferenceTaskGenerate,
		RuntimeEndpoint: "http://pw-" + serviceID.String() + ".ani-tenant-" + tenantID.String() + ".svc.cluster.local:8000",
	}
}

func routeHeader(t *testing.T, route Object, name string) string {
	t.Helper()
	rules := objectList(t, objectMap(t, route.Body["spec"])["rules"])
	if len(rules) != 1 {
		t.Fatalf("rules = %#v", rules)
	}
	matches := objectList(t, objectMap(t, rules[0])["matches"])
	if len(matches) != 1 {
		t.Fatalf("matches = %#v", matches)
	}
	for _, item := range objectList(t, objectMap(t, matches[0])["headers"]) {
		header := objectMap(t, item)
		if header["name"] == name {
			if header["type"] != "Exact" {
				t.Fatalf("header %q type = %#v", name, header["type"])
			}
			value, _ := header["value"].(string)
			return value
		}
	}
	return ""
}

func routeRemoves(t *testing.T, route Object, name string) bool {
	t.Helper()
	rules := objectList(t, objectMap(t, route.Body["spec"])["rules"])
	backendRefs := objectList(t, objectMap(t, rules[0])["backendRefs"])
	removed := objectList(t, objectMap(t, objectMap(t, backendRefs[0])["headerMutation"])["remove"])
	for _, item := range removed {
		if item == name {
			return true
		}
	}
	return false
}

func objectMap(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %#v", value)
	}
	return result
}

func objectList(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("expected list, got %#v", value)
	}
	return result
}
