package ports

import (
	"context"
	"time"
)

type ImageRef struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

type ImageTag struct {
	Name   string
	Digest string
}

type ImageScanStatus struct {
	Status     string
	Critical   int
	High       int
	Medium     int
	Low        int
	ReportURL  string
	ProviderID string
}

type RegistryPermissionAction string

const (
	RegistryPermissionPull   RegistryPermissionAction = "pull"
	RegistryPermissionPush   RegistryPermissionAction = "push"
	RegistryPermissionDelete RegistryPermissionAction = "delete"
	RegistryPermissionScan   RegistryPermissionAction = "scan"
)

type RegistryPermissionState string

const (
	RegistryPermissionActive    RegistryPermissionState = "active"
	RegistryPermissionDuplicate RegistryPermissionState = "duplicate"
)

type RegistryScanState string

const (
	RegistryScanNotScanned RegistryScanState = "not_scanned"
	RegistryScanPending    RegistryScanState = "pending"
	RegistryScanRunning    RegistryScanState = "running"
	RegistryScanComplete   RegistryScanState = "complete"
	RegistryScanFailed     RegistryScanState = "failed"
)

type RegistryProjectListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
}

type RegistryProjectRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	Public         bool
}

type RegistryProject struct {
	ID         string
	TenantID   string
	Name       string
	Public     bool
	DevProfile DevProfileInfo
	CreatedAt  time.Time
}

type RegistryProjectListResult struct {
	Items      []RegistryProject
	NextCursor string
	DevProfile DevProfileInfo
}

type RegistryRepositoryListRequest struct {
	TenantID string
	Project  string
	Limit    int
	Cursor   string
}

type RegistryRepository struct {
	Project       string
	Name          string
	ArtifactCount int
	PullCount     int
	Permission    *RegistryPermission
	DevProfile    DevProfileInfo
}

type RegistryRepositoryListResult struct {
	Items      []RegistryRepository
	NextCursor string
	DevProfile DevProfileInfo
}

type RegistryArtifactListRequest struct {
	TenantID   string
	Project    string
	Repository string
	Limit      int
	Cursor     string
}

type RegistryArtifact struct {
	Project    string
	Repository string
	Digest     string
	Tags       []string
	MediaType  string
	SizeBytes  int64
	PushedAt   time.Time
	ScanStatus RegistryScanResult
	DevProfile DevProfileInfo
}

type RegistryArtifactListResult struct {
	Items      []RegistryArtifact
	NextCursor string
	DevProfile DevProfileInfo
}

type RegistryPermissionRequest struct {
	TenantID       string
	Project        string
	Repository     string
	IdempotencyKey string
	Subject        string
	Actions        []RegistryPermissionAction
}

type RegistryPermission struct {
	Project    string
	Repository string
	Subject    string
	Actions    []RegistryPermissionAction
	State      RegistryPermissionState
	DevProfile DevProfileInfo
	UpdatedAt  time.Time
}

type RegistryScanResultRequest struct {
	TenantID string
	Image    string
}

type RegistryScanResult struct {
	Image      string
	Status     RegistryScanState
	Critical   int
	High       int
	Medium     int
	Low        int
	ReportURL  string
	ProviderID string
	DevProfile DevProfileInfo
	ScannedAt  time.Time
}

type RegistryPullSecretRequest struct {
	TenantID       string
	Project        string
	IdempotencyKey string
	Name           string
	Namespace      string
}

type RegistryPullSecret struct {
	Project    string
	Name       string
	SecretRef  string
	Registry   string
	Username   string
	Namespace  string
	State      RegistryPermissionState
	DevProfile DevProfileInfo
	CreatedAt  time.Time
}

type RegistryProjectScanReportRequest struct {
	TenantID string
	Project  string
}

type RegistryProjectScanReport struct {
	Project          string
	Status           RegistryScanState
	Critical         int
	High             int
	Medium           int
	Low              int
	ArtifactsTotal   int
	ScannedArtifacts int
	ProviderID       string
	DevProfile       DevProfileInfo
	ScannedAt        time.Time
}

type RegistryOverviewRequest struct {
	TenantID string
}

type RegistryOverviewResourceSummary struct {
	Kind      string
	Total     int
	Available int
	Pending   int
	Failed    int
	SizeBytes int64
}

type RegistryOverviewVulnerabilitySummary struct {
	Critical int
	High     int
	Medium   int
	Low      int
}

type RegistryOverviewCapability struct {
	Key         string
	Label       string
	Status      string
	Path        string
	Description string
}

type RegistryOverviewRelationship struct {
	Source   string
	Target   string
	Relation string
}

