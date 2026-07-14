package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// localEmailNotificationService implements ports.EmailNotificationService
// with in-memory storage. Data is lost on restart.
type localEmailNotificationService struct {
	mu             sync.Mutex
	smtpConfig     *ports.EmailSmtpConfigRecord
	recipients     map[string]ports.EmailRecipientRecord
	recipientsById map[string]string // email -> ID
	subscriptions  map[string]bool    // event_type -> enabled
	idem           map[string]string // idempotency key -> result ID
	testIdem       map[string]ports.EmailTestSendResult
	smtpProvider   ports.SMTPProvider // nil = simulate only
}

type EmailNotificationServiceOption func(*localEmailNotificationService)

func WithSMTPProvider(provider ports.SMTPProvider) EmailNotificationServiceOption {
	return func(s *localEmailNotificationService) {
		s.smtpProvider = provider
	}
}

func NewLocalEmailNotificationService(options ...EmailNotificationServiceOption) ports.EmailNotificationService {
	s := &localEmailNotificationService{
		recipients:     map[string]ports.EmailRecipientRecord{},
		recipientsById: map[string]string{},
		subscriptions: map[string]bool{
			"platform_alert_p0":    false,
			"platform_alert_p1":    false,
			"incident_created":     false,
			"incident_escalated":   false,
			"platform_task_failed": false,
		},
		idem:     map[string]string{},
		testIdem: map[string]ports.EmailTestSendResult{},
	}
	for _, option := range options {
		option(s)
	}
	return s
}

func (s *localEmailNotificationService) GetSmtpConfig(ctx context.Context) (ports.EmailSmtpConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx
	if s.smtpConfig == nil {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: smtp config not yet saved", ports.ErrNotFound)
	}
	rec := *s.smtpConfig
	rec.Password = ""
	rec.AuthCode = ""
	rec.PasswordConfigured = s.smtpConfig.Password != ""
	rec.AuthCodeConfigured = s.smtpConfig.AuthCode != ""
	return rec, nil
}

func (s *localEmailNotificationService) PutSmtpConfig(ctx context.Context, req ports.EmailSmtpConfigPutRequest) (ports.EmailSmtpConfigRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	if req.IdempotencyKey == "" {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: idempotency_key required", ports.ErrInvalid)
	}
	if req.SmtpHost == "" {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: smtp_host required", ports.ErrInvalid)
	}
	if req.SmtpPort < 1 || req.SmtpPort > 65535 {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: smtp_port out of range", ports.ErrInvalid)
	}
	switch req.Encryption {
	case "none", "starttls", "ssl":
	default:
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: encryption must be none/starttls/ssl", ports.ErrInvalid)
	}
	if req.FromAddress == "" {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: from_address required", ports.ErrInvalid)
	}
	if req.Username == "" {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: username required (SMTP 认证用户名，通常为完整邮箱地址)", ports.ErrInvalid)
	}

	// Idempotency check
	if id, ok := s.idem[req.IdempotencyKey]; ok {
		if id == "smtp" && s.smtpConfig != nil {
			rec := *s.smtpConfig
			rec.Password = ""
			rec.AuthCode = ""
			rec.PasswordConfigured = s.smtpConfig.Password != ""
			rec.AuthCodeConfigured = s.smtpConfig.AuthCode != ""
			return rec, nil
		}
	}

	// Preserve password and auth_code if empty
	password := req.Password
	if password == "" && s.smtpConfig != nil {
		password = s.smtpConfig.Password
	}
	authCode := req.AuthCode
	if authCode == "" && s.smtpConfig != nil {
		authCode = s.smtpConfig.AuthCode
	}

	// 首次保存时，password 和 auth_code 至少填一个
	if s.smtpConfig == nil && password == "" && authCode == "" {
		return ports.EmailSmtpConfigRecord{}, fmt.Errorf("%w: 登录密码和授权码至少填写一个", ports.ErrInvalid)
	}

	now := time.Now().UTC()
	s.smtpConfig = &ports.EmailSmtpConfigRecord{
		SmtpHost:    req.SmtpHost,
		SmtpPort:    req.SmtpPort,
		Encryption:  req.Encryption,
		FromAddress: req.FromAddress,
		Username:    req.Username,
		Password:    password,
		AuthCode:    authCode,
		Configured:  true,
	}
	_ = now
	s.idem[req.IdempotencyKey] = "smtp"

	rec := *s.smtpConfig
	rec.Password = ""
	rec.AuthCode = ""
	rec.PasswordConfigured = password != ""
	rec.AuthCodeConfigured = authCode != ""
	return rec, nil
}

