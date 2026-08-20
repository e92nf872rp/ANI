package coresdk

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	mintCaller        = "inference-service"
	mintScope         = "scope:platform-workloads:write"
	mintTTLSeconds    = 300
	mintRefreshWindow = 30 * time.Second
)

type serviceTokenAPI interface {
	IssueServiceToken(context.Context, *authv1.IssueServiceTokenRequest, ...grpc.CallOption) (*authv1.AccessToken, error)
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

type Minter struct {
	client serviceTokenAPI
	secret string
	now    func() time.Time
	mu     sync.Mutex
	cache  map[uuid.UUID]cachedToken
}

// NewMinter 按租户向 auth-service 换 Core service JWT，带 30s 刷新窗口。
func NewMinter(client serviceTokenAPI, secret string) (*Minter, error) {
	if client == nil {
		return nil, fmt.Errorf("auth-service client is required")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("auth mint secret is required")
	}
	return &Minter{client: client, secret: secret, now: time.Now, cache: map[uuid.UUID]cachedToken{}}, nil
}

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

// Token 返回该租户访问 platform-workloads 的 service JWT。
func (m *Minter) Token(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if tenantID == uuid.Nil {
		return "", fmt.Errorf("service token tenant id is required")
	}
	now := m.now()
	m.mu.Lock()
	if cached, ok := m.cache[tenantID]; ok && now.Add(mintRefreshWindow).Before(cached.expiresAt) {
		token := cached.token
		m.mu.Unlock()
		return token, nil
	}
	m.mu.Unlock()

	issued, err := m.client.IssueServiceToken(ctx, &authv1.IssueServiceTokenRequest{
		CallerService: mintCaller,
		CallerSecret:  m.secret,
		TenantId:      tenantID.String(),
		Scope:         mintScope,
		TtlSeconds:    mintTTLSeconds,
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
	m.cache[tenantID] = cachedToken{token: token, expiresAt: now.Add(ttl)}
	m.mu.Unlock()
	return token, nil
}
