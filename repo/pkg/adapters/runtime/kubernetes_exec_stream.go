package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	kubernetesExecProtocol = "v4.channel.k8s.io"

	kubernetesExecStdinChannel  = 0
	kubernetesExecStdoutChannel = 1
	kubernetesExecStderrChannel = 2
	kubernetesExecResizeChannel = 4
)

type kubernetesExecDialer func(context.Context, kubernetesExecRequest) (io.ReadWriteCloser, error)

type kubernetesExecRequest struct {
	Client    *KubernetesRESTClient
	Namespace string
	PodName   string
	Query     url.Values
}

func (o *PrometheusInstanceObservability) ConnectExecSession(ctx context.Context, session ports.InstanceExecSessionRecord, stream ports.InstanceExecTerminalStream) error {
	if stream == nil {
		return fmt.Errorf("%w: exec terminal stream is required", ports.ErrInvalid)
	}
	if err := validateInstanceObservationIdentity(session.TenantID, session.InstanceID); err != nil {
		return err
	}
	podName, err := o.resolveObservationPodName(ctx, session.TenantID, session.InstanceID)
	if err != nil {
		return err
	}
	dialer := o.execDialer
	if dialer == nil {
		dialer = dialKubernetesExecWebSocket
	}
	conn, err := dialer(ctx, kubernetesExecRequest{
		Client:    o.kubeClient,
		Namespace: tenantNamespace(session.TenantID),
		PodName:   podName,
		Query:     kubernetesExecQuery(session),
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	outputDone := make(chan error, 1)
	go func() {
		outputDone <- forwardKubernetesExecOutput(ctx, conn, stream)
	}()
	inputErr := forwardKubernetesExecInput(ctx, conn, stream)
	_ = conn.Close()
	outputErr := <-outputDone
	if inputErr != nil && !isExpectedExecStreamClose(inputErr) {
		return inputErr
	}
	if outputErr != nil && !isExpectedExecStreamClose(outputErr) {
		return outputErr
	}
	return nil
}

func kubernetesExecQuery(session ports.InstanceExecSessionRecord) url.Values {
	values := url.Values{}
	if strings.TrimSpace(session.Container) != "" {
		values.Set("container", strings.TrimSpace(session.Container))
	}
	command := normalizeKubernetesExecCommand(session.Command, session.TTY)
	for _, arg := range command {
		values.Add("command", arg)
	}
	values.Set("stdin", "true")
	values.Set("stdout", "true")
	values.Set("stderr", "true")
	values.Set("tty", strconv.FormatBool(session.TTY))
	return values
}

func normalizeKubernetesExecCommand(command []string, tty bool) []string {
	cleaned := make([]string, 0, len(command))
	for _, part := range command {
		if strings.TrimSpace(part) != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 && tty {
		return []string{"/bin/sh"}
	}
	if len(cleaned) == 0 {
		return []string{"/bin/sh", "-c", "true"}
	}
	return cleaned
}

func forwardKubernetesExecInput(ctx context.Context, conn io.Writer, stream ports.InstanceExecTerminalStream) error {
	for {
		msg, err := stream.Recv(ctx)
		if err != nil {
			return err
		}
		switch msg.Op {
		case "stdin":
			if len(msg.Data) == 0 {
				continue
			}
			if _, err := conn.Write(append([]byte{kubernetesExecStdinChannel}, msg.Data...)); err != nil {
				return err
			}
		case "resize":
			if msg.Cols <= 0 || msg.Rows <= 0 {
				continue
			}
			payload, err := json.Marshal(struct {
				Width  int `json:"Width"`
				Height int `json:"Height"`
			}{Width: msg.Cols, Height: msg.Rows})
			if err != nil {
				return err
			}
			if _, err := conn.Write(append([]byte{kubernetesExecResizeChannel}, payload...)); err != nil {
				return err
			}
		}
	}
}

func forwardKubernetesExecOutput(ctx context.Context, conn io.Reader, stream ports.InstanceExecTerminalStream) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 1 {
			channel := buf[0]
			switch channel {
			case kubernetesExecStdoutChannel, kubernetesExecStderrChannel:
				if sendErr := stream.Send(ctx, ports.InstanceExecTerminalServerMessage{Op: "stdout", Data: append([]byte(nil), buf[1:n]...)}); sendErr != nil {
					return sendErr
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func isExpectedExecStreamClose(err error) bool {
	return err == nil || err == io.EOF || errorsIsNetClosed(err)
}

func errorsIsNetClosed(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}

type kubernetesExecWebSocketConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	pending []byte
}

func dialKubernetesExecWebSocket(ctx context.Context, request kubernetesExecRequest) (io.ReadWriteCloser, error) {
	if request.Client == nil {
		return nil, ports.ErrNotConfigured
	}
	endpoint := request.Client.host + podPath(request.Namespace, request.PodName) + "/exec?" + request.Query.Encode()
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
	req.Header.Set("Sec-WebSocket-Protocol", kubernetesExecProtocol)
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
		return nil, fmt.Errorf("%w: Kubernetes exec websocket upgrade returned HTTP %d", ports.ErrInvalid, resp.StatusCode)
	}
	if protocol := resp.Header.Get("Sec-WebSocket-Protocol"); protocol != "" && protocol != kubernetesExecProtocol {
		_ = rawConn.Close()
		return nil, fmt.Errorf("%w: Kubernetes exec websocket protocol %q is not supported", ports.ErrInvalid, protocol)
	}
	return &kubernetesExecWebSocketConn{conn: rawConn, reader: reader}, nil
}

func kubernetesExecTLSConfig(client *KubernetesRESTClient, serverName string) *tls.Config {
	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if client == nil || client.httpClient == nil {
		return config
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		return config
	}
	config = transport.TLSClientConfig.Clone()
	config.ServerName = serverName
	if config.MinVersion == 0 {
		config.MinVersion = tls.VersionTLS12
	}
	config.NextProtos = []string{"http/1.1"}
	return config
}

func newWebSocketClientKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

func (c *kubernetesExecWebSocketConn) Read(payload []byte) (int, error) {
	if len(c.pending) > 0 {
		n := copy(payload, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	for {
		opcode, frame, err := readKubernetesWebSocketFrame(c.reader)
		if err != nil {
			return 0, err
		}
		switch opcode {
		case 1, 2:
			n := copy(payload, frame)
			if n < len(frame) {
				c.pending = append(c.pending, frame[n:]...)
			}
			return n, nil
		case 8:
			return 0, io.EOF
		case 9:
			_ = writeKubernetesWebSocketClientFrame(c.conn, 10, frame)
		}
	}
}

func (c *kubernetesExecWebSocketConn) Write(payload []byte) (int, error) {
	if err := writeKubernetesWebSocketClientFrame(c.conn, 2, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *kubernetesExecWebSocketConn) Close() error {
	return c.conn.Close()
}

func readKubernetesWebSocketFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = uint64(extended[0])<<8 | uint64(extended[1])
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = 0
		for _, b := range extended {
			length = length<<8 | uint64(b)
		}
	}
	if length > 1<<20 {
		return 0, nil, fmt.Errorf("%w: Kubernetes exec websocket frame too large", ports.ErrInvalid)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func writeKubernetesWebSocketClientFrame(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 65535:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		for shift := 56; shift >= 0; shift -= 8 {
			header = append(header, byte(uint64(length)>>shift))
		}
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(masked)
	return err
}
