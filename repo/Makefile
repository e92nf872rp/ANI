# ANI Monorepo — 统一构建入口
# 使用方式：make <target>

.PHONY: help deps deps-down deps-status deps-clean \
        tools-proto gen-api gen-console-api gen-proto gen-core-sdk gen-api-docs \
        build build-gateway build-auth-service build-model-service build-task-service build-reconcile-worker build-cli \
        test test-go test-python test-cover \
        validate-infra validate-gpu-preflight validate-gpu-contracts validate-runtime-contracts validate-instance-contracts validate-instance-planner validate-instance-renderer validate-instance-admission validate-instance-audit validate-instance-provider-dry-run validate-instance-provider-apply validate-instance-status-reconcile validate-instance-orchestrator validate-instance-store validate-instance-provider-adapter validate-instance-service validate-instance-lifecycle-ops validate-m1-e2e validate-kubernetes-provider-execution validate-kubernetes-rest-client validate-kubernetes-bootstrap-wiring validate-kubernetes-lifecycle-execution validate-kubernetes-ops-execution validate-m1-real-provider-profile validate-real-k8s-profile validate-vcluster-live-gate validate-vcluster-upgrade-live-gate validate-k8s-node-pool-live-gate validate-kubeovn-network-live-gate validate-kubevirt-vm-live-gate validate-reconcile-ha-live-gate validate-kms-sm4-live-gate validate-secrets-live-gate validate-storage-live-gate validate-gpu-inventory-live-gate validate-object-store-live-gate validate-vector-store-live-gate validate-instance-observability-live-gate validate-sprint13-b-track-production-shape validate-gateway-ratelimit validate-gateway-idempotency validate-adapter-resilience-timeout validate-resilience-faultinjection-live-gate validate-resilience-degradation validate-ha-failover-live-gate validate-readyz-dataplane-live-gate validate-sprint14-resilience-live-gate validate-core-installer validate-core-offline validate-core-cli validate-sprint7-core-regression validate-core-release-hardening validate-core-installer-live validate-core-offline-pack validate-core-doc-consistency validate-sprint8-core-release validate-core-release-evidence validate-sprint9-core-rc validate-sprint9-core-doc-consistency validate-sprint9-rc validate-core-artifact-manifest validate-core-version-policy validate-sprint10-final-readiness validate-sprint10-core-doc-consistency validate-sprint10-release-prep validate-sprint11-storage-disk-plan validate-sprint11-core-real-deployment validate-sprint11-rook-ceph-formal-deployment validate-sprint11-rook-ceph-live-deployment-result validate-sprint11-rook-ceph-vm-storage-smoke validate-sprint11-rook-ceph-reboot-resilience validate-sprint11-safe-completion validate-sprint11-core-doc-consistency validate-sprint11-real-deployment validate-demo-instances validate-core-alpha validate-network-alpha validate-storage-alpha validate-vector-alpha validate-workload-identity validate-core-dev-profile validate-sdk-alpha validate-sdk-beta validate-sdk-mock-smoke validate-sprint3-closure validate-sprint4-closure validate-spec-split validate-core-beta validate-core-api-compatibility validate-mock-a validate-doc-api validate-doc-entrypoints validate-auth-contract validate-auth-dex-smoke validate-auth-dex-production-gate validate-services validate-architecture \
        lint lint-go lint-python lint-ts \
        dev-gateway dev-console dev-core-mock \
        image image-gateway image-auth-service \
        clean

