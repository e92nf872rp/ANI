package service

import (
	"context"
	"strings"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	platformsettingsv1 "github.com/kubercloud/ani/pkg/generated/pb/platform_settings/v1"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// PlatformAdminService is the gRPC server for platform admin management.
type PlatformAdminService struct {
	platformsettingsv1.UnimplementedPlatformAdminServiceServer
	coreClient ports.CorePlatformUserClient
	auditStore ports.PlatformAdminAuditStore
}

var _ platformsettingsv1.PlatformAdminServiceServer = (*PlatformAdminService)(nil)

// NewPlatformAdminService constructs the service with Core client + audit store injected.
func NewPlatformAdminService(coreClient ports.CorePlatformUserClient, auditStore ports.PlatformAdminAuditStore) *PlatformAdminService {
	return &PlatformAdminService{
		coreClient: coreClient,
		auditStore: auditStore,
	}
}

// Register attaches this service to a gRPC server.
func (s *PlatformAdminService) Register(server *grpc.Server) {
	platformsettingsv1.RegisterPlatformAdminServiceServer(server, s)
}

func (s *PlatformAdminService) CreatePlatformAdmin(ctx context.Context, req *platformsettingsv1.CreatePlatformAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.create"
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.auditStore, action, nil, err)
		return nil, err
	}

	// 步骤 1：入参校验；失败审计写入 role_id（不含 password）
	// 幂等由网关层处理，本服务不消费 idempotency_key。
	if detail := validateCreatePlatformAdmin(req.GetEmail(), req.GetUsername(), req.GetDisplayName(), req.GetRoleId(), req.GetPassword()); detail != "" {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"role_id": strings.TrimSpace(req.GetRoleId())}, err)
		return nil, err
	}
	if s.coreClient == nil {
		err := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, nil, err)
		return nil, err
	}

	// 步骤 2：调 Core 创建（冲突透传；失败审计不含 role）
	createdID, err := s.coreClient.Create(ctx, ports.PlatformUserCreateInput{
		Email:       strings.TrimSpace(req.GetEmail()),
		Username:    strings.TrimSpace(req.GetUsername()),
		DisplayName: strings.TrimSpace(req.GetDisplayName()),
		RoleID:      strings.TrimSpace(req.GetRoleId()),
		Password:    req.GetPassword(),
	})
	if err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, nil, mapped)
		return nil, mapped
	}

	// 步骤 3：成功审计仅 target_id（不含 password/role）
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{
		"target_id": createdID,
	})

	return &commonv1.IdempotentResult{
		Id:      createdID,
		Message: "platform admin created",
	}, nil
}

func (s *PlatformAdminService) ListPlatformAdmins(ctx context.Context, req *platformsettingsv1.ListPlatformAdminsRequest) (*platformsettingsv1.ListPlatformAdminsResponse, error) {
	// 只读：不写审计。
	if s.coreClient == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
	}
	// 步骤 1：分页默认 limit=20，上限 100；校验 status/source 枚举
	limit := 20
	cursor := ""
	if req != nil && req.GetPage() != nil {
		if l := int(req.GetPage().GetLimit()); l > 0 {
			limit = l
			if limit > 100 {
				limit = 100
			}
		}
		cursor = req.GetPage().GetCursor()
	}
	if req != nil {
		if detail := validateListPlatformAdminFilters(req.GetStatus(), req.GetSource()); detail != "" {
			return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
		}
	}
	// 步骤 2：组装过滤条件（source 过滤枚举 local|third_party，透传 Core）
	filter := ports.PlatformUserListFilter{Limit: limit, Cursor: cursor}
	if req != nil {
		roleID := strings.TrimSpace(req.GetRoleId())
		if roleID != "" {
			if _, err := uuid.Parse(roleID); err != nil {
				return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "role_id must be a uuid")
			}
			filter.RoleID = roleID
		}
		filter.Status = req.GetStatus()
		filter.Source = req.GetSource()
		filter.Search = req.GetSearch()
	}
	// 步骤 3：调 Core List
	res, err := s.coreClient.List(ctx, filter)
	if err != nil {
		return nil, mapDomainError(err)
	}
	// 步骤 4：映射列表项（不含 email；剥 username 前缀；source 推断）
	items := make([]*platformsettingsv1.PlatformAdminListItem, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, toPlatformAdminListItem(it))
	}
	return &platformsettingsv1.ListPlatformAdminsResponse{
		Items:      items,
		NextCursor: res.NextCursor,
	}, nil
}

