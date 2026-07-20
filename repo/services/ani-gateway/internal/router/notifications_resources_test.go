package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestNotificationAPIReturnsDisabledChannelWhenNeverConfigured(t *testing.T) {
	api := newNotificationAPI()

	rec, err := api.service.GetEmailChannel(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("GetEmailChannel error = %v", err)
	}
	response := emailChannelFromRecord(rec)
	if response.ID != "platform-default" {
		t.Fatalf("id = %q, want platform-default", response.ID)
	}
	if response.State != "disabled" {
		t.Fatalf("state = %q, want disabled", response.State)
	}
	if response.HasPassword {
		t.Fatalf("has_password = true, want false when never configured")
	}
	requireLocalCoreDevProfile(t, response.DevProfile, "local-notification-service")
}

func TestNotificationAPIChannelUpdatePreservesPasswordWhenBlank(t *testing.T) {
	api := newNotificationAPI()
	ctx := context.Background()

	first, err := api.service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch-1",
		Host:           "smtp.example.com",
		Port:           587,
		Encryption:     ports.EmailChannelEncryptionStartTLS,
		Username:       "alerting@example.com",
		Password:       "secret",
		FromAddress:    "alerting@example.com",
		State:          ports.EmailChannelActive,
	})
	if err != nil {
		t.Fatalf("UpdateEmailChannel(first) error = %v", err)
	}
	firstResponse := emailChannelFromRecord(first)
	if !firstResponse.HasPassword {
		t.Fatalf("has_password = false, want true")
	}

	updated, err := api.service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch-2",
		Host:           "smtp2.example.com",
		Port:           587,
		Encryption:     ports.EmailChannelEncryptionStartTLS,
		Username:       "alerting@example.com",
		FromAddress:    "alerting@example.com",
		State:          ports.EmailChannelActive,
	})
	if err != nil {
		t.Fatalf("UpdateEmailChannel(update) error = %v", err)
	}
	updatedResponse := emailChannelFromRecord(updated)
	if updatedResponse.Host != "smtp2.example.com" {
		t.Fatalf("host = %q, want smtp2.example.com", updatedResponse.Host)
	}
	if !updatedResponse.HasPassword {
		t.Fatalf("has_password = false after blank update; want preserved true")
	}
}

func TestNotificationAPIRecipientCRUDFlow(t *testing.T) {
	api := newNotificationAPI()
	ctx := context.Background()

	created, err := api.service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "rcp-1",
		Email:          "oncall@example.com",
		DisplayName:    "Oncall",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("CreateEmailRecipient error = %v", err)
	}
	createdResponse := emailRecipientFromRecord(created)
	if createdResponse.Email != "oncall@example.com" || createdResponse.State != "active" {
		t.Fatalf("created = %+v, want active oncall@example.com", createdResponse)
	}
	requireLocalCoreDevProfile(t, createdResponse.DevProfile, "local-notification-service")
}

func TestNotificationAPIEventsCatalogReturnsFiveFrozen(t *testing.T) {
	api := newNotificationAPI()
	items, err := api.service.ListNotificationEvents(context.Background())
	if err != nil {
		t.Fatalf("ListNotificationEvents error = %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("events = %d, want 5", len(items))
	}
}

func TestNotificationAPISubscriptionsUpsert(t *testing.T) {
	api := newNotificationAPI()
	ctx := context.Background()

	updated, err := api.service.UpdateEmailSubscriptions(ctx, ports.EmailSubscriptionUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "sub-1",
		Items: []ports.EmailSubscriptionUpdateItem{
			{EventType: ports.NotificationEventPlatformAlertP0, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("UpdateEmailSubscriptions error = %v", err)
	}
	enabled := 0
	for _, rec := range updated {
		if rec.Enabled {
			enabled++
		}
	}
	if enabled != 1 {
		t.Fatalf("enabled count = %d, want 1", enabled)
	}
}

func TestNotificationAPITestSendPreconditions(t *testing.T) {
	api := newNotificationAPI()
	ctx := context.Background()

	// 无通道
	_, err := api.service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "test-1",
	})
	if err == nil {
		t.Fatalf("expected error when channel not configured")
	}
}
