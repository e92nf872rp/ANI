package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
)

// TenantAdminSvcClient 基于 Core Go SDK（anisdk.Client）实现 ports.TenantAdminSvcClient。
// 仅调用 Core OpenAPI `/api/v1/admin/...` 租户用户 API，不直连 users / user_roles。
type TenantAdminSvcClient struct {
	sdk anisdk.Client
}

var _ ports.TenantAdminSvcClient = (*TenantAdminSvcClient)(nil)

// NewTenantAdminSvcClient 从环境变量构造 Core 租户管理员 API 客户端（CORE_API_BASE_URL / CORE_API_TOKEN）。
func NewTenantAdminSvcClient() ports.TenantAdminSvcClient {
	return &TenantAdminSvcClient{sdk: newCoreSDKClient()}
}

// MatchUser 调用 Core GET /admin/tenants/{id}/user-lookup。
func (c *TenantAdminSvcClient) MatchUser(ctx context.Context, tenantID uuid.UUID, email, username string) (uuid.UUID, error) {
	_ = ctx
	q := url.Values{}
	q.Set("email", strings.TrimSpace(email))
	q.Set("username", strings.TrimSpace(username))
	path := fmt.Sprintf("/admin/tenants/%s/user-lookup?%s", tenantID.String(), q.Encode())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return uuid.Nil, mapSDKError(err)
	}
	user, err := decodeTenantUser(raw)
	if err != nil {
		return uuid.Nil, err
	}
	return user.ID, nil
}

// IsAlreadyAdmin 经 GetUser 判断 role 是否为 tenant-admin。
func (c *TenantAdminSvcClient) IsAlreadyAdmin(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	user, err := c.GetUser(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	return user.Role == ports.TenantAdminRoleAdmin, nil
}

// GetUser 调用 Core GET /admin/tenants/{id}/users/{user_id}。
func (c *TenantAdminSvcClient) GetUser(ctx context.Context, tenantID, userID uuid.UUID) (ports.AdminWithTenant, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/users/%s", tenantID.String(), userID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.AdminWithTenant{}, mapSDKError(err)
	}
	return decodeTenantUser(raw)
}

// GetAdminDetail 与 GetUser 同 Core 端点；is_inviting / is_expired 由 service 合成。
func (c *TenantAdminSvcClient) GetAdminDetail(ctx context.Context, tenantID, userID uuid.UUID) (ports.AdminWithTenant, error) {
	return c.GetUser(ctx, tenantID, userID)
}

// ListTenantAdmins 调用 Core GET /admin/tenant-users。
func (c *TenantAdminSvcClient) ListTenantAdmins(ctx context.Context, filter ports.TenantAdminListFilter) (ports.ListResult, error) {
	_ = ctx
	q := url.Values{}
	if filter.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", filter.Limit))
	}
	if strings.TrimSpace(filter.Cursor) != "" {
		q.Set("cursor", strings.TrimSpace(filter.Cursor))
	}
	if filter.TenantID != nil {
		q.Set("tenant_id", filter.TenantID.String())
	}
	if strings.TrimSpace(filter.Status) != "" {
		q.Set("status", strings.TrimSpace(filter.Status))
	}
	if strings.TrimSpace(filter.Search) != "" {
		q.Set("search", strings.TrimSpace(filter.Search))
	}
	path := "/admin/tenant-users"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.ListResult{}, mapSDKError(err)
	}
	obj, err := asObject(raw)
	if err != nil {
		return ports.ListResult{}, err
	}
	itemsRaw, err := asObjectSlice(obj["items"])
	if err != nil {
		return ports.ListResult{}, err
	}
	items := make([]ports.AdminWithTenant, 0, len(itemsRaw))
	for _, it := range itemsRaw {
		user, decodeErr := decodeTenantUserObject(it)
		if decodeErr != nil {
			return ports.ListResult{}, decodeErr
		}
		items = append(items, user)
	}
	return ports.ListResult{
		Items:      items,
		NextCursor: strings.TrimSpace(stringField(obj, "next_cursor")),
	}, nil
}

