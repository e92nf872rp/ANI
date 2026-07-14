# BOSS 平台集成与通知域

> **PRD/SPEC/HTML 同步**：integration 域 **4 模块**已于 2026-07-13 与满配详文对齐。

| 模块 | 详文 | HTML ID | 优先级 |
|---|---|---|:---:|
| 运维 Webhook | [`ops-webhook.md`](ops-webhook.md) | `boss.integration.webhook` | P1 |
| 企业通知集成 | [`enterprise-notification.md`](enterprise-notification.md) | `boss.integration.bot` | P2 |
| 运营系统对接 | [`ops-system-integration.md`](ops-system-integration.md) | `boss.integration.third_party` | P2 |
| 邮件通知 | [`email-notification.md`](email-notification.md) | `boss.integration.email` | P1 |

Phase 0 GAP：[`governance/boss-phase0-gap-integration.md`](../governance/boss-phase0-gap-integration.md)

```bash
python3 scripts/validate-boss-integration-gate.py
python3 scripts/sync-boss-integration-domain.py
```

权威顺序：详文 > SPEC > PRD > HTML 摘要。
