package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/kubercloud/ani/pkg/ports"
)

func setupEmailNotificationRouter(t *testing.T) (*server.Hertz, ports.EmailNotificationStore) {
	t.Helper()
	store := &inMemEmailStore{
		mu:            sync.Mutex{},
		recipients:    map[string]ports.EmailRecipient{},
		subscriptions: defaultTestSubscriptions(),
	}
	h := server.New()
	h.Use(
		func(ctx context.Context, c *app.RequestContext) { c.Set("tenant_id", "demo-tenant"); c.Next(ctx) },
	)
	registerEmailNotificationResourcesWithService(h.Group("/api/v1"), store)
	return h, store
}

func defaultTestSubscriptions() map[string]ports.EmailSubscription {
	now := time.Now()
	return map[string]ports.EmailSubscription{
		"platform_alert_p0":    {EventType: "platform_alert_p0", Description: "平台告警 P0", Enabled: false, UpdatedAt: now},
		"platform_alert_p1":    {EventType: "platform_alert_p1", Description: "平台告警 P1", Enabled: false, UpdatedAt: now},
		"incident_created":     {EventType: "incident_created", Description: "Incident 创建", Enabled: false, UpdatedAt: now},
		"incident_escalated":   {EventType: "incident_escalated", Description: "Incident 升级", Enabled: false, UpdatedAt: now},
		"platform_task_failed": {EventType: "platform_task_failed", Description: "平台关键任务失败", Enabled: false, UpdatedAt: now},
	}
}

func performReq(t *testing.T, h *server.Hertz, method, path, body string) *protocol.Response {
	t.Helper()
	resp := ut.PerformRequest(h.Engine, method, path,
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	return resp
}

func TestEmailNotif_GetSmtpConfig_Empty(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	resp := performReq(t, h, http.MethodGet, "/api/v1/notifications/email/smtp", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body()))
	}
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Configured {
		t.Error("expected Configured=false")
	}
}

func TestEmailNotif_PutSmtpConfig_New(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-1","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":"secret123"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body()))
	}
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Configured {
		t.Error("expected Configured=true")
	}
	if !r.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if r.HasAuthCode {
		t.Error("expected HasAuthCode=false")
	}
}

func TestEmailNotif_PutSmtpConfig_KeepPassword(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body1 := `{"idempotency_key":"idem-1","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":"original"}`
	performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body1)

	body2 := `{"idempotency_key":"idem-2","smtp_host":"smtp.new.com","smtp_port":25,"encryption":"none","from_address":"new@ani.example.com","username":"new"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body2)
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.HasPassword {
		t.Error("expected HasPassword=true (kept)")
	}
}

func TestEmailNotif_PutSmtpConfig_ClearPassword(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body1 := `{"idempotency_key":"idem-1","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":"original"}`
	performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body1)

	body2 := `{"idempotency_key":"idem-2","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":""}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body2)
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.HasPassword {
		t.Error("expected HasPassword=false after clear")
	}
}

