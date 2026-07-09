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
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type observabilityAPI struct {
	service ports.ObservabilityService
}

type createObservabilityAlertRuleRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Name           string            `json:"name"`
	PromQL         string            `json:"promql"`
	Duration       string            `json:"duration"`
	Severity       string            `json:"severity"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	Enabled        *bool             `json:"enabled"`
}

type updateObservabilityAlertRuleRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Name           string            `json:"name"`
	PromQL         string            `json:"promql"`
	Duration       string            `json:"duration"`
	Severity       string            `json:"severity"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	Enabled        *bool             `json:"enabled"`
}

type observabilityQueryResponse struct {
	Query      string                 `json:"query"`
	ResultType string                 `json:"result_type"`
	Results    []observabilitySample  `json:"results"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type observabilitySample struct {
	Metric    map[string]string `json:"metric"`
	Value     float64           `json:"value"`
	Timestamp string            `json:"timestamp,omitempty"`
}

type observabilityAlertRuleResponse struct {
	ID          string                 `json:"id"`
	TenantID    string                 `json:"tenant_id"`
	Name        string                 `json:"name"`
	PromQL      string                 `json:"promql"`
	Duration    string                 `json:"duration"`
	Severity    string                 `json:"severity"`
	Labels      map[string]string      `json:"labels,omitempty"`
	Annotations map[string]string      `json:"annotations,omitempty"`
	Enabled     bool                   `json:"enabled"`
	State       string                 `json:"state"`
	DevProfile  coreDevProfileResponse `json:"dev_profile"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

func newObservabilityAPI() *observabilityAPI {
	return &observabilityAPI{service: runtimeadapter.NewLocalObservabilityService()}
}

func registerObservability(v1 *route.RouterGroup) {
	api := newObservabilityAPI()
	v1.GET("/observability/query", api.query)
	v1.GET("/observability/alert-rules", api.listAlertRules)
	v1.POST("/observability/alert-rules", api.createAlertRule)
	v1.GET("/observability/alert-rules/:rule_id", api.getAlertRule)
	v1.PATCH("/observability/alert-rules/:rule_id", api.updateAlertRule)
	v1.DELETE("/observability/alert-rules/:rule_id", api.deleteAlertRule)
}

func (api *observabilityAPI) query(ctx context.Context, c *app.RequestContext) {
	result, err := api.service.Query(ctx, ports.ObservabilityQueryRequest{
		TenantID: middleware.GetTenantID(c),
		Query:    c.Query("query"),
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, observabilityQueryFromResult(result))
}

func (api *observabilityAPI) createAlertRule(ctx context.Context, c *app.RequestContext) {
	var req createObservabilityAlertRuleRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid alert rule request")
		return
	}
	duration, err := observabilityDuration(req.Duration)
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	record, err := api.service.CreateAlertRule(ctx, ports.ObservabilityAlertRuleCreateRequest{
		TenantID:       middleware.GetTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		PromQL:         req.PromQL,
		Duration:       duration,
		Severity:       ports.ObservabilityAlertSeverity(strings.TrimSpace(req.Severity)),
		Labels:         req.Labels,
		Annotations:    req.Annotations,
		Enabled:        boolValue(req.Enabled, true),
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusCreated, observabilityAlertRuleFromRecord(record))
}

func (api *observabilityAPI) listAlertRules(ctx context.Context, c *app.RequestContext) {
	records, err := api.service.ListAlertRules(ctx, ports.ObservabilityAlertRuleListRequest{
		TenantID: middleware.GetTenantID(c),
		Limit:    queryInt(c, "limit", 20),
		Cursor:   c.Query("cursor"),
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	items := make([]observabilityAlertRuleResponse, 0, len(records))
	for _, record := range records {
		items = append(items, observabilityAlertRuleFromRecord(record))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}

func (api *observabilityAPI) getAlertRule(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetAlertRule(ctx, ports.ObservabilityAlertRuleGetRequest{
		TenantID: middleware.GetTenantID(c),
		RuleID:   c.Param("rule_id"),
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, observabilityAlertRuleFromRecord(record))
}

func (api *observabilityAPI) updateAlertRule(ctx context.Context, c *app.RequestContext) {
	var req updateObservabilityAlertRuleRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid alert rule request")
		return
	}
	duration, err := observabilityDuration(req.Duration)
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	record, err := api.service.UpdateAlertRule(ctx, ports.ObservabilityAlertRuleUpdateRequest{
		TenantID:       middleware.GetTenantID(c),
		RuleID:         c.Param("rule_id"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		PromQL:         req.PromQL,
		Duration:       duration,
		Severity:       ports.ObservabilityAlertSeverity(strings.TrimSpace(req.Severity)),
		Labels:         req.Labels,
		Annotations:    req.Annotations,
		Enabled:        req.Enabled,
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, observabilityAlertRuleFromRecord(record))
}

func (api *observabilityAPI) deleteAlertRule(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.DeleteAlertRule(ctx, ports.ObservabilityAlertRuleGetRequest{
		TenantID: middleware.GetTenantID(c),
		RuleID:   c.Param("rule_id"),
	})
	if err != nil {
		writeObservabilityError(c, err)
		return
	}
	c.JSON(http.StatusOK, observabilityAlertRuleFromRecord(record))
}

func observabilityQueryFromResult(result ports.ObservabilityQueryResult) observabilityQueryResponse {
	items := make([]observabilitySample, 0, len(result.Results))
	for _, sample := range result.Results {
		item := observabilitySample{Metric: sample.Metric, Value: sample.Value}
		if !sample.Timestamp.IsZero() {
			item.Timestamp = sample.Timestamp.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return observabilityQueryResponse{
		Query:      result.Query,
		ResultType: string(result.ResultType),
		Results:    items,
		DevProfile: devProfileFromPort(result.DevProfile),
	}
}

func observabilityAlertRuleFromRecord(record ports.ObservabilityAlertRuleRecord) observabilityAlertRuleResponse {
	return observabilityAlertRuleResponse{
		ID:          record.RuleID,
		TenantID:    record.TenantID,
		Name:        record.Name,
		PromQL:      record.PromQL,
		Duration:    record.Duration.String(),
		Severity:    string(record.Severity),
		Labels:      record.Labels,
		Annotations: record.Annotations,
		Enabled:     record.Enabled,
		State:       string(record.State),
		DevProfile:  devProfileFromPort(record.DevProfile),
		CreatedAt:   networkTime(record.CreatedAt),
		UpdatedAt:   networkTime(record.UpdatedAt),
	}
}

func devProfileFromPort(profile ports.DevProfileInfo) coreDevProfileResponse {
	return coreDevProfileResponse{
		Mode:         profile.Mode,
		Provider:     profile.Provider,
		RealProvider: profile.RealProvider,
		Reason:       profile.Reason,
	}
}

func observabilityDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || duration <= 0 {
		return 0, errors.New("duration must be a positive Go duration string")
	}
	return duration, nil
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func writeObservabilityError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