func (s *localEmailNotificationService) ListRecipients(ctx context.Context) ([]ports.EmailRecipientRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx
	result := make([]ports.EmailRecipientRecord, 0, len(s.recipients))
	for _, r := range s.recipients {
		result = append(result, r)
	}
	return result, nil
}

func (s *localEmailNotificationService) CreateRecipient(ctx context.Context, req ports.EmailRecipientCreateRequest) (ports.EmailRecipientRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	if req.IdempotencyKey == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: idempotency_key required", ports.ErrInvalid)
	}
	if req.Email == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: email required", ports.ErrInvalid)
	}
	if !strings.Contains(req.Email, "@") {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: invalid email format", ports.ErrInvalid)
	}

	// Idempotency
	if id, ok := s.idem[req.IdempotencyKey]; ok {
		if rec, ok := s.recipients[id]; ok {
			return rec, nil
		}
	}

	// Uniqueness
	if _, exists := s.recipientsById[req.Email]; exists {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient email already exists", ports.ErrConflict)
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	rec := ports.EmailRecipientRecord{
		ID:        id,
		Email:     req.Email,
		Label:     req.Label,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.recipients[id] = rec
	s.recipientsById[req.Email] = id
	s.idem[req.IdempotencyKey] = id

	return rec, nil
}

func (s *localEmailNotificationService) UpdateRecipient(ctx context.Context, req ports.EmailRecipientUpdateRequest) (ports.EmailRecipientRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	if req.IdempotencyKey == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: idempotency_key required", ports.ErrInvalid)
	}
	if req.ID == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient_id required", ports.ErrInvalid)
	}

	rec, exists := s.recipients[req.ID]
	if !exists {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient not found", ports.ErrNotFound)
	}

	// Idempotency
	if id, ok := s.idem[req.IdempotencyKey]; ok && id == req.ID {
		return rec, nil
	}

	// Check email uniqueness if changing
	if req.Email != "" && req.Email != rec.Email {
		if _, exists := s.recipientsById[req.Email]; exists {
			return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient email already exists", ports.ErrConflict)
		}
		delete(s.recipientsById, rec.Email)
		rec.Email = req.Email
		s.recipientsById[req.Email] = rec.ID
	}
	if req.Label != "" {
		rec.Label = req.Label
	}
	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	rec.UpdatedAt = time.Now().UTC()
	s.recipients[req.ID] = rec
	s.idem[req.IdempotencyKey] = req.ID

	return rec, nil
}

func (s *localEmailNotificationService) DeleteRecipient(ctx context.Context, req ports.EmailRecipientDeleteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	rec, exists := s.recipients[req.ID]
	if !exists {
		return fmt.Errorf("%w: recipient not found", ports.ErrNotFound)
	}
	delete(s.recipients, req.ID)
	delete(s.recipientsById, rec.Email)
	return nil
}

func (s *localEmailNotificationService) ListSubscriptions(ctx context.Context) ([]ports.EmailSubscriptionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx
	result := make([]ports.EmailSubscriptionRecord, 0, len(s.subscriptions))
	for eventType, enabled := range s.subscriptions {
		result = append(result, ports.EmailSubscriptionRecord{
			EventType: eventType,
			Enabled:   enabled,
		})
	}
	return result, nil
}

func (s *localEmailNotificationService) PutSubscriptions(ctx context.Context, req ports.EmailSubscriptionsPutRequest) ([]ports.EmailSubscriptionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key required", ports.ErrInvalid)
	}

	validEvents := map[string]bool{
		"platform_alert_p0":    true,
		"platform_alert_p1":    true,
		"incident_created":     true,
		"incident_escalated":   true,
		"platform_task_failed": true,
	}

	for _, sub := range req.Subscriptions {
		if !validEvents[sub.EventType] {
			return nil, fmt.Errorf("%w: invalid event_type %s", ports.ErrInvalid, sub.EventType)
		}
	}

	// Idempotency: if same key used, return current state
	if _, ok := s.idem[req.IdempotencyKey]; ok {
		return s.listSubscriptionsLocked(), nil
	}

	// Replace all subscriptions
	for eventType := range s.subscriptions {
		s.subscriptions[eventType] = false
	}
	for _, sub := range req.Subscriptions {
		s.subscriptions[sub.EventType] = sub.Enabled
	}
	s.idem[req.IdempotencyKey] = "subscriptions"

	return s.listSubscriptionsLocked(), nil
}

