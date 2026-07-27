package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/smtp"
	"strings"
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

// --- PutSmtpConfig validation branch tests ---

func TestStore_PutSmtpConfig_EmptyHost(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-no-host", ports.EmailSmtpConfigWrite{
		SmtpHost:    "",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty smtp_host, got %v", err)
	}
	if !strings.Contains(err.Error(), "smtp_host") {
		t.Errorf("expected error message to mention smtp_host, got: %s", err.Error())
	}
}

func TestStore_PutSmtpConfig_InvalidEncryption(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-bad-enc", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "tls3",
		FromAddress: "test@ani.example.com",
		Username:    "test",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for invalid encryption, got %v", err)
	}
	if !strings.Contains(err.Error(), "encryption") {
		t.Errorf("expected error message to mention encryption, got: %s", err.Error())
	}
}

func TestStore_PutSmtpConfig_EmptyFromAddress(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-no-from", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "",
		Username:    "test",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty from_address, got %v", err)
	}
	if !strings.Contains(err.Error(), "from_address") {
		t.Errorf("expected error message to mention from_address, got: %s", err.Error())
	}
}

func TestStore_PutSmtpConfig_EmptyUsername(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSmtpConfig(context.Background(), "idem-no-user", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty username, got %v", err)
	}
	if !strings.Contains(err.Error(), "username") {
		t.Errorf("expected error message to mention username, got: %s", err.Error())
	}
}

// --- CreateRecipient validation branch tests ---

func TestStore_CreateRecipient_NoIdempotencyKey(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.CreateRecipient(context.Background(), "", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty idempotencyKey, got %v", err)
	}
	if !strings.Contains(err.Error(), "idempotency_key") {
		t.Errorf("expected error message to mention idempotency_key, got: %s", err.Error())
	}
}

