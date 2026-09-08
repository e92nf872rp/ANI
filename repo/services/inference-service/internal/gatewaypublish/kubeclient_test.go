package gatewaypublish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestKubeClientApplyUsesServerSideApplyForOnlyManagedKinds(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotAuthorization string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		gotPath = request.URL.RequestURI()
		gotMethod = request.Method
		gotContentType = request.Header.Get("Content-Type")
		gotAuthorization = request.Header.Get("Authorization")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(body)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(successfulResponse(request.URL.Path)))
	}))
	defer server.Close()

	client, err := NewKubeClient(server.URL, "ani_dev_test_token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	object := renderedObject(t, KindBackend)
	if _, err := client.Apply(context.Background(), object); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotPath != "/apis/gateway.envoyproxy.io/v1alpha1/namespaces/ani-aigw/backends/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a?fieldManager=ani-inference-gateway-publisher" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotContentType != "application/apply-patch+yaml" {
		t.Fatalf("content type = %q", gotContentType)
	}
	if gotAuthorization != "Bearer ani_dev_test_token" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if !strings.Contains(gotBody, `"kind":"Backend"`) || !strings.Contains(gotBody, `"namespace":"ani-aigw"`) {
		t.Fatalf("apply body = %q", gotBody)
	}
}

func TestKubeClientGetUsesOnlyApprovedResourceURLs(t *testing.T) {
	requests := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(successfulResponse(request.URL.Path)))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{KindBackend, KindAIServiceBackend, KindAIGatewayRoute, KindGateway} {
		name := "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a"
		if kind == KindGateway {
			name = "ani-aigw"
		}
		got, err := client.Get(context.Background(), kind, "ani-aigw", name)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != kind || got.Namespace != "ani-aigw" || got.Name != name || got.Generation != 9 {
			t.Fatalf("get %s = %#v", kind, got)
		}
	}
	want := []string{
		"GET /apis/gateway.envoyproxy.io/v1alpha1/namespaces/ani-aigw/backends/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a",
		"GET /apis/aigateway.envoyproxy.io/v1beta1/namespaces/ani-aigw/aiservicebackends/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a",
		"GET /apis/aigateway.envoyproxy.io/v1beta1/namespaces/ani-aigw/aigatewayroutes/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a",
		"GET /apis/gateway.networking.k8s.io/v1/namespaces/ani-aigw/gateways/ani-aigw",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %q", requests)
	}
}

func TestKubeClientErrorsAreRedactedAndDelete404Succeeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodGet:
			writer.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			writer.WriteHeader(http.StatusNotFound)
		default:
			writer.WriteHeader(http.StatusConflict)
		}
		_, _ = writer.Write([]byte(`{"message":"Authorization: Bearer ani_dev_fake_credential"}`))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "ani_dev_real_token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), KindBackend, "ani-aigw", "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("get error = %v", err)
	}
	if err := client.Delete(context.Background(), renderedObject(t, KindBackend)); err != nil {
		t.Fatalf("delete 404 = %v", err)
	}
	_, err = client.Apply(context.Background(), renderedObject(t, KindBackend))
	if err == nil || !strings.Contains(err.Error(), "PATCH Backend/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a: HTTP 409") {
		t.Fatalf("apply error = %v", err)
	}
	for _, secret := range []string{"ani_dev_fake_credential", "ani_dev_real_token", "Authorization", "Bearer"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestKubeClientRejectsTraversalAndTimesOutWithoutLeakingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "ani_dev_timeout_token", &http.Client{Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), KindBackend, "ani-aigw", "../secrets"); err == nil || strings.Contains(err.Error(), "secrets") {
		t.Fatalf("traversal error = %v", err)
	}
	_, err = client.Get(context.Background(), KindBackend, "ani-aigw", "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	if err == nil {
		t.Fatal("timeout was accepted")
	}
	if strings.Contains(err.Error(), "ani_dev_timeout_token") {
		t.Fatalf("timeout leaked token: %v", err)
	}
}

