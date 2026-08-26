package authz

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Mode 是 Gateway authz policy 的生效模式。
type Mode string

const (
	ModeOff   Mode = "off"
	ModePilot Mode = "pilot"
	ModeFull  Mode = "full"
)

// Config 承载 policy mode、auth mode 和 pilot operation allowlist。
type Config struct {
	Mode            Mode
	AuthMode        string
	PilotOperations map[string]struct{}
}

// ConfigFromEnv 从环境变量解析配置并执行 ValidateBase。
func ConfigFromEnv() (Config, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_AUTHZ_POLICY_MODE"))))
	if mode == "" {
		mode = ModeOff
	}
	if mode != ModeOff && mode != ModePilot && mode != ModeFull {
		return Config{}, fmt.Errorf("invalid GATEWAY_AUTHZ_POLICY_MODE %q", mode)
	}
	allow := map[string]struct{}{}
	for _, value := range strings.Split(os.Getenv("GATEWAY_AUTHZ_PILOT_OPERATIONS"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			allow[value] = struct{}{}
		}
	}
	cfg := Config{
		Mode:            mode,
		AuthMode:        strings.ToLower(strings.TrimSpace(os.Getenv("ANI_AUTH_MODE"))),
		PilotOperations: allow,
	}
	if err := cfg.ValidateBase(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ValidateBase 校验 mode 与 ANI_AUTH_MODE 组合及 pilot allowlist 约束。
func (c Config) ValidateBase() error {
	if c.AuthMode == "dev" && c.Mode != ModeOff {
		return errors.New("ANI_AUTH_MODE=dev only supports GATEWAY_AUTHZ_POLICY_MODE=off")
	}
	if c.Mode != ModePilot && len(c.PilotOperations) != 0 {
		return errors.New("pilot operations require GATEWAY_AUTHZ_POLICY_MODE=pilot")
	}
	return nil
}

// functionalMVPPilotOperations 冻结 Functional MVP 的 pilot 唯一集合。
// 只允许 listQuotaMeta；空集、额外项、拼写错误都必须启动失败。
var functionalMVPPilotOperations = map[string]struct{}{
	"listQuotaMeta": {},
}

func sameOperationSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for operationID := range left {
		if _, ok := right[operationID]; !ok {
			return false
		}
	}
	return true
}

// Validate 在 ValidateBase 之上执行带 registry 的完整配置校验：
// pilot 集合必须严格等于 Functional MVP 唯一集合，且每个 pilot operation
// 必须存在 generated policy。监听前调用，失败即启动失败。
func (c Config) Validate(registry Registry) error {
	if err := c.ValidateBase(); err != nil {
		return err
	}
	switch c.Mode {
	case ModeOff, ModeFull:
		// ValidateBase 已保证 allowlist 为空。
		return nil
	case ModePilot:
		if c.AuthMode != "auth_service" {
			return errors.New("pilot requires ANI_AUTH_MODE=auth_service")
		}
		if !sameOperationSet(c.PilotOperations, functionalMVPPilotOperations) {
			return errors.New("functional MVP pilot operations must equal {listQuotaMeta}")
		}
	default:
		return errors.New("unsupported authz policy mode")
	}
	for operationID := range functionalMVPPilotOperations {
		policy, ok := registry.LookupOperation(operationID)
		if !ok || policy.Source != PolicySourceGenerated {
			return fmt.Errorf("pilot operation %q has no generated policy", operationID)
		}
	}
	return nil
}

// EffectiveSource 按 mode 返回 policy 的有效 source。
func (c Config) EffectiveSource(policy Policy) PolicySource {
	if policy.Source == PolicySourcePublic {
		return PolicySourcePublic
	}
	switch c.Mode {
	case ModeOff:
		return PolicySourceLegacy
	case ModePilot:
		if policy.Source == PolicySourceGenerated {
			if _, ok := c.PilotOperations[policy.OperationID]; ok {
				return PolicySourceGenerated
			}
		}
		return PolicySourceLegacy
	case ModeFull:
		return policy.Source
	default:
		return PolicySourceLegacy
	}
}