func (s *PlatformAdminService) ListPlatformAdminRoles(ctx context.Context, _ *platformsettingsv1.ListPlatformAdminRolesRequest) (*platformsettingsv1.ListPlatformAdminRolesResponse, error) {
	// 只读：不写审计。
	if s.coreClient == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
	}
	// 步骤 1：调 Core 角色列表
	roles, err := s.coreClient.ListPlatformRoles(ctx)
	if err != nil {
		return nil, mapDomainError(err)
	}
	// 步骤 2：映射 name + permissions 数组（原样）
	items := make([]*platformsettingsv1.PlatformRole, 0, len(roles))
	for _, role := range roles {
		item, mapErr := toPlatformRole(role)
		if mapErr != nil {
			return nil, businessError(codes.Internal, ports.ErrCoreUnavailable, mapErr.Error())
		}
		items = append(items, item)
	}
	return &platformsettingsv1.ListPlatformAdminRolesResponse{Items: items}, nil
}

func (s *PlatformAdminService) GetPlatformAdmin(ctx context.Context, req *platformsettingsv1.GetPlatformAdminRequest) (*platformsettingsv1.PlatformAdminDetail, error) {
	// 只读：不写审计。
	if s.coreClient == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
	}
	if req == nil || strings.TrimSpace(req.GetUserId()) == "" {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id required")
	}
	// 步骤 1：校验 userId UUID
	userID, err := uuid.Parse(strings.TrimSpace(req.GetUserId()))
	if err != nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id must be a uuid")
	}
	// 步骤 2：调 Core Get；不存在 → PLATFORM_USER_NOT_FOUND
	dto, err := s.coreClient.Get(ctx, userID)
	if err != nil {
		return nil, mapDomainError(err)
	}
	// 步骤 3：映射详情全字段（含 email/created_at；不含 password）
	return toPlatformAdminDetail(dto), nil
}

func (s *PlatformAdminService) GetPlatformAdminPermissions(ctx context.Context, req *platformsettingsv1.GetPlatformAdminPermissionsRequest) (*platformsettingsv1.PlatformAdminPermissions, error) {
	// 只读：不写审计。
	if s.coreClient == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
	}
	if req == nil || strings.TrimSpace(req.GetUserId()) == "" {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id required")
	}
	// 步骤 1：校验 userId UUID
	userID, err := uuid.Parse(strings.TrimSpace(req.GetUserId()))
	if err != nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id must be a uuid")
	}
	// 步骤 2：调 Core 权限查询
	dto, err := s.coreClient.GetPlatformUserPermissions(ctx, userID)
	if err != nil {
		return nil, mapDomainError(err)
	}
	// 步骤 3：映射 user_id / role / permissions
	perms, mapErr := toStructPermissions(dto.Permissions)
	if mapErr != nil {
		return nil, businessError(codes.Internal, ports.ErrCoreUnavailable, mapErr.Error())
	}
	return &platformsettingsv1.PlatformAdminPermissions{
		UserId:      dto.UserID,
		RoleId:      dto.RoleID,
		Role:        dto.Role,
		Permissions: perms,
	}, nil
}