func (s *localEmailNotificationService) listSubscriptionsLocked() []ports.EmailSubscriptionRecord {
	result := make([]ports.EmailSubscriptionRecord, 0, 5)
	for _, eventType := range []string{"platform_alert_p0", "platform_alert_p1", "incident_created", "incident_escalated", "platform_task_failed"} {
		result = append(result, ports.EmailSubscriptionRecord{
			EventType: eventType,
			Enabled:   s.subscriptions[eventType],
		})
	}
	return result
}

func (s *localEmailNotificationService) ListEvents(ctx context.Context) ([]ports.EmailEventInfoRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx
	return []ports.EmailEventInfoRecord{
		{EventType: "platform_alert_p0", DisplayName: "平台告警 P0", Description: "平台级 P0 告警事件"},
		{EventType: "platform_alert_p1", DisplayName: "平台告警 P1", Description: "平台级 P1 告警事件"},
		{EventType: "incident_created", DisplayName: "Incident 创建", Description: "Incident 创建事件"},
		{EventType: "incident_escalated", DisplayName: "Incident 升级", Description: "Incident 升级事件"},
		{EventType: "platform_task_failed", DisplayName: "平台关键任务失败", Description: "平台关键任务失败事件"},
	}, nil
}

func (s *localEmailNotificationService) SendTestEmail(ctx context.Context, req ports.EmailTestSendRequest) (ports.EmailTestSendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = ctx

	if req.IdempotencyKey == "" {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: idempotency_key required", ports.ErrInvalid)
	}

	// Idempotency
	if result, ok := s.testIdem[req.IdempotencyKey]; ok {
		return result, nil
	}

	// Preconditions
	if s.smtpConfig == nil || !s.smtpConfig.Configured {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: 请先配置发信通道", ports.ErrFailedPrecondition)
	}
	var enabledRecipients []ports.EmailRecipientRecord
	for _, r := range s.recipients {
		if r.Enabled {
			enabledRecipients = append(enabledRecipients, r)
		}
	}
	if len(enabledRecipients) == 0 {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: 请在「收件邮箱」中添加至少一个启用的收件人", ports.ErrFailedPrecondition)
	}

	// Build recipient list
	var toEmails, toNames []string
	for _, r := range enabledRecipients {
		toEmails = append(toEmails, r.Email)
		if r.Label != "" {
			toNames = append(toNames, r.Label)
		} else {
			toNames = append(toNames, r.Email)
		}
	}

	fromName := s.smtpConfig.Username
	if fromName == "" {
		fromName = s.smtpConfig.FromAddress
	}

	subject := "【ANI 平台】邮件通知测试"
	content := fmt.Sprintf(
		"这是一封来自 ANI 平台的测试邮件。\n\n"+
			"发送通道: %s:%d (%s)\n"+
			"发件人: %s\n"+
			"收件人: %s\n"+
			"发送时间: %s\n\n"+
			"如果您收到此邮件，说明邮件通知配置正确。",
		s.smtpConfig.SmtpHost, s.smtpConfig.SmtpPort, s.smtpConfig.Encryption,
		s.smtpConfig.FromAddress,
		strings.Join(toEmails, ", "),
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
	)

	// Send via SMTP provider if configured, otherwise simulate
	if s.smtpProvider != nil {
		// 决定认证模式：优先授权码，回退密码
		var authMode, authPassword string
		if s.smtpConfig.AuthCode != "" {
			authMode = "auth_code"
			authPassword = s.smtpConfig.AuthCode
		} else if s.smtpConfig.Password != "" {
			authMode = "password"
			authPassword = s.smtpConfig.Password
		} else {
			// 两者都为空，仍然尝试发送（某些 SMTP 服务器不需要认证）
			authMode = "none"
			authPassword = ""
		}

		slog.Info("sending test email via SMTP",
			"smtp_host", s.smtpConfig.SmtpHost,
			"smtp_port", s.smtpConfig.SmtpPort,
			"encryption", s.smtpConfig.Encryption,
			"from", s.smtpConfig.FromAddress,
			"recipients", len(toEmails),
			"auth_mode", authMode,
			"username", s.smtpConfig.Username,
			"auth_code_len", len(s.smtpConfig.AuthCode),
			"password_len", len(s.smtpConfig.Password),
		)
		sendResult, err := s.smtpProvider.Send(ctx, ports.SMTPSendRequest{
			SmtpHost:    s.smtpConfig.SmtpHost,
			SmtpPort:    s.smtpConfig.SmtpPort,
			Encryption:  s.smtpConfig.Encryption,
			Username:    s.smtpConfig.Username,
			Password:    authPassword,
			AuthMode:    authMode,
			FromAddress: s.smtpConfig.FromAddress,
			ToAddresses: toEmails,
			Subject:     subject,
			Body:        content,
		})
		if err != nil {
			result := ports.EmailTestSendResult{
				Status:    "failed",
				Message:   fmt.Sprintf("SMTP 发送异常: %v", err),
				FromName:  fromName,
				FromEmail: s.smtpConfig.FromAddress,
				ToName:    strings.Join(toNames, ", "),
				ToEmails:  strings.Join(toEmails, ", "),
				Subject:   subject,
				Content:   content,
				SentAt:    time.Now().UTC().Format(time.RFC3339),
				AuthMode:  authMode,
				Username:  s.smtpConfig.Username,
				Password:  authPassword,
			}
			slog.Error("email test send FAILED",
				"auth_mode", authMode,
				"username", s.smtpConfig.Username,
				"auth_password_len", len(authPassword),
				"error", err.Error(),
			)
			s.testIdem[req.IdempotencyKey] = result
			return result, nil
		}
		if !sendResult.Sent {
			result := ports.EmailTestSendResult{
				Status:    "failed",
				Message:   fmt.Sprintf("SMTP 发送失败: %s", sendResult.Err),
				FromName:  fromName,
				FromEmail: s.smtpConfig.FromAddress,
				ToName:    strings.Join(toNames, ", "),
				ToEmails:  strings.Join(toEmails, ", "),
				Subject:   subject,
				Content:   content,
				SentAt:    time.Now().UTC().Format(time.RFC3339),
				AuthMode:  authMode,
				Username:  s.smtpConfig.Username,
				Password:  authPassword,
			}
			slog.Error("email test send FAILED",
				"auth_mode", authMode,
				"username", s.smtpConfig.Username,
				"auth_password_len", len(authPassword),
				"error", sendResult.Err,
			)
			s.testIdem[req.IdempotencyKey] = result
			return result, nil
		}
		result := ports.EmailTestSendResult{
			Status:    "sent",
			Message:   fmt.Sprintf("测试邮件已通过 SMTP 发送，共 %d 个收件人", len(enabledRecipients)),
			FromName:  fromName,
			FromEmail: s.smtpConfig.FromAddress,
			ToName:    strings.Join(toNames, ", "),
			ToEmails:  strings.Join(toEmails, ", "),
			Subject:   subject,
			Content:   content,
			SentAt:    time.Now().UTC().Format(time.RFC3339),
			AuthMode:  authMode,
			Username:  s.smtpConfig.Username,
			Password:  authPassword,
		}
		slog.Info("email test send SUCCESS",
			"auth_mode", authMode,
			"username", s.smtpConfig.Username,
			"auth_password_len", len(authPassword),
		)
		s.testIdem[req.IdempotencyKey] = result
		return result, nil
	}

	// No SMTP provider — simulate success (local-only mode)
	result := ports.EmailTestSendResult{
		Status:    "sent",
		Message:   fmt.Sprintf("测试邮件已发送（local adapter 模拟），共 %d 个收件人", len(enabledRecipients)),
		FromName:  fromName,
		FromEmail: s.smtpConfig.FromAddress,
		ToName:    strings.Join(toNames, ", "),
		ToEmails:  strings.Join(toEmails, ", "),
		Subject:   subject,
		Content:   content,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
	}
	s.testIdem[req.IdempotencyKey] = result
	return result, nil
}
