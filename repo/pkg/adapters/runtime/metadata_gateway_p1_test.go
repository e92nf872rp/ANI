package runtime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const gatewayP1TenantID = "00000000-0000-0000-0000-000000000001"

func TestMetadataBrandingServiceQueriesPlatformBranding(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	tx := &fakeMetadataTx{
		row: brandingFakeRow{
			platformName:   "KuberCloud ANI",
			primaryColor:   "#1677FF",
			secondaryColor: "#13C2C2",
			updatedAt:      now,
		},
	}
	service := NewMetadataBrandingService(fakeMetadataStore{tx: tx})
	record, err := service.GetBranding(context.Background())
	if err != nil {
		t.Fatalf("GetBranding() error = %v", err)
	}
	if record.PlatformName != "KuberCloud ANI" {
		t.Fatalf("platform_name = %q, want KuberCloud ANI", record.PlatformName)
	}
	if !strings.Contains(tx.queryRowSQL, "FROM platform_branding") {
		t.Fatalf("query = %q, want platform_branding select", tx.queryRowSQL)
	}
}

func TestMetadataBrandingServiceUpdatesPlatformBranding(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	tx := &fakeMetadataTx{
		row: brandingFakeRow{
			platformName:   "KuberCloud ANI",
			primaryColor:   "#1677FF",
			secondaryColor: "#13C2C2",
			updatedAt:      now,
		},
	}
	service := NewMetadataBrandingService(fakeMetadataStore{tx: tx})
	record, err := service.UpdateBranding(context.Background(), ports.BrandingUpdateRequest{
		IdempotencyKey: "branding-update-a",
		PlatformName:   "ANI Dev Platform",
		PrimaryColor:   "#FF5500",
		SecondaryColor: "#00AA88",
	})
	if err != nil {
		t.Fatalf("UpdateBranding() error = %v", err)
	}
	if record.PlatformName != "ANI Dev Platform" {
		t.Fatalf("platform_name = %q, want ANI Dev Platform", record.PlatformName)
	}
	if !strings.Contains(tx.sql, "UPDATE platform_branding") {
		t.Fatalf("sql = %q, want platform_branding update", tx.sql)
	}
}

func TestMetadataAsyncTaskServiceGetTaskUsesTenantScopedQuery(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	tx := &fakeMetadataTx{
		row: asyncTaskFakeRow{
			id:             "11111111-1111-1111-1111-111111111111",
			tenantID:       gatewayP1TenantID,
			idempotencyKey: "task-demo",
			taskType:       "model.import",
			status:         "pending",
			maxAttempts:    3,
			createdAt:      now,
		},
	}
	service := NewMetadataAsyncTaskService(fakeMetadataStore{tx: tx})
	record, err := service.GetTask(context.Background(), gatewayP1TenantID, "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if record.Status != "pending" {
		t.Fatalf("status = %q, want pending", record.Status)
	}
	if !strings.Contains(tx.queryRowSQL, "FROM async_tasks") {
		t.Fatalf("query = %q, want async_tasks select", tx.queryRowSQL)
	}
}

func TestMetadataAsyncTaskServiceCancelTaskUpdatesStatus(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	tx := &fakeMetadataTx{
		row: asyncTaskFakeRow{
			id:             "11111111-1111-1111-1111-111111111111",
			tenantID:       gatewayP1TenantID,
			idempotencyKey: "task-demo",
			taskType:       "model.import",
			status:         "cancelled",
			maxAttempts:    3,
			createdAt:      now,
			completedAt:    &now,
		},
	}
	service := NewMetadataAsyncTaskService(fakeMetadataStore{tx: tx})
	record, err := service.CancelTask(context.Background(), gatewayP1TenantID, "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	if record.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", record.Status)
	}
	if !strings.Contains(tx.queryRowSQL, "UPDATE async_tasks") {
		t.Fatalf("query = %q, want async_tasks update", tx.queryRowSQL)
	}
}

func TestMetadataMeteringServiceReportTokenUsageWritesReportAndMeteringRows(t *testing.T) {
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{err: sql.ErrNoRows},
	}
	service := NewMetadataMeteringService(fakeMetadataStore{tx: tx}, WithMetadataMeteringClock(func() time.Time {
		return time.Unix(300, 0).UTC()
	}))
	report, err := service.ReportTokenUsage(context.Background(), ports.TokenUsageReportRequest{
		TenantID:       gatewayP1TenantID,
		IdempotencyKey: "meter-demo",
		Source:         "inference",
		Model:          "demo-model",
		InputTokens:    10,
		OutputTokens:   5,
	})
	if err != nil {
		t.Fatalf("ReportTokenUsage() error = %v", err)
	}
	if report.State != ports.TokenUsageReportAccepted {
		t.Fatalf("state = %q, want accepted", report.State)
	}
	if len(tx.execs) < 4 {
		t.Fatalf("exec count = %d, want token report plus metering inserts", len(tx.execs))
	}
	if !strings.Contains(strings.Join(tx.execs, "\n"), "INSERT INTO metering_token_reports") {
		t.Fatalf("execs = %#v, want metering_token_reports insert", tx.execs)
	}
}

