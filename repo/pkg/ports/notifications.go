package ports

import (
	"context"
	"time"
)

// EmailChannelState 表示平台 SMTP 通道的启停状态。
type EmailChannelState string

const (
	EmailChannelStateActive   EmailChannelState = "active"
	EmailChannelStateDisabled EmailChannelState = "disabled"
)

// EmailEncryption 表示 SMTP 通道的加密方式。
type EmailEncryption string

const (
	EmailEncryptionNone     EmailEncryption = "none"
	EmailEncryptionStartTLS EmailEncryption = "starttls"
	EmailEncryptionSSL      EmailEncryption = "ssl"
)

// EmailRecipientState 表示收件人记录的当前状态（软删除用 deleted）。
type EmailRecipientState string

const (
	EmailRecipientStateActive  EmailRecipientState = "active"
	EmailRecipientStateDeleted EmailRecipientState = "deleted"
)

// NotificationEventType 表示平台事件目录中的事件类型枚举（首期冻结）。
type NotificationEventType string

const (
	NotificationEventTypePlatformAlertP0            NotificationEventType = "platform_alert_p0"
	NotificationEventTypePlatformAlertP1            NotificationEventType = "platform_alert_p1"
	NotificationEventTypeIncidentCreated            NotificationEventType = "incident_created"
	NotificationEventTypeIncidentEscalated          NotificationEventType = "incident_escalated"
	NotificationEventTypePlatformCriticalTaskFailed NotificationEventType = "platform_critical_task_failed"
)

// NotificationEventCategory 表示事件目录条目的分类。
type NotificationEventCategory string

const (
	NotificationEventCategoryAlert    NotificationEventCategory = "alert"
	NotificationEventCategoryIncident NotificationEventCategory = "incident"
	NotificationEventCategoryTask     NotificationEventCategory = "task"
)

// NotificationEventSeverity 表示事件目录条目的严重程度。
type NotificationEventSeverity string

const (
	NotificationEventSeverityInfo     NotificationEventSeverity = "info"
	NotificationEventSeverityWarning  NotificationEventSeverity = "warning"
	NotificationEventSeverityCritical NotificationEventSeverity = "critical"
)

// EmailTestSendStatus 表示一次测试发送的入队结果。
type EmailTestSendStatus string

const (
	EmailTestSendStatusAccepted EmailTestSendStatus = "accepted"
	EmailTestSendStatusPartial  EmailTestSendStatus = "partial"
	EmailTestSendStatusFailed   EmailTestSendStatus = "failed"
)

