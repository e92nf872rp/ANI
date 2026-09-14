package core

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
)

// CorePlatformUserClient 基于 Core Go SDK 实现 ports.CorePlatformUserClient。
type CorePlatformUserClient struct {
	sdk anisdk.Client
}

var _ ports.CorePlatformUserClient = (*CorePlatformUserClient)(nil)

// NewCorePlatformUserClient 从环境变量构造 Core 平台用户 API 客户端。
func NewCorePlatformUserClient() ports.CorePlatformUserClient {
	return &CorePlatformUserClient{sdk: newCoreSDKClient()}
}

// Create 调用 Core POST /admin/platform-users，仅返回新建用户 id（不回查详情）。
// 幂等由网关层处理；本客户端不重复实现 idempotency_key。
func (c *CorePlatformUserClient) Create(ctx context.Context, in ports.PlatformUserCreateInput) (string, error) {
	// 步骤 1：组装创建 body（明文 password 仅本次透传；不落日志）
	body := map[string]any{
		"email":        in.Email,
		"username":     in.Username,
		"display_name": in.DisplayName,
		"role_id":      in.RoleID,
		"password":     in.Password,
	}
	raw, err := c.sdk.Request("POST", "/admin/platform-users", anisdk.RequestOptions{Body: body, Context: ctx})
	if err != nil {
		return "", mapSDKError(err)
	}
	// 步骤 2：从创建响应取出 user id（Create 契约仅 id/message）
	obj, err := asObject(raw)
	if err != nil {
		return "", err
	}
	id := stringField(obj, "id")
	if id == "" {
		return "", fmt.Errorf("%w: create response missing id", ports.ErrCoreUnavailable)
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("%w: invalid create id: %v", ports.ErrCoreUnavailable, err)
	}
	return id, nil
}

// List 调用 Core GET /admin/platform-users（游标 + 过滤参数透传）。
func (c *CorePlatformUserClient) List(ctx context.Context, filter ports.PlatformUserListFilter) (ports.PlatformUserListDTO, error) {
	// 步骤 1：组装游标与过滤 query
	params := anisdk.CursorParams(filter.Limit, filter.Cursor)
	if filter.RoleID != "" {
		params["role_id"] = filter.RoleID
	}
	if filter.Status != "" {
		params["status"] = filter.Status
	}
	if filter.Source != "" {
		params["source"] = filter.Source
	}
	if filter.Search != "" {
		params["search"] = filter.Search
	}
	// 步骤 2：调用 Core 列表接口
	raw, err := c.sdk.Request("GET", "/admin/platform-users", anisdk.RequestOptions{Params: params, Context: ctx})
	if err != nil {
		return ports.PlatformUserListDTO{}, mapSDKError(err)
	}
	// 步骤 3：解码 items / next_cursor
	obj, err := asObject(raw)
	if err != nil {
		return ports.PlatformUserListDTO{}, err
	}
	itemsRaw, err := asObjectSlice(obj["items"])
	if err != nil {
		return ports.PlatformUserListDTO{}, err
	}
	items := make([]ports.PlatformUserDTO, 0, len(itemsRaw))
	for _, it := range itemsRaw {
		dto, err := decodePlatformUser(it)
		if err != nil {
			return ports.PlatformUserListDTO{}, err
		}
		items = append(items, dto)
	}
	return ports.PlatformUserListDTO{
		Items:      items,
		NextCursor: stringField(obj, "next_cursor"),
	}, nil
}

// Get 调用 Core GET /admin/platform-users/{userId}。
func (c *CorePlatformUserClient) Get(ctx context.Context, userID uuid.UUID) (ports.PlatformUserDTO, error) {
	// 步骤 1：拼路径并调用 Core 详情接口
	path := fmt.Sprintf("/admin/platform-users/%s", userID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{Context: ctx})
	if err != nil {
		return ports.PlatformUserDTO{}, mapSDKError(err)
	}
	// 步骤 2：解码为 PlatformUserDTO
	return decodePlatformUser(raw)
}

