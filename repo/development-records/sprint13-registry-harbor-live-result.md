# SPRINT13-REGISTRY-HARBOR-LIVE-A — Harbor production-shaped live gate result

> 记录类型：Sprint 13 B-track production-shaped live result
> 完成日期：2026-06-30
> 范围：ANI Core Registry / Harbor v2.0 真实 provider
> 状态：**production-shaped gate passed**；不代表 full platform production ready

## 环境

| 项 | 值 |
|---|---|
| Harbor | 节点 `192.168.102.81`，域名 `docker.kubercon.local`（`/etc/hosts` 解析），HTTPS 自签 |
| Gateway | 本机 dev `:8080`（`ANI_AUTH_MODE=dev`），`REGISTRY_PROVIDER=harbor` |
| Harbor 凭据 | admin（lab 默认密码，未写入 evidence） |
| PG | `DATABASE_URL` 启用，`registry_projects` write-through |

## Live gate 命令

```bash
HOST_IP=$(hostname -I | awk '{print $1}')
export GATEWAY_URL="http://${HOST_IP}:8080/api/v1"
export HARBOR_URL='https://docker.kubercon.local'
export HARBOR_USERNAME=admin
export HARBOR_PASSWORD='<lab-secret>'
export TENANT_ID='00000000-0000-0000-0000-000000000001'
export HARBOR_TLS_INSECURE=true
export CLEANUP=true
./scripts/run_registry_harbor_production_live_gate.sh
```

## Evidence 摘要

见 `development-records/live-evidence/sprint13-registry-harbor-live-evidence.json`：

- `project_create_status`: 201
- `projects_list_status`: 200
- `repositories_list_status`: 200
- `scan_report_status`: 200
- `pull_secret_status`: 201
- `production_shape.status`: passed
- `cleanup_project_status`: 200

## 代码侧补充（同批次）

- `REGISTRY_TLS_INSECURE`：适配 `docker.kubercon.local` 自签 HTTPS
- `registry_projects` upsert：`ON CONFLICT (tenant_id, name)`，避免 local→harbor 切换时 PG 唯一约束冲突
- `scripts/run_registry_harbor_production_live_gate.sh`：默认文档改为本机 `:8080` Gateway

## 边界

- production-shaped passed 只证明 Core `/registry/*` + Harbor adapter + Gateway runtime 在 approved lab Harbor 上可复跑；不标 full platform production ready
- 未覆盖真实镜像推拉、K8s pull secret 注入、Trivy 全量扫描 live 路径
