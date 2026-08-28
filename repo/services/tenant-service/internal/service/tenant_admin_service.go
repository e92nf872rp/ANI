package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	inviteTokenBytes          = 32
	inviteExpireDuration      = 72 * time.Hour
	maxTenantAdminUsernameLen = 64
)

// TenantAdminService 是租户管理员域的 gRPC 服务。
// 网关经 TenantAdminServiceClient 转发 /api/v1/svc/tenant-admins* 与 /tenants/{tenantId}/admins*。
type TenantAdminService struct {
	tenantv1.UnimplementedTenantAdminServiceServer
	core    ports.TenantAdminSvcClient
	tenants ports.TenantSvcClient
	store   ports.TenantAdminStore
	audit   ports.TenantPlanAuditStore // 复用 audit_logs 写入（与配额套餐同一 store）
}

var _ tenantv1.TenantAdminServiceServer = (*TenantAdminService)(nil)

// NewTenantAdminService 装配 Core 客户端、邀请 store 与审计 store。
// ListAvailableTenants 走 tenants；Invite 走 core + store，审计走 audit（writeAudit*）。
func NewTenantAdminService(core ports.TenantAdminSvcClient, tenants ports.TenantSvcClient, store ports.TenantAdminStore, audit ports.TenantPlanAuditStore) *TenantAdminService {
	return &TenantAdminService{core: core, tenants: tenants, store: store, audit: audit}
}

// Register 向 gRPC Server 注册本服务（由 services/pkg/bootstrap.RunGRPC 回调）。
func (s *TenantAdminService) Register(server *grpc.Server) {
	tenantv1.RegisterTenantAdminServiceServer(server, s)
}

// unimplemented 返回 gRPC Unimplemented 标准错误，供存根方法使用。
func unimplemented() error {
	return status.Error(codes.Unimplemented, ports.ErrNotImplemented.Error())
}

// ListAvailableTenants 返回非 disabled 租户列表（SPEC §5.1.11 / US-011）。
// 只读、无审计；经 Core GET /admin/tenant-admins/available-tenants（TenantService）。
func (s *TenantAdminService) ListAvailableTenants(ctx context.Context, _ *tenantv1.ListAvailableTenantsRequest) (*tenantv1.ListAvailableTenantsResponse, error) {
	// 步骤 1：校验 Core 租户客户端已注入
	if s.tenants == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant api unavailable")
	}
	// 步骤 2：调用 Core TenantSvcClient 拉取非 disabled 租户
	items, err := s.tenants.ListAvailableTenants(ctx)
	if err != nil {
		return nil, mapStoreError(err)
	}
	// 步骤 3：映射为 gRPC AvailableTenant 列表并返回
	out := make([]*tenantv1.AvailableTenant, 0, len(items))
	for _, t := range items {
		out = append(out, &tenantv1.AvailableTenant{
			Id:          t.ID.String(),
			Name:        t.Name,
			DisplayName: t.DisplayName,
			Status:      string(t.Status),
		})
	}
	return &tenantv1.ListAvailableTenantsResponse{Items: out}, nil
}

// ListTenantRoles 返回租户可分配角色列表（SPEC §5.1.12 / US-012 / FR-8）。
// 只读、无审计；经 Core GET /admin/tenants/{id}/roles。
func (s *TenantAdminService) ListTenantRoles(ctx context.Context, req *tenantv1.ListTenantRolesRequest) (*tenantv1.ListTenantRolesResponse, error) {
	// 步骤 1：校验依赖与路径参数
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
	}
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		return nil, err
	}

	// 步骤 2：Core 查询可分配角色（已排除 platform-*）
	roles, err := s.core.ListAssignableRoles(ctx, tenantID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 3：映射 gRPC 响应（id / tenant_id / name / permissions）
	out := make([]*tenantv1.TenantAssignableRole, 0, len(roles))
	for _, r := range roles {
		item := &tenantv1.TenantAssignableRole{
			Id:   r.ID.String(),
			Name: r.Name,
		}
		if r.TenantID != nil {
			item.TenantId = wrapperspb.String(r.TenantID.String())
		}
		perms := r.Permissions
		if perms == nil {
			perms = []any{}
		}
		lv, lvErr := structpb.NewList(perms)
		if lvErr != nil {
			return nil, businessError(codes.Internal, ports.ErrCoreUnavailable, "encode role permissions")
		}
		item.Permissions = lv
		out = append(out, item)
	}
	return &tenantv1.ListTenantRolesResponse{Items: out}, nil
}

