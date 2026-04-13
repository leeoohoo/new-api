#!/usr/bin/env bash
set -u

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WEB_DIR="$PROJECT_ROOT/web"

LEGACY_BACKEND_PORT="${BACKEND_PORT-}"
LEGACY_FRONTEND_PORT="${FRONTEND_PORT-}"
BACKEND_PORT="${NEW_API_BACKEND_PORT:-3000}"
FRONTEND_PORT="${NEW_API_FRONTEND_PORT:-5173}"
BACKEND_CMD="${BACKEND_CMD:-go run main.go --port ${BACKEND_PORT}}"
FRONTEND_CMD="${FRONTEND_CMD:-}"

EXIT_CODE=0
STOPPING=0
BACKEND_WRAPPER_PID=""
FRONTEND_WRAPPER_PID=""
FRONTEND_RUNNER=""

is_valid_port() {
  local port="$1"
  [[ "$port" =~ ^[0-9]+$ ]] && ((port >= 1 && port <= 65535))
}

usage() {
  cat <<EOF
Usage: ./start-dev.sh [--help]

Start backend and frontend dev servers concurrently with isolated ports.

Environment variables:
  NEW_API_BACKEND_PORT   Backend listen port (default: 3000)
  NEW_API_FRONTEND_PORT  Frontend dev server port (default: 5173)
  BACKEND_CMD    Override backend command
  FRONTEND_CMD   Override frontend command

Notes:
  - Backend always runs with PORT=BACKEND_PORT to avoid global PORT pollution.
  - Generic BACKEND_PORT/FRONTEND_PORT are ignored to avoid cross-project pollution.
  - Frontend runs with PORT removed from inherited environment (env -u PORT).
  - If FRONTEND_CMD is not set: prefer bun, fallback to npm.

Examples:
  PORT=3997 NEW_API_BACKEND_PORT=3000 NEW_API_FRONTEND_PORT=5173 ./start-dev.sh
  NEW_API_BACKEND_PORT=3100 NEW_API_FRONTEND_PORT=5175 ./start-dev.sh
  BACKEND_CMD='go run main.go --port 3200' FRONTEND_CMD='npm run dev -- --host 0.0.0.0 --port 5175' ./start-dev.sh

Quick checks for global PORT pollution:
  1) Current shell value:
     env | grep -E '^(PORT|BACKEND_PORT|FRONTEND_PORT|NEW_API_BACKEND_PORT|NEW_API_FRONTEND_PORT)='
  2) Shell startup files:
     rg -n '(^|[[:space:]])(export[[:space:]]+)?PORT=' ~/.zshrc ~/.zprofile ~/.bashrc ~/.profile 2>/dev/null
  3) Project env files:
     rg -n '^PORT=' .env .env.local web/.env web/.env.local 2>/dev/null
  4) Remove/comment polluted entries and reload shell:
     exec "\$SHELL" -l
EOF
}

if [[ -n "${LEGACY_BACKEND_PORT}" || -n "${LEGACY_FRONTEND_PORT}" ]]; then
  echo "[dev] detected generic BACKEND_PORT/FRONTEND_PORT in environment; ignoring them to avoid cross-project pollution."
  echo "[dev] use NEW_API_BACKEND_PORT and NEW_API_FRONTEND_PORT for this project."
fi

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ ! -d "$WEB_DIR" ]]; then
  echo "[dev] web directory not found: $WEB_DIR" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "[dev] 'go' is not available in PATH" >&2
  exit 1
fi

if ! is_valid_port "$BACKEND_PORT"; then
  echo "[dev] invalid BACKEND_PORT: $BACKEND_PORT" >&2
  exit 1
fi
if ! is_valid_port "$FRONTEND_PORT"; then
  echo "[dev] invalid FRONTEND_PORT: $FRONTEND_PORT" >&2
  exit 1
fi

if [[ -n "$FRONTEND_CMD" ]]; then
  FRONTEND_RUNNER="custom"
