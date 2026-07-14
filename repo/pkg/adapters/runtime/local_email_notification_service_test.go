package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// ── SMTP config ──────────────────────────────────────────────────────────

func TestLocalEmailNotificationService_PutSmtpConfig_RequiresIdempotencyKey(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		SmtpHost: "smtp.example.com", SmtpPort: 465, Encryption: "ssl",
		FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_RequiresHost(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpPort: 465, Encryption: "ssl",
		FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_RequiresPasswordOrAuthCode(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_RejectsBadEncryption(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "tls", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_Success_PreservesPassword(t *testing.T) {
	s := NewLocalEmailNotificationService()
	rec, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !rec.Configured {
		t.Fatal("Configured should be true")
	}
	if !rec.PasswordConfigured {
		t.Fatal("PasswordConfigured should be true")
	}
	if rec.Password != "" {
		t.Fatal("Password should not be echoed")
	}
	if rec.AuthCode != "" {
		t.Fatal("AuthCode should not be echoed")
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_IdempotentRetry(t *testing.T) {
	s := NewLocalEmailNotificationService()
	req := ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	}
	rec1, err := s.PutSmtpConfig(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	rec2, err := s.PutSmtpConfig(context.Background(), req)
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if rec1 != rec2 {
		t.Fatal("idempotent retry should return same record")
	}
}

func TestLocalEmailNotificationService_PutSmtpConfig_PreservesPasswordOnEmptyUpdate(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Update with empty password — should preserve previous password
	rec, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k2", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !rec.PasswordConfigured {
		t.Fatal("PasswordConfigured should remain true after empty update")
	}
}

func TestLocalEmailNotificationService_GetSmtpConfig_NotFound(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.GetSmtpConfig(context.Background())
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ── Recipients ───────────────────────────────────────────────────────────

func TestLocalEmailNotificationService_CreateRecipient_Success(t *testing.T) {
	s := NewLocalEmailNotificationService()
	rec, err := s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com", Label: "User",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if rec.ID == "" {
		t.Fatal("ID should not be empty")
	}
	if !rec.Enabled {
		t.Fatal("Enabled should default to true")
	}
}

func TestLocalEmailNotificationService_CreateRecipient_DuplicateEmail(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	_, err = s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r2", Email: "user@example.com",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestLocalEmailNotificationService_CreateRecipient_IdempotentRetry(t *testing.T) {
	s := NewLocalEmailNotificationService()
	req := ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com", Label: "User",
	}
	rec1, err := s.CreateRecipient(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	rec2, err := s.CreateRecipient(context.Background(), req)
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if rec1.ID != rec2.ID {
		t.Fatal("idempotent retry should return same recipient")
	}
}

func TestLocalEmailNotificationService_UpdateRecipient_ChangeEmail(t *testing.T) {
	s := NewLocalEmailNotificationService()
	rec, err := s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "old@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	updated, err := s.UpdateRecipient(context.Background(), ports.EmailRecipientUpdateRequest{
		IdempotencyKey: "u1", ID: rec.ID, Email: "new@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if updated.Email != "new@example.com" {
		t.Fatalf("Email = %q, want new@example.com", updated.Email)
	}
}

func TestLocalEmailNotificationService_UpdateRecipient_NotFound(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.UpdateRecipient(context.Background(), ports.EmailRecipientUpdateRequest{
		IdempotencyKey: "u1", ID: "nonexistent", Email: "new@example.com",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLocalEmailNotificationService_DeleteRecipient_Success(t *testing.T) {
	s := NewLocalEmailNotificationService()
	rec, err := s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	err = s.DeleteRecipient(context.Background(), ports.EmailRecipientDeleteRequest{ID: rec.ID})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	recs, _ := s.ListRecipients(context.Background())
	if len(recs) != 0 {
		t.Fatal("recipients should be empty after delete")
	}
}

func TestLocalEmailNotificationService_DeleteRecipient_NotFound(t *testing.T) {
	s := NewLocalEmailNotificationService()
	err := s.DeleteRecipient(context.Background(), ports.EmailRecipientDeleteRequest{ID: "nonexistent"})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ── Subscriptions ─────────────────────────────────────────────────────────

func TestLocalEmailNotificationService_ListSubscriptions_DefaultAllDisabled(t *testing.T) {
	s := NewLocalEmailNotificationService()
	subs, err := s.ListSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(subs) != 5 {
		t.Fatalf("len = %d, want 5", len(subs))
	}
	for _, sub := range subs {
		if sub.Enabled {
			t.Fatalf("event %s should be disabled by default", sub.EventType)
		}
	}
}

func TestLocalEmailNotificationService_PutSubscriptions_Success(t *testing.T) {
	s := NewLocalEmailNotificationService()
	subs, err := s.PutSubscriptions(context.Background(), ports.EmailSubscriptionsPutRequest{
		IdempotencyKey: "s1",
		Subscriptions: []ports.EmailSubscriptionRecord{
			{EventType: "platform_alert_p0", Enabled: true},
			{EventType: "incident_created", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	enabledCount := 0
	for _, sub := range subs {
		if sub.Enabled {
			enabledCount++
		}
	}
	if enabledCount != 2 {
		t.Fatalf("enabled count = %d, want 2", enabledCount)
	}
}

func TestLocalEmailNotificationService_PutSubscriptions_RejectsInvalidEvent(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSubscriptions(context.Background(), ports.EmailSubscriptionsPutRequest{
		IdempotencyKey: "s1",
		Subscriptions: []ports.EmailSubscriptionRecord{
			{EventType: "unknown_event", Enabled: true},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestLocalEmailNotificationService_PutSubscriptions_IdempotentRetry(t *testing.T) {
	s := NewLocalEmailNotificationService()
	req := ports.EmailSubscriptionsPutRequest{
		IdempotencyKey: "s1",
		Subscriptions: []ports.EmailSubscriptionRecord{
			{EventType: "platform_alert_p0", Enabled: true},
		},
	}
	subs1, err := s.PutSubscriptions(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	subs2, err := s.PutSubscriptions(context.Background(), req)
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if len(subs1) != len(subs2) {
		t.Fatal("idempotent retry should return same list length")
	}
}

// ── Idempotency key namespacing ────────────────────────────────────────────

func TestLocalEmailNotificationService_IdempotencyKeyNamespacedAcrossOperations(t *testing.T) {
	s := NewLocalEmailNotificationService()
	// Use the same idempotency key for two different operations
	// PutSmtpConfig should succeed
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "shared-key", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("PutSmtpConfig err = %v", err)
	}
	// PutSubscriptions with same key should NOT be treated as idempotent retry
	subs, err := s.PutSubscriptions(context.Background(), ports.EmailSubscriptionsPutRequest{
		IdempotencyKey: "shared-key",
		Subscriptions: []ports.EmailSubscriptionRecord{
			{EventType: "platform_alert_p0", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("PutSubscriptions err = %v", err)
	}
	// Verify the subscription was actually applied
	found := false
	for _, sub := range subs {
		if sub.EventType == "platform_alert_p0" && sub.Enabled {
			found = true
		}
	}
	if !found {
		t.Fatal("subscription was not applied — idempotency key collision prevented update")
	}
}

// ── Test send ────────────────────────────────────────────────────────────

func TestLocalEmailNotificationService_SendTestEmail_NoSmtpConfig(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.SendTestEmail(context.Background(), ports.EmailTestSendRequest{
		IdempotencyKey: "t1",
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("err = %v, want ErrFailedPrecondition", err)
	}
}

func TestLocalEmailNotificationService_SendTestEmail_NoRecipients(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, err := s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	_, err = s.SendTestEmail(context.Background(), ports.EmailTestSendRequest{
		IdempotencyKey: "t1",
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("err = %v, want ErrFailedPrecondition", err)
	}
}

func TestLocalEmailNotificationService_SendTestEmail_SimulatedSuccess(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, _ = s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	_, _ = s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com",
	})
	result, err := s.SendTestEmail(context.Background(), ports.EmailTestSendRequest{
		IdempotencyKey: "t1",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Status != "sent" {
		t.Fatalf("status = %q, want sent", result.Status)
	}
	if !strings.Contains(result.ToEmails, "user@example.com") {
		t.Fatalf("ToEmails = %q, should contain user@example.com", result.ToEmails)
	}
}

func TestLocalEmailNotificationService_SendTestEmail_IdempotentRetry(t *testing.T) {
	s := NewLocalEmailNotificationService()
	_, _ = s.PutSmtpConfig(context.Background(), ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: "k1", SmtpHost: "smtp.example.com", SmtpPort: 465,
		Encryption: "ssl", FromAddress: "from@example.com", Username: "from@example.com",
		Password: "secret",
	})
	_, _ = s.CreateRecipient(context.Background(), ports.EmailRecipientCreateRequest{
		IdempotencyKey: "r1", Email: "user@example.com",
	})
	req := ports.EmailTestSendRequest{IdempotencyKey: "t1"}
	r1, _ := s.SendTestEmail(context.Background(), req)
	r2, _ := s.SendTestEmail(context.Background(), req)
	if r1 != r2 {
		t.Fatal("idempotent retry should return same result")
	}
}

// ── Events ────────────────────────────────────────────────────────────────

func TestLocalEmailNotificationService_ListEvents_Returns5Events(t *testing.T) {
	s := NewLocalEmailNotificationService()
	events, err := s.ListEvents(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len = %d, want 5", len(events))
	}
}