func TestKubeClientRedacts422AndInvalidJSONResponses(t *testing.T) {
	for name, response := range map[string]struct {
		status int
		body   string
	}{
		"unprocessable": {status: http.StatusUnprocessableEntity, body: `{"message":"Bearer ani_dev_response_token"}`},
		"invalid JSON":  {status: http.StatusOK, body: `{"message":"ani_dev_json_token"`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet {
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				writer.WriteHeader(response.status)
				_, _ = writer.Write([]byte(response.body))
			}))
			defer server.Close()
			client, err := NewKubeClient(server.URL, "ani_dev_client_token", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Apply(context.Background(), renderedObject(t, KindBackend))
			if err == nil {
				t.Fatal("invalid Kubernetes response was accepted")
			}
			if response.status == http.StatusUnprocessableEntity && !strings.Contains(err.Error(), "HTTP 422") {
				t.Fatalf("422 error = %v", err)
			}
			for _, secret := range []string{"ani_dev_response_token", "ani_dev_json_token", "ani_dev_client_token", "Bearer"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestKubeClientRejectsUnboundedKindsAndInputs(t *testing.T) {
	if _, err := NewKubeClient("https://example.test?token=secret", "token", http.DefaultClient); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("base URL error = %v", err)
	}
	if _, err := NewKubeClient("https://example.test", "  ", http.DefaultClient); err == nil {
		t.Fatal("empty token was accepted")
	}
	for _, raw := range []string{
		"https://example.test?",
		"https://example.test#",
		"https:opaque",
		"https://example.test/%2f",
	} {
		if _, err := NewKubeClient(raw, "token", http.DefaultClient); err == nil {
			t.Fatalf("ambiguous base URL %q was accepted", raw)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Apply(context.Background(), Object{Kind: "Secret", Namespace: "ani-aigw", Name: "anything"}); err == nil {
		t.Fatal("unbounded kind was accepted")
	}
	if _, err := client.Get(context.Background(), KindGateway, "ani-aigw", "other-gateway"); err == nil {
		t.Fatal("non-owned Gateway was accepted")
	}
	if err := client.Delete(context.Background(), Object{Kind: KindGateway, Namespace: "ani-aigw", Name: "ani-aigw"}); err == nil {
		t.Fatal("Gateway delete was accepted")
	}
	for _, kind := range []Kind{KindBackend, KindAIServiceBackend, KindAIGatewayRoute} {
		unowned := renderedObject(t, kind)
		unowned.Name = "ani-c40-chat-vllm"
		unowned.Body["metadata"].(map[string]any)["name"] = unowned.Name
		if _, err := client.Apply(context.Background(), unowned); err == nil {
			t.Fatalf("%s apply accepted an unowned C40 name", kind)
		}
		if _, err := client.Get(context.Background(), kind, "ani-aigw", unowned.Name); err == nil {
			t.Fatalf("%s get accepted an unowned C40 name", kind)
		}
		if err := client.Delete(context.Background(), unowned); err == nil {
			t.Fatalf("%s delete accepted an unowned C40 name", kind)
		}
	}
}

func TestKubeClientApplyRejectsForeignExistingOwnerWithoutPatch(t *testing.T) {
	patches := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPatch {
			patches++
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":9,"labels":{"app.kubernetes.io/managed-by":"foreign-controller","ani.kubercloud.io/tenant-id":"00000000-0000-0000-0000-000000000001","ani.kubercloud.io/inference-service-id":"182df9a4-4a6a-4eed-9d50-51a458a15f6a","ani.kubercloud.io/publication-generation":"7"}}}`))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Apply(context.Background(), renderedObject(t, KindBackend)); err == nil {
		t.Fatal("foreign existing object was adopted")
	}
	if patches != 0 {
		t.Fatalf("foreign object received %d PATCH requests", patches)
	}
}

func TestKubeClientDeleteUsesOwnedUIDPrecondition(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","uid":"uid-1","generation":9,"labels":{"app.kubernetes.io/managed-by":"ani-inference-gateway-publisher","ani.kubercloud.io/tenant-id":"00000000-0000-0000-0000-000000000001","ani.kubercloud.io/inference-service-id":"182df9a4-4a6a-4eed-9d50-51a458a15f6a","ani.kubercloud.io/publication-generation":"7"}}}`))
			return
		}
		deletes++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"uid":"uid-1"`) {
			t.Fatalf("delete precondition body = %s", body)
		}
		_, _ = writer.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Success"}`))
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), renderedObject(t, KindBackend)); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("delete calls = %d", deletes)
	}
}

