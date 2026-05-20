#!/usr/bin/env bash
set -Eeuo pipefail

# 无源码部署脚本：
# - 本地构建前端与 Linux/amd64 二进制
# - 仅打包并上传运行产物（new-api + models.json）
# - 远端仅替换 /app/new-api 下的运行文件，不上传源码
# - 自动备份、重启、健康检查，失败自动回滚
#
# 默认目标：10.184.4.227 的 3000 服务（new-api.service）
# 用法：
#   chmod +x ./deploy_runtime_only_3000.sh
#   ./deploy_runtime_only_3000.sh
#
# 常用环境变量：
#   REMOTE=appuser@10.184.4.227
#   SSH_PORT=22
#   SERVICE_NAME=new-api.service
#   REMOTE_APP_DIR=/app/new-api
#   TARGET_BIN=/app/new-api/new-api
#   TARGET_MODELS=/app/new-api/models.json
#   HEALTH_URL=http://127.0.0.1:3000/api/status
#   SKIP_WEB_BUILD=0
#   KEEP_LOCAL_BUNDLE=1
#   GOPROXY=https://goproxy.cn,direct
#   GOSUMDB=off

REMOTE="${REMOTE:-appuser@10.184.4.227}"
SSH_PORT="${SSH_PORT:-22}"
SERVICE_NAME="${SERVICE_NAME:-new-api.service}"
REMOTE_APP_DIR="${REMOTE_APP_DIR:-/app/new-api}"
TARGET_BIN="${TARGET_BIN:-${REMOTE_APP_DIR}/new-api}"
TARGET_MODELS="${TARGET_MODELS:-${REMOTE_APP_DIR}/models.json}"
BACKUP_DIR="${BACKUP_DIR:-${REMOTE_APP_DIR}/backups}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:3000/api/status}"
SKIP_WEB_BUILD="${SKIP_WEB_BUILD:-0}"
KEEP_LOCAL_BUNDLE="${KEEP_LOCAL_BUNDLE:-1}"
REMOTE_SAFE_PATH="${REMOTE_SAFE_PATH:-/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin}"
GOOS_VALUE="${GOOS:-linux}"
GOARCH_VALUE="${GOARCH:-amd64}"
CGO_ENABLED_VALUE="${CGO_ENABLED:-0}"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() {
  echo "[$(date +'%F %T')] $*"
}

die() {
  echo "[ERROR] $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    die "缺少 sha256sum 或 shasum，无法计算校验值"
  fi
}

build_frontend_if_needed() {
  if [[ "$SKIP_WEB_BUILD" == "1" ]]; then
    log "跳过前端构建（SKIP_WEB_BUILD=1）"
    [[ -f "$PROJECT_ROOT/web/dist/index.html" ]] || die "已跳过构建，但 web/dist/index.html 不存在"
    return 0
  fi

  local version
  version="$(cat "$PROJECT_ROOT/VERSION" 2>/dev/null || true)"

  log "开始本地构建前端 web/dist"
  pushd "$PROJECT_ROOT/web" >/dev/null
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
}

build_runtime_bundle() {
  local ts="$1"
  local commit="$2"

  BUNDLE_DIR="$PROJECT_ROOT/.tmp/deploy_runtime_only/$ts"
  PKG_DIR="$BUNDLE_DIR/pkg"
  LOCAL_BIN="$PKG_DIR/new-api"
  LOCAL_MODELS="$PKG_DIR/models.json"
  LOCAL_TAR="$BUNDLE_DIR/newapi-runtime.tar.gz"
  LOCAL_CHECKSUMS="$BUNDLE_DIR/checksums.txt"

  rm -rf "$BUNDLE_DIR"
  mkdir -p "$PKG_DIR"

  [[ -f "$PROJECT_ROOT/models.json" ]] || die "缺少 models.json"
  [[ -f "$PROJECT_ROOT/web/dist/index.html" ]] || die "缺少 web/dist/index.html，请先构建前端"

  log "开始本地构建 ${GOOS_VALUE}/${GOARCH_VALUE} 二进制"
  (
    cd "$PROJECT_ROOT"
    env \
      CGO_ENABLED="$CGO_ENABLED_VALUE" \
      GOOS="$GOOS_VALUE" \
      GOARCH="$GOARCH_VALUE" \
      go build -trimpath -o "$LOCAL_BIN" .
  )

  [[ -f "$LOCAL_BIN" ]] || die "本地构建未生成二进制: $LOCAL_BIN"
  cp "$PROJECT_ROOT/models.json" "$LOCAL_MODELS"
  chmod 755 "$LOCAL_BIN"

  log "打包运行产物（仅包含 new-api 与 models.json）"
  (
    cd "$PKG_DIR"
    tar -czf "$LOCAL_TAR" new-api models.json
  )

  BIN_SHA="$(sha256_file "$LOCAL_BIN")"
  MODELS_SHA="$(sha256_file "$LOCAL_MODELS")"
  TAR_SHA="$(sha256_file "$LOCAL_TAR")"

  cat >"$LOCAL_CHECKSUMS" <<EOF
commit=${commit}
binary_sha256=${BIN_SHA}
models_sha256=${MODELS_SHA}
tarball_sha256=${TAR_SHA}
EOF

  log "本地构建完成"
  log "- bundle: $LOCAL_TAR"
  log "- binary sha256: $BIN_SHA"
  log "- models sha256: $MODELS_SHA"
  log "- tar sha256: $TAR_SHA"
}

