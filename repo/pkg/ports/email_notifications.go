package ports

import (
	"context"
	"time"
)

// EmailNotificationService is the port for platform-level email notification
// configuration (SMTP, recipients, subscriptions, test send).
type EmailNotificationService interface {
	GetSmtpConfig(ctx context.Context) (EmailSmtpConfigRecord, error)
	PutSmtpConfig(ctx context.Context, req EmailSmtpConfigPutRequest) (EmailSmtpConfigRecord, error)
	ListRecipients(ctx context.Context) ([]EmailRecipientRecord, error)
	CreateRecipient(ctx context.Context, req EmailRecipientCreateRequest) (EmailRecipientRecord, error)
	UpdateRecipient(ctx context.Context, req EmailRecipientUpdateRequest) (EmailRecipientRecord, error)
	DeleteRecipient(ctx context.Context, req EmailRecipientDeleteRequest) error
	ListSubscriptions(ctx context.Context) ([]EmailSubscriptionRecord, error)
	PutSubscriptions(ctx context.Context, req EmailSubscriptionsPutRequest) ([]EmailSubscriptionRecord, error)
	ListEvents(ctx context.Context) ([]EmailEventInfoRecord, error)
	SendTestEmail(ctx context.Context, req EmailTestSendRequest) (EmailTestSendResult, error)
}

// EmailSmtpConfigRecord is the stored SMTP configuration.
type EmailSmtpConfigRecord struct {
	SmtpHost          string
	SmtpPort          int
	Encryption        string
	FromAddress       string
	Username          string
	Password          string
	AuthCode          string // SMTP 授权码（QQ邮箱/163邮箱等）
	PasswordConfigured bool  // 是否已配置密码（不回显明文）
	AuthCodeConfigured bool  // 是否已配置授权码（不回显明文）
	Configured        bool
}

// EmailSmtpConfigPutRequest is the request to save SMTP config.
type EmailSmtpConfigPutRequest struct {
	IdempotencyKey string
	SmtpHost       string
	SmtpPort       int
	Encryption     string
	FromAddress    string
	Username       string
	Password       string
	AuthCode       string // SMTP 授权码
}

// EmailRecipientRecord is a stored recipient.
type EmailRecipientRecord struct {
	ID        string
	Email     string
	Label     string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EmailRecipientCreateRequest is the request to add a recipient.
type EmailRecipientCreateRequest struct {
	IdempotencyKey string
	Email          string
	Label          string
}

// EmailRecipientUpdateRequest is the request to update a recipient.
type EmailRecipientUpdateRequest struct {
	IdempotencyKey string
	ID             string
	Email          string
	Label          string
	Enabled        *bool
}

// EmailRecipientDeleteRequest is the request to delete a recipient.
type EmailRecipientDeleteRequest struct {
	ID string
}

// EmailSubscriptionRecord is a stored event subscription.
type EmailSubscriptionRecord struct {
	EventType string
	Enabled   bool
}

// EmailSubscriptionsPutRequest is the request to batch-save subscriptions.
type EmailSubscriptionsPutRequest struct {
	IdempotencyKey string
	Subscriptions  []EmailSubscriptionRecord
}

// EmailEventInfoRecord describes an event type.
type EmailEventInfoRecord struct {
	EventType    string
	DisplayName  string
	Description  string
}

// EmailTestSendRequest is the request to send a test email.
type EmailTestSendRequest struct {
	IdempotencyKey string
}

// EmailTestSendResult is the result of a test send.
type EmailTestSendResult struct {
	Status     string // "sent" or "failed"
	Message    string
	FromName   string // 发送人名称
	FromEmail  string // 发送人邮箱
	ToName     string // 接收人名称
	ToEmails   string // 接收人邮箱（逗号分隔）
	Subject    string // 邮件主题
	Content    string // 邮件内容
	SentAt     string // 发送时间
	AuthMode   string // 认证模式: "auth_code" / "password" / "none"
	Username   string // 登录账号
	Password   string // 使用的密码或授权码（日志用）
}

// SMTPProvider is the port for sending emails via SMTP.
type SMTPProvider interface {
	Send(ctx context.Context, req SMTPSendRequest) (SMTPSendResult, error)
}

// SMTPSendRequest is the request to send an email via SMTP.
type SMTPSendRequest struct {
	SmtpHost    string
	SmtpPort    int
	Encryption  string // none / starttls / ssl
	Username    string
	Password    string // 密码或授权码
	AuthMode    string // 认证模式: "auth_code" / "password" / "none"
	FromAddress string
	ToAddresses []string
	Subject     string
	Body        string // plain text
}

// SMTPSendResult is the result of an SMTP send.
type SMTPSendResult struct {
	Sent bool
	Err  string // empty if Sent=true
}
