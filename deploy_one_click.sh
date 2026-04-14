#!/usr/bin/env bash
set -euo pipefail

# 一键部署脚本（默认目标：10.184.4.227）
# 用法：
#   chmod +x ./deploy_one_click.sh
#   ./deploy_one_click.sh
#
# 可选环境变量：
#   REMOTE_HOST=10.184.4.227
#   REMOTE_PORT=22
#   REMOTE_USER=appuser
#   REMOTE_SRC_DIR=/home/appuser/new_api/new-api
#   REMOTE_APP_DIR=/app/new-api
#   SERVICE_NAME=new-api.service
#   APP_PORT=8080
#   BUILD_FRONTEND=1      # 1: 本地构建前端 dist；0: 跳过构建（要求 web/dist 已存在）
#   SKIP_CONFIRM=0        # 1: 跳过部署确认
#   SYNC_DELETE=1         # 1: rsync 时删除远端多余文件

REMOTE_HOST="${REMOTE_HOST:-10.184.4.227}"
REMOTE_PORT="${REMOTE_PORT:-22}"
REMOTE_USER="${REMOTE_USER:-appuser}"

REMOTE_SRC_DIR="${REMOTE_SRC_DIR:-/home/appuser/new_api/new-api}"
REMOTE_APP_DIR="${REMOTE_APP_DIR:-/app/new-api}"
SERVICE_NAME="${SERVICE_NAME:-new-api.service}"
APP_PORT="${APP_PORT:-8080}"

BUILD_FRONTEND="${BUILD_FRONTEND:-1}"
SKIP_CONFIRM="${SKIP_CONFIRM:-0}"
SYNC_DELETE="${SYNC_DELETE:-1}"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() {
  echo "[$(date +'%F %T')] $*"
}

