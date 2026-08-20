package coresdk

import (
	"context"
	"testing"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc"
)

type mintStub struct {
	token     string
	expiresIn int32
	err       error
	calls     int
	req       *authv1.IssueServiceTokenRequest
}

func (s *mintStub) IssueServiceToken(_ context.Context, in *authv1.IssueServiceTokenRequest, _ ...grpc.CallOption) (*authv1.AccessToken, error) {
	s.calls++
	s.req = in
	if s.err != nil {
		return nil, s.err
	}
	return &authv1.AccessToken{AccessToken: s.token, ExpiresIn: s.expiresIn}, nil
}

func TestMinterCachesUnexpiredToken(t *testing.T) {
	stub := &mintStub{token: "aaa.bbb.ccc", expiresIn: 300}
	minter, err := NewMinter(stub, "mint-secret")
	if err != nil {
		t.Fatalf("NewMinter: %v", err)
	}
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	first, err := minter.Token(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	second, err := minter.Token(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Token second: %v", err)
	}
	if first != second || stub.calls != 1 {
		t.Fatalf("calls = %d first=%q second=%q", stub.calls, first, second)
	}
	if stub.req.GetCallerService() != "inference-service" || stub.req.GetTenantId() != tenantID.String() {
		t.Fatalf("req = %+v", stub.req)
	}
}

func TestMinterRejectsNilTenant(t *testing.T) {
	minter, err := NewMinter(&mintStub{token: "aaa.bbb.ccc"}, "mint-secret")
	if err != nil {
		t.Fatalf("NewMinter: %v", err)
	}
	if _, err := minter.Token(context.Background(), uuid.Nil); err == nil {
		t.Fatal("expected tenant id error")
	}
}