func (s *PlatformAdminService) UpdatePlatformAdminRole(ctx context.Context, req *platformsettingsv1.UpdatePlatformAdminRoleRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.change_role"
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.auditStore, action, nil, err)
		return nil, err
	}
	// 步骤 1：校验 user_id / role_id（不白名单三角色；幂等仅外层网关，本服务不消费）
	userIDRaw := strings.TrimSpace(req.GetUserId())
	roleIDRaw := strings.TrimSpace(req.GetRoleId())
	if userIDRaw == "" {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id required")
		writeAuditFailure(ctx, s.auditStore, action, nil, err)
		return nil, err
	}
	userID, err := uuid.Parse(userIDRaw)
	if err != nil {
		mapped := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id must be a uuid")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userIDRaw}, mapped)
		return nil, mapped
	}
	if roleIDRaw == "" {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "role_id required")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, err)
		return nil, err
	}
	roleID, err := uuid.Parse(roleIDRaw)
	if err != nil {
		mapped := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "role_id must be a uuid")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	if s.coreClient == nil {
		err := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, err)
		return nil, err
	}

	// 步骤 2：改前取 old_role（不存在 → 404）；解析 new_role 名供审计（SPEC: old_role/new_role）
	current, err := s.coreClient.Get(ctx, userID)
	if err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	newRoleName := resolvePlatformRoleName(ctx, s.coreClient, roleID.String())

	// 步骤 3：调 Core ChangeRole（Services→Core 不传幂等键；幂等仅外层网关）
	if err := s.coreClient.ChangeRole(ctx, userID, roleID); err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{
			"target_id": userID.String(),
			"old_role":  current.Role,
			"new_role":  newRoleName,
		}, mapped)
		return nil, mapped
	}

	// 步骤 4：成功审计 target_id + old_role + new_role
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{
		"target_id": userID.String(),
		"old_role":  current.Role,
		"new_role":  newRoleName,
	})
	return &commonv1.IdempotentResult{
		Id:      userID.String(),
		Message: "platform admin role updated",
	}, nil
}

func (s *PlatformAdminService) ResetPlatformAdminPassword(ctx context.Context, req *platformsettingsv1.ResetPlatformAdminPasswordRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.reset_password"
	var userIDRaw string
	if req != nil {
		userIDRaw = req.GetUserId()
	}
	userID, err := parsePlatformAdminUserID(req == nil, userIDRaw, action, s.auditStore, ctx)
	if err != nil {
		return nil, err
	}
	// 步骤 1：Services 预校验 new_password 复杂度（Core 强校验）；幂等仅外层网关，本服务不消费 idempotency_key
	if detail := validatePasswordComplexity(req.GetNewPassword()); detail != "" {
		mapped := businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	if s.coreClient == nil {
		mapped := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 2：调 Core ResetPassword（bcrypt 与旧不同校验在 Core Store）
	if err := s.coreClient.ResetPassword(ctx, userID, req.GetNewPassword()); err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 3：成功审计（不含密码明文）
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()})
	return &commonv1.IdempotentResult{
		Id:      userID.String(),
		Message: "platform admin password reset",
	}, nil
}

func (s *PlatformAdminService) DisablePlatformAdmin(ctx context.Context, req *platformsettingsv1.DisablePlatformAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.disable"
	var userIDRaw string
	if req != nil {
		userIDRaw = req.GetUserId()
	}
	userID, err := parsePlatformAdminUserID(req == nil, userIDRaw, action, s.auditStore, ctx)
	if err != nil {
		return nil, err
	}
	if s.coreClient == nil {
		mapped := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 1：调 Core SetStatus(disabled)；最后管理员保护在 Core 事务内
	if err := s.coreClient.SetStatus(ctx, userID, "disabled"); err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 2：成功审计
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()})
	return &commonv1.IdempotentResult{
		Id:      userID.String(),
		Message: "platform admin disabled",
	}, nil
}

func (s *PlatformAdminService) EnablePlatformAdmin(ctx context.Context, req *platformsettingsv1.EnablePlatformAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.enable"
	var userIDRaw string
	if req != nil {
		userIDRaw = req.GetUserId()
	}
	userID, err := parsePlatformAdminUserID(req == nil, userIDRaw, action, s.auditStore, ctx)
	if err != nil {
		return nil, err
	}
	if s.coreClient == nil {
		mapped := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 1：调 Core SetStatus(active)；启用无最后管理员保护
	if err := s.coreClient.SetStatus(ctx, userID, "active"); err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 2：成功审计
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()})
	return &commonv1.IdempotentResult{
		Id:      userID.String(),
		Message: "platform admin enabled",
	}, nil
}

