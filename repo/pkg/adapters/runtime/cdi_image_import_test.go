package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type cdiFakeCreateCall struct {
	APIGroupVersion string
	Resource        string
	Namespace       string
	Body            map[string]any
}

type fakeCDIRESTClient struct {
	createCalls []cdiFakeCreateCall
	dataVolumes map[string]map[string]any
	deleted     []string
	getErr      error
	tokenValue  string
}

func newFakeCDIRESTClient() *fakeCDIRESTClient {
	return &fakeCDIRESTClient{dataVolumes: map[string]map[string]any{}, tokenValue: "tok-fake"}
}

func fakeCDIKey(namespace, name string) string { return namespace + "/" + name }

func (f *fakeCDIRESTClient) CreateNamespacedResource(_ context.Context, apiGroupVersion, resource, namespace string, body map[string]any) (map[string]any, error) {
	f.createCalls = append(f.createCalls, cdiFakeCreateCall{apiGroupVersion, resource, namespace, body})
	switch resource {
	case "datavolumes":
		metadata, _ := body["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		metadata["creationTimestamp"] = "2026-07-09T00:00:00Z"
		created := map[string]any{
			"apiVersion": body["apiVersion"],
			"kind":       body["kind"],
			"metadata":   metadata,
			"spec":       body["spec"],
			"status":     map[string]any{"phase": "Pending"},
		}
		f.dataVolumes[fakeCDIKey(namespace, name)] = created
		return created, nil
	case "uploadtokenrequests":
		return map[string]any{"status": map[string]any{"token": f.tokenValue}}, nil
	default:
		return nil, errors.New("fake CDI client: unsupported resource " + resource)
	}
}

func (f *fakeCDIRESTClient) GetNamespacedResource(_ context.Context, _ string, _ string, namespace, name string) (map[string]any, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	doc, ok := f.dataVolumes[fakeCDIKey(namespace, name)]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return doc, nil
}

func (f *fakeCDIRESTClient) ListNamespacedResource(_ context.Context, _ string, _ string, namespace, _ string) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(f.dataVolumes))
	for key, doc := range f.dataVolumes {
		if key[:len(namespace)] == namespace {
			items = append(items, doc)
		}
	}
	return items, nil
}

func (f *fakeCDIRESTClient) DeleteNamespacedResource(_ context.Context, _ string, _ string, namespace, name string) error {
	key := fakeCDIKey(namespace, name)
	if _, ok := f.dataVolumes[key]; !ok {
		return ports.ErrNotFound
	}
	delete(f.dataVolumes, key)
	f.deleted = append(f.deleted, key)
	return nil
}

var _ CDIRESTClient = (*fakeCDIRESTClient)(nil)

func fixedCDIClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC) }
}

func TestCDIImageImportCreateUploadCreatesDataVolumeAndUploadToken(t *testing.T) {
	fake := newFakeCDIRESTClient()
	svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001", WithCDIImageImportClock(fixedCDIClock()))

	session, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(fake.createCalls) != 2 {
		t.Fatalf("createCalls = %d, want 2 (DataVolume + UploadTokenRequest)", len(fake.createCalls))
	}

	dvCall := fake.createCalls[0]
	if dvCall.Resource != "datavolumes" {
		t.Fatalf("first create resource = %s, want datavolumes", dvCall.Resource)
	}
	spec, _ := dvCall.Body["spec"].(map[string]any)
	source, _ := spec["source"].(map[string]any)
	if _, ok := source["upload"]; !ok {
		t.Fatalf("spec.source = %+v, want upload source", source)
	}
	storage, _ := spec["storage"].(map[string]any)
	if storage["storageClassName"] != "ani-rbd-ssd" {
		t.Fatalf("storageClassName = %v, want ani-rbd-ssd", storage["storageClassName"])
	}
	metadata, _ := dvCall.Body["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations["cdi.kubevirt.io/storage.bind.immediate.requested"] != "true" {
		t.Fatalf("annotations = %+v, want immediate bind annotation", annotations)
	}

	tokenCall := fake.createCalls[1]
	if tokenCall.Resource != "uploadtokenrequests" {
		t.Fatalf("second create resource = %s, want uploadtokenrequests", tokenCall.Resource)
	}
	tokenSpec, _ := tokenCall.Body["spec"].(map[string]any)
	if tokenSpec["pvcName"] != metadata["name"] {
		t.Fatalf("uploadtokenrequest pvcName = %v, want DataVolume name %v", tokenSpec["pvcName"], metadata["name"])
	}

	if session.UploadURL != "https://cdi-uploadproxy.example:31001/v1beta1/upload" {
		t.Fatalf("upload_url = %s, want base+path", session.UploadURL)
	}
	if session.Token != "tok-fake" {
		t.Fatalf("token = %s, want tok-fake", session.Token)
	}
	if session.Image.State != ports.ImageStatePending {
		t.Fatalf("image state = %s, want pending", session.Image.State)
	}
	if session.Image.ID == "" {
		t.Fatal("image id must not be empty")
	}
}

