package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalNotificationServiceReturnsUnsetChannelWhenNeverConfigured(t *testing.T) {
	service := NewLocalNotificationService(WithNotificationClock(func() time.Time {
		return time.Unix(2200, 0).UTC()
	}))

	rec, err := service.GetEmailChannel(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("GetEmailChannel() error = %v", err)
	}
	if rec.ID != "platform-default" {
		t.Fatalf("id = %q, want platform-default", rec.ID)
	}
	if rec.State != ports.EmailChannelDisabled {
		t.Fatalf("state = %q, want disabled", rec.State)
	}
	if rec.HasPassword {
		t.Fatalf("has_password = true, want false when never configured")
	}
	if rec.DevProfile.Mode != "local" || rec.DevProfile.RealProvider {
		t.Fatalf("dev profile = %+v, want local non-real marker", rec.DevProfile)
	}
}

func TestLocalNotificationServiceUpdateEmailChannelPersistsPasswordWriteOnly(t *testing.T) {
	service := NewLocalNotificationService(WithNotificationClock(func() time.Time {
		return time.Unix(2200, 0).UTC()
	}))
	ctx := context.Background()

	first, err := service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch-create",
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
	if !first.HasPassword {
		t.Fatalf("has_password = false, want true after password set")
	}
	if first.State != ports.EmailChannelActive {
		t.Fatalf("state = %q, want active", first.State)
	}

	// 幂等：重复 idempotency_key 返回同一记录
	dup, err := service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch-create",
		Host:           "smtp.example.com",
		Port:           587,
		Encryption:     ports.EmailChannelEncryptionStartTLS,
		Username:       "alerting@example.com",
		Password:       "ignored",
		FromAddress:    "alerting@example.com",
	})
	if err != nil {
		t.Fatalf("UpdateEmailChannel(dup) error = %v", err)
	}
	if dup.UpdatedAt != first.UpdatedAt {
		t.Fatalf("dup updated_at = %v, want %v (idempotent)", dup.UpdatedAt, first.UpdatedAt)
	}

	// 更新留空保留密码
	updated, err := service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch-update-host",
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
	if updated.Host != "smtp2.example.com" {
		t.Fatalf("host = %q, want smtp2.example.com", updated.Host)
	}
	if !updated.HasPassword {
		t.Fatalf("has_password = false after blank-password update; want preserved true")
	}
}