die() {
  echo "[ERROR] $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

confirm() {
  [[ "$SKIP_CONFIRM" == "1" ]] && return 0
  echo
  echo "将部署到：${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PORT}"
  echo "- 远端源码目录: ${REMOTE_SRC_DIR}"
  echo "- 远端运行目录: ${REMOTE_APP_DIR}"
  echo "- systemd 服务: ${SERVICE_NAME}"
  echo "- 应用端口(前端访问端口): ${APP_PORT}"
  echo
  read -r -p "确认继续部署？[y/N] " ans
  [[ "$ans" =~ ^[Yy]$ ]] || die "已取消"
}

build_frontend_if_needed() {
  if [[ "$BUILD_FRONTEND" != "1" ]]; then
    log "跳过前端构建（BUILD_FRONTEND=${BUILD_FRONTEND}）"
    [[ -f "$PROJECT_ROOT/web/dist/index.html" ]] || die "已跳过构建，但 web/dist/index.html 不存在"
    return 0
  fi

  log "开始本地构建前端 web/dist"
  pushd "$PROJECT_ROOT/web" >/dev/null

  local version
  version="$(cat "$PROJECT_ROOT/VERSION" 2>/dev/null || true)"

  if command -v bun >/dev/null 2>&1; then
    log "使用 bun 构建前端"
    bun install --frozen-lockfile || bun install
    DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$version" bun run build
  elif command -v npm >/dev/null 2>&1; then
    log "使用 npm 构建前端"
    if [[ -f package-lock.json ]]; then
      npm ci --no-audit --no-fund || npm install --no-audit --no-fund
    else
      npm install --no-audit --no-fund
    fi
    DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION="$version" npm run build
  else
    popd >/dev/null
    die "本机既没有 bun 也没有 npm，无法构建前端"
  fi

  popd >/dev/null

  [[ -f "$PROJECT_ROOT/web/dist/index.html" ]] || die "前端构建完成但缺少 web/dist/index.html"
  log "前端构建完成"
}

sync_code() {
  log "检查 SSH 连通性"
  ssh -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" "echo '[remote] ssh ok on ' \"\$(hostname)\""

  log "创建远端源码目录: ${REMOTE_SRC_DIR}"
  ssh -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" "mkdir -p '$REMOTE_SRC_DIR'"

  local -a delete_arg=()
  [[ "$SYNC_DELETE" == "1" ]] && delete_arg+=(--delete)

  log "开始 rsync 同步本地代码到远端（不使用 git）"
  rsync -az "${delete_arg[@]}" \
    -e "ssh -p ${REMOTE_PORT}" \
    --exclude ".git/" \
    --exclude ".idea/" \
    --exclude ".vscode/" \
    --exclude ".DS_Store" \
    --exclude "node_modules/" \
    --exclude "web/node_modules/" \
    --exclude "logs/" \
    --exclude "data/" \
    --exclude "one-api.db" \
    --exclude ".deploy/" \
    "$PROJECT_ROOT/" "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_SRC_DIR}/"

  log "代码同步完成"
}

remote_deploy() {
  log "开始执行远端构建 + 替换 + 重启 + 验活"

  ssh -p "$REMOTE_PORT" "$REMOTE_USER@$REMOTE_HOST" \
    "SRC_DIR='$REMOTE_SRC_DIR' APP_DIR='$REMOTE_APP_DIR' SERVICE_NAME='$SERVICE_NAME' APP_PORT='$APP_PORT' bash -s" <<'REMOTE_SCRIPT'
set -euo pipefail

log() {
  echo "[remote][$(date +'%F %T')] $*"
}

die() {
  echo "[remote][ERROR] $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

rollback() {
  if [[ -n "${BACKUP_BIN:-}" ]]; then
    log "开始回滚二进制: ${BACKUP_BIN} -> ${APP_DIR}/new-api"
    sudo cp "${BACKUP_BIN}" "${APP_DIR}/new-api"
    sudo systemctl restart "${SERVICE_NAME}" || true
  else
    log "未找到备份二进制，无法自动回滚"
  fi
}

require_cmd go
require_cmd curl
require_cmd sha256sum
require_cmd sudo

sudo -n true >/dev/null 2>&1 || die "需要免密 sudo（sudo -n true 失败）"
[[ -f "${SRC_DIR}/main.go" ]] || die "源码目录不存在 main.go: ${SRC_DIR}"
[[ -f "${SRC_DIR}/web/dist/index.html" ]] || die "缺少前端产物: ${SRC_DIR}/web/dist/index.html"

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
TMP_BIN="/tmp/new-api.${TIMESTAMP}.new"
TMP_STATUS="/tmp/new-api.${TIMESTAMP}.status.json"
BACKUP_BIN=""

cleanup() {
  rm -f "${TMP_BIN}" "${TMP_STATUS}" || true
}
trap cleanup EXIT

log "go version: $(go version)"
log "构建新二进制（包含 go:embed web/dist）"
cd "${SRC_DIR}"
go build -trimpath -o "${TMP_BIN}" .
NEW_SHA="$(sha256sum "${TMP_BIN}" | awk '{print $1}')"
log "新二进制 SHA256: ${NEW_SHA}"

log "准备运行目录: ${APP_DIR}"
sudo install -d -m 755 "${APP_DIR}" "${APP_DIR}/logs" "${APP_DIR}/backups"

if sudo test -f "${APP_DIR}/new-api"; then
  BACKUP_BIN="${APP_DIR}/backups/new-api.${TIMESTAMP}.bak"
  sudo cp "${APP_DIR}/new-api" "${BACKUP_BIN}"
  log "已备份旧二进制: ${BACKUP_BIN}"
fi

sudo install -m 755 "${TMP_BIN}" "${APP_DIR}/new-api"

log "写入 systemd override（端口: ${APP_PORT}）"
OVERRIDE_DIR="/etc/systemd/system/${SERVICE_NAME}.d"
OVERRIDE_FILE="${OVERRIDE_DIR}/override.conf"
sudo install -d -m 755 "${OVERRIDE_DIR}"
sudo tee "${OVERRIDE_FILE}" >/dev/null <<EOF
[Service]
ExecStart=
ExecStart=${APP_DIR}/new-api --port ${APP_PORT} --log-dir ${APP_DIR}/logs
EOF

sudo systemctl daemon-reload
if ! sudo systemctl restart "${SERVICE_NAME}"; then
  log "重启失败，执行回滚"
  rollback
  exit 1
fi

ACTIVE_STATE="$(sudo systemctl is-active "${SERVICE_NAME}" || true)"
if [[ "${ACTIVE_STATE}" != "active" ]]; then
  log "服务状态异常: ${ACTIVE_STATE}，执行回滚"
  rollback
  exit 1
fi

log "开始健康检查: http://127.0.0.1:${APP_PORT}/api/status"
ok=0
for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:${APP_PORT}/api/status" >"${TMP_STATUS}" 2>/dev/null; then
    ok=1
    break
  fi
  sleep 1
done

if [[ "${ok}" -ne 1 ]]; then
  log "健康检查失败，执行回滚"
  rollback
  exit 1
fi

log "部署成功"
echo "----- DEPLOY VERIFY -----"
echo "[1] systemd 状态"
sudo systemctl status "${SERVICE_NAME}" --no-pager -l | sed -n '1,20p'
echo
echo "[2] 新二进制信息"
sudo ls -l --time-style=long-iso "${APP_DIR}/new-api"
echo "sha256=${NEW_SHA}"
echo
echo "[3] /api/status"
cat "${TMP_STATUS}"
REMOTE_SCRIPT

  log "远端部署流程执行完成"
}

main() {
  cd "$PROJECT_ROOT"

  require_cmd ssh
  require_cmd rsync

  [[ "$APP_PORT" =~ ^[0-9]+$ ]] || die "APP_PORT 必须是数字"
  (( APP_PORT >= 1 && APP_PORT <= 65535 )) || die "APP_PORT 超出范围: ${APP_PORT}"

  local git_rev
  git_rev="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo 'unknown')"

  log "本地项目目录: ${PROJECT_ROOT}"
  log "本地代码版本: ${git_rev}"
  confirm
  build_frontend_if_needed
  sync_code
  remote_deploy

  echo
  log "✅ 一键部署完成"
  log "如需免确认执行：SKIP_CONFIRM=1 ./deploy_one_click.sh"
  log "如需改端口：APP_PORT=8080 ./deploy_one_click.sh"
}

main "$@"
