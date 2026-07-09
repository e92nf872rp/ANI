package runtime

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const kubeVirtPlainProtocol = "plain.kubevirt.io"

type kubeVirtConsoleRequest struct {
	Client      *KubernetesRESTClient
	Namespace   string
	VMName      string
	Subresource string
}

type bufferedNetConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedNetConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (o *KubernetesInstanceOps) ConnectConsoleSession(ctx context.Context, session ports.WorkloadInstanceConsoleSession, browser io.ReadWriteCloser) error {
	if browser == nil {
		return fmt.Errorf("%w: browser websocket stream is required", ports.ErrInvalid)
	}
	if o.client == nil {
		return ports.ErrNotConfigured
	}
	if strings.TrimSpace(session.VMName) == "" {
		return fmt.Errorf("%w: console session vm name is required", ports.ErrInvalid)
	}
	subresource := strings.TrimSpace(session.Subresource)
	if subresource == "" {
		subresource = "vnc"
	}
	upstream, err := dialKubeVirtConsoleWebSocket(ctx, kubeVirtConsoleRequest{
		Client:      o.client,
		Namespace:   tenantNamespace(session.TenantID),
		VMName:      session.VMName,
		Subresource: subresource,
	})
	if err != nil {
		return err
	}
	defer upstream.Close()

	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(upstream, browser)
		errCh <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(browser, upstream)
		errCh <- copyErr
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && !isExpectedExecStreamClose(err) && firstErr == nil {
			firstErr = err
		}
		_ = browser.Close()
		_ = upstream.Close()
	}
	return firstErr
}

func dialKubeVirtConsoleWebSocket(ctx context.Context, request kubeVirtConsoleRequest) (io.ReadWriteCloser, error) {
	if request.Client == nil {
		return nil, ports.ErrNotConfigured
	}
	endpoint := request.Client.host + kubeVirtSubresourcePath(request.Namespace, request.VMName, request.Subresource)
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	address := u.Host
	if !strings.Contains(address, ":") {
		if u.Scheme == "https" || u.Scheme == "wss" {
			address += ":443"
		} else {
			address += ":80"
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" || u.Scheme == "wss" {
		tlsConn := tls.Client(rawConn, kubernetesExecTLSConfig(request.Client, u.Hostname()))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		rawConn = tlsConn
	}
	key, err := newWebSocketClientKey()
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	req.Host = u.Host
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Protocol", kubeVirtPlainProtocol)
	if request.Client.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+request.Client.bearerToken)
	}
	if err := req.Write(rawConn); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	reader := bufio.NewReader(rawConn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = rawConn.Close()
		return nil, fmt.Errorf("%w: KubeVirt %s websocket upgrade returned HTTP %d", ports.ErrInvalid, request.Subresource, resp.StatusCode)
	}
	if protocol := resp.Header.Get("Sec-WebSocket-Protocol"); protocol != "" && protocol != kubeVirtPlainProtocol {
		_ = rawConn.Close()
		return nil, fmt.Errorf("%w: KubeVirt websocket protocol %q is not supported", ports.ErrInvalid, protocol)
	}
	// Return raw websocket transport so browser noVNC frames can be proxied
	// byte-for-byte to KubeVirt without re-framing.
	return &bufferedNetConn{Conn: rawConn, reader: reader}, nil
}

var _ ports.WorkloadInstanceConsoleSessionConnector = (*KubernetesInstanceOps)(nil)
