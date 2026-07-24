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

type emailNotificationAPI struct {
	store ports.EmailNotificationStore
}

// Request / response types (hand-written, matching OpenAPI schemas)

type putEmailSmtpConfigRequest struct {
	IdempotencyKey string  `json:"idempotency_key"`
	SmtpHost       string  `json:"smtp_host"`
	SmtpPort       int     `json:"smtp_port"`
	Encryption     string  `json:"encryption"`
	FromAddress    string  `json:"from_address"`
	Username       string  `json:"username"`
	Password       *string `json:"password,omitempty"`
	AuthCode       *string `json:"auth_code,omitempty"`
}

type emailSmtpConfigResponse struct {
	Configured  bool   `json:"configured"`
	SmtpHost    string `json:"smtp_host,omitempty"`
	SmtpPort    int    `json:"smtp_port,omitempty"`
	Encryption  string `json:"encryption,omitempty"`
	FromAddress string `json:"from_address,omitempty"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
	HasAuthCode bool   `json:"has_auth_code,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type emailRecipientResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type emailRecipientListResponse struct {
	Items []emailRecipientResponse `json:"items"`
	Total int                      `json:"total"`
}

type createEmailRecipientRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	Label          string `json:"label"`
}

type updateEmailRecipientRequest struct {
	IdempotencyKey string  `json:"idempotency_key"`
	Email          *string `json:"email,omitempty"`
	Label          *string `json:"label,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
}

type emailSubscriptionResponse struct {
	EventType   string `json:"event_type"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   string `json:"updated_at"`
}

type emailSubscriptionListResponse struct {
	Items []emailSubscriptionResponse `json:"items"`
	Total int                         `json:"total"`
}

type putEmailSubscriptionsRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Subscriptions  []struct {
		EventType string `json:"event_type"`
		Enabled   bool   `json:"enabled"`
	} `json:"subscriptions"`
}

type sendTestEmailRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type sendTestEmailResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	SentAt    string `json:"sent_at,omitempty"`
}

func newEmailNotificationAPIWithStore(store ports.EmailNotificationStore) *emailNotificationAPI {
	if store == nil {
		store = runtimeadapter.NewLocalEmailNotificationStore()
	}
	return &emailNotificationAPI{store: store}
}

func registerEmailNotificationResourcesWithService(v1 *route.RouterGroup, store ports.EmailNotificationStore) {
	api := newEmailNotificationAPIWithStore(store)
	v1.GET("/notifications/email/smtp", api.getEmailSmtpConfig)
	v1.PUT("/notifications/email/smtp", api.putEmailSmtpConfig)
	v1.GET("/notifications/email/recipients", api.listEmailRecipients)
	v1.POST("/notifications/email/recipients", api.createEmailRecipient)
	v1.PATCH("/notifications/email/recipients/:recipient_id", api.updateEmailRecipient)
	v1.DELETE("/notifications/email/recipients/:recipient_id", api.deleteEmailRecipient)
	v1.GET("/notifications/email/subscriptions", api.listEmailSubscriptions)
	v1.PUT("/notifications/email/subscriptions", api.putEmailSubscriptions)
	v1.POST("/notifications/email/test", api.sendTestEmail)
}

// --- Handlers ---

func (api *emailNotificationAPI) getEmailSmtpConfig(ctx context.Context, c *app.RequestContext) {
	cfg, err := api.store.GetSmtpConfig(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	if cfg == nil {
		c.JSON(http.StatusOK, emailSmtpConfigResponse{Configured: false})
		return
	}
	c.JSON(http.StatusOK, smtpConfigToResponse(cfg))
}

func (api *emailNotificationAPI) putEmailSmtpConfig(ctx context.Context, c *app.RequestContext) {
	var req putEmailSmtpConfigRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid smtp config request")
		return
	}
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	}
	if idemKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	cfg, err := api.store.PutSmtpConfig(ctx, idemKey, ports.EmailSmtpConfigWrite{
		SmtpHost:    req.SmtpHost,
		SmtpPort:    req.SmtpPort,
		Encryption:  req.Encryption,
		FromAddress: req.FromAddress,
		Username:    req.Username,
		Password:    req.Password,
		AuthCode:    req.AuthCode,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, smtpConfigToResponse(cfg))
}

func (api *emailNotificationAPI) listEmailRecipients(ctx context.Context, c *app.RequestContext) {
	recipients, err := api.store.ListRecipients(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]emailRecipientResponse, 0, len(recipients))
	for _, r := range recipients {
		items = append(items, recipientToResponse(r))
	}
	c.JSON(http.StatusOK, emailRecipientListResponse{Items: items, Total: len(items)})
}

func (api *emailNotificationAPI) createEmailRecipient(ctx context.Context, c *app.RequestContext) {
	var req createEmailRecipientRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid recipient request")
		return
	}
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	}
	if idemKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	rec, err := api.store.CreateRecipient(ctx, idemKey, ports.EmailRecipientWrite{
		Email: req.Email,
		Label: req.Label,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, recipientToResponse(*rec))
}

