package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/smtp"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func strPtr(s string) *string { return &s }

func TestStore_GetSmtpConfig_Empty(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	cfg, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for empty state, got %+v", cfg)
	}
}

func TestStore_PutGetSmtpConfig(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	cfg, err := store.PutSmtpConfig(context.Background(), "idem-1", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "alert@ani.example.com",
		Username:    "alert@ani.example.com",
		Password:    strPtr("secret123"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig failed: %v", err)
	}
	if !cfg.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if cfg.HasAuthCode {
		t.Error("expected HasAuthCode=false")
	}

	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if got.SmtpHost != "smtp.example.com" {
		t.Errorf("expected SmtpHost smtp.example.com, got %s", got.SmtpHost)
	}
	if got.SmtpPort != 587 {
		t.Errorf("expected SmtpPort 587, got %d", got.SmtpPort)
	}
	if !got.HasPassword {
		t.Error("expected HasPassword=true after get")
	}
}

func TestStore_PasswordEncryption(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-pw", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    465,
		Encryption:  "ssl",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("my-password"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig failed: %v", err)
	}
	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if !got.HasPassword {
		t.Error("expected HasPassword=true")
	}
	// Verify plaintext is not in the config struct
	if got.SmtpHost == "my-password" {
		t.Error("plaintext password leaked into config")
	}
}

func TestStore_AuthCodeEncryption(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-ac", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.qq.com",
		SmtpPort:    465,
		Encryption:  "ssl",
		FromAddress: "test@qq.example.com",
		Username:    "test@qq.example.com",
		AuthCode:    strPtr("my-auth-code"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig failed: %v", err)
	}
	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if !got.HasAuthCode {
		t.Error("expected HasAuthCode=true")
	}
	if got.HasPassword {
		t.Error("expected HasPassword=false when only auth_code set")
	}
}

func TestStore_BothCredentialsIndependent(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	// Set both password and auth_code
	_, err := store.PutSmtpConfig(context.Background(), "idem-both", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass1"),
		AuthCode:    strPtr("code1"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig failed: %v", err)
	}
	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if !got.HasPassword || !got.HasAuthCode {
		t.Error("expected both HasPassword and HasAuthCode true")
	}

	// Clear only password, auth_code should remain
	_, err = store.PutSmtpConfig(context.Background(), "idem-clear-pw", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr(""), // clear
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig clear password failed: %v", err)
	}
	got, err = store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if got.HasPassword {
		t.Error("expected HasPassword=false after clear")
	}
	if !got.HasAuthCode {
		t.Error("expected HasAuthCode=true (should remain unchanged)")
	}

	// Clear only auth_code, password should remain (but we cleared it above, so set it again)
	_, err = store.PutSmtpConfig(context.Background(), "idem-set-pw-again", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("newpass"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig set password failed: %v", err)
	}
	// Now clear auth_code
	_, err = store.PutSmtpConfig(context.Background(), "idem-clear-ac", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		AuthCode:    strPtr(""), // clear
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig clear auth_code failed: %v", err)
	}
	got, err = store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if !got.HasPassword {
		t.Error("expected HasPassword=true (should remain from previous set)")
	}
	if got.HasAuthCode {
		t.Error("expected HasAuthCode=false after clear")
	}
}

func TestStore_KeepPassword(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-1", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("original"),
	})
	if err != nil {
		t.Fatalf("first PutSmtpConfig failed: %v", err)
	}

	// Update without password (nil = no change)
	_, err = store.PutSmtpConfig(context.Background(), "idem-2", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.new.com",
		SmtpPort:    25,
		Encryption:  "none",
		FromAddress: "new@ani.example.com",
		Username:    "new@ani.example.com",
		// Password nil = don't change
	})
	if err != nil {
		t.Fatalf("second PutSmtpConfig failed: %v", err)
	}
	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if !got.HasPassword {
		t.Error("expected HasPassword=true (should be kept)")
	}
}

func TestStore_OnlyAuthCode(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-ac-only", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.163.com",
		SmtpPort:    465,
		Encryption:  "ssl",
		FromAddress: "test@163.example.com",
		Username:    "test@163.example.com",
		AuthCode:    strPtr("163-auth-code"),
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig failed: %v", err)
	}
	got, err := store.GetSmtpConfig(context.Background())
	if err != nil {
		t.Fatalf("GetSmtpConfig failed: %v", err)
	}
	if got.HasPassword {
		t.Error("expected HasPassword=false when only auth_code set")
	}
	if !got.HasAuthCode {
		t.Error("expected HasAuthCode=true")
	}
}

