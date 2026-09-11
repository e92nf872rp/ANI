package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// mintCaller 是本服务在 auth-service IssueServiceToken 的调用方名。
	mintCaller = "tenant-service"
	// mintPermissions 是签名 token 携带的权威权限集合：
	// /admin/tenants*（V2 policy，resource=tenants）+ /admin/quota-meta（V2，resource=quota）
	// + 其余 legacy /admin/* 路径（roles=[platform-admin] 放行，不读 permissions）。
	mintPermissions = "scope:tenants:*,scope:quota:read"
	mintTTLSeconds = 300
	// mintRefreshWindow 是 token 剩余寿命低于该窗口时提前刷新。
	mintRefreshWindow = 30 * time.Second
)

type serviceTokenAPI interface {
	IssueServiceToken(context.Context, *authv1.IssueServiceTokenRequest, ...grpc.CallOption) (*authv1.AccessToken, error)
}

// Minter 向 auth-service 换取访问 Core /admin/* 的平台管理面 JWT。
// tenant-service 调用面全部是平台边界（无租户上下文），缓存单 token 即可，
// 无需 inference-service 的 per-tenant map。
type Minter struct {
	client    serviceTokenAPI
	secret    string
	now       func() time.Time
	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// NewMinter 构造带缓存的 token minter。
func NewMinter(client serviceTokenAPI, secret string) (*Minter, error) {
	if client == nil {
		return nil, fmt.Errorf("auth-service client is required")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("auth mint secret is required")
	}
	return &Minter{client: client, secret: secret, now: time.Now}, nil
}

// DialMinter 连接 auth-service gRPC 并构造 Minter。
func DialMinter(addr, secret string) (*Minter, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("auth-service gRPC address is empty")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial auth-service %s: %w", addr, err)
	}
	return NewMinter(authv1.NewAuthServiceClient(conn), secret)
}

// Token 返回访问 Core /admin/* 的平台管理面 JWT；过期前 mintRefreshWindow 内提前刷新。
func (m *Minter) Token(ctx context.Context) (string, error) {
	now := m.now()
	m.mu.Lock()
	if m.cached != "" && now.Add(mintRefreshWindow).Before(m.expiresAt) {
		token := m.cached
		m.mu.Unlock()
		return token, nil
	}
	m.mu.Unlock()

	issued, err := m.client.IssueServiceToken(ctx, &authv1.IssueServiceTokenRequest{
		CallerService:    mintCaller,
		CallerSecret:    m.secret,
		CredentialDomain: "platform",
		Permissions:      strings.Split(mintPermissions, ","),
		TtlSeconds:      mintTTLSeconds,
	})
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(issued.GetAccessToken())
	if token == "" {
		return "", fmt.Errorf("auth-service returned an empty service token")
	}
	ttl := time.Duration(issued.GetExpiresIn()) * time.Second
	if ttl <= 0 {
		ttl = time.Duration(mintTTLSeconds) * time.Second
	}
	m.mu.Lock()
	m.cached = token
	m.expiresAt = now.Add(ttl)
	m.mu.Unlock()
	return token, nil
}