// InviteTenantAdmin 邀请现有租户用户为管理员（SPEC §5.1.1 / US-001）。
// 匹配用户、校验已是 admin / pending 邀请后写 invitation + 审计；不改 users.status、不绑角色。
func (s *TenantAdminService) InviteTenantAdmin(ctx context.Context, req *tenantv1.InviteTenantAdminRequest) (*tenantv1.InvitationResult, error) {
	const action = "tenant_admin.invite"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if s.store == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	}

	// 步骤 2：校验入参
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	email := strings.TrimSpace(req.GetEmail())
	username := strings.TrimSpace(req.GetUsername())
	if err := validateInviteIdentity(email, username); err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
			"email": email, "username": username,
		}, err, &tenantID)
		return nil, err
	}

	// 步骤 3：在指定租户内按 email+username 匹配现有用户（不新建）
	userID, err := s.core.MatchUser(ctx, tenantID, email, username)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
			"email": email, "username": username,
		}, mapped, &tenantID)
		return nil, mapped
	}

	details := map[string]any{"target_id": userID.String()}

	// 步骤 4：已是本租户 tenant-admin → 409
	alreadyAdmin, err := s.core.IsAlreadyAdmin(ctx, tenantID, userID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}
	if alreadyAdmin {
		err := businessError(codes.AlreadyExists, ports.ErrTenantAdminAlreadyAdmin, "user is already a tenant admin")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, err, &tenantID)
		return nil, err
	}

	// 步骤 5：已有 inviting 邀请 → 409（引导重发）
	pending, err := s.store.HasPendingInvitation(ctx, tenantID, userID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}
	if pending {
		err := businessError(codes.AlreadyExists, ports.ErrTenantInvitationPending, "pending invitation exists; use resend")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, err, &tenantID)
		return nil, err
	}

	// 步骤 6：生成一次性 token，仅存 SHA-256 哈希
	token, tokenHash, err := generateInviteToken()
	if err != nil {
		mapped := status.Errorf(codes.Internal, "generate invite token: %v", err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}
	expireAt := time.Now().UTC().Add(inviteExpireDuration)

	// 步骤 7：INSERT tenant_admin_invitation（不改角色 / users.status）
	inv, err := s.store.InsertInvitation(ctx, ports.TenantAdminInvitation{
		TenantID:  tenantID,
		UserID:    userID,
		TokenHash: tokenHash,
		Status:    ports.InvitationStatusInviting,
		ExpireAt:  expireAt,
	})
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 8：写成功审计（details 含 target_id + token_hash；失败不阻断已成功业务）
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
		"target_id":  userID.String(),
		"token_hash": tokenHash,
	}, &tenantID)

	// 步骤 9：通知渠道占位（拼接邀请链接由通知服务后续承接）
	slog.Info("tenant_admin invite notification placeholder",
		"tenant_id", tenantID.String(),
		"invitation_id", inv.ID.String(),
		"target_id", userID.String(),
	)

	// 步骤 10：返回明文 token（仅本次）
	return &tenantv1.InvitationResult{
		Id:       inv.ID.String(),
		Token:    token,
		ExpireAt: timestamppb.New(inv.ExpireAt.UTC()),
		Message:  "invitation sent",
	}, nil
}

