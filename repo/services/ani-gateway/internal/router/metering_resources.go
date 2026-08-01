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

type meteringAPI struct {
	service ports.MeteringService
}

type reportTokenUsageRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Source         string            `json:"source"`
	Model          string            `json:"model"`
	InputTokens    int64             `json:"input_tokens"`
	OutputTokens   int64             `json:"output_tokens"`
	RequestID      string            `json:"request_id"`
	InstanceID     string            `json:"instance_id"`
	OccurredAt     string            `json:"occurred_at"`
	Labels         map[string]string `json:"labels"`
}

type meteringUsageResponse struct {
	Items      []meteringUsageItem    `json:"items"`
	Total      int                    `json:"total"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}

type meteringUsageItem struct {
	TenantID      string  `json:"tenant_id,omitempty"`
	ResourceType  string  `json:"resource_type"`
	TotalQuantity float64 `json:"total_quantity"`
	Unit          string  `json:"unit"`
	Period        string  `json:"period,omitempty"`
}

type tokenUsageReportResponse struct {
	ID           string                 `json:"id"`
	TenantID     string                 `json:"tenant_id"`
	Source       string                 `json:"source"`
	Model        string                 `json:"model"`
	InputTokens  int64                  `json:"input_tokens"`
	OutputTokens int64                  `json:"output_tokens"`
	TotalTokens  int64                  `json:"total_tokens"`
	RequestID    string                 `json:"request_id,omitempty"`
	InstanceID   string                 `json:"instance_id,omitempty"`
	State        string                 `json:"state"`
	DevProfile   coreDevProfileResponse `json:"dev_profile"`
	CreatedAt    string                 `json:"created_at"`
}

func newMeteringAPI() *meteringAPI {
	return &meteringAPI{service: runtimeadapter.NewLocalMeteringService()}
}

func registerMetering(v1 *route.RouterGroup) {
	api := newMeteringAPI()
	v1.GET("/metering/usage", api.queryUsage)
	v1.POST("/metering/token-usage", api.reportTokenUsage)
}

func (api *meteringAPI) queryUsage(ctx context.Context, c *app.RequestContext) {
	startTime, err := optionalRFC3339(c.Query("start_time"))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "start_time must be RFC3339")
		return
	}
	endTime, err := optionalRFC3339(c.Query("end_time"))
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "end_time must be RFC3339")
		return
	}
	result, err := api.service.QueryUsage(ctx, ports.MeteringUsageQueryRequest{
		TenantID:     instanceTenantID(c),
		StartTime:    startTime,
		EndTime:      endTime,
		ResourceType: ports.MeteringResourceType(strings.TrimSpace(c.Query("resource_type"))),
		GroupBy:      strings.TrimSpace(c.Query("group_by")),
	})
	if err != nil {
		writeMeteringError(c, err)
		return
	}
	c.JSON(http.StatusOK, meteringUsageFromResult(result))
}

func (api *meteringAPI) reportTokenUsage(ctx context.Context, c *app.RequestContext) {
	var req reportTokenUsageRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid token usage request")
		return
	}
	occurredAt, err := optionalRFC3339(req.OccurredAt)
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "occurred_at must be RFC3339")
		return
	}
	record, err := api.service.ReportTokenUsage(ctx, ports.TokenUsageReportRequest{
		TenantID:       instanceTenantID(c),
		IdempotencyKey: req.IdempotencyKey,
		Source:         req.Source,
		Model:          req.Model,
		InputTokens:    req.InputTokens,
		OutputTokens:   req.OutputTokens,
		RequestID:      req.RequestID,
		InstanceID:     req.InstanceID,
		OccurredAt:     occurredAt,
		Labels:         req.Labels,
	})
	if err != nil {
		writeMeteringError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, tokenUsageReportFromRecord(record))
}

func meteringUsageFromResult(result ports.MeteringUsageResult) meteringUsageResponse {
	items := make([]meteringUsageItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, meteringUsageItem{
			TenantID:      item.TenantID,
			ResourceType:  string(item.ResourceType),
			TotalQuantity: item.TotalQuantity,
			Unit:          item.Unit,
			Period:        item.Period,
		})
	}
	return meteringUsageResponse{
		Items:      items,
		Total:      len(items),
		DevProfile: devProfileFromPort(result.DevProfile),
	}
}

func tokenUsageReportFromRecord(record ports.TokenUsageReportRecord) tokenUsageReportResponse {
	return tokenUsageReportResponse{
		ID:           record.ReportID,
		TenantID:     record.TenantID,
		Source:       record.Source,
		Model:        record.Model,
		InputTokens:  record.InputTokens,
		OutputTokens: record.OutputTokens,
		TotalTokens:  record.TotalTokens,
		RequestID:    record.RequestID,
		InstanceID:   record.InstanceID,
		State:        string(record.State),
		DevProfile:   devProfileFromPort(record.DevProfile),
		CreatedAt:    networkTime(record.CreatedAt),
	}
}

func optionalRFC3339(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func writeMeteringError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
