package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PermissionStore 是权威权限查询的接口；生产注入 *permissionStore，测试注入 spy。
type PermissionStore interface {
	Allows(ctx context.Context, principal principalRecord, resource, action, boundary string) (bool, error)
}

type permissionStore struct {
	db *pgxpool.Pool
}

func newPermissionStore(db *pgxpool.Pool) *permissionStore {
	return &permissionStore{db: db}
}

// Permission 对应 roles.permissions 的单条记录；Scope 为空时按 tenant 处理。
type Permission struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
	Scope    string   `json:"scope"`
}

var errInvalidPermissionScope = errors.New("invalid permission scope")

var errInvalidAuthorizationBoundary = errors.New("invalid authorization boundary")

// permissionsFromScopes 将签名凭证携带的 permissions（格式 scope:<resource>:<action>）
// 解析为 Permission 结构；boundary 继承已验证 Principal 的 domain。
// 缺前缀、空 scope、未知格式或非法 resource/action 一律 fail closed。
func permissionsFromScopes(permissions []string, boundary string) ([]Permission, error) {
	if boundary != "tenant" && boundary != "platform" {
		return nil, fmt.Errorf("%w: boundary", errInvalidPermissionScope)
	}
	var result []Permission
	for _, raw := range permissions {
		normalized := strings.TrimSpace(raw)
		if !strings.HasPrefix(normalized, "scope:") {
			return nil, fmt.Errorf("%w: prefix", errInvalidPermissionScope)
		}
		value := strings.TrimPrefix(normalized, "scope:")
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 || !validScopePart(parts[0]) || !validScopePart(parts[1]) {
			return nil, fmt.Errorf("%w: resource/action", errInvalidPermissionScope)
		}
		result = append(result, Permission{Resource: parts[0], Actions: []string{parts[1]}, Scope: boundary})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: empty", errInvalidPermissionScope)
	}
	return result, nil
}

// userPermissions 从 users -> user_roles -> roles.permissions 读取权威 user 权限。
// platform user 只取平台 built-in role；tenant user 取本租户 role 加平台 built-in role，
// 最终能否放行仍由 evaluator 的 required boundary 限制。
func (s *permissionStore) userPermissions(ctx context.Context, principal principalRecord) ([]Permission, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.permissions
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.id = $1
		  AND u.status = 'active'
		  AND (($2 = 'platform' AND u.tenant_id IS NULL AND r.tenant_id IS NULL)
		    OR ($2 = 'tenant' AND u.tenant_id = $3
		        AND (r.tenant_id = $3 OR r.tenant_id IS NULL)))
	`, principal.SubjectID, principal.Domain, nullableTenant(principal.TenantID))
	if err != nil {
		return nil, fmt.Errorf("query authoritative permissions: %w", err)
	}
	defer rows.Close()
	return decodePermissionRows(rows)
}

func (s *permissionStore) Allows(
	ctx context.Context, principal principalRecord,
	resource, action, requiredBoundary string,
) (bool, error) {
	var permissions []Permission
	var err error
	switch principal.Kind {
	case "user":
		permissions, err = s.userPermissions(ctx, principal)
	case "api_key", "service":
		permissions, err = permissionsFromScopes(principal.Permissions, principal.Domain)
	default:
		return false, errors.New("unsupported principal kind")
	}
	if err != nil {
		return false, err
	}
	for _, permission := range permissions {
		if permissionAllows(permission, resource, action, requiredBoundary) {
			return true, nil
		}
	}
	return false, nil
}

// permissionAllows 判断单条权限是否覆盖 resource/action/required boundary。
// own 分支只表示 permission scope 允许进入 own-boundary operation，
// 不证明目标对象属于当前 user；对象所有权仍由 handler/store/RLS 校验。
func permissionAllows(p Permission, resource, action, requiredBoundary string) bool {
	resourceMatch := p.Resource == resource || p.Resource == "*"
	actionMatch := slices.Contains(p.Actions, action) || slices.Contains(p.Actions, "*")
	if !resourceMatch || !actionMatch {
		return false
	}
	switch requiredBoundary {
	case "platform":
		return p.Scope == "platform"
	case "tenant":
		return p.Scope == "tenant"
	case "own":
		return p.Scope == "own" || p.Scope == "tenant"
	default:
		return false
	}
}

func decodePermissionRows(rows pgx.Rows) ([]Permission, error) {
	var result []Permission
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var decoded []Permission
		if err := json.Unmarshal(raw, &decoded); err != nil {
			// 权限数据不符合 003 契约时 fail closed，不能静默放行。
			return nil, fmt.Errorf("%w: decode", errInvalidPermissionScope)
		}
		result = append(result, decoded...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// nullableTenant 将 V2 Principal 的 tenant ID 字符串转为 SQL 参数；空值（platform）传 NULL。
func nullableTenant(tenantID string) any {
	if tenantID == "" {
		return nil
	}
	return tenantID
}