// ResendTenantAdminInvitation 重发邀请（SPEC §5.1.2 / US-002）。
// 仅最新一条 inviting/expired 可重发；终态 accepted/rejected → 409；无记录 → 404。
func (s *TenantAdminService) ResendTenantAdminInvitation(ctx context.Context, req *tenantv1.ResendTenantAdminInvitationRequest) (*tenantv1.InvitationResult, error) {
	const action = "tenant_admin.resend_invitation"

	// 步骤 1：校验依赖
	if s.store == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	}

	// 步骤 2：校验入参
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, &tenantID)
		return nil, err
	}

	details := map[string]any{"target_id": userID.String()}

	// 步骤 3：取最新邀请
	inv, err := s.store.GetLatestInvitation(ctx, tenantID, userID)
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 4：终态不可重发；仅 inviting / expired 允许
	switch inv.Status {
	case ports.InvitationStatusAccepted:
		err := businessError(codes.FailedPrecondition, ports.ErrTenantInvitationSettled, "invitation already accepted")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, err, &tenantID)
		return nil, err
	case ports.InvitationStatusRejected:
		err := businessError(codes.FailedPrecondition, ports.ErrTenantInvitationSettled, "invitation already rejected")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, err, &tenantID)
		return nil, err
	case ports.InvitationStatusInviting, ports.InvitationStatusExpired:
		// ok
	default:
		err := businessError(codes.FailedPrecondition, ports.ErrTenantInvitationSettled, "invitation status not resendable: "+inv.Status)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, err, &tenantID)
		return nil, err
	}

	// 步骤 5：重新生成 token，刷新过期时间，状态回归 inviting
	token, tokenHash, err := generateInviteToken()
	if err != nil {
		mapped := status.Errorf(codes.Internal, "generate invite token: %v", err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}
	expireAt := time.Now().UTC().Add(inviteExpireDuration)
	updated, err := s.store.UpdateInvitation(ctx, ports.TenantAdminInvitation{
		ID:        inv.ID,
		TokenHash: tokenHash,
		ExpireAt:  expireAt,
		Status:    ports.InvitationStatusInviting,
	})
	if err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 6：成功审计（含 target_id + token_hash）
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
		"target_id":  userID.String(),
		"token_hash": tokenHash,
	}, &tenantID)

	// 步骤 7：通知占位
	slog.Info("tenant_admin resend invitation notification placeholder",
		"tenant_id", tenantID.String(),
		"invitation_id", updated.ID.String(),
		"target_id", userID.String(),
	)

	// 步骤 8：明文 token 仅本次返回
	return &tenantv1.InvitationResult{
		Id:       updated.ID.String(),
		Token:    token,
		ExpireAt: timestamppb.New(updated.ExpireAt.UTC()),
		Message:  "invitation resent",
	}, nil
}

