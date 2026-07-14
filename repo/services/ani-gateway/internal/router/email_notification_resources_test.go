package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// ── newEmailNotificationAPI wiring ──────────────────────────────────────────

func TestNewEmailNotificationAPI_DefaultsToLocalService(t *testing.T) {
	api := newEmailNotificationAPI()
	if api == nil {
		t.Fatal("api should not be nil")
	}
	if api.service == nil {
		t.Fatal("service should not be nil")
	}
	// Verify it's functional by listing events
	events, err := api.service.ListEvents(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len = %d, want 5", len(events))
	}
}

func TestNewEmailNotificationAPIWithService_UsesProvidedService(t *testing.T) {
	custom := &fakeEmailNotificationService{}
	api := newEmailNotificationAPIWithService(custom)
	if api.service != custom {
		t.Fatal("service should be the custom service")
	}
}

// ── smtpConfigToResponse ─────────────────────────────────────────────────────

func TestSmtpConfigToResponse_DoesNotEchoPassword(t *testing.T) {
	rec := ports.EmailSmtpConfigRecord{
		SmtpHost: "smtp.example.com", SmtpPort: 465, Encryption: "ssl",
		FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret", AuthCode: "authcode",
		PasswordConfigured: true, AuthCodeConfigured: true,
		Configured: true,
	}
	resp := smtpConfigToResponse(rec)
	if _, hasPassword := resp["password"]; hasPassword {
		t.Fatal("password should not be in response")
	}
	if _, hasAuthCode := resp["auth_code"]; hasAuthCode {
		t.Fatal("auth_code should not be in response")
	}
	if resp["password_configured"] != true {
		t.Fatal("password_configured should be true")
	}
	if resp["auth_code_configured"] != true {
		t.Fatal("auth_code_configured should be true")
	}
	if resp["configured"] != true {
		t.Fatal("configured should be true")
	}
}

func TestSmtpConfigToResponse_Fields(t *testing.T) {
	rec := ports.EmailSmtpConfigRecord{
		SmtpHost: "smtp.example.com", SmtpPort: 587, Encryption: "starttls",
		FromAddress: "from@example.com", Username: "user@example.com",
		Configured: true,
	}
	resp := smtpConfigToResponse(rec)
	if resp["smtp_host"] != "smtp.example.com" {
		t.Fatalf("smtp_host = %v", resp["smtp_host"])
	}
	if resp["smtp_port"] != 587 {
		t.Fatalf("smtp_port = %v", resp["smtp_port"])
	}
	if resp["encryption"] != "starttls" {
		t.Fatalf("encryption = %v", resp["encryption"])
	}
	if resp["from_address"] != "from@example.com" {
		t.Fatalf("from_address = %v", resp["from_address"])
	}
	if resp["username"] != "user@example.com" {
		t.Fatalf("username = %v", resp["username"])
	}
}

// ── recipientToResponse ─────────────────────────────────────────────────────

func TestRecipientToResponse_NilLabel(t *testing.T) {
	rec := ports.EmailRecipientRecord{
		ID: "r1", Email: "user@example.com", Label: "",
		Enabled: true,
	}
	resp := recipientToResponse(rec)
	if resp["label"] != nil {
		t.Fatalf("label = %v, want nil for empty label", resp["label"])
	}
}

func TestRecipientToResponse_NonEmptyLabel(t *testing.T) {
	rec := ports.EmailRecipientRecord{
		ID: "r1", Email: "user@example.com", Label: "User Name",
		Enabled: true,
	}
	resp := recipientToResponse(rec)
	if resp["label"] != "User Name" {
		t.Fatalf("label = %v, want User Name", resp["label"])
	}
}

func TestRecipientToResponse_HasRequiredFields(t *testing.T) {
	rec := ports.EmailRecipientRecord{
		ID: "r1", Email: "user@example.com", Label: "User",
		Enabled: true,
	}
	resp := recipientToResponse(rec)
	if resp["id"] != "r1" {
		t.Fatalf("id = %v", resp["id"])
	}
	if resp["email"] != "user@example.com" {
		t.Fatalf("email = %v", resp["email"])
	}
	if resp["enabled"] != true {
		t.Fatalf("enabled = %v", resp["enabled"])
	}
	if resp["created_at"] == nil {
		t.Fatal("created_at should not be nil")
	}
}

// ── Fake service for handler wiring tests ───────────────────────────────────

type fakeEmailNotificationService struct{}

func (f *fakeEmailNotificationService) GetSmtpConfig(ctx context.Context) (ports.EmailSmtpConfigRecord, error) {
	return ports.EmailSmtpConfigRecord{}, nil
}

func (f *fakeEmailNotificationService) PutSmtpConfig(ctx context.Context, req ports.EmailSmtpConfigPutRequest) (ports.EmailSmtpConfigRecord, error) {
	return ports.EmailSmtpConfigRecord{}, nil
}

func (f *fakeEmailNotificationService) ListRecipients(ctx context.Context) ([]ports.EmailRecipientRecord, error) {
	return nil, nil
}

func (f *fakeEmailNotificationService) CreateRecipient(ctx context.Context, req ports.EmailRecipientCreateRequest) (ports.EmailRecipientRecord, error) {
	return ports.EmailRecipientRecord{}, nil
}

func (f *fakeEmailNotificationService) UpdateRecipient(ctx context.Context, req ports.EmailRecipientUpdateRequest) (ports.EmailRecipientRecord, error) {
	return ports.EmailRecipientRecord{}, nil
}

func (f *fakeEmailNotificationService) DeleteRecipient(ctx context.Context, req ports.EmailRecipientDeleteRequest) error {
	return nil
}

func (f *fakeEmailNotificationService) ListSubscriptions(ctx context.Context) ([]ports.EmailSubscriptionRecord, error) {
	return nil, nil
}

func (f *fakeEmailNotificationService) PutSubscriptions(ctx context.Context, req ports.EmailSubscriptionsPutRequest) ([]ports.EmailSubscriptionRecord, error) {
	return nil, nil
}

func (f *fakeEmailNotificationService) ListEvents(ctx context.Context) ([]ports.EmailEventInfoRecord, error) {
	return nil, nil
}

func (f *fakeEmailNotificationService) SendTestEmail(ctx context.Context, req ports.EmailTestSendRequest) (ports.EmailTestSendResult, error) {
	return ports.EmailTestSendResult{}, nil
}
