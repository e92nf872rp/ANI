package policyclient

import (
	"context"
	"time"

	inferencev1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/extauth"
)

type Client struct {
	rpc     inferencev1.InferenceControlClient
	timeout time.Duration
}

func New(rpc inferencev1.InferenceControlClient, timeout time.Duration) *Client {
	return &Client{rpc: rpc, timeout: timeout}
}
func (c *Client) CheckInferenceAccess(ctx context.Context, tenant, user, keyID, keyPrefix, model, path, requestID string, stream bool) (extauth.AccessDecision, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resp, err := c.rpc.CheckInferenceAccess(callCtx, &inferencev1.CheckInferenceAccessRequest{TenantId: tenant, UserId: user, ApiKeyId: keyID, KeyPrefix: keyPrefix, ServedModelName: model, OpenaiPath: path, RequestId: requestID, Stream: stream})
	if err != nil {
		return extauth.AccessDecision{}, err
	}
	return extauth.AccessDecision{
		HTTPStatus:         int(resp.GetHttpStatus()),
		InferenceServiceID: resp.GetInferenceServiceId(),
		LeaseID:            resp.GetLeaseId(),
		RetryAfterSeconds:  int(resp.GetRetryAfterSeconds()),
	}, nil
}
