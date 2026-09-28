#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_DIR="$ROOT/.run"
PID_DIR="$RUN_DIR/pids"
ETCD_CONTAINER="${HYPERFAAS_ETCD_CONTAINER:-hyperfaas-etcd}"

log() { printf '==> %s\n' "$*"; }

stop_component() {
  local name="$1"
  local pid_file="$PID_DIR/$name.pid"
  if [[ ! -f "$pid_file" ]]; then
    return 0
  fi
  local pid
  pid="$(cat "$pid_file")"
  if kill -0 "$pid" 2>/dev/null; then
    [[ "${HYPERFAAS_QUIET_STOP:-}" == "1" ]] || log "stopping $name (pid $pid)"
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 50); do
      if ! kill -0 "$pid" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
      [[ "${HYPERFAAS_QUIET_STOP:-}" == "1" ]] || log "force stopping $name (pid $pid)"
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$pid_file"
}

mkdir -p "$PID_DIR"

# Prefer reverse dependency order when names are known, then sweep any remaining pids
# (covers leaf-N / worker-lN-wM from the big fake cluster).
KNOWN_ORDER=(ingress)
if compgen -G "$PID_DIR/leaf-*.pid" >/dev/null; then
  # shellcheck disable=SC2012
  mapfile -t leaf_pids < <(ls -1 "$PID_DIR"/leaf-*.pid 2>/dev/null | sort -r)
  for pf in "${leaf_pids[@]}"; do
    KNOWN_ORDER+=("$(basename "$pf" .pid)")
  done
else
  KNOWN_ORDER+=(leaf)
fi
if compgen -G "$PID_DIR/worker*.pid" >/dev/null; then
  # shellcheck disable=SC2012
  mapfile -t worker_pids < <(ls -1 "$PID_DIR"/worker*.pid 2>/dev/null | sort -r)
  for pf in "${worker_pids[@]}"; do
    KNOWN_ORDER+=("$(basename "$pf" .pid)")
  done
else
  KNOWN_ORDER+=(worker)
fi
KNOWN_ORDER+=(controlplane)

declare -A stopped=()
for name in "${KNOWN_ORDER[@]}"; do
  stop_component "$name"
  stopped["$name"]=1
done

# Sweep any leftover pid files (unknown names).
shopt -s nullglob
for pf in "$PID_DIR"/*.pid; do
  name="$(basename "$pf" .pid)"
  if [[ -z "${stopped[$name]:-}" ]]; then
    stop_component "$name"
  fi
done
shopt -u nullglob

if docker ps -a --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
  if docker ps --format '{{.Names}}' | grep -qx "$ETCD_CONTAINER"; then
    [[ "${HYPERFAAS_QUIET_STOP:-}" == "1" ]] || log "stopping etcd container"
    docker stop "$ETCD_CONTAINER" >/dev/null
  fi
fi

# Drop stale fake-cluster env so a later `just test-dst` does not point at a dead topology.
if [[ -f "$RUN_DIR/fake-cluster/env.sh" ]]; then
  rm -f "$RUN_DIR/fake-cluster/env.sh"
fi

[[ "${HYPERFAAS_QUIET_STOP:-}" == "1" ]] || log "cluster stopped"
