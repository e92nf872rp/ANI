#!/usr/bin/env python3
"""Validate documentation entrypoint boundaries.

CLAUDE.md must stay a lightweight rule index. Architecture diagrams must keep
Core/Services boundaries aligned with the code and OpenAPI contract split.
"""

from __future__ import annotations

import re
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
PROJECT_ROOT = REPO_ROOT.parent
REPOSITORY_WIDE_STALE_PATTERNS = (
    "POST /inference-services",
    "POST /knowledge-bases",
    "POST /models/import",
    "GET /knowledge-bases",
    "/api/v1/models",
    "/api/v1/inference-services",
    "/api/v1/knowledge-bases",
    "npx openapi-typescript ../../api/openapi/v1.yaml -o src/api/schema.d.ts",
    "api.GET('/models'",
    "模型工程平台",
    "待补服务层",
    "RKE",
    "Rancher",
)


def read(path: Path) -> str:
    if not path.exists():
        raise SystemExit(f"missing required file: {path}")
    return path.read_text(encoding="utf-8")


def require(text: str, needle: str, path: Path) -> None:
    if needle not in text:
        raise SystemExit(f"{path}: missing required text: {needle}")


def reject(text: str, needle: str, path: Path) -> None:
    if needle in text:
        raise SystemExit(f"{path}: forbidden dynamic-detail text found: {needle}")


def matches_stale_pattern(text: str, pattern: str) -> bool:
    if pattern == "RKE":
        return re.search(r"(?<![A-Z])RKE(?![A-Z])", text) is not None
    return pattern in text


def contains_stale_pattern(text: str, patterns: tuple[str, ...] = REPOSITORY_WIDE_STALE_PATTERNS) -> bool:
    return any(matches_stale_pattern(text, pattern) for pattern in patterns)


def markdown_paths() -> list[Path]:
    ignored_parts = {"node_modules", ".git"}
    paths: list[Path] = []
    for path in PROJECT_ROOT.rglob("*.md"):
        if ignored_parts.intersection(path.parts):
            continue
        paths.append(path)
    return paths


