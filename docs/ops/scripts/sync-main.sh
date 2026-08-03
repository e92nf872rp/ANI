#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-main}"
if [[ -n "$(git status --porcelain)" ]]; then
  echo "工作区不干净；同步前不会覆盖未提交修改。" >&2
  git status --short >&2
  exit 1
fi

git fetch origin main
case "$MODE" in
  main)
    git switch main
    git merge --ff-only origin/main
    echo "本地 main 已同步到 $(git rev-parse --short origin/main)。"
    ;;
  --rebase)
    CURRENT="$(git branch --show-current)"
    if [[ -z "$CURRENT" || "$CURRENT" == "main" ]]; then
      echo "--rebase 只能在 feature/fix/docs/ai/codex 分支使用。" >&2
      exit 2
    fi
    git rebase origin/main
    echo "$CURRENT 已 rebase 到 origin/main；请重新运行本地门禁。"
    ;;
  *)
    echo "用法: $0 [main|--rebase]" >&2
    exit 2
    ;;
esac