# ── 帮助 ──────────────────────────────────────────────────────────────────────
help:
	@echo "ANI Monorepo 构建命令"
	@echo ""
	@echo "  开发环境:"
	@echo "    make deps           启动本地依赖服务（PG/MinIO/NATS/Redis/Milvus）"
	@echo "    make deps-down      停止本地依赖服务"
	@echo "    make deps-status    查看依赖服务状态"
	@echo "    make deps-clean     停止并清除所有数据（危险）"
	@echo ""
	@echo "  代码生成:"
	@echo "    make tools-proto    安装本地 Protobuf 生成插件"
	@echo "    make gen-api        从 OpenAPI Spec 生成 Gateway Go 代码 + Console TS 类型"
	@echo "    make gen-console-api  仅从 services/v1.yaml 生成 Console TypeScript 类型"
	@echo "    make gen-proto      从 Protobuf 生成 Go gRPC 代码"
	@echo "    make gen-core-sdk   从 Core/Services API 契约生成四语言 SDK Alpha"
	@echo "    make gen-api-docs   从 Core/Services API 契约生成静态 API 文档"
	@echo "    make gen-core-api-compat-baseline  生成 Core API v1 兼容性基线（仅有意更新时使用）"
	@echo ""
	@echo "  构建:"
	@echo "    make build          构建所有 Go 服务和工具"
	@echo "    make build-gateway  仅构建 ANI Gateway"
	@echo "    make build-auth-service  仅构建 Auth Service"
	@echo "    make build-model-service  仅构建 Model Service"
	@echo "    make build-task-service   仅构建 Task Service"
	@echo "    make build-reconcile-worker  仅构建 Reconcile Worker"
	@echo "    make build-cli     仅构建 ANI Core CLI"
	@echo ""
	@echo "  测试:"
	@echo "    make test           运行所有测试（Go + Python）"
	@echo "    make test-cover     生成测试覆盖率报告"
	@echo "    make validate-infra 校验 M1 基础设施 K8s YAML"
	@echo "    make validate-gpu-preflight 校验 M1-INFRA-F GPU 预检契约"
	@echo "    make validate-gpu-contracts 校验 M1-GPU-A 异构 GPU 契约"
	@echo "    make validate-runtime-contracts 校验 M1-RUNTIME-A 实例运行时契约"
	@echo "    make validate-instance-contracts 校验 M1-INSTANCE-A 实例对象/网络/存储契约"
	@echo "    make validate-instance-planner 校验 M1-INSTANCE-B 实例规划器契约"
	@echo "    make validate-instance-renderer 校验 M1-INSTANCE-C provider renderer 契约"
	@echo "    make validate-instance-admission 校验 M1-INSTANCE-D admission guardrail 契约"
	@echo "    make validate-instance-audit 校验 M1-INSTANCE-E audit trail 契约"
	@echo "    make validate-instance-provider-dry-run 校验 M1-INSTANCE-F provider dry-run 契约"
	@echo "    make validate-instance-provider-apply 校验 M1-INSTANCE-G provider apply gate 契约"
	@echo "    make validate-instance-status-reconcile 校验 M1-INSTANCE-H status reconcile 契约"
	@echo "    make validate-instance-orchestrator 校验 M1-INSTANCE-I instance orchestration 契约"
	@echo "    make validate-instance-store 校验 M1-INSTANCE-J instance persistence/query 契约"
	@echo "    make validate-instance-provider-adapter 校验 M1-INSTANCE-K provider adapter 契约"
	@echo "    make validate-instance-service 校验 M1-INSTANCE-L instance service API 契约"
	@echo "    make validate-instance-lifecycle-ops 校验 M1-INSTANCE-M lifecycle/ops API 契约"
	@echo "    make validate-m1-e2e 校验 M1 端到端集成剖面"
	@echo "    make validate-kubernetes-provider-execution 校验 M1-INSTANCE-N Kubernetes provider 执行剖面"
	@echo "    make validate-kubernetes-rest-client 校验 M1-INSTANCE-O Kubernetes REST client"
	@echo "    make validate-kubernetes-bootstrap-wiring 校验 M1-INSTANCE-P Kubernetes provider bootstrap wiring"
	@echo "    make validate-kubernetes-lifecycle-execution 校验 M1-INSTANCE-Q Kubernetes lifecycle execution"
	@echo "    make validate-kubernetes-ops-execution 校验 M1-INSTANCE-R Kubernetes visual ops execution"
	@echo "    make validate-m1-real-provider-profile 校验 M1-E2E-B real provider 回归剖面"
	@echo "    make validate-real-k8s-profile 校验 Sprint 5 REAL-K8S-LAB-A 真实底座门禁"
	@echo "    make validate-vcluster-live-gate 校验 Sprint 5 M1-K8S-LIVE-A vCluster live 验证门禁"
	@echo "    make validate-vcluster-upgrade-live-gate 校验 Sprint 5 M1-K8S-LIVE-C vCluster upgrade live 验证门禁"
	@echo "    make validate-k8s-node-pool-live-gate 校验 Sprint 5 M1-K8S-LIVE-B node pool live 验证门禁"
	@echo "    make validate-kubeovn-network-live-gate 校验 Sprint 5 M1-NETWORK-LIVE-A Kube-OVN network live 验证门禁"
	@echo "    make validate-kubevirt-vm-live-gate 校验 Sprint 5 M1-KUBEVIRT-LIVE-A KubeVirt VM live 验证门禁"
	@echo "    make validate-reconcile-ha-live-gate 校验 Sprint 5 M1-RECONCILE-LIVE-A controller HA live 验证门禁"
	@echo "    make validate-kms-sm4-live-gate 校验 Sprint 5 M1-ENCRYPT-LIVE-A KMS/SM4 live 验证门禁"
	@echo "    make validate-secrets-live-gate 校验 Sprint 5 M1-SECRETS-LIVE-A Secret live 验证门禁"
	@echo "    make validate-storage-live-gate 校验 Sprint 13 S03 storage snapshot/mount-target live gate 契约"
	@echo "    make validate-gpu-inventory-live-gate 校验 Sprint 13 S04 GPU inventory/occupancy live gate 契约"
	@echo "    make validate-object-store-live-gate 校验 Sprint 13 S05 object-store MinIO live gate 契约"
	@echo "    make validate-vector-store-live-gate 校验 Sprint 13 S06 vector-store Milvus live gate 契约"
	@echo "    make validate-instance-observability-live-gate 校验 Sprint 13 S07 instance observability Prometheus live gate 契约"
	@echo "    make validate-sprint13-b-track-production-shape 校验 Sprint 13 S01-S04/S05-S07 B轨生产形态验收标准"
	@echo "    make validate-resilience-faultinjection-live-gate 校验 Sprint 14 R-P1-5 resilience fault-injection local gate"
	@echo "    make validate-resilience-degradation 校验 Sprint 14 R-P1-6 strong/weak dependency degradation local gate"
	@echo "    make validate-ha-failover-live-gate 校验 Sprint 14 R-P2-7 multi-endpoint failover config local gate（未执行真实 primary kill）"
	@echo "    make validate-core-installer 校验 Sprint 7 Core installer profile"
	@echo "    make validate-core-offline 校验 Sprint 7 Core offline package manifest"
	@echo "    make validate-core-cli 校验 Sprint 7/8 Core CLI"
	@echo "    make validate-sprint7-core-regression 校验 Sprint 7 Core regression profile"
	@echo "    make validate-core-release-hardening 校验 Sprint 8 Core release hardening profile"
	@echo "    make validate-core-installer-live 校验 Sprint 8 Core installer live readiness profile"
	@echo "    make validate-core-offline-pack 校验 Sprint 8 Core offline package lock"
	@echo "    make validate-core-doc-consistency 校验 Sprint 8 Core 文档一致性"
	@echo "    make validate-sprint8-core-release 校验 Sprint 8 Core release 聚合门禁"
	@echo "    make validate-core-release-evidence 校验 Sprint 9 Core release evidence manifest"
	@echo "    make validate-sprint9-core-rc 校验 Sprint 9 Core RC readiness profile"
	@echo "    make validate-sprint9-core-doc-consistency 校验 Sprint 9 Core 文档一致性"
	@echo "    make validate-sprint9-rc 校验 Sprint 9 Core RC readiness 聚合门禁"
	@echo "    make validate-core-artifact-manifest 校验 Sprint 10 Core artifact manifest"
	@echo "    make validate-core-version-policy 校验 Sprint 10 Core version policy"
	@echo "    make validate-sprint10-final-readiness 校验 Sprint 10 Core release-prep readiness profile"
	@echo "    make validate-sprint10-core-doc-consistency 校验 Sprint 10 Core 文档一致性"
	@echo "    make validate-sprint10-release-prep 校验 Sprint 10 Core release-prep 聚合门禁"
	@echo "    make validate-sprint11-storage-disk-plan 校验 Sprint 11 Core 真实部署存储盘风险计划"
	@echo "    make validate-sprint11-core-real-deployment 校验 Sprint 11 Core 真实部署验证 profile"
	@echo "    make validate-sprint11-rook-ceph-formal-deployment 校验 Sprint 11 Rook-Ceph 正式部署代码包"
	@echo "    make validate-sprint11-rook-ceph-live-deployment-result 校验 Sprint 11 Rook-Ceph 真实部署结果"
	@echo "    make validate-sprint11-rook-ceph-vm-storage-smoke 校验 Sprint 11 KubeVirt VM RBD 存储 smoke 结果"
	@echo "    make validate-sprint11-rook-ceph-reboot-resilience 校验 Sprint 11 Rook-Ceph 逐节点重启恢复结果"
	@echo "    make validate-sprint11-safe-completion 校验 Sprint 11 Core 安全完成 profile"
	@echo "    make validate-sprint11-core-doc-consistency 校验 Sprint 11 Core 文档一致性"
	@echo "    make validate-sprint11-real-deployment 校验 Sprint 11 Core 真实部署验证聚合门禁"
	@echo "    make validate-demo-instances 校验阶段性实例 Demo API"
	@echo "    make validate-core-alpha 校验 Sprint 2 Core API Alpha 契约"
	@echo "    make validate-network-alpha 校验 Sprint 3 网络资源 Core API 契约"
	@echo "    make validate-storage-alpha 校验 Sprint 3 存储资源 Core API 契约"
	@echo "    make validate-vector-alpha 校验 Sprint 3 向量存储 Core API 契约"
	@echo "    make validate-workload-identity 校验 Sprint 3 Workload Identity 契约"
	@echo "    make validate-core-dev-profile 校验 Sprint 3 Core dev/local profile 边界"
	@echo "    make validate-sdk-alpha 校验 Sprint 3 四语言 SDK Alpha"
	@echo "    make validate-sdk-beta 校验 Sprint 4 SDK Beta 幂等 helper"
	@echo "    make validate-sdk-mock-smoke 校验 Sprint 4 四语言 SDK 调用 Core Mock Server"
	@echo "    make validate-sprint3-closure 校验 Sprint 3 闭环审查门禁"
	@echo "    make validate-sprint4-closure 校验 Sprint 4 关联性闭环门禁"
	@echo "    make validate-spec-split 校验 Sprint 4 Core/Services API 分层"
	@echo "    make validate-core-beta 校验 Sprint 4 Core API Beta 准备矩阵"
	@echo "    make validate-core-api-compatibility 校验 Sprint 4 Core API v1 兼容性基线"
	@echo "    make validate-mock-a 校验 Sprint 4 Core Mock Server 覆盖"
	@echo "    make validate-doc-api 校验 Sprint 4 静态 API 文档"
	@echo "    make validate-doc-entrypoints 校验 CLAUDE.md/文档入口职责边界"
	@echo "    make validate-auth-contract 校验 M2.2 Auth Gateway/API 契约一致性"
	@echo "    make validate-auth-dex-smoke 校验 M2.2 Docker Dex OIDC auth-code flow（需 Docker）"
	@echo "    make validate-services 校验 Services PR boundary/spec/SDK/model/RAG/Console 真实门禁"
	@echo "    make validate-architecture 校验 ports/adapters 组件边界"
	@echo ""
	@echo "  代码检查:"
	@echo "    make lint           运行所有 linter"
	@echo ""
	@echo "  本地开发:"
	@echo "    make dev-gateway    启动 ANI Gateway（热重载）"
	@echo "    make dev-console    启动 Console 前端（Vite HMR）"
	@echo "    make dev-core-mock  启动 Core API Mock Server（http://127.0.0.1:4010/api/v1）"