def main() -> None:
    claude_path = PROJECT_ROOT / "CLAUDE.md"
    docs_index_path = PROJECT_ROOT / "ANI-DOCS-INDEX.md"
    plan_path = PROJECT_ROOT / "ANI-06-开发计划.md"
    architecture_path = PROJECT_ROOT / "ANI-05-系统架构设计.md"
    product_definition_path = PROJECT_ROOT / "KuberCloud AI 专有云定义.md"
    deployment_path = PROJECT_ROOT / "ANI-07-部署工程设计.md"
    component_arch_path = PROJECT_ROOT / "ANI-13-开源组件松耦合适配器架构.md"
    coding_spec_path = PROJECT_ROOT / "ANI-11-代码实现规范.md"
    repo_readme_path = REPO_ROOT / "README.md"
    current_sprint_path = REPO_ROOT / "CURRENT-SPRINT.md"
    infra_config_path = REPO_ROOT / "deploy/manifests/m1-infra-a/10-platform-config.yaml"
    infra_readme_path = REPO_ROOT / "deploy/manifests/m1-infra-a/README.md"
    strategic_doc_paths = [
        PROJECT_ROOT / "ANI-00-产品战略与开发哲学.md",
        PROJECT_ROOT / "ANI-01-客户画像与场景分析.md",
        PROJECT_ROOT / "ANI-02-产品功能设计.md",
        PROJECT_ROOT / "ANI-03-产品路线图.md",
        PROJECT_ROOT / "ANI-04-技术栈设计.md",
        PROJECT_ROOT / "ANI-05-系统架构设计.md",
        PROJECT_ROOT / "ANI-07-部署工程设计.md",
        PROJECT_ROOT / "ANI-08-安全架构设计.md",
        PROJECT_ROOT / "ANI-09-数据模型设计.md",
        PROJECT_ROOT / "ANI-10-GPT审查提示词集.md",
        PROJECT_ROOT / "ANI-11-代码实现规范.md",
        PROJECT_ROOT / "ANI-12-版本管理策略.md",
        PROJECT_ROOT / "ANI-13-开源组件松耦合适配器架构.md",
        PROJECT_ROOT / "KuberCloud AI 专有云定义.md",
    ]

    claude = read(claude_path)
    docs_index = read(docs_index_path)
    plan = read(plan_path)
    architecture = read(architecture_path)
    product_definition = read(product_definition_path)
    deployment = read(deployment_path)
    component_arch = read(component_arch_path)
    coding_spec = read(coding_spec_path)
    repo_readme = read(repo_readme_path)
    current_sprint = read(current_sprint_path)
    infra_config = read(infra_config_path)
    infra_readme = read(infra_readme_path)

    claude_lines = len(claude.splitlines())
    if claude_lines > 180:
        raise SystemExit(f"{claude_path}: expected <= 180 lines, got {claude_lines}")

    require(claude, "轻量入口和强制规则索引", claude_path)
    require(claude, "禁止写入单批次完成清单", claude_path)
    require(claude, "动态进度必须写入 `repo/CURRENT-SPRINT.md`", claude_path)
    require(claude, "Karpathy 五条开发原则", claude_path)
    require(claude, "跨层控制面契约", claude_path)
    require(claude, "Core OpenAPI REST API / Core SDK", claude_path)
    require(claude, "直接调用 Core 内部 gRPC service", claude_path)

    reject(claude, "当前 Sprint 4 已完成并需保持可追溯", claude_path)
    reject(claude, "当前本地代码只证明 Sprint 5", claude_path)

    require(docs_index, "只维护稳定规则和入口，不维护批次流水账", docs_index_path)
    require(docs_index, "ANI-05-系统架构设计.md", docs_index_path)
    require(docs_index, "修改文档入口后必须运行 `make validate-doc-entrypoints`", docs_index_path)
    require(docs_index, "Core OpenAPI REST API 与 Core/Services 跨层控制面契约", docs_index_path)
    require(plan, "不维护批次流水账", plan_path)
    require(plan, "ANI-05-系统架构设计.md", plan_path)
    require(plan, "Services 功能与接口定义门禁（2026-06-15 至 2026-06-20）", plan_path)
    require(plan, "旧 Services 逻辑", plan_path)
    require(plan, "前端功能定义", plan_path)
    require(plan, "Services 接口定义", plan_path)
    require(plan, "上游原生 Kubernetes", plan_path)
    require(plan, "GitHub 社区热度", plan_path)
    require(plan, "源码和文档质量", plan_path)
    require(plan, "可替换路径", plan_path)
    require(plan, "Core/Services 的跨层控制面契约", plan_path)
    require(plan, "直接调用 Core 内部 gRPC service", plan_path)
    require(current_sprint, "## 文档入口边界", current_sprint_path)
    require(current_sprint, "make validate-doc-entrypoints", current_sprint_path)

    require(architecture, "ANI Core 和 ANI Services", architecture_path)
    require(architecture, "repo/api/openapi/v1.yaml", architecture_path)
    require(architecture, "repo/api/openapi/services/v1.yaml", architecture_path)
    require(architecture, "repo/sdks/core/", architecture_path)
    require(architecture, "repo/sdks/services/", architecture_path)
    require(architecture, "model-service/`、`repo/services/kb-service", architecture_path)
    require(architecture, "禁止 import Core 内部包", architecture_path)
    require(architecture, "local-profile", architecture_path)
    require(architecture, "real-provider", architecture_path)
    require(architecture, "make validate-real-k8s-profile", architecture_path)
    require(architecture, "Services 重定义门禁", architecture_path)
    require(architecture, "2026-06-15 至 2026-06-20", architecture_path)
    require(architecture, "云服务、人机交互展现和功能编排层", architecture_path)
    require(architecture, "PaaS", architecture_path)
    require(architecture, "原生 Kubernetes", architecture_path)
    require(architecture, "与开源社区同步", architecture_path)
    require(architecture, "GitHub 社区热度", architecture_path)
    require(architecture, "可替换退出路径", architecture_path)
    require(architecture, "Core/Services 的跨层控制面契约是 OpenAPI REST + Core SDK", architecture_path)
    require(architecture, "gRPC 是 Core 内部高效通信机制", architecture_path)
    require(architecture, "REST/SDK 不是只给网页前端使用", architecture_path)
    reject(architecture, "POST /api/v1/models/deploy", architecture_path)
    reject(architecture, "/api/v1/models", architecture_path)
    reject(architecture, "AI 推理层（Python）", architecture_path)

    require(component_arch, "开源组件准入标准", component_arch_path)
    require(component_arch, "GitHub stars", component_arch_path)
    require(component_arch, "源码可读", component_arch_path)
    require(component_arch, "运维运营", component_arch_path)
    require(component_arch, "不得仅因 GitHub stars 高就选型", component_arch_path)
    require(component_arch, "新开源项目", component_arch_path)
    require(component_arch, "替代组件、数据迁移路径和回滚方式", component_arch_path)

    require(coding_spec, "前端 Core API Client / Services API Client 强制拆分", coding_spec_path)
    require(coding_spec, "本文中的 **API Client** 指", coding_spec_path)
    require(coding_spec, "不是用户浏览器、不是 Console 页面本身", coding_spec_path)
    require(coding_spec, "API 调用方", coding_spec_path)
    require(coding_spec, "`/api/v1`", coding_spec_path)
    require(coding_spec, "`/api/v1/svc`", coding_spec_path)
    require(coding_spec, "旧 `model-service`", coding_spec_path)
    require(coding_spec, "REST / SDK / gRPC 术语边界", coding_spec_path)
    require(coding_spec, "gRPC Client", coding_spec_path)
    require(coding_spec, "作为 ANI Services 绕过 Core OpenAPI 的跨层产品接口", coding_spec_path)

    require(product_definition, "原生 Kubernetes", product_definition_path)
    require(product_definition, "IaaS、PaaS、AI 全生命周期", product_definition_path)
    require(product_definition, "三种工作负载形态", product_definition_path)

    require(deployment, "上游原生 Kubernetes", deployment_path)
    require(deployment, "与开源社区同步", deployment_path)

    require(infra_config, 'install.mode.supported: "native-k8s,attach-k8s,karmada"', infra_config_path)
    require(infra_readme, "Native Kubernetes bootstrap automation", infra_readme_path)

    require(repo_readme, "Phase 1 / Sprint 5 收敛中", repo_readme_path)
    require(repo_readme, "model-service/          # ANI Services", repo_readme_path)
    require(repo_readme, "6.15-6.20 后按新定义", repo_readme_path)
    require(repo_readme, "services/v1.yaml 为 Services", repo_readme_path)
    require(repo_readme, "pkg/", repo_readme_path)
    reject(repo_readme, "当前阶段：Phase 1 / Sprint 2", repo_readme_path)
    reject(repo_readme, "SPEC-CORE-ALPHA → M1-INSTANCE-U", repo_readme_path)

    stale_patterns = (
        "当前阶段：Phase 1 / Sprint 2",
        "当前执行阶段是 **Phase 1 / Sprint 2**",
        "当前实现已进入 `Phase 1 / Sprint 2`",
        "SPEC-CORE-ALPHA → M1-INSTANCE-U",
        "POST /api/v1/models",
        "POST /inference-services",
        "POST /knowledge-bases",
        "POST /models/import",
        "GET /knowledge-bases",
        "/api/v1/models",
        "/api/v1/inference-services",
        "/api/v1/knowledge-bases",
        "npx openapi-typescript ../../api/openapi/v1.yaml -o src/api/schema.d.ts",
        "api.GET('/models'",
        "待补服务层",
        "模型工程平台",
        "模型管理平台",
        "当前实现位置是",
        "唯一权威定义",
        "pkg/ports/ 31个接口",
        "pkg/adapters/runtime/ 28个文件",
        "RKE",
        "当前最新稳定版",
        "Core REST API / Core SDK",
        "REST API/SDK",
        "gRPC API 以 Protobuf 为唯一契约来源",
        "唯一的 API 权威来源",
        "路由分发 → 对应 gRPC Service",
        "Core REST API 唯一真实来源",
    )
    for path in strategic_doc_paths:
        content = read(path)
        if path.name not in {"ANI-05-系统架构设计.md", "ANI-06-开发计划.md"}:
            require(content, "ANI-DOCS-INDEX.md", path)
            require(content, "repo/CURRENT-SPRINT.md", path)
        for pattern in stale_patterns:
            if matches_stale_pattern(content, pattern):
                raise SystemExit(f"{path}: stale or ambiguous documentation text found: {pattern}")

    for path in markdown_paths():
        content = read(path)
        for pattern in REPOSITORY_WIDE_STALE_PATTERNS:
            if matches_stale_pattern(content, pattern):
                raise SystemExit(f"{path}: repository-wide stale documentation text found: {pattern}")

    print("document entrypoint boundaries valid")


if __name__ == "__main__":
    main()
