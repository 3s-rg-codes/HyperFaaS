#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_DIR="$ROOT/.run"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
ETCD_CONTAINER="${HYPERFAAS_ETCD_CONTAINER:-hyperfaas-etcd}"
NETWORK_NAME="${HYPERFAAS_DOCKER_NETWORK:-hyperfaas-network}"

mkdir -p "$LOG_DIR" "$PID_DIR"

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

is_pid_running() {
  local pid="$1"
  kill -0 "$pid" 2>/dev/null
}

port_open() {
  local addr="$1"
  local host="${addr%:*}"
  local port="${addr##*:}"
  (echo >/dev/tcp/"$host"/"$port") >/dev/null 2>&1
}

wait_tcp() {
  local name="$1"
  local addr="$2"
  local timeout="${3:-60}"
  local host="${addr%:*}"
  local port="${addr##*:}"
  local start_ts
  start_ts="$(date +%s)"

  while true; do
    if port_open "$addr"; then
      log "$name ready at $addr"
      return 0
    fi
    if (( $(date +%s) - start_ts >= timeout )); then
      die "$name not ready at $addr after ${timeout}s (see $LOG_DIR)"
    fi
    sleep 0.2
  done
}

require_port_free() {
  local name="$1"
  local addr="$2"
  if port_open "$addr"; then
    die "$name port $addr is already in use; run \`just stop\` or free the port manually"
  fi
}

start_component() {
  local name="$1"
  shift
  local pid_file="$PID_DIR/$name.pid"
  local log_file="$LOG_DIR/$name.log"

  if [[ -f "$pid_file" ]]; then
    local old_pid
    old_pid="$(cat "$pid_file")"
    if is_pid_running "$old_pid"; then
      log "$name already running (pid $old_pid)"
      return 0
    fi
    rm -f "$pid_file"
  fi

  log "starting $name"
  : >"$log_file"
  nohup "$@" >>"$log_file" 2>&1 &
  echo $! >"$pid_file"

  sleep 0.3
  if ! is_pid_running "$(cat "$pid_file")"; then
    die "$name exited immediately (see $log_file):$(tail -n 3 "$log_file" | tr '\n' ' ')"
  fi
}

require_cmd docker
require_cmd go
require_cmd just

if ! docker info >/dev/null 2>&1; then
  die "Docker is not available; start the Docker daemon before running \`just start\`"
fi

log "stopping any previously started cluster processes"
HYPERFAAS_QUIET_STOP=1 bash "$ROOT/scripts/stop.sh"

for addr in \
  "127.0.0.1:8081" \
  "127.0.0.1:50054" \
  "127.0.0.1:50052" \
  "127.0.0.1:50050" \
  "127.0.0.1:50053" \
  "127.0.0.1:8080" \
  "127.0.0.1:50055"; do
  require_port_free "component" "$addr"
done

if ! docker network inspect "$NETWORK_NAME" >/dev/null 2>&1; then
  log "creating docker network $NETWORK_NAME"
  docker network create "$NETWORK_NAME" >/dev/null
fi

if docker ps -a --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
  if ! docker inspect -f '{{.HostConfig.NetworkMode}}' "$ETCD_CONTAINER" 2>/dev/null | grep -qx 'host'; then
    log "recreating etcd container with host networking (gRPC-compatible)"
    docker rm -f "$ETCD_CONTAINER" >/dev/null
  elif docker ps --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
    log "etcd container already running"
  else
    log "starting existing etcd container"
    docker start "$ETCD_CONTAINER" >/dev/null
  fi
fi
if ! docker ps -a --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
  log "starting etcd container"
  docker run -d --name "$ETCD_CONTAINER" \
    --network host \
    gcr.io/etcd-development/etcd:v3.6.11 \
    /usr/local/bin/etcd \
      --advertise-client-urls http://127.0.0.1:2379 \
      --listen-client-urls http://127.0.0.1:2379 \
      --listen-peer-urls http://127.0.0.1:2380 \
      --initial-advertise-peer-urls http://127.0.0.1:2380 \
      --initial-cluster default=http://127.0.0.1:2380 \
    >/dev/null
fi

wait_tcp "etcd" "127.0.0.1:2379" 30

log "building binaries"
just build >/dev/null

log "ensuring local function images"
just build-local-function-images

start_component controlplane ./bin/controlplane -config configs/controlplane.yaml
start_component worker ./bin/worker -config configs/worker.yaml
start_component leaf ./bin/leaf -config configs/leaf.yaml
start_component ingress ./bin/ingress -config configs/ingress.yaml

wait_tcp "controlplane HTTP" "127.0.0.1:8081" 60
wait_tcp "controlplane gRPC" "127.0.0.1:50054" 60
wait_tcp "worker gRPC" "127.0.0.1:50052" 60
wait_tcp "leaf gRPC" "127.0.0.1:50050" 60
wait_tcp "ingress HTTP" "127.0.0.1:8080" 60
wait_tcp "ingress gRPC proxy" "127.0.0.1:50055" 60

for name in controlplane worker leaf ingress; do
  pid_file="$PID_DIR/$name.pid"
  if [[ ! -f "$pid_file" ]] || ! is_pid_running "$(cat "$pid_file")"; then
    die "$name is not running after startup (see $LOG_DIR/$name.log)"
  fi
done

if grep -q "bootstrap failed" "$LOG_DIR/ingress.log" 2>/dev/null; then
  die "ingress bootstrap failed (see $LOG_DIR/ingress.log)"
fi

log "cluster is up"
log "  controlplane HTTP  http://127.0.0.1:8081"
log "  controlplane gRPC  127.0.0.1:50054"
log "  ingress HTTP       http://127.0.0.1:8080"
log "  ingress gRPC proxy 127.0.0.1:50055"
log "  logs               $LOG_DIR"
log "run \`just test-dst\` in another terminal, then \`just stop\` when finished"