// ChangeRole 调用 Core PUT /admin/platform-users/{userId}/role。
// 幂等由网关层处理；本客户端不重复实现 idempotency_key。
func (c *CorePlatformUserClient) ChangeRole(ctx context.Context, userID uuid.UUID, roleID uuid.UUID) error {
	// 步骤 1：拼路径并提交新角色 id
	path := fmt.Sprintf("/admin/platform-users/%s/role", userID.String())
	_, err := c.sdk.Request("PUT", path, anisdk.RequestOptions{
		Body:    map[string]any{"role_id": roleID.String()},
		Context: ctx,
	})
	// 步骤 2：映射 Core 错误码为领域哨兵
	return mapSDKError(err)
}

// ResetPassword 调用 Core POST /admin/platform-users/{userId}/reset-password。
func (c *CorePlatformUserClient) ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	// 步骤 1：拼路径并提交新密码（明文仅本次透传）
	path := fmt.Sprintf("/admin/platform-users/%s/reset-password", userID.String())
	_, err := c.sdk.Request("POST", path, anisdk.RequestOptions{
		Body:    map[string]any{"new_password": newPassword},
		Context: ctx,
	})
	// 步骤 2：映射 Core 错误码为领域哨兵
	return mapSDKError(err)
}

// SetStatus 调用 Core disable/enable 端点（status=active → enable，否则 disable）。
func (c *CorePlatformUserClient) SetStatus(ctx context.Context, userID uuid.UUID, status string) error {
	// 步骤 1：按目标状态选择 disable / enable
	action := "disable"
	if status == "active" {
		action = "enable"
	}
	// 步骤 2：调用对应 Core 写接口
	path := fmt.Sprintf("/admin/platform-users/%s/%s", userID.String(), action)
	_, err := c.sdk.Request("POST", path, anisdk.RequestOptions{Context: ctx})
	// 步骤 3：映射 Core 错误码为领域哨兵
	return mapSDKError(err)
}

// SoftDelete 调用 Core DELETE /admin/platform-users/{userId}。
func (c *CorePlatformUserClient) SoftDelete(ctx context.Context, userID uuid.UUID) error {
	// 步骤 1：拼路径并调用 Core 软删除
	path := fmt.Sprintf("/admin/platform-users/%s", userID.String())
	_, err := c.sdk.Request("DELETE", path, anisdk.RequestOptions{Context: ctx})
	// 步骤 2：映射 Core 错误码为领域哨兵
	return mapSDKError(err)
}

// ListPlatformRoles 调用 Core GET /admin/platform-users/roles。
func (c *CorePlatformUserClient) ListPlatformRoles(ctx context.Context) ([]ports.PlatformRoleDTO, error) {
	// 步骤 1：调用 Core 角色列表接口
	raw, err := c.sdk.Request("GET", "/admin/platform-users/roles", anisdk.RequestOptions{Context: ctx})
	if err != nil {
		return nil, mapSDKError(err)
	}
	// 步骤 2：解码 items[]
	obj, err := asObject(raw)
	if err != nil {
		return nil, err
	}
	itemsRaw, err := asObjectSlice(obj["items"])
	if err != nil {
		return nil, err
	}
	items := make([]ports.PlatformRoleDTO, 0, len(itemsRaw))
	for _, it := range itemsRaw {
		dto, err := decodePlatformRole(it)
		if err != nil {
			return nil, err
		}
		items = append(items, dto)
	}
	return items, nil
}

// GetPlatformUserPermissions 调用 Core GET /admin/platform-users/{userId}/permissions。
func (c *CorePlatformUserClient) GetPlatformUserPermissions(ctx context.Context, userID uuid.UUID) (ports.PlatformUserPermissionsDTO, error) {
	// 步骤 1：拼路径并调用 Core 权限查询接口
	path := fmt.Sprintf("/admin/platform-users/%s/permissions", userID.String())
	raw, err := c.sdk.Request("GET", path, anisdk.RequestOptions{Context: ctx})
	if err != nil {
		return ports.PlatformUserPermissionsDTO{}, mapSDKError(err)
	}
	// 步骤 2：解码为 PlatformUserPermissionsDTO
	return decodePlatformUserPermissions(raw)
}
