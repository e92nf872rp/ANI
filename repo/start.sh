#!/usr/bin/env bash
# ANI Monorepo 本地启动脚本
#
# 用途: 在没有 make / docker 的环境也能直接启动 ANI 开发栈。
# 默认行为(无参数): 初始化 .env + 启动 Core Mock + Console 前端,进入交互模式,
#                   同时实时输出所有服务的日志到当前窗口。
# 交互模式下窗口保持打开,输入 quit/exit 或按 Ctrl+C 可停止全部服务并退出。
#
# 常用命令(直接执行):
#   ./start.sh             # 默认: 启动 mock + console,实时日志 + 交互模式
#   ./start.sh setup        # 仅做初始化(.env + 构建 Go 二进制)
#   ./start.sh build       # 仅构建 Go 二进制
#   ./start.sh bg          # 后台启动 mock + console,脚本立即退出(服务保留)
#   ./start.sh stop        # 停止所有由本脚本启动的后台进程
#   ./start.sh status      # 查看后台进程状态
#   ./start.sh logs <name> # 跟踪某个后台进程日志(mock|console|gateway)
#
# 交互模式内置命令:
#   status   查看服务状态
#   stop     停止全部服务(不退出)
#   start    重新启动全部服务
#   quit     停止全部服务并退出
#   exit     同 quit
#   help     显示帮助
#   Ctrl+C   同 quit(快捷键)
#
# 说明: 本脚本不替代 Makefile,只是补齐 Windows / 无 make / 无 docker 环境的启动路径。
# 真正的依赖服务(PG/MinIO/NATS/Redis/Milvus)仍需通过 `make deps`(docker compose)拉起。
# 如需运行依赖真实后端的 ani-gateway/auth-service 等,请先准备 .env 并启动 docker compose。

set -euo pipefail

# ── 路径与目录 ────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

RUN_DIR="$SCRIPT_DIR/.run"
LOG_DIR="$SCRIPT_DIR/.logs"
mkdir -p "$RUN_DIR" "$LOG_DIR"

ENV_FILE="$SCRIPT_DIR/.env"
ENV_EXAMPLE="$SCRIPT_DIR/.env.example"

# 后台 tail 的 PID,用于退出时清理
TAIL_PID=""

# 终端是否支持 OSC 8 超链接(现代终端如 Windows Terminal / iTerm2 / GNOME Terminal)
# 检测优先级:
#   1. FORCE_OSC8=1 强制启用(用于调试或已知终端支持但检测不准时)
#   2. FORCE_OSC8=0 强制关闭
#   3. 默认:TTY + TERM_PROGRAM 非空且不是 "vscode"(VSCode 终端对 OSC 8 支持不稳定)
if [[ "${FORCE_OSC8:-}" == "1" ]]; then
  HAS_OSC8=1
elif [[ "${FORCE_OSC8:-}" == "0" ]]; then
  HAS_OSC8=0
elif [[ -t 1 && -n "${TERM_PROGRAM:-}" && "${TERM_PROGRAM:-}" != "vscode" ]]; then
  HAS_OSC8=1
else
  HAS_OSC8=0
fi

# ── 颜色(可选,无 tty 时自动关闭) ─────────────────────────────────────────────
if [[ -t 1 ]]; then
  C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_BLUE=$'\033[34m'; C_CYAN=$'\033[36m'; C_RESET=$'\033[0m'
else
  C_GREEN=""; C_YELLOW=""; C_RED=""; C_BLUE=""; C_CYAN=""; C_RESET=""
fi

# 生成可点击超链接。用法: mklink <url> <text>
# 支持的终端会输出 OSC 8 序列,不支持时退化为纯文本 URL。
mklink() {
  local url="$1" text="${2:-$1}"
  if (( HAS_OSC8 )); then
    # OSC 8: \033]8;;URL\033\\TEXT\033]8;;\033\\
    printf '\033]8;;%s\033\\%s\033]8;;\033\\' "$url" "$text"
  else
    printf '%s' "$url"
  fi
}

log()   { printf '%s[%s]%s %s\n' "$C_GREEN"  "OK"    "$C_RESET" "$*"; }
warn()  { printf '%s[%s]%s %s\n' "$C_YELLOW" "WARN"  "$C_RESET" "$*" >&2; }
err()   { printf '%s[%s]%s %s\n' "$C_RED"    "ERR"   "$C_RESET" "$*" >&2; }
info()  { printf '%s[%s]%s %s\n' "$C_BLUE"   "INFO"  "$C_RESET" "$*"; }

# ── 依赖检测 ────────────────────────────────────────────────────────────────
require() {
  command -v "$1" >/dev/null 2>&1 || { err "缺少依赖: $1"; exit 127; }
}

