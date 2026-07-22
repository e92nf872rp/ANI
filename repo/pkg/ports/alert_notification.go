package ports

import (
	"context"
	"time"
)

// AlertNotificationEventType 首期冻结的告警事件类型枚举。
// 对齐 PRD prd-boss-email-notification.md US-003 / Decision #3：
//   平台告警 P0、平台告警 P1、Incident 创建、Incident 升级、平台关键任务失败。
// 枚举可后续追加（值递增），不删除已有值。
type AlertNotificationEventType string

const (
	AlertNotificationEventPlatformAlertP0    AlertNotificationEventType = "platform_alert_p0"
	AlertNotificationEventPlatformAlertP1    AlertNotificationEventType = "platform_alert_p1"
	AlertNotificationEventIncidentCreated    AlertNotificationEventType = "incident_created"
	AlertNotificationEventIncidentEscalated  AlertNotificationEventType = "incident_escalated"
	AlertNotificationEventPlatformTaskFailed AlertNotificationEventType = "platform_task_failed"
)

// AlertNotificationSeverity 对齐 openapi/v1.yaml
//   ObservabilityAlertRule.severity  enum: [info, warning, critical]
//   InstanceSecurityEvent.severity   enum: [info, warning, critical]
// 复用 ports/observability.go 中已定义的 ObservabilityAlertSeverity 常量，
// 此处不再重复声明 severity 类型。

// AlertNotificationEvent 表示一条告警事件，用于触发邮件通知。
// TenantID 为空表示平台级跨租户运维告警（PRD: 跨租户运维告警）。
type AlertNotificationEvent struct {
	EventID     string
	EventType   AlertNotificationEventType
	Severity    ObservabilityAlertSeverity
	TenantID    string // empty for platform-level
	Source      string // e.g. "prometheus" / "incident-manager" / "task-runner"
	Summary     string // one-line summary; used as email subject
	Description string // multi-line body; used as email body
	OccurredAt  time.Time
	Labels      map[string]string // 对齐 ObservabilityAlertRule.labels
	Payload     map[string]string // event-specific structured fields
}

// AlertEmailSubscription 每个事件类型的邮件开关。
// 对齐 PRD US-003: 每个事件可独立开关邮件；全局一份启用中收件人列表共用。
type AlertEmailSubscription struct {
	ID          string
	EventType   AlertNotificationEventType
	EmailEnabled bool
	UpdatedAt   time.Time
}

// AlertEmailSubscriptionListResult 展示首期冻结事件清单的当前订阅状态。
type AlertEmailSubscriptionListResult struct {
	Items      []AlertEmailSubscription
	Total      int
	DevProfile DevProfileInfo
}

// UpdateAlertEmailSubscriptionsRequest 批量保存邮件订阅开关。
// 对齐 PRD US-003: 支持批量保存；FR-10: idempotency_key。
type UpdateAlertEmailSubscriptionsRequest struct {
	IdempotencyKey string
	Updates       []AlertEmailSubscription
}

// UpdateAlertEmailSubscriptionsResult 批量保存后的最新订阅状态。
type UpdateAlertEmailSubscriptionsResult struct {
	Items      []AlertEmailSubscription
	UpdatedAt   time.Time
	DevProfile  DevProfileInfo
}

// AlertEmailDispatchRequest 由内部 alertmanager / incident-manager / task-runner 调用，
// 触发一封告警邮件投递到已启用订阅的全局收件人列表。
// 对齐 PRD FR-10: idempotency_key。
type AlertEmailDispatchRequest struct {
	IdempotencyKey string
	Event          AlertNotificationEvent
}

// AlertEmailDispatchResult 一次告警邮件投递结果。
// 本轮不要求独立投递历史 list（PRD Non-Goals），但保留单条投递结果反馈用于 US-004。
type AlertEmailDispatchResult struct {
	DispatchID         string
	QueuedRecipientCount int
	QueuedAt           time.Time
	DevProfile         DevProfileInfo
}

// TestAlertEmailDispatchRequest 平台管理员在 BOSS 通知设置页面发起测试发送。
// 对齐 PRD US-004: 在已配置通道且至少有一个启用收件人时可发起测试发送。
type TestAlertEmailDispatchRequest struct {
	IdempotencyKey string
	EventType      AlertNotificationEventType // 模拟事件类型，必须为冻结枚举内
	TestRecipient  string                     // optional; empty = use all enabled recipients
}

// TestAlertEmailDispatchResult 测试发送结果反馈。
// 对齐 PRD US-004: 成功/失败有明确反馈（错误态优先展示可读 message；有 request_id 则展示）。
type TestAlertEmailDispatchResult struct {
	DispatchID         string
	QueuedRecipientCount int
	Status             string // queued | sent | failed
	ErrorMessage       string
	RequestID          string // for frontend display (PRD US-004)
	CompletedAt        time.Time
	DevProfile         DevProfileInfo
}

// AlertNotificationService 表达告警事件的邮件通知能力边界。
//
// 职责范围（对齐 PRD prd-boss-email-notification.md）：
//   - 列出/批量更新首期冻结事件清单的邮件订阅开关（US-003）
//   - 投递告警邮件到已启用订阅的全局收件人列表（FR-6）
//   - 测试发送告警邮件，校验通道与收件人前置条件（US-004 / FR-7）
//
// 不包含：
//   - SMTP 通道配置（US-001，另立 EmailChannel port）
//   - 收件人列表 CRUD（US-002，另立 EmailRecipient port）
//   - 完整投递历史 list（PRD Non-Goals，P2）
type AlertNotificationService interface {
	// ListAlertEmailSubscriptions 返回首期冻结事件清单的当前邮件订阅状态。
	// 对齐 PRD US-003: 展示首期冻结事件清单。无分页参数：条目数量 ≤ 10。
	ListAlertEmailSubscriptions(ctx context.Context) (AlertEmailSubscriptionListResult, error)

	// UpdateAlertEmailSubscriptions 批量保存邮件订阅开关。
	// 对齐 PRD US-003: 支持批量保存；FR-10: idempotency_key。
	UpdateAlertEmailSubscriptions(ctx context.Context, request UpdateAlertEmailSubscriptionsRequest) (UpdateAlertEmailSubscriptionsResult, error)

	// DispatchAlertEmail 由内部服务调用，触发一封告警邮件投递。
	// 对齐 PRD FR-10: idempotency_key。
	DispatchAlertEmail(ctx context.Context, request AlertEmailDispatchRequest) (AlertEmailDispatchResult, error)

	// TestDispatchAlertEmail 平台管理员在 BOSS 页面发起测试发送。
	// 对齐 PRD US-004: 在已配置通道且至少有一个启用收件人时可发起测试发送。
	TestDispatchAlertEmail(ctx context.Context, request TestAlertEmailDispatchRequest) (TestAlertEmailDispatchResult, error)
}
