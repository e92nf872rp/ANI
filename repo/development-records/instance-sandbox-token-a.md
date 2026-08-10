# INSTANCE-SANDBOX-TOKEN-A

> 日期：2026-08-02
> 范围：ANI Core / Instance Management / Sandbox signed token auth

## 目标

把 `POST …/sandbox/tokens` 从占位 `sandbox_token_<n>` 升级为可验证的短期访问凭证，并让 Gateway 用该凭证鉴权 sandbox 子资源。

## 边界

- 不改 OpenAPI v1
- 平台 Bearer / API Key 路径不变；sandbox token 仅可访问对应 instance 的 `/sandbox/{files,ports,code-runs}`
- 禁止用 sandbox token 再签发 token，或访问 checkpoints / lifecycle
- checkpoint real-provider、EIP/LB、替换平台登录均不在本批

## 实现要点

- `pkg/security/sandboxtoken`：HMAC 签发/校验（`ani.sbx.<payload>.<sig>`），claims 含 tid/iid/scopes/exp/jti
- `LocalSandboxRuntime.CreateToken` 签发签名 token；幂等仍按 idempotency_key 重放未过期结果
- Gateway Auth：识别 `ani.sbx.` Bearer，本地验签并注入 `scope=sandbox` + instance/scopes
- Gateway RBAC：`scope=sandbox` 跳过 auth-service permission check，按 instance 绑定与 capability scope 放行
- 签名密钥：`SANDBOX_TOKEN_SIGNING_KEY`；未设置时使用进程内随机密钥（同进程签发/验签）

## 验证

```bash
cd repo
go test ./pkg/security/sandboxtoken/ ./pkg/adapters/runtime/ -run 'TestIssueAndParse|TestLocalSandboxRuntimeCreateToken' -count=1
cd services/ani-gateway && go test ./internal/middleware/ ./internal/router/ -run 'TestAuthAcceptsSandbox|TestCreateSandboxToken|TestSandboxTokenAllows' -count=1
python3 scripts/validate_sandbox_live_gate.py
```

真实 live（2026-08-02）：

```bash
cd repo
python3 scripts/validate_sandbox_live_gate.py --live \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --name ani-sandbox-token-live1 \
  --image-ref docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12 \
  --evidence-output development-records/live-evidence/instance-sandbox-token-live-20260802.json
```

结果：`status=passed`  
evidence：`development-records/live-evidence/instance-sandbox-token-live-20260802.json`  
Gateway：`docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-token-20260802-v1`  
证明：`token_prefix=ani.sbx.`、sandbox token 列 files=200、再签发=403、错 instance=403；files/ports/code-run 仍通过。  
subresources：`code-run-real; files-real; ports-real; token-signed; checkpoint local-session-deferred`