func TestCDIImageImportCreateUploadIsIdempotent(t *testing.T) {
	fake := newFakeCDIRESTClient()
	svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001")
	req := ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	}

	first, err := svc.CreateUpload(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUpload(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Image.ID != second.Image.ID {
		t.Fatalf("image id mismatch: first=%s second=%s", first.Image.ID, second.Image.ID)
	}

	dvCreateCalls := 0
	for _, call := range fake.createCalls {
		if call.Resource == "datavolumes" {
			dvCreateCalls++
		}
	}
	if dvCreateCalls != 1 {
		t.Fatalf("datavolume create calls = %d, want 1 (idempotent replay must not recreate the DataVolume)", dvCreateCalls)
	}
}

func TestCDIImageImportRejectsNonISOFormats(t *testing.T) {
	fake := newFakeCDIRESTClient()
	svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001")

	_, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "disk.qcow2",
		Format:         ports.ImageFormatQCOW2,
		SizeGiB:        10,
	})
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if len(fake.createCalls) != 0 {
		t.Fatalf("createCalls = %d, want 0 for unsupported format", len(fake.createCalls))
	}
}

func TestCDIImageImportGetMapsDataVolumePhaseToImageState(t *testing.T) {
	cases := []struct {
		phase string
		want  ports.ImageState
	}{
		{"", ports.ImageStatePending},
		{"WaitForFirstConsumer", ports.ImageStatePending},
		{"UploadScheduled", ports.ImageStateUploading},
		{"UploadReady", ports.ImageStateUploading},
		{"ImportInProgress", ports.ImageStateProcessing},
		{"Succeeded", ports.ImageStateReady},
		{"Failed", ports.ImageStateFailed},
	}
	for _, tc := range cases {
		fake := newFakeCDIRESTClient()
		fake.dataVolumes[fakeCDIKey("ani-tenant-tenant-a", "img-fixed")] = map[string]any{
			"metadata": map[string]any{
				"name":              "img-fixed",
				"namespace":         "ani-tenant-tenant-a",
				"creationTimestamp": "2026-07-09T00:00:00Z",
				"annotations":       map[string]any{cdiImageNameAnnotation: "ubuntu-2204"},
			},
			"spec": map[string]any{
				"storage": map[string]any{
					"storageClassName": "ani-rbd-ssd",
					"resources":        map[string]any{"requests": map[string]any{"storage": "5Gi"}},
				},
			},
			"status": map[string]any{"phase": tc.phase},
		}
		svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001")
		record, err := svc.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: "img-fixed"})
		if err != nil {
			t.Fatalf("phase=%q err=%v", tc.phase, err)
		}
		if record.State != tc.want {
			t.Fatalf("phase=%q state=%s, want %s", tc.phase, record.State, tc.want)
		}
		if record.Name != "ubuntu-2204" {
			t.Fatalf("phase=%q name=%s, want ubuntu-2204", tc.phase, record.Name)
		}
		if record.SizeGiB != 5 {
			t.Fatalf("phase=%q size_gib=%d, want 5", tc.phase, record.SizeGiB)
		}
		if record.StorageClass != "ani-rbd-ssd" {
			t.Fatalf("phase=%q storage_class=%s, want ani-rbd-ssd", tc.phase, record.StorageClass)
		}
	}
}

func TestCDIImageImportGetNotFound(t *testing.T) {
	fake := newFakeCDIRESTClient()
	svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001")
	_, err := svc.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: "missing"})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCDIImageImportListAndDelete(t *testing.T) {
	fake := newFakeCDIRESTClient()
	svc := NewCDIImageImportService(fake, "https://cdi-uploadproxy.example:31001")

	session, err := svc.CreateUpload(context.Background(), ports.ImageUploadCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "iso-1",
		Name:           "ubuntu-2204",
		Format:         ports.ImageFormatISO,
		SizeGiB:        5,
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := svc.List(context.Background(), ports.ImageListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 || len(listed.Items) != 1 || listed.Items[0].ID != session.Image.ID {
		t.Fatalf("listed = %+v", listed)
	}

	deleted, err := svc.Delete(context.Background(), ports.ImageDeleteRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.State != ports.ImageStateDeleting {
		t.Fatalf("deleted state = %s, want deleting", deleted.State)
	}

	_, err = svc.Get(context.Background(), ports.ImageGetRequest{TenantID: "tenant-a", ImageID: session.Image.ID})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after delete", err)
	}
}
