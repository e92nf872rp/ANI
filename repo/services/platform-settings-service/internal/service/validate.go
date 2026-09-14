package service

import (
	"net/mail"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// validateCreatePlatformAdmin 校验创建入参（SPEC §5.3）；失败返回可读 detail。
// role_id 仅做 UUID 非空检查：合法平台角色以 Core roles 表为准，后期增删角色不改本服务。
func validateCreatePlatformAdmin(email, username, displayName, roleID, password string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return "email required"
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(addr.Address, email) {
		return "email must be RFC 5322"
	}

	username = strings.TrimSpace(username)
	if n := len([]rune(username)); n < 1 || n > 64 {
		return "username must be 1-64 characters"
	}
	if strings.Contains(username, ":") {
		return "username must not contain ':'"
	}

	displayName = strings.TrimSpace(displayName)
	if n := len([]rune(displayName)); n < 1 || n > 128 {
		return "display_name must be 1-128 characters"
	}

	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return "role_id required"
	}
	if _, err := uuid.Parse(roleID); err != nil {
		return "role_id must be a uuid"
	}

	if detail := validatePasswordComplexity(password); detail != "" {
		return detail
	}
	return ""
}

// validatePasswordComplexity：8-64 字符，大写/小写/数字/特殊字符四类至少三类。
func validatePasswordComplexity(password string) string {
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

// validateListAuditLogFilters 校验 audit-logs query 的 result 枚举；action 透传 store。
func validateListAuditLogFilters(action, result string) string {
	_ = action
	switch strings.TrimSpace(result) {
	case "", "success", "failure":
		return ""
	default:
		return "result must be success or failure"
	}
}

// validateListPlatformAdminFilters 校验列表 query 枚举；role_id/search 透传 Core。
func validateListPlatformAdminFilters(status, source string) string {
	switch strings.TrimSpace(status) {
	case "", "active", "disabled":
	default:
		return "status must be active or disabled"
	}
	switch strings.TrimSpace(source) {
	case "", "local", "third_party":
	default:
		return "source must be local or third_party"
	}
	return ""
}