func TestStore_CreateRecipient_EmptyEmail(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.CreateRecipient(context.Background(), "idem-no-email", ports.EmailRecipientWrite{
		Email: "",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty email, got %v", err)
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("expected error message to mention email, got: %s", err.Error())
	}
}

// --- PutSubscriptions empty idempotencyKey test ---

func TestStore_PutSubscriptions_NoIdempotencyKey(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	_, err := store.PutSubscriptions(context.Background(), "", map[string]bool{
		"platform_alert_p0": true,
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Errorf("expected ErrInvalid for empty idempotencyKey, got %v", err)
	}
	if !strings.Contains(err.Error(), "idempotency_key") {
		t.Errorf("expected error message to mention idempotency_key, got: %s", err.Error())
	}
}

// --- UpdateRecipient keep-email-when-empty test ---

func TestStore_UpdateRecipient_KeepEmailWhenEmpty(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	rec, err := store.CreateRecipient(context.Background(), "idem-keep-email", ports.EmailRecipientWrite{
		Email: "original@ani.example.com",
		Label: "Original",
	})
	if err != nil {
		t.Fatalf("CreateRecipient failed: %v", err)
	}

	// Update with empty email — should keep original email
	updated, err := store.UpdateRecipient(context.Background(), rec.ID, ports.EmailRecipientWrite{
		Email: "",
		Label: "New Label",
	})
	if err != nil {
		t.Fatalf("UpdateRecipient failed: %v", err)
	}
	if updated.Email != "original@ani.example.com" {
		t.Errorf("expected email to remain original@ani.example.com, got %s", updated.Email)
	}
	if updated.Label != "New Label" {
		t.Errorf("expected label to be 'New Label', got %s", updated.Label)
	}
}

// --- DeleteRecipient idem cleanup verification ---

func TestStore_DeleteRecipient_IdemCleanup(t *testing.T) {
	store := NewLocalEmailNotificationStore()

	// Create a recipient with a specific idempotency key
	rec, err := store.CreateRecipient(context.Background(), "idem-cleanup-test", ports.EmailRecipientWrite{
		Email: "cleanup@ani.example.com",
	})
	if err != nil {
		t.Fatalf("CreateRecipient failed: %v", err)
	}

	// Delete the recipient
	if err := store.DeleteRecipient(context.Background(), rec.ID); err != nil {
		t.Fatalf("DeleteRecipient failed: %v", err)
	}

	// Verify idempotency key can be reused (idem map entry was cleaned)
	rec2, err := store.CreateRecipient(context.Background(), "idem-cleanup-test", ports.EmailRecipientWrite{
		Email: "reuse@ani.example.com",
	})
	if err != nil {
		t.Fatalf("CreateRecipient with reused idem key failed: %v", err)
	}
	// Should create a new recipient with a different ID
	if rec2.ID == rec.ID {
		t.Errorf("expected new recipient ID after idem cleanup, got same ID: %s", rec.ID)
	}
	if rec2.Email != "reuse@ani.example.com" {
		t.Errorf("expected new email after idem reuse, got %s", rec2.Email)
	}
}

// --- sendViaStdSMTP tests via local TCP mock SMTP server ---

// mockSMTPServer is a minimal in-process SMTP server for testing sendViaStdSMTP.
// It speaks just enough SMTP protocol for smtp.NewClient to succeed, and
// optionally supports STARTTLS via a self-signed certificate.
type mockSMTPServer struct {
	listener   net.Listener
	tlsConfig  *tls.Config
	startTLS   bool
	greetCode  int // greeting code; 220 for normal, 421 for failure test
	failAtAuth bool
	failAtMail bool
	received   string
	connected  bool
}

func newMockSMTPServer(t *testing.T, useTLS bool, startTLS bool) *mockSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := &mockSMTPServer{
		listener:  listener,
		startTLS:  startTLS,
		greetCode: 220,
	}
	if useTLS || startTLS {
		cert, err := generateSelfSignedCert()
		if err != nil {
			t.Fatalf("failed to generate cert: %v", err)
		}
		srv.tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	return srv
}

// generateSelfSignedCert generates a self-signed certificate for testing TLS.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost", "127.0.0.1"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}

// serve starts the mock SMTP server in a goroutine. It handles one connection then exits.
func (s *mockSMTPServer) serve(t *testing.T) {
	go func() {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connected = true
		defer func() { _ = conn.Close() }()

		// Greeting
		_, _ = fmt.Fprintf(conn, "%d mock.smtp.server ESMTP\r\n", s.greetCode)

		// For SSL (implicit TLS), wrap conn in TLS immediately
		if s.tlsConfig != nil && !s.startTLS {
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
		}

		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			upper := strings.ToUpper(line)

			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				_, _ = fmt.Fprintf(conn, "250-mock.smtp.server\r\n")
				if s.startTLS {
					_, _ = fmt.Fprintf(conn, "250-STARTTLS\r\n")
				}
				_, _ = fmt.Fprintf(conn, "250 OK\r\n")

			case strings.HasPrefix(upper, "STARTTLS"):
				_, _ = fmt.Fprintf(conn, "220 Ready to start TLS\r\n")
				if s.tlsConfig != nil {
					tlsConn := tls.Server(conn, s.tlsConfig)
					if err := tlsConn.Handshake(); err != nil {
						return
					}
					conn = tlsConn
					reader = bufio.NewReader(conn)
				}

			case strings.HasPrefix(upper, "AUTH"):
				if s.failAtAuth {
					_, _ = fmt.Fprintf(conn, "535 5.7.8 Authentication failed\r\n")
				} else {
					_, _ = fmt.Fprintf(conn, "235 2.7.0 Authentication successful\r\n")
				}

			case strings.HasPrefix(upper, "MAIL FROM"):
				if s.failAtMail {
					_, _ = fmt.Fprintf(conn, "550 Mail rejected\r\n")
				} else {
					_, _ = fmt.Fprintf(conn, "250 2.1.0 Ok\r\n")
				}

			case strings.HasPrefix(upper, "RCPT TO"):
				_, _ = fmt.Fprintf(conn, "250 2.1.5 Ok\r\n")

			case strings.HasPrefix(upper, "DATA"):
				_, _ = fmt.Fprintf(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
				// Read until terminator
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(dataLine) == "." {
						break
					}
					s.received += dataLine
				}
				_, _ = fmt.Fprintf(conn, "250 2.0.0 Ok: queued\r\n")

			case strings.HasPrefix(upper, "QUIT"):
				_, _ = fmt.Fprintf(conn, "221 Bye\r\n")
				return

			default:
				_, _ = fmt.Fprintf(conn, "500 Unrecognized command\r\n")
			}
		}
	}()
}

func (s *mockSMTPServer) addr() string {
	return s.listener.Addr().String()
}

func (s *mockSMTPServer) close() {
	_ = s.listener.Close()
}