func TestDecodeObjectRejectsAmbiguousOrMismatchedSuccessBodies(t *testing.T) {
	fallback := renderedObject(t, KindBackend)
	for name, body := range map[string]string{
		"empty":                 "",
		"null":                  "null",
		"array":                 "[]",
		"trailing JSON":         `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":7}} {}`,
		"wrong API version":     `{"apiVersion":"v1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":7}}`,
		"wrong kind":            `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Secret","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":7}}`,
		"wrong name":            `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"other","namespace":"ani-aigw","generation":7}}`,
		"wrong namespace":       `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"other","generation":7}}`,
		"missing generation":    `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw"}}`,
		"fractional generation": `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":7.5}}`,
		"overflow generation":   `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":9223372036854775808}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeObject([]byte(body), fallback); err == nil {
				t.Fatalf("decodeObject accepted %s", name)
			}
		})
	}
}

func TestDecodeObjectAcceptsExpectedObjectAndObjectStatus(t *testing.T) {
	fallback := renderedObject(t, KindBackend)
	body := `{"apiVersion":"gateway.envoyproxy.io/v1alpha1","kind":"Backend","metadata":{"name":"ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a","namespace":"ani-aigw","generation":9},"status":{"conditions":[]}}`
	object, err := decodeObject([]byte(body), fallback)
	if err != nil || object.Generation != 9 || object.Status == nil {
		t.Fatalf("decodeObject = (%#v, %v)", object, err)
	}
}

func TestValidObjectBodyRequiresRenderedSpec(t *testing.T) {
	object := managedObject(KindBackend)
	if validObjectBody(object) {
		t.Fatal("Apply accepted an object without the rendered Backend spec")
	}
}

func TestValidObjectBodyRejectsAnyExtraRenderedShapeField(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *Object){
		"top level": func(t *testing.T, object *Object) {
			object.Body["extra"] = true
		},
		"metadata": func(t *testing.T, object *Object) {
			objectMap(t, object.Body["metadata"])["extra"] = true
		},
		"labels": func(t *testing.T, object *Object) {
			objectMap(t, objectMap(t, object.Body["metadata"])["labels"])["extra"] = true
		},
		"Backend spec": func(t *testing.T, object *Object) {
			object.Body["spec"].(map[string]any)["extra"] = true
		},
		"AIServiceBackend backendRef namespace": func(t *testing.T, object *Object) {
			object.Body["spec"].(map[string]any)["backendRef"].(map[string]any)["namespace"] = "ani-aigw"
		},
		"AIServiceBackend schema extra": func(t *testing.T, object *Object) {
			object.Body["spec"].(map[string]any)["schema"].(map[string]any)["extra"] = true
		},
		"AIGatewayRoute parentRef namespace": func(t *testing.T, object *Object) {
			parent := objectList(t, object.Body["spec"].(map[string]any)["parentRefs"])[0].(map[string]any)
			parent["namespace"] = "ani-aigw"
		},
		"AIGatewayRoute rule filters": func(t *testing.T, object *Object) {
			rule := objectList(t, object.Body["spec"].(map[string]any)["rules"])[0].(map[string]any)
			rule["filters"] = []any{}
		},
		"AIGatewayRoute match path": func(t *testing.T, object *Object) {
			rule := objectList(t, object.Body["spec"].(map[string]any)["rules"])[0].(map[string]any)
			match := objectList(t, rule["matches"])[0].(map[string]any)
			match["path"] = map[string]any{"type": "Exact", "value": "/v1/chat/completions"}
		},
		"AIGatewayRoute header extra": func(t *testing.T, object *Object) {
			rule := objectList(t, object.Body["spec"].(map[string]any)["rules"])[0].(map[string]any)
			header := objectList(t, objectList(t, rule["matches"])[0].(map[string]any)["headers"])[0].(map[string]any)
			header["extra"] = true
		},
		"AIGatewayRoute headerMutation add": func(t *testing.T, object *Object) {
			rule := objectList(t, object.Body["spec"].(map[string]any)["rules"])[0].(map[string]any)
			ref := objectList(t, rule["backendRefs"])[0].(map[string]any)
			ref["headerMutation"].(map[string]any)["add"] = []any{}
		},
		"AIGatewayRoute headerMutation set": func(t *testing.T, object *Object) {
			rule := objectList(t, object.Body["spec"].(map[string]any)["rules"])[0].(map[string]any)
			ref := objectList(t, rule["backendRefs"])[0].(map[string]any)
			ref["headerMutation"].(map[string]any)["set"] = []any{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			kind := KindBackend
			switch {
			case strings.HasPrefix(name, "AIServiceBackend"):
				kind = KindAIServiceBackend
			case strings.HasPrefix(name, "AIGatewayRoute"):
				kind = KindAIGatewayRoute
			}
			object := renderedObject(t, kind)
			mutate(t, &object)
			if validObjectBody(object) {
				t.Fatalf("Apply accepted extra field in %s", name)
			}
		})
	}
}

func TestKubeClientRejectsRedirectsWithoutForwardingBearer(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/apis/gateway.envoyproxy.io/v1alpha1/namespaces/ani-aigw/backends/ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a" {
			t.Fatalf("redirect reached %q with Authorization %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.Header().Set("Location", "/unexpected")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client, err := NewKubeClient(server.URL, "ani_dev_redirect_token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), KindBackend, "ani-aigw", "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || strings.Contains(err.Error(), "ani_dev_redirect_token") {
		t.Fatalf("redirect error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestResourcePathRejectsUnownedNames(t *testing.T) {
	for _, kind := range []Kind{KindBackend, KindAIServiceBackend, KindAIGatewayRoute} {
		for _, name := range []string{"ani-c40-chat-vllm", "unowned"} {
			if _, err := resourcePath(kind, "ani-aigw", name, true, false); err == nil {
				t.Fatalf("%s resource path accepted %q", kind, name)
			}
		}
	}
}

func managedObject(kind Kind) Object {
	apiVersion := "aigateway.envoyproxy.io/v1beta1"
	if kind == KindBackend {
		apiVersion = "gateway.envoyproxy.io/v1alpha1"
	}
	return Object{
		Kind: kind, Namespace: "ani-aigw", Name: "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a", Generation: 7,
		Body: map[string]any{
			"apiVersion": apiVersion, "kind": string(kind),
			"metadata": map[string]any{
				"name": "ani-inf-182df9a4-4a6a-4eed-9d50-51a458a15f6a", "namespace": "ani-aigw",
				"labels": map[string]any{
					"app.kubernetes.io/managed-by":             "ani-inference-gateway-publisher",
					"ani.kubercloud.io/tenant-id":              "00000000-0000-0000-0000-000000000001",
					"ani.kubercloud.io/inference-service-id":   "182df9a4-4a6a-4eed-9d50-51a458a15f6a",
					"ani.kubercloud.io/publication-generation": "7",
				},
			},
		},
	}
}

func renderedObject(t *testing.T, kind Kind) Object {
	t.Helper()
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	serviceID := uuid.MustParse("182df9a4-4a6a-4eed-9d50-51a458a15f6a")
	objects, err := Render(publicationTarget(tenantID, serviceID))
	if err != nil {
		t.Fatal(err)
	}
	switch kind {
	case KindBackend:
		return objects.Backend
	case KindAIServiceBackend:
		return objects.AIServiceBackend
	case KindAIGatewayRoute:
		return objects.AIGatewayRoute
	default:
		t.Fatalf("no rendered object for %s", kind)
		return Object{}
	}
}

func resourceKindForPath(path string) string {
	if strings.Contains(path, "/gateways/") {
		return "Gateway"
	}
	if strings.Contains(path, "/aiservicebackends/") {
		return "AIServiceBackend"
	}
	if strings.Contains(path, "/aigatewayroutes/") {
		return "AIGatewayRoute"
	}
	return "Backend"
}

func successfulResponse(path string) string {
	kind := resourceKindForPath(path)
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	labels := ""
	if strings.HasPrefix(name, "ani-inf-") {
		labels = `,"labels":{"app.kubernetes.io/managed-by":"ani-inference-gateway-publisher","ani.kubercloud.io/tenant-id":"00000000-0000-0000-0000-000000000001","ani.kubercloud.io/inference-service-id":"182df9a4-4a6a-4eed-9d50-51a458a15f6a","ani.kubercloud.io/publication-generation":"7"}`
	}
	return fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"metadata":{"name":%q,"namespace":"ani-aigw","generation":9%s}}`, apiVersion(Kind(kind)), kind, name, labels)
}
