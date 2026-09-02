package gatewaypublish

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	serviceAccountDirectory = "/var/run/secrets/kubernetes.io/serviceaccount"
	serviceAccountTokenPath = serviceAccountDirectory + "/token"
	serviceAccountCAPath    = serviceAccountDirectory + "/ca.crt"
	maxResponseBytes        = 1 << 20
)

type KubeClient struct {
	baseURL string
	client  *http.Client
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(copy)
}

func NewKubeClient(baseURL, token string, client *http.Client) (*KubeClient, error) {
	base, err := validatedBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("kubernetes service account token is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	baseTransport := client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	copy.Transport = bearerTransport{base: baseTransport, token: token}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &KubeClient{baseURL: base, client: &copy}, nil
}

// NewInClusterKubeClient loads only the mounted service-account material at
// runtime. It deliberately has no package initialization side effects.
func NewInClusterKubeClient(timeout time.Duration) (*KubeClient, error) {
	if timeout <= 0 {
		return nil, errors.New("kubernetes request timeout must be positive")
	}
	tokenBytes, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, errors.New("cannot read kubernetes service account token")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, errors.New("kubernetes service account token is empty")
	}
	caBytes, err := os.ReadFile(serviceAccountCAPath)
	if err != nil {
		return nil, errors.New("cannot read kubernetes service account CA")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("invalid kubernetes service account CA")
	}
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS"))
	if host == "" || port == "" {
		return nil, errors.New("kubernetes service host and HTTPS port are required")
	}
	portValue, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portValue == 0 {
		return nil, errors.New("invalid kubernetes service HTTPS port")
	}
	return NewKubeClient("https://"+net.JoinHostPort(host, port), token, &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	})
}

func (client *KubeClient) Apply(ctx context.Context, object Object) (Object, error) {
	path, err := resourcePath(object.Kind, object.Namespace, object.Name, true, true)
	if err != nil || !validObjectBody(object) {
		return Object{}, errors.New("invalid managed Kubernetes object")
	}
	existing, err := client.Get(ctx, object.Kind, object.Namespace, object.Name)
	if err == nil && !sameManagedOwner(existing, object, false) {
		return Object{}, errors.New("existing Kubernetes object is not owned by this publication")
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Object{}, err
	}
	payload, err := json.Marshal(object.Body)
	if err != nil {
		return Object{}, errors.New("cannot encode Kubernetes apply object")
	}
	response, err := client.request(ctx, http.MethodPatch, path+"?fieldManager=ani-inference-gateway-publisher", payload, "application/apply-patch+yaml")
	if err != nil {
		return Object{}, err
	}
	applied, err := decodeObject(response, object)
	if err != nil {
		return Object{}, err
	}
	if !sameManagedOwner(applied, object, true) {
		return Object{}, errors.New("applied Kubernetes object ownership mismatch")
	}
	return applied, nil
}

func (client *KubeClient) Get(ctx context.Context, kind Kind, namespace, name string) (Object, error) {
	path, err := resourcePath(kind, namespace, name, true, false)
	if err != nil {
		return Object{}, errors.New("invalid managed Kubernetes object")
	}
	response, err := client.request(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return Object{}, err
	}
	return decodeObject(response, Object{Kind: kind, Namespace: namespace, Name: name})
}

func (client *KubeClient) Delete(ctx context.Context, expected Object) error {
	path, err := resourcePath(expected.Kind, expected.Namespace, expected.Name, true, true)
	if err != nil || !validManagedReference(expected) {
		return errors.New("invalid managed Kubernetes object")
	}
	existing, err := client.Get(ctx, expected.Kind, expected.Namespace, expected.Name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sameManagedOwner(existing, expected, false) {
		return errors.New("existing Kubernetes object is not owned by this publication")
	}
	metadata, _ := existing.Body["metadata"].(map[string]any)
	uid, _ := metadata["uid"].(string)
	if strings.TrimSpace(uid) == "" {
		return errors.New("existing Kubernetes object UID is missing")
	}
	payload, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "DeleteOptions",
		"preconditions": map[string]any{"uid": uid},
	})
	if err != nil {
		return errors.New("cannot encode Kubernetes delete precondition")
	}
	_, err = client.request(ctx, http.MethodDelete, path, payload, "application/json")
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (client *KubeClient) request(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot create Kubernetes request")
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s Kubernetes request: %w", method, context.DeadlineExceeded)
		}
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%s Kubernetes request: %w", method, context.Canceled)
		}
		return nil, fmt.Errorf("%s Kubernetes request failed", method)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		discardBounded(response.Body)
		return nil, ErrNotFound
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		discardBounded(response.Body)
		return nil, fmt.Errorf("%s %s: HTTP %d", method, kindName(path), response.StatusCode)
	}
	result, err := readBounded(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%s Kubernetes response invalid", method)
	}
	return result, nil
}