# ── 依赖服务 ──────────────────────────────────────────────────────────────────
COMPOSE = docker compose -f deploy/docker/docker-compose.yml
GO_CACHE_ENV = GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod
BUF_CACHE_ENV = BUF_CACHE_DIR=$(CURDIR)/.cache/buf
GO_PACKAGES = ./pkg/... ./services/ani-gateway/... ./services/auth-service/... ./services/model-service/... ./services/task-service/... ./services/reconcile-worker/...
TOOLS_BIN = $(CURDIR)/.bin

deps:
	@echo "→ 启动本地依赖服务..."
	$(COMPOSE) up -d
	@echo "→ 等待服务就绪..."
	@$(COMPOSE) exec postgres pg_isready -U ani -t 30 && echo "✅ PostgreSQL 就绪"
	@echo "✅ 所有依赖服务已启动"
	@echo ""
	@echo "  MinIO Console: http://localhost:9001  (ani-admin / ani_dev_password)"
	@echo "  NATS Monitor:  http://localhost:8222"
	@echo "  Milvus:        localhost:19530"

deps-down:
	$(COMPOSE) down

deps-status:
	$(COMPOSE) ps

deps-clean:
	@echo "⚠️  这将删除所有本地开发数据，是否确认？[y/N]" && read ans && [ $${ans:-N} = y ]
	$(COMPOSE) down -v

# ── 代码生成 ──────────────────────────────────────────────────────────────────
tools-proto:
	@echo "→ 安装 Protobuf 生成插件到 .bin..."
	@mkdir -p .bin
	@test -x "$(TOOLS_BIN)/protoc-gen-go" || GOBIN=$(TOOLS_BIN) $(GO_CACHE_ENV) go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.33.0
	@test -x "$(TOOLS_BIN)/protoc-gen-go-grpc" || GOBIN=$(TOOLS_BIN) $(GO_CACHE_ENV) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
	@test -x "$(TOOLS_BIN)/protoc-gen-grpc-gateway" || GOBIN=$(TOOLS_BIN) $(GO_CACHE_ENV) go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.19.1
	@echo "✅ Protobuf 工具已安装"

gen-api:
	@echo "→ 从 OpenAPI Spec 生成 Go 代码..."
	cd services/ani-gateway && \
		go generate ./...
	@echo "→ 从 Services OpenAPI Spec 生成 Console TypeScript 类型..."
	npx openapi-typescript api/openapi/services/v1.yaml -o frontends/console/src/api/schema.d.ts
	@echo "→ 从 Core OpenAPI Spec 生成 Console TypeScript 类型..."
	node frontends/console/scripts/gen-core-schema.mjs
	@echo "✅ API 代码生成完成"

gen-console-api:
	@echo "→ 从 Services OpenAPI Spec 生成 Console TypeScript 类型..."
	npx openapi-typescript api/openapi/services/v1.yaml -o frontends/console/src/api/schema.d.ts
	@echo "→ 从 Core OpenAPI Spec 生成 Console TypeScript 类型..."
	node frontends/console/scripts/gen-core-schema.mjs
	@echo "✅ Console API types generated"

gen-proto: tools-proto
	@echo "→ 从 Protobuf 生成 Go gRPC 代码..."
	cd api/proto && PATH="$(TOOLS_BIN):$$PATH" $(BUF_CACHE_ENV) buf generate --template buf.gen.yaml .
	@echo "✅ Protobuf 代码生成完成"

gen-core-sdk:
	@echo "→ 从 Core/Services API 契约生成四语言 SDK Alpha..."
	python scripts/gen_sdk_alpha.py
	@echo "✅ SDK Alpha 生成完成"

gen-api-docs:
	@echo "→ 从 Core/Services API 契约生成静态 API 文档..."
	python scripts/generate_api_docs.py
	@echo "✅ API docs generated"

gen-core-api-compat-baseline:
	@echo "→ 生成 Core API v1 兼容性基线..."
	python scripts/generate_core_api_compatibility_baseline.py
	@echo "✅ Core API compatibility baseline generated"

# ── 构建 ──────────────────────────────────────────────────────────────────────
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS = -X main.Version=$(VERSION) -X main.BuildTime=$(shell date -u +%Y%m%dT%H%M%SZ)

build: build-gateway build-auth-service build-model-service build-task-service build-reconcile-worker build-cli

build-gateway:
	@echo "→ 构建 ANI Gateway..."
	@mkdir -p bin
	cd services/ani-gateway && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/ani-gateway .
	@echo "✅ bin/ani-gateway"

build-auth-service:
	@echo "→ 构建 Auth Service..."
	@mkdir -p bin
	cd services/auth-service && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/auth-service .
	@echo "✅ bin/auth-service"

build-model-service:
	@echo "→ 构建 Model Service..."
	@mkdir -p bin
	cd services/model-service && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/model-service .
	@echo "✅ bin/model-service"

build-task-service:
	@echo "→ 构建 Task Service..."
	@mkdir -p bin
	cd services/task-service && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/task-service .
	@echo "✅ bin/task-service"

build-reconcile-worker:
	@echo "→ 构建 Reconcile Worker..."
	@mkdir -p bin
	cd services/reconcile-worker && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/reconcile-worker .
	@echo "✅ bin/reconcile-worker"

build-cli:
	@echo "→ 构建 ANI Core CLI..."
	@mkdir -p bin
	cd cli/ani && \
		$(GO_CACHE_ENV) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../../bin/ani .
	@echo "✅ bin/ani"

# ── 测试 ──────────────────────────────────────────────────────────────────────
test: validate-architecture validate-auth-contract test-go test-python

test-go:
	@echo "→ 运行 Go 单元测试..."
	$(GO_CACHE_ENV) go test $(GO_PACKAGES) -v -timeout 120s

test-python:
	@echo "→ 检查 Python 服务语法（RAG）..."
	python -m compileall -q ai/rag-engine

test-cover:
	@echo "→ 生成 Go 测试覆盖率报告..."
	$(GO_CACHE_ENV) go test $(GO_PACKAGES) -coverprofile=coverage.out -covermode=atomic
	$(GO_CACHE_ENV) go tool cover -html=coverage.out -o coverage.html
	@echo "✅ coverage.html"

