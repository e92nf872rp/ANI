package router

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

const defaultCDIUploadPath = "/v1beta1/upload"

// proxyImageUpload streams a browser ISO upload to the in-cluster CDI
// uploadproxy. The path is auth-public: the CDI UploadTokenRequest bearer
// token in Authorization is the credential. This avoids browsers talking to
// the NodePort HTTPS endpoint whose certificate SANs are cluster-internal
// DNS names only (cdi-uploadproxy.cdi.svc).
func (api *imageAPI) proxyImageUpload(ctx context.Context, c *app.RequestContext) {
	if string(c.Method()) == http.MethodOptions {
		writeImageUploadCORS(c)
		c.Status(http.StatusNoContent)
		return
	}
	writeImageUploadCORS(c)

	upstream := strings.TrimRight(strings.TrimSpace(api.uploadProxyURL), "/")
	if upstream == "" {
		upstream = strings.TrimRight(strings.TrimSpace(os.Getenv("CDI_UPLOADPROXY_URL")), "/")
	}
	if upstream == "" {
		writeDemoError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "CDI upload proxy is not configured")
		return
	}

	auth := strings.TrimSpace(string(c.GetHeader("Authorization")))
	if auth == "" {
		writeDemoError(c, http.StatusUnauthorized, "UNAUTHORIZED", "upload token is required")
		return
	}

	var body io.Reader
	if raw := c.Request.Body(); len(raw) > 0 {
		body = bytes.NewReader(raw)
	} else if stream := c.RequestBodyStream(); stream != nil {
		body = stream
	} else {
		body = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream+defaultCDIUploadPath, body)
	if err != nil {
		writeDemoError(c, http.StatusBadGateway, "UPLOAD_PROXY_FAILED", err.Error())
		return
	}
	req.Header.Set("Authorization", auth)
	if ct := string(c.GetHeader("Content-Type")); ct != "" {
		req.Header.Set("Content-Type", ct)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	// Only forward Content-Length when the body is a live stream; buffered
	// bodies let net/http derive length from the reader.
	if _, ok := body.(*bytes.Reader); !ok {
		if cl := string(c.GetHeader("Content-Length")); cl != "" {
			req.ContentLength = parseContentLength(cl)
		}
	}

	client := api.uploadHTTPClient
	if client == nil {
		client = defaultCDIUploadHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		writeDemoError(c, http.StatusBadGateway, "UPLOAD_PROXY_FAILED", err.Error())
		return
	}
	defer resp.Body.Close()

	for _, key := range []string{"Content-Type", "Content-Length"} {
		if v := resp.Header.Get(key); v != "" {
			c.Response.Header.Set(key, v)
		}
	}
	c.SetStatusCode(resp.StatusCode)
	if _, err := io.Copy(c, resp.Body); err != nil {
		return
	}
}

func writeImageUploadCORS(c *app.RequestContext) {
	c.Response.Header.Set("Access-Control-Allow-Origin", "*")
	c.Response.Header.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	c.Response.Header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Content-Length")
	c.Response.Header.Set("Access-Control-Max-Age", "86400")
}

func defaultCDIUploadHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Hour,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				// CDI uploadproxy serves a short-lived cluster CA cert whose
				// SANs are service DNS names, not the NodePort IP.
				InsecureSkipVerify: true, //nolint:gosec
			},
			ForceAttemptHTTP2: false,
		},
	}
}

func parseContentLength(raw string) int64 {
	var n int64
	_, _ = fmt.Sscan(strings.TrimSpace(raw), &n)
	return n
}

func gatewayPublicUploadProxyURL(c *app.RequestContext, publicBase string) string {
	base := strings.TrimRight(strings.TrimSpace(publicBase), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("INSTANCE_CONSOLE_BASE_URL")), "/")
	}
	if base == "" {
		scheme := "http"
		if proto := string(c.GetHeader("X-Forwarded-Proto")); proto != "" {
			scheme = proto
		}
		host := string(c.Host())
		if host == "" {
			host = "127.0.0.1:30080"
		}
		base = scheme + "://" + host
	}
	return base + "/api/v1/images/upload-proxy"
}
