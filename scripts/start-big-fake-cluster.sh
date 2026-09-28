#!/usr/bin/env bash
# Start a large local HyperFaaS cluster with in-process fake worker runtimes.
# Default topology: 4 leaves × 10 workers (40 workers total). No Docker function sandboxes.
#
# Usage:
#   just start-big-fake   # or: bash scripts/start-big-fake-cluster.sh
#   just test-dst         # uses .run/fake-cluster/env.sh when present
#   just stop
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_DIR="$ROOT/.run"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
CFG_DIR="$RUN_DIR/fake-cluster/configs"
ENV_FILE="$RUN_DIR/fake-cluster/env.sh"
ETCD_CONTAINER="${HYPERFAAS_ETCD_CONTAINER:-hyperfaas-etcd}"

NUM_LEAVES="${HYPERFAAS_FAKE_LEAVES:-4}"
WORKERS_PER_LEAF="${HYPERFAAS_FAKE_WORKERS_PER_LEAF:-10}"
# Default off; set HYPERFAAS_FAKE_SIMULATE_START_LATENCY=1 to enable heavy-tailed CreateSandbox delays.
SIMULATE_START_LATENCY="${HYPERFAAS_FAKE_SIMULATE_START_LATENCY:-0}"
LEAF_ROUTING_POLICY="${HYPERFAAS_FAKE_LEAF_ROUTING_POLICY:-available-capacity}"
# Ingress-to-leaf transport arm: h2c (default) | http1 | http1-nokeepalive.
LEAF_TRANSPORT="${HYPERFAAS_FAKE_LEAF_TRANSPORT:-h2c}"
BOUNDED_LOADS_BOUND="${HYPERFAAS_FAKE_BOUNDED_LOADS_BOUND:-1.0}"
BOUNDED_LOADS_MAX_CHAIN_LEN="${HYPERFAAS_FAKE_BOUNDED_LOADS_MAX_CHAIN_LEN:-3}"
UNLIMITED_CONCURRENCY_TARGET="${HYPERFAAS_FAKE_UNLIMITED_CONCURRENCY_TARGET:-100}"
TARGET_UTILIZATION="${HYPERFAAS_FAKE_TARGET_UTILIZATION:-1.0}"
SIMULATE_START_LATENCY_YAML="false"
if [[ "$SIMULATE_START_LATENCY" == "1" || "$SIMULATE_START_LATENCY" == "true" || "$SIMULATE_START_LATENCY" == "TRUE" ]]; then
  SIMULATE_START_LATENCY_YAML="true"
fi

mkdir -p "$LOG_DIR" "$PID_DIR" "$CFG_DIR"

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
    sleep 0.1
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

  sleep 0.05
  if ! is_pid_running "$(cat "$pid_file")"; then
    die "$name exited immediately (see $log_file):$(tail -n 5 "$log_file" | tr '\n' ' ')"
  fi
}

# Ports:
#   controlplane: 8081 / 50054 (unchanged)
#   ingress:      8080 / 50055 (unchanged)
#   leaf L:       invocation 2100L, grpc-proxy 2200L, http 2300L  (L=1..N)
#   worker global index G=1..N*W: 20000+G
# NOTE: keep these below the kernel ephemeral range (typically 32768-60999).
# The previous 51000+/52000+ allocation collided with ephemeral client ports at
# large topologies and made `start-big-fake` fail intermittently on bind.
leaf_invocation_port() { echo $((21000 + $1)); }
leaf_proxy_port() { echo $((22000 + $1)); }
leaf_http_port() { echo $((23000 + $1)); }
worker_port() { echo $((20000 + $1)); }

require_cmd docker
require_cmd go
require_cmd just

if ! docker info >/dev/null 2>&1; then
  die "Docker is not available; start the Docker daemon (needed for etcd only)"
fi

if ! [[ "$NUM_LEAVES" =~ ^[1-9][0-9]*$ ]]; then
  die "HYPERFAAS_FAKE_LEAVES must be a positive integer, got $NUM_LEAVES"
fi
if ! [[ "$WORKERS_PER_LEAF" =~ ^[1-9][0-9]*$ ]]; then
  die "HYPERFAAS_FAKE_WORKERS_PER_LEAF must be a positive integer, got $WORKERS_PER_LEAF"
fi

TOTAL_WORKERS=$((NUM_LEAVES * WORKERS_PER_LEAF))
log "topology: ${NUM_LEAVES} leaves × ${WORKERS_PER_LEAF} workers = ${TOTAL_WORKERS} fake workers"
log "simulate_sandbox_start_latency=${SIMULATE_START_LATENCY_YAML}"

log "stopping any previously started cluster processes"
HYPERFAAS_QUIET_STOP=1 bash "$ROOT/scripts/stop.sh"

PORTS_TO_CHECK=("127.0.0.1:8081" "127.0.0.1:50054" "127.0.0.1:8080" "127.0.0.1:50055")
for ((l=1; l<=NUM_LEAVES; l++)); do
  PORTS_TO_CHECK+=("127.0.0.1:$(leaf_invocation_port "$l")")
  PORTS_TO_CHECK+=("127.0.0.1:$(leaf_proxy_port "$l")")
  PORTS_TO_CHECK+=("127.0.0.1:$(leaf_http_port "$l")")
