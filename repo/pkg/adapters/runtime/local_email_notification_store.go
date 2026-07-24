package runtime

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// smtpSendTimeout is the maximum duration for a single test email send.
const smtpSendTimeout = 15 * time.Second

// localEmailNotificationStore is an in-memory implementation of
// ports.EmailNotificationStore for dev/local profile.
type localEmailNotificationStore struct {
	mu             sync.Mutex
	smtpConfig     *smtpConfigEntry
	recipients     map[string]recipientEntry
	subscriptions  map[string]ports.EmailSubscription
	idem           map[string]string // idempotencyKey -> result key
	now            func() time.Time
	smtpDialerFunc func(host string, port string, auth smtp.Auth) (smtpClient, error)
}

type smtpConfigEntry struct {
	cfg      ports.EmailSmtpConfig
	password string // plaintext stored in-memory only (dev profile)
	authCode string // plaintext stored in-memory only (dev profile)
	idemKey  string
}

type recipientEntry struct {
	rec     ports.EmailRecipient
	idemKey string
}

// smtpClient is a minimal interface to allow test mocking of net/smtp.
type smtpClient interface {
	Mail(from string) error
	Rcpt(to string) error
	Data() (io.WriteCloser, error)
	Quit() error
	Close() error
	StartTLS(config *tls.Config) error
}

type EmailNotificationStoreOption func(*localEmailNotificationStore)

func WithEmailNotificationStoreClock(now func() time.Time) EmailNotificationStoreOption {
	return func(s *localEmailNotificationStore) {
		if now != nil {
			s.now = now
		}
	}
}

func WithEmailNotificationSMTPDialer(f func(host string, port string, auth smtp.Auth) (smtpClient, error)) EmailNotificationStoreOption {
	return func(s *localEmailNotificationStore) {
		s.smtpDialerFunc = f
	}
}

func NewLocalEmailNotificationStore(options ...EmailNotificationStoreOption) ports.EmailNotificationStore {
	store := &localEmailNotificationStore{
		recipients:    map[string]recipientEntry{},
		subscriptions: defaultSubscriptions(),
		idem:          map[string]string{},
		now:           time.Now,
	}
	for _, opt := range options {
		opt(store)
	}
	return store
}

func defaultSubscriptions() map[string]ports.EmailSubscription {
	now := time.Now()
	subs := map[string]ports.EmailSubscription{
		"platform_alert_p0":    {EventType: "platform_alert_p0", Description: "平台告警 P0", Enabled: false, UpdatedAt: now},
		"platform_alert_p1":    {EventType: "platform_alert_p1", Description: "平台告警 P1", Enabled: false, UpdatedAt: now},
		"incident_created":     {EventType: "incident_created", Description: "Incident 创建", Enabled: false, UpdatedAt: now},
		"incident_escalated":   {EventType: "incident_escalated", Description: "Incident 升级", Enabled: false, UpdatedAt: now},
		"platform_task_failed": {EventType: "platform_task_failed", Description: "平台关键任务失败", Enabled: false, UpdatedAt: now},
	}
	return subs
}

func (s *localEmailNotificationStore) GetSmtpConfig(_ context.Context) (*ports.EmailSmtpConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.smtpConfig == nil {
		return nil, nil
	}
	cfg := s.smtpConfig.cfg
	return &cfg, nil
}