check_basic_deps() {
  local missing=()
  command -v go     >/dev/null 2>&1 || missing+=(go)
  command -v node   >/dev/null 2>&1 || missing+=(node)
  command -v python >/dev/null 2>&1 || missing+=(python)
  if (( ${#missing[@]} > 0 )); then
    err "以下工具未安装: ${missing[*]}"
    err "请先安装: Go(https://go.dev/dl/)、Node(https://nodejs.org/)、Python 3(https://www.python.org/)"
    exit 127
  fi
}

# ── .env 初始化 ──────────────────────────────────────────────────────────────
ensure_env_file() {
  if [[ -f "$ENV_FILE" ]]; then
    log ".env 已存在,跳过复制"
    return 0
  fi
  if [[ ! -f "$ENV_EXAMPLE" ]]; then
    warn ".env.example 不存在,将创建空 .env"
    : >"$ENV_FILE"
    return 0
  fi
  cp "$ENV_EXAMPLE" "$ENV_FILE"
  log "已从 .env.example 复制 .env(默认为本地开发值)"
}

# ── 前端依赖 ─────────────────────────────────────────────────────────────────
ensure_console_deps() {
  if [[ ! -d "$SCRIPT_DIR/frontends/console/node_modules" ]]; then
    info "首次运行,安装 Console 前端依赖(npm install)..."
    npm --prefix "$SCRIPT_DIR/frontends/console" install
  fi
}

# ── 进程管理 ─────────────────────────────────────────────────────────────────
start_bg() {
  # 用法: start_bg <name> <command...>
  local name="$1"; shift
  local pidfile="$RUN_DIR/$name.pid"
  local logfile="$LOG_DIR/$name.log"

  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    warn "$name 已在运行 (PID $(cat "$pidfile"))"
    return 0
  fi

  info "启动 $name -> 日志: $logfile"
  nohup "$@" >"$logfile" 2>&1 &
  local pid=$!
  echo "$pid" >"$pidfile"
  sleep 1
  if kill -0 "$pid" 2>/dev/null; then
    log "$name 已启动 (PID $pid)"
  else
    err "$name 启动失败,查看日志: $logfile"
    tail -n 20 "$logfile" >&2 || true
    return 1
  fi
}

stop_bg() {
  local name="$1"
  local pidfile="$RUN_DIR/$name.pid"
  if [[ ! -f "$pidfile" ]]; then
    warn "$name 未在运行(无 PID 文件)"
    return 0
  fi
  local pid
  pid="$(cat "$pidfile")"
  if kill -0 "$pid" 2>/dev/null; then
    info "停止 $name (PID $pid)"
    kill "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    if kill -0 "$pid" 2>/dev/null; then
      warn "未响应 SIGTERM,发送 SIGKILL"
      kill -9 "$pid" 2>/dev/null || true
    fi
    log "$name 已停止"
  else
    warn "$name 进程不存在,清理 PID 文件"
  fi
  rm -f "$pidfile"
}

status_bg() {
  local names=("mock" "console" "gateway")
  printf '%-10s %-8s %s\n' "NAME" "PID" "STATUS"
  for name in "${names[@]}"; do
    local pidfile="$RUN_DIR/$name.pid"
    if [[ -f "$pidfile" ]]; then
      local pid
      pid="$(cat "$pidfile")"
      if kill -0 "$pid" 2>/dev/null; then
        printf '%-10s %-8s %s\n' "$name" "$pid" "${C_GREEN}running${C_RESET}"
      else
        printf '%-10s %-8s %s\n' "$name" "$pid" "${C_RED}dead${C_RESET}"
      fi
    else
      printf '%-10s %-8s %s\n' "$name" "-" "stopped"
    fi
  done
}

stop_all() {
  stop_bg mock
  stop_bg console
  stop_bg gateway
}

start_all_services() {
  start_mock
  start_console
}

# ── 各服务的启动入口 ─────────────────────────────────────────────────────────
start_mock() {
  require python
  start_bg mock python "$SCRIPT_DIR/scripts/serve_core_mock.py" \
    --host 127.0.0.1 --port 4010
  echo ""
  info "Core API Mock Server 地址: $(mklink http://127.0.0.1:4010/api/v1)"
}

start_console() {
  require node
  ensure_console_deps
  start_bg console npm --prefix "$SCRIPT_DIR/frontends/console" run dev
  echo ""
  info "Console 前端地址: $(mklink http://localhost:5173) (Vite 默认绑 localhost,实际端口以日志输出为准)"
}

start_gateway() {
  if [[ ! -x "$SCRIPT_DIR/bin/ani-gateway" ]]; then
    err "bin/ani-gateway 不存在,请先运行: ./start.sh build"
    return 1
  fi
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
  fi
  start_bg gateway "$SCRIPT_DIR/bin/ani-gateway"
  echo ""
  info "ANI Gateway 地址: $(mklink "http://127.0.0.1:${GATEWAY_PORT:-8080}")"
  warn "gateway 启动需要 PostgreSQL / NATS / Redis 已就绪"
  warn "如未启动依赖,请用 docker compose: docker compose -f deploy/docker/docker-compose.yml up -d"
}

# ── 日志实时跟随 ────────────────────────────────────────────────────────────
# 启动后台 tail 进程,把所有服务日志实时输出到当前终端。
# 使用 awk 把日志行里的 URL 转成 OSC 8 超链接(在支持的终端可点击)。
# OSC 8 格式: \033]8;;URL\033\\TEXT\033]8;;\033\\
# 支持的终端: Windows Terminal / iTerm2 / GNOME Terminal / WezTerm 等。
# 不支持的终端会显示原始 URL(终端会忽略未知 OSC 序列)。
start_log_tail() {
  local files=()
  for name in mock console gateway; do
    local logfile="$LOG_DIR/$name.log"
    [[ -f "$logfile" ]] && files+=("$logfile")
  done
  if (( ${#files[@]} == 0 )); then
    return 0
  fi

  local urlify_awk='
# 先去除 ANSI 颜色/格式转义序列,再做 URL 匹配和 OSC 8 包裹
# ANSI CSI 序列: \x1B[ ... m  (含颜色、加粗等)
# OSC 8 目标终端会自己处理这些序列,这里去除是为了让 URL 正则能跨色码匹配
{
  # 剥离 ANSI CSI 序列: ESC [ 数字;... 字母
  gsub(/\033\[[0-9;]*[a-zA-Z]/, "", $0)
  line = $0
  while (match(line, /https?:\/\/[a-zA-Z0-9._-]+(:[0-9]+)?(\/[a-zA-Z0-9._\/-]*)?/)) {
    url = substr(line, RSTART, RLENGTH)
    prefix = substr(line, 1, RSTART - 1)
    suffix = substr(line, RSTART + RLENGTH)
    line = prefix "\033]8;;" url "\033\\" url "\033]8;;\033\\" suffix
  }
  print line
}
'

  if (( HAS_OSC8 )); then
    tail -n 0 -F "${files[@]}" 2>/dev/null | awk "$urlify_awk" &
  else
    tail -n 0 -F "${files[@]}" 2>/dev/null &
  fi
  TAIL_PID=$!
  # disown 后台 tail,使其不接收 Ctrl+C 信号(由 trap 统一清理)
  disown "$TAIL_PID" 2>/dev/null || true
}

stop_log_tail() {
  if [[ -n "$TAIL_PID" ]] && kill -0 "$TAIL_PID" 2>/dev/null; then
    kill "$TAIL_PID" 2>/dev/null || true
    wait "$TAIL_PID" 2>/dev/null || true
    TAIL_PID=""
  fi
}

# ── 交互模式 ────────────────────────────────────────────────────────────────
show_banner() {
  echo ""
  printf '%s═══════════════════════════════════════════════════════════════%s\n' "$C_CYAN" "$C_RESET"
  printf '%s  ANI 开发栈已启动(日志实时输出中)%s\n' "$C_CYAN" "$C_RESET"
  printf '  Core API Mock:  %s\n' "$(mklink http://127.0.0.1:4010/api/v1)"
  printf '  Console 前端:   %s\n' "$(mklink http://localhost:5173)"
  printf '%s%s\n' "$C_CYAN" "$C_RESET"
  printf '%s  交互命令(输入后回车):%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    status  - 查看服务状态%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    stop    - 停止全部服务(不退出)%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    start   - 重新启动全部服务%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    quit    - 停止全部服务并退出%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    exit    - 同 quit%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    help    - 显示帮助%s\n' "$C_CYAN" "$C_RESET"
  printf '%s    Ctrl+C  - 同 quit(快捷键)%s\n' "$C_CYAN" "$C_RESET"
  printf '%s═══════════════════════════════════════════════════════════════%s\n' "$C_CYAN" "$C_RESET"
  echo ""
}

show_interactive_help() {
  echo ""
  printf '%s交互命令:%s\n' "$C_CYAN" "$C_RESET"
  printf '  status   - 查看服务状态\n'
  printf '  stop     - 停止全部服务(不退出)\n'
  printf '  start    - 重新启动全部服务\n'
  printf '  quit     - 停止全部服务并退出\n'
  printf '  exit     - 同 quit\n'
  printf '  help     - 显示本帮助\n'
  printf '  Ctrl+C   - 同 quit(快捷键)\n'
  echo ""
}

# 清理函数:停止日志 tail + 停止所有服务
cleanup_and_exit() {
  echo ""
  echo ""
  warn "退出中:停止日志跟随与全部服务..."
  stop_log_tail
  stop_all
  warn "已退出"
  exit 0
}

interactive_loop() {
  # trap Ctrl+C / SIGTERM:走 cleanup_and_exit
  trap 'cleanup_and_exit' INT TERM

  while true; do
    printf '%sani>%s ' "$C_CYAN" "$C_RESET"
    local line=""
    if ! read -r line; then
      # read 失败(EOF / Ctrl+D):走清理流程
      cleanup_and_exit
    fi
    case "$line" in
      quit|exit)
        cleanup_and_exit
        ;;
      status)
        echo ""
        status_bg
        echo ""
        ;;
      stop)
        echo ""
        warn "停止全部服务..."
        stop_all
        echo ""
        ;;
      start)
        echo ""
        # 重启前先停掉旧的 tail,重启服务后再启动新的 tail
        stop_log_tail
        warn "重新启动全部服务..."
        stop_all
        start_all_services
        echo ""
        start_log_tail
        log "日志跟随已重启"
        echo ""
        ;;
      help)
        show_interactive_help
        ;;
      "")
        # 空行,直接重新提示
        ;;
      *)
        err "未知命令: $line (输入 help 查看)"
        ;;
    esac
  done
}