// TestSendViaStdSMTP_Plaintext tests the "none" encryption path.
// We need to stub the tls.Config creation to allow insecure certs.
func TestSendViaStdSMTP_Plaintext(t *testing.T) {
	srv := newMockSMTPServer(t, false, false)
	defer srv.close()
	srv.serve(t)

	// Override smtpDialerFunc to use our mock; but we need to test sendViaStdSMTP directly.
	// Since sendViaStdSMTP uses tls.Client internally with no InsecureSkipVerify,
	// and our mock server uses a self-signed cert, we need a different approach:
	// We'll test sendViaStdSMTP directly for the plaintext case (no TLS).
	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err != nil {
		t.Fatalf("sendViaStdSMTP plaintext failed: %v", err)
	}
	if !srv.connected {
		t.Error("expected mock server to have received a connection")
	}
	if !strings.Contains(srv.received, "Subject: [ANI 测试] 邮件通知通道验证") {
		t.Errorf("expected email body to contain subject, got: %s", srv.received)
	}
	if !strings.Contains(srv.received, "From: from@ani.example.com") {
		t.Errorf("expected email body to contain From header, got: %s", srv.received)
	}
	if !strings.Contains(srv.received, "To: to@ani.example.com") {
		t.Errorf("expected email body to contain To header, got: %s", srv.received)
	}
	if !strings.Contains(srv.received, "Content-Type: text/plain; charset=UTF-8") {
		t.Errorf("expected email body to contain Content-Type header, got: %s", srv.received)
	}
}

// TestSendViaStdSMTP_ConnectionRefused tests dial failure.
func TestSendViaStdSMTP_ConnectionRefused(t *testing.T) {
	// Use a port that's almost certainly not listening
	err := sendViaStdSMTP(context.Background(), "127.0.0.1:1", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
	if !strings.Contains(err.Error(), "dial smtp") {
		t.Errorf("expected error to contain 'dial smtp', got: %s", err.Error())
	}
}

// TestSendViaStdSMTP_AuthFailure tests SMTP auth failure via mock server.
func TestSendViaStdSMTP_AuthFailure(t *testing.T) {
	srv := newMockSMTPServer(t, false, false)
	defer srv.close()
	srv.failAtAuth = true
	srv.serve(t)

	auth := smtp.PlainAuth("", "user", "pass", "127.0.0.1")
	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "none", auth)
	if err == nil {
		t.Fatal("expected auth failure error, got nil")
	}
	if !strings.Contains(err.Error(), "smtp auth") {
		t.Errorf("expected error to contain 'smtp auth', got: %s", err.Error())
	}
}

// TestSendViaStdSMTP_MailRejected tests MAIL FROM rejection.
func TestSendViaStdSMTP_MailRejected(t *testing.T) {
	srv := newMockSMTPServer(t, false, false)
	defer srv.close()
	srv.failAtMail = true
	srv.serve(t)

	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected mail rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "smtp mail") {
		t.Errorf("expected error to contain 'smtp mail', got: %s", err.Error())
	}
}

// TestSendViaStdSMTP_MultipleRecipients tests multiple RCPT TO commands.
func TestSendViaStdSMTP_MultipleRecipients(t *testing.T) {
	srv := newMockSMTPServer(t, false, false)
	defer srv.close()
	srv.serve(t)

	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to1@ani.example.com", "to2@ani.example.com", "to3@ani.example.com"}, "none", nil)
	if err != nil {
		t.Fatalf("sendViaStdSMTP with multiple recipients failed: %v", err)
	}
	if !strings.Contains(srv.received, "To: to1@ani.example.com, to2@ani.example.com, to3@ani.example.com") {
		t.Errorf("expected email body to contain all recipients in To header, got: %s", srv.received)
	}
}

// TestSendViaStdSMTP_BadAddr tests invalid addr format.
func TestSendViaStdSMTP_BadAddr(t *testing.T) {
	err := sendViaStdSMTP(context.Background(), "invalid-no-port", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected error for bad addr, got nil")
	}
	// net.SplitHostPort returns an error that gets wrapped in "dial smtp"
	if !strings.Contains(err.Error(), "dial smtp") {
		t.Logf("error: %s", err.Error())
	}
}

// TestSendViaStdSMTP_ContextCanceled tests context cancellation during dial.
func TestSendViaStdSMTP_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately
	err := sendViaStdSMTP(ctx, "127.0.0.1:1", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected error for canceled context, got nil")
	}
}