// ListAllTenantAdmins 跨租户管理员列表（SPEC §5.1.3 / US-003）。
// 只读、无审计；合并 Core tenant-admin 与本地 inviting/expired 邀请标记。
func (s *TenantAdminService) ListAllTenantAdmins(ctx context.Context, req *tenantv1.ListAllTenantAdminsRequest) (*tenantv1.ListAllTenantAdminsResponse, error) {
	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if s.store == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	}
	if req == nil {
		req = &tenantv1.ListAllTenantAdminsRequest{}
	}

	// 步骤 2：三选一校验 status / is_inviting / is_expired
	statusFilter := strings.TrimSpace(req.GetStatus())
	hasStatus := statusFilter != ""
	hasInviting := req.GetIsInviting() != nil
	hasExpired := req.GetIsExpired() != nil
	exclusive := 0
	if hasStatus {
		exclusive++
	}
	if hasInviting {
		exclusive++
	}
	if hasExpired {
		exclusive++
	}
	if exclusive > 1 {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "status, is_inviting, and is_expired are mutually exclusive")
	}
	if hasStatus && statusFilter != ports.TenantAdminUserStatusActive && statusFilter != ports.TenantAdminUserStatusDisabled {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "status must be active or disabled")
	}
	if hasInviting && !req.GetIsInviting().GetValue() {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "is_inviting filter only supports true")
	}
	if hasExpired && !req.GetIsExpired().GetValue() {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "is_expired filter only supports true")
	}

	// 步骤 3：解析 tenant_id / search / 分页
	var tenantID *uuid.UUID
	if raw := strings.TrimSpace(req.GetTenantId()); raw != "" {
		id, err := parseTenantAdminUUID(raw, "tenant_id")
		if err != nil {
			return nil, err
		}
		tenantID = &id
	}
	search := strings.TrimSpace(req.GetSearch())
	roleFilter := strings.TrimSpace(req.GetRole())
	if roleFilter != "" && roleFilter != ports.TenantAdminRoleAdmin && roleFilter != ports.TenantAdminRoleUser && roleFilter != ports.TenantAdminRoleAuditor {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "role must be tenant-admin, user, or auditor")
	}
	sourceFilter := strings.TrimSpace(req.GetSource())
	if sourceFilter != "" && sourceFilter != ports.TenantAdminSourceLocal && sourceFilter != ports.TenantAdminSourceThirdParty {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "source must be local or third_party")
	}
	limit := 20
	cursor := ""
	if page := req.GetPage(); page != nil {
		if page.GetLimit() > 0 {
			limit = int(page.GetLimit())
		}
		cursor = strings.TrimSpace(page.GetCursor())
	}
	if limit > 100 {
		limit = 100
	}

	// 步骤 4：拉取邀请标记（按过滤模式收窄）
	flagStatus := ""
	switch {
	case hasInviting:
		flagStatus = ports.InvitationStatusInviting
	case hasExpired:
		flagStatus = ports.InvitationStatusExpired
	}
	flags, err := s.store.ListInvitationFlags(ctx, tenantID, flagStatus)
	if err != nil {
		return nil, mapStoreError(err)
	}
	flagByKey := make(map[string]ports.InvitationFlag, len(flags))
	for _, f := range flags {
		flagByKey[tenantUserKey(f.TenantID, f.UserID)] = f
	}

	// 步骤 5：组装候选用户
	merged := make(map[string]ports.AdminWithTenant)
	switch {
	case hasInviting, hasExpired:
		// 仅邀请中 / 仅已过期：按标记批量 GetUser
		users, batchErr := s.batchGetUsersByFlags(ctx, flags)
		if batchErr != nil {
			return nil, batchErr
		}
		for _, f := range flags {
			if user, ok := users[f.UserID]; ok {
				merged[tenantUserKey(f.TenantID, f.UserID)] = user
			}
		}
	default:
		// 默认 / status：Core tenant-admin + 邀请中/已过期非 admin 用户
		admins, listErr := s.listAllCoreTenantAdmins(ctx, tenantID, statusFilter, search)
		if listErr != nil {
			return nil, listErr
		}
		for _, admin := range admins {
			merged[tenantUserKey(admin.Tenant.ID, admin.ID)] = admin
		}
		pendingFlags := make([]ports.InvitationFlag, 0)
		for _, f := range flags {
			key := tenantUserKey(f.TenantID, f.UserID)
			if _, ok := merged[key]; ok {
				continue
			}
			pendingFlags = append(pendingFlags, f)
		}
		if len(pendingFlags) > 0 {
			users, batchErr := s.batchGetUsersByFlags(ctx, pendingFlags)
			if batchErr != nil {
				return nil, batchErr
			}
			for _, f := range pendingFlags {
				if user, ok := users[f.UserID]; ok {
					if statusFilter != "" && user.Status != statusFilter {
						continue
					}
					merged[tenantUserKey(f.TenantID, f.UserID)] = user
				}
			}
		}
	}

	// 步骤 6：填充邀请标记 + search / role / source 过滤（邀请-only 路径未走 Core search）
	items := make([]ports.AdminWithTenant, 0, len(merged))
	for key, user := range merged {
		if search != "" && !tenantAdminSearchMatch(user, search) {
			continue
		}
		if roleFilter != "" && user.Role != roleFilter {
			continue
		}
		if sourceFilter != "" && user.Source != sourceFilter {
			continue
		}
		if f, ok := flagByKey[key]; ok {
			user.IsInviting = f.IsInviting
			user.IsExpired = f.IsExpired
		} else {
			user.IsInviting = false
			user.IsExpired = false
		}
		// 列表不回传 created_at/updated_at 字段语义由网关控制；此处保留排序用时间
		items = append(items, user)
	}

	// 步骤 7：按 created_at DESC, id DESC 排序并游标分页
	sort.Slice(items, func(i, j int) bool {
		ci, cj := adminCreatedAt(items[i]), adminCreatedAt(items[j])
		if !ci.Equal(cj) {
			return ci.After(cj)
		}
		return items[i].ID.String() > items[j].ID.String()
	})
	pageItems, nextCursor, pageErr := pageTenantAdmins(items, limit, cursor)
	if pageErr != nil {
		return nil, pageErr
	}

	// 步骤 8：映射 proto（列表不含 timestamps）
	out := make([]*tenantv1.AdminWithTenant, 0, len(pageItems))
	for _, it := range pageItems {
		out = append(out, adminWithTenantToProto(it, false))
	}
	return &tenantv1.ListAllTenantAdminsResponse{Items: out, NextCursor: nextCursor}, nil
}