func TestMetadataMeteringServiceReportTokenUsageReturnsDuplicate(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	tx := &fakeMetadataTx{
		row: tokenReportFakeRow{
			reportID:     "meter_existing",
			tenantID:     gatewayP1TenantID,
			source:       "inference",
			model:        "demo-model",
			inputTokens:  10,
			outputTokens: 5,
			createdAt:    now,
		},
	}
	service := NewMetadataMeteringService(fakeMetadataStore{tx: tx})
	report, err := service.ReportTokenUsage(context.Background(), ports.TokenUsageReportRequest{
		TenantID:       gatewayP1TenantID,
		IdempotencyKey: "meter-demo",
		Source:         "inference",
		Model:          "demo-model",
		InputTokens:    10,
		OutputTokens:   5,
	})
	if err != nil {
		t.Fatalf("ReportTokenUsage() error = %v", err)
	}
	if report.State != ports.TokenUsageReportDuplicate {
		t.Fatalf("state = %q, want duplicate", report.State)
	}
}

func TestMetadataVectorStoreMetadataStoreUpsertsVectorStore(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataVectorStoreMetadataStore(fakeMetadataStore{tx: tx})
	now := time.Unix(400, 0).UTC()
	record := ports.VectorStoreRecord{
		TenantID:  gatewayP1TenantID,
		StoreID:   "vst_demo",
		Name:      "demo-store",
		Dimension: 8,
		Metric:    "cosine",
		State:     ports.VectorStoreReady,
		Reason:    "test",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.UpsertVectorStore(context.Background(), record, "idem-demo"); err != nil {
		t.Fatalf("UpsertVectorStore() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO vector_stores") {
		t.Fatalf("sql = %q, want vector_stores upsert", tx.sql)
	}
}

func TestMetadataVectorStoreMetadataStoreGetVectorStoreNotFound(t *testing.T) {
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{err: errors.New("no rows")},
	}
	store := NewMetadataVectorStoreMetadataStore(fakeMetadataStore{tx: tx})
	_, err := store.GetVectorStore(context.Background(), gatewayP1TenantID, "missing")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetVectorStore() error = %v, want ErrNotFound", err)
	}
}

func TestLocalVectorStoreServicePersistsCreateToMetadataStore(t *testing.T) {
	tx := &fakeMetadataTx{}
	metadataStore := NewMetadataVectorStoreMetadataStore(fakeMetadataStore{tx: tx})
	service := NewLocalVectorStoreService(WithVectorStoreMetadataStore(metadataStore))
	_, err := service.CreateVectorStore(context.Background(), ports.VectorStoreCreateRequest{
		TenantID:       gatewayP1TenantID,
		IdempotencyKey: "vector-demo",
		Name:           "demo-store",
		Dimension:      4,
		Metric:         "cosine",
	})
	if err != nil {
		t.Fatalf("CreateVectorStore() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO vector_stores") {
		t.Fatalf("sql = %q, want vector_stores upsert", tx.sql)
	}
}

type brandingFakeRow struct {
	platformName   string
	logoLightURL   string
	logoDarkURL    string
	faviconURL     string
	primaryColor   string
	secondaryColor string
	loginBgURL     string
	icpNumber      string
	updatedAt      time.Time
}

func (r brandingFakeRow) Scan(dest ...any) error {
	*dest[0].(*string) = r.platformName
	*dest[1].(*string) = r.logoLightURL
	*dest[2].(*string) = r.logoDarkURL
	*dest[3].(*string) = r.faviconURL
	*dest[4].(*string) = r.primaryColor
	*dest[5].(*string) = r.secondaryColor
	dest[6].(*sql.NullString).String = r.loginBgURL
	dest[7].(*sql.NullString).String = r.icpNumber
	*dest[8].(*time.Time) = r.updatedAt
	return nil
}

type asyncTaskFakeRow struct {
	id             string
	tenantID       string
	idempotencyKey string
	taskType       string
	resourceType   string
	resourceID     string
	status         string
	attemptCount   int
	maxAttempts    int
	progressPct    int
	result         []byte
	errorMessage   string
	deadLetterAt   *time.Time
	createdAt      time.Time
	startedAt      *time.Time
	completedAt    *time.Time
}

func (r asyncTaskFakeRow) Scan(dest ...any) error {
	*dest[0].(*string) = r.id
	*dest[1].(*string) = r.tenantID
	*dest[2].(*string) = r.idempotencyKey
	*dest[3].(*string) = r.taskType
	*dest[4].(*string) = r.resourceType
	*dest[5].(*string) = r.resourceID
	*dest[6].(*string) = r.status
	*dest[7].(*int) = r.attemptCount
	*dest[8].(*int) = r.maxAttempts
	*dest[9].(*int) = r.progressPct
	*dest[10].(*[]byte) = r.result
	*dest[11].(*string) = r.errorMessage
	*dest[12].(**time.Time) = r.deadLetterAt
	*dest[13].(*time.Time) = r.createdAt
	*dest[14].(**time.Time) = r.startedAt
	*dest[15].(**time.Time) = r.completedAt
	return nil
}

type tokenReportFakeRow struct {
	reportID     string
	tenantID     string
	source       string
	model        string
	inputTokens  int64
	outputTokens int64
	requestID    string
	instanceID   string
	createdAt    time.Time
}

func (r tokenReportFakeRow) Scan(dest ...any) error {
	*dest[0].(*string) = r.reportID
	*dest[1].(*string) = r.tenantID
	*dest[2].(*string) = r.source
	*dest[3].(*string) = r.model
	*dest[4].(*int64) = r.inputTokens
	*dest[5].(*int64) = r.outputTokens
	*dest[6].(*string) = r.requestID
	*dest[7].(*string) = r.instanceID
	*dest[8].(*time.Time) = r.createdAt
	return nil
}