func resourcePath(kind Kind, namespace, name string, requireName, writable bool) (string, error) {
	if namespace != gatewayNamespace || !validResourceName(namespace) || (requireName && !validResourceName(name)) {
		return "", errors.New("invalid managed Kubernetes resource")
	}
	group, version, resource := "", "", ""
	switch kind {
	case KindBackend:
		if !managedResourceName(name) {
			return "", errors.New("unowned Kubernetes resource")
		}
		group, version, resource = "gateway.envoyproxy.io", "v1alpha1", "backends"
	case KindAIServiceBackend:
		if !managedResourceName(name) {
			return "", errors.New("unowned Kubernetes resource")
		}
		group, version, resource = "aigateway.envoyproxy.io", "v1beta1", "aiservicebackends"
	case KindAIGatewayRoute:
		if !managedResourceName(name) {
			return "", errors.New("unowned Kubernetes resource")
		}
		group, version, resource = "aigateway.envoyproxy.io", "v1beta1", "aigatewayroutes"
	case KindGateway:
		if writable || name != gatewayName {
			return "", errors.New("gateway access is read-only")
		}
		group, version, resource = "gateway.networking.k8s.io", "v1", "gateways"
	default:
		return "", errors.New("unsupported Kubernetes kind")
	}
	return "/apis/" + group + "/" + version + "/namespaces/" + namespace + "/" + resource + "/" + name, nil
}

func validObjectBody(object Object) bool {
	if object.Body == nil || !exactKeys(object.Body, "apiVersion", "kind", "metadata", "spec") || object.Body["kind"] != string(object.Kind) || object.Body["apiVersion"] != apiVersion(object.Kind) {
		return false
	}
	metadata, ok := object.Body["metadata"].(map[string]any)
	if !ok || !exactKeys(metadata, "name", "namespace", "labels") || metadata["name"] != object.Name || metadata["namespace"] != object.Namespace || !managedResourceName(object.Name) || object.Generation <= 0 {
		return false
	}
	serviceID, _ := uuid.Parse(strings.TrimPrefix(object.Name, "ani-inf-"))
	labels, ok := metadata["labels"].(map[string]any)
	if !ok || !exactKeys(labels, "app.kubernetes.io/managed-by", "ani.kubercloud.io/tenant-id", "ani.kubercloud.io/inference-service-id", "ani.kubercloud.io/publication-generation") || labels["app.kubernetes.io/managed-by"] != publisherName || labels["ani.kubercloud.io/inference-service-id"] != serviceID.String() {
		return false
	}
	tenantID, err := uuid.Parse(labelString(labels, "ani.kubercloud.io/tenant-id"))
	if err != nil || tenantID == uuid.Nil {
		return false
	}
	if labelString(labels, "ani.kubercloud.io/publication-generation") != strconv.FormatInt(object.Generation, 10) {
		return false
	}
	return validManagedSpec(object.Kind, object.Body["spec"], object.Name, tenantID, serviceID)
}

func validManagedReference(object Object) bool {
	if object.Kind != KindBackend && object.Kind != KindAIServiceBackend && object.Kind != KindAIGatewayRoute {
		return false
	}
	if object.Namespace != gatewayNamespace || !managedResourceName(object.Name) || object.Generation <= 0 || object.Body == nil {
		return false
	}
	metadata, ok := object.Body["metadata"].(map[string]any)
	if !ok || metadata["name"] != object.Name || metadata["namespace"] != object.Namespace {
		return false
	}
	labels, ok := metadata["labels"].(map[string]any)
	if !ok || labels["app.kubernetes.io/managed-by"] != publisherName {
		return false
	}
	serviceID, err := uuid.Parse(labelString(labels, "ani.kubercloud.io/inference-service-id"))
	if err != nil || serviceID == uuid.Nil || ResourceName(serviceID) != object.Name {
		return false
	}
	tenantID, err := uuid.Parse(labelString(labels, "ani.kubercloud.io/tenant-id"))
	if err != nil || tenantID == uuid.Nil {
		return false
	}
	generation, err := strconv.ParseInt(labelString(labels, "ani.kubercloud.io/publication-generation"), 10, 64)
	return err == nil && generation == object.Generation
}

