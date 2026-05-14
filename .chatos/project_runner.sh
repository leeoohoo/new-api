#!/usr/bin/env bash
set -euo pipefail

# project_runner.sh
# 启动策略依据（已读取）：
# - go.mod                -> backend (Go)
# - web/package.json      -> frontend (Vite)
# - electron/package.json -> electron (桌面端，默认不自动启动)
# - docker-compose.yml    -> 存在容器化方案，但本脚本默认管理本地进程

PROJECT_ROOT="/Users/lilei/project/work/zj/new-api"
RUNNER_ROOT="$PROJECT_ROOT/project_runner"
LOG_DIR="$RUNNER_ROOT/logs"
PID_DIR="$RUNNER_ROOT/pids"
BIN_DIR="$RUNNER_ROOT/bin"

BACKEND_PORT="${BACKEND_PORT:-3000}"
FRONTEND_PORT="${FRONTEND_PORT:-5173}"

# 可选覆盖项
BACKEND_CMD="${BACKEND_CMD:-}"              # 不为空时直接使用（例如 go run main.go --port 3000）
BACKEND_BIN="${BACKEND_BIN:-$BIN_DIR/new-api}" # BACKEND_CMD 为空时，默认先 go build 再启动该二进制
FRONTEND_CMD="${FRONTEND_CMD:-}"
WORKER_CMD="${WORKER_CMD:-}"
WORKER_DIR="${WORKER_DIR:-$PROJECT_ROOT}"

ENABLE_ELECTRON="${ENABLE_ELECTRON:-0}"
ELECTRON_CMD="${ELECTRON_CMD:-npm run dev-app}"

usage() {
  cat <<'EOF'
Usage: ./.chatos/project_runner.sh <start|stop|restart>

Commands:
  start    启动服务（backend / frontend / worker[若可识别]）
  stop     停止服务（PID 优先，必要时按端口补偿回收）
  restart  stop + start

Environment variables:
  BACKEND_PORT      后端端口（默认 3000）
  FRONTEND_PORT     前端端口（默认 5173）

  BACKEND_CMD       自定义后端启动命令（设置后不再自动 go build）
  BACKEND_BIN       自动构建模式下后端二进制路径（默认 project_runner/bin/new-api）

  FRONTEND_CMD      自定义前端启动命令
  WORKER_CMD        自定义 worker 启动命令
  WORKER_DIR        worker 工作目录（默认项目根）

  ENABLE_ELECTRON   是否启动 electron（0/1，默认 0）
  ELECTRON_CMD      自定义 electron 启动命令

Logs:
  project_runner/logs/{runner,backend,frontend,worker,electron}.log
EOF
}

ts() {
  date '+%Y-%m-%d %H:%M:%S'
}

ensure_dirs() {
  mkdir -p "$LOG_DIR" "$PID_DIR" "$BIN_DIR"
}

runner_log() {
  local msg="$1"
  ensure_dirs
  echo "[$(ts)] $msg" | tee -a "$LOG_DIR/runner.log"
}

service_log_file() {
  local service="$1"
  echo "$LOG_DIR/${service}.log"
}

service_pid_file() {
  local service="$1"
  echo "$PID_DIR/${service}.pid"
}

service_meta_file() {
  local service="$1"
  echo "$PID_DIR/${service}.meta"
}

service_port() {
  local service="$1"
  case "$service" in
    backend)
      echo "$BACKEND_PORT"
      ;;
    frontend)
      echo "$FRONTEND_PORT"
      ;;
    *)
      echo ""
      ;;
  esac
}

is_pid_running() {
  local pid="$1"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null
}

cmd_token() {
  local cmd="$1"
  local first="${cmd%% *}"

  # 兼容 "binary" / 'binary' 形式，避免 token 末尾残留引号导致误判
  first="${first#\"}"
  first="${first%\"}"
  first="${first#\'}"
  first="${first%\'}"

  first="${first##*/}"
  echo "$first"
}

pids_on_port() {
  local port="$1"
  [[ -n "$port" ]] || return 0

  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true
    return 0
  fi

  if command -v ss >/dev/null 2>&1; then
    ss -ltnp 2>/dev/null \
      | awk -v p=":$port" '$4 ~ p {print $NF}' \
      | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' \
      | sort -u || true
    return 0
  fi
}