func (s *PlatformAdminService) DeletePlatformAdmin(ctx context.Context, req *platformsettingsv1.DeletePlatformAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "platform_admin.delete"
	var userIDRaw string
	if req != nil {
		userIDRaw = req.GetUserId()
	}
	userID, err := parsePlatformAdminUserID(req == nil, userIDRaw, action, s.auditStore, ctx)
	if err != nil {
		return nil, err
	}
	if s.coreClient == nil {
		mapped := businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core client not configured")
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 1：调 Core SoftDelete；最后管理员保护在 Core 事务内
	if err := s.coreClient.SoftDelete(ctx, userID); err != nil {
		mapped := mapDomainError(err)
		writeAuditFailure(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()}, mapped)
		return nil, mapped
	}
	// 步骤 2：成功审计
	writeAuditSuccess(ctx, s.auditStore, action, map[string]any{"target_id": userID.String()})
	return &commonv1.IdempotentResult{
		Id:      userID.String(),
		Message: "platform admin deleted",
	}, nil
}

func (s *PlatformAdminService) ListPlatformAdminAuditLogs(ctx context.Context, req *platformsettingsv1.ListPlatformAdminAuditLogsRequest) (*platformsettingsv1.ListPlatformAdminAuditLogsResponse, error) {
	// 只读：不写审计、不调 Core。
	if req == nil {
		req = &platformsettingsv1.ListPlatformAdminAuditLogsRequest{}
	}
	userIDRaw := strings.TrimSpace(req.GetUserId())
	if userIDRaw == "" {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id required")
	}
	userID, err := uuid.Parse(userIDRaw)
	if err != nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id must be a uuid")
	}
	if detail := validateListAuditLogFilters(req.GetAction(), req.GetResult()); detail != "" {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, detail)
	}
	if s.auditStore == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "audit store not configured")
	}
	// 步骤 1：分页默认 limit=20，上限 100
	limit := 20
	cursor := ""
	if page := req.GetPage(); page != nil {
		if l := int(page.GetLimit()); l > 0 {
			limit = l
			if limit > 100 {
				limit = 100
			}
		}
		cursor = page.GetCursor()
	}
	// 步骤 2：直查 audit_logs（target_id = userId；tenant_id IS NULL）
	listed, err := s.auditStore.ListUserAuditLogs(ctx, userID, ports.AuditLogFilter{
		Limit:  limit,
		Cursor: cursor,
		Action: strings.TrimSpace(req.GetAction()),
		Result: strings.TrimSpace(req.GetResult()),
	})
	if err != nil {
		return nil, mapDomainError(err)
	}
	// 步骤 3：映射 items[] + next_cursor
	items := make([]*platformsettingsv1.PlatformAdminAuditLog, 0, len(listed.Items))
	for _, it := range listed.Items {
		pb, mapErr := toPlatformAdminAuditLog(it)
		if mapErr != nil {
			return nil, businessError(codes.Internal, ports.ErrCoreUnavailable, mapErr.Error())
		}
		items = append(items, pb)
	}
	return &platformsettingsv1.ListPlatformAdminAuditLogsResponse{
		Items:      items,
		NextCursor: listed.NextCursor,
	}, nil
}

// parsePlatformAdminUserID 校验 path/body 中的 user_id；失败写审计并返回业务错误。
func parsePlatformAdminUserID(reqNil bool, userIDRaw, action string, audit ports.PlatformAdminAuditStore, ctx context.Context) (uuid.UUID, error) {
	if reqNil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, audit, action, nil, err)
		return uuid.Nil, err
	}
	userIDRaw = strings.TrimSpace(userIDRaw)
	if userIDRaw == "" {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id required")
		writeAuditFailure(ctx, audit, action, nil, err)
		return uuid.Nil, err
	}
	userID, err := uuid.Parse(userIDRaw)
	if err != nil {
		mapped := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "user_id must be a uuid")
		writeAuditFailure(ctx, audit, action, map[string]any{"target_id": userIDRaw}, mapped)
		return uuid.Nil, mapped
	}
	return userID, nil
}

