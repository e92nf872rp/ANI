package middleware

import (
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
)

const (
	ctxSandboxInstanceID = "sandbox_instance_id"
	ctxSandboxScopes     = "sandbox_scopes"
)

func setSandboxContext(c *app.RequestContext, claims sandboxtoken.Claims) {
	c.Set(ctxSandboxInstanceID, claims.InstanceID)
	c.Set(ctxSandboxScopes, claims.Scopes)
}

// GetSandboxInstanceID returns the instance bound to a sandbox token, if any.
func GetSandboxInstanceID(c *app.RequestContext) string {
	return c.GetString(ctxSandboxInstanceID)
}

func sandboxScopes(c *app.RequestContext) []string {
	v, ok := c.Get(ctxSandboxScopes)
	if !ok || v == nil {
		return nil
	}
	scopes, ok := v.([]string)
	if !ok {
		return nil
	}
	return scopes
}

func isSandboxSubresourcePath(path string) bool {
	_, resource, _, ok := parseSandboxSubresourcePath(path)
	return ok && resource != ""
}

// parseSandboxSubresourcePath extracts instance id and sandbox resource name.
// Expected: /api/v1/instances/{id}/sandbox/{resource}[...]
func parseSandboxSubresourcePath(path string) (instanceID, resource, remainder string, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// api v1 instances {id} sandbox {resource} ...
	if len(parts) < 6 {
		return "", "", "", false
	}
	if parts[0] != "api" || parts[1] != "v1" || parts[2] != "instances" || parts[4] != "sandbox" {
		return "", "", "", false
	}
	instanceID = parts[3]
	resource = parts[5]
	if instanceID == "" || resource == "" {
		return "", "", "", false
	}
	remainder = strings.Join(parts[6:], "/")
	return instanceID, resource, remainder, true
}

// sandboxTokenAllows checks instance binding and capability scopes for sandbox tokens.
// Platform-only operations (tokens, checkpoints, lifecycle) are denied.
func sandboxTokenAllows(c *app.RequestContext, path string) bool {
	boundInstance := GetSandboxInstanceID(c)
	instanceID, resource, _, ok := parseSandboxSubresourcePath(path)
	if !ok || boundInstance == "" || instanceID != boundInstance {
		return false
	}
	required, allowed := sandboxResourceRequiredScope(resource)
	if !allowed {
		return false
	}
	for _, scope := range sandboxScopes(c) {
		if scope == required {
			return true
		}
	}
	return false
}

func sandboxResourceRequiredScope(resource string) (scope string, allowed bool) {
	switch resource {
	case "files":
		return "files", true
	case "ports":
		return "ports", true
	case "code-runs":
		return "exec", true
	case "tokens", "checkpoints":
		return "", false
	default:
		return "", false
	}
}