wait_port_listening() {
  local port="$1"
  local timeout_secs="$2"
  local i=0

  while [[ "$i" -lt "$timeout_secs" ]]; do
    if [[ -n "$(pids_on_port "$port" | head -n1)" ]]; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done

  return 1
}

start_service() {
  local service="$1"
  local workdir="$2"
  local cmd="$3"

  local log_file pid_file meta_file port
  log_file="$(service_log_file "$service")"
  pid_file="$(service_pid_file "$service")"
  meta_file="$(service_meta_file "$service")"
  port="$(service_port "$service")"

  ensure_dirs

  if [[ -f "$pid_file" ]]; then
    local existing_pid
    existing_pid="$(cat "$pid_file" 2>/dev/null || true)"
    if [[ -n "$existing_pid" ]] && is_pid_running "$existing_pid"; then
      runner_log "$service 已在运行 (pid=$existing_pid)，跳过重复启动"
      return 0
    fi
    rm -f "$pid_file" "$meta_file"
  fi

  {
    echo "[$(ts)] ==== START $service ===="
    echo "[$(ts)] workdir: $workdir"
    echo "[$(ts)] command: $cmd"
  } >> "$log_file"

  nohup bash -lc "cd \"$workdir\" && exec $cmd" </dev/null >> "$log_file" 2>&1 &
  local pid=$!

  echo "$pid" > "$pid_file"
  {
    echo "token=$(cmd_token "$cmd")"
    echo "workdir=$workdir"
    echo "cmd=$cmd"
    echo "port=$port"
    echo "started_at=$(ts)"
  } > "$meta_file"

  # 先确认主进程存活
  sleep 1
  if ! is_pid_running "$pid"; then
    runner_log "$service 启动失败（进程已退出），请查看: $log_file"
    rm -f "$pid_file" "$meta_file"
    return 1
  fi

  # 若定义了服务端口，等待端口就绪（减少“进程在但未 ready”的误判）
  if [[ -n "$port" ]]; then
    if ! wait_port_listening "$port" 20; then
      runner_log "$service 启动超时：端口 $port 未监听，请查看: $log_file"
      rm -f "$pid_file" "$meta_file"
      return 1
    fi
  fi

  runner_log "$service 启动成功 (pid=$pid)，日志: $log_file"
  return 0
}

stop_service_by_port() {
  local service="$1"
  local port
  port="$(service_port "$service")"

  [[ -n "$port" ]] || return 0

  local pids
  pids="$(pids_on_port "$port" | tr '\n' ' ')"
  if [[ -z "${pids// /}" ]]; then
    return 0
  fi

  runner_log "$service 检测到端口 $port 仍被占用，按端口回收进程: $pids"

  local p
  for p in $pids; do
    kill "$p" 2>/dev/null || true
  done

  local i=0
  local remain
  while [[ "$i" -lt 5 ]]; do
    remain="$(pids_on_port "$port" | tr '\n' ' ')"
    if [[ -z "${remain// /}" ]]; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done

  remain="$(pids_on_port "$port" | tr '\n' ' ')"
  for p in $remain; do
    if is_pid_running "$p"; then
      kill -9 "$p" 2>/dev/null || true
    fi
  done

  sleep 1
  remain="$(pids_on_port "$port" | tr '\n' ' ')"
  if [[ -n "${remain// /}" ]]; then
    runner_log "$service 端口 $port 仍被占用，停止失败，残留进程: $remain"
    return 1
  fi

  return 0
}

