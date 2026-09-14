package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
)

const defaultCoreAPIBaseURL = "http://127.0.0.1:8080/api/v1"

// defaultCoreAPITimeout 是 platform-settings-service 调 Core API 的 HTTP 超时。
const defaultCoreAPITimeout = 10 * time.Second

func newCoreSDKClient() anisdk.Client {
	base := strings.TrimSpace(os.Getenv("CORE_API_BASE_URL"))
	if base == "" {
		base = defaultCoreAPIBaseURL
	}
	c := anisdk.NewClient(strings.TrimRight(base, "/"), strings.TrimSpace(os.Getenv("CORE_API_TOKEN")))
	// 使用独立 http.Client，避免修改全局 http.DefaultClient。
	c.HTTPClient = &http.Client{Timeout: defaultCoreAPITimeout}
	return c
}

func mapSDKError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr anisdk.APIError
	if errors.As(err, &apiErr) {
		detail := strings.TrimSpace(apiErr.Message)
		if detail == "" {
			detail = apiErr.Code
		}
		switch strings.TrimSpace(apiErr.Code) {
		case ports.ErrPlatformUserNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrPlatformUserNotFound, detail)
		case ports.ErrRoleNotFound.Error():
			return fmt.Errorf("%w: %s", ports.ErrRoleNotFound, detail)
		case ports.ErrUsernameAlreadyExists.Error():
			return fmt.Errorf("%w: %s", ports.ErrUsernameAlreadyExists, detail)
		case ports.ErrLastPlatformAdmin.Error():
			return fmt.Errorf("%w: %s", ports.ErrLastPlatformAdmin, detail)
		case ports.ErrPasswordSameAsOld.Error():
			return fmt.Errorf("%w: %s", ports.ErrPasswordSameAsOld, detail)
		case ports.ErrStatusUnchanged.Error():
			return fmt.Errorf("%w: %s", ports.ErrStatusUnchanged, detail)
		case ports.ErrRoleChangeInvalid.Error():
			return fmt.Errorf("%w: %s", ports.ErrRoleChangeInvalid, detail)
		case ports.ErrValidationFailed.Error():
			return fmt.Errorf("%w: %s", ports.ErrValidationFailed, detail)
		default:
			// Core 端点未实现时常返回 NOT_FOUND / NOT_IMPLEMENTED / 空码 → 统一 CORE_UNAVAILABLE。
			return fmt.Errorf("%w: %s", ports.ErrCoreUnavailable, detail)
		}
	}
	return fmt.Errorf("%w: %v", ports.ErrCoreUnavailable, err)
}

func asObject(v any) (map[string]any, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: empty response", ports.ErrCoreUnavailable)
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	if s, ok := v.(string); ok {
		var out map[string]any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("%w: decode object: %v", ports.ErrCoreUnavailable, err)
		}
		return out, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ports.ErrCoreUnavailable, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: decode object: %v", ports.ErrCoreUnavailable, err)
	}
	return out, nil
}

func asObjectSlice(v any) ([]map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected array", ports.ErrCoreUnavailable)
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		obj, err := asObject(it)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func optionalStringField(m map[string]any, key string) *string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	s := stringField(m, key)
	return &s
}

func timeField(m map[string]any, key string) *time.Time {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func decodePlatformUser(raw any) (ports.PlatformUserDTO, error) {
	obj, err := asObject(raw)
	if err != nil {
		return ports.PlatformUserDTO{}, err
	}
	created := timeField(obj, "created_at")
	out := ports.PlatformUserDTO{
		ID:          stringField(obj, "id"),
		Email:       stringField(obj, "email"),
		Username:    stringField(obj, "username"), // 含 local:/oidc: 前缀；Services 层对外响应剥除
		DisplayName: optionalStringField(obj, "display_name"),
		RoleID:      stringField(obj, "role_id"),
		Role:        stringField(obj, "role"),
		Status:      stringField(obj, "status"),
		Source:      stringField(obj, "source"),
		LastLoginAt: timeField(obj, "last_login_at"),
	}
	if created != nil {
		out.CreatedAt = *created
	}
	return out, nil
}

func decodePlatformRole(raw any) (ports.PlatformRoleDTO, error) {
	obj, err := asObject(raw)
	if err != nil {
		return ports.PlatformRoleDTO{}, err
	}
	perms, err := permissionsField(obj, "permissions")
	if err != nil {
		return ports.PlatformRoleDTO{}, err
	}
	return ports.PlatformRoleDTO{
		ID:          stringField(obj, "id"),
		Name:        stringField(obj, "name"),
		Permissions: perms,
	}, nil
}

func decodePlatformUserPermissions(raw any) (ports.PlatformUserPermissionsDTO, error) {
	obj, err := asObject(raw)
	if err != nil {
		return ports.PlatformUserPermissionsDTO{}, err
	}
	perms, err := permissionsField(obj, "permissions")
	if err != nil {
		return ports.PlatformUserPermissionsDTO{}, err
	}
	return ports.PlatformUserPermissionsDTO{
		UserID:      stringField(obj, "user_id"),
		RoleID:      stringField(obj, "role_id"),
		Role:        stringField(obj, "role"),
		Permissions: perms,
	}, nil
}

func permissionsField(m map[string]any, key string) ([]map[string]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: expected permissions array", ports.ErrCoreUnavailable)
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		obj, err := asObject(it)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, nil
}