deploy_over_single_ssh() {
  local ts="$1"
  local commit="$2"
  local remote_script remote_cmd remote_args

  REMOTE_TAR="/tmp/newapi-runtime-${ts}.tar.gz"

  remote_script=$(cat <<'EOS'
set -Eeuo pipefail

REMOTE_TAR="$1"
EXPECTED_TAR_SHA="$2"
TARGET_BIN="$3"
TARGET_MODELS="$4"
SERVICE_NAME="$5"
HEALTH_URL="$6"
COMMIT="$7"
BACKUP_DIR="$8"
TS="$9"
APP_DIR="$(dirname "$TARGET_BIN")"
WORK_DIR="/tmp/newapi-runtime-deploy-${TS}"

log() {
  echo "[remote][$(date +'%F %T')] $*"
}

die() {
  echo "[remote][ERROR] $*" >&2
  exit 1
}

sha256_file_remote() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    die "缺少 sha256sum 或 shasum，无法计算远端校验值"
  fi
}

SUDO=""
if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  SUDO="sudo -n"
fi

run_root() {
  if [[ -n "$SUDO" ]]; then
    $SUDO "$@"
  else
    "$@"
  fi
}

cleanup() {
  rm -rf "$WORK_DIR" "$REMOTE_TAR" || true
}
trap cleanup EXIT

rollback() {
  log "执行回滚"
  if [[ -n "${BACKUP_BIN:-}" && -f "${BACKUP_BIN}" ]]; then
    run_root install -m 755 "$BACKUP_BIN" "$TARGET_BIN"
  fi
  if [[ -n "${BACKUP_MODELS:-}" && -f "${BACKUP_MODELS}" ]]; then
    run_root install -m 644 "$BACKUP_MODELS" "$TARGET_MODELS"
  fi
  if command -v systemctl >/dev/null 2>&1; then
    run_root systemctl restart "$SERVICE_NAME" || true
  fi
}

on_error() {
  local exit_code=$?
  if [[ "${DEPLOY_APPLIED:-0}" == "1" ]]; then
    rollback || true
  fi
  exit "$exit_code"
}
trap on_error ERR

log "接收运行包到远端: $REMOTE_TAR"
cat > "$REMOTE_TAR"
[[ -s "$REMOTE_TAR" ]] || die "上传得到的远端运行包为空: $REMOTE_TAR"

ACTUAL_TAR_SHA="$(sha256_file_remote "$REMOTE_TAR")"
[[ "$ACTUAL_TAR_SHA" == "$EXPECTED_TAR_SHA" ]] || die "远端运行包校验失败: expected=$EXPECTED_TAR_SHA actual=$ACTUAL_TAR_SHA"
log "远端运行包校验通过: $ACTUAL_TAR_SHA"

mkdir -p "$WORK_DIR"
tar -xzf "$REMOTE_TAR" -C "$WORK_DIR"

NEW_BIN="$WORK_DIR/new-api"
NEW_MODELS="$WORK_DIR/models.json"
[[ -f "$NEW_BIN" ]] || die "运行包缺少 new-api"
[[ -f "$NEW_MODELS" ]] || die "运行包缺少 models.json"

if [[ ! -w "$APP_DIR" && -z "$SUDO" && "$(id -u)" -ne 0 ]]; then
  die "无权限写入 $APP_DIR，且 sudo 不可用"
fi

run_root install -d -m 755 "$APP_DIR" "$APP_DIR/logs" "$BACKUP_DIR"

BACKUP_BIN=""
BACKUP_MODELS=""
if run_root test -f "$TARGET_BIN"; then
  BACKUP_BIN="$BACKUP_DIR/new-api.bak.${TS}"
  run_root cp -a "$TARGET_BIN" "$BACKUP_BIN"
fi
if run_root test -f "$TARGET_MODELS"; then
  BACKUP_MODELS="$BACKUP_DIR/models.json.bak.${TS}"
  run_root cp -a "$TARGET_MODELS" "$BACKUP_MODELS"
fi

DEPLOY_APPLIED=1
run_root install -m 755 "$NEW_BIN" "$TARGET_BIN"
run_root install -m 644 "$NEW_MODELS" "$TARGET_MODELS"

if ! command -v systemctl >/dev/null 2>&1; then
  die "systemctl 不存在，无法重启服务"
fi
run_root systemctl restart "$SERVICE_NAME"
run_root systemctl is-active --quiet "$SERVICE_NAME"

sleep 1
HEALTH_OK_URL=""
for ((i=1; i<=30; i++)); do
  if curl -fsS --max-time 3 "$HEALTH_URL" >/tmp/newapi_health.json; then
    HEALTH_OK_URL="$HEALTH_URL"
    break
  fi
  ALT_HEALTH="${HEALTH_URL/127.0.0.1/localhost}"
  if [[ "$ALT_HEALTH" != "$HEALTH_URL" ]] && curl -fsS --max-time 3 "$ALT_HEALTH" >/tmp/newapi_health.json; then
    HEALTH_OK_URL="$ALT_HEALTH"
    break
  fi
  sleep 2
done

[[ -n "$HEALTH_OK_URL" ]] || die "健康检查失败"
trap - ERR

log "部署成功"
echo "DEPLOY_OK commit=$COMMIT"
echo "health_url=$HEALTH_OK_URL"
echo "binary_sha256=$(sha256_file_remote "$TARGET_BIN")"
echo "models_sha256=$(sha256_file_remote "$TARGET_MODELS")"
run_root systemctl status "$SERVICE_NAME" --no-pager -l | sed -n '1,20p'
cat /tmp/newapi_health.json
EOS
)

  remote_cmd="export PATH='$REMOTE_SAFE_PATH'; /bin/bash -c $(printf '%q' "$remote_script") _"
  remote_args="$(printf ' %q' "$REMOTE_TAR" "$TAR_SHA" "$TARGET_BIN" "$TARGET_MODELS" "$SERVICE_NAME" "$HEALTH_URL" "$commit" "$BACKUP_DIR" "$ts")"

  log "通过单次 SSH 连接上传运行包并执行远端部署: $REMOTE_TAR"
  ssh -p "$SSH_PORT" -o ConnectTimeout=8 "$REMOTE" "$remote_cmd$remote_args" < "$LOCAL_TAR"
}

