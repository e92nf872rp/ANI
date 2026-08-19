package router

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/pkg/ports"
)

// TestWriteQuotaErrorMapping 验证 writeQuotaError 将 adapter 哨兵错误映射为正确的
// HTTP 状态码与 code（与 openapi v1.yaml 的响应约定一致）。
func TestWriteQuotaErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: ports.ErrInvalid, wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED"},
		{name: "tenant not found", err: ports.ErrTenantNotFound, wantStatus: http.StatusNotFound, wantCode: "TENANT_NOT_FOUND"},
		{name: "quota not found", err: ports.ErrQuotaNotFound, wantStatus: http.StatusNotFound, wantCode: "QUOTA_NOT_FOUND"},
		{name: "quota resource not registered", err: ports.ErrQuotaResourceNotRegistered, wantStatus: http.StatusUnprocessableEntity, wantCode: "QUOTA_RESOURCE_NOT_REGISTERED"},
		{name: "quota already exists", err: ports.ErrQuotaAlreadyExists, wantStatus: http.StatusConflict, wantCode: "QUOTA_ALREADY_EXISTS"},
		{name: "unexpected", err: errors.New("unknown"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &app.RequestContext{}
			writeQuotaError(c, test.err)
			body := string(c.Response.Body())
			if c.Response.StatusCode() != test.wantStatus {
				t.Fatalf("status = %d, want %d", c.Response.StatusCode(), test.wantStatus)
			}
			if !strings.Contains(body, test.wantCode) {
				t.Fatalf("body = %q, want contain code %q", body, test.wantCode)
			}
		})
	}
}

func TestWriteQuotaUpsertErrorMapping(t *testing.T) {
	const tenantID = "5dbb1d01-0000-4000-8000-000000000001"
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid",
			err:         fmt.Errorf("%w: total 不能为负数", ports.ErrInvalid),
			wantStatus:  http.StatusBadRequest,
			wantCode:    "VALIDATION_FAILED",
			wantMessage: "配额更新失败，已回滚。total 不能为负数",
		},
		{
			name:        "tenant not found",
			err:         ports.ErrTenantNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    "TENANT_NOT_FOUND",
			wantMessage: "租户不存在: tenant_id=" + tenantID,
		},
		{
			name:        "quota resource not registered",
			err:         fmt.Errorf("%w: resource_type 'gpu_count' 未在 resource_quota_meta 中注册或已禁用", ports.ErrQuotaResourceNotRegistered),
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    "QUOTA_RESOURCE_NOT_REGISTERED",
			wantMessage: "配额更新失败，已回滚。resource_type 'gpu_count'",
		},
		{
			name:        "quota update uncertain",
			err:         ports.ErrQuotaUpdateUncertain,
			wantStatus:  http.StatusNetworkAuthenticationRequired,
			wantCode:    "QUOTA_UPDATE_UNCERTAIN",
			wantMessage: "配额更新失败，无法确认事务状态",
		},
		{
			name:        "unexpected",
			err:         errors.New("unknown"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "INTERNAL",
			wantMessage: "internal server error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := &app.RequestContext{}
			writeQuotaUpsertError(c, tenantID, test.err)
			body := string(c.Response.Body())
			if c.Response.StatusCode() != test.wantStatus {
				t.Fatalf("status = %d, want %d", c.Response.StatusCode(), test.wantStatus)
			}
			if !strings.Contains(body, test.wantCode) {
				t.Fatalf("body = %q, want contain code %q", body, test.wantCode)
			}
			if !strings.Contains(body, test.wantMessage) {
				t.Fatalf("body = %q, want contain message %q", body, test.wantMessage)
			}
		})
	}
}