else
  if command -v bun >/dev/null 2>&1; then
    FRONTEND_CMD="bun run dev --host 0.0.0.0 --port ${FRONTEND_PORT}"
    FRONTEND_RUNNER="bun"
  elif command -v npm >/dev/null 2>&1; then
    FRONTEND_CMD="npm run dev -- --host 0.0.0.0 --port ${FRONTEND_PORT}"
    FRONTEND_RUNNER="npm"
  else
    echo "[dev] neither 'bun' nor 'npm' is available in PATH" >&2
    exit 1
  fi
fi

start_service() {
  local name="$1"
  local workdir="$2"
  local cmd="$3"
  local mode="$4"

  (
    set +e
    cd "$workdir" || exit 1

    case "$mode" in
      backend)
        env PORT="$BACKEND_PORT" bash -lc "$cmd" \
          > >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }') \
          2> >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }' >&2) &
        ;;
      frontend)
        env -u PORT FRONTEND_PORT="$FRONTEND_PORT" bash -lc "$cmd" \
          > >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }') \
          2> >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }' >&2) &
        ;;
      *)
        bash -lc "$cmd" \
          > >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }') \
          2> >(awk -v prefix="[$name] " '{ print prefix $0; fflush(); }' >&2) &
        ;;
    esac

    local child_pid=$!
    trap 'kill "$child_pid" 2>/dev/null || true; wait "$child_pid" 2>/dev/null || true' INT TERM EXIT
    wait "$child_pid"
  ) &
}

cleanup() {
  if [[ "$STOPPING" -eq 1 ]]; then
    return
  fi
  STOPPING=1

  echo "[dev] stopping child processes..."

  if [[ -n "$BACKEND_WRAPPER_PID" ]] && kill -0 "$BACKEND_WRAPPER_PID" 2>/dev/null; then
    kill "$BACKEND_WRAPPER_PID" 2>/dev/null || true
  fi
  if [[ -n "$FRONTEND_WRAPPER_PID" ]] && kill -0 "$FRONTEND_WRAPPER_PID" 2>/dev/null; then
    kill "$FRONTEND_WRAPPER_PID" 2>/dev/null || true
  fi

  if [[ -n "$BACKEND_WRAPPER_PID" ]]; then
    wait "$BACKEND_WRAPPER_PID" 2>/dev/null || true
  fi
  if [[ -n "$FRONTEND_WRAPPER_PID" ]]; then
    wait "$FRONTEND_WRAPPER_PID" 2>/dev/null || true
  fi

  echo "[dev] all child processes stopped"
}

trap 'EXIT_CODE=130; cleanup' INT
trap 'EXIT_CODE=143; cleanup' TERM
trap 'cleanup' EXIT

echo "[dev] project root: $PROJECT_ROOT"
echo "[dev] backend port: $BACKEND_PORT"
echo "[dev] frontend port: $FRONTEND_PORT"
echo "[dev] backend cmd: $BACKEND_CMD"
echo "[dev] frontend cmd: $FRONTEND_CMD ($FRONTEND_RUNNER)"
echo "[dev] backend env: PORT=$BACKEND_PORT"
echo "[dev] frontend env: PORT is unset"

start_service "backend" "$PROJECT_ROOT" "$BACKEND_CMD" "backend"
BACKEND_WRAPPER_PID="$!"
start_service "frontend" "$WEB_DIR" "$FRONTEND_CMD" "frontend"
FRONTEND_WRAPPER_PID="$!"

echo "[dev] backend wrapper pid: $BACKEND_WRAPPER_PID"
echo "[dev] frontend wrapper pid: $FRONTEND_WRAPPER_PID"

while true; do
  if [[ "$STOPPING" -eq 1 ]]; then
    break
  fi

  if ! kill -0 "$BACKEND_WRAPPER_PID" 2>/dev/null; then
    if [[ "$STOPPING" -eq 0 ]]; then
      echo "[dev] backend process exited"
      EXIT_CODE=1
    fi
    break
  fi

  if ! kill -0 "$FRONTEND_WRAPPER_PID" 2>/dev/null; then
    if [[ "$STOPPING" -eq 0 ]]; then
      echo "[dev] frontend process exited"
      EXIT_CODE=1
    fi
    break
  fi

  sleep 1
done

exit "$EXIT_CODE"