// listAllCoreTenantAdmins 分页拉取 Core 租户管理员全量列表（游标翻页至 nextCursor 为空）。
func (s *TenantAdminService) listAllCoreTenantAdmins(ctx context.Context, tenantID *uuid.UUID, status, search string) ([]ports.AdminWithTenant, error) {
	const pageSize = 100
	var all []ports.AdminWithTenant
	cursor := ""
	for {
		res, err := s.core.ListTenantAdmins(ctx, ports.TenantAdminListFilter{
			Limit:    pageSize,
			Cursor:   cursor,
			TenantID: tenantID,
			Status:   status,
			Search:   search,
		})
		if err != nil {
			return nil, mapStoreError(err)
		}
		all = append(all, res.Items...)
		if res.NextCursor == "" || len(res.Items) == 0 {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// batchGetUsersByFlags 按 flag 的 (tenant_id, user_id) 批量查询 Core 用户。
// 同一 tenant_id 的 flag 合并为一次 BatchGetUsers 调用。
func (s *TenantAdminService) batchGetUsersByFlags(ctx context.Context, flags []ports.InvitationFlag) (map[uuid.UUID]ports.AdminWithTenant, error) {
	byTenant := make(map[uuid.UUID][]uuid.UUID)
	for _, f := range flags {
		byTenant[f.TenantID] = append(byTenant[f.TenantID], f.UserID)
	}
	out := make(map[uuid.UUID]ports.AdminWithTenant)
	for tenantID, uids := range byTenant {
		users, err := s.core.BatchGetUsers(ctx, tenantID, uids)
		if err != nil {
			return nil, mapStoreError(err)
		}
		for uid, user := range users {
			out[uid] = user
		}
	}
	return out, nil
}

// tenantUserKey 生成 tenant_id|user_id 复合键，用于去重合并。
func tenantUserKey(tenantID, userID uuid.UUID) string {
	return tenantID.String() + "|" + userID.String()
}

// tenantAdminSearchMatch 按 search 关键词模糊匹配 username / email / display_name（大小写不敏感）。
func tenantAdminSearchMatch(user ports.AdminWithTenant, search string) bool {
	q := strings.ToLower(strings.TrimSpace(search))
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(user.Username), q) {
		return true
	}
	if strings.Contains(strings.ToLower(user.Email), q) {
		return true
	}
	if user.DisplayName != nil && strings.Contains(strings.ToLower(*user.DisplayName), q) {
		return true
	}
	return false
}

// adminCreatedAt 安全提取用户的 created_at，为零值时返回 time.Time{}。
func adminCreatedAt(user ports.AdminWithTenant) time.Time {
	if user.CreatedAt != nil {
		return user.CreatedAt.UTC()
	}
	return time.Time{}
}

// pageTenantAdmins 对已排序的内存列表执行游标分页，返回当前页 items + nextCursor。
func pageTenantAdmins(items []ports.AdminWithTenant, limit int, cursor string) ([]ports.AdminWithTenant, string, error) {
	start := 0
	if strings.TrimSpace(cursor) != "" {
		createdAt, id, err := types.DecodeCursor(cursor)
		if err != nil {
			return nil, "", businessError(codes.InvalidArgument, ports.ErrValidationFailed, "invalid cursor")
		}
		start = len(items)
		for i, it := range items {
			ci := adminCreatedAt(it)
			if ci.Before(createdAt) || (ci.Equal(createdAt) && it.ID.String() < id.String()) {
				start = i
				break
			}
		}
	}
	if start >= len(items) {
		return []ports.AdminWithTenant{}, "", nil
	}
	end := start + limit
	nextCursor := ""
	if end < len(items) {
		last := items[end-1]
		nextCursor = types.EncodeCursor(adminCreatedAt(last), last.ID)
	} else {
		end = len(items)
	}
	return items[start:end], nextCursor, nil
}

// includeTimestamps 控制是否填充 created_at / updated_at（详情页需要，列表页不需要）。
func adminWithTenantToProto(user ports.AdminWithTenant, includeTimestamps bool) *tenantv1.AdminWithTenant {
	out := &tenantv1.AdminWithTenant{
		Id:         user.ID.String(),
		Email:      user.Email,
		Username:   user.Username,
		Role:       user.Role,
		Status:     user.Status,
		IsInviting: user.IsInviting,
		IsExpired:  user.IsExpired,
		Source:     user.Source,
		Tenant: &tenantv1.TenantAdminTenantRef{
			Id:          user.Tenant.ID.String(),
			Name:        user.Tenant.Name,
			DisplayName: user.Tenant.DisplayName,
		},
	}
	if user.DisplayName != nil {
		out.DisplayName = wrapperspb.String(*user.DisplayName)
	}
	if user.LastLoginAt != nil {
		out.LastLoginAt = timestamppb.New(user.LastLoginAt.UTC())
	}
	if includeTimestamps {
		if user.CreatedAt != nil {
			out.CreatedAt = timestamppb.New(user.CreatedAt.UTC())
		}
		if user.UpdatedAt != nil {
			out.UpdatedAt = timestamppb.New(user.UpdatedAt.UTC())
		}
	}
	return out
}

// GetTenantAdminDetail 管理员详情（SPEC §5.1.9 / US-004）。
// Core 用户全字段 + 本地邀请标记；只读、无审计。
func (s *TenantAdminService) GetTenantAdminDetail(ctx context.Context, req *tenantv1.GetTenantAdminDetailRequest) (*tenantv1.AdminWithTenant, error) {
	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if s.store == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	}
	if req == nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}

	// 步骤 3：Core 拉取用户详情（含 created_at/updated_at/tenant；username 已去前缀，source 已推断）
	user, err := s.core.GetAdminDetail(ctx, tenantID, userID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 4：按最新邀请 status 合成 is_inviting / is_expired（互斥）
	flags, err := s.store.GetInvitationFlags(ctx, tenantID, userID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	user.IsInviting = flags.IsInviting
	user.IsExpired = flags.IsExpired

	// 步骤 5：详情含 timestamps
	return adminWithTenantToProto(user, true), nil
}

// UpdateTenantAdminRole 修改管理员角色（SPEC §5.1.5 / US-005）。
// Core 按 role_id upsert 改绑 user_roles；写审计 tenant_admin.change_role。
func (s *TenantAdminService) UpdateTenantAdminRole(ctx context.Context, req *tenantv1.UpdateTenantAdminRoleRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant_admin.change_role"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}

	// 解析请求体
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}
	roleID, err := parseTenantAdminUUID(req.GetRoleId(), "role_id")
	if err != nil {
		mapped := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "role_id must be a valid uuid")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
			"tenant_id": tenantID.String(), "target_id": userID.String(),
		}, mapped, &tenantID)
		return nil, mapped
	}

	details := map[string]any{
		"tenant_id":   tenantID.String(),
		"target_id":   userID.String(),
		"new_role_id": roleID.String(),
	}
	// 步骤 2：读取旧角色（审计 old_role）
	var oldRoleID uuid.UUID
	oldRole := ""
	perms, getErr := s.core.GetRolePermissions(ctx, tenantID, userID)
	if getErr != nil {
		mapped := mapStoreError(getErr)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}
	oldRoleID = perms.RoleID
	oldRole = perms.Role
	details["old_role"] = oldRole
	if oldRoleID != uuid.Nil {
		details["old_role_id"] = oldRoleID.String()
	}
	// 步骤 3：Core 按 role_id 改绑
	if err := s.core.ChangeRole(ctx, tenantID, userID, roleID); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 4：读新角色名写入审计 new_role
	newRole := ""
	if perms, getErr := s.core.GetRolePermissions(ctx, tenantID, userID); getErr == nil {
		newRole = perms.Role
	}
	details["new_role"] = newRole
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, details, &tenantID)

	return &commonv1.IdempotentResult{Id: userID.String(), Message: "role updated"}, nil
}

