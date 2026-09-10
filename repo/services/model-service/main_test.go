package main

import (
	"context"
	"testing"

	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicModelServiceServerRejectsDownloadURL(t *testing.T) {
	server := publicModelServiceServer{ModelServiceServer: modelv1.UnimplementedModelServiceServer{}}
	_, err := server.GetModelDownloadURL(context.Background(), &modelv1.GetModelDownloadURLRequest{
		TenantId:       "00000000-0000-0000-0000-000000000001",
		ModelVersionId: "00000000-0000-0000-0000-000000000002",
		Requester:      "init-container",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetModelDownloadURL() code = %s, want %s", status.Code(err), codes.PermissionDenied)
	}
}

type recordingPublicModelServiceDelegate struct {
	modelv1.UnimplementedModelServiceServer
	called bool
}

func (d *recordingPublicModelServiceDelegate) GetModelVersion(context.Context, *modelv1.GetModelVersionRequest) (*modelv1.GetModelVersionResponse, error) {
	d.called = true
	return &modelv1.GetModelVersionResponse{}, nil
}

func TestPublicModelServiceServerDelegatesNormalControlPlaneMethods(t *testing.T) {
	delegate := &recordingPublicModelServiceDelegate{}
	server := publicModelServiceServer{ModelServiceServer: delegate}
	if _, err := server.GetModelVersion(context.Background(), &modelv1.GetModelVersionRequest{TenantId: "tenant-a", ModelVersionId: "version-a"}); err != nil {
		t.Fatalf("GetModelVersion() error = %v", err)
	}
	if !delegate.called {
		t.Fatal("GetModelVersion() did not delegate through the normal 9103 wrapper")
	}
}