stop_service() {
  local service="$1"
  local pid_file meta_file log_file
  pid_file="$(service_pid_file "$service")"
  meta_file="$(service_meta_file "$service")"
  log_file="$(service_log_file "$service")"

  if [[ ! -f "$pid_file" ]]; then
    runner_log "$service 未发现 pid 文件，尝试按端口补偿停止"
    if ! stop_service_by_port "$service"; then
      return 1
    fi
    return 0
  fi

  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    rm -f "$pid_file" "$meta_file"
    runner_log "$service pid 文件为空，已清理"
    if ! stop_service_by_port "$service"; then
      return 1
    fi
    return 0
  fi

  if ! is_pid_running "$pid"; then
    rm -f "$pid_file" "$meta_file"
    runner_log "$service 进程不存在（可能已退出），已清理 pid 文件"
    if ! stop_service_by_port "$service"; then
      return 1
    fi
    return 0
  fi

  local should_kill_pid=1

  # 额外保护：校验 token（降低 PID 复用误杀概率）
  if [[ -f "$meta_file" ]]; then
    local token actual_cmd
    token="$(grep '^token=' "$meta_file" 2>/dev/null | head -n1 | cut -d'=' -f2- || true)"
    actual_cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    if [[ -n "$token" && "$actual_cmd" != *"$token"* ]]; then
      should_kill_pid=0
      runner_log "$service pid=$pid 命令与记录不匹配，跳过 PID 终止以避免误杀"
      runner_log "$service recorded_token=$token, actual_cmd=$actual_cmd"
      echo "[$(ts)] WARN: token mismatch when stopping pid=$pid" >> "$log_file"
    fi
  else
    runner_log "$service 缺少 meta 文件，按 PID 直接停止 pid=$pid"
  fi

  if [[ "$should_kill_pid" -eq 1 ]]; then
    echo "[$(ts)] stopping pid=$pid" >> "$log_file"
    kill "$pid" 2>/dev/null || true

    local i=0
    while [[ "$i" -lt 20 ]]; do
      if ! is_pid_running "$pid"; then
        break
      fi
      sleep 1
      i=$((i + 1))
    done

    if is_pid_running "$pid"; then
      echo "[$(ts)] force kill pid=$pid" >> "$log_file"
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi

  rm -f "$pid_file" "$meta_file"

  # PID 优先后，按端口做补偿（解决 go run 等父子进程残留）
  if ! stop_service_by_port "$service"; then
    return 1
  fi

  # 无端口服务（如 worker/electron）也要严格校验，避免误报“已停止”
  if is_pid_running "$pid"; then
    runner_log "$service 停止失败，pid=$pid 仍在运行"
    return 1
  fi

  runner_log "$service 已停止"
}

detect_frontend_cmd() {
  if [[ -n "$FRONTEND_CMD" ]]; then
    echo "$FRONTEND_CMD"
    return 0
  fi

  # 强制 strictPort，避免 Vite 自动漂移到 5174/5175 导致误判
  if command -v bun >/dev/null 2>&1; then
    echo "bun run dev --host 0.0.0.0 --port ${FRONTEND_PORT} --strictPort"
    return 0
  fi

  if command -v npm >/dev/null 2>&1; then
    echo "npm run dev -- --host 0.0.0.0 --port ${FRONTEND_PORT} --strictPort"
    return 0
  fi

  return 1
}

start_backend() {
  local log_file cmd
  log_file="$(service_log_file "backend")"

  if [[ ! -f "$PROJECT_ROOT/go.mod" ]]; then
    runner_log "未检测到 go.mod，跳过 backend"
    return 1
  fi

  if [[ -n "$BACKEND_CMD" ]]; then
    cmd="$BACKEND_CMD"
    start_service "backend" "$PROJECT_ROOT" "$cmd"
    return $?
  fi

  if ! command -v go >/dev/null 2>&1; then
    runner_log "go 不存在，无法启动 backend"
    echo "[$(ts)] TODO: install go or set BACKEND_CMD" >> "$log_file"
    return 1
  fi

  {
    echo "[$(ts)] ==== BUILD backend ===="
    echo "[$(ts)] command: go build -o $BACKEND_BIN ."
  } >> "$log_file"

  if ! (cd "$PROJECT_ROOT" && go build -o "$BACKEND_BIN" . >> "$log_file" 2>&1); then
    runner_log "backend 构建失败，请查看: $log_file"
    return 1
  fi

  chmod +x "$BACKEND_BIN" 2>/dev/null || true
  cmd="\"$BACKEND_BIN\" --port ${BACKEND_PORT}"

  start_service "backend" "$PROJECT_ROOT" "$cmd"
}

start_frontend() {
  local log_file cmd
  log_file="$(service_log_file "frontend")"

  if [[ ! -f "$PROJECT_ROOT/web/package.json" ]]; then
    runner_log "未检测到 web/package.json，跳过 frontend"
    return 1
  fi

  if ! cmd="$(detect_frontend_cmd)"; then
    runner_log "bun/npm 均不存在，无法启动 frontend"
    echo "[$(ts)] TODO: install bun or npm, or set FRONTEND_CMD" >> "$log_file"
    return 1
  fi

  start_service "frontend" "$PROJECT_ROOT/web" "$cmd"
}