// EmailChannel 表示平台级 SMTP 发信通道（单例）。
// 敏感字段（password、auth_code）不回显明文，仅通过 HasPassword / HasAuthCode 表示是否已配置。
type EmailChannel struct {
	ID             string
	Host           string
	Port           int
	Encryption     EmailEncryption
	Username       string
	HasPassword    bool
	HasAuthCode    bool
	FromAddress    string
	FromName       string
	ReplyTo        string
	State          EmailChannelState
	LastVerifiedAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EmailChannelUpdateRequest 用于创建或更新平台 SMTP 通道。
// Password 与 AuthCode 均为 write-only：创建时按需填写其一，更新时留空表示保留原值。
type EmailChannelUpdateRequest struct {
	IdempotencyKey string
	Host           string
	Port           int
	Encryption     EmailEncryption
	Username       string
	Password       string
	AuthCode       string
	FromAddress    string
	FromName       string
	ReplyTo        string
	State          EmailChannelState
}

// EmailRecipient 表示平台级收件邮箱（全局一份列表，所有已开启订阅的事件共用）。
type EmailRecipient struct {
	ID          string
	Email       string
	DisplayName string
	Enabled     bool
	State       EmailRecipientState
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EmailRecipientCreateRequest 用于新增平台邮件收件人。
type EmailRecipientCreateRequest struct {
	IdempotencyKey string
	Email          string
	DisplayName    string
	Enabled        *bool
}

// EmailRecipientUpdateRequest 用于部分更新收件人；字段为 nil 表示不修改。
type EmailRecipientUpdateRequest struct {
	IdempotencyKey string
	RecipientID    string
	Email          *string
	DisplayName    *string
	Enabled        *bool
}

// EmailRecipientGetRequest 用于获取单个收件人。
type EmailRecipientGetRequest struct {
	RecipientID string
}

// EmailRecipientDeleteRequest 用于软删除收件人。
type EmailRecipientDeleteRequest struct {
	RecipientID string
}

// EmailRecipientListRequest 用于查询收件人列表。
type EmailRecipientListRequest struct {
	Limit   int
	Cursor  string
	Enabled *bool
}

// EmailRecipientListResult 表示收件人列表查询结果。
type EmailRecipientListResult struct {
	Items      []EmailRecipient
	Total      int
	NextCursor string
}

// NotificationEvent 表示平台事件目录条目（首期冻结枚举，后续追加属于兼容性新增）。
type NotificationEvent struct {
	EventType NotificationEventType
	Name      string
	Summary   string
	Category  NotificationEventCategory
	Severity  NotificationEventSeverity
}

// NotificationEventListResult 表示平台事件目录列表。
type NotificationEventListResult struct {
	Items []NotificationEvent
	Total int
}

// EmailSubscription 表示按事件类型粒度的邮件订阅开关。
type EmailSubscription struct {
	EventType NotificationEventType
	Enabled   bool
	UpdatedAt time.Time
}

// EmailSubscriptionUpdate 是 EmailSubscriptionUpdateRequest.Items 中的单条更新项。
type EmailSubscriptionUpdate struct {
	EventType NotificationEventType
	Enabled   bool
}

// EmailSubscriptionUpdateRequest 用于批量更新邮件订阅开关；
// 服务端以 EventType 为 key 做 upsert，整个 items 数组原子替换当前设置。
type EmailSubscriptionUpdateRequest struct {
	IdempotencyKey string
	Items          []EmailSubscriptionUpdate
}

// EmailSubscriptionListResult 表示邮件订阅列表。
type EmailSubscriptionListResult struct {
	Items []EmailSubscription
	Total int
}

// EmailTestSendRequest 用于触发一次测试发送。
// 前置条件：通道已配置（HasPassword=true 或 HasAuthCode=true，State=active）且至少有一个 Enabled=true 的收件人。
type EmailTestSendRequest struct {
	IdempotencyKey string
	RecipientIDs   []string
	Subject        string
}

// EmailTestSendRejection 表示测试发送中被拒绝的单个收件人及原因。
type EmailTestSendRejection struct {
	RecipientID string
	Code        string
	Message     string
}

// EmailTestSendResponse 表示测试发送的入队结果；本端点不创建投递历史。
type EmailTestSendResponse struct {
	RequestID            string
	Status               EmailTestSendStatus
	AcceptedRecipientIDs []string
	RejectedRecipientIDs []EmailTestSendRejection
}

// NotificationService 抽象平台级邮件通知能力，包括 SMTP 通道、收件人、订阅与测试发送。
// 实现需保证：写操作尊重 IdempotencyKey；Password / AuthCode 不在响应中回显；删除为软删除。
type NotificationService interface {
	// GetEmailChannel 返回平台 SMTP 通道（单例）。未配置时返回 state=disabled + has_password=false，不返回 NotFound。
	GetEmailChannel(ctx context.Context) (EmailChannel, error)

	// UpdateEmailChannel 创建或更新平台 SMTP 通道；password / auth_code 为 write-only，留空保留原值。
	UpdateEmailChannel(ctx context.Context, req EmailChannelUpdateRequest) (EmailChannel, error)

	// ListEmailRecipients 列出平台邮件收件人，支持按 enabled 过滤与游标分页。
	ListEmailRecipients(ctx context.Context, req EmailRecipientListRequest) (EmailRecipientListResult, error)

	// CreateEmailRecipient 新增平台邮件收件人；email 唯一冲突时返回 ErrConflict。
	CreateEmailRecipient(ctx context.Context, req EmailRecipientCreateRequest) (EmailRecipient, error)

	// GetEmailRecipient 获取单个收件人；不存在时返回 ErrNotFound。
	GetEmailRecipient(ctx context.Context, req EmailRecipientGetRequest) (EmailRecipient, error)

	// UpdateEmailRecipient 部分更新收件人；字段为 nil 表示不修改。
	UpdateEmailRecipient(ctx context.Context, req EmailRecipientUpdateRequest) (EmailRecipient, error)

	// DeleteEmailRecipient 软删除收件人；关联订阅不受影响，但该收件人不再收到任何邮件。
	DeleteEmailRecipient(ctx context.Context, req EmailRecipientDeleteRequest) error

	// ListNotificationEvents 返回平台事件目录（首期冻结枚举）；客户端不应自造枚举。
	ListNotificationEvents(ctx context.Context) (NotificationEventListResult, error)

	// ListEmailSubscriptions 返回每个 event_type 的 enabled 状态；未显式设置的默认为 false。
	ListEmailSubscriptions(ctx context.Context) (EmailSubscriptionListResult, error)

	// UpdateEmailSubscriptions 批量更新邮件订阅开关；items 数组原子替换当前设置。
	// event_type 必须来自 ListNotificationEvents 返回的目录。
	UpdateEmailSubscriptions(ctx context.Context, req EmailSubscriptionUpdateRequest) (EmailSubscriptionListResult, error)

	// SendEmailTest 触发一次测试发送；前置条件不满足时返回 ErrFailedPrecondition。
	// 不创建投递历史，仅返回入队结果（accepted / partial / failed）。
	SendEmailTest(ctx context.Context, req EmailTestSendRequest) (EmailTestSendResponse, error)
}