// ChangeRole 调用 Core PUT /admin/tenants/{id}/users/{user_id}/role。
func (c *TenantAdminSvcClient) ChangeRole(ctx context.Context, tenantID, userID uuid.UUID, role string) error {
	_ = ctx
	headers, err := idempotencyHeaders()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/admin/tenants/%s/users/%s/role", tenantID.String(), userID.String())
	_, err = c.sdk.Request("PUT", path, anisdk.RequestOptions{
		Body:    map[string]any{"role": role},
		Headers: headers,
	})
	if err != nil {
		return mapSDKError(err)
	}
	return nil
}

// GetRolePermissions 调用 Core GET /admin/tenants/{id}/users/{user_id}/role。
func (c *TenantAdminSvcClient) GetRolePermissions(ctx context.Context, tenantID, userID uuid.UUID) (ports.UserPermissions, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/users/%s/role", tenantID.String(), userID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.UserPermissions{}, mapSDKError(err)
	}
	obj, err := asObject(raw)
	if err != nil {
		return ports.UserPermissions{}, err
	}
	uid, err := uuid.Parse(strings.TrimSpace(stringField(obj, "user_id")))
	if err != nil {
		return ports.UserPermissions{}, fmt.Errorf("%w: user_id: %v", ports.ErrCoreUnavailable, err)
	}
	tidRaw := strings.TrimSpace(stringField(obj, "tenant_id"))
	var tid *uuid.UUID
	if tidRaw != "" {
		parsed, parseErr := uuid.Parse(tidRaw)
		if parseErr != nil {
			return ports.UserPermissions{}, fmt.Errorf("%w: tenant_id: %v", ports.ErrCoreUnavailable, parseErr)
		}
		tid = &parsed
	}
	perms, err := decodePermissionEntries(obj["permissions"])
	if err != nil {
		return ports.UserPermissions{}, err
	}
	return ports.UserPermissions{
		UserID:      uid,
		TenantID:    tid,
		Role:        stringField(obj, "role"),
		Permissions: perms,
	}, nil
}

// GetChangeableRoles 调用 Core GET /admin/tenants/{id}/users/{user_id}/changeable-roles。
func (c *TenantAdminSvcClient) GetChangeableRoles(ctx context.Context, tenantID, userID uuid.UUID) (ports.ChangeableRoles, error) {
	_ = ctx
	path := fmt.Sprintf("/admin/tenants/%s/users/%s/changeable-roles", tenantID.String(), userID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{})
	if err != nil {
		return ports.ChangeableRoles{}, mapSDKError(err)
	}
	obj, err := asObject(raw)
	if err != nil {
		return ports.ChangeableRoles{}, err
	}
	optsRaw, err := asObjectSlice(obj["changeable_roles"])
	if err != nil {
		return ports.ChangeableRoles{}, err
	}
	opts := make([]ports.ChangeableRoleOption, 0, len(optsRaw))
	for _, it := range optsRaw {
		opts = append(opts, ports.ChangeableRoleOption{
			Role:  stringField(it, "role"),
			Label: stringField(it, "label"),
		})
	}
	return ports.ChangeableRoles{
		CurrentRole:     stringField(obj, "current_role"),
		ChangeableRoles: opts,
	}, nil
}

// SetStatus 调用 Core POST /admin/tenants/{id}/users/{user_id}/status。
func (c *TenantAdminSvcClient) SetStatus(ctx context.Context, tenantID, userID uuid.UUID, status string) error {
	_ = ctx
	headers, err := idempotencyHeaders()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/admin/tenants/%s/users/%s/status", tenantID.String(), userID.String())
	_, err = c.sdk.Request("POST", path, anisdk.RequestOptions{
		Body:    map[string]any{"status": status},
		Headers: headers,
	})
	if err != nil {
		return mapSDKError(err)
	}
	return nil
}