done
for ((g=1; g<=TOTAL_WORKERS; g++)); do
  PORTS_TO_CHECK+=("127.0.0.1:$(worker_port "$g")")
done
for addr in "${PORTS_TO_CHECK[@]}"; do
  require_port_free "component" "$addr"
done

if docker ps -a --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
  # Recreate when the existing container was started without host networking;
  # published ports alone break etcd gRPC clients on the host with recent etcd builds.
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

log "writing generated configs under $CFG_DIR"
rm -rf "$CFG_DIR"
mkdir -p "$CFG_DIR"

cp "$ROOT/configs/controlplane.yaml" "$CFG_DIR/controlplane.yaml"

# --- workers + leaf configs ---
WORKER_ADDRS_CSV=()
WORKER_LEAF_IDS_CSV=()
LEAF_ADDRS_CSV=()
INGRESS_LEAVES_YAML=""

global_worker=0
for ((l=1; l<=NUM_LEAVES; l++)); do
  inv_port="$(leaf_invocation_port "$l")"
  proxy_port="$(leaf_proxy_port "$l")"
  http_port="$(leaf_http_port "$l")"
  leaf_workers_yaml=""
  for ((w=1; w<=WORKERS_PER_LEAF; w++)); do
    global_worker=$((global_worker + 1))
    wport="$(worker_port "$global_worker")"
    wname="worker-l${l}-w${w}"
    cat >"$CFG_DIR/${wname}.yaml" <<EOF
node_id: ${wname}

logging:
  level: info
  format: text

server:
  listen_address: "127.0.0.1:${wport}"

runtime:
  type: fake
  fake:
    simulate_sandbox_start_latency: ${SIMULATE_START_LATENCY_YAML}

stats:
  update_buffer_size: 10000
  metrics_interval: 1s
  budget_cpu: 64
  budget_memory: 68719476736
EOF
    leaf_workers_yaml+="  - address: \"127.0.0.1:${wport}\""$'\n'
    WORKER_ADDRS_CSV+=("127.0.0.1:${wport}")
    WORKER_LEAF_IDS_CSV+=("${l}")
  done

  cat >"$CFG_DIR/leaf-${l}.yaml" <<EOF
leaf_id: ${l}
node_id: leaf-${l}

grpc_proxy:
  listen_address: "127.0.0.1:${proxy_port}"

logging:
  level: info
  format: text

server:
  listen_address: "127.0.0.1:${inv_port}"
  pprof_address: "127.0.0.1:$((6070 + l))"

http_invocation_address: "127.0.0.1:${http_port}"

workers:
${leaf_workers_yaml}
dataplane:
  scale_to_zero_after: 10s
  max_instances_per_worker: 0
  start_tokens_per_worker: 16
  max_starts_per_reconcile: 0
  dirigent_strict_admission: false
  dial_timeout: 5s
  start_timeout: 60s
  stop_timeout: 10s
  status_backoff: 2s
  routing_state_heartbeat_interval: 500ms
  http_max_idle_conns: 4096
  http_max_idle_conns_per_host: 200
  http_max_conns_per_host: 200
  http_idle_conn_timeout: 90s
  containerized: false

autoscaling:
  reconcile_interval: 2s
  unlimited_concurrency_target: ${UNLIMITED_CONCURRENCY_TARGET}
  target_utilization: ${TARGET_UTILIZATION}

controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s
EOF

  INGRESS_LEAVES_YAML+="  - id: ${l}"$'\n'
  INGRESS_LEAVES_YAML+="    invocation_address: \"127.0.0.1:${inv_port}\""$'\n'
  INGRESS_LEAVES_YAML+="    http_invocation_address: \"127.0.0.1:${http_port}\""$'\n'
  INGRESS_LEAVES_YAML+="    grpc_proxy_address: \"127.0.0.1:${proxy_port}\""$'\n'
  LEAF_ADDRS_CSV+=("127.0.0.1:${inv_port}")
done

cat >"$CFG_DIR/ingress.yaml" <<EOF
node_id: ingress-1

logging:
  level: info
  format: text

server:
  http_address: "0.0.0.0:8080"
  grpc_proxy_address: "0.0.0.0:50055"
  pprof_address: "127.0.0.1:6061"

controlplane:
  address: "127.0.0.1:50054"
  dial_timeout: 5s

leaves:
${INGRESS_LEAVES_YAML}
routing:
  # The routing policy is published through the control-plane PlatformConfig
  # document below, not baked into YAML.
  leaf_transport: ${LEAF_TRANSPORT}
  state_sync_interval: 5s
EOF