type RegistryOverviewQuickAction struct {
	Key         string
	Label       string
	Path        string
	Description string
}

type RegistryOverviewDeleteRisk struct {
	Kind string
	Risk string
}

type RegistryOverview struct {
	Resources       []RegistryOverviewResourceSummary
	Vulnerabilities RegistryOverviewVulnerabilitySummary
	Capabilities    []RegistryOverviewCapability
	CreateOrder     []string
	Relationships   []RegistryOverviewRelationship
	QuickActions    []RegistryOverviewQuickAction
	DeleteRisks     []RegistryOverviewDeleteRisk
}

type RegistryImageListRequest struct {
	TenantID   string
	Project    string
	Repository string
	Tag        string
	ScanStatus RegistryScanState
	Limit      int
	Cursor     string
}

type RegistryImage struct {
	Project     string
	Repository  string
	Tag         string
	Image       string
	Registry    string
	Digest      string
	MediaType   string
	SizeBytes   int64
	PullCommand string
	PushedAt    time.Time
	ScanStatus  RegistryScanResult
	DevProfile  DevProfileInfo
}

type RegistryImageListResult struct {
	Items      []RegistryImage
	NextCursor string
	DevProfile DevProfileInfo
}

type RegistryCommand struct {
	Label   string
	Command string
}

type RegistryPushInstructionsRequest struct {
	TenantID   string
	Project    string
	Repository string
}

type RegistryPushInstructions struct {
	Project           string
	Registry          string
	RepositoryExample string
	Commands          []RegistryCommand
	DevProfile        DevProfileInfo
}

type RegistryTagDeleteRequest struct {
	TenantID   string
	Project    string
	Repository string
	Tag        string
}

type RegistryDeletedTag struct {
	Project    string
	Repository string
	Tag        string
	Digest     string
	DeletedAt  time.Time
}

type RegistryImageReferenceListRequest struct {
	TenantID   string
	Project    string
	Repository string
	Tag        string
}

type RegistryImageReference struct {
	Kind       string
	ID         string
	Name       string
	Route      string
	State      string
	DevProfile DevProfileInfo
}

type RegistryImageReferenceListResult struct {
	Project       string
	Repository    string
	Tag           string
	Image         string
	Items         []RegistryImageReference
	DeleteBlocked bool
	DevProfile    DevProfileInfo
}

type RegistryPullSecretWriteRequest struct {
	TenantID         string
	Namespace        string
	Name             string
	Registry         string
	Username         string
	DockerConfigJSON string
}

type RegistryPullSecretWriter interface {
	ApplyRegistryPullSecret(ctx context.Context, request RegistryPullSecretWriteRequest) error
}

type RegistryImageReferenceReader interface {
	ListRegistryImageReferences(ctx context.Context, request RegistryImageReferenceListRequest) (RegistryImageReferenceListResult, error)
}

type ImageRegistry interface {
	EnsureProject(ctx context.Context, tenantID string) error
	ListTags(ctx context.Context, repository string) ([]ImageTag, error)
	GetScanStatus(ctx context.Context, ref ImageRef) (ImageScanStatus, error)
	CreateProject(ctx context.Context, request RegistryProjectRequest) (RegistryProject, error)
	ListProjects(ctx context.Context, request RegistryProjectListRequest) (RegistryProjectListResult, error)
	ListRepositories(ctx context.Context, request RegistryRepositoryListRequest) (RegistryRepositoryListResult, error)
	ListArtifacts(ctx context.Context, request RegistryArtifactListRequest) (RegistryArtifactListResult, error)
	SetRepositoryPermission(ctx context.Context, request RegistryPermissionRequest) (RegistryPermission, error)
	GetScanResult(ctx context.Context, request RegistryScanResultRequest) (RegistryScanResult, error)
	CreatePullSecret(ctx context.Context, request RegistryPullSecretRequest) (RegistryPullSecret, error)
	GetProjectScanReport(ctx context.Context, request RegistryProjectScanReportRequest) (RegistryProjectScanReport, error)
	GetOverview(ctx context.Context, request RegistryOverviewRequest) (RegistryOverview, error)
	ListImages(ctx context.Context, request RegistryImageListRequest) (RegistryImageListResult, error)
	GetPushInstructions(ctx context.Context, request RegistryPushInstructionsRequest) (RegistryPushInstructions, error)
	DeleteTag(ctx context.Context, request RegistryTagDeleteRequest) (RegistryDeletedTag, error)
	ListTagReferences(ctx context.Context, request RegistryImageReferenceListRequest) (RegistryImageReferenceListResult, error)
}
