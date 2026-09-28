package main

import (
	"net"
	"net/http"
	"time"

	"github.com/kubercloud/ani/services/ani-gateway/internal/router"
)

// newGatewayKaiwuProxyHTTPClient 创建开物代理专用 HTTP 客户端。它禁止自动
// 跟随重定向，以便 Gateway 能改写 DSH 的 303 Location 和 Set-Cookie。
func newGatewayKaiwuProxyHTTPClient() router.KaiwuProxyHTTPClient {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