func TestLocalNotificationServiceRejectsInvalidChannelInput(t *testing.T) {
	service := NewLocalNotificationService()
	ctx := context.Background()

	cases := []struct {
		name string
		req  ports.EmailChannelUpdateRequest
	}{
		{
			name: "missing tenant_id",
			req: ports.EmailChannelUpdateRequest{
				IdempotencyKey: "k", Host: "h", Port: 587,
				Encryption: ports.EmailChannelEncryptionStartTLS,
				Username:   "u", FromAddress: "f@example.com",
			},
		},
		{
			name: "missing idempotency_key",
			req: ports.EmailChannelUpdateRequest{
				TenantID: "t", Host: "h", Port: 587,
				Encryption: ports.EmailChannelEncryptionStartTLS,
				Username:   "u", FromAddress: "f@example.com",
			},
		},
		{
			name: "invalid port",
			req: ports.EmailChannelUpdateRequest{
				TenantID: "t", IdempotencyKey: "k", Host: "h", Port: 99999,
				Encryption: ports.EmailChannelEncryptionStartTLS,
				Username:   "u", FromAddress: "f@example.com",
			},
		},
		{
			name: "unsupported encryption",
			req: ports.EmailChannelUpdateRequest{
				TenantID: "t", IdempotencyKey: "k", Host: "h", Port: 587,
				Encryption: "bogus", Username: "u", FromAddress: "f@example.com",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.UpdateEmailChannel(ctx, tc.req)
			if !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLocalNotificationServiceManagesRecipientsCRUD(t *testing.T) {
	service := NewLocalNotificationService(WithNotificationClock(func() time.Time {
		return time.Unix(2300, 0).UTC()
	}))
	ctx := context.Background()

	created, err := service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "rcp-1",
		Email:          "oncall@example.com",
		DisplayName:    "Oncall",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("CreateEmailRecipient error = %v", err)
	}
	if created.ID == "" || !strings.HasPrefix(created.ID, "rcp-") {
		t.Fatalf("id = %q, want rcp- prefixed", created.ID)
	}
	if created.State != ports.EmailRecipientActive {
		t.Fatalf("state = %q, want active", created.State)
	}

	// 幂等：重复 idempotency_key 返回同一记录
	dup, err := service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "rcp-1",
		Email:          "oncall@example.com",
	})
	if err != nil {
		t.Fatalf("CreateEmailRecipient(dup) error = %v", err)
	}
	if dup.ID != created.ID {
		t.Fatalf("dup id = %q, want %q", dup.ID, created.ID)
	}

	// email 冲突返回 409
	_, err = service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "rcp-2",
		Email:          "oncall@example.com",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("duplicate email err = %v, want ErrConflict", err)
	}

	// 列出
	list, _, err := service.ListEmailRecipients(ctx, ports.EmailRecipientListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("ListEmailRecipients error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	// 部分更新
	updated, err := service.UpdateEmailRecipient(ctx, ports.EmailRecipientUpdateRequest{
		TenantID:       "tenant-a",
		RecipientID:    created.ID,
		IdempotencyKey: "rcp-update",
		DisplayName:    "Oncall Rotation",
		Enabled:        boolPtr(false),
	})
	if err != nil {
		t.Fatalf("UpdateEmailRecipient error = %v", err)
	}
	if updated.DisplayName != "Oncall Rotation" || updated.Enabled {
		t.Fatalf("updated = %+v, want new name + disabled", updated)
	}

	// 过滤 enabled=false
	disabledOnly, _, err := service.ListEmailRecipients(ctx, ports.EmailRecipientListRequest{
		TenantID: "tenant-a",
		Enabled:  boolPtr(false),
	})
	if err != nil {
		t.Fatalf("ListEmailRecipients(filter) error = %v", err)
	}
	if len(disabledOnly) != 1 {
		t.Fatalf("disabled list len = %d, want 1", len(disabledOnly))
	}

	// 软删除
	if err := service.DeleteEmailRecipient(ctx, ports.EmailRecipientGetRequest{
		TenantID: "tenant-a", RecipientID: created.ID,
	}); err != nil {
		t.Fatalf("DeleteEmailRecipient error = %v", err)
	}
	// 再获取应 404
	_, err = service.GetEmailRecipient(ctx, ports.EmailRecipientGetRequest{
		TenantID: "tenant-a", RecipientID: created.ID,
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetEmailRecipient after delete err = %v, want ErrNotFound", err)
	}
}

func TestLocalNotificationServiceEventsCatalogReturnsFrozenList(t *testing.T) {
	service := NewLocalNotificationService()
	events, err := service.ListNotificationEvents(context.Background())
	if err != nil {
		t.Fatalf("ListNotificationEvents error = %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("events len = %d, want 5 (first frozen set)", len(events))
	}
	seen := map[ports.NotificationEventType]bool{}
	for _, ev := range events {
		seen[ev.EventType] = true
	}
	for _, want := range []ports.NotificationEventType{
		ports.NotificationEventPlatformAlertP0,
		ports.NotificationEventPlatformAlertP1,
		ports.NotificationEventIncidentCreated,
		ports.NotificationEventIncidentEscalated,
		ports.NotificationEventPlatformCriticalTaskFailed,
	} {
		if !seen[want] {
			t.Fatalf("event %q not in catalog", want)
		}
	}
}

func TestLocalNotificationServiceSubscriptionsUpsertAndList(t *testing.T) {
	service := NewLocalNotificationService(WithNotificationClock(func() time.Time {
		return time.Unix(2400, 0).UTC()
	}))
	ctx := context.Background()

	// 初始全部为 false
	initial, err := service.ListEmailSubscriptions(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("ListEmailSubscriptions error = %v", err)
	}
	for _, sub := range initial {
		if sub.Enabled {
			t.Fatalf("event %q initially enabled, want false", sub.EventType)
		}
	}

	// upsert
	updated, err := service.UpdateEmailSubscriptions(ctx, ports.EmailSubscriptionUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "sub-batch-1",
		Items: []ports.EmailSubscriptionUpdateItem{
			{EventType: ports.NotificationEventPlatformAlertP0, Enabled: true},
			{EventType: ports.NotificationEventIncidentCreated, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("UpdateEmailSubscriptions error = %v", err)
	}
	enabledCount := 0
	for _, sub := range updated {
		if sub.Enabled {
			enabledCount++
		}
	}
	if enabledCount != 2 {
		t.Fatalf("enabled = %d, want 2", enabledCount)
	}

	// 持久化
	persisted, err := service.ListEmailSubscriptions(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("ListEmailSubscriptions(persisted) error = %v", err)
	}
	for _, sub := range persisted {
		if sub.EventType == ports.NotificationEventPlatformAlertP0 && !sub.Enabled {
			t.Fatalf("platform_alert_p0 not persisted as enabled")
		}
	}

	// 不支持的 event_type 返回 400
	_, err = service.UpdateEmailSubscriptions(ctx, ports.EmailSubscriptionUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "sub-bad",
		Items: []ports.EmailSubscriptionUpdateItem{
			{EventType: "bogus_event", Enabled: true},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("unsupported event err = %v, want ErrInvalid", err)
	}
}

func TestLocalNotificationServiceTestSendChecksPreconditions(t *testing.T) {
	ctx := context.Background()

	// 无通道
	service := NewLocalNotificationService()
	_, err := service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "test-1",
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("no channel err = %v, want ErrFailedPrecondition", err)
	}

	// 有通道无收件人
	service = NewLocalNotificationService()
	_, _ = service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "ch",
		Host:           "smtp.example.com",
		Port:           587,
		Encryption:     ports.EmailChannelEncryptionStartTLS,
		Username:       "alerting@example.com",
		Password:       "secret",
		FromAddress:    "alerting@example.com",
		State:          ports.EmailChannelActive,
	})
	_, err = service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "test-2",
	})
	if !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("no recipients err = %v, want ErrFailedPrecondition", err)
	}

	// 有通道 + 有收件人，入队成功
	_, _ = service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "rcp",
		Email:          "oncall@example.com",
		Enabled:        true,
	})
	result, err := service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "test-3",
	})
	if err != nil {
		t.Fatalf("SendEmailTest error = %v", err)
	}
	if result.Status != ports.EmailTestSendAccepted {
		t.Fatalf("status = %q, want accepted", result.Status)
	}
	if len(result.AcceptedRecipientIDs) != 1 {
		t.Fatalf("accepted = %d, want 1", len(result.AcceptedRecipientIDs))
	}
	if result.RequestID == "" || !strings.HasPrefix(result.RequestID, "req-") {
		t.Fatalf("request_id = %q, want req- prefixed", result.RequestID)
	}

	// 指定不存在的 recipient_id 返回 404
	_, err = service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "test-4",
		RecipientIDs:   []string{"rcp-not-exists"},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown recipient err = %v, want ErrNotFound", err)
	}
}