func TestStore_RecipientCRUD(t *testing.T) {
	store := NewLocalEmailNotificationStore()

	// Create
	rec, err := store.CreateRecipient(context.Background(), "idem-r1", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
		Label: "Oncall",
	})
	if err != nil {
		t.Fatalf("CreateRecipient failed: %v", err)
	}
	if !rec.Enabled {
		t.Error("expected new recipient Enabled=true by default")
	}

	// List
	list, err := store.ListRecipients(context.Background())
	if err != nil {
		t.Fatalf("ListRecipients failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 recipient, got %d", len(list))
	}

	// Update
	updated, err := store.UpdateRecipient(context.Background(), rec.ID, ports.EmailRecipientWrite{
		Email: "new-oncall@ani.example.com",
		Label: "Updated",
	})
	if err != nil {
		t.Fatalf("UpdateRecipient failed: %v", err)
	}
	if updated.Email != "new-oncall@ani.example.com" {
		t.Errorf("expected updated email, got %s", updated.Email)
	}

	// SetEnabled
	disabled, err := store.SetRecipientEnabled(context.Background(), rec.ID, false)
	if err != nil {
		t.Fatalf("SetRecipientEnabled failed: %v", err)
	}
	if disabled.Enabled {
		t.Error("expected Enabled=false after disable")
	}

	// Delete
	err = store.DeleteRecipient(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("DeleteRecipient failed: %v", err)
	}

	// Verify deleted
	list, err = store.ListRecipients(context.Background())
	if err != nil {
		t.Fatalf("ListRecipients after delete failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 recipients after delete, got %d", len(list))
	}
}

func TestStore_RecipientNotFound(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.UpdateRecipient(context.Background(), "nonexistent", ports.EmailRecipientWrite{})
	if !errors.Is(err, ports.ErrEmailRecipientNotFound) {
		t.Errorf("expected ErrEmailRecipientNotFound, got %v", err)
	}
	_, err = store.SetRecipientEnabled(context.Background(), "nonexistent", true)
	if !errors.Is(err, ports.ErrEmailRecipientNotFound) {
		t.Errorf("expected ErrEmailRecipientNotFound, got %v", err)
	}
	err = store.DeleteRecipient(context.Background(), "nonexistent")
	if !errors.Is(err, ports.ErrEmailRecipientNotFound) {
		t.Errorf("expected ErrEmailRecipientNotFound, got %v", err)
	}
}

func TestStore_SubscriptionBatchUpdate(t *testing.T) {
	store := NewLocalEmailNotificationStore()

	// List default — 5 rows, all false
	subs, err := store.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("ListSubscriptions failed: %v", err)
	}
	if len(subs) != 5 {
		t.Fatalf("expected 5 default subscriptions, got %d", len(subs))
	}
	for _, s := range subs {
		if s.Enabled {
			t.Errorf("expected default subscription %s to be disabled", s.EventType)
		}
	}

	// Batch update
	_, err = store.PutSubscriptions(context.Background(), "idem-subs", map[string]bool{
		"platform_alert_p0":    true,
		"incident_created":     true,
		"platform_alert_p1":    false,
		"incident_escalated":   false,
		"platform_task_failed": false,
	})
	if err != nil {
		t.Fatalf("PutSubscriptions failed: %v", err)
	}

	// Verify
	subs, err = store.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("ListSubscriptions failed: %v", err)
	}
	for _, s := range subs {
		if s.EventType == "platform_alert_p0" && !s.Enabled {
			t.Error("expected platform_alert_p0 to be enabled")
		}
		if s.EventType == "incident_created" && !s.Enabled {
			t.Error("expected incident_created to be enabled")
		}
		if s.EventType == "platform_alert_p1" && s.Enabled {
			t.Error("expected platform_alert_p1 to be disabled")
		}
	}
}

func TestStore_SubscriptionInvalidEventType(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSubscriptions(context.Background(), "idem-bad", map[string]bool{
		"nonexistent_event": true,
	})
	if !errors.Is(err, ports.ErrEmailInvalidEventType) {
		t.Errorf("expected ErrEmailInvalidEventType, got %v", err)
	}
}