// GetTenantAdminRole 查询管理员角色与权限（SPEC §5.1.8 / US-009）。
// 只读、无审计；Core JOIN user_roles + roles 返回 role 及 permissions JSONB；
// 仅租户成员（tenant_id 非空）可查，平台角色（platform-* 前缀）不可查。
func (s *TenantAdminService) GetTenantAdminRole(ctx context.Context, req *tenantv1.GetTenantAdminRoleRequest) (*tenantv1.UserPermissions, error) {
	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		return nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
	}
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}

	// 步骤 2：Core 查询权限（已拒绝平台账户 / platform-*）
	perms, err := s.core.GetRolePermissions(ctx, tenantID, userID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 3：映射 gRPC（TenantID 可为 nil — 平台账户场景）
	out := &tenantv1.UserPermissions{
		UserId: perms.UserID.String(),
		RoleId: perms.RoleID.String(),
		Role:   perms.Role,
	}
	if perms.TenantID != nil {
		out.TenantId = wrapperspb.String(perms.TenantID.String())
	}
	permItems := perms.Permissions
	if permItems == nil {
		permItems = []any{}
	}
	lv, lvErr := structpb.NewList(permItems)
	if lvErr != nil {
		return nil, businessError(codes.Internal, ports.ErrCoreUnavailable, "encode permissions")
	}
	out.Permissions = lv
	return out, nil
}

