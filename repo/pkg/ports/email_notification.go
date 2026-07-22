package ports

import (
	"context"
	"errors"
	"time"
)

// EmailSmtpConfig holds the platform-level SMTP sending channel configuration.
// Password and AuthCode are never returned in plaintext; only HasPassword /
// HasAuthCode booleans are exposed.
type EmailSmtpConfig struct {
	SmtpHost    string
	SmtpPort    int
	Encryption  string // "none" | "starttls" | "ssl"
	FromAddress string
	Username    string
	HasPassword bool
	HasAuthCode bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EmailSmtpConfigWrite carries user-supplied SMTP configuration for a PUT
// request. Password and AuthCode use pointer semantics:
//   - nil  = do not modify existing value
//   - &""  = clear existing value
//   - &"x" = overwrite with new value (encrypted at storage layer)
// Password and AuthCode are independent; setting one does not affect the other.
type EmailSmtpConfigWrite struct {
	SmtpHost    string
	SmtpPort    int
	Encryption  string
	FromAddress string
	Username    string
	Password    *string
	AuthCode    *string
}

// EmailRecipient is a platform-level email recipient entry.
type EmailRecipient struct {
	ID        string
	Email     string
	Label     string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EmailRecipientWrite carries user-supplied recipient fields for create/update.
type EmailRecipientWrite struct {
	Email string
	Label string
}

// EmailSubscription is a single event-type email subscription toggle.
type EmailSubscription struct {
	EventType   string
	Description string
	Enabled     bool
	UpdatedAt   time.Time
}

// EmailTestSendResult is the synchronous result of a test email send.
type EmailTestSendResult struct {
	Success   bool
	Message   string
	RequestID string
}

// EmailNotificationStore abstracts platform-level email notification
// configuration: SMTP channel, recipients, event subscriptions, and test send.
// Implementations MUST enforce platform RBAC (scope:notifications:read/write)
// and MUST NOT trust body-supplied tenant_id for platform-level operations.
// Write operations MUST use idempotencyKey for deduplication.
type EmailNotificationStore interface {
	GetSmtpConfig(ctx context.Context) (*EmailSmtpConfig, error)
	PutSmtpConfig(ctx context.Context, idempotencyKey string, cfg EmailSmtpConfigWrite) (*EmailSmtpConfig, error)

	ListRecipients(ctx context.Context) ([]EmailRecipient, error)
	CreateRecipient(ctx context.Context, idempotencyKey string, w EmailRecipientWrite) (*EmailRecipient, error)
	UpdateRecipient(ctx context.Context, id string, w EmailRecipientWrite) (*EmailRecipient, error)
	SetRecipientEnabled(ctx context.Context, id string, enabled bool) (*EmailRecipient, error)
	DeleteRecipient(ctx context.Context, id string) error

	ListSubscriptions(ctx context.Context) ([]EmailSubscription, error)
	PutSubscriptions(ctx context.Context, idempotencyKey string, subs map[string]bool) ([]EmailSubscription, error)

	SendTestEmail(ctx context.Context, idempotencyKey string) (*EmailTestSendResult, error)
}

// Email notification store errors. Use errors.Is to let handlers map to HTTP.
var (
	ErrEmailSmtpNotConfigured  = errors.New("smtp channel not configured")
	ErrEmailNoEnabledRecipient = errors.New("no enabled email recipients")
	ErrEmailNoCredentials      = errors.New("smtp credentials not configured (password or auth_code required)")
	ErrEmailRecipientNotFound  = errors.New("email recipient not found")
	ErrEmailStoreUnavailable   = errors.New("email notification store unavailable")
	ErrEmailInvalidEventType   = errors.New("invalid email subscription event type")
)