validate-infra:
	@echo "→ 校验 M1-INFRA-A Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-infra-a
	@echo "→ 校验 M1-INFRA-C Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-infra-c
	@echo "→ 校验 M1-INFRA-D Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-infra-d
	@echo "→ 校验 M1-INFRA-E Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-infra-e
	@echo "→ 校验 M1-INFRA-F Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-infra-f
	$(MAKE) validate-gpu-preflight
	@echo "→ 校验 M1-GPU-A Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-gpu-a
	$(MAKE) validate-gpu-contracts
	@echo "→ 校验 M1-RUNTIME-A Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-runtime-a
	$(MAKE) validate-runtime-contracts
	@echo "→ 校验 M1-INSTANCE-A Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-a
	$(MAKE) validate-instance-contracts
	@echo "→ 校验 M1-INSTANCE-B Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-b
	$(MAKE) validate-instance-planner
	@echo "→ 校验 M1-INSTANCE-C Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-c
	$(MAKE) validate-instance-renderer
	@echo "→ 校验 M1-INSTANCE-D Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-d
	$(MAKE) validate-instance-admission
	@echo "→ 校验 M1-INSTANCE-E Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-e
	$(MAKE) validate-instance-audit
	@echo "→ 校验 M1-INSTANCE-F Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-f
	$(MAKE) validate-instance-provider-dry-run
	@echo "→ 校验 M1-INSTANCE-G Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-g
	$(MAKE) validate-instance-provider-apply
	@echo "→ 校验 M1-INSTANCE-H Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-h
	$(MAKE) validate-instance-status-reconcile
	@echo "→ 校验 M1-INSTANCE-I Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-i
	$(MAKE) validate-instance-orchestrator
	@echo "→ 校验 M1-INSTANCE-J Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-j
	$(MAKE) validate-instance-store
	@echo "→ 校验 M1-INSTANCE-K Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-k
	$(MAKE) validate-instance-provider-adapter
	@echo "→ 校验 M1-INSTANCE-L Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-l
	$(MAKE) validate-instance-service
	@echo "→ 校验 M1-INSTANCE-M Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-m
	$(MAKE) validate-instance-lifecycle-ops
	@echo "→ 校验 M1-E2E Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-e2e
	$(MAKE) validate-m1-e2e
	@echo "→ 校验 M1-INSTANCE-N Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-n
	$(MAKE) validate-kubernetes-provider-execution
	@echo "→ 校验 M1-INSTANCE-O Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-o
	$(MAKE) validate-kubernetes-rest-client
	@echo "→ 校验 M1-INSTANCE-P Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-p
	$(MAKE) validate-kubernetes-bootstrap-wiring
	@echo "→ 校验 M1-INSTANCE-Q Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-q
	$(MAKE) validate-kubernetes-lifecycle-execution
	@echo "→ 校验 M1-INSTANCE-R Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-instance-r
	$(MAKE) validate-kubernetes-ops-execution
	@echo "→ 校验 M1-E2E-B Kubernetes manifests..."
	python scripts/validate_k8s_yaml.py deploy/manifests/m1-e2e-b
	$(MAKE) validate-m1-real-provider-profile
	@echo "→ 校验基础设施 YAML 配置..."
	python scripts/validate_yaml.py deploy/manifests deploy/helm
	@echo "✅ infrastructure manifests valid"

validate-gpu-preflight:
	@echo "→ 校验 M1-INFRA-F GPU preflight contract..."
	python scripts/validate_gpu_preflight_contract.py deploy/manifests/m1-infra-f
	@echo "✅ GPU preflight contract valid"

validate-gpu-contracts:
	@echo "→ 校验 M1-GPU-A heterogeneous GPU contracts..."
	python scripts/validate_gpu_contracts.py deploy/manifests/m1-gpu-a
	@echo "✅ heterogeneous GPU contracts valid"

validate-runtime-contracts:
	@echo "→ 校验 M1-RUNTIME-A workload runtime contracts..."
	python scripts/validate_runtime_contracts.py deploy/manifests/m1-runtime-a
	@echo "✅ workload runtime contracts valid"

validate-instance-contracts:
	@echo "→ 校验 M1-INSTANCE-A instance contracts..."
	python scripts/validate_instance_contracts.py deploy/manifests/m1-instance-a
	@echo "✅ instance contracts valid"

validate-instance-planner:
	@echo "→ 校验 M1-INSTANCE-B instance planner contract..."
	python scripts/validate_instance_planner.py deploy/manifests/m1-instance-b
	@echo "✅ instance planner contract valid"

validate-instance-renderer:
	@echo "→ 校验 M1-INSTANCE-C provider renderer contract..."
	python scripts/validate_instance_renderer.py deploy/manifests/m1-instance-c
	@echo "✅ instance renderer contract valid"

validate-instance-admission:
	@echo "→ 校验 M1-INSTANCE-D admission guardrail contract..."
	python scripts/validate_instance_admission.py deploy/manifests/m1-instance-d
	@echo "✅ instance admission contract valid"

validate-instance-audit:
	@echo "→ 校验 M1-INSTANCE-E audit trail contract..."
	python scripts/validate_instance_audit.py deploy/manifests/m1-instance-e deploy/migrations/20260501_001_init_schema.sql
	@echo "✅ instance audit contract valid"

validate-instance-provider-dry-run:
	@echo "→ 校验 M1-INSTANCE-F provider dry-run contract..."
	python scripts/validate_instance_provider_dry_run.py deploy/manifests/m1-instance-f
	@echo "✅ instance provider dry-run contract valid"

validate-instance-provider-apply:
	@echo "→ 校验 M1-INSTANCE-G provider apply gate contract..."
	python scripts/validate_instance_provider_apply.py deploy/manifests/m1-instance-g
	@echo "✅ instance provider apply gate contract valid"

validate-instance-status-reconcile:
	@echo "→ 校验 M1-INSTANCE-H status reconcile contract..."
	python scripts/validate_instance_status_reconcile.py deploy/manifests/m1-instance-h
	@echo "✅ instance status reconcile contract valid"

validate-instance-orchestrator:
	@echo "→ 校验 M1-INSTANCE-I instance orchestration contract..."
	python scripts/validate_instance_orchestrator.py deploy/manifests/m1-instance-i
	@echo "✅ instance orchestration contract valid"

validate-instance-store:
	@echo "→ 校验 M1-INSTANCE-J instance persistence/query contract..."
	python scripts/validate_instance_store.py deploy/manifests/m1-instance-j deploy/migrations/20260501_001_init_schema.sql
	@echo "✅ instance persistence/query contract valid"

validate-instance-provider-adapter:
	@echo "→ 校验 M1-INSTANCE-K provider adapter contract..."
	python scripts/validate_instance_provider_adapter.py deploy/manifests/m1-instance-k
	@echo "✅ instance provider adapter contract valid"

validate-instance-service:
	@echo "→ 校验 M1-INSTANCE-L instance service contract..."
	python scripts/validate_instance_service.py deploy/manifests/m1-instance-l
	@echo "✅ instance service contract valid"

validate-instance-lifecycle-ops:
	@echo "→ 校验 M1-INSTANCE-M lifecycle/ops contract..."
	python scripts/validate_instance_lifecycle_ops.py deploy/manifests/m1-instance-m
	@echo "✅ instance lifecycle/ops contract valid"

validate-m1-e2e:
	@echo "→ 校验 M1 end-to-end integration profile..."
	python scripts/validate_m1_e2e_profile.py deploy/manifests/m1-e2e
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime -run TestM1E2EProfile -v
	@echo "✅ M1 end-to-end integration profile valid"

validate-kubernetes-provider-execution:
	@echo "→ 校验 M1-INSTANCE-N Kubernetes provider execution profile..."
	python scripts/validate_kubernetes_provider_execution.py deploy/manifests/m1-instance-n
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime -run TestKubernetesProviderExecutionProfile -v
	@echo "✅ Kubernetes provider execution profile valid"

validate-kubernetes-rest-client:
	@echo "→ 校验 M1-INSTANCE-O Kubernetes REST client..."
	python scripts/validate_kubernetes_rest_client.py deploy/manifests/m1-instance-o
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime -run TestKubernetesRESTClient -v
	@echo "✅ Kubernetes REST client valid"

validate-kubernetes-bootstrap-wiring:
	@echo "→ 校验 M1-INSTANCE-P Kubernetes provider bootstrap wiring..."
	python scripts/validate_kubernetes_bootstrap_wiring.py deploy/manifests/m1-instance-p
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/bootstrap -run TestNewCapabilities -v
	@echo "✅ Kubernetes provider bootstrap wiring valid"