func (s *localEmailNotificationStore) PutSmtpConfig(_ context.Context, idempotencyKey string, w ports.EmailSmtpConfigWrite) (*ports.EmailSmtpConfig, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if w.SmtpHost == "" {
		return nil, fmt.Errorf("%w: smtp_host is required", ports.ErrInvalid)
	}
	if w.SmtpPort < 1 || w.SmtpPort > 65535 {
		return nil, fmt.Errorf("%w: smtp_port must be 1-65535", ports.ErrInvalid)
	}
	switch w.Encryption {
	case "none", "starttls", "ssl":
	default:
		return nil, fmt.Errorf("%w: encryption must be none/starttls/ssl", ports.ErrInvalid)
	}
	if w.FromAddress == "" {
		return nil, fmt.Errorf("%w: from_address is required", ports.ErrInvalid)
	}
	if w.Username == "" {
		return nil, fmt.Errorf("%w: username is required", ports.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotent replay
	if existing, ok := s.idem["smtp:"+idempotencyKey]; ok && existing == "smtp" {
		if s.smtpConfig != nil {
			cfg := s.smtpConfig.cfg
			return &cfg, nil
		}
	}

	now := s.now()
	if s.smtpConfig == nil {
		// INSERT
		s.smtpConfig = &smtpConfigEntry{
			cfg: ports.EmailSmtpConfig{
				SmtpHost:    w.SmtpHost,
				SmtpPort:    w.SmtpPort,
				Encryption:  w.Encryption,
				FromAddress: w.FromAddress,
				Username:    w.Username,
				HasPassword: false,
				HasAuthCode: false,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
			idemKey: idempotencyKey,
		}
	} else {
		// UPDATE
		s.smtpConfig.cfg.SmtpHost = w.SmtpHost
		s.smtpConfig.cfg.SmtpPort = w.SmtpPort
		s.smtpConfig.cfg.Encryption = w.Encryption
		s.smtpConfig.cfg.FromAddress = w.FromAddress
		s.smtpConfig.cfg.Username = w.Username
		s.smtpConfig.cfg.UpdatedAt = now
	}

	// Handle password: nil = no change, &"" = clear, &"x" = overwrite
	if w.Password != nil {
		if *w.Password == "" {
			s.smtpConfig.password = ""
			s.smtpConfig.cfg.HasPassword = false
		} else {
			s.smtpConfig.password = *w.Password
			s.smtpConfig.cfg.HasPassword = true
		}
	}
	// Handle auth_code independently
	if w.AuthCode != nil {
		if *w.AuthCode == "" {
			s.smtpConfig.authCode = ""
			s.smtpConfig.cfg.HasAuthCode = false
		} else {
			s.smtpConfig.authCode = *w.AuthCode
			s.smtpConfig.cfg.HasAuthCode = true
		}
	}

	s.idem["smtp:"+idempotencyKey] = "smtp"
	cfg := s.smtpConfig.cfg
	return &cfg, nil
}

func (s *localEmailNotificationStore) ListRecipients(_ context.Context) ([]ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.EmailRecipient, 0, len(s.recipients))
	for _, entry := range s.recipients {
		out = append(out, entry.rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *localEmailNotificationStore) CreateRecipient(_ context.Context, idempotencyKey string, w ports.EmailRecipientWrite) (*ports.EmailRecipient, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if w.Email == "" {
		return nil, fmt.Errorf("%w: email is required", ports.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if id, ok := s.idem["recipient:"+idempotencyKey]; ok {
		if entry, found := s.recipients[id]; found {
			rec := entry.rec
			return &rec, nil
		}
	}

	now := s.now()
	rec := ports.EmailRecipient{
		ID:        uuid.NewString(),
		Email:     w.Email,
		Label:     w.Label,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.recipients[rec.ID] = recipientEntry{rec: rec, idemKey: idempotencyKey}
	s.idem["recipient:"+idempotencyKey] = rec.ID
	return &rec, nil
}

func (s *localEmailNotificationStore) UpdateRecipient(_ context.Context, id string, w ports.EmailRecipientWrite) (*ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recipients[id]
	if !ok {
		return nil, ports.ErrEmailRecipientNotFound
	}
	if w.Email != "" {
		entry.rec.Email = w.Email
	}
	entry.rec.Label = w.Label
	entry.rec.UpdatedAt = s.now()
	s.recipients[id] = entry
	rec := entry.rec
	return &rec, nil
}

func (s *localEmailNotificationStore) SetRecipientEnabled(_ context.Context, id string, enabled bool) (*ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recipients[id]
	if !ok {
		return nil, ports.ErrEmailRecipientNotFound
	}
	entry.rec.Enabled = enabled
	entry.rec.UpdatedAt = s.now()
	s.recipients[id] = entry
	rec := entry.rec
	return &rec, nil
}

func (s *localEmailNotificationStore) DeleteRecipient(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recipients[id]
	if !ok {
		return ports.ErrEmailRecipientNotFound
	}
	delete(s.recipients, id)
	if entry.idemKey != "" {
		delete(s.idem, "recipient:"+entry.idemKey)
	}
	return nil
}

func (s *localEmailNotificationStore) ListSubscriptions(_ context.Context) ([]ports.EmailSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.EmailSubscription, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EventType < out[j].EventType })
	return out, nil
}

func (s *localEmailNotificationStore) PutSubscriptions(ctx context.Context, idempotencyKey string, subs map[string]bool) ([]ports.EmailSubscription, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	now := s.now()
	for eventType, enabled := range subs {
		if _, ok := s.subscriptions[eventType]; !ok {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: unknown event_type %s", ports.ErrEmailInvalidEventType, eventType)
		}
		s.subscriptions[eventType] = ports.EmailSubscription{
			EventType:   eventType,
			Description: s.subscriptions[eventType].Description,
			Enabled:     enabled,
			UpdatedAt:   now,
		}
	}
	// Snapshot results before unlocking
	result := make([]ports.EmailSubscription, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		result = append(result, sub)
	}
	s.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].EventType < result[j].EventType })
	return result, nil
}

func (s *localEmailNotificationStore) SendTestEmail(ctx context.Context, idempotencyKey string) (*ports.EmailTestSendResult, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}

	s.mu.Lock()
	if s.smtpConfig == nil {
		s.mu.Unlock()
		return nil, ports.ErrEmailSmtpNotConfigured
	}

	// Check for enabled recipients
	enabledRecipients := make([]string, 0)
	for _, entry := range s.recipients {
		if entry.rec.Enabled {
			enabledRecipients = append(enabledRecipients, entry.rec.Email)
		}
	}
	if len(enabledRecipients) == 0 {
		s.mu.Unlock()
		return nil, ports.ErrEmailNoEnabledRecipient
	}

	if !s.smtpConfig.cfg.HasPassword && !s.smtpConfig.cfg.HasAuthCode {
		s.mu.Unlock()
		return nil, ports.ErrEmailNoCredentials
	}

	// Snapshot config for sending
	smtpHost := s.smtpConfig.cfg.SmtpHost
	smtpPort := fmt.Sprintf("%d", s.smtpConfig.cfg.SmtpPort)
	fromAddress := s.smtpConfig.cfg.FromAddress
	username := s.smtpConfig.cfg.Username
	encryption := s.smtpConfig.cfg.Encryption
	// auth_code priority over password
	var credential string
	if s.smtpConfig.cfg.HasAuthCode {
		credential = s.smtpConfig.authCode
	} else {
		credential = s.smtpConfig.password
	}
	s.mu.Unlock()

	// Send SMTP email
	addr := smtpHost + ":" + smtpPort
	var auth smtp.Auth
	if username != "" && credential != "" {
		auth = smtp.PlainAuth("", username, credential, smtpHost)
	}

	// Generate a request ID for troubleshooting this send attempt.
	// The store layer does not have access to the gateway middleware's
	// X-Request-ID, so we generate a UUID to correlate logs and errors.
	requestID := uuid.NewString()

	var sendErr error
	if s.smtpDialerFunc != nil {
		sendErr = sendViaCustomDialer(ctx, s.smtpDialerFunc, addr, fromAddress, enabledRecipients, encryption, auth)
	} else {
		sendErr = sendViaStdSMTP(ctx, addr, fromAddress, enabledRecipients, encryption, auth)
	}

	if sendErr != nil {
		return &ports.EmailTestSendResult{
			Success:   false,
			Message:   sendErr.Error(),
			RequestID: requestID,
		}, nil
	}

	return &ports.EmailTestSendResult{
		Success:   true,
		Message:   "测试邮件已发送",
		RequestID: requestID,
	}, nil
}

func sendViaStdSMTP(ctx context.Context, addr, from string, to []string, encryption string, auth smtp.Auth) error {
	// Apply a send timeout to the context
	ctx, cancel := context.WithTimeout(ctx, smtpSendTimeout)
	defer cancel()

	host, _, _ := net.SplitHostPort(addr)

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}

	// Wrap in tls for ssl (implicit TLS)
	if encryption == "ssl" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer func() { _ = client.Quit() }()

	// STARTTLS upgrade for starttls encryption
	if encryption == "starttls" {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	subject := "[ANI 测试] 邮件通知通道验证"
	body := "这是一封来自 ANI 平台的测试邮件，用于验证邮件通知通道与收件人配置是否正确。"
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, strings.Join(to, ", "), subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}

	return nil
}

func sendViaCustomDialer(_ context.Context, dialer func(host string, port string, auth smtp.Auth) (smtpClient, error), addr, from string, to []string, encryption string, auth smtp.Auth) error {
	parts := strings.SplitN(addr, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid addr: %s", addr)
	}
	client, err := dialer(parts[0], parts[1], auth)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	subject := "[ANI 测试] 邮件通知通道验证"
	body := "这是一封来自 ANI 平台的测试邮件，用于验证邮件通知通道与收件人配置是否正确。"
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", from, strings.Join(to, ", "), subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
