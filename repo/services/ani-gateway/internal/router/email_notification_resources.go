// Package router: email notification handlers backed by ports.EmailNotificationService.
//
// RBAC is enforced by the global middleware chain (Auth → RBAC) before
// reaching these handlers; in dev mode (ANI_AUTH_MODE=dev) auth is bypassed.
//
// Routes are registered under /api/v1/notifications/email/* (Core API).
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// emailNotificationAPI wraps the port interface for handler use.
type emailNotificationAPI struct {
	service ports.EmailNotificationService
}

func newEmailNotificationAPI() *emailNotificationAPI {
	return newEmailNotificationAPIWithService(nil)
}

func newEmailNotificationAPIWithService(service ports.EmailNotificationService) *emailNotificationAPI {
	if service == nil {
		service = runtimeadapter.NewLocalEmailNotificationService()
	}
	return &emailNotificationAPI{service: service}
}

func registerEmailNotifications(v1 *route.RouterGroup) {
	registerEmailNotificationsWithService(v1, nil)
}

func registerEmailNotificationsWithService(v1 *route.RouterGroup, service ports.EmailNotificationService) {
	api := newEmailNotificationAPIWithService(service)
	v1.GET("/notifications/email/smtp", api.getEmailSmtpConfig)
	v1.PUT("/notifications/email/smtp", api.putEmailSmtpConfig)
	v1.GET("/notifications/email/recipients", api.listEmailRecipients)
	v1.POST("/notifications/email/recipients", api.createEmailRecipient)
	v1.PATCH("/notifications/email/recipients/:recipient_id", api.updateEmailRecipient)
	v1.DELETE("/notifications/email/recipients/:recipient_id", api.deleteEmailRecipient)
	v1.GET("/notifications/email/subscriptions", api.listEmailSubscriptions)
	v1.PUT("/notifications/email/subscriptions", api.putEmailSubscriptions)
	v1.GET("/notifications/email/events", api.listEmailEvents)
	v1.POST("/notifications/email/test", api.sendEmailTest)
}

// ── Request types ───────────────────────────────────────────────────────

type emailSmtpPutRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	SmtpHost       string `json:"smtp_host"`
	SmtpPort       int    `json:"smtp_port"`
	Encryption     string `json:"encryption"`
	FromAddress    string `json:"from_address"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	AuthCode       string `json:"auth_code"`
}

type emailRecipientCreateReq struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	Label          string `json:"label"`
}

type emailRecipientUpdateReq struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	Label          string `json:"label"`
	Enabled        *bool  `json:"enabled"`
}

type emailSubscriptionsPutReq struct {
	IdempotencyKey string `json:"idempotency_key"`
	Subscriptions  []struct {
		EventType string `json:"event_type"`
		Enabled   bool   `json:"enabled"`
	} `json:"subscriptions"`
}

type emailTestSendReq struct {
	IdempotencyKey string `json:"idempotency_key"`
}

// ── Handlers ────────────────────────────────────────────────────────────

func (api *emailNotificationAPI) getEmailSmtpConfig(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetSmtpConfig(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, smtpConfigToResponse(rec))
}

func (api *emailNotificationAPI) putEmailSmtpConfig(ctx context.Context, c *app.RequestContext) {
	var req emailSmtpPutRequest
	if err := c.BindJSON(&req); err != nil {
		writeEmailNotificationError(c, fmt.Errorf("%w: invalid request body", ports.ErrInvalid))
		return
	}
	rec, err := api.service.PutSmtpConfig(ctx, ports.EmailSmtpConfigPutRequest{
		IdempotencyKey: req.IdempotencyKey,
		SmtpHost:       req.SmtpHost,
		SmtpPort:       req.SmtpPort,
		Encryption:     req.Encryption,
		FromAddress:    req.FromAddress,
		Username:       req.Username,
		Password:       req.Password,
		AuthCode:       req.AuthCode,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, smtpConfigToResponse(rec))
}

func (api *emailNotificationAPI) listEmailRecipients(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListRecipients(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		items = append(items, recipientToResponse(r))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *emailNotificationAPI) createEmailRecipient(ctx context.Context, c *app.RequestContext) {
	var req emailRecipientCreateReq
	if err := c.BindJSON(&req); err != nil {
		writeEmailNotificationError(c, fmt.Errorf("%w: invalid request body", ports.ErrInvalid))
		return
	}
	rec, err := api.service.CreateRecipient(ctx, ports.EmailRecipientCreateRequest{
		IdempotencyKey: req.IdempotencyKey,
		Email:          req.Email,
		Label:          req.Label,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, recipientToResponse(rec))
}

func (api *emailNotificationAPI) updateEmailRecipient(ctx context.Context, c *app.RequestContext) {
	recipientID := c.Param("recipient_id")
	if recipientID == "" {
		writeEmailNotificationError(c, fmt.Errorf("%w: recipient_id required", ports.ErrInvalid))
		return
	}
	var req emailRecipientUpdateReq
	if err := c.BindJSON(&req); err != nil {
		writeEmailNotificationError(c, fmt.Errorf("%w: invalid request body", ports.ErrInvalid))
		return
	}
	rec, err := api.service.UpdateRecipient(ctx, ports.EmailRecipientUpdateRequest{
		IdempotencyKey: req.IdempotencyKey,
		ID:             recipientID,
		Email:          req.Email,
		Label:          req.Label,
		Enabled:        req.Enabled,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.JSON(http.StatusOK, recipientToResponse(rec))
}

func (api *emailNotificationAPI) deleteEmailRecipient(ctx context.Context, c *app.RequestContext) {
	recipientID := c.Param("recipient_id")
	if recipientID == "" {
		writeEmailNotificationError(c, fmt.Errorf("%w: recipient_id required", ports.ErrInvalid))
		return
	}
	err := api.service.DeleteRecipient(ctx, ports.EmailRecipientDeleteRequest{ID: recipientID})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (api *emailNotificationAPI) listEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListSubscriptions(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, s := range recs {
		items = append(items, map[string]any{
			"event_type": s.EventType,
			"enabled":    s.Enabled,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *emailNotificationAPI) putEmailSubscriptions(ctx context.Context, c *app.RequestContext) {
	var req emailSubscriptionsPutReq
	if err := c.BindJSON(&req); err != nil {
		writeEmailNotificationError(c, fmt.Errorf("%w: invalid request body", ports.ErrInvalid))
		return
	}
	subs := make([]ports.EmailSubscriptionRecord, 0, len(req.Subscriptions))
	for _, s := range req.Subscriptions {
		subs = append(subs, ports.EmailSubscriptionRecord{
			EventType: s.EventType,
			Enabled:   s.Enabled,
		})
	}
	recs, err := api.service.PutSubscriptions(ctx, ports.EmailSubscriptionsPutRequest{
		IdempotencyKey: req.IdempotencyKey,
		Subscriptions:  subs,
	})
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, s := range recs {
		items = append(items, map[string]any{
			"event_type": s.EventType,
			"enabled":    s.Enabled,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *emailNotificationAPI) listEmailEvents(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListEvents(ctx)
	if err != nil {
		writeEmailNotificationError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, e := range recs {
		items = append(items, map[string]any{
			"event_type":   e.EventType,
			"display_name": e.DisplayName,
			"description":  e.Description,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (api *emailNotificationAPI) sendEmailTest(ctx context.Context, c *app.RequestContext) {
	var req emailTestSendReq
	if err := c.BindJSON(&req); err != nil {
		writeEmailNotificationError(c, fmt.Errorf("%w: invalid request body", ports.ErrInvalid))
		return
	}
	slog.Info("email test send requested",
		"request_id", middleware.GetRequestID(c),
		"idempotency_key", req.IdempotencyKey,
	)
	result, err := api.service.SendTestEmail(ctx, ports.EmailTestSendRequest{
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		slog.Error("email test send failed",
			"request_id", middleware.GetRequestID(c),
			"error", err.Error(),
		)
		writeEmailNotificationError(c, err)
		return
	}
	slog.Info("email test send completed",
		"request_id", middleware.GetRequestID(c),
		"status", result.Status,
		"message", result.Message,
		"auth_mode", result.AuthMode,
		"username", result.Username,
		"from_name", result.FromName,
		"from_email", result.FromEmail,
		"to_emails", result.ToEmails,
		"subject", result.Subject,
		"sent_at", result.SentAt,
	)
	if result.Status == "failed" {
		slog.Error("email test send FAILED",
			"request_id", middleware.GetRequestID(c),
			"error", result.Message,
			"auth_mode", result.AuthMode,
			"username", result.Username,
			"from_email", result.FromEmail,
			"to_emails", result.ToEmails,
		)
	}
	resp := map[string]any{
		"status":      result.Status,
		"message":     result.Message,
		"from_name":   result.FromName,
		"from_email":  result.FromEmail,
		"to_name":     result.ToName,
		"to_emails":   result.ToEmails,
		"subject":     result.Subject,
		"content":     result.Content,
		"sent_at":     result.SentAt,
		"auth_mode":   result.AuthMode,
		"username":   result.Username,
	}
	if result.Status == "failed" {
		resp["request_id"] = middleware.GetRequestID(c)
	}
	c.JSON(http.StatusOK, resp)
}

// ── Response helpers ────────────────────────────────────────────────────

func smtpConfigToResponse(rec ports.EmailSmtpConfigRecord) map[string]any {
	return map[string]any{
		"smtp_host":            rec.SmtpHost,
		"smtp_port":            rec.SmtpPort,
		"encryption":           rec.Encryption,
		"from_address":          rec.FromAddress,
		"username":             rec.Username,
		"configured":            rec.Configured,
		"password_configured":  rec.PasswordConfigured,
		"auth_code_configured": rec.AuthCodeConfigured,
	}
}

func recipientToResponse(r ports.EmailRecipientRecord) map[string]any {
	resp := map[string]any{
		"id":         r.ID,
		"email":      r.Email,
		"enabled":    r.Enabled,
		"created_at": r.CreatedAt.Format(time.RFC3339),
		"updated_at": r.UpdatedAt.Format(time.RFC3339),
	}
	if r.Label != "" {
		resp["label"] = r.Label
	} else {
		resp["label"] = nil
	}
	return resp
}

// ── Error helper ─────────────────────────────────────────────────────────

func writeEmailNotificationError(c *app.RequestContext, err error) {
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
