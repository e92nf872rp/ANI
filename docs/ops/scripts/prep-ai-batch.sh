#!/usr/bin/env bash
set -euo pipefail

SCOPE="${1:-}"
SLUG="${2:-}"
if [[ -z "$SCOPE" || -z "$SLUG" ]]; then
  echo "用法: $0 <scope> <slug>" >&2
  exit 2
fi
case "$SCOPE" in
  core|services|gateway|auth|storage|network|k8s|gpu|observability|rag|frontend|infra|docs|ci) ;;
  *) echo "不支持的 scope: $SCOPE" >&2; exit 2 ;;
esac
if [[ -n "$(git status --porcelain)" ]]; then
  echo "工作区不干净；请使用独立 worktree 或先提交。" >&2
  exit 1
fi
if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "请先切换到 main；脚本不会隐式切换分支。" >&2
  exit 1
fi

git fetch origin main
git merge --ff-only origin/main
BRANCH="ai/${SCOPE}/${SLUG}"
git switch -c "$BRANCH"
git config --local commit.template "$(git rev-parse --show-toplevel)/.gitmessage-ai-batch"
echo "已创建 $BRANCH。请先建立 AI Coding Batch Issue，再按切片创建可合并的 feature PR。"

