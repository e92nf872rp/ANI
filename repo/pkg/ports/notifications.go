package ports

import (
	"context"
	"time"
)

// EmailChannelEncryption 表示 SMTP 通道的加密方式。
type EmailChannelEncryption string

const (
	EmailChannelEncryptionNone     EmailChannelEncryption = "none"
	EmailChannelEncryptionStartTLS EmailChannelEncryption = "starttls"
	EmailChannelEncryptionSSL      EmailChannelEncryption = "ssl"
)

// EmailChannelState 表示 SMTP 通道的启用状态。
type EmailChannelState string

const (
	EmailChannelActive   EmailChannelState = "active"
	EmailChannelDisabled EmailChannelState = "disabled"
)

// EmailRecipientState 表示收件人的状态。
type EmailRecipientState string

const (
	EmailRecipientActive  EmailRecipientState = "active"
	EmailRecipientDeleted EmailRecipientState = "deleted"
)

// NotificationEventType 表示平台事件类型枚举（首期冻结）。
type NotificationEventType string

const (
	NotificationEventPlatformAlertP0            NotificationEventType = "platform_alert_p0"
	NotificationEventPlatformAlertP1            NotificationEventType = "platform_alert_p1"
	NotificationEventIncidentCreated            NotificationEventType = "incident_created"
	NotificationEventIncidentEscalated          NotificationEventType = "incident_escalated"
	NotificationEventPlatformCriticalTaskFailed NotificationEventType = "platform_critical_task_failed"
)

// NotificationEventCategory 表示平台事件分类。
type NotificationEventCategory string

const (
	NotificationEventCategoryAlert    NotificationEventCategory = "alert"
	NotificationEventCategoryIncident NotificationEventCategory = "incident"
	NotificationEventCategoryTask     NotificationEventCategory = "task"
)

// NotificationEventSeverity 表示事件严重级别。
type NotificationEventSeverity string

const (
	NotificationEventSeverityInfo     NotificationEventSeverity = "info"
	NotificationEventSeverityWarning  NotificationEventSeverity = "warning"
	NotificationEventSeverityCritical NotificationEventSeverity = "critical"
)

// EmailTestSendStatus 表示测试发送的入队状态。
type EmailTestSendStatus string

const (
	EmailTestSendAccepted EmailTestSendStatus = "accepted"
	EmailTestSendPartial  EmailTestSendStatus = "partial"
	EmailTestSendFailed   EmailTestSendStatus = "failed"
)

// EmailChannelRecord 表示平台级 SMTP 通道记录。
// HasPassword 表示服务端是否已持有密码；明文从不回显。
type EmailChannelRecord struct {
	ID             string
	Host           string
	Port           int
	Encryption     EmailChannelEncryption
	Username       string
	HasPassword    bool
	FromAddress    string
	FromName       string
	ReplyTo        string
	State          EmailChannelState
	LastVerifiedAt time.Time
	DevProfile     DevProfileInfo
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EmailChannelUpdateRequest 创建或更新平台 SMTP 通道。
// Password 为 write-only：更新时留空表示保留原值，非空则覆盖。
type EmailChannelUpdateRequest struct {
	TenantID       string
	IdempotencyKey string
	Host           string
	Port           int
	Encryption     EmailChannelEncryption
	Username       string
	Password       string
	FromAddress    string
	FromName       string
	ReplyTo        string
	State          EmailChannelState
}

// EmailRecipientRecord 表示平台级收件人记录。
type EmailRecipientRecord struct {
	ID          string
	Email       string
	DisplayName string
	Enabled     bool
	State       EmailRecipientState
	DevProfile  DevProfileInfo
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EmailRecipientCreateRequest 新增收件人请求。
type EmailRecipientCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Email          string
	DisplayName    string
	Enabled        bool
}

// EmailRecipientUpdateRequest 更新收件人请求。
type EmailRecipientUpdateRequest struct {
	TenantID       string
	RecipientID    string
	IdempotencyKey string
	Email          string
	DisplayName    string
	Enabled        *bool
}

// EmailRecipientGetRequest 获取或删除收件人请求。
type EmailRecipientGetRequest struct {
	TenantID    string
	RecipientID string
}

// EmailRecipientListRequest 列出收件人请求。
type EmailRecipientListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
	Enabled  *bool
}

// NotificationEventItem 平台事件目录条目。
type NotificationEventItem struct {
	EventType   NotificationEventType
	Name        string
	Description string
	Category    NotificationEventCategory
	Severity    NotificationEventSeverity
}

// EmailSubscriptionRecord 邮件订阅开关记录。
type EmailSubscriptionRecord struct {
	EventType NotificationEventType
	Enabled   bool
	UpdatedAt time.Time
}

// EmailSubscriptionUpdateItem 批量更新订阅的单条条目。
type EmailSubscriptionUpdateItem struct {
	EventType NotificationEventType
	Enabled   bool
}

// EmailSubscriptionUpdateRequest 批量更新订阅请求。
type EmailSubscriptionUpdateRequest struct {
	TenantID       string
	IdempotencyKey string
	Items          []EmailSubscriptionUpdateItem
}

// EmailTestSendRequest 测试发送请求。
// RecipientIDs 为空表示使用全部 enabled=true 的收件人。
type EmailTestSendRequest struct {
	TenantID       string
	IdempotencyKey string
	RecipientIDs   []string
	Subject        string
}

// EmailTestSendRejection 表示被拒绝的收件人及其原因。
type EmailTestSendRejection struct {
	RecipientID string
	Code        string
	Message     string
}

// EmailTestSendResult 测试发送结果。
type EmailTestSendResult struct {
	RequestID            string
	Status               EmailTestSendStatus
	AcceptedRecipientIDs []string
	RejectedRecipients   []EmailTestSendRejection
}

// NotificationService 平台邮件通知能力 port。
// 实现方必须保证：
//   - 所有写操作识别 IdempotencyKey，重复提交返回同一记录
//   - Password 字段在持久化层不可回显明文，响应只暴露 HasPassword
//   - DevProfile 字段必须返回，local adapter 固定 Mode="local"、RealProvider=false
type NotificationService interface {
	GetEmailChannel(ctx context.Context, tenantID string) (EmailChannelRecord, error)
	UpdateEmailChannel(ctx context.Context, req EmailChannelUpdateRequest) (EmailChannelRecord, error)

	ListEmailRecipients(ctx context.Context, req EmailRecipientListRequest) ([]EmailRecipientRecord, string, error)
	CreateEmailRecipient(ctx context.Context, req EmailRecipientCreateRequest) (EmailRecipientRecord, error)
	GetEmailRecipient(ctx context.Context, req EmailRecipientGetRequest) (EmailRecipientRecord, error)
	UpdateEmailRecipient(ctx context.Context, req EmailRecipientUpdateRequest) (EmailRecipientRecord, error)
	DeleteEmailRecipient(ctx context.Context, req EmailRecipientGetRequest) error

	ListNotificationEvents(ctx context.Context) ([]NotificationEventItem, error)

	ListEmailSubscriptions(ctx context.Context, tenantID string) ([]EmailSubscriptionRecord, error)
	UpdateEmailSubscriptions(ctx context.Context, req EmailSubscriptionUpdateRequest) ([]EmailSubscriptionRecord, error)

	SendEmailTest(ctx context.Context, req EmailTestSendRequest) (EmailTestSendResult, error)
}