start_worker() {
  local log_file
  log_file="$(service_log_file "worker")"

  # 1) 显式命令优先
  if [[ -n "$WORKER_CMD" ]]; then
    start_service "worker" "$WORKER_DIR" "$WORKER_CMD"
    return $?
  fi

  # 2) 常见结构自动识别（当前仓库未识别到可直接运行的 worker）
  if [[ -f "$PROJECT_ROOT/worker/package.json" ]]; then
    if command -v npm >/dev/null 2>&1; then
      start_service "worker" "$PROJECT_ROOT/worker" "npm run dev"
      return $?
    fi
  fi

  if [[ -f "$PROJECT_ROOT/worker/main.go" ]]; then
    if command -v go >/dev/null 2>&1; then
      start_service "worker" "$PROJECT_ROOT/worker" "go run ."
      return $?
    fi
  fi

  # 3) 按要求明确提示待人工补充
  {
    echo "[$(ts)] TODO: 未检测到可自动启动的本地 worker。"
    echo "[$(ts)] TODO: 如需启动 worker，请设置 WORKER_CMD 和（可选）WORKER_DIR。"
    echo "[$(ts)] TODO: 示例: WORKER_CMD='python worker.py' WORKER_DIR='$PROJECT_ROOT/worker' ./.chatos/project_runner.sh start"
  } >> "$log_file"

  runner_log "worker 启动命令待人工补充，详情见 ${log_file}"
  return 1
}

start_electron() {
  local log_file
  log_file="$(service_log_file "electron")"

  if [[ ! -f "$PROJECT_ROOT/electron/package.json" ]]; then
    return 1
  fi

  if [[ "$ENABLE_ELECTRON" != "1" ]]; then
    {
      echo "[$(ts)] INFO: 检测到 electron/package.json。"
      echo "[$(ts)] INFO: Electron 属于桌面应用，默认不自动启动。"
      echo "[$(ts)] INFO: 如需启动，请设置 ENABLE_ELECTRON=1。"
    } >> "$log_file"
    runner_log "electron 默认未启动（如需启动：ENABLE_ELECTRON=1）"
    return 1
  fi

  if ! command -v npm >/dev/null 2>&1; then
    runner_log "npm 不存在，无法启动 electron"
    echo "[$(ts)] TODO: install npm or set ELECTRON_CMD" >> "$log_file"
    return 1
  fi

  start_service "electron" "$PROJECT_ROOT/electron" "$ELECTRON_CMD"
}

start_all() {
  ensure_dirs
  runner_log "========== project runner start =========="

  local started=0

  if start_backend; then
    started=$((started + 1))
  fi

  if start_frontend; then
    started=$((started + 1))
  fi

  # worker 无法自动识别时只打日志，不阻断其他服务
  if start_worker; then
    started=$((started + 1))
  fi

  # electron 默认不启动，不计入失败
  if start_electron; then
    started=$((started + 1))
  fi

  runner_log "start 完成，成功启动服务数: $started"

  if [[ "$started" -eq 0 ]]; then
    runner_log "未成功启动任何服务，请检查日志目录: $LOG_DIR"
    return 1
  fi
}

stop_all() {
  ensure_dirs
  runner_log "========== project runner stop =========="

  local failed=0

  # 逆序停止
  if ! stop_service "electron"; then
    failed=$((failed + 1))
  fi
  if ! stop_service "worker"; then
    failed=$((failed + 1))
  fi
  if ! stop_service "frontend"; then
    failed=$((failed + 1))
  fi
  if ! stop_service "backend"; then
    failed=$((failed + 1))
  fi

  if [[ "$failed" -gt 0 ]]; then
    runner_log "stop 完成，但有 $failed 个服务停止失败"
    return 1
  fi

  runner_log "stop 完成"
}

main() {
  if [[ ! -d "$PROJECT_ROOT" ]]; then
    echo "项目根目录不存在: $PROJECT_ROOT" >&2
    exit 1
  fi

  local action="${1:-}"
  case "$action" in
    start)
      start_all
      ;;
    stop)
      stop_all
      ;;
    restart)
      stop_all
      start_all
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
