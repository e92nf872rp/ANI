package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type notificationAPI struct {
	service ports.NotificationService
}

// ── 请求体 ────────────────────────────────────────────────────────────────────

type emailChannelUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Encryption     string `json:"encryption"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	FromAddress    string `json:"from_address"`
	FromName       string `json:"from_name"`
	ReplyTo        string `json:"reply_to"`
	State          string `json:"state"`
}

type emailRecipientCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	DisplayName    string `json:"display_name"`
	Enabled        *bool  `json:"enabled"`
}

type emailRecipientUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	DisplayName    string `json:"display_name"`
	Enabled        *bool  `json:"enabled"`
}

type emailSubscriptionUpdateItem struct {
	EventType string `json:"event_type"`
	Enabled   bool   `json:"enabled"`
}

type emailSubscriptionUpdateRequest struct {
	IdempotencyKey string                        `json:"idempotency_key"`
	Items          []emailSubscriptionUpdateItem `json:"items"`
}

type emailTestSendRequest struct {
	IdempotencyKey string   `json:"idempotency_key"`
	RecipientIDs   []string `json:"recipient_ids"`
	Subject        string   `json:"subject"`
}

// ── 响应体 ────────────────────────────────────────────────────────────────────

type emailChannelResponse struct {
	ID             string                 `json:"id"`
	Host           string                 `json:"host"`
	Port           int                    `json:"port"`
	Encryption     string                 `json:"encryption"`
	Username       string                 `json:"username"`
	HasPassword    bool                   `json:"has_password"`
	FromAddress    string                 `json:"from_address"`
	FromName       string                 `json:"from_name,omitempty"`
	ReplyTo        string                 `json:"reply_to,omitempty"`
	State          string                 `json:"state"`
	LastVerifiedAt string                 `json:"last_verified_at,omitempty"`
	DevProfile     coreDevProfileResponse `json:"dev_profile"`
	CreatedAt      string                 `json:"created_at"`
	UpdatedAt      string                 `json:"updated_at"`
}

type emailRecipientResponse struct {
	ID          string                 `json:"id"`
	Email       string                 `json:"email"`
	DisplayName string                 `json:"display_name,omitempty"`
	Enabled     bool                   `json:"enabled"`
	State       string                 `json:"state"`
	DevProfile  coreDevProfileResponse `json:"dev_profile"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

type emailRecipientListResponse struct {
	Items      []emailRecipientResponse `json:"items"`
	Total      int                      `json:"total"`
	NextCursor string                   `json:"next_cursor"`
}

type notificationEventResponse struct {
	EventType   string `json:"event_type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
}

type notificationEventListResponse struct {
	Items []notificationEventResponse `json:"items"`
	Total int                         `json:"total"`
}

type emailSubscriptionResponse struct {
	EventType string `json:"event_type"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at"`
}

type emailSubscriptionListResponse struct {
	Items []emailSubscriptionResponse `json:"items"`
	Total int                         `json:"total"`
}