# ── 子命令 ──────────────────────────────────────────────────────────────────
cmd_setup() {
  check_basic_deps
  ensure_env_file
  log "setup 完成(未构建二进制,如需构建请运行: ./start.sh build)"
}

cmd_build() {
  check_basic_deps
  info "构建 Go 服务二进制(写入 $SCRIPT_DIR/bin/)..."
  mkdir -p "$SCRIPT_DIR/bin"
  local pkgs=(
    "services/ani-gateway:ani-gateway"
    "services/auth-service:auth-service"
    "services/model-service:model-service"
    "services/task-service:task-service"
    "services/reconcile-worker:reconcile-worker"
    "cli/ani:ani"
  )
  local ldflags="-X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev) -X main.BuildTime=$(date -u +%Y%m%dT%H%M%SZ)"
  for entry in "${pkgs[@]}"; do
    local pkg="${entry%%:*}"
    local out="${entry##*:}"
    info "  -> 构建 $out ($pkg)"
    (
      cd "$SCRIPT_DIR/$pkg"
      CGO_ENABLED=0 GOCACHE="$SCRIPT_DIR/.cache/go-build" GOMODCACHE="$SCRIPT_DIR/.cache/gomod" \
        go build -ldflags "$ldflags" -o "$SCRIPT_DIR/bin/$out" .
    )
  done
  log "Go 二进制构建完成"
}