validate-kubernetes-lifecycle-execution:
	@echo "→ 校验 M1-INSTANCE-Q Kubernetes lifecycle execution..."
	python scripts/validate_kubernetes_lifecycle_execution.py deploy/manifests/m1-instance-q
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime ./pkg/bootstrap -run 'TestKubernetesLifecycleExecutor|TestLocalInstanceServiceLifecycleUsesProviderExecutor|TestNewCapabilitiesCanWireKubernetesRESTLifecycleProvider|TestNewCapabilitiesRejectsKubernetesRESTLifecycleWithoutHost' -v
	@echo "✅ Kubernetes lifecycle execution valid"

validate-kubernetes-ops-execution:
	@echo "→ 校验 M1-INSTANCE-R Kubernetes visual ops execution..."
	python scripts/validate_kubernetes_ops_execution.py deploy/manifests/m1-instance-r
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime ./pkg/bootstrap -run 'TestKubernetesInstanceOps|TestNewCapabilitiesCanWireKubernetesRESTOpsProvider|TestNewCapabilitiesRejectsKubernetesRESTOpsWithoutHost' -v
	@echo "✅ Kubernetes visual ops execution valid"

validate-m1-real-provider-profile:
	@echo "→ 校验 M1-E2E-B real provider integration regression profile..."
	python scripts/validate_m1_real_provider_profile.py deploy/manifests/m1-e2e-b
	GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(CURDIR)/.cache/gomod go test ./pkg/adapters/runtime -run TestM1RealProviderProfile -v
	@echo "✅ M1 real provider integration regression profile valid"

validate-real-k8s-profile:
	@echo "→ 校验 Sprint 5 REAL-K8S-LAB-A 真实底座门禁..."
	python scripts/validate_real_k8s_profile.py
	python scripts/validate_real_k8s_profile_test.py
	@echo "✅ REAL-K8S-LAB-A real provider lab gate valid"

validate-vcluster-live-gate:
	@echo "→ 校验 Sprint 5 M1-K8S-LIVE-A vCluster live 验证门禁..."
	python scripts/validate_vcluster_live_gate.py
	python scripts/validate_vcluster_live_gate_test.py
	@echo "✅ M1-K8S-LIVE-A vCluster live validation gate valid"

validate-vcluster-upgrade-live-gate:
	@echo "→ 校验 Sprint 5 M1-K8S-LIVE-C vCluster upgrade live 验证门禁..."
	python scripts/validate_vcluster_upgrade_live_gate.py
	python scripts/validate_vcluster_upgrade_live_gate_test.py
	@echo "✅ M1-K8S-LIVE-C vCluster upgrade live validation gate valid"

validate-k8s-node-pool-live-gate:
	@echo "→ 校验 Sprint 5 M1-K8S-LIVE-B node pool live 验证门禁..."
	python scripts/validate_k8s_node_pool_live_gate.py
	python scripts/validate_k8s_node_pool_live_gate_test.py
	@echo "✅ M1-K8S-LIVE-B node pool live validation gate valid"

validate-kubeovn-network-live-gate:
	@echo "→ 校验 Sprint 5 M1-NETWORK-LIVE-A Kube-OVN network live 验证门禁..."
	python scripts/validate_kubeovn_network_live_gate.py
	python scripts/validate_kubeovn_network_live_gate_test.py
	@echo "✅ M1-NETWORK-LIVE-A Kube-OVN network live validation gate valid"

validate-kubevirt-vm-live-gate:
	@echo "→ 校验 Sprint 5 M1-KUBEVIRT-LIVE-A KubeVirt VM live 验证门禁..."
	python scripts/validate_kubevirt_vm_live_gate.py
	python scripts/validate_kubevirt_vm_live_gate_test.py
	@echo "✅ M1-KUBEVIRT-LIVE-A KubeVirt VM live validation gate valid"

validate-reconcile-ha-live-gate:
	@echo "→ 校验 Sprint 5 M1-RECONCILE-LIVE-A controller HA live 验证门禁..."
	python scripts/validate_reconcile_ha_live_gate.py
	python scripts/validate_reconcile_ha_live_gate_test.py
	@echo "✅ M1-RECONCILE-LIVE-A controller HA live validation gate valid"

validate-kms-sm4-live-gate:
	@echo "→ 校验 Sprint 5 M1-ENCRYPT-LIVE-A KMS/SM4 live 验证门禁..."
	python scripts/validate_kms_sm4_live_gate.py
	python scripts/validate_kms_sm4_live_gate_test.py
	@echo "✅ M1-ENCRYPT-LIVE-A KMS/SM4 live validation gate valid"

validate-secrets-live-gate:
	@echo "→ 校验 Sprint 5 M1-SECRETS-LIVE-A Secret live 验证门禁..."
	python scripts/validate_secrets_live_gate.py
	python scripts/validate_secrets_live_gate_test.py
	@echo "✅ M1-SECRETS-LIVE-A Secret live validation gate valid"

validate-storage-live-gate:
	@echo "→ 校验 Sprint 13 S03 storage snapshot/mount-target live gate 契约..."
	python scripts/validate_storage_live_gate.py
	python scripts/validate_storage_live_gate_test.py
	@echo "✅ SPRINT13-STORAGE-ROOK-CEPH-A storage live gate contract valid"

validate-gpu-inventory-live-gate:
	@echo "→ 校验 Sprint 13 S04 GPU inventory/occupancy live gate 契约..."
	python scripts/validate_gpu_inventory_live_gate.py
	python scripts/validate_gpu_inventory_live_gate_test.py
	@echo "✅ SPRINT13-GPU-INVENTORY-DCGM-A GPU inventory live gate contract valid"

validate-object-store-live-gate:
	@echo "→ 校验 Sprint 13 S05 object-store MinIO live gate 契约..."
	python scripts/validate_object_store_live_gate.py
	python scripts/validate_object_store_live_gate_test.py
	@echo "✅ SPRINT13-OBJECTSTORE-MINIO-A object-store live gate contract valid"

validate-vector-store-live-gate:
	@echo "→ 校验 Sprint 13 S06 vector-store Milvus live gate 契约..."
	python scripts/validate_vector_store_live_gate.py
	python scripts/validate_vector_store_live_gate_test.py
	@echo "✅ SPRINT13-VECTOR-MILVUS-A vector-store live gate contract valid"

validate-instance-observability-live-gate:
	@echo "→ 校验 Sprint 13 S07 instance observability Prometheus live gate 契约..."
	python scripts/validate_instance_observability_live_gate.py
	python scripts/validate_instance_observability_live_gate_test.py
	@echo "✅ SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A instance observability live gate contract valid"

validate-sprint13-b-track-production-shape:
	@echo "→ 校验 Sprint 13 S01-S04/S05-S07 B轨生产形态验收标准..."
	python scripts/validate_sprint13_b_track_production_shape.py
	python scripts/validate_sprint13_b_track_production_shape_test.py
	@echo "✅ Sprint 13 production-shaped acceptance standard valid"

validate-gateway-ratelimit:
	@echo "→ 校验 Sprint 14 R-P0-1 Gateway rate-limit gate..."
	$(GO_CACHE_ENV) go test ./services/ani-gateway/internal/middleware -run RateLimit -v
	@echo "✅ Gateway rate-limit gate valid"

validate-gateway-idempotency:
	@echo "→ 校验 Sprint 14 R-P0-2 Gateway idempotency replay gate..."
	$(GO_CACHE_ENV) go test ./services/ani-gateway/internal/middleware -run Idempotent -v
	@echo "✅ Gateway idempotency replay gate valid"

validate-adapter-resilience-timeout:
	@echo "→ 校验 Sprint 14 R-P0-3 adapter per-call timeout gate..."
	$(GO_CACHE_ENV) go test ./pkg/adapters/resilience ./pkg/adapters/runtime ./pkg/adapters/objectstore ./pkg/adapters/vectorstore -run 'TestDoEnforcesTimeout|TestKubernetesRESTClientEnforcesRequestTimeout|TestMinIOObjectStoreEnforcesRequestTimeout|TestMilvusVectorStoreEnforcesRequestTimeout' -v
	@echo "✅ Adapter per-call timeout gate valid"

