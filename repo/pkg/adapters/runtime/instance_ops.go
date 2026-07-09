package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalInstanceOpsGuard struct {
	enabled     bool
	now         func() time.Time
	consoleBase string
	mu          sync.RWMutex
	consoleSess map[string]ports.WorkloadInstanceConsoleSession
}

type InstanceOpsOption func(*LocalInstanceOpsGuard)

func WithInstanceOpsEnabled(enabled bool) InstanceOpsOption {
	return func(ops *LocalInstanceOpsGuard) {
		ops.enabled = enabled
	}
}

func WithInstanceOpsClock(now func() time.Time) InstanceOpsOption {
	return func(ops *LocalInstanceOpsGuard) {
		if now != nil {
			ops.now = now
		}
	}
}

func WithInstanceOpsConsoleBaseURL(baseURL string) InstanceOpsOption {
	return func(ops *LocalInstanceOpsGuard) {
		ops.consoleBase = strings.TrimSpace(baseURL)
	}
}

func NewLocalInstanceOpsGuard(options ...InstanceOpsOption) *LocalInstanceOpsGuard {
	ops := &LocalInstanceOpsGuard{
		now:         time.Now,
		consoleSess: make(map[string]ports.WorkloadInstanceConsoleSession),
	}
	for _, option := range options {
		option(ops)
	}
	return ops
}

func (g *LocalInstanceOpsGuard) Run(_ context.Context, request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceOpsResult, error) {
	if err := validateOpsRequest(request, record); err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	if !g.enabled {
		return ports.WorkloadInstanceOpsResult{
			Action:    request.Action,
			Accepted:  false,
			Reason:    "instance ops are disabled by execution switch",
			CheckedAt: g.now().UTC(),
		}, nil
	}
	sessionID := opsSessionID(request)
	protocol := opsProtocol(request)
	connectURL := opsConnectURL(request, record, g.now().UTC())
	token := ""
	switch request.Action {
	case ports.WorkloadInstanceOpsVMConsole, ports.WorkloadInstanceOpsVMVNC, ports.WorkloadInstanceOpsVMSerial:
		session, err := g.issueConsoleSession(request, record)
		if err != nil {
			return ports.WorkloadInstanceOpsResult{}, err
		}
		sessionID = session.ID
		protocol = session.Protocol
		connectURL = session.ConnectURL
		token = session.Token
	}
	return ports.WorkloadInstanceOpsResult{
		Action:     request.Action,
		Accepted:   true,
		SessionID:  sessionID,
		Protocol:   protocol,
		ConnectURL: connectURL,
		URL:        connectURL,
		Token:      token,
		Reason:     "accepted by local instance ops guard",
		CheckedAt:  g.now().UTC(),
		ExpiresAt:  g.now().UTC().Add(15 * time.Minute),
	}, nil
}

func (g *LocalInstanceOpsGuard) GetConsoleSession(_ context.Context, request ports.WorkloadInstanceConsoleSessionGetRequest) (ports.WorkloadInstanceConsoleSession, error) {
	if strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: instance_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: session_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Token) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: token is required", ports.ErrUnauthorized)
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, record := range g.consoleSess {
		if record.ID != request.SessionID {
			continue
		}
		if request.TenantID != "" && record.TenantID != request.TenantID {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
		}
		if record.InstanceID != request.InstanceID {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
		}
		if record.Token != request.Token {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrUnauthorized
		}
		if !g.now().UTC().Before(record.ExpiresAt) {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrExpired
		}
		return record, nil
	}
	return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
}

func (g *LocalInstanceOpsGuard) issueConsoleSession(request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceConsoleSession, error) {
	token, err := newInstanceExecToken()
	if err != nil {
		return ports.WorkloadInstanceConsoleSession{}, err
	}
	sessionID := uuid.NewString()
	session := ports.WorkloadInstanceConsoleSession{
		ID:          sessionID,
		TenantID:    record.TenantID,
		InstanceID:  record.InstanceID,
		VMName:      firstNonEmpty(record.Name, record.InstanceID),
		Protocol:    opsProtocol(request),
		Subresource: consoleSubresource(request.Action),
		ConnectURL:  instanceConsoleWSURL(g.consoleBase, record.InstanceID, sessionID, token),
		Token:       token,
		ExpiresAt:   g.now().UTC().Add(15 * time.Minute),
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.consoleSess[sessionID] = session
	return session, nil
}

func validateOpsRequest(request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) error {
	if request.TenantID == "" || request.InstanceID == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required for instance ops", ports.ErrInvalid)
	}
	if request.UserID == "" || request.PermissionProof == "" {
		return fmt.Errorf("%w: user id and permission proof are required for instance ops", ports.ErrInvalid)
	}
	if request.TenantID != record.TenantID || request.InstanceID != record.InstanceID {
		return fmt.Errorf("%w: ops request does not match instance record", ports.ErrInvalid)
	}
	switch request.Action {
	case ports.WorkloadInstanceOpsLogs, ports.WorkloadInstanceOpsEvents, ports.WorkloadInstanceOpsMetrics:
		return nil
	case ports.WorkloadInstanceOpsTerminal, ports.WorkloadInstanceOpsExec:
		if record.Kind == ports.WorkloadKindVM {
			return fmt.Errorf("%w: terminal and exec ops are container-only", ports.ErrUnsupported)
		}
		if record.Status.State != ports.WorkloadStateRunning {
			return fmt.Errorf("%w: terminal and exec require running instance", ports.ErrConflict)
		}
		return nil
	case ports.WorkloadInstanceOpsVMConsole, ports.WorkloadInstanceOpsVMVNC, ports.WorkloadInstanceOpsVMSerial:
		if record.Kind != ports.WorkloadKindVM {
			return fmt.Errorf("%w: vm console ops require vm instance", ports.ErrUnsupported)
		}
		if record.Status.State != ports.WorkloadStateRunning {
			return fmt.Errorf("%w: vm console ops require running instance", ports.ErrConflict)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported instance ops action %q", ports.ErrUnsupported, request.Action)
	}
}

func opsSessionID(request ports.WorkloadInstanceOpsRequest) string {
	switch request.Action {
	case ports.WorkloadInstanceOpsTerminal, ports.WorkloadInstanceOpsExec,
		ports.WorkloadInstanceOpsVMConsole, ports.WorkloadInstanceOpsVMVNC, ports.WorkloadInstanceOpsVMSerial:
		return request.InstanceID + "/" + string(request.Action)
	default:
		return ""
	}
}

func opsProtocol(request ports.WorkloadInstanceOpsRequest) string {
	if request.Protocol != "" {
		return request.Protocol
	}
	switch request.Action {
	case ports.WorkloadInstanceOpsVMVNC:
		return "vnc"
	case ports.WorkloadInstanceOpsVMSerial:
		return "serial-console"
	case ports.WorkloadInstanceOpsVMConsole:
		return "console"
	case ports.WorkloadInstanceOpsTerminal:
		return "web-terminal"
	case ports.WorkloadInstanceOpsExec:
		return "exec"
	default:
		return ""
	}
}

func opsConnectURL(request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord, checkedAt time.Time) string {
	protocol := opsProtocol(request)
	if protocol == "" {
		return ""
	}
	return "/api/v1/demo/instances/" + record.InstanceID + "/sessions/" + string(request.Action) + "?protocol=" + protocol + "&issued_at=" + checkedAt.Format("20060102150405")
}

var (
	_ ports.WorkloadInstanceOps                = (*LocalInstanceOpsGuard)(nil)
	_ ports.WorkloadInstanceConsoleSessionStore = (*LocalInstanceOpsGuard)(nil)
)
