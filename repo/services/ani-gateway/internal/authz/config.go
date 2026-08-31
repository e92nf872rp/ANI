package authz

import (
	"os"
	"strings"
)

// Config 承载 auth mode。
// policy 路由恒为契约即开关：带 x-ani-authz（generated）的 operation 走 V2，
// 其余走 legacy；dev 环境无 auth-service，generated 同样回落 legacy（保留 dev 旁路）。
type Config struct {
	AuthMode string
}

// ConfigFromEnv 从环境变量解析配置。
func ConfigFromEnv() Config {
	return Config{
		AuthMode: strings.ToLower(strings.TrimSpace(os.Getenv("ANI_AUTH_MODE"))),
	}
}

// EffectiveSource 返回 policy 的有效 source：
// public 恒放行；dev 无 auth-service 一律 legacy；
// 其余按契约直通：generated→V2，legacy→legacy。
func (c Config) EffectiveSource(policy Policy) PolicySource {
	if policy.Source == PolicySourcePublic {
		return PolicySourcePublic
	}
	if c.AuthMode == "dev" {
		return PolicySourceLegacy
	}
	return policy.Source
}