validate-resilience-faultinjection-live-gate:
	@echo "→ 校验 Sprint 14 R-P1-5 resilience fault-injection local gate（未执行真实后端 kill / network partition）..."
	$(GO_CACHE_ENV) go test ./pkg/adapters/resilience ./pkg/adapters/runtime -run 'TestRetryableClassification|TestDoRetriesTransientThenSucceeds|TestBreakerOpensAfterSustainedFailures|TestKubernetesRESTErrorClassifiesRetryable|TestKubernetesRESTClientHealthUsesRetryPolicy|TestKubernetesRESTClientApplyDoesNotRetryWrites' -v
	@echo "✅ Resilience fault-injection local gate valid（not production ready）"

validate-resilience-degradation:
	@echo "→ 校验 Sprint 14 R-P1-6 resilience degradation local gate..."
	$(GO_CACHE_ENV) go test ./pkg/adapters/resilience ./pkg/bootstrap -run 'TestDependencyModeFor|TestRunProbeChecksFailsOnStrongDependencyFailure|TestWeakDependencyDownYieldsDegradedNotFail' -v
	@echo "✅ Resilience degradation local gate valid（not production ready）"

validate-ha-failover-live-gate:
	@echo "→ 校验 Sprint 14 R-P2-7 HA failover config local gate（未执行真实 primary kill / topology failover）..."
	$(GO_CACHE_ENV) go test ./pkg/bootstrap ./pkg/adapters/objectstore ./pkg/adapters/vectorstore ./services/ani-gateway -run 'TestRedisFailoverConfigParsesSentinel|TestRedisClusterConfigParsesAddrs|TestConfigEnvironmentOverridesRedisFailover|TestNewCapabilitiesCanWireMinIOEndpointList|TestNewCapabilitiesCanWireMilvusEndpointList|TestMinIOAcceptsEndpointList|TestMinIOHealthFailsOverEndpointList|TestMilvusAcceptsEndpointList|TestMilvusHealthFailsOverEndpointList|TestGatewayRedisConfigFromEnvParsesSentinel|TestGatewayStorageConfigFromEnvIncludesObjectStoreProvider|TestGatewayVectorStoreConfigFromEnvIncludesMilvusProvider' -v
	@echo "✅ HA failover config local gate valid（not production failover ready）"

validate-readyz-dataplane-live-gate:
	@echo "→ 校验 Sprint 14 R-P0-4 data-plane readyz local gate（未执行真实后端 kill）..."
	$(GO_CACHE_ENV) go test ./pkg/bootstrap ./pkg/adapters/objectstore ./pkg/adapters/vectorstore ./pkg/adapters/runtime -run 'TestDependencyProbeChecksReports(ObjectStore|VectorStore|KubernetesAPI)Unavailable|TestDependencyProbeChecksIgnoreNotConfiguredDataPlane|TestMinIOObjectStoreHealthUsesSignedRootRequest|TestMilvusVectorStoreHealthListsCollections|TestKubernetesRESTClientHealthCallsVersion|TestLocalK8sClusterServiceHealth' -v
	@echo "✅ Data-plane readyz local gate valid（not production ready）"

validate-sprint14-resilience-live-gate:
	@echo "→ 校验 Sprint14 resilience live gate contract（真实执行需显式 --live）..."
	python scripts/validate_sprint14_resilience_live_gate.py
	python scripts/validate_sprint14_resilience_live_gate_test.py
	@echo "✅ Sprint14 resilience live gate contract valid"

validate-core-installer:
	@echo "→ 校验 Sprint 7 CORE-INSTALLER-A Core installer profile..."
	python scripts/validate_core_installer.py
	python scripts/validate_core_installer_test.py
	@echo "✅ Sprint 7 Core installer profile valid"

validate-core-offline:
	@echo "→ 校验 Sprint 7 CORE-OFFLINE-A Core offline package manifest..."
	python scripts/validate_core_offline.py
	python scripts/validate_core_offline_test.py
	@echo "✅ Sprint 7 Core offline package manifest valid"

validate-core-cli:
	@echo "→ 校验 Sprint 7/8 Core CLI..."
	$(GO_CACHE_ENV) go test ./cli/ani -v
	@echo "✅ Sprint 7/8 Core CLI valid"

validate-sprint7-core-regression:
	@echo "→ 校验 Sprint 7 CORE-REGRESSION-A Core regression profile..."
	python scripts/validate_sprint7_core_regression.py
	python scripts/validate_sprint7_core_regression_test.py
	@echo "✅ Sprint 7 Core regression profile valid"

validate-core-release-hardening:
	@echo "→ 校验 Sprint 8 CORE-HARDEN-A Core release hardening profile..."
	python scripts/validate_core_release_hardening.py
	python scripts/validate_core_release_hardening_test.py
	@echo "✅ Sprint 8 Core release hardening profile valid"

validate-core-installer-live:
	@echo "→ 校验 Sprint 8 CORE-INSTALLER-LIVE-A Core installer live readiness profile..."
	python scripts/validate_core_installer_live.py
	python scripts/validate_core_installer_live_test.py
	@echo "✅ Sprint 8 Core installer live readiness profile valid"

validate-core-offline-pack:
	@echo "→ 校验 Sprint 8 CORE-OFFLINE-PACK-A Core offline package lock..."
	python scripts/validate_core_offline_pack.py
	python scripts/validate_core_offline_pack_test.py
	@echo "✅ Sprint 8 Core offline package lock valid"

validate-core-doc-consistency:
	@echo "→ 校验 Sprint 8 CORE-DOC-CONSISTENCY-A Core 文档一致性..."
	python scripts/validate_core_doc_consistency.py
	python scripts/validate_core_doc_consistency_test.py
	@echo "✅ Sprint 8 Core documentation consistency valid"

validate-sprint8-core-release: validate-core-release-hardening validate-core-installer-live validate-core-offline-pack validate-core-cli validate-core-doc-consistency
	@echo "✅ Sprint 8 Core release gates valid"

validate-core-release-evidence:
	@echo "→ 校验 Sprint 9 CORE-RELEASE-EVIDENCE-A Core release evidence manifest..."
	python scripts/validate_core_release_evidence.py
	python scripts/validate_core_release_evidence_test.py
	@echo "✅ Sprint 9 Core release evidence manifest valid"

validate-sprint9-core-rc:
	@echo "→ 校验 Sprint 9 CORE-RC-GATE-A Core RC readiness profile..."
	python scripts/validate_sprint9_core_rc.py
	python scripts/validate_sprint9_core_rc_test.py
	@echo "✅ Sprint 9 Core RC readiness profile valid"

validate-sprint9-core-doc-consistency:
	@echo "→ 校验 Sprint 9 CORE-RC-DOC-CONSISTENCY-A Core 文档一致性..."
	python scripts/validate_sprint9_core_doc_consistency.py
	python scripts/validate_sprint9_core_doc_consistency_test.py
	@echo "✅ Sprint 9 Core documentation consistency valid"

validate-sprint9-rc: validate-sprint8-core-release validate-core-release-evidence validate-sprint9-core-rc validate-core-offline-pack validate-core-cli validate-sprint9-core-doc-consistency
	@echo "✅ Sprint 9 Core RC readiness gates valid"

validate-core-artifact-manifest:
	@echo "→ 校验 Sprint 10 CORE-ARTIFACT-MANIFEST-A Core artifact manifest..."
	python scripts/validate_core_artifact_manifest.py
	python scripts/validate_core_artifact_manifest_test.py
	@echo "✅ Sprint 10 Core artifact manifest valid"