func sameManagedOwner(existing, expected Object, exactGeneration bool) bool {
	if !validManagedReference(expected) || existing.Kind != expected.Kind || existing.Namespace != expected.Namespace || existing.Name != expected.Name {
		return false
	}
	existingMetadata, ok := existing.Body["metadata"].(map[string]any)
	if !ok {
		return false
	}
	existingLabels, ok := existingMetadata["labels"].(map[string]any)
	if !ok || existingLabels["app.kubernetes.io/managed-by"] != publisherName {
		return false
	}
	expectedMetadata, _ := expected.Body["metadata"].(map[string]any)
	expectedLabels, _ := expectedMetadata["labels"].(map[string]any)
	for _, label := range []string{"ani.kubercloud.io/tenant-id", "ani.kubercloud.io/inference-service-id"} {
		if labelString(existingLabels, label) != labelString(expectedLabels, label) {
			return false
		}
	}
	existingGeneration, err := strconv.ParseInt(labelString(existingLabels, "ani.kubercloud.io/publication-generation"), 10, 64)
	if err != nil || existingGeneration <= 0 || existingGeneration > expected.Generation {
		return false
	}
	return !exactGeneration || existingGeneration == expected.Generation
}

func validManagedSpec(kind Kind, value any, name string, tenantID, serviceID uuid.UUID) bool {
	spec, ok := value.(map[string]any)
	if !ok {
		return false
	}
	switch kind {
	case KindBackend:
		endpoints, ok := spec["endpoints"].([]any)
		if !exactKeys(spec, "type", "endpoints") || !ok || spec["type"] != "Endpoints" || len(endpoints) != 1 {
			return false
		}
		endpoint, ok := endpoints[0].(map[string]any)
		if !ok || !exactKeys(endpoint, "fqdn") {
			return false
		}
		fqdn, ok := endpoint["fqdn"].(map[string]any)
		if !ok || !exactKeys(fqdn, "hostname", "port") {
			return false
		}
		port, ok := fqdn["port"].(int)
		return ok && fqdn["hostname"] == runtimeServiceHost(serviceID, tenantID) && port > 0 && port <= 65535
	case KindAIServiceBackend:
		backendRef, refOK := spec["backendRef"].(map[string]any)
		schema, schemaOK := spec["schema"].(map[string]any)
		return exactKeys(spec, "backendRef", "schema") && refOK && schemaOK && exactKeys(backendRef, "group", "kind", "name") && exactKeys(schema, "name", "version") && backendRef["group"] == "gateway.envoyproxy.io" && backendRef["kind"] == "Backend" && backendRef["name"] == name && schema["name"] == "OpenAI" && schema["version"] == "v1"
	case KindAIGatewayRoute:
		parents, parentsOK := spec["parentRefs"].([]any)
		rules, rulesOK := spec["rules"].([]any)
		if !exactKeys(spec, "parentRefs", "rules") || !parentsOK || !rulesOK || len(parents) != 1 || len(rules) != 1 {
			return false
		}
		parent, parentOK := parents[0].(map[string]any)
		rule, ruleOK := rules[0].(map[string]any)
		if !parentOK || !ruleOK || !exactKeys(parent, "group", "kind", "name") || !exactKeys(rule, "matches", "backendRefs") || parent["group"] != "gateway.networking.k8s.io" || parent["kind"] != "Gateway" || parent["name"] != gatewayName {
			return false
		}
		return validRouteRule(rule, name, tenantID, serviceID)
	default:
		return false
	}
}

