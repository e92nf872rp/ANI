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
  echo "工作区不干净；请先提交或另建 worktree。" >&2
  git status --short >&2
  exit 1
fi
if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "请先切换到 main；脚本不会替你切换未确认的工作分支。" >&2
  exit 1
fi

BRANCH="feature/${SCOPE}/${SLUG}"
git fetch origin main
git merge --ff-only origin/main
git switch -c "$BRANCH"
git config --local commit.template "$(git rev-parse --show-toplevel)/.gitmessage"
echo "已创建 $BRANCH，开发和其他分支可以并行进行。"

