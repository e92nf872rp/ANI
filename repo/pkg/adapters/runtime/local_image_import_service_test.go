package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalImageImportCreateUploadISO(t *testing.T) {
	svc := NewLocalImageImportService(WithImageImportUploadBaseURL("https://upload.example"))
	session, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "img-1", Name: "ubuntu.iso", Format: ports.ImageFormatISO, SizeGiB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Image.State != ports.ImageStateUploading {
		t.Fatalf("state=%s", session.Image.State)
	}
	if session.UploadURL == "" || session.Token == "" {
		t.Fatal("missing upload credentials")
	}
	got, err := svc.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != session.Image.ID {
		t.Fatal(got.ID)
	}
}

func TestLocalImageImportCreateUploadIsIdempotent(t *testing.T) {
	svc := NewLocalImageImportService(WithImageImportUploadBaseURL("https://upload.example"))
	req := ports.ImageUploadCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "img-1", Name: "ubuntu.iso", Format: ports.ImageFormatISO, SizeGiB: 5,
	}

	first, err := svc.CreateUpload(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUpload(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Image.ID != first.Image.ID || second.UploadURL != first.UploadURL || second.Token != first.Token {
		t.Fatalf("idempotency mismatch: first=%+v second=%+v", first, second)
	}
}

func TestLocalImageImportRejectsNonISOFormats(t *testing.T) {
	svc := NewLocalImageImportService()

	_, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "img-1", Name: "disk.qcow2", Format: ports.ImageFormatQCOW2, SizeGiB: 10,
	})
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestLocalImageImportListAndDelete(t *testing.T) {
	svc := NewLocalImageImportService()
	session, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID: "tenant-a", IdempotencyKey: "img-1", Name: "ubuntu.iso", Format: ports.ImageFormatISO, SizeGiB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := svc.List(context.Background(), ports.ImageListRequest{TenantID: "tenant-a", Format: ports.ImageFormatISO, State: ports.ImageStateUploading})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != session.Image.ID {
		t.Fatalf("listed=%+v", listed)
	}

	deleted, err := svc.Delete(context.Background(), ports.ImageDeleteRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.State != ports.ImageStateDeleted {
		t.Fatalf("state=%s", deleted.State)
	}
	_, err = svc.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}