cmd_default() {
  check_basic_deps
  ensure_env_file
  start_mock
  start_console
  show_banner
  # 启动后台日志实时跟随
  start_log_tail
  info "日志实时跟随已启动(服务日志将实时输出到本窗口)"
  echo ""
  # 进入交互循环
  interactive_loop
}

cmd_bg() {
  check_basic_deps
  ensure_env_file
  start_mock
  start_console
  echo ""
  log "已后台启动 mock + console"
  info "  - Core API Mock: http://127.0.0.1:4010/api/v1"
  info "  - Console UI:    http://localhost:5173"
  info "停止: ./start.sh stop"
  info "日志: ./start.sh logs mock | ./start.sh logs console"
}

cmd_stop()   { stop_all; }
cmd_status() { status_bg; }

cmd_logs() {
  local name="${1:-}"
  if [[ -z "$name" ]]; then
    err "用法: ./start.sh logs <mock|console|gateway>"
    return 2
  fi
  local logfile="$LOG_DIR/$name.log"
  if [[ ! -f "$logfile" ]]; then
    err "日志不存在: $logfile"
    return 1
  fi
  info "跟踪 $name 日志 (Ctrl+C 退出)..."
  tail -n 50 -f "$logfile"
}

cmd_help() {
  sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'
}

# ── 入口 ────────────────────────────────────────────────────────────────────
main() {
  local cmd="${1:-default}"
  case "$cmd" in
    default|all)     cmd_default ;;
    setup)           cmd_setup ;;
    build)           cmd_build ;;
    mock)            check_basic_deps; start_mock; show_banner; start_log_tail; interactive_loop ;;
    console)         check_basic_deps; start_console; show_banner; start_log_tail; interactive_loop ;;
    gateway)         check_basic_deps; start_gateway; show_banner; start_log_tail; interactive_loop ;;
    bg)              cmd_bg ;;
    stop)            cmd_stop ;;
    status)          cmd_status ;;
    logs)            cmd_logs "${2:-}" ;;
    help|-h|--help)  cmd_help ;;
    *)
      err "未知命令: $cmd"
      cmd_help
      exit 2
      ;;
  esac
}

main "$@"