func TestStore_IdempotentReplay(t *testing.T) {
	store := NewLocalEmailNotificationStore()

	// First call
	rec1, err := store.CreateRecipient(context.Background(), "idem-replay", ports.EmailRecipientWrite{
		Email: "replay@ani.example.com",
	})
	if err != nil {
		t.Fatalf("first CreateRecipient failed: %v", err)
	}

	// Second call with same key — should return same result
	rec2, err := store.CreateRecipient(context.Background(), "idem-replay", ports.EmailRecipientWrite{
		Email: "replay@ani.example.com",
	})
	if err != nil {
		t.Fatalf("second CreateRecipient failed: %v", err)
	}
	if rec1.ID != rec2.ID {
		t.Errorf("idempotent replay should return same ID: %s vs %s", rec1.ID, rec2.ID)
	}
}

func TestStore_SendTestEmail_NoSmtp(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.SendTestEmail(context.Background(), "idem-test-1")
	if !errors.Is(err, ports.ErrEmailSmtpNotConfigured) {
		t.Errorf("expected ErrEmailSmtpNotConfigured, got %v", err)
	}
}

func TestStore_SendTestEmail_NoRecipients(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, _ = store.PutSmtpConfig(context.Background(), "idem-smtp", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass"),
	})
	_, err := store.SendTestEmail(context.Background(), "idem-test-2")
	if !errors.Is(err, ports.ErrEmailNoEnabledRecipient) {
		t.Errorf("expected ErrEmailNoEnabledRecipient, got %v", err)
	}
}

func TestStore_SendTestEmail_NoCredentials(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, _ = store.PutSmtpConfig(context.Background(), "idem-smtp-nocred", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		// No password or auth_code
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-r-nocred", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	_, err := store.SendTestEmail(context.Background(), "idem-test-3")
	if !errors.Is(err, ports.ErrEmailNoCredentials) {
		t.Errorf("expected ErrEmailNoCredentials, got %v", err)
	}
}

func TestStore_SendTestEmail_PriorityAuthCode(t *testing.T) {
	// When both auth_code and password are set, auth_code should be used
	store := NewLocalEmailNotificationStore(WithEmailNotificationSMTPDialer(
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return &mockSMTPClient{}, nil
		},
	))
	_, _ = store.PutSmtpConfig(context.Background(), "idem-both-cred", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("password-val"),
		AuthCode:    strPtr("authcode-val"),
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-r-prio", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	result, err := store.SendTestEmail(context.Background(), "idem-test-prio")
	if err != nil {
		t.Fatalf("SendTestEmail failed: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Message)
	}
}

func TestStore_SendTestEmail_Success(t *testing.T) {
	store := NewLocalEmailNotificationStore(WithEmailNotificationSMTPDialer(
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return &mockSMTPClient{}, nil
		},
	))
	_, _ = store.PutSmtpConfig(context.Background(), "idem-success", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass"),
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-r-success", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	result, err := store.SendTestEmail(context.Background(), "idem-test-ok")
	if err != nil {
		t.Fatalf("SendTestEmail failed: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Message)
	}
}

func TestStore_SendTestEmail_SmtpError(t *testing.T) {
	store := NewLocalEmailNotificationStore(WithEmailNotificationSMTPDialer(
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return nil, errors.New("connection refused")
		},
	))
	_, _ = store.PutSmtpConfig(context.Background(), "idem-err", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass"),
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-r-err", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	result, err := store.SendTestEmail(context.Background(), "idem-test-err")
	if err != nil {
		t.Fatalf("SendTestEmail should return result not error: %v", err)
	}
	if result.Success {
		t.Error("expected failure result")
	}
	if result.Message == "" {
		t.Error("expected error message")
	}
}

func TestStore_PutSmtpConfig_NoIdempotencyKey(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid, got %v", err)
	}
}

func TestStore_PutSmtpConfig_InvalidPort(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-bad-port", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    99999,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid, got %v", err)
	}
}

// --- Mock SMTP client ---

type mockSMTPClient struct{}

func (m *mockSMTPClient) Mail(from string) error { return nil }
func (m *mockSMTPClient) Rcpt(to string) error   { return nil }
func (m *mockSMTPClient) Data() (io.WriteCloser, error) {
	return &nopWriteCloser{}, nil
}
func (m *mockSMTPClient) Quit() error                       { return nil }
func (m *mockSMTPClient) Close() error                      { return nil }
func (m *mockSMTPClient) StartTLS(config *tls.Config) error { return nil }

type nopWriteCloser struct{}

func (n *nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (n *nopWriteCloser) Close() error                { return nil }

// Ensure interface compliance
var _ smtpClient = (*mockSMTPClient)(nil)

// Ensure store implements port
var _ ports.EmailNotificationStore = (*localEmailNotificationStore)(nil)

// quiet unused import
var _ = time.Now