type emailTestSendRejectionResponse struct {
	RecipientID string `json:"recipient_id"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

type emailTestSendResponse struct {
	RequestID            string                           `json:"request_id"`
	Status               string                           `json:"status"`
	AcceptedRecipientIDs []string                         `json:"accepted_recipient_ids"`
	RejectedRecipients   []emailTestSendRejectionResponse `json:"rejected_recipient_ids"`
}

// ── 构造与注册 ──────────────────────────────────────────────────────────────────

func newNotificationAPI() *notificationAPI {
	return newNotificationAPIWithService(nil)
}

func newNotificationAPIWithService(service ports.NotificationService) *notificationAPI {
	if service == nil {
		service = runtimeadapter.NewLocalNotificationService()
	}
	return &notificationAPI{service: service}
}

func registerNotificationsResourcesWithService(v1 *route.RouterGroup, service ports.NotificationService) {
	api := newNotificationAPIWithService(service)
	v1.GET("/notifications/email/channel", api.getEmailChannel)
	v1.PUT("/notifications/email/channel", api.updateEmailChannel)

	v1.GET("/notifications/email/recipients", api.listEmailRecipients)
	v1.POST("/notifications/email/recipients", api.createEmailRecipient)
	v1.GET("/notifications/email/recipients/:recipient_id", api.getEmailRecipient)
	v1.PATCH("/notifications/email/recipients/:recipient_id", api.updateEmailRecipient)
	v1.DELETE("/notifications/email/recipients/:recipient_id", api.deleteEmailRecipient)

	v1.GET("/notifications/events", api.listNotificationEvents)

	v1.GET("/notifications/email/subscriptions", api.listEmailSubscriptions)
	v1.PUT("/notifications/email/subscriptions", api.updateEmailSubscriptions)

	v1.POST("/notifications/email/test-send", api.sendEmailTest)
}

// ── 通道 ────────────────────────────────────────────────────────────────────────

func (api *notificationAPI) getEmailChannel(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetEmailChannel(ctx, demoTenantID(c))
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, emailChannelFromRecord(rec))
}

func (api *notificationAPI) updateEmailChannel(ctx context.Context, c *app.RequestContext) {
	var req emailChannelUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid email channel request")
		return
	}
	state := ports.EmailChannelState(strings.TrimSpace(req.State))
	if state == "" {
		state = ports.EmailChannelActive
	}
	rec, err := api.service.UpdateEmailChannel(ctx, ports.EmailChannelUpdateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Host:           req.Host,
		Port:           req.Port,
		Encryption:     ports.EmailChannelEncryption(strings.TrimSpace(req.Encryption)),
		Username:       req.Username,
		Password:       req.Password,
		FromAddress:    req.FromAddress,
		FromName:       req.FromName,
		ReplyTo:        req.ReplyTo,
		State:          state,
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, emailChannelFromRecord(rec))
}

// ── 收件人 ──────────────────────────────────────────────────────────────────────

func (api *notificationAPI) listEmailRecipients(ctx context.Context, c *app.RequestContext) {
	req := ports.EmailRecipientListRequest{
		TenantID: demoTenantID(c),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   strings.TrimSpace(c.Query("cursor")),
	}
	if v := strings.TrimSpace(c.Query("enabled")); v != "" {
		switch strings.ToLower(v) {
		case "true", "1":
			b := true
			req.Enabled = &b
		case "false", "0":
			b := false
			req.Enabled = &b
		}
	}
	records, nextCursor, err := api.service.ListEmailRecipients(ctx, req)
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	items := make([]emailRecipientResponse, 0, len(records))
	for _, record := range records {
		items = append(items, emailRecipientFromRecord(record))
	}
	c.JSON(http.StatusOK, emailRecipientListResponse{
		Items:      items,
		Total:      len(items),
		NextCursor: nextCursor,
	})
}

func (api *notificationAPI) createEmailRecipient(ctx context.Context, c *app.RequestContext) {
	var req emailRecipientCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid email recipient request")
		return
	}
	rec, err := api.service.CreateEmailRecipient(ctx, ports.EmailRecipientCreateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Email:          req.Email,
		DisplayName:    req.DisplayName,
		Enabled:        boolValue(req.Enabled, true),
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, emailRecipientFromRecord(rec))
}

func (api *notificationAPI) getEmailRecipient(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetEmailRecipient(ctx, ports.EmailRecipientGetRequest{
		TenantID:    demoTenantID(c),
		RecipientID: c.Param("recipient_id"),
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, emailRecipientFromRecord(rec))
}

func (api *notificationAPI) updateEmailRecipient(ctx context.Context, c *app.RequestContext) {
	var req emailRecipientUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid email recipient update request")
		return
	}
	rec, err := api.service.UpdateEmailRecipient(ctx, ports.EmailRecipientUpdateRequest{
		TenantID:       demoTenantID(c),
		RecipientID:    c.Param("recipient_id"),
		IdempotencyKey: req.IdempotencyKey,
		Email:          req.Email,
		DisplayName:    req.DisplayName,
		Enabled:        req.Enabled,
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, emailRecipientFromRecord(rec))
}

func (api *notificationAPI) deleteEmailRecipient(ctx context.Context, c *app.RequestContext) {
	if err := api.service.DeleteEmailRecipient(ctx, ports.EmailRecipientGetRequest{
		TenantID:    demoTenantID(c),
		RecipientID: c.Param("recipient_id"),
	}); err != nil {
		writeNotificationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ── 事件目录 ──────────────────────────────────────────────────────────────────

func (api *notificationAPI) listNotificationEvents(ctx context.Context, c *app.RequestContext) {
	items, err := api.service.ListNotificationEvents(ctx)
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	out := make([]notificationEventResponse, 0, len(items))
	for _, item := range items {
		out = append(out, notificationEventResponse{
			EventType:   string(item.EventType),
			Name:        item.Name,
			Description: item.Description,
			Category:    string(item.Category),
			Severity:    string(item.Severity),
		})
	}
	c.JSON(http.StatusOK, notificationEventListResponse{Items: out, Total: len(out)})
}

// ── 订阅 ──────────────────────────────────────────────────────────────────────

func (api *notificationAPI) listEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListEmailSubscriptions(ctx, demoTenantID(c))
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	items := make([]emailSubscriptionResponse, 0, len(records))
	for _, rec := range records {
		items = append(items, emailSubscriptionResponse{
			EventType: string(rec.EventType),
			Enabled:   rec.Enabled,
			UpdatedAt: notificationTime(rec.UpdatedAt),
		})
	}
	c.JSON(http.StatusOK, emailSubscriptionListResponse{Items: items, Total: len(items)})
}

func (api *notificationAPI) updateEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	var req emailSubscriptionUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid email subscription update request")
		return
	}
	items := make([]ports.EmailSubscriptionUpdateItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, ports.EmailSubscriptionUpdateItem{
			EventType: ports.NotificationEventType(strings.TrimSpace(item.EventType)),
			Enabled:   item.Enabled,
		})
	}
	records, err := api.service.UpdateEmailSubscriptions(ctx, ports.EmailSubscriptionUpdateRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Items:          items,
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	out := make([]emailSubscriptionResponse, 0, len(records))
	for _, rec := range records {
		out = append(out, emailSubscriptionResponse{
			EventType: string(rec.EventType),
			Enabled:   rec.Enabled,
			UpdatedAt: notificationTime(rec.UpdatedAt),
		})
	}
	c.JSON(http.StatusOK, emailSubscriptionListResponse{Items: out, Total: len(out)})
}

// ── 测试发送 ──────────────────────────────────────────────────────────────────

func (api *notificationAPI) sendEmailTest(ctx context.Context, c *app.RequestContext) {
	var req emailTestSendRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid email test send request")
		return
	}
	result, err := api.service.SendEmailTest(ctx, ports.EmailTestSendRequest{
		TenantID:       demoTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		RecipientIDs:   req.RecipientIDs,
		Subject:        req.Subject,
	})
	if err != nil {
		writeNotificationError(c, err)
		return
	}
	rejected := make([]emailTestSendRejectionResponse, 0, len(result.RejectedRecipients))
	for _, r := range result.RejectedRecipients {
		rejected = append(rejected, emailTestSendRejectionResponse{
			RecipientID: r.RecipientID,
			Code:        r.Code,
			Message:     r.Message,
		})
	}
	c.JSON(http.StatusOK, emailTestSendResponse{
		RequestID:            result.RequestID,
		Status:               string(result.Status),
		AcceptedRecipientIDs: result.AcceptedRecipientIDs,
		RejectedRecipients:   rejected,
	})
}

// ── 转换函数 ──────────────────────────────────────────────────────────────────

func emailChannelFromRecord(r ports.EmailChannelRecord) emailChannelResponse {
	resp := emailChannelResponse{
		ID:          r.ID,
		Host:        r.Host,
		Port:        r.Port,
		Encryption:  string(r.Encryption),
		Username:    r.Username,
		HasPassword: r.HasPassword,
		FromAddress: r.FromAddress,
		FromName:    r.FromName,
		ReplyTo:     r.ReplyTo,
		State:       string(r.State),
		DevProfile:  coreDevProfileFromPort(r.DevProfile),
		CreatedAt:   notificationTime(r.CreatedAt),
		UpdatedAt:   notificationTime(r.UpdatedAt),
	}
	if !r.LastVerifiedAt.IsZero() {
		resp.LastVerifiedAt = r.LastVerifiedAt.Format(time.RFC3339)
	}
	return resp
}

func emailRecipientFromRecord(r ports.EmailRecipientRecord) emailRecipientResponse {
	return emailRecipientResponse{
		ID:          r.ID,
		Email:       r.Email,
		DisplayName: r.DisplayName,
		Enabled:     r.Enabled,
		State:       string(r.State),
		DevProfile:  coreDevProfileFromPort(r.DevProfile),
		CreatedAt:   notificationTime(r.CreatedAt),
		UpdatedAt:   notificationTime(r.UpdatedAt),
	}
}

func notificationTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func writeNotificationError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