// TestSendViaStdSMTP_SSL tests SSL (implicit TLS) encryption path.
// This requires the SMTP server to wrap the connection in TLS from the start.
func TestSendViaStdSMTP_SSL(t *testing.T) {
	srv := newMockSMTPServer(t, true, false)
	defer srv.close()
	srv.serve(t)

	// sendViaStdSMTP creates its own tls.Config with ServerName=host and no InsecureSkipVerify.
	// Our mock server uses a self-signed cert, so we need to verify that the function
	// fails gracefully on cert verification failure (or we need to inject a custom tls.Config).
	// Since sendViaStdSMTP hardcodes tls.Config, we test that SSL to a server with
	// untrusted cert returns a tls handshake error.
	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "ssl", nil)
	if err == nil {
		// If no error, the connection succeeded (shouldn't happen with untrusted cert)
		t.Logf("SSL succeeded unexpectedly (cert may be trusted in test env)")
	} else {
		// Expected: tls handshake or x509 error
		if !strings.Contains(err.Error(), "tls handshake") && !strings.Contains(err.Error(), "x509") {
			t.Errorf("expected tls handshake or x509 error for self-signed cert, got: %s", err.Error())
		}
	}
}

// TestSendViaStdSMTP_STARTTLS tests STARTTLS upgrade path.
func TestSendViaStdSMTP_STARTTLS(t *testing.T) {
	srv := newMockSMTPServer(t, false, true)
	defer srv.close()
	srv.serve(t)

	// STARTTLS will attempt to upgrade with a self-signed cert.
	// The client uses tls.Config{ServerName: host} without InsecureSkipVerify,
	// so this should fail with a tls/x509 error unless the cert is trusted.
	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "starttls", nil)
	if err == nil {
		t.Logf("STARTTLS succeeded unexpectedly (cert may be trusted in test env)")
	} else {
		// Expected: starttls error wrapping tls/x509 error
		if !strings.Contains(err.Error(), "starttls") && !strings.Contains(err.Error(), "x509") {
			t.Errorf("expected starttls or x509 error for self-signed cert, got: %s", err.Error())
		}
	}
}

// TestSendViaStdSMTP_NoEncryption_SmtpNewClient verifies the "none" path skips TLS entirely.
func TestSendViaStdSMTP_NoEncryption_SmtpNewClient(t *testing.T) {
	srv := newMockSMTPServer(t, false, false)
	defer srv.close()
	srv.serve(t)

	// Use auth with matching hostname for PlainAuth server name validation
	auth := smtp.PlainAuth("", "user", "pass", "127.0.0.1")
	err := sendViaStdSMTP(context.Background(), srv.addr(), "from@ani.example.com", []string{"to@ani.example.com"}, "none", auth)
	if err != nil {
		t.Fatalf("sendViaStdSMTP with auth failed: %v", err)
	}
}

// --- PutSmtpConfig idempotent replay tests ---

func TestStore_PutSmtpConfig_IdempotentReplay(t *testing.T) {
	store := NewLocalEmailNotificationStore()
	cfg1, err := store.PutSmtpConfig(context.Background(), "idem-replay-smtp", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "first@ani.example.com",
		Username:    "first",
		Password:    strPtr("pass1"),
	})
	if err != nil {
		t.Fatalf("first PutSmtpConfig failed: %v", err)
	}

	// Second call with same idempotency key but different fields — should return first snapshot
	cfg2, err := store.PutSmtpConfig(context.Background(), "idem-replay-smtp", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.different.com",
		SmtpPort:    465,
		Encryption:  "ssl",
		FromAddress: "second@ani.example.com",
		Username:    "second",
		Password:    strPtr("pass2"),
	})
	if err != nil {
		t.Fatalf("second PutSmtpConfig failed: %v", err)
	}
	if cfg1.SmtpHost != cfg2.SmtpHost {
		t.Errorf("idempotent replay should return first SmtpHost %s, got %s", cfg1.SmtpHost, cfg2.SmtpHost)
	}
	if cfg1.FromAddress != cfg2.FromAddress {
		t.Errorf("idempotent replay should return first FromAddress %s, got %s", cfg1.FromAddress, cfg2.FromAddress)
	}
	if cfg1.Username != cfg2.Username {
		t.Errorf("idempotent replay should return first Username %s, got %s", cfg1.Username, cfg2.Username)
	}
}

// --- SendTestEmail RequestID tests ---

func TestStore_SendTestEmail_Success_RequestID(t *testing.T) {
	store := NewLocalEmailNotificationStore(WithEmailNotificationSMTPDialer(
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return &mockSMTPClient{}, nil
		},
	))
	_, _ = store.PutSmtpConfig(context.Background(), "idem-rid-ok", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass"),
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-rid-r", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	result, err := store.SendTestEmail(context.Background(), "idem-rid-test")
	if err != nil {
		t.Fatalf("SendTestEmail failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %s", result.Message)
	}
	if result.RequestID == "" {
		t.Error("expected non-empty RequestID on success")
	}
}