func toPlatformAdminAuditLog(it ports.AuditLog) (*platformsettingsv1.PlatformAdminAuditLog, error) {
	var detailsPB *structpb.Struct
	if it.Details != nil {
		st, err := structpb.NewStruct(it.Details)
		if err != nil {
			return nil, err
		}
		detailsPB = st
	}
	var operatorPB *wrapperspb.StringValue
	if it.UserID != nil {
		operatorPB = wrapperspb.String(it.UserID.String())
	}
	return &platformsettingsv1.PlatformAdminAuditLog{
		Id:        it.ID.String(),
		Action:    it.Action,
		Resource:  it.Resource,
		Result:    it.Result,
		Details:   detailsPB,
		CreatedAt: timestamppb.New(it.CreatedAt),
		UserId:    operatorPB,
	}, nil
}

func toPlatformAdminListItem(it ports.PlatformUserDTO) *platformsettingsv1.PlatformAdminListItem {
	item := &platformsettingsv1.PlatformAdminListItem{
		Id:       it.ID,
		Username: stripPlatformUsernamePrefix(it.Username),
		RoleId:   it.RoleID,
		Role:     it.Role,
		Status:   it.Status,
		Source:   normalizePlatformSource(it.Username, it.Source),
	}
	if it.DisplayName != nil {
		item.DisplayName = *it.DisplayName
	}
	if it.LastLoginAt != nil {
		item.LastLoginAt = timestamppb.New(*it.LastLoginAt)
	}
	return item
}

func toPlatformAdminDetail(it ports.PlatformUserDTO) *platformsettingsv1.PlatformAdminDetail {
	detail := &platformsettingsv1.PlatformAdminDetail{
		Id:        it.ID,
		Email:     it.Email,
		Username:  stripPlatformUsernamePrefix(it.Username),
		RoleId:    it.RoleID,
		Role:      it.Role,
		Status:    it.Status,
		Source:    normalizePlatformSource(it.Username, it.Source),
		CreatedAt: timestamppb.New(it.CreatedAt),
	}
	if it.DisplayName != nil {
		detail.DisplayName = *it.DisplayName
	}
	if it.LastLoginAt != nil {
		detail.LastLoginAt = timestamppb.New(*it.LastLoginAt)
	}
	return detail
}

func toPlatformRole(role ports.PlatformRoleDTO) (*platformsettingsv1.PlatformRole, error) {
	perms, err := toStructPermissions(role.Permissions)
	if err != nil {
		return nil, err
	}
	return &platformsettingsv1.PlatformRole{
		Id:          role.ID,
		Name:        role.Name,
		Permissions: perms,
	}, nil
}

// resolvePlatformRoleName 将 role_id 解析为角色名供审计；失败时回退为 role_id 本身。
func resolvePlatformRoleName(ctx context.Context, client ports.CorePlatformUserClient, roleID string) string {
	if client == nil || strings.TrimSpace(roleID) == "" {
		return roleID
	}
	roles, err := client.ListPlatformRoles(ctx)
	if err != nil {
		return roleID
	}
	for _, role := range roles {
		if role.ID == roleID {
			return role.Name
		}
	}
	return roleID
}

func toStructPermissions(items []map[string]any) ([]*structpb.Struct, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]*structpb.Struct, 0, len(items))
	for _, item := range items {
		st, err := structpb.NewStruct(item)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// stripPlatformUsernamePrefix 对外响应剥除 local:/oidc: 存储前缀。
func stripPlatformUsernamePrefix(username string) string {
	switch {
	case strings.HasPrefix(username, "local:"):
		return strings.TrimPrefix(username, "local:")
	case strings.HasPrefix(username, "oidc:"):
		return strings.TrimPrefix(username, "oidc:")
	default:
		return username
	}
}

// normalizePlatformSource：oidc: → third_party，local: → local；已有合法 source 优先保留。
func normalizePlatformSource(username, source string) string {
	switch strings.TrimSpace(source) {
	case "local", "third_party", "unknown":
		return strings.TrimSpace(source)
	}
	switch {
	case strings.HasPrefix(username, "oidc:"):
		return "third_party"
	case strings.HasPrefix(username, "local:"):
		return "local"
	default:
		return "unknown"
	}
}
