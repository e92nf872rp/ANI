package coresdk

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func kubeProxyURL(endpoint, path string) (string, error) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_API_HOST"))
	if host == "" {
		return "", fmt.Errorf("KUBERNETES_API_HOST is required for kubernetes_proxy probes")
	}
	namespace, name, port, err := parseClusterService(endpoint)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(host, "https://") && !strings.HasPrefix(host, "http://") {
		host = "https://" + host
	}
	base, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil {
		return "", err
	}
	base.Path = fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%d/proxy%s", namespace, name, port, path)
	base.RawQuery = ""
	base.Fragment = ""
	return base.String(), nil
}

func parseClusterService(endpoint string) (namespace, name string, port int, err error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil {
		return "", "", 0, fmt.Errorf("untrusted runtime endpoint")
	}
	host := parsed.Hostname()
	port = 8000
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return "", "", 0, fmt.Errorf("untrusted runtime endpoint")
		}
	}
	host = strings.TrimSuffix(host, ".cluster.local")
	host = strings.TrimSuffix(host, ".svc")
	parts := strings.Split(host, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, fmt.Errorf("runtime endpoint is not a cluster service DNS")
	}
	return strings.Join(parts[1:], "."), parts[0], port, nil
}

func kubeHTTPClient() *http.Client {
	if strings.TrimSpace(os.Getenv("INFERENCE_RUNTIME_PROBE_VIA")) != "kubernetes_proxy" {
		return nil
	}
	token := strings.TrimSpace(os.Getenv("KUBERNETES_BEARER_TOKEN"))
	caFile := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE"))
	if token == "" || caFile == "" {
		return nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil
	}
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &bearerRoundTripper{
			token: token,
			base:  &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
	}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}
