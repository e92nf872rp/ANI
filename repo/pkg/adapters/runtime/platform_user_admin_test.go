package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
	"golang.org/x/crypto/bcrypt"
)

func TestPostgresPlatformUserAdminStore_Create_Success(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{false}},          // username exists?
		quotaFakeRow{values: []any{"platform-ops"}}, // role name by id
		quotaFakeRow{values: []any{userID, now}},
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})

	out, err := store.Create(context.Background(), ports.PlatformUserCreate{
		Email: "ops@ani.io", Username: "ops", DisplayName: "Ops",
		RoleID: roleID, PasswordHash: "$2a$12$hashed",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.ID != userID || out.Username != "local:ops" || out.RoleID != roleID || out.Role != "platform-ops" || out.Source != "local" {
		t.Fatalf("out=%+v", out)
	}
	if !hasExec(tx, "INSERT INTO user_roles") {
		t.Fatalf("missing role bind: %#v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_Create_UsernameConflict(t *testing.T) {
	t.Parallel()
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{true}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.Create(context.Background(), ports.PlatformUserCreate{
		Email: "ops@ani.io", Username: "ops", DisplayName: "Ops",
		RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), PasswordHash: "hash",
	})
	if !errors.Is(err, ports.ErrUsernameAlreadyExists) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_Create_UniqueViolationMapped(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{false}},
		quotaFakeRow{values: []any{"platform-ops"}},
		quotaFakeRow{err: &pgconn.PgError{Code: "23505"}},
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.Create(context.Background(), ports.PlatformUserCreate{
		Email: "ops@ani.io", Username: "ops", DisplayName: "Ops",
		RoleID: roleID, PasswordHash: "hash",
	})
	if !errors.Is(err, ports.ErrUsernameAlreadyExists) {
		t.Fatalf("err=%v", err)
	}
	_ = userID
}

func TestPostgresPlatformUserAdminStore_Create_RoleNotFound(t *testing.T) {
	t.Parallel()
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{false}},
		quotaFakeRow{err: pgx.ErrNoRows},
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.Create(context.Background(), ports.PlatformUserCreate{
		Email: "ops@ani.io", Username: "ops", DisplayName: "Ops",
		RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000099"), PasswordHash: "hash",
	})
	if !errors.Is(err, ports.ErrRoleNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.Get(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	if !errors.Is(err, ports.ErrPlatformUserNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_Get_Success(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	dn := "Ops"
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	var lastLogin *time.Time
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, "ops@ani.io", "local:ops", &dn, uuid.MustParse("00000000-0000-0000-0000-000000000006"), "platform-ops", "active", lastLogin, created,
	}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	out, err := store.Get(context.Background(), userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.Username != "local:ops" || out.RoleID != uuid.MustParse("00000000-0000-0000-0000-000000000006") || out.Role != "platform-ops" {
		t.Fatalf("out=%+v", out)
	}
}

func TestPostgresPlatformUserAdminStore_List_Success(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	dn := "Ops"
	last := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{userID, "ops@ani.io", "local:ops", &dn, uuid.MustParse("00000000-0000-0000-0000-000000000006"), "platform-ops", "active", &last, created}},
	}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	res, err := store.List(context.Background(), ports.PlatformUserFilter{
		Limit: 20, RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Status: "active", Source: "local", Search: "ops",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Role != "platform-ops" || res.Items[0].RoleID != uuid.MustParse("00000000-0000-0000-0000-000000000006") {
		t.Fatalf("res=%+v", res)
	}
	if len(tx.querySQLs) != 1 {
		t.Fatalf("queries=%v", tx.querySQLs)
	}
	sql := tx.querySQLs[0]
	if strings.Contains(sql, "u.email ILIKE") {
		t.Fatalf("search must not match email: %s", sql)
	}
	if !strings.Contains(sql, "REGEXP_REPLACE(u.username") {
		t.Fatalf("search must strip username prefix before ILIKE: %s", sql)
	}
}

func TestPostgresPlatformUserAdminStore_List_NextCursor(t *testing.T) {
	t.Parallel()
	makeRow := func(id string, created time.Time) quotaFakeRow {
		dn := "Ops"
		var lastLogin *time.Time
		return quotaFakeRow{values: []any{
			uuid.MustParse(id), "ops@ani.io", "local:ops", &dn, uuid.MustParse("00000000-0000-0000-0000-000000000006"), "platform-ops", "active", lastLogin, created,
		}}
	}
	t1 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		makeRow("11111111-1111-1111-1111-111111111111", t1),
		makeRow("22222222-2222-2222-2222-222222222222", t2),
	}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	res, err := store.List(context.Background(), ports.PlatformUserFilter{Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Items) != 1 || res.NextCursor == "" {
		t.Fatalf("res=%+v", res)
	}
}

func TestPostgresPlatformUserAdminStore_List_InvalidSource(t *testing.T) {
	t.Parallel()
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := store.List(context.Background(), ports.PlatformUserFilter{Source: "invalid"})
	if !errors.Is(err, ports.ErrValidationFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_List_InvalidStatus(t *testing.T) {
	t.Parallel()
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := store.List(context.Background(), ports.PlatformUserFilter{Status: "pending"})
	if !errors.Is(err, ports.ErrValidationFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_SetStatus_LastAdmin(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{"active", "platform-admin"}}, // current status+role
		quotaFakeRow{values: []any{int64(0)}},                   // other active admins
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.SetStatus(context.Background(), userID, "disabled")
	if !errors.Is(err, ports.ErrLastPlatformAdmin) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_SetStatus_AlreadyDisabled(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"disabled", "platform-ops"}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.SetStatus(context.Background(), userID, "disabled")
	if !errors.Is(err, ports.ErrStatusUnchanged) {
		t.Fatalf("err=%v", err)
	}
	if hasExec(tx, "UPDATE users") {
		t.Fatalf("unchanged status must not update: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_SetStatus_AlreadyActive(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"active", "platform-ops"}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.SetStatus(context.Background(), userID, "active")
	if !errors.Is(err, ports.ErrStatusUnchanged) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_SetStatus_DisableSuccess(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{"active", "platform-ops"}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.SetStatus(context.Background(), userID, "disabled"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !hasExec(tx, "UPDATE users") {
		t.Fatalf("want UPDATE users, exec=%v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_SoftDelete_LastAdmin(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{"active", "platform-admin"}}, // current status+role
		quotaFakeRow{values: []any{int64(0)}},                   // other active admins
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.SoftDelete(context.Background(), userID)
	if !errors.Is(err, ports.ErrLastPlatformAdmin) {
		t.Fatalf("err=%v", err)
	}
	if hasExec(tx, "UPDATE users") {
		t.Fatalf("last-admin must not soft-delete: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_SoftDelete_Success(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{"active", "platform-admin"}}, // current status+role
		quotaFakeRow{values: []any{int64(1)}},                   // other active admins
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.SoftDelete(context.Background(), userID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if !hasExec(tx, "is_deleted = TRUE") {
		t.Fatalf("want soft-delete UPDATE, exec=%v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ResetPassword_ValidationFailed(t *testing.T) {
	t.Parallel()
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: &quotaFakeTx{}})
	err := store.ResetPassword(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), "short")
	if !errors.Is(err, ports.ErrValidationFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_ResetPassword_NotFound(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.ResetPassword(context.Background(), userID, "NewPass2@")
	if !errors.Is(err, ports.ErrPlatformUserNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_ResetPassword_SameAsOld(t *testing.T) {
	t.Parallel()
	const oldPwd = "Abcd1234!"
	hash, err := bcrypt.GenerateFromPassword([]byte(oldPwd), bcryptCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	hashStr := string(hash)
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{&hashStr}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err = store.ResetPassword(context.Background(), userID, oldPwd)
	if !errors.Is(err, ports.ErrPasswordSameAsOld) {
		t.Fatalf("err=%v", err)
	}
	if hasExec(tx, "UPDATE users SET password_hash") {
		t.Fatalf("same password must not update: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ResetPassword_Success(t *testing.T) {
	t.Parallel()
	const oldPwd = "OldPass1!"
	const newPwd = "NewPass2@"
	oldHash, err := bcrypt.GenerateFromPassword([]byte(oldPwd), bcryptCost)
	if err != nil {
		t.Fatalf("hash old: %v", err)
	}
	oldHashStr := string(oldHash)
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{&oldHashStr}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.ResetPassword(context.Background(), userID, newPwd); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if !hasExec(tx, "UPDATE users SET password_hash") {
		t.Fatalf("want password UPDATE, exec=%v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ResetPassword_NoExistingHash(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{(*string)(nil)}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.ResetPassword(context.Background(), userID, "NewPass2@"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if !hasExec(tx, "UPDATE users SET password_hash") {
		t.Fatalf("want password UPDATE, exec=%v", tx.execSQLs)
	}
}

func TestInferPlatformSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		username string
		want     string
	}{
		{"oidc:alice", "third_party"},
		{"local:ops", "local"},
		{"ops", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		if got := inferPlatformSource(tc.username); got != tc.want {
			t.Fatalf("inferPlatformSource(%q)=%q want %q", tc.username, got, tc.want)
		}
	}
}

func TestValidatePlatformUserCreateFields(t *testing.T) {
	t.Parallel()
	err := validatePlatformUserCreateFields("bad", "a:b", "", uuid.Nil, "")
	if !errors.Is(err, ports.ErrValidationFailed) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_ListPlatformRoles(t *testing.T) {
	t.Parallel()
	adminPerms := []byte(`[{"resource":"tenants","actions":["*"],"scope":"platform"}]`)
	opsPerms := []byte(`[{"resource":"resource_pool","actions":["read","list"],"scope":"platform"}]`)
	readonlyPerms := []byte(`[{"resource":"metering","actions":["read"],"scope":"platform"}]`)
	tx := &quotaFakeTx{}
	// fake 仅回放 SQL 过滤后的行：tenant-admin 等非 platform-% 不应出现在结果集。
	adminID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	opsID := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	roID := uuid.MustParse("00000000-0000-0000-0000-000000000008")
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{adminID, "platform-admin", adminPerms}},
		{values: []any{opsID, "platform-ops", opsPerms}},
		{values: []any{roID, "platform-readonly", readonlyPerms}},
	}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})

	roles, err := store.ListPlatformRoles(context.Background())
	if err != nil {
		t.Fatalf("ListPlatformRoles: %v", err)
	}
	if len(roles) != 3 {
		t.Fatalf("roles=%+v", roles)
	}
	sql := ""
	if len(tx.querySQLs) == 1 {
		sql = tx.querySQLs[0]
	}
	if !strings.Contains(sql, "tenant_id IS NULL") || !strings.Contains(sql, "name LIKE 'platform-%'") {
		t.Fatalf("sql must filter platform roles only: %q", sql)
	}
	if roles[0].Name != "platform-admin" {
		t.Fatalf("admin role=%+v", roles[0])
	}
	for _, role := range roles {
		if strings.HasPrefix(role.Name, "tenant-") || !strings.HasPrefix(role.Name, "platform-") {
			t.Fatalf("non-platform role leaked: %+v", role)
		}
		if len(role.Permissions) != 1 {
			t.Fatalf("permissions=%+v", role.Permissions)
		}
		p := role.Permissions[0]
		if p["resource"] == nil || p["actions"] == nil || p["scope"] == nil {
			t.Fatalf("permission missing keys: %+v", p)
		}
	}
}

func TestPostgresPlatformUserAdminStore_ListPlatformRoles_Empty(t *testing.T) {
	t.Parallel()
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: nil})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	roles, err := store.ListPlatformRoles(context.Background())
	if err != nil {
		t.Fatalf("ListPlatformRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("roles=%+v", roles)
	}
}

func TestPostgresPlatformUserAdminStore_GetPlatformUserPermissions_Success(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	raw := []byte(`[{"resource":"tenants","actions":["read"],"scope":"platform"}]`)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{uuid.MustParse("00000000-0000-0000-0000-000000000006"), "platform-readonly", raw}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})

	out, err := store.GetPlatformUserPermissions(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetPlatformUserPermissions: %v", err)
	}
	if out.UserID != userID || out.RoleID != uuid.MustParse("00000000-0000-0000-0000-000000000006") || out.Role != "platform-readonly" {
		t.Fatalf("out=%+v", out)
	}
	if len(out.Permissions) != 1 || out.Permissions[0]["resource"] != "tenants" {
		t.Fatalf("permissions=%+v", out.Permissions)
	}
	sql := ""
	if len(tx.querySQLs) == 1 {
		sql = tx.querySQLs[0]
	}
	if !strings.Contains(sql, "r.tenant_id IS NULL") || !strings.Contains(sql, "r.name LIKE 'platform-%'") ||
		!strings.Contains(sql, "u.is_deleted = FALSE") {
		t.Fatalf("sql must require platform role + not deleted: %q", sql)
	}
}

func TestPostgresPlatformUserAdminStore_GetPlatformUserPermissions_EmptyPermissions(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{uuid.MustParse("00000000-0000-0000-0000-000000000006"), "platform-ops", []byte(`[]`)}})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	out, err := store.GetPlatformUserPermissions(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetPlatformUserPermissions: %v", err)
	}
	if out.Role != "platform-ops" || len(out.Permissions) != 0 {
		t.Fatalf("want empty permissions slice, got %+v", out)
	}
}

func TestPostgresPlatformUserAdminStore_GetPlatformUserPermissions_NotFound(t *testing.T) {
	t.Parallel()
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.GetPlatformUserPermissions(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	if !errors.Is(err, ports.ErrPlatformUserNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPostgresPlatformUserAdminStore_GetPlatformUserPermissions_SoftDeletedOrNonPlatform(t *testing.T) {
	t.Parallel()
	// 软删 / 无平台角色绑定 / 仅绑 tenant 角色：SQL 过滤后均无行 → PLATFORM_USER_NOT_FOUND。
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	_, err := store.GetPlatformUserPermissions(context.Background(), uuid.MustParse("22222222-2222-2222-2222-222222222222"))
	if !errors.Is(err, ports.ErrPlatformUserNotFound) {
		t.Fatalf("err=%v", err)
	}
	if len(tx.querySQLs) != 1 || !strings.Contains(tx.querySQLs[0], "r.name LIKE 'platform-%'") {
		t.Fatalf("sql=%v", tx.querySQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ChangeRole_UpdateExisting(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{true}},                     // ensurePlatformUserExists
		quotaFakeRow{values: []any{"platform-ops"}},           // lookup new role name
		quotaFakeRow{values: []any{true}},                     // binding exists
		quotaFakeRow{values: []any{"active", "platform-ops"}}, // current status+role（同角色升级路径不触发 last-admin）
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.ChangeRole(context.Background(), userID, roleID); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
	if !hasExec(tx, "UPDATE user_roles") {
		t.Fatalf("want UPDATE user_roles, exec=%v", tx.execSQLs)
	}
	if hasExec(tx, "DELETE FROM user_roles") || hasExec(tx, "INSERT INTO user_roles") {
		t.Fatalf("update path must not delete/insert: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ChangeRole_InsertWhenMissing(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{true}},           // ensurePlatformUserExists
		quotaFakeRow{values: []any{"platform-ops"}}, // lookup role name
		quotaFakeRow{values: []any{false}},          // binding missing
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.ChangeRole(context.Background(), userID, roleID); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
	if !hasExec(tx, "INSERT INTO user_roles") {
		t.Fatalf("want INSERT user_roles, exec=%v", tx.execSQLs)
	}
	if hasExec(tx, "DELETE FROM user_roles") || hasExec(tx, "UPDATE user_roles") {
		t.Fatalf("insert path must not delete/update: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ChangeRole_LastPlatformAdmin(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{true}},                       // ensurePlatformUserExists
		quotaFakeRow{values: []any{"platform-ops"}},             // lookup new role
		quotaFakeRow{values: []any{true}},                       // binding exists
		quotaFakeRow{values: []any{"active", "platform-admin"}}, // current status+role
		quotaFakeRow{values: []any{int64(0)}},                   // other active admins
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	err := store.ChangeRole(context.Background(), userID, roleID)
	if !errors.Is(err, ports.ErrLastPlatformAdmin) {
		t.Fatalf("err=%v", err)
	}
	if hasExec(tx, "UPDATE user_roles") || hasExec(tx, "INSERT INTO user_roles") {
		t.Fatalf("last-admin must not mutate roles: %v", tx.execSQLs)
	}
}

func TestPostgresPlatformUserAdminStore_ChangeRole_DemoteWhenOtherAdminsExist(t *testing.T) {
	t.Parallel()
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{true}},                       // ensurePlatformUserExists
		quotaFakeRow{values: []any{"platform-ops"}},             // lookup new role
		quotaFakeRow{values: []any{true}},                       // binding exists
		quotaFakeRow{values: []any{"active", "platform-admin"}}, // current status+role
		quotaFakeRow{values: []any{int64(1)}},                   // other active admins
	)
	store := NewPostgresPlatformUserAdminStore(&quotaFakeStore{tx: tx})
	if err := store.ChangeRole(context.Background(), userID, roleID); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
	if !hasExec(tx, "UPDATE user_roles") {
		t.Fatalf("want UPDATE user_roles, exec=%v", tx.execSQLs)
	}
}

func TestDecodeRolePermissionsJSON(t *testing.T) {
	t.Parallel()
	empty, err := decodeRolePermissionsJSON(nil)
	if err != nil || empty != nil {
		t.Fatalf("nil raw: %v %#v", err, empty)
	}
	arr, err := decodeRolePermissionsJSON([]byte(`[{"resource":"metering","actions":["read"],"scope":"platform"}]`))
	if err != nil || len(arr) != 1 || arr[0]["resource"] != "metering" {
		t.Fatalf("arr=%v err=%v", arr, err)
	}
	if _, err := decodeRolePermissionsJSON([]byte(`{"bad":true}`)); err == nil {
		t.Fatal("want decode error for object JSON")
	}
}