cleanup_local_bundle() {
  if [[ "$KEEP_LOCAL_BUNDLE" == "1" ]]; then
    log "保留本地运行包目录: $BUNDLE_DIR"
  else
    rm -rf "$BUNDLE_DIR"
    log "已清理本地运行包目录"
  fi
}

main() {
  cd "$PROJECT_ROOT"

  need_cmd ssh
  need_cmd tar
  need_cmd go
  need_cmd curl
  need_cmd git

  [[ -f "$PROJECT_ROOT/go.mod" ]] || die "请在项目根目录执行（缺少 go.mod）"
  [[ -d "$PROJECT_ROOT/web" ]] || die "缺少 web 目录"

  local commit ts
  commit="$(git -C "$PROJECT_ROOT" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
  ts="$(date +%Y%m%d_%H%M%S)"

  log "=== 无源码部署开始 ==="
  log "remote=$REMOTE service=$SERVICE_NAME"
  log "target_bin=$TARGET_BIN"
  log "target_models=$TARGET_MODELS"

  build_frontend_if_needed
  build_runtime_bundle "$ts" "$commit"
  deploy_over_single_ssh "$ts" "$commit"
  cleanup_local_bundle

  log "✅ 无源码部署成功: commit=$commit remote=$REMOTE service=$SERVICE_NAME"
}

main "$@"
