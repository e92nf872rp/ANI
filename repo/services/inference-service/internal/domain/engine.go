package domain

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxEngineEnvItems     = 32
	maxEngineCommandItems = 64
	maxEngineEnvNameLen   = 64
	maxEngineEnvValueLen  = 4096
	maxEngineCommandLen   = 4096
)

var posixEngineEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var reservedEngineEnvNames = map[string]struct{}{
	"CUDA_VISIBLE_DEVICES":       {},
	"NVIDIA_VISIBLE_DEVICES":     {},
	"NVIDIA_DRIVER_CAPABILITIES": {},
	"PYTHONPATH":                 {},
	"PATH":                       {},
	"LD_PRELOAD":                 {},
	"LD_LIBRARY_PATH":            {},
	"RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES": {},
}

// ValidateEngine 校验创建时冻结的租户 engine。nil 表示沿用平台默认。
func ValidateEngine(engine *Engine) error {
	if engine == nil {
		return nil
	}
	if len(engine.Env) > maxEngineEnvItems {
		return fmt.Errorf("engine.env exceeds %d items", maxEngineEnvItems)
	}
	if engine.Command != nil && len(engine.Command) == 0 {
		return fmt.Errorf("engine.command must not be empty")
	}
	if len(engine.Command) > maxEngineCommandItems {
		return fmt.Errorf("engine.command exceeds %d items", maxEngineCommandItems)
	}
	seen := map[string]struct{}{}
	for _, item := range engine.Env {
		name := strings.TrimSpace(item.Name)
		value := item.Value
		if name == "" || !posixEngineEnvName.MatchString(name) || len(name) > maxEngineEnvNameLen {
			return fmt.Errorf("engine.env name is invalid")
		}
		if strings.TrimSpace(value) == "" || len(value) > maxEngineEnvValueLen {
			return fmt.Errorf("engine.env value is invalid")
		}
		if _, reserved := reservedEngineEnvNames[name]; reserved {
			return fmt.Errorf("engine.env name %s is reserved", name)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("engine.env name %s is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	for _, part := range engine.Command {
		if strings.TrimSpace(part) == "" || len(part) > maxEngineCommandLen {
			return fmt.Errorf("engine.command item is invalid")
		}
	}
	return nil
}