func (s *TenantAdminService) GetChangeableRoles(context.Context, *tenantv1.GetChangeableRolesRequest) (*tenantv1.GetChangeableRolesResponse, error) {
	return nil, unimplemented()
}

// ResetTenantAdminPassword 重置管理员密码（SPEC §5.1.7 / US-007）。
// 校验 new_password 复杂度；Core bcrypt(cost=12) 更新 password_hash；明文不落审计/日志/响应。
func (s *TenantAdminService) ResetTenantAdminPassword(ctx context.Context, req *tenantv1.ResetTenantAdminPasswordRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant_admin.reset_password"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	// 步骤 3：校验密码复杂度（明文不落审计）
	if err := validateTenantAdminPassword(req.GetNewPassword()); err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{
			"tenant_id": tenantID.String(), "target_id": userID.String(),
		}, err, &tenantID)
		return nil, err
	}

	details := map[string]any{
		"tenant_id": tenantID.String(),
		"target_id": userID.String(),
	}

	if err := s.core.ResetPassword(ctx, tenantID, userID, req.GetNewPassword()); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 5：写审计 success（details 不含 new_password）
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, details, &tenantID)
	return &commonv1.IdempotentResult{Id: userID.String(), Message: "password reset"}, nil
}

// DisableTenantAdmin 禁用管理员（SPEC §5.1.6 / US-008）。
func (s *TenantAdminService) DisableTenantAdmin(ctx context.Context, req *tenantv1.DisableTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant_admin.disable"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	details := map[string]any{
		"tenant_id": tenantID.String(),
		"target_id": userID.String(),
	}

	// 步骤 3：Core 更新状态（Core 层校验不可重复 disable）
	if err := s.core.SetStatus(ctx, tenantID, userID, ports.TenantAdminUserStatusDisabled); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 4：写审计 success
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, details, &tenantID)
	return &commonv1.IdempotentResult{Id: userID.String(), Message: "admin disabled"}, nil
}

// EnableTenantAdmin 启用管理员（SPEC §5.1.6 / US-008）。
func (s *TenantAdminService) EnableTenantAdmin(ctx context.Context, req *tenantv1.EnableTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant_admin.enable"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	details := map[string]any{
		"tenant_id": tenantID.String(),
		"target_id": userID.String(),
	}

	// 步骤 3：Core 更新状态（Core 层校验不可重复 enable）
	if err := s.core.SetStatus(ctx, tenantID, userID, ports.TenantAdminUserStatusActive); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 4：写审计 success
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, details, &tenantID)
	return &commonv1.IdempotentResult{Id: userID.String(), Message: "admin enabled"}, nil
}

// DeleteTenantAdmin 软删除管理员（SPEC §5.1.6 / US-008）。
// 不幂等；Core 写 is_deleted/deleted_at（不改 status）；写审计 tenant_admin.delete。
func (s *TenantAdminService) DeleteTenantAdmin(ctx context.Context, req *tenantv1.DeleteTenantAdminRequest) (*commonv1.IdempotentResult, error) {
	const action = "tenant_admin.delete"

	// 步骤 1：校验依赖
	if s.core == nil {
		return nil, businessError(codes.Unavailable, ports.ErrCoreUnavailable, "core tenant admin api unavailable")
	}
	if req == nil {
		err := businessError(codes.InvalidArgument, ports.ErrValidationFailed, "request required")
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, nil, err, nil)
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, map[string]any{"tenant_id": tenantID.String()}, err, &tenantID)
		return nil, err
	}

	details := map[string]any{
		"tenant_id": tenantID.String(),
		"target_id": userID.String(),
	}

	// 步骤 3：Core 软删除
	if err := s.core.SoftDelete(ctx, tenantID, userID); err != nil {
		mapped := mapStoreError(err)
		writeAuditFailure(ctx, s.audit, auditResourceTenantAdmin, action, details, mapped, &tenantID)
		return nil, mapped
	}

	// 步骤 4：写审计 success
	writeAuditSuccess(ctx, s.audit, auditResourceTenantAdmin, action, details, &tenantID)
	return &commonv1.IdempotentResult{Id: userID.String(), Message: "admin deleted"}, nil
}

