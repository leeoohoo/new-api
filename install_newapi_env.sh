#!/usr/bin/env bash
set -euo pipefail

APP_USER="${APP_USER:-appuser}"
APP_DIR="${APP_DIR:-/app/new-api}"
APP_PORT="${APP_PORT:-3000}"

DB_NAME="${DB_NAME:-new_api}"
DB_USER="${DB_USER:-newapi}"
DB_PASS="${DB_PASS:-}"
REDIS_PASS="${REDIS_PASS:-}"

YUM_BASE_OPTS=(-y --disablerepo=pgdg12\* --disablerepo=pgdg13\*)

log() {
  echo "[$(date +'%F %T')] $*"
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

yum_install() {
  yum "${YUM_BASE_OPTS[@]}" install "$@"
}

detect_redis_service() {
  if systemctl list-unit-files --type=service | awk '{print $1}' | grep -qx 'redis.service'; then
    echo "redis.service"
    return 0
  fi
  if systemctl list-unit-files --type=service | awk '{print $1}' | grep -qx 'redis-server.service'; then
    echo "redis-server.service"
    return 0
  fi
  return 1
}

if [[ $EUID -ne 0 ]]; then
  die "Please run with sudo/root."
fi

if [[ -z "$DB_PASS" ]]; then
  die "DB_PASS is required, for example: sudo DB_PASS='StrongPass123' ./install_newapi_env.sh"
fi

if [[ ! "$DB_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  die "DB_USER must match ^[A-Za-z_][A-Za-z0-9_]*$"
fi

if [[ ! "$DB_NAME" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  die "DB_NAME must match ^[A-Za-z_][A-Za-z0-9_]*$"
fi

if [[ ! "$DB_PASS" =~ ^[A-Za-z0-9._~-]+$ ]]; then
  die "DB_PASS contains unsafe chars for DSN. Use only letters, numbers, dot, underscore, hyphen, tilde."
fi

log "[1/7] Install base tools"
yum_install epel-release
yum_install yum-utils curl wget git tar unzip openssl

yum-config-manager --disable 'pgdg12*' 'pgdg13*' >/dev/null 2>&1 || true

log "[2/7] Install PostgreSQL 14, Redis, Nginx"
yum_install https://download.postgresql.org/pub/repos/yum/reporpms/EL-7-x86_64/pgdg-redhat-repo-latest.noarch.rpm || true
yum-config-manager --disable 'pgdg12*' 'pgdg13*' >/dev/null 2>&1 || true
yum-config-manager --enable 'pgdg14' >/dev/null 2>&1 || true

yum "${YUM_BASE_OPTS[@]}" --enablerepo=pgdg14 install postgresql14-server postgresql14

if ! rpm -q redis >/dev/null 2>&1; then
  if ! yum "${YUM_BASE_OPTS[@]}" --enablerepo=epel install redis; then
    yum_install https://rpms.remirepo.net/enterprise/remi-release-7.rpm || true
    yum "${YUM_BASE_OPTS[@]}" --enablerepo=remi install redis || die "Failed to install redis from epel/remi"
  fi
fi

if ! rpm -q nginx >/dev/null 2>&1; then
  yum "${YUM_BASE_OPTS[@]}" --enablerepo=epel install nginx || yum_install nginx
fi

log "[3/7] Init and start services"
if [[ ! -f /var/lib/pgsql/14/data/PG_VERSION ]]; then
  /usr/pgsql-14/bin/postgresql-14-setup initdb
fi
systemctl enable postgresql-14.service
systemctl start postgresql-14.service

REDIS_SERVICE="$(detect_redis_service || true)"
if [[ -z "$REDIS_SERVICE" ]]; then
  systemctl list-unit-files | grep -i redis || true
  die "Redis service unit not found"
fi
systemctl enable "$REDIS_SERVICE"
systemctl start "$REDIS_SERVICE"

systemctl enable nginx.service || true
if ! systemctl start nginx.service; then
  log "WARN: nginx start failed (likely 80/443 in use). Continue."
fi

log "[4/7] Create DB/user"
PSQL_BIN="$(command -v psql || true)"
if [[ -z "$PSQL_BIN" && -x /usr/pgsql-14/bin/psql ]]; then
  PSQL_BIN="/usr/pgsql-14/bin/psql"
fi
[[ -n "$PSQL_BIN" ]] || die "psql not found"

# Avoid runuser warning when current working directory is not accessible to postgres.
cd /
DB_PASS_SQL=${DB_PASS//\'/\'\'}
runuser -u postgres -- "$PSQL_BIN" -tAc "SELECT 1 FROM pg_roles WHERE rolname='${DB_USER}'" | grep -q 1 || \
  runuser -u postgres -- "$PSQL_BIN" -c "CREATE USER \"${DB_USER}\" WITH PASSWORD '${DB_PASS_SQL}';"
runuser -u postgres -- "$PSQL_BIN" -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" | grep -q 1 || \
  runuser -u postgres -- "$PSQL_BIN" -c "CREATE DATABASE \"${DB_NAME}\" OWNER \"${DB_USER}\";"

log "[5/7] Harden Redis (localhost only)"
REDIS_CONF=""
if [[ -f /etc/redis.conf ]]; then
  REDIS_CONF=/etc/redis.conf
elif [[ -f /etc/redis/redis.conf ]]; then
  REDIS_CONF=/etc/redis/redis.conf
fi

if [[ -n "$REDIS_CONF" ]]; then
  sed -ri 's/^#?[[:space:]]*bind[[:space:]].*/bind 127.0.0.1 ::1/' "$REDIS_CONF"
  sed -ri 's/^#?[[:space:]]*protected-mode[[:space:]].*/protected-mode yes/' "$REDIS_CONF"
  if [[ -n "$REDIS_PASS" ]]; then
    sed -ri '/^#?[[:space:]]*requirepass[[:space:]]+/d' "$REDIS_CONF"
    printf '\nrequirepass %s\n' "$REDIS_PASS" >> "$REDIS_CONF"
  fi
  systemctl restart "$REDIS_SERVICE"
fi

log "[6/7] Create app dir and .env"
id "$APP_USER" >/dev/null 2>&1 || useradd -m -s /sbin/nologin "$APP_USER" || useradd -m "$APP_USER"
install -d -o "$APP_USER" -g "$APP_USER" -m 755 "$APP_DIR" "$APP_DIR/logs"

SESSION_SECRET="$(openssl rand -hex 32)"
CRYPTO_SECRET="$(openssl rand -hex 32)"
if [[ -n "$REDIS_PASS" ]]; then
  REDIS_CONN="redis://:${REDIS_PASS}@127.0.0.1:6379/0"
else
  REDIS_CONN="redis://127.0.0.1:6379/0"
fi

cat > "${APP_DIR}/.env" <<EOF
PORT=${APP_PORT}
SQL_DSN=postgresql://${DB_USER}:${DB_PASS}@127.0.0.1:5432/${DB_NAME}?sslmode=disable
REDIS_CONN_STRING=${REDIS_CONN}
SESSION_SECRET=${SESSION_SECRET}
CRYPTO_SECRET=${CRYPTO_SECRET}
TZ=Asia/Shanghai
ERROR_LOG_ENABLED=true
BATCH_UPDATE_ENABLED=true
EOF

chown "$APP_USER:$APP_USER" "${APP_DIR}/.env"
chmod 600 "${APP_DIR}/.env"

log "[7/7] Write systemd service"
cat > /etc/systemd/system/new-api.service <<EOF
[Unit]
Description=new-api service
After=network.target postgresql-14.service ${REDIS_SERVICE}
Wants=postgresql-14.service ${REDIS_SERVICE}

[Service]
User=${APP_USER}
WorkingDirectory=${APP_DIR}
EnvironmentFile=${APP_DIR}/.env
ExecStart=${APP_DIR}/new-api --port ${APP_PORT} --log-dir ${APP_DIR}/logs
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable new-api

echo
echo "Done: base environment installed."
echo "Next:"
echo "  1) Put binary at ${APP_DIR}/new-api"
echo "  2) chmod +x ${APP_DIR}/new-api"
echo "  3) systemctl start new-api"
echo "  4) systemctl status new-api --no-pager -l | sed -n '1,40p'"
echo "  5) curl -s http://127.0.0.1:${APP_PORT}/api/status"