func validRouteRule(rule map[string]any, name string, tenantID, serviceID uuid.UUID) bool {
	matches, matchesOK := rule["matches"].([]any)
	refs, refsOK := rule["backendRefs"].([]any)
	if !matchesOK || !refsOK || len(matches) != 1 || len(refs) != 1 {
		return false
	}
	match, matchOK := matches[0].(map[string]any)
	if !matchOK {
		return false
	}
	headers, headersOK := match["headers"].([]any)
	if !exactKeys(match, "headers") || !headersOK || len(headers) != 3 {
		return false
	}
	want := map[string]string{
		"x-ani-tenant-id":            tenantID.String(),
		"x-ani-inference-service-id": serviceID.String(),
	}
	modelFound := false
	for _, value := range headers {
		header, ok := value.(map[string]any)
		if !ok || !exactKeys(header, "name", "type", "value") || header["type"] != "Exact" {
			return false
		}
		headerName, nameOK := header["name"].(string)
		headerValue, valueOK := header["value"].(string)
		if !nameOK || !valueOK {
			return false
		}
		if headerName == "x-ai-eg-model" {
			modelFound = headerValue != "" && headerValue == strings.TrimSpace(headerValue)
			continue
		}
		if want[headerName] != headerValue {
			return false
		}
		delete(want, headerName)
	}
	if !modelFound || len(want) != 0 {
		return false
	}
	ref, ok := refs[0].(map[string]any)
	if !ok || !exactKeys(ref, "name", "priority", "weight", "headerMutation") || ref["name"] != name || ref["priority"] != 0 || ref["weight"] != 1 {
		return false
	}
	mutation, ok := ref["headerMutation"].(map[string]any)
	if !ok || !exactKeys(mutation, "remove") {
		return false
	}
	removed, ok := mutation["remove"].([]any)
	if !ok || len(removed) != 6 {
		return false
	}
	wantedRemoval := map[string]bool{"Authorization": true, "x-api-key": true, "x-ai-eg-model": true, "x-ani-tenant-id": true, "x-ani-inference-service-id": true, "x-ani-user-id": true}
	for _, value := range removed {
		name, ok := value.(string)
		if !ok || !wantedRemoval[name] {
			return false
		}
		delete(wantedRemoval, name)
	}
	return len(wantedRemoval) == 0
}

func exactKeys(object map[string]any, allowed ...string) bool {
	if len(object) != len(allowed) {
		return false
	}
	for _, key := range allowed {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func managedResourceName(name string) bool {
	if !strings.HasPrefix(name, "ani-inf-") {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(name, "ani-inf-"))
	return err == nil && id != uuid.Nil && ResourceName(id) == name
}

func labelString(labels map[string]any, name string) string {
	value, _ := labels[name].(string)
	return value
}

func apiVersion(kind Kind) string {
	switch kind {
	case KindBackend:
		return "gateway.envoyproxy.io/v1alpha1"
	case KindAIServiceBackend, KindAIGatewayRoute:
		return "aigateway.envoyproxy.io/v1beta1"
	case KindGateway:
		return "gateway.networking.k8s.io/v1"
	default:
		return ""
	}
}

func validResourceName(name string) bool {
	if name == "" || len(name) > 253 || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validatedBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.Path != "" {
		return "", errors.New("invalid Kubernetes API base URL")
	}
	return u.Scheme + "://" + u.Host, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	result, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil || len(result) > maxResponseBytes {
		return nil, errors.New("response body is invalid")
	}
	return result, nil
}

func discardBounded(reader io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, maxResponseBytes))
}

func decodeObject(body []byte, fallback Object) (Object, error) {
	if len(body) == 0 {
		return Object{}, errors.New("invalid Kubernetes response JSON")
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil || decoded == nil {
		return Object{}, errors.New("invalid Kubernetes response JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Object{}, errors.New("invalid Kubernetes response JSON")
	}
	if decoded["apiVersion"] != apiVersion(fallback.Kind) || decoded["kind"] != string(fallback.Kind) {
		return Object{}, errors.New("invalid Kubernetes response identity")
	}
	metadata, ok := decoded["metadata"].(map[string]any)
	if !ok || metadata["name"] != fallback.Name || metadata["namespace"] != fallback.Namespace {
		return Object{}, errors.New("invalid Kubernetes response identity")
	}
	generation, ok := metadata["generation"].(json.Number)
	if !ok {
		return Object{}, errors.New("invalid Kubernetes response generation")
	}
	generationValue, err := generation.Int64()
	if err != nil || generationValue <= 0 {
		return Object{}, errors.New("invalid Kubernetes response generation")
	}
	fallback.Body = decoded
	if status, ok := decoded["status"].(map[string]any); ok {
		fallback.Status = status
	}
	fallback.Generation = generationValue
	return fallback, nil
}

func kindName(path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "Kubernetes"
	}
	name := parts[len(parts)-1]
	for kind := range map[Kind]struct{}{KindBackend: {}, KindAIServiceBackend: {}, KindAIGatewayRoute: {}, KindGateway: {}} {
		if strings.Contains(path, "/"+strings.ToLower(string(kind))+"s/") {
			return string(kind) + "/" + name
		}
	}
	if strings.Contains(path, "/aiservicebackends/") {
		return string(KindAIServiceBackend) + "/" + name
	}
	if strings.Contains(path, "/aigatewayroutes/") {
		return string(KindAIGatewayRoute) + "/" + name
	}
	return "Kubernetes/" + name
}
