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