func TestEmailNotif_PutSmtpConfig_ClearAuthCode(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body1 := `{"idempotency_key":"idem-1","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":"pass1","auth_code":"code1"}`
	performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body1)

	body2 := `{"idempotency_key":"idem-2","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","auth_code":""}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body2)
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.HasPassword {
		t.Error("expected HasPassword=true (kept)")
	}
	if r.HasAuthCode {
		t.Error("expected HasAuthCode=false after clear")
	}
}

func TestEmailNotif_PutSmtpConfig_BothCredentials(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-both","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert","password":"pass1","auth_code":"code1"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body()))
	}
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if !r.HasAuthCode {
		t.Error("expected HasAuthCode=true")
	}
}

func TestEmailNotif_PutSmtpConfig_OnlyAuthCode(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-ac","smtp_host":"smtp.qq.com","smtp_port":465,"encryption":"ssl","from_address":"test@qq.example.com","username":"test","auth_code":"qqcode"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body)
	var r emailSmtpConfigResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.HasPassword {
		t.Error("expected HasPassword=false")
	}
	if !r.HasAuthCode {
		t.Error("expected HasAuthCode=true")
	}
}

func TestEmailNotif_PutSmtpConfig_NoIdempotencyKey(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body)
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_PutSmtpConfig_InvalidPort(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-bad","smtp_host":"smtp.example.com","smtp_port":99999,"encryption":"starttls","from_address":"alert@ani.example.com","username":"alert"}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", body)
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_ListRecipients_Empty(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	resp := performReq(t, h, http.MethodGet, "/api/v1/notifications/email/recipients", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
	var r emailRecipientListResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Total != 0 {
		t.Errorf("expected 0, got %d", r.Total)
	}
}

func TestEmailNotif_CreateRecipient_Valid(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-r1","email":"oncall@ani.example.com","label":"Oncall"}`
	resp := performReq(t, h, http.MethodPost, "/api/v1/notifications/email/recipients", body)
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode(), string(resp.Body()))
	}
	var r emailRecipientResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Email != "oncall@ani.example.com" {
		t.Errorf("expected email, got %s", r.Email)
	}
	if !r.Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestEmailNotif_UpdateRecipient_NotFound(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-up","email":"new@ani.example.com"}`
	resp := performReq(t, h, http.MethodPatch, "/api/v1/notifications/email/recipients/nonexistent", body)
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_DeleteRecipient_Success(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	// Create
	body := `{"idempotency_key":"idem-del","email":"delete@ani.example.com","label":"ToDelete"}`
	resp := performReq(t, h, http.MethodPost, "/api/v1/notifications/email/recipients", body)
	var created emailRecipientResponse
	if err := json.Unmarshal(resp.Body(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Delete
	respDel := performReq(t, h, http.MethodDelete, "/api/v1/notifications/email/recipients/"+created.ID, "")
	if respDel.StatusCode() != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", respDel.StatusCode())
	}
}

func TestEmailNotif_ListSubscriptions_Default(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	resp := performReq(t, h, http.MethodGet, "/api/v1/notifications/email/subscriptions", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
	var r emailSubscriptionListResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Total != 5 {
		t.Fatalf("expected 5, got %d", r.Total)
	}
	for _, s := range r.Items {
		if s.Enabled {
			t.Errorf("expected %s disabled", s.EventType)
		}
	}
}

func TestEmailNotif_PutSubscriptions_BatchUpdate(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-subs","subscriptions":[{"event_type":"platform_alert_p0","enabled":true},{"event_type":"incident_created","enabled":true},{"event_type":"platform_alert_p1","enabled":false},{"event_type":"incident_escalated","enabled":false},{"event_type":"platform_task_failed","enabled":false}]}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/subscriptions", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body()))
	}
	var r emailSubscriptionListResponse
	if err := json.Unmarshal(resp.Body(), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, s := range r.Items {
		if s.EventType == "platform_alert_p0" && !s.Enabled {
			t.Error("expected platform_alert_p0 enabled")
		}
	}
}

func TestEmailNotif_PutSubscriptions_InvalidEventType(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-bad","subscriptions":[{"event_type":"nonexistent","enabled":true}]}`
	resp := performReq(t, h, http.MethodPut, "/api/v1/notifications/email/subscriptions", body)
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_SendTestEmail_NoSmtp(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	body := `{"idempotency_key":"idem-test"}`
	resp := performReq(t, h, http.MethodPost, "/api/v1/notifications/email/test", body)
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_SendTestEmail_NoRecipients(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	smtpBody := `{"idempotency_key":"idem-smtp","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"test@ani.example.com","username":"test","password":"pass"}`
	performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", smtpBody)

	testBody := `{"idempotency_key":"idem-test"}`
	resp := performReq(t, h, http.MethodPost, "/api/v1/notifications/email/test", testBody)
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode())
	}
}

func TestEmailNotif_SendTestEmail_NoCredentials(t *testing.T) {
	h, _ := setupEmailNotificationRouter(t)
	smtpBody := `{"idempotency_key":"idem-smtp-nocred","smtp_host":"smtp.example.com","smtp_port":587,"encryption":"starttls","from_address":"test@ani.example.com","username":"test"}`
	performReq(t, h, http.MethodPut, "/api/v1/notifications/email/smtp", smtpBody)

	recBody := `{"idempotency_key":"idem-r","email":"oncall@ani.example.com"}`
	performReq(t, h, http.MethodPost, "/api/v1/notifications/email/recipients", recBody)

	testBody := `{"idempotency_key":"idem-test"}`
	resp := performReq(t, h, http.MethodPost, "/api/v1/notifications/email/test", testBody)
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode())
	}
}

// --- inMemEmailStore: a minimal in-memory EmailNotificationStore for handler tests ---

type inMemEmailStore struct {
	mu            sync.Mutex
	smtpConfig    *ports.EmailSmtpConfig
	password      string
	authCode      string
	recipients    map[string]ports.EmailRecipient
	subscriptions map[string]ports.EmailSubscription
	idem          map[string]string
}

func (s *inMemEmailStore) GetSmtpConfig(_ context.Context) (*ports.EmailSmtpConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.smtpConfig == nil {
		return nil, nil
	}
	cfg := *s.smtpConfig
	return &cfg, nil
}

