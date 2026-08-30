package authclient

import (
	"context"
	"testing"
	"time"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeAuthClient struct {
	authv1.AuthServiceClient
	validateToken func(context.Context, *authv1.ValidateTokenRequest) (*commonv1.TenantContext, error)
}

func (f *fakeAuthClient) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*commonv1.TenantContext, error) {
	return f.validateToken(ctx, req)
}

func TestValidateTokenUsesBoundedContext(t *testing.T) {
	fake := &fakeAuthClient{
		validateToken: func(ctx context.Context, _ *authv1.ValidateTokenRequest) (*commonv1.TenantContext, error) {
			<-ctx.Done()
			return nil, status.Error(codes.DeadlineExceeded, "deadline exceeded")
		},
	}

	client := New(fake, 10*time.Millisecond)
	_, err := client.ValidateToken(context.Background(), "ani_dev_tenant_secret")
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("got %v", err)
	}
}

func TestValidateTokenPassesRawTokenOnce(t *testing.T) {
	const token = "  ani_dev_tenant_secret/with?symbols  "
	var gotToken string
	var calls int
	fake := &fakeAuthClient{
		validateToken: func(_ context.Context, req *authv1.ValidateTokenRequest) (*commonv1.TenantContext, error) {
			calls++
			gotToken = req.GetToken()
			return &commonv1.TenantContext{TenantId: "tenant-1"}, nil
		},
	}

	client := New(fake, time.Second)
	got, err := client.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetTenantId() != "tenant-1" {
		t.Fatalf("unexpected tenant context: %+v", got)
	}
	if calls != 1 {
		t.Fatalf("ValidateToken called %d times, want 1", calls)
	}
	if gotToken != token {
		t.Fatalf("token changed from %q to %q", token, gotToken)
	}
}