// ListTenantAdminAuditLogs 查询指定管理员操作历史（SPEC §5.1.10 / US-010）。
// 只读、无审计；按 tenant_id + details.target_id 游标分页；不调 Core。
func (s *TenantAdminService) ListTenantAdminAuditLogs(ctx context.Context, req *tenantv1.ListTenantAdminAuditLogsRequest) (*tenantv1.ListTenantAdminAuditLogsResponse, error) {
	// 步骤 1：校验依赖
	if s.store == nil {
		return nil, businessError(codes.Unavailable, ports.ErrStoreUnavailable, "tenant admin store unavailable")
	}
	if req == nil {
		req = &tenantv1.ListTenantAdminAuditLogsRequest{}
	}

	// 步骤 2：解析路径参数
	tenantID, err := parseTenantAdminUUID(req.GetTenantId(), "tenant_id")
	if err != nil {
		return nil, err
	}
	userID, err := parseTenantAdminUUID(req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}

	// 步骤 3：游标分页入参
	limit := 20
	cursor := ""
	if page := req.GetPage(); page != nil {
		if page.GetLimit() > 0 {
			limit = int(page.GetLimit())
		}
		if limit > 100 {
			limit = 100
		}
		cursor = page.GetCursor()
	}

	// 步骤 4：查 audit_logs（目标管理员 = details.target_id）
	listed, err := s.store.ListAuditLogs(ctx, tenantID, userID, ports.TenantAdminAuditLogFilter{
		Limit:  limit,
		Cursor: cursor,
		Action: strings.TrimSpace(req.GetAction()),
		Result: strings.TrimSpace(req.GetResult()),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	// 步骤 5：映射 gRPC TenantAdminAuditLog
	items := make([]*tenantv1.TenantAdminAuditLog, 0, len(listed.Items))
	for _, it := range listed.Items {
		pb, mapErr := tenantAdminAuditLogToPB(it)
		if mapErr != nil {
			return nil, status.Errorf(codes.Internal, "INTERNAL_ERROR: encode audit log: %v", mapErr)
		}
		items = append(items, pb)
	}
	return &tenantv1.ListTenantAdminAuditLogsResponse{
		Items:      items,
		NextCursor: listed.NextCursor,
	}, nil
}

// tenantAdminAuditLogToPB 把管理员审计领域对象映射为 gRPC TenantAdminAuditLog。
func tenantAdminAuditLogToPB(log ports.AuditLogListItem) (*tenantv1.TenantAdminAuditLog, error) {
	details := log.Details
	if details == nil {
		details = map[string]any{}
	}
	st, err := structpb.NewStruct(details)
	if err != nil {
		return nil, err
	}
	out := &tenantv1.TenantAdminAuditLog{
		Id:        log.ID.String(),
		Action:    log.Action,
		Resource:  log.Resource,
		Result:    log.Result,
		Details:   st,
		CreatedAt: timestamppb.New(log.CreatedAt),
	}
	if log.UserID != nil {
		out.UserId = wrapperspb.String(log.UserID.String())
	}
	return out, nil
}

// validateInviteIdentity 校验邀请入参：email 合法且 username 非空、长度 1-64、不含冒号。
func validateInviteIdentity(email, username string) error {
	if email == "" {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "email required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "email must be a valid address")
	}
	if username == "" {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "username required")
	}
	n := utf8.RuneCountInString(username)
	if n < 1 || n > maxTenantAdminUsernameLen {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "username must be 1-64 characters")
	}
	if strings.Contains(username, ":") {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "username must not contain ':'")
	}
	return nil
}

// validateTenantAdminPassword 校验 8-64 字符且四类（大写/小写/数字/特殊）至少三类。
func validateTenantAdminPassword(password string) error {
	// 步骤 1：长度 8-64
	n := utf8.RuneCountInString(password)
	if n < 8 || n > 64 {
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "new_password must be 8-64 characters")
	}
	// 步骤 2：统计字符类别（大写/小写/数字/特殊）
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
	// 步骤 3：至少三类
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
		return businessError(codes.InvalidArgument, ports.ErrValidationFailed, "new_password complexity insufficient")
	}
	return nil
}

// parseTenantAdminUUID 解析并校验 UUID 路径/请求参数，空值或格式非法返回 InvalidArgument。
func parseTenantAdminUUID(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, field+" required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, businessError(codes.InvalidArgument, ports.ErrValidationFailed, field+" must be a uuid")
	}
	return id, nil
}

// generateInviteToken 生成 32 字节随机邀请 token，返回明文 token 与其 SHA-256 哈希（仅存哈希）。
func generateInviteToken() (token string, tokenHash string, err error) {
	buf := make([]byte, inviteTokenBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(token))
	tokenHash = hex.EncodeToString(sum[:])
	return token, tokenHash, nil
}