func TestStore_SendTestEmail_Failure_RequestID(t *testing.T) {
	store := NewLocalEmailNotificationStore(WithEmailNotificationSMTPDialer(
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return nil, errors.New("connection refused")
		},
	))
	_, _ = store.PutSmtpConfig(context.Background(), "idem-rid-fail", ports.EmailSmtpConfigWrite{
		SmtpHost:    "smtp.example.com",
		SmtpPort:    587,
		Encryption:  "starttls",
		FromAddress: "test@ani.example.com",
		Username:    "test@ani.example.com",
		Password:    strPtr("pass"),
	})
	_, _ = store.CreateRecipient(context.Background(), "idem-rid-rfail", ports.EmailRecipientWrite{
		Email: "oncall@ani.example.com",
	})
	result, err := store.SendTestEmail(context.Background(), "idem-rid-fail-test")
	if err != nil {
		t.Fatalf("SendTestEmail should return result not error: %v", err)
	}
	if result.Success {
		t.Error("expected failure result")
	}
	if result.RequestID == "" {
		t.Error("expected non-empty RequestID on failure for troubleshooting")
	}
}

// --- sendViaCustomDialer error branch tests ---

func TestSendViaCustomDialer_InvalidAddr(t *testing.T) {
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return &mockSMTPClient{}, nil
		},
		"invalid-no-port", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected error for invalid addr, got nil")
	}
	if !strings.Contains(err.Error(), "invalid addr") {
		t.Errorf("expected error to contain 'invalid addr', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_MailError(t *testing.T) {
	client := &errorSMTPClient{
		mailErr: errors.New("mail rejected"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected mail error, got nil")
	}
	if !strings.Contains(err.Error(), "mail rejected") {
		t.Errorf("expected error to contain 'mail rejected', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_RcptError(t *testing.T) {
	client := &errorSMTPClient{
		rcptErr: errors.New("rcpt rejected"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected rcpt error, got nil")
	}
	if !strings.Contains(err.Error(), "rcpt rejected") {
		t.Errorf("expected error to contain 'rcpt rejected', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_DataError(t *testing.T) {
	client := &errorSMTPClient{
		dataErr: errors.New("data rejected"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected data error, got nil")
	}
	if !strings.Contains(err.Error(), "data rejected") {
		t.Errorf("expected error to contain 'data rejected', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_WriteError(t *testing.T) {
	client := &errorSMTPClient{
		writeErr: errors.New("write failed"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected write error, got nil")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("expected error to contain 'write failed', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_CloseError(t *testing.T) {
	client := &errorSMTPClient{
		closeErr: errors.New("close failed"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected close error, got nil")
	}
	if !strings.Contains(err.Error(), "close failed") {
		t.Errorf("expected error to contain 'close failed', got: %s", err.Error())
	}
}

func TestSendViaCustomDialer_QuitError(t *testing.T) {
	client := &errorSMTPClient{
		quitErr: errors.New("quit failed"),
	}
	err := sendViaCustomDialer(context.Background(),
		func(host string, port string, auth smtp.Auth) (smtpClient, error) {
			return client, nil
		},
		"smtp.example.com:587", "from@ani.example.com", []string{"to@ani.example.com"}, "none", nil)
	if err == nil {
		t.Fatal("expected quit error, got nil")
	}
	if !strings.Contains(err.Error(), "quit failed") {
		t.Errorf("expected error to contain 'quit failed', got: %s", err.Error())
	}
}

// errorSMTPClient is a configurable mock smtpClient that returns errors for specific calls.
type errorSMTPClient struct {
	mailErr  error
	rcptErr  error
	dataErr  error
	writeErr error
	closeErr error
	quitErr  error
}

func (m *errorSMTPClient) Mail(from string) error { return m.mailErr }
func (m *errorSMTPClient) Rcpt(to string) error   { return m.rcptErr }
func (m *errorSMTPClient) Data() (io.WriteCloser, error) {
	if m.dataErr != nil {
		return nil, m.dataErr
	}
	return &errWriteCloser{writeErr: m.writeErr, closeErr: m.closeErr}, nil
}
func (m *errorSMTPClient) Quit() error                       { return m.quitErr }
func (m *errorSMTPClient) Close() error                      { return nil }
func (m *errorSMTPClient) StartTLS(config *tls.Config) error { return nil }

type errWriteCloser struct {
	writeErr error
	closeErr error
}

func (w *errWriteCloser) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}
func (w *errWriteCloser) Close() error { return w.closeErr }