func (api *emailNotificationAPI) updateEmailRecipient(ctx context.Context, c *app.RequestContext) {
	var req updateEmailRecipientRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid recipient update request")
		return
	}
	id := c.Param("recipient_id")
	if id == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "recipient_id is required")
		return
	}
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	}
	if idemKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}

	// If enabled is provided, use SetRecipientEnabled; otherwise update fields
	if req.Enabled != nil {
		rec, err := api.store.SetRecipientEnabled(ctx, id, *req.Enabled)
		if err != nil {
			writeEmailNotificationError(c, err)
			return
		}
		// If email or label also provided, do a follow-up update
		if req.Email != nil || req.Label != nil {
			emailVal := ""
			if req.Email != nil {
				emailVal = *req.Email
			}
			labelVal := ""
			if req.Label != nil {
				labelVal = *req.Label
			}
			rec, err = api.store.UpdateRecipient(ctx, id, ports.EmailRecipientWrite{
				Email: emailVal,
				Label: labelVal,
			})
			if err != nil {
				writeEmailNotificationError(c, err)
				return
			}
		}
		c.JSON(http.StatusOK, recipientToResponse(*rec))
		return
	}

	emailVal := ""
	if req.Email != nil {
		emailVal = *req.Email
	}
	labelVal := ""
	if req.Label != nil {
		labelVal = *req.Label
	}
	rec, err := api.store.UpdateRecipient(ctx, id, ports.EmailRecipientWrite{
		Email: emailVal,
		Label: labelVal,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, recipientToResponse(*rec))
}

func (api *emailNotificationAPI) deleteEmailRecipient(ctx context.Context, c *app.RequestContext) {
	id := c.Param("recipient_id")
	if id == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "recipient_id is required")
		return
	}
	if err := api.store.DeleteRecipient(ctx, id); err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (api *emailNotificationAPI) listEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	subs, err := api.store.ListSubscriptions(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]emailSubscriptionResponse, 0, len(subs))
	for _, s := range subs {
		items = append(items, subscriptionToResponse(s))
	}
	c.JSON(http.StatusOK, emailSubscriptionListResponse{Items: items, Total: len(items)})
}

func (api *emailNotificationAPI) putEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	var req putEmailSubscriptionsRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid subscriptions request")
		return
	}
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	}
	if idemKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	subs := make(map[string]bool, len(req.Subscriptions))
	for _, s := range req.Subscriptions {
		subs[s.EventType] = s.Enabled
	}
	result, err := api.store.PutSubscriptions(ctx, idemKey, subs)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]emailSubscriptionResponse, 0, len(result))
	for _, s := range result {
		items = append(items, subscriptionToResponse(s))
	}
	c.JSON(http.StatusOK, emailSubscriptionListResponse{Items: items, Total: len(items)})
}

func (api *emailNotificationAPI) sendTestEmail(ctx context.Context, c *app.RequestContext) {
	var req sendTestEmailRequest
	if err := c.BindJSON(&req); err != nil {
		// Body may be empty for test send; allow it
		req = sendTestEmailRequest{}
	}
	idemKey := strings.TrimSpace(req.IdempotencyKey)
	if idemKey == "" {
		idemKey = strings.TrimSpace(string(c.GetHeader("Idempotency-Key")))
	}
	if idemKey == "" {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is required")
		return
	}
	result, err := api.store.SendTestEmail(ctx, idemKey)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, sendTestEmailResponse{
		Success:   result.Success,
		Message:   result.Message,
		RequestID: result.RequestID,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Mappers ---

func smtpConfigToResponse(cfg *ports.EmailSmtpConfig) emailSmtpConfigResponse {
	if cfg == nil {
		return emailSmtpConfigResponse{Configured: false}
	}
	return emailSmtpConfigResponse{
		Configured:  true,
		SmtpHost:    cfg.SmtpHost,
		SmtpPort:    cfg.SmtpPort,
		Encryption:  cfg.Encryption,
		FromAddress: cfg.FromAddress,
		Username:    cfg.Username,
		HasPassword: cfg.HasPassword,
		HasAuthCode: cfg.HasAuthCode,
		CreatedAt:   cfg.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   cfg.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func recipientToResponse(r ports.EmailRecipient) emailRecipientResponse {
	return emailRecipientResponse{
		ID:        r.ID,
		Email:     r.Email,
		Label:     r.Label,
		Enabled:   r.Enabled,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func subscriptionToResponse(s ports.EmailSubscription) emailSubscriptionResponse {
	return emailSubscriptionResponse{
		EventType:   s.EventType,
		Description: s.Description,
		Enabled:     s.Enabled,
		UpdatedAt:   s.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// --- Error handler ---

func writeEmailNotificationError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrEmailRecipientNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrEmailSmtpNotConfigured),
		errors.Is(err, ports.ErrEmailNoEnabledRecipient),
		errors.Is(err, ports.ErrEmailNoCredentials),
		errors.Is(err, ports.ErrFailedPrecondition):
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrInvalid), errors.Is(err, ports.ErrEmailInvalidEventType):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, ports.ErrEmailStoreUnavailable):
		writeDemoError(c, http.StatusServiceUnavailable, "STORE_UNAVAILABLE", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
