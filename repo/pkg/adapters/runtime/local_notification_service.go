package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// LocalNotificationService 实现 ports.NotificationService 的 local profile 版本。
// 所有数据保存在内存，不与真实 SMTP 服务器通信；DevProfile 固定 Mode=local、RealProvider=false。
type LocalNotificationService struct {
	mu            sync.Mutex
	now           func() time.Time
	channel       *emailChannelEntry
	recipients    map[string]emailRecipientEntry
	subscriptions map[ports.NotificationEventType]ports.EmailSubscriptionRecord
	idempotency   map[string]string
}

type emailChannelEntry struct {
	record   ports.EmailChannelRecord
	password string
}

type emailRecipientEntry struct {
	record ports.EmailRecipientRecord
}

// NotificationServiceOption 配置 LocalNotificationService 的可选参数。
type NotificationServiceOption func(*LocalNotificationService)

// WithNotificationClock 注入自定义时钟，主要供测试使用。
func WithNotificationClock(now func() time.Time) NotificationServiceOption {
	return func(service *LocalNotificationService) {
		if now != nil {
			service.now = now
		}
	}
}

// NewLocalNotificationService 构造 local profile NotificationService。
func NewLocalNotificationService(options ...NotificationServiceOption) *LocalNotificationService {
	service := &LocalNotificationService{
		now:           func() time.Time { return time.Now().UTC() },
		recipients:    map[string]emailRecipientEntry{},
		subscriptions: map[ports.NotificationEventType]ports.EmailSubscriptionRecord{},
		idempotency:   map[string]string{},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// GetEmailChannel 返回平台 SMTP 通道。未配置时返回 disabled + has_password=false。
func (s *LocalNotificationService) GetEmailChannel(_ context.Context, _ string) (ports.EmailChannelRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channel == nil {
		return unsetEmailChannelRecord(s.now()), nil
	}
	return s.channel.record, nil
}

// UpdateEmailChannel 创建或更新平台 SMTP 通道。
// Password 为 write-only：留空保留原值；非空覆盖。
func (s *LocalNotificationService) UpdateEmailChannel(_ context.Context, req ports.EmailChannelUpdateRequest) (ports.EmailChannelRecord, error) {
	if err := validateEmailChannelUpdateRequest(req); err != nil {
		return ports.EmailChannelRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 幂等：重复 idempotency_key 返回同一记录
	if req.IdempotencyKey != "" {
		if existing, ok := s.lookupIdempotentChannel(req.TenantID, req.IdempotencyKey); ok {
			return existing, nil
		}
	}

	now := s.now()
	state := req.State
	if state == "" {
		state = ports.EmailChannelActive
	}

	record := ports.EmailChannelRecord{
		ID:          "platform-default",
		Host:        req.Host,
		Port:        req.Port,
		Encryption:  req.Encryption,
		Username:    req.Username,
		HasPassword: false,
		FromAddress: req.FromAddress,
		FromName:    req.FromName,
		ReplyTo:     req.ReplyTo,
		State:       state,
		DevProfile:  notificationDevProfile(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	password := ""
	if s.channel != nil {
		// 首次创建沿用既有时间戳
		record.CreatedAt = s.channel.record.CreatedAt
		// 留空保留原密码；非空覆盖
		if req.Password == "" {
			password = s.channel.password
			record.HasPassword = s.channel.record.HasPassword
			record.LastVerifiedAt = s.channel.record.LastVerifiedAt
		} else {
			password = req.Password
			record.HasPassword = true
		}
	} else if req.Password != "" {
		password = req.Password
		record.HasPassword = true
	}

	s.channel = &emailChannelEntry{record: record, password: password}
	if req.IdempotencyKey != "" {
		s.idempotency[s.channelKey(req.TenantID, req.IdempotencyKey)] = record.ID
	}
	return record, nil
}

// ListEmailRecipients 返回全局一份收件人列表，支持 enabled 过滤与 cursor 分页。
func (s *LocalNotificationService) ListEmailRecipients(_ context.Context, req ports.EmailRecipientListRequest) ([]ports.EmailRecipientRecord, string, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, "", fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(s.recipients))
	for id := range s.recipients {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	start := 0
	if req.Cursor != "" {
		idx := sort.SearchStrings(ids, req.Cursor)
		if idx < len(ids) && ids[idx] == req.Cursor {
			start = idx + 1
		} else {
			start = idx
		}
	}

	items := make([]ports.EmailRecipientRecord, 0, limit)
	nextCursor := ""
	for i := start; i < len(ids) && len(items) < limit; i++ {
		rec := s.recipients[ids[i]].record
		if req.Enabled != nil && rec.Enabled != *req.Enabled {
			continue
		}
		items = append(items, rec)
	}
	if start+len(items) < len(ids) && len(items) == limit {
		nextCursor = ids[start+len(items)-1]
	}
	return items, nextCursor, nil
}

// CreateEmailRecipient 新增收件人，email 重复返回 409。
func (s *LocalNotificationService) CreateEmailRecipient(_ context.Context, req ports.EmailRecipientCreateRequest) (ports.EmailRecipientRecord, error) {
	if err := validateRecipientCreateRequest(req); err != nil {
		return ports.EmailRecipientRecord{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.IdempotencyKey != "" {
		if existing, ok := s.lookupIdempotentRecipient(req.TenantID, req.IdempotencyKey); ok {
			return existing, nil
		}
	}

	// email 重复检查
	for _, entry := range s.recipients {
		if entry.record.State == ports.EmailRecipientActive && strings.EqualFold(entry.record.Email, req.Email) {
			return ports.EmailRecipientRecord{}, fmt.Errorf("%w: email already exists", ports.ErrConflict)
		}
	}

	now := s.now()
	rec := ports.EmailRecipientRecord{
		ID:          "rcp-" + uuid.NewString(),
		Email:       req.Email,
		DisplayName: req.DisplayName,
		Enabled:     req.Enabled,
		State:       ports.EmailRecipientActive,
		DevProfile:  notificationDevProfile(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.recipients[rec.ID] = emailRecipientEntry{record: rec}
	if req.IdempotencyKey != "" {
		s.idempotency[s.recipientKey(req.TenantID, req.IdempotencyKey)] = rec.ID
	}
	return rec, nil
}

// GetEmailRecipient 返回单个收件人；不存在返回 404。
func (s *LocalNotificationService) GetEmailRecipient(_ context.Context, req ports.EmailRecipientGetRequest) (ports.EmailRecipientRecord, error) {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.RecipientID) == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: tenant_id and recipient_id are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recipients[req.RecipientID]
	if !ok || entry.record.State == ports.EmailRecipientDeleted {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient not found", ports.ErrNotFound)
	}
	return entry.record, nil
}

// UpdateEmailRecipient 部分更新收件人。
func (s *LocalNotificationService) UpdateEmailRecipient(_ context.Context, req ports.EmailRecipientUpdateRequest) (ports.EmailRecipientRecord, error) {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.RecipientID) == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: tenant_id and recipient_id are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.IdempotencyKey != "" {
		if existing, ok := s.lookupIdempotentRecipient(req.TenantID, req.IdempotencyKey); ok && existing.ID == req.RecipientID {
			return existing, nil
		}
	}

	entry, ok := s.recipients[req.RecipientID]
	if !ok || entry.record.State == ports.EmailRecipientDeleted {
		return ports.EmailRecipientRecord{}, fmt.Errorf("%w: recipient not found", ports.ErrNotFound)
	}
	rec := entry.record
	if req.Email != "" {
		// email 改动检查冲突
		for id, other := range s.recipients {
			if id == req.RecipientID {
				continue
			}
			if other.record.State == ports.EmailRecipientActive && strings.EqualFold(other.record.Email, req.Email) {
				return ports.EmailRecipientRecord{}, fmt.Errorf("%w: email already exists", ports.ErrConflict)
			}
		}
		rec.Email = req.Email
	}
	if req.DisplayName != "" {
		rec.DisplayName = req.DisplayName
	}
	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	rec.UpdatedAt = s.now()
	s.recipients[req.RecipientID] = emailRecipientEntry{record: rec}
	if req.IdempotencyKey != "" {
		s.idempotency[s.recipientKey(req.TenantID, req.IdempotencyKey)] = rec.ID
	}
	return rec, nil
}

// DeleteEmailRecipient 软删除收件人。
func (s *LocalNotificationService) DeleteEmailRecipient(_ context.Context, req ports.EmailRecipientGetRequest) error {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.RecipientID) == "" {
		return fmt.Errorf("%w: tenant_id and recipient_id are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.recipients[req.RecipientID]
	if !ok || entry.record.State == ports.EmailRecipientDeleted {
		return fmt.Errorf("%w: recipient not found", ports.ErrNotFound)
	}
	rec := entry.record
	rec.State = ports.EmailRecipientDeleted
	rec.Enabled = false
	rec.UpdatedAt = s.now()
	s.recipients[req.RecipientID] = emailRecipientEntry{record: rec}
	return nil
}

// ListNotificationEvents 返回首期冻结的平台事件目录。
func (s *LocalNotificationService) ListNotificationEvents(_ context.Context) ([]ports.NotificationEventItem, error) {
	return defaultNotificationEvents(), nil
}

// ListEmailSubscriptions 返回当前订阅开关；未显式设置的 event_type 不返回（默认 false）。
func (s *LocalNotificationService) ListEmailSubscriptions(_ context.Context, tenantID string) ([]ports.EmailSubscriptionRecord, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := defaultNotificationEvents()
	items := make([]ports.EmailSubscriptionRecord, 0, len(events))
	for _, ev := range events {
		if rec, ok := s.subscriptions[ev.EventType]; ok {
			items = append(items, rec)
		} else {
			items = append(items, ports.EmailSubscriptionRecord{
				EventType: ev.EventType,
				Enabled:   false,
				UpdatedAt: time.Time{},
			})
		}
	}
	return items, nil
}

// UpdateEmailSubscriptions 批量 upsert 订阅开关；event_type 必须在目录内。
func (s *LocalNotificationService) UpdateEmailSubscriptions(_ context.Context, req ports.EmailSubscriptionUpdateRequest) ([]ports.EmailSubscriptionRecord, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if len(req.Items) == 0 {
		return nil, fmt.Errorf("%w: items must not be empty", ports.ErrInvalid)
	}

	validEvents := map[ports.NotificationEventType]struct{}{}
	for _, ev := range defaultNotificationEvents() {
		validEvents[ev.EventType] = struct{}{}
	}
	for _, item := range req.Items {
		if _, ok := validEvents[item.EventType]; !ok {
			return nil, fmt.Errorf("%w: unsupported event_type %q", ports.ErrInvalid, item.EventType)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for _, item := range req.Items {
		s.subscriptions[item.EventType] = ports.EmailSubscriptionRecord{
			EventType: item.EventType,
			Enabled:   item.Enabled,
			UpdatedAt: now,
		}
	}

	items := make([]ports.EmailSubscriptionRecord, 0, len(validEvents))
	for _, ev := range defaultNotificationEvents() {
		if rec, ok := s.subscriptions[ev.EventType]; ok {
			items = append(items, rec)
		} else {
			items = append(items, ports.EmailSubscriptionRecord{
				EventType: ev.EventType,
				Enabled:   false,
				UpdatedAt: time.Time{},
			})
		}
	}
	return items, nil
}

// SendEmailTest 校验前置条件并返回入队结果。本地不真实发邮件。
func (s *LocalNotificationService) SendEmailTest(_ context.Context, req ports.EmailTestSendRequest) (ports.EmailTestSendResult, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}

	s.mu.Lock()
	if s.channel == nil || !s.channel.record.HasPassword || s.channel.record.State != ports.EmailChannelActive {
		s.mu.Unlock()
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: email channel not configured or disabled", ports.ErrFailedPrecondition)
	}

	// 选择目标收件人
	var targets []ports.EmailRecipientRecord
	if len(req.RecipientIDs) > 0 {
		for _, id := range req.RecipientIDs {
			entry, ok := s.recipients[id]
			if !ok || entry.record.State == ports.EmailRecipientDeleted {
				s.mu.Unlock()
				return ports.EmailTestSendResult{}, fmt.Errorf("%w: recipient %s not found", ports.ErrNotFound, id)
			}
			if !entry.record.Enabled {
				continue
			}
			targets = append(targets, entry.record)
		}
	} else {
		for _, entry := range s.recipients {
			if entry.record.State == ports.EmailRecipientActive && entry.record.Enabled {
				targets = append(targets, entry.record)
			}
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	}
	s.mu.Unlock()

	if len(targets) == 0 {
		return ports.EmailTestSendResult{}, fmt.Errorf("%w: no enabled recipients", ports.ErrFailedPrecondition)
	}

	accepted := make([]string, 0, len(targets))
	for _, t := range targets {
		accepted = append(accepted, t.ID)
	}
	return ports.EmailTestSendResult{
		RequestID:            "req-" + uuid.NewString(),
		Status:               ports.EmailTestSendAccepted,
		AcceptedRecipientIDs: accepted,
		RejectedRecipients:   []ports.EmailTestSendRejection{},
	}, nil
}

// lookupIdempotentChannel 返回同一 idempotency_key 下的通道记录（若有）。
func (s *LocalNotificationService) lookupIdempotentChannel(tenantID, key string) (ports.EmailChannelRecord, bool) {
	id, ok := s.idempotency[s.channelKey(tenantID, key)]
	if !ok || id != "platform-default" || s.channel == nil {
		return ports.EmailChannelRecord{}, false
	}
	return s.channel.record, true
}

// lookupIdempotentRecipient 返回同一 idempotency_key 下的收件人记录（若有）。
func (s *LocalNotificationService) lookupIdempotentRecipient(tenantID, key string) (ports.EmailRecipientRecord, bool) {
	id, ok := s.idempotency[s.recipientKey(tenantID, key)]
	if !ok {
		return ports.EmailRecipientRecord{}, false
	}
	entry, ok := s.recipients[id]
	if !ok || entry.record.State == ports.EmailRecipientDeleted {
		return ports.EmailRecipientRecord{}, false
	}
	return entry.record, true
}

func (s *LocalNotificationService) channelKey(tenantID, key string) string {
	return "channel\x00" + tenantID + "\x00" + key
}

func (s *LocalNotificationService) recipientKey(tenantID, key string) string {
	return "recipient\x00" + tenantID + "\x00" + key
}

func validateEmailChannelUpdateRequest(req ports.EmailChannelUpdateRequest) error {
	if strings.TrimSpace(req.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.Host) == "" {
		return fmt.Errorf("%w: host is required", ports.ErrInvalid)
	}
	if req.Port < 1 || req.Port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ports.ErrInvalid)
	}
	switch req.Encryption {
	case ports.EmailChannelEncryptionNone, ports.EmailChannelEncryptionStartTLS, ports.EmailChannelEncryptionSSL:
	case "":
		return fmt.Errorf("%w: encryption is required", ports.ErrInvalid)
	default:
		return fmt.Errorf("%w: unsupported encryption %q", ports.ErrInvalid, req.Encryption)
	}
	if strings.TrimSpace(req.Username) == "" {
		return fmt.Errorf("%w: username is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.FromAddress) == "" {
		return fmt.Errorf("%w: from_address is required", ports.ErrInvalid)
	}
	if req.State != "" && req.State != ports.EmailChannelActive && req.State != ports.EmailChannelDisabled {
		return fmt.Errorf("%w: unsupported state %q", ports.ErrInvalid, req.State)
	}
	return nil
}

func validateRecipientCreateRequest(req ports.EmailRecipientCreateRequest) error {
	if strings.TrimSpace(req.TenantID) == "" {
		return fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency_key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(req.Email) == "" {
		return fmt.Errorf("%w: email is required", ports.ErrInvalid)
	}
	return nil
}

func unsetEmailChannelRecord(now time.Time) ports.EmailChannelRecord {
	return ports.EmailChannelRecord{
		ID:          "platform-default",
		Host:        "",
		Port:        0,
		Encryption:  "",
		Username:    "",
		HasPassword: false,
		FromAddress: "",
		FromName:    "",
		ReplyTo:     "",
		State:       ports.EmailChannelDisabled,
		DevProfile:  notificationDevProfile(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func defaultNotificationEvents() []ports.NotificationEventItem {
	return []ports.NotificationEventItem{
		{
			EventType:   ports.NotificationEventPlatformAlertP0,
			Name:        "平台告警 P0",
			Description: "平台级 P0 告警触发",
			Category:    ports.NotificationEventCategoryAlert,
			Severity:    ports.NotificationEventSeverityCritical,
		},
		{
			EventType:   ports.NotificationEventPlatformAlertP1,
			Name:        "平台告警 P1",
			Description: "平台级 P1 告警触发",
			Category:    ports.NotificationEventCategoryAlert,
			Severity:    ports.NotificationEventSeverityWarning,
		},
		{
			EventType:   ports.NotificationEventIncidentCreated,
			Name:        "Incident 创建",
			Description: "平台 Incident 事件新建",
			Category:    ports.NotificationEventCategoryIncident,
			Severity:    ports.NotificationEventSeverityWarning,
		},
		{
			EventType:   ports.NotificationEventIncidentEscalated,
			Name:        "Incident 升级",
			Description: "平台 Incident 事件升级",
			Category:    ports.NotificationEventCategoryIncident,
			Severity:    ports.NotificationEventSeverityCritical,
		},
		{
			EventType:   ports.NotificationEventPlatformCriticalTaskFailed,
			Name:        "平台关键任务失败",
			Description: "平台关键任务执行失败",
			Category:    ports.NotificationEventCategoryTask,
			Severity:    ports.NotificationEventSeverityCritical,
		},
	}
}

func notificationDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-notification-service",
		RealProvider: false,
		Reason:       "local profile records notification intent; it is not a real SMTP provider execution",
	}
}

var _ ports.NotificationService = (*LocalNotificationService)(nil)
