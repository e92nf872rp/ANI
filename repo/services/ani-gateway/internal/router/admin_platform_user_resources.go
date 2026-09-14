package router

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"golang.org/x/crypto/bcrypt"
)

// adminPlatformUserAPI holds PlatformUserAdminStore for Core /admin/platform-users*.
type adminPlatformUserAPI struct {
	store ports.PlatformUserAdminStore
}

// registerAdminPlatformUserResources registers Core platform-user endpoints:
//
//	POST   /admin/platform-users
//	GET    /admin/platform-users
//	GET    /admin/platform-users/:userId
//	PUT    /admin/platform-users/:userId/role
//	POST   /admin/platform-users/:userId/reset-password
//	POST   /admin/platform-users/:userId/disable
//	POST   /admin/platform-users/:userId/enable
//	DELETE /admin/platform-users/:userId
//	GET    /admin/platform-users/roles
//	GET    /admin/platform-users/:userId/permissions
func registerAdminPlatformUserResources(v1 *route.RouterGroup, store ports.PlatformUserAdminStore) {
	if store == nil {
		return
	}
	api := adminPlatformUserAPI{store: store}
	v1.POST("/admin/platform-users", api.createPlatformUser)
	v1.GET("/admin/platform-users", api.listPlatformUsers)
	v1.GET("/admin/platform-users/roles", api.listPlatformUserRoles)
	v1.GET("/admin/platform-users/:userId/permissions", api.getPlatformUserPermissions)
	v1.GET("/admin/platform-users/:userId", api.getPlatformUser)
	v1.PUT("/admin/platform-users/:userId/role", api.updatePlatformUserRole)
	v1.POST("/admin/platform-users/:userId/reset-password", api.resetPlatformUserPassword)
	v1.POST("/admin/platform-users/:userId/disable", api.disablePlatformUser)
	v1.POST("/admin/platform-users/:userId/enable", api.enablePlatformUser)
	v1.DELETE("/admin/platform-users/:userId", api.deletePlatformUser)
}

type adminPlatformUserCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Email          string `json:"email"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	RoleID         string `json:"role_id"`
	Password       string `json:"password"`
}

type adminPlatformUserRoleUpdateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	RoleID         string `json:"role_id"`
}

type adminPlatformUserResetPasswordRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	NewPassword    string `json:"new_password"`
}

type adminPlatformUserIdempotentRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type adminPlatformUserResponse struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	RoleID      string     `json:"role_id"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	Source      string     `json:"source"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (api *adminPlatformUserAPI) createPlatformUser(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 body（幂等由网关统一中间件处理，本 handler 不重复实现）
	var req adminPlatformUserCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid create request")
		return
	}
	// 步骤 2：密码复杂度校验（与 Store.ResetPassword / Services validate 对齐）
	if detail := validateAdminPlatformUserPassword(req.Password); detail != "" {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", detail)
		return
	}
	// 步骤 3：明文密码 bcrypt 后交给 Store（Store 不哈希）
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", "hash password failed")
		return
	}
	roleID, err := uuid.Parse(strings.TrimSpace(req.RoleID))
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "role_id must be a uuid")
		return
	}
	// 步骤 4：调 PlatformUserAdminStore.Create
	created, err := api.store.Create(ctx, ports.PlatformUserCreate{
		Email:        req.Email,
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		RoleID:       roleID,
		PasswordHash: string(hash),
	})
	if err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	// 步骤 5：返回 UserMutationResult
	// Create 响应仅 id/message；username 前缀在列表/详情由 toAdminPlatformUserResponse 剥除。
	c.JSON(http.StatusOK, map[string]string{
		"id":      created.ID.String(),
		"message": "platform user created",
	})
}

func (api *adminPlatformUserAPI) listPlatformUsers(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 limit 等 query
	limit := 20
	if raw := strings.TrimSpace(string(c.Query("limit"))); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be a positive integer")
			return
		}
		limit = n
		if limit > 100 {
			limit = 100
		}
	}
	// 步骤 2：调 Store.List 并映射响应
	var roleID uuid.UUID
	if raw := strings.TrimSpace(string(c.Query("role_id"))); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "role_id must be a uuid")
			return
		}
		roleID = parsed
	}
	res, err := api.store.List(ctx, ports.PlatformUserFilter{
		Limit:  limit,
		Cursor: string(c.Query("cursor")),
		RoleID: roleID,
		Status: string(c.Query("status")),
		Source: string(c.Query("source")),
		Search: string(c.Query("search")),
	})
	if err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	items := make([]adminPlatformUserResponse, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, toAdminPlatformUserResponse(it))
	}
	body := map[string]any{"items": items}
	if res.NextCursor != "" {
		body["next_cursor"] = res.NextCursor
	} else {
		body["next_cursor"] = nil
	}
	c.JSON(http.StatusOK, body)
}

func (api *adminPlatformUserAPI) getPlatformUser(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 path userId
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	// 步骤 2：调 Store.Get
	user, err := api.store.Get(ctx, userID)
	if err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminPlatformUserResponse(user))
}

func (api *adminPlatformUserAPI) listPlatformUserRoles(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：调 Store.ListPlatformRoles（不分页）
	roles, err := api.store.ListPlatformRoles(ctx)
	if err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	// 步骤 2：组装响应 items
	items := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		items = append(items, toAdminPlatformRoleJSON(role))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *adminPlatformUserAPI) getPlatformUserPermissions(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 path userId
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	// 步骤 2：调 Store.GetPlatformUserPermissions
	perms, err := api.store.GetPlatformUserPermissions(ctx, userID)
	if err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminPlatformUserPermissionsJSON(perms))
}

func (api *adminPlatformUserAPI) updatePlatformUserRole(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 path + body（幂等由网关统一中间件处理，本 handler 不校验 idempotency_key）
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	var req adminPlatformUserRoleUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid role update request")
		return
	}
	roleID, err := uuid.Parse(strings.TrimSpace(req.RoleID))
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "role_id must be a uuid")
		return
	}
	// 步骤 2：调 Store.ChangeRole
	if err := api.store.ChangeRole(ctx, userID, roleID); err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"id":      userID.String(),
		"message": "platform user role updated",
	})
}

func (api *adminPlatformUserAPI) resetPlatformUserPassword(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 path + body（明文密码仅本次透传；幂等由网关统一中间件处理）
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	var req adminPlatformUserResetPasswordRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid reset password request")
		return
	}
	// 步骤 2：调 Store.ResetPassword（Store 内 bcrypt）
	if err := api.store.ResetPassword(ctx, userID, req.NewPassword); err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"id":      userID.String(),
		"message": "platform user password reset",
	})
}

func (api *adminPlatformUserAPI) disablePlatformUser(ctx context.Context, c *app.RequestContext) {
	api.mutatePlatformUserStatus(ctx, c, "disabled", "platform user disabled")
}

func (api *adminPlatformUserAPI) enablePlatformUser(ctx context.Context, c *app.RequestContext) {
	api.mutatePlatformUserStatus(ctx, c, "active", "platform user enabled")
}

func (api *adminPlatformUserAPI) mutatePlatformUserStatus(ctx context.Context, c *app.RequestContext, status, message string) {
	// 步骤 1：解析 path（幂等由网关统一中间件处理，本 handler 不校验 idempotency_key）
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	var req adminPlatformUserIdempotentRequest
	// body 可空；兼容仅 header 幂等键或空 body 的调用方（Services→Core）
	_ = c.BindJSON(&req)
	// 步骤 2：调 Store.SetStatus
	if err := api.store.SetStatus(ctx, userID, status); err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"id":      userID.String(),
		"message": message,
	})
}

func (api *adminPlatformUserAPI) deletePlatformUser(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：解析 path（幂等由网关统一中间件处理，本 handler 不校验 idempotency_key）
	userID, ok := parseAdminPlatformUserID(c)
	if !ok {
		return
	}
	var req adminPlatformUserIdempotentRequest
	_ = c.BindJSON(&req)
	// 步骤 2：调 Store.SoftDelete
	if err := api.store.SoftDelete(ctx, userID); err != nil {
		writeAdminPlatformUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]string{
		"id":      userID.String(),
		"message": "platform user deleted",
	})
}

func parseAdminPlatformUserID(c *app.RequestContext) (uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Param("userId"))
	id, err := uuid.Parse(raw)
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "userId must be a uuid")
		return uuid.Nil, false
	}
	return id, true
}

func toAdminPlatformUserResponse(u ports.PlatformUserAdmin) adminPlatformUserResponse {
	dn := ""
	if u.DisplayName != nil {
		dn = *u.DisplayName
	}
	return adminPlatformUserResponse{
		ID:          u.ID.String(),
		Email:       u.Email,
		Username:    stripAdminPlatformUsernamePrefix(u.Username),
		DisplayName: dn,
		RoleID:      u.RoleID.String(),
		Role:        u.Role,
		Status:      u.Status,
		Source:      u.Source,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}

func stripAdminPlatformUsernamePrefix(username string) string {
	switch {
	case strings.HasPrefix(username, "local:"):
		return strings.TrimPrefix(username, "local:")
	case strings.HasPrefix(username, "oidc:"):
		return strings.TrimPrefix(username, "oidc:")
	default:
		return username
	}
}

func toAdminPlatformRoleJSON(role ports.PlatformRole) map[string]any {
	return map[string]any{
		"id":          role.ID.String(),
		"name":        role.Name,
		"permissions": role.Permissions,
	}
}

func toAdminPlatformUserPermissionsJSON(p ports.PlatformUserPermissions) map[string]any {
	return map[string]any{
		"user_id":     p.UserID.String(),
		"role_id":     p.RoleID.String(),
		"role":        p.Role,
		"permissions": p.Permissions,
	}
}

// validateAdminPlatformUserPassword：8-64 字符，大写/小写/数字/特殊字符四类至少三类。
func validateAdminPlatformUserPassword(password string) string {
	n := len([]rune(password))
	if n < 8 || n > 64 {
		return "password must be 8-64 characters"
	}
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			special = true
		}
	}
	classes := 0
	if upper {
		classes++
	}
	if lower {
		classes++
	}
	if digit {
		classes++
	}
	if special {
		classes++
	}
	if classes < 3 {
		return "password must include at least 3 of: upper, lower, digit, special"
	}
	return ""
}

func writeAdminPlatformUserError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrValidationFailed), errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrPlatformUserNotFound):
		writeDemoError(c, http.StatusNotFound, "PLATFORM_USER_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrRoleNotFound):
		writeDemoError(c, http.StatusNotFound, "ROLE_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrUsernameAlreadyExists):
		writeDemoError(c, http.StatusConflict, "USERNAME_ALREADY_EXISTS", err.Error())
	case errors.Is(err, ports.ErrLastPlatformAdmin):
		writeDemoError(c, http.StatusUnprocessableEntity, "LAST_PLATFORM_ADMIN", err.Error())
	case errors.Is(err, ports.ErrPasswordSameAsOld):
		writeDemoError(c, http.StatusUnprocessableEntity, "PASSWORD_SAME_AS_OLD", err.Error())
	case errors.Is(err, ports.ErrStatusUnchanged):
		writeDemoError(c, http.StatusUnprocessableEntity, "STATUS_UNCHANGED", err.Error())
	case errors.Is(err, ports.ErrRoleChangeInvalid):
		writeDemoError(c, http.StatusUnprocessableEntity, "ROLE_CHANGE_INVALID", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
