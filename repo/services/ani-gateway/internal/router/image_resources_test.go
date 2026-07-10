package router

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestImageAPICreateUploadListGetDelete(t *testing.T) {
	api := newImageAPI()

	session, err := api.service.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := imageUploadSessionFromRecord(session)
	if resp.UploadURL == "" || resp.Token == "" {
		t.Fatalf("upload session response missing url/token: %+v", resp)
	}
	if resp.Image.State != string(ports.ImageStateUploading) {
		t.Fatalf("image state = %s, want uploading", resp.Image.State)
	}
	requireLocalCoreDevProfile(t, resp.Image.DevProfile, "local-image-import-service")

	listed, err := api.service.List(context.Background(), ports.ImageListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || listed.Items[0].ID != session.Image.ID {
		t.Fatalf("listed = %+v", listed)
	}

	got, err := api.service.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if imageFromRecord(got).ID != session.Image.ID {
		t.Fatalf("get id mismatch")
	}

	deleted, err := api.service.Delete(context.Background(), ports.ImageDeleteRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if imageFromRecord(deleted).State != string(ports.ImageStateDeleted) {
		t.Fatalf("deleted state = %s, want deleted", imageFromRecord(deleted).State)
	}
}

func TestImageAPIRejectsUnsupportedFormat(t *testing.T) {
	api := newImageAPI()
	_, err := api.service.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "img-1",
		Name:           "disk.qcow2",
		Format:         ports.ImageFormatQCOW2,
		SizeGiB:        10,
	})
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestImageAPIUsesInjectedService(t *testing.T) {
	service := &fakeImageImportService{
		record: ports.ImageRecord{ID: "img-injected", TenantID: "tenant-a", Name: "ubuntu-2204", Format: ports.ImageFormatISO, State: ports.ImageStateReady},
	}
	api := newImageAPIWithService(service)

	got, err := api.service.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: "img-injected"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !service.getCalled {
		t.Fatalf("injected service Get was not called")
	}
	if got.ID != "img-injected" {
		t.Fatalf("image id = %s, want injected record", got.ID)
	}
}

type fakeImageImportService struct {
	getCalled bool
	record    ports.ImageRecord
	session   ports.ImageUploadSession
}

func (s *fakeImageImportService) CreateUpload(context.Context, ports.ImageUploadCreateRequest) (ports.ImageUploadSession, error) {
	if s.session.Token != "" || s.session.UploadURL != "" {
		return s.session, nil
	}
	return ports.ImageUploadSession{}, ports.ErrUnsupported
}

func (s *fakeImageImportService) Get(_ context.Context, req ports.ImageGetRequest) (ports.ImageRecord, error) {
	s.getCalled = true
	s.record.TenantID = req.TenantID
	return s.record, nil
}

func (s *fakeImageImportService) List(context.Context, ports.ImageListRequest) (ports.ImageListResult, error) {
	return ports.ImageListResult{}, ports.ErrUnsupported
}

func (s *fakeImageImportService) Delete(context.Context, ports.ImageDeleteRequest) (ports.ImageRecord, error) {
	return ports.ImageRecord{}, ports.ErrUnsupported
}

var _ ports.ImageImportService = (*fakeImageImportService)(nil)