// SoftDelete 调用 Core DELETE /admin/tenants/{id}/users/{user_id}。
func (c *TenantAdminSvcClient) SoftDelete(ctx context.Context, tenantID, userID uuid.UUID) error {
	_ = ctx
	headers, err := idempotencyHeaders()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/admin/tenants/%s/users/%s", tenantID.String(), userID.String())
	_, err = c.sdk.Request("DELETE", path, anisdk.RequestOptions{Headers: headers})
	if err != nil {
		return mapSDKError(err)
	}
	return nil
}

// ResetPassword 调用 Core POST /admin/tenants/{id}/users/{user_id}/reset-password。
func (c *TenantAdminSvcClient) ResetPassword(ctx context.Context, tenantID, userID uuid.UUID, newPassword string) error {
	_ = ctx
	headers, err := idempotencyHeaders()
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/admin/tenants/%s/users/%s/reset-password", tenantID.String(), userID.String())
	_, err = c.sdk.Request("POST", path, anisdk.RequestOptions{
		Body:    map[string]any{"new_password": newPassword},
		Headers: headers,
	})
	if err != nil {
		return mapSDKError(err)
	}
	return nil
}

func decodeTenantUser(raw any) (ports.AdminWithTenant, error) {
	obj, err := asObject(raw)
	if err != nil {
		return ports.AdminWithTenant{}, err
	}
	return decodeTenantUserObject(obj)
}

func decodeTenantUserObject(obj map[string]any) (ports.AdminWithTenant, error) {
	id, err := uuid.Parse(strings.TrimSpace(stringField(obj, "id")))
	if err != nil {
		return ports.AdminWithTenant{}, fmt.Errorf("%w: user id: %v", ports.ErrCoreUnavailable, err)
	}
	tenantObj, _ := asObject(obj["tenant"])
	tenantIDRaw := strings.TrimSpace(stringField(tenantObj, "id"))
	if tenantIDRaw == "" {
		tenantIDRaw = strings.TrimSpace(stringField(obj, "tenant_id"))
	}
	tenantID, err := uuid.Parse(tenantIDRaw)
	if err != nil {
		return ports.AdminWithTenant{}, fmt.Errorf("%w: tenant id: %v", ports.ErrCoreUnavailable, err)
	}
	var displayName *string
	if dn := strings.TrimSpace(stringField(obj, "display_name")); dn != "" {
		displayName = &dn
	}
	var lastLogin *time.Time
	if t := parseOptionalTimeField(obj, "last_login_at"); t != nil {
		lastLogin = t
	}
	createdAt := parseTimeField(obj, "created_at")
	updatedAt := parseTimeField(obj, "updated_at")
	var createdPtr, updatedPtr *time.Time
	if !createdAt.IsZero() {
		createdPtr = &createdAt
	}
	if !updatedAt.IsZero() {
		updatedPtr = &updatedAt
	}
	return ports.AdminWithTenant{
		ID:          id,
		Email:       stringField(obj, "email"),
		Username:    stringField(obj, "username"),
		DisplayName: displayName,
		Role:        stringField(obj, "role"),
		Status:      stringField(obj, "status"),
		Source:      stringField(obj, "source"),
		LastLoginAt: lastLogin,
		CreatedAt:   createdPtr,
		UpdatedAt:   updatedPtr,
		Tenant: ports.TenantRef{
			ID:          tenantID,
			Name:        stringField(tenantObj, "name"),
			DisplayName: stringField(tenantObj, "display_name"),
		},
	}, nil
}

func decodePermissionEntries(raw any) ([]ports.PermissionEntry, error) {
	items, err := asObjectSlice(raw)
	if err != nil {
		return nil, err
	}
	out := make([]ports.PermissionEntry, 0, len(items))
	for _, it := range items {
		out = append(out, ports.PermissionEntry{
			Resource: stringField(it, "resource"),
			Action:   stringField(it, "action"),
			Scope:    stringField(it, "scope"),
		})
	}
	return out, nil
}

func parseOptionalTimeField(m map[string]any, key string) *time.Time {
	t := parseTimeField(m, key)
	if t.IsZero() {
		return nil
	}
	return &t
}
