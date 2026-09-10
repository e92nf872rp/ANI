// platform_audit.go 实现平台审计日志只读检索接口：
// GET /api/v1/platform/audit-logs（BOSS「平台审计且合规 → 平台审计日志」）。
// 数据源为 kube-apiserver 审计日志经 fluent-bit 采集进 Loki 的独立
// stream="kubernetes-audit"；按 timestamp(+auditID) 全局倒序 + 游标翻页，
// total 为近似量；Loki 不可用/查询失败时直接返回错误（过渡方案不降级）。
package router

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

type platformAuditAPI struct {
	service ports.PlatformAuditService
}

type platformAuditLogResponse struct {
	Items       []platformAuditLogItemResponse `json:"items"`
	NextAfter   string                         `json:"next_after"`
	TotalApprox int64                          `json:"total_approx"`
	DevProfile  coreDevProfileResponse         `json:"dev_profile"`
}

type platformAuditLogItemResponse struct {
	AuditID      string                        `json:"audit_id"`
	Timestamp    string                        `json:"timestamp"`
	Verb         string                        `json:"verb"`
	User         platformAuditUserResponse     `json:"user"`
	Resource     platformAuditResourceResponse `json:"resource"`
	ResponseCode int64                         `json:"response_code"`
	Detail       platformAuditDetailResponse   `json:"detail"`
}

type platformAuditUserResponse struct {
	Username string   `json:"username"`
	Groups   []string `json:"groups"`
}

type platformAuditResourceResponse struct {
	Namespace string `json:"namespace"`
	Resource  string `json:"resource"`
	Name      string `json:"name"`
}

type platformAuditDetailResponse struct {
	RequestURI string `json:"request_uri"`
	UserAgent  string `json:"user_agent"`
}

// newPlatformAuditAPI 构造审计 API。service 由 main 装配必选注入（默认
// 直连 Loki）；nil 不再回退 local 假数据——过渡方案宁可报错也不降级。
func newPlatformAuditAPI(service ports.PlatformAuditService) *platformAuditAPI {
	return &platformAuditAPI{service: service}
}

func registerPlatformAudit(v1 *route.RouterGroup, service ports.PlatformAuditService) {
	api := newPlatformAuditAPI(service)
	v1.GET("/platform/audit-logs", api.getPlatformAuditLogs)
}

func (api *platformAuditAPI) getPlatformAuditLogs(ctx context.Context, c *app.RequestContext) {
	query, err := parsePlatformAuditQuery(c)
	if err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}

	result, err := api.service.QueryAuditLogs(ctx, query)
	if err != nil {
		if errors.Is(err, ports.ErrPlatformAuditInvalid) {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		writeInstanceError(c, http.StatusInternalServerError, "PLATFORM_AUDIT_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, platformAuditResponseFromResult(result))
}

// parsePlatformAuditQuery 解析并校验查询参数。
// time_from/time_to 必须成对且 time_from<=time_to；page_size 默认 20、上限 100 钳制。
func parsePlatformAuditQuery(c *app.RequestContext) (ports.PlatformAuditLogQuery, error) {
	timeFromStr := c.Query("time_from")
	timeToStr := c.Query("time_to")
	if timeFromStr == "" || timeToStr == "" {
		return ports.PlatformAuditLogQuery{}, errors.New("time_from and time_to are required")
	}
	timeFrom, err := time.Parse(time.RFC3339, timeFromStr)
	if err != nil {
		return ports.PlatformAuditLogQuery{}, errors.New("invalid time_from: must be RFC3339")
	}
	timeTo, err := time.Parse(time.RFC3339, timeToStr)
	if err != nil {
		return ports.PlatformAuditLogQuery{}, errors.New("invalid time_to: must be RFC3339")
	}
	if timeFrom.After(timeTo) {
		return ports.PlatformAuditLogQuery{}, errors.New("time_from must not be after time_to")
	}

	pageSize := 20
	if v := c.Query("page_size"); v != "" {
		parsed, err := parseIntQuery(c, "page_size")
		if err != nil {
			return ports.PlatformAuditLogQuery{}, err
		}
		pageSize = parsed
	}

	return ports.PlatformAuditLogQuery{
		TimeFrom:     &timeFrom,
		TimeTo:       &timeTo,
		User:         c.Query("user"),
		Verb:         c.Query("verb"),
		ResourceType: c.Query("resource_type"),
		Namespace:    c.Query("namespace"),
		After:        c.Query("after"),
		PageSize:     pageSize,
		Keyword:      c.Query("keyword"),
	}, nil
}

// parseIntQuery 把查询参数解析为 int；非法返回 BAD_REQUEST 语义错误。
func parseIntQuery(c *app.RequestContext, name string) (int, error) {
	var parsed int
	if v, err := strconv.Atoi(c.Query(name)); err != nil {
		return 0, errors.New("invalid " + name + ": must be an integer")
	} else {
		parsed = v
	}
	return parsed, nil
}

func platformAuditResponseFromResult(result ports.PlatformAuditLogResult) platformAuditLogResponse {
	items := make([]platformAuditLogItemResponse, 0, len(result.Items))
	for _, item := range result.Items {
		groups := item.User.Groups
		if groups == nil {
			groups = []string{}
		}
		items = append(items, platformAuditLogItemResponse{
			AuditID:   item.AuditID,
			Timestamp: item.Timestamp.UTC().Format(time.RFC3339),
			Verb:      item.Verb,
			User: platformAuditUserResponse{
				Username: item.User.Username,
				Groups:   groups,
			},
			Resource: platformAuditResourceResponse{
				Namespace: item.Resource.Namespace,
				Resource:  item.Resource.Resource,
				Name:      item.Resource.Name,
			},
			ResponseCode: item.ResponseCode,
			Detail: platformAuditDetailResponse{
				RequestURI: item.Detail.RequestURI,
				UserAgent:  item.Detail.UserAgent,
			},
		})
	}
	return platformAuditLogResponse{
		Items:       items,
		NextAfter:   result.NextAfter,
		TotalApprox: result.TotalApprox,
		DevProfile: coreDevProfileResponse{
			Mode:         result.DevProfile.Mode,
			Provider:     result.DevProfile.Provider,
			RealProvider: result.DevProfile.RealProvider,
			Reason:       result.DevProfile.Reason,
		},
	}
}
