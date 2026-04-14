#!/usr/bin/env bash
set -Eeuo pipefail

REMOTE="${REMOTE:-appuser@10.184.4.227}"
SSH_PORT="${SSH_PORT:-22}"
REMOTE_BASE="${REMOTE_BASE:-/home/appuser/new_api}"
SERVICE_NAME="${SERVICE_NAME:-new-api.service}"
TARGET_BIN="${TARGET_BIN:-/app/new-api/new-api}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:3000/api/status}"
SKIP_WEB_BUILD="${SKIP_WEB_BUILD:-0}"
REMOTE_SAFE_PATH="${REMOTE_SAFE_PATH:-/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin}"

need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "缺少命令: $1"; exit 1; }; }

need_cmd ssh
need_cmd rsync
need_cmd curl
need_cmd git
if [[ "$SKIP_WEB_BUILD" != "1" ]]; then
  need_cmd npm
fi

[[ -f go.mod ]] || { echo "请在项目根目录执行（缺少 go.mod）"; exit 1; }
[[ -d web ]] || { echo "缺少 web 目录"; exit 1; }

COMMIT="$(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
TS="$(date +%Y%m%d_%H%M%S)"
REMOTE_RELEASE="$REMOTE_BASE/releases/$TS"

echo "[1/6] SSH 连通性检查..."
ssh -p "$SSH_PORT" -o ConnectTimeout=8 "$REMOTE" "export PATH='$REMOTE_SAFE_PATH'; echo connected >/dev/null"

if [[ "$SKIP_WEB_BUILD" != "1" ]]; then
  echo "[2/6] 本地构建前端 web/dist ..."
  (
    cd web
    if ! npm ci; then
      echo "npm ci 失败，尝试 npm ci --legacy-peer-deps ..."
      npm ci --legacy-peer-deps
    fi
    npm run build
  )
else
  echo "[2/6] 跳过前端构建（SKIP_WEB_BUILD=1）"
fi
[[ -f web/dist/index.html ]] || { echo "web/dist/index.html 不存在，停止"; exit 1; }

echo "[3/6] 上传源码到远端: $REMOTE_RELEASE"
ssh -p "$SSH_PORT" "$REMOTE" "export PATH='$REMOTE_SAFE_PATH'; /usr/bin/mkdir -p '$REMOTE_RELEASE'"
# macOS 自带 rsync(2.6.x) 不支持 --progress；自动降级到 --progress
RSYNC_PROGRESS_ARGS=(--progress)
if rsync --version 2>/dev/null | head -n 1 | grep -Eq 'version 3\.'; then
  RSYNC_PROGRESS_ARGS=(--progress)
fi

rsync -az "${RSYNC_PROGRESS_ARGS[@]}" -e "ssh -p $SSH_PORT" \
  --exclude '.git' \
  --exclude '.deploy' \
  --exclude 'node_modules' \
  --exclude 'web/node_modules' \
  ./ "$REMOTE:$REMOTE_RELEASE/"

echo "[4/6] 远端编译 + 替换二进制 + 重启服务"
ssh -p "$SSH_PORT" "$REMOTE" "export PATH='$REMOTE_SAFE_PATH'; /bin/bash -s -- '$REMOTE_RELEASE' '$TARGET_BIN' '$SERVICE_NAME' '$HEALTH_URL' '$COMMIT'" <<'EOS'
set -Eeuo pipefail
REL="$1"
TARGET_BIN="$2"
SERVICE="$3"
HEALTH="$4"
COMMIT="$5"

export PATH="/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

cd "$REL"
[[ -f web/dist/index.html ]] || { echo "ERROR: web/dist/index.html 缺失"; exit 20; }

go version
go build -o new-api .

NEW_BIN="$REL/new-api"
[[ -x "$NEW_BIN" ]] || { echo "ERROR: 编译产物不存在"; exit 21; }

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

TARGET_DIR="$(dirname "$TARGET_BIN")"
if [[ ! -w "$TARGET_DIR" && -z "$SUDO" && "$(id -u)" -ne 0 ]]; then
  echo "ERROR: 无权限写入 $TARGET_BIN，且 sudo 不可用"
  exit 22
fi

BK="${TARGET_BIN}.bak.$(date +%Y%m%d_%H%M%S)"
if [[ -f "$TARGET_BIN" ]]; then
  run_root cp -a "$TARGET_BIN" "$BK"
fi

run_root install -m 755 "$NEW_BIN" "$TARGET_BIN"

if command -v systemctl >/dev/null 2>&1; then
  run_root systemctl restart "$SERVICE"
  run_root systemctl is-active --quiet "$SERVICE"
else
  echo "ERROR: systemctl 不存在，无法重启服务"
  exit 23
fi

sleep 1
HEALTH_OK_URL=""
for ((i=1; i<=30; i++)); do
  if curl -fsS --max-time 3 "$HEALTH" >/tmp/newapi_health.json; then
    HEALTH_OK_URL="$HEALTH"
    break
  fi
  # 某些机器仅 IPv6/localhost 可用，给一个兜底
  ALT_HEALTH="${HEALTH/127.0.0.1/localhost}"
  if [[ "$ALT_HEALTH" != "$HEALTH" ]] && curl -fsS --max-time 3 "$ALT_HEALTH" >/tmp/newapi_health.json; then
    HEALTH_OK_URL="$ALT_HEALTH"
    break
  fi
  sleep 2
done

if [[ -z "$HEALTH_OK_URL" ]]; then
  echo "ERROR: 健康检查失败，开始回滚..."
  if [[ -f "$BK" ]]; then
    run_root install -m 755 "$BK" "$TARGET_BIN"
    run_root systemctl restart "$SERVICE" || true
  fi
  exit 24
fi

echo "DEPLOY_OK commit=$COMMIT"
echo "health_url=$HEALTH_OK_URL"
cat /tmp/newapi_health.json
EOS

echo "[5/6] 端口确认（3000）"
ssh -p "$SSH_PORT" "$REMOTE" "export PATH='$REMOTE_SAFE_PATH'; ss -lntp | grep ':3000' || true"

echo "[6/6] 完成"
echo "✅ 部署成功: commit=$COMMIT, remote=$REMOTE, service=$SERVICE_NAME, port=3000"
