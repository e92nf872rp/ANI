package policyclient

import (
	"context"
	"testing"
	"time"

	inferencev1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/envoy-authz-adapter/internal/extauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeInferenceClient struct {
	inferencev1.InferenceControlClient
	request  *inferencev1.CheckInferenceAccessRequest
	err      error
	response *inferencev1.CheckInferenceAccessResponse
}

func (f *fakeInferenceClient) CheckInferenceAccess(_ context.Context, req *inferencev1.CheckInferenceAccessRequest, _ ...grpc.CallOption) (*inferencev1.CheckInferenceAccessResponse, error) {
	f.request = req
	return f.response, f.err
}

func TestCheckInferenceAccessMapsStructuredDecisionWithoutRawAPIKey(t *testing.T) {
	fake := &fakeInferenceClient{response: &inferencev1.CheckInferenceAccessResponse{HttpStatus: 429, InferenceServiceId: "service-a", LeaseId: "lease-a", RetryAfterSeconds: 7}}
	client := New(fake, time.Second)
	decision, err := client.CheckInferenceAccess(context.Background(), "tenant-a", "user-a", "key-a", "ani_test_sec", "ani-qwen3", "/v1/chat/completions", "request-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if decision != (extauth.AccessDecision{HTTPStatus: 429, InferenceServiceID: "service-a", LeaseID: "lease-a", RetryAfterSeconds: 7}) {
		t.Fatalf("decision = %#v", decision)
	}
	if got := fake.request; got.GetTenantId() != "tenant-a" || got.GetUserId() != "user-a" || got.GetApiKeyId() != "key-a" || got.GetKeyPrefix() != "ani_test_sec" || got.GetServedModelName() != "ani-qwen3" || got.GetOpenaiPath() != "/v1/chat/completions" || got.GetRequestId() != "request-a" || !got.GetStream() {
		t.Fatalf("request = %#v", got)
	}
}

func TestCheckInferenceAccessReturnsRPCFailure(t *testing.T) {
	client := New(&fakeInferenceClient{err: status.Error(codes.Unavailable, "unavailable")}, time.Second)
	if _, err := client.CheckInferenceAccess(context.Background(), "tenant-a", "user-a", "key-a", "ani_test_sec", "ani-qwen3", "/v1/chat/completions", "request-a", false); status.Code(err) != codes.Unavailable {
		t.Fatalf("error = %v", err)
	}
}