validate-core-version-policy:
	@echo "→ 校验 Sprint 10 CORE-VERSION-POLICY-A Core version policy..."
	python scripts/validate_core_version_policy.py
	python scripts/validate_core_version_policy_test.py
	@echo "✅ Sprint 10 Core version policy valid"

validate-sprint10-final-readiness:
	@echo "→ 校验 Sprint 10 CORE-FINAL-READINESS-A Core release-prep readiness profile..."
	python scripts/validate_sprint10_final_readiness.py
	python scripts/validate_sprint10_final_readiness_test.py
	@echo "✅ Sprint 10 Core release-prep readiness profile valid"

validate-sprint10-core-doc-consistency:
	@echo "→ 校验 Sprint 10 CORE-FINAL-DOC-CONSISTENCY-A Core 文档一致性..."
	python scripts/validate_sprint10_core_doc_consistency.py
	python scripts/validate_sprint10_core_doc_consistency_test.py
	@echo "✅ Sprint 10 Core documentation consistency valid"

validate-sprint10-release-prep: validate-sprint9-rc validate-core-artifact-manifest validate-core-version-policy validate-sprint10-final-readiness validate-core-cli validate-sprint10-core-doc-consistency
	@echo "✅ Sprint 10 Core release-prep gates valid"

validate-sprint11-storage-disk-plan:
	@echo "→ 校验 Sprint 11 CORE-STORAGE-DISK-RISK-A Core 存储盘风险计划..."
	python scripts/validate_sprint11_storage_disk_plan.py
	python scripts/validate_sprint11_storage_disk_plan_test.py
	@echo "✅ Sprint 11 Core storage disk risk plan valid"

validate-sprint11-core-real-deployment:
	@echo "→ 校验 Sprint 11 CORE-REAL-DEPLOY-A Core 真实部署验证 profile..."
	python scripts/validate_sprint11_core_real_deployment.py
	python scripts/validate_sprint11_core_real_deployment_test.py
	@echo "✅ Sprint 11 Core real deployment validation profile valid"

validate-sprint11-rook-ceph-formal-deployment:
	@echo "→ 校验 Sprint 11 CORE-ROOK-CEPH-FORMAL-DEPLOYMENT-A Rook-Ceph 正式部署代码包..."
	python scripts/validate_sprint11_rook_ceph_formal_deployment.py
	python scripts/validate_sprint11_rook_ceph_formal_deployment_test.py
	python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-rook-ceph-formal-deployment.yaml
	@echo "✅ Sprint 11 Rook-Ceph formal deployment bundle valid"

validate-sprint11-rook-ceph-live-deployment-result:
	@echo "→ 校验 Sprint 11 CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A Rook-Ceph 真实部署结果..."
	python scripts/validate_sprint11_rook_ceph_live_deployment_result.py
	python scripts/validate_sprint11_rook_ceph_live_deployment_result_test.py
	python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-rook-ceph-live-deployment-result.yaml
	@echo "✅ Sprint 11 Rook-Ceph live deployment result valid"

validate-sprint11-rook-ceph-vm-storage-smoke:
	@echo "→ 校验 Sprint 11 CORE-ROOK-CEPH-VM-STORAGE-SMOKE-A KubeVirt VM RBD 存储 smoke 结果..."
	python scripts/validate_sprint11_rook_ceph_vm_storage_smoke.py
	python scripts/validate_sprint11_rook_ceph_vm_storage_smoke_test.py
	python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-rook-ceph-vm-storage-smoke-result.yaml
	@echo "✅ Sprint 11 KubeVirt VM RBD storage smoke result valid"

validate-sprint11-rook-ceph-reboot-resilience:
	@echo "→ 校验 Sprint 11 CORE-ROOK-CEPH-REBOOT-RESILIENCE-A 逐节点重启恢复结果..."
	python scripts/validate_sprint11_rook_ceph_reboot_resilience.py
	python scripts/validate_sprint11_rook_ceph_reboot_resilience_test.py
	python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-rook-ceph-reboot-resilience-result.yaml
	@echo "✅ Sprint 11 Rook-Ceph reboot resilience result valid"

validate-sprint11-safe-completion:
	@echo "→ 校验 Sprint 11 CORE-SAFE-COMPLETION-A Core 安全完成 profile..."
	python scripts/validate_sprint11_safe_completion.py
	python scripts/validate_sprint11_safe_completion_test.py
	python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-core-safe-completion.yaml
	@echo "✅ Sprint 11 Core safe completion profile valid"

validate-sprint11-core-doc-consistency:
	@echo "→ 校验 Sprint 11 CORE-REAL-DEPLOY-DOC-CONSISTENCY-A Core 文档一致性..."
	python scripts/validate_sprint11_core_doc_consistency.py
	python scripts/validate_sprint11_core_doc_consistency_test.py
	@echo "✅ Sprint 11 Core real deployment documentation consistency valid"

validate-sprint11-real-deployment: validate-sprint10-release-prep validate-sprint11-storage-disk-plan validate-sprint11-core-real-deployment validate-sprint11-rook-ceph-formal-deployment validate-sprint11-rook-ceph-live-deployment-result validate-sprint11-rook-ceph-vm-storage-smoke validate-sprint11-rook-ceph-reboot-resilience validate-sprint11-safe-completion validate-real-k8s-profile validate-sprint11-core-doc-consistency
	@echo "✅ Sprint 11 Core real deployment validation gates valid"

validate-demo-instances:
	@echo "→ 校验阶段性 VM/container/GPU container Demo API..."
	$(GO_CACHE_ENV) go test ./services/ani-gateway/internal/router -run TestDemoInstance -v
	@echo "✅ staged instance demo API valid"

validate-core-alpha:
	@echo "→ 校验 Sprint 2 Core API Alpha 契约..."
	python scripts/validate_core_alpha_contract.py
	@echo "✅ Core API Alpha contract valid"

validate-network-alpha:
	@echo "→ 校验 Sprint 3 网络资源 Core API 契约..."
	python scripts/validate_network_alpha_contract.py
	$(GO_CACHE_ENV) go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestLocalNetworkService|TestMetadataNetworkStore|TestLocalNetworkStatusReconciler|TestKubeOVNNetworkRenderer|TestKubeOVNNetworkProvider|TestKubernetesRESTClientSupports.*Network|TestKubernetesRESTClientObserves.*Network|TestKubernetesRESTClientMapsNetwork|TestNetworkAPI' -v
	@echo "✅ network Core API contract valid"

validate-storage-alpha:
	@echo "→ 校验 Sprint 3 存储资源 Core API 契约..."
	python scripts/validate_storage_alpha_contract.py
	$(GO_CACHE_ENV) go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestLocalStorageService|TestMetadataStorageStore|TestLocalStorageStatusReconciler|TestKubernetesStorageRenderer|TestKubernetesStorageProvider|TestKubernetesRESTClientObservesStorage|TestKubernetesRESTClientMapsStorage|TestStorageAPI' -v
	@echo "✅ storage Core API contract valid"

validate-vector-alpha:
	@echo "→ 校验 Sprint 3 向量存储 Core API 契约..."
	python scripts/validate_vector_alpha_contract.py
	$(GO_CACHE_ENV) go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestLocalVectorStoreService|TestVectorStoreAPI' -v
	@echo "✅ vector store Core API contract valid"

validate-workload-identity:
	@echo "→ 校验 Sprint 3 Workload Identity 契约..."
	python scripts/validate_workload_identity_contract.py
	$(GO_CACHE_ENV) go test ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/router -run 'TestLocalInstanceService.*WorkloadIdentity|TestKubernetesDryRunRendererInjectsWorkloadIdentity|TestNewCapabilitiesDefaultsToLocalProviderAdapters|TestDemoInstance' -v
	@echo "✅ workload identity contract valid"