# Join worker addresses for DST harness
IFS=','; WORKER_CSV="${WORKER_ADDRS_CSV[*]}"; WORKER_LEAF_CSV="${WORKER_LEAF_IDS_CSV[*]}"; LEAF_CSV="${LEAF_ADDRS_CSV[*]}"; unset IFS
cat >"$ENV_FILE" <<EOF
# Auto-generated by start-big-fake-cluster.sh — sourced by \`just test-dst\` when present.
export HYPERFAAS_CP_HTTP=127.0.0.1:8081
export HYPERFAAS_CP_GRPC=127.0.0.1:50054
export HYPERFAAS_INGRESS_HTTP=127.0.0.1:8080
export HYPERFAAS_INGRESS_GRPC_PROXY=127.0.0.1:50055
export HYPERFAAS_WORKER_GRPC=${WORKER_CSV}
export HYPERFAAS_FAKE_WORKER_LEAF_IDS=${WORKER_LEAF_CSV}
export HYPERFAAS_LEAF_GRPC=127.0.0.1:$(leaf_invocation_port 1)
export HYPERFAAS_LEAF_GRPCs=${LEAF_CSV}
export HYPERFAAS_FAKE_CLUSTER=1
export HYPERFAAS_FAKE_LEAVES=${NUM_LEAVES}
export HYPERFAAS_FAKE_WORKERS_PER_LEAF=${WORKERS_PER_LEAF}
export HYPERFAAS_FAKE_LEAF_ROUTING_POLICY=${LEAF_ROUTING_POLICY}
export HYPERFAAS_FAKE_LEAF_TRANSPORT=${LEAF_TRANSPORT}
EOF

# Publish the desired routing policy through the control-plane PlatformConfig
# document. Static YAML keeps topology only; policy lives in the document.
routing_policy_json() {
  case "$LEAF_ROUTING_POLICY" in
    available-capacity) echo '{"availableCapacity":{}}' ;;
    consistent-hashing) echo '{"consistentHashing":{}}' ;;
    round-robin) echo '{"roundRobin":{}}' ;;
    random) echo '{"random":{}}' ;;
    least-loaded) echo '{"leastLoaded":{}}' ;;
    bounded-loads) echo "{\"boundedLoads\":{\"bound\":${BOUNDED_LOADS_BOUND},\"maxChainLen\":${BOUNDED_LOADS_MAX_CHAIN_LEN}}}" ;;
    *) die "unknown HYPERFAAS_FAKE_LEAF_ROUTING_POLICY: $LEAF_ROUTING_POLICY" ;;
  esac
}

publish_platform_policy() {
  local routing payload
  routing="$(routing_policy_json)"
  payload="{\"config\":{\"routing\":${routing},\"placement\":{\"balancedRoundRobin\":{}}},\"expectedVersion\":0}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -X PUT -H 'Content-Type: application/json' -d "$payload" \
      "http://127.0.0.1:8081/v1/platform/config" >/dev/null \
      || die "failed to publish platform policy (see $LOG_DIR/controlplane.log)"
    log "published platform policy (routing=${LEAF_ROUTING_POLICY})"
  else
    log "curl not found; leaving the control-plane default policy active"
  fi
}

# --- start processes ---
start_component controlplane ./bin/controlplane -config "$CFG_DIR/controlplane.yaml"
wait_tcp "controlplane HTTP" "127.0.0.1:8081" 60
wait_tcp "controlplane gRPC" "127.0.0.1:50054" 60

global_worker=0
for ((l=1; l<=NUM_LEAVES; l++)); do
  for ((w=1; w<=WORKERS_PER_LEAF; w++)); do
    global_worker=$((global_worker + 1))
    wname="worker-l${l}-w${w}"
    start_component "$wname" ./bin/worker -config "$CFG_DIR/${wname}.yaml"
  done
done

# Wait for first and last worker as a cheap readiness sample, then a short settle.
wait_tcp "worker-l1-w1" "127.0.0.1:$(worker_port 1)" 60
wait_tcp "last-worker" "127.0.0.1:$(worker_port "$TOTAL_WORKERS")" 60

for ((l=1; l<=NUM_LEAVES; l++)); do
  start_component "leaf-${l}" ./bin/leaf -config "$CFG_DIR/leaf-${l}.yaml"
done
for ((l=1; l<=NUM_LEAVES; l++)); do
  wait_tcp "leaf-${l}" "127.0.0.1:$(leaf_invocation_port "$l")" 60
  wait_tcp "leaf-${l} HTTP invocation" "127.0.0.1:$(leaf_http_port "$l")" 60
done

start_component ingress ./bin/ingress -config "$CFG_DIR/ingress.yaml"
wait_tcp "ingress HTTP" "127.0.0.1:8080" 60
wait_tcp "ingress gRPC proxy" "127.0.0.1:50055" 60

publish_platform_policy

if grep -q "bootstrap failed" "$LOG_DIR/ingress.log" 2>/dev/null; then
  die "ingress bootstrap failed (see $LOG_DIR/ingress.log)"
fi

log "fake cluster is up (${NUM_LEAVES} leaves, ${TOTAL_WORKERS} workers)"
log "  controlplane HTTP  http://127.0.0.1:8081"
log "  ingress HTTP       http://127.0.0.1:8080"
log "  env file           $ENV_FILE"
log "run \`just test-dst\` (auto-sources env file), then \`just stop\` when finished"