func (s *inMemEmailStore) PutSmtpConfig(_ context.Context, idempotencyKey string, w ports.EmailSmtpConfigWrite) (*ports.EmailSmtpConfig, error) {
	if idempotencyKey == "" {
		return nil, ports.ErrInvalid
	}
	if w.SmtpHost == "" || w.FromAddress == "" || w.Username == "" {
		return nil, ports.ErrInvalid
	}
	if w.SmtpPort < 1 || w.SmtpPort > 65535 {
		return nil, ports.ErrInvalid
	}
	switch w.Encryption {
	case "none", "starttls", "ssl":
	default:
		return nil, ports.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.smtpConfig == nil {
		s.smtpConfig = &ports.EmailSmtpConfig{
			SmtpHost:    w.SmtpHost,
			SmtpPort:    w.SmtpPort,
			Encryption:  w.Encryption,
			FromAddress: w.FromAddress,
			Username:    w.Username,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if s.idem == nil {
			s.idem = map[string]string{}
		}
	} else {
		s.smtpConfig.SmtpHost = w.SmtpHost
		s.smtpConfig.SmtpPort = w.SmtpPort
		s.smtpConfig.Encryption = w.Encryption
		s.smtpConfig.FromAddress = w.FromAddress
		s.smtpConfig.Username = w.Username
		s.smtpConfig.UpdatedAt = now
	}
	if w.Password != nil {
		if *w.Password == "" {
			s.password = ""
			s.smtpConfig.HasPassword = false
		} else {
			s.password = *w.Password
			s.smtpConfig.HasPassword = true
		}
	}
	if w.AuthCode != nil {
		if *w.AuthCode == "" {
			s.authCode = ""
			s.smtpConfig.HasAuthCode = false
		} else {
			s.authCode = *w.AuthCode
			s.smtpConfig.HasAuthCode = true
		}
	}
	cfg := *s.smtpConfig
	return &cfg, nil
}

func (s *inMemEmailStore) ListRecipients(_ context.Context) ([]ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.EmailRecipient, 0, len(s.recipients))
	for _, r := range s.recipients {
		out = append(out, r)
	}
	return out, nil
}

func (s *inMemEmailStore) CreateRecipient(_ context.Context, idempotencyKey string, w ports.EmailRecipientWrite) (*ports.EmailRecipient, error) {
	if idempotencyKey == "" {
		return nil, ports.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idem != nil {
		if id, ok := s.idem["r:"+idempotencyKey]; ok {
			if r, found := s.recipients[id]; found {
				return &r, nil
			}
		}
	}
	now := time.Now()
	rec := ports.EmailRecipient{
		ID:        "rec-" + idempotencyKey,
		Email:     w.Email,
		Label:     w.Label,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.recipients[rec.ID] = rec
	if s.idem == nil {
		s.idem = map[string]string{}
	}
	s.idem["r:"+idempotencyKey] = rec.ID
	return &rec, nil
}

func (s *inMemEmailStore) UpdateRecipient(_ context.Context, id string, w ports.EmailRecipientWrite) (*ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recipients[id]
	if !ok {
		return nil, ports.ErrEmailRecipientNotFound
	}
	if w.Email != "" {
		r.Email = w.Email
	}
	r.Label = w.Label
	r.UpdatedAt = time.Now()
	s.recipients[id] = r
	return &r, nil
}

func (s *inMemEmailStore) SetRecipientEnabled(_ context.Context, id string, enabled bool) (*ports.EmailRecipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recipients[id]
	if !ok {
		return nil, ports.ErrEmailRecipientNotFound
	}
	r.Enabled = enabled
	r.UpdatedAt = time.Now()
	s.recipients[id] = r
	return &r, nil
}

func (s *inMemEmailStore) DeleteRecipient(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.recipients[id]; !ok {
		return ports.ErrEmailRecipientNotFound
	}
	delete(s.recipients, id)
	return nil
}

func (s *inMemEmailStore) ListSubscriptions(_ context.Context) ([]ports.EmailSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.EmailSubscription, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		out = append(out, sub)
	}
	return out, nil
}

func (s *inMemEmailStore) PutSubscriptions(_ context.Context, idempotencyKey string, subs map[string]bool) ([]ports.EmailSubscription, error) {
	if idempotencyKey == "" {
		return nil, ports.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for eventType, enabled := range subs {
		if _, ok := s.subscriptions[eventType]; !ok {
			return nil, ports.ErrEmailInvalidEventType
		}
		s.subscriptions[eventType] = ports.EmailSubscription{
			EventType:   eventType,
			Description: s.subscriptions[eventType].Description,
			Enabled:     enabled,
			UpdatedAt:   now,
		}
	}
	out := make([]ports.EmailSubscription, 0, len(s.subscriptions))
	for _, sub := range s.subscriptions {
		out = append(out, sub)
	}
	return out, nil
}

func (s *inMemEmailStore) SendTestEmail(_ context.Context, idempotencyKey string) (*ports.EmailTestSendResult, error) {
	if idempotencyKey == "" {
		return nil, ports.ErrInvalid
	}
	s.mu.Lock()
	if s.smtpConfig == nil {
		s.mu.Unlock()
		return nil, ports.ErrEmailSmtpNotConfigured
	}
	hasEnabled := false
	for _, r := range s.recipients {
		if r.Enabled {
			hasEnabled = true
			break
		}
	}
	if !hasEnabled {
		s.mu.Unlock()
		return nil, ports.ErrEmailNoEnabledRecipient
	}
	if !s.smtpConfig.HasPassword && !s.smtpConfig.HasAuthCode {
		s.mu.Unlock()
		return nil, ports.ErrEmailNoCredentials
	}
	s.mu.Unlock()
	return &ports.EmailTestSendResult{
		Success: true,
		Message: "测试邮件已发送",
	}, nil
}
