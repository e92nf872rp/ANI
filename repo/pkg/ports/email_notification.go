package ports

import (
	"context"
	"time"
)

// EmailEncryption 表示 SMTP 通道加密方式。adapter 按此分支构造 SMTP 客户端。
type EmailEncryption string

const (
	EmailEncryptionNone     EmailEncryption = "none"
	EmailEncryptionStartTLS EmailEncryption = "starttls"
	EmailEncryptionSSL      EmailEncryption = "ssl"
)

// NotificationEventType 表示平台事件类型枚举。订阅按此字段做 upsert。
type NotificationEventType string

const (
	NotificationEventPlatformAlertP0            NotificationEventType = "platform_alert_p0"
	NotificationEventPlatformAlertP1            NotificationEventType = "platform_alert_p1"
	NotificationEventIncidentCreated            NotificationEventType = "incident_created"
	NotificationEventIncidentEscalated          NotificationEventType = "incident_escalated"
	NotificationEventPlatformCriticalTaskFailed NotificationEventType = "platform_critical_task_failed"
)

// EmailChannel 表示平台级 SMTP 发信通道（单例，id 固定为 "platform-default"）。
// HasPassword / HasAuthCode 为布尔位，不回显明文。
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
	State          string
	LastVerifiedAt time.Time
	DevProfile     DevProfileInfo
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EmailChannelUpdateRequest 用于创建或更新 SMTP 通道。
// Password 与 AuthCode 均为 write-only，留空表示保留原值。
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
	State          string
}

// EmailRecipient 表示平台级收件邮箱（全局一份列表）。
type EmailRecipient struct {
	ID          string
	Email       string
	DisplayName string
	Enabled     bool
	State       string
	DevProfile  DevProfileInfo
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type EmailRecipientCreateRequest struct {
	IdempotencyKey string
	Email          string
	DisplayName    string
	Enabled        bool
}

// EmailRecipientUpdateRequest 用于部分更新；Enabled 为指针，nil 表示不改。
type EmailRecipientUpdateRequest struct {
	IdempotencyKey string
	Email          string
	DisplayName    string
	Enabled        *bool
}

type EmailRecipientListRequest struct {
	Limit   int
	Cursor  string
	Enabled *bool
}

type EmailRecipientListResult struct {
	Items      []EmailRecipient
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

// NotificationEvent 表示平台事件目录条目。Category 与 Severity 为纯展示属性。
type NotificationEvent struct {
	EventType   NotificationEventType
	Name        string
	Description string
	Category    string
	Severity    string
}

type NotificationEventListResult struct {
	Items []NotificationEvent
	Total int
}

// EmailSubscription 表示按事件类型粒度的订阅开关。
type EmailSubscription struct {
	EventType NotificationEventType
	Enabled   bool
	UpdatedAt time.Time
}

type EmailSubscriptionListResult struct {
	Items []EmailSubscription
	Total int
}

type EmailSubscriptionUpdateItem struct {
	EventType NotificationEventType
	Enabled   bool
}

type EmailSubscriptionUpdateRequest struct {
	IdempotencyKey string
	Items         []EmailSubscriptionUpdateItem
}

type EmailTestSendRequest struct {
	IdempotencyKey string
	RecipientIDs  []string
	Subject       string
}

type EmailTestSendRejectedRecipient struct {
	RecipientID string
	Code        string
	Message     string
}

type EmailTestSendResult struct {
	RequestID            string
	Status               string
	AcceptedRecipientIDs []string
	RejectedRecipientIDs []EmailTestSendRejectedRecipient
}

// EmailNotificationService 定义平台邮件通知能力边界。
type EmailNotificationService interface {
	GetEmailChannel(ctx context.Context) (EmailChannel, error)
	UpdateEmailChannel(ctx context.Context, req EmailChannelUpdateRequest) (EmailChannel, error)
	ListEmailRecipients(ctx context.Context, req EmailRecipientListRequest) (EmailRecipientListResult, error)
	CreateEmailRecipient(ctx context.Context, req EmailRecipientCreateRequest) (EmailRecipient, error)
	GetEmailRecipient(ctx context.Context, recipientID string) (EmailRecipient, error)
	UpdateEmailRecipient(ctx context.Context, recipientID string, req EmailRecipientUpdateRequest) (EmailRecipient, error)
	DeleteEmailRecipient(ctx context.Context, recipientID string) error
	ListNotificationEvents(ctx context.Context) (NotificationEventListResult, error)
	ListEmailSubscriptions(ctx context.Context) (EmailSubscriptionListResult, error)
	UpdateEmailSubscriptions(ctx context.Context, req EmailSubscriptionUpdateRequest) (EmailSubscriptionListResult, error)
	SendEmailTest(ctx context.Context, req EmailTestSendRequest) (EmailTestSendResult, error)
}
