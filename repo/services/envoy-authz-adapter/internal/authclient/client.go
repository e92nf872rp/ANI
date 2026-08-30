package authclient

import (
	"context"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
)

type Client struct {
	rpc     authv1.AuthServiceClient
	timeout time.Duration
}

func New(rpc authv1.AuthServiceClient, timeout time.Duration) *Client {
	return &Client{rpc: rpc, timeout: timeout}
}

func (c *Client) ValidateToken(ctx context.Context, token string) (*commonv1.TenantContext, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.rpc.ValidateToken(callCtx, &authv1.ValidateTokenRequest{Token: token})
}