validate-core-dev-profile:
	@echo "→ 校验 Sprint 3 Core dev/local profile 边界..."
	python scripts/validate_core_dev_profile_contract.py
	$(GO_CACHE_ENV) go test ./services/ani-gateway/internal/router -run 'TestDemoInstance|TestNetworkAPI|TestStorageAPI|TestVectorStoreAPI' -v
	@echo "✅ Core dev/local profile contract valid"

validate-sdk-alpha:
	@echo "→ 校验 Sprint 3 四语言 SDK Alpha..."
	python scripts/validate_sdk_alpha.py
	@echo "✅ SDK Alpha valid"

validate-sdk-beta:
	@echo "→ 校验 Sprint 4 SDK Beta helper..."
	python scripts/validate_sdk_beta_test.py
	python scripts/validate_sdk_beta.py
	$(MAKE) validate-sdk-alpha
	@echo "✅ SDK Beta helpers valid"

validate-sdk-mock-smoke:
	@echo "→ 校验 Sprint 4 SDK-MOCK-SMOKE-A/B/C/D 四语言 SDK 调用 Core Mock Server..."
	python scripts/validate_sdk_mock_smoke.py
	@echo "✅ SDK mock smoke valid"

validate-sprint3-closure:
	@echo "→ 校验 Sprint 3 闭环审查门禁..."
	python scripts/validate_sprint3_closure.py
	$(MAKE) validate-network-alpha
	$(MAKE) validate-storage-alpha
	$(MAKE) validate-vector-alpha
	$(MAKE) validate-workload-identity
	$(MAKE) validate-sdk-alpha
	$(MAKE) validate-core-dev-profile
	@echo "✅ Sprint 3 closure valid"

validate-sprint4-closure:
	@echo "→ 校验 Sprint 4 关联性闭环门禁..."
	python scripts/validate_sprint4_closure.py
	$(MAKE) validate-spec-split
	$(MAKE) validate-core-beta
	$(MAKE) validate-core-api-compatibility
	$(MAKE) validate-sdk-beta
	$(MAKE) validate-mock-a
	$(MAKE) validate-doc-api
	$(MAKE) validate-sdk-mock-smoke
	@echo "✅ Sprint 4 closure valid"

validate-spec-split:
	@echo "→ 校验 Sprint 4 SPEC-SPLIT-A Core/Services API 分层..."
	python scripts/validate_spec_split_contract.py
	$(GO_CACHE_ENV) go test ./services/ani-gateway/internal/middleware ./services/ani-gateway/internal/router -run 'TestInferPermission|TestAuthPublicPaths|TestAuthProtectedPaths' -v
	@echo "✅ SPEC-SPLIT-A contract valid"

validate-core-beta:
	@echo "→ 校验 Sprint 4 SPEC-CORE-BETA Core API Beta 准备矩阵..."
	python scripts/validate_core_beta_contract.py
	$(GO_CACHE_ENV) go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestLocalNetworkService|TestLocalStorageService|TestLocalVectorStoreService|TestNetworkAPI|TestStorageAPI|TestVectorStoreAPI' -v
	@echo "✅ SPEC-CORE-BETA contract valid"

validate-core-api-compatibility:
	@echo "→ 校验 Sprint 4 SPEC-COMPAT-A Core API v1 兼容性基线..."
	python scripts/validate_core_api_compatibility.py
	@echo "✅ Core API v1 compatibility valid"

validate-mock-a:
	@echo "→ 校验 Sprint 4 MOCK-A Core Mock Server 覆盖..."
	python scripts/validate_mock_server_contract.py
	@echo "✅ MOCK-A Core Mock Server contract valid"

validate-doc-api:
	@echo "→ 校验 Sprint 4 DOC-API-A 静态 API 文档..."
	python scripts/validate_api_docs_contract.py
	@echo "✅ DOC-API-A static API docs valid"

validate-doc-entrypoints:
	@echo "→ 校验文档入口职责边界..."
	python scripts/validate_doc_entrypoints.py
	python scripts/validate_doc_entrypoints_test.py
	@echo "✅ document entrypoints valid"

validate-auth-contract:
	@echo "→ 校验 M2.2 Auth Gateway/API 契约..."
	python scripts/validate_auth_gateway_contract.py
	@echo "✅ Auth Gateway/API contract valid"

validate-auth-dex-smoke:
	@echo "→ 启动 Dex 并校验 OIDC auth-code flow..."
	$(COMPOSE) --profile auth up -d dex
	python scripts/smoke_auth_dex.py
	@echo "✅ Dex OIDC smoke passed"

validate-auth-dex-production-gate:
	@echo "→ 校验 Sprint 13 Auth/Dex production-shaped gate..."
	python scripts/validate_auth_dex_production_gate.py
	python scripts/validate_auth_dex_production_gate_test.py
	@echo "✅ Sprint 13 Auth/Dex production-shaped gate valid"

validate-services:
	@echo "→ 校验 Services boundary baseline..."
	python scripts/validate_services_boundary.py --root .
	python scripts/validate_yaml.py api/openapi/services/v1.yaml
	$(MAKE) validate-spec-split
	$(MAKE) validate-sdk-beta
	@echo "→ 运行 Services model-service Go tests..."
	$(GO_CACHE_ENV) go test ./services/model-service/... -v
	@echo "→ 检查 RAG Python compile/test..."
	python -m compileall -q ai/rag-engine
	@if [ -n "$$(find ai/rag-engine -type f \( -name 'test_*.py' -o -name '*_test.py' \) -print -quit)" ]; then \
		python -m pytest ai/rag-engine -q; \
	else \
		echo "ℹ️  no RAG pytest files found under ai/rag-engine; compile check completed"; \
	fi
	@echo "→ 安装 Console npm dependencies from frontends/console/package-lock.json..."
	npm --prefix frontends/console ci
	@echo "→ 从 Services OpenAPI source 重新生成 Console API schema..."
	npm --prefix frontends/console run gen-api
	git diff --exit-code -- frontends/console/src/api/schema.d.ts frontends/console/src/api/core-schema.d.ts
	$(MAKE) validate-architecture
	@echo "✅ Services PR gate valid"

validate-architecture:
	@echo "→ 校验开源组件 ports/adapters 边界..."
	python scripts/validate_component_imports.py --root .
	@echo "✅ architecture guardrails valid"

# ── 代码检查 ──────────────────────────────────────────────────────────────────
lint: lint-go lint-python lint-ts

lint-go:
	@echo "→ Go 静态分析..."
	golangci-lint run ./...
	gosec -severity high ./services/... ./operators/...

lint-python:
	@echo "→ Python 代码检查..."
	cd ai && ruff check . && mypy .

lint-ts:
	@echo "→ TypeScript 代码检查..."
	npm --prefix frontends/console run lint

# ── 本地开发（热重载）────────────────────────────────────────────────────────
dev-gateway:
	@echo "→ 启动 ANI Gateway（air 热重载）..."
	cd services/ani-gateway && air

dev-console:
	@echo "→ 启动 Console 前端（Vite HMR）..."
	npm --prefix frontends/console run dev

dev-core-mock:
	@echo "→ 启动 Core API Mock Server..."
	python scripts/serve_core_mock.py --host 127.0.0.1 --port 4010

# ── 镜像构建 ──────────────────────────────────────────────────────────────────
REGISTRY ?= harbor.ani.internal/ani

image: image-gateway image-auth-service

image-gateway:
	docker build -t $(REGISTRY)/ani-gateway:$(VERSION) \
		-f services/ani-gateway/Dockerfile \
		.

image-auth-service:
	docker build -t $(REGISTRY)/ani-auth-service:$(VERSION) \
		-f services/auth-service/Dockerfile \
		.

# ── 清理 ──────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/
	find . -name '*.test' -delete
	find . -name 'coverage.out' -delete
