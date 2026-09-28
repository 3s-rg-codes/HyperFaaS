#!/usr/bin/env bash
# Local whole-platform load gate for HyperFaaS.
#
# Purpose: fast, token-cheap feedback before any GCE run. It drives the REAL
# load generator through the REAL local platform (generator -> ingress -> leaf
# -> in-process fake worker) and prints one compact metrics block.
#
# Commands (see also the `just` recipes):
#   just load-local                # default: sustained quick run
#   just load-local bursty         # sustained | bursty | skewed | increasing
#   just load-local skewed
#   LOAD_LOCAL_RPS=600 just load-local sustained   # deeper run
#   LOAD_LOCAL_WRITE_BASELINE=1 just load-local    # refresh checked-in baseline
#   just load-local-stop           # stop the local cluster
#
# Direct script use:  bash scripts/load-local.sh [scenario]
#
# It reuses a running local cluster when the topology and the platform source
# fingerprint (HEAD + uncommitted platform diff) are unchanged, and rebuilds/
# restarts otherwise. Results land in .run/load-local/latest.{json,csv} and are
# compared against scripts/load-local-baseline.json when present.
#
# Caveat: fake workers emulate sandbox execution (echo), so sandbox service time
# is synthetic. Control-plane, routing, admission, autoscaling and proxy cost is
# real, but generator and platform share this laptop's CPU.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$ROOT/.." && pwd)"

RUN_DIR="$ROOT/.run"
STATE_DIR="$RUN_DIR/load-local"
PID_DIR="$RUN_DIR/pids"
ENV_FILE="$RUN_DIR/fake-cluster/env.sh"
BIN_DIR="$STATE_DIR/bin"
SETUP_BIN="$BIN_DIR/ideal-arch-setup"
GEN_BIN="$BIN_DIR/generator"
STATE_FILE="$STATE_DIR/state.txt"
DEPLOYED_FILE="$STATE_DIR/deployed.json"
CLUSTER_MARKER="$STATE_DIR/cluster.json"
GEN_LOG="$STATE_DIR/generator.log"
GEN_CSV="$STATE_DIR/latest.csv"
CPU_JSON="$STATE_DIR/cpu.json"
LATEST_JSON="$STATE_DIR/latest.json"
REPORT="$ROOT/scripts/load-local-report.py"
BASELINE="${LOAD_LOCAL_BASELINE:-$ROOT/scripts/load-local-baseline.json}"

SETUP_DIR="$REPO_ROOT/bench/preliminary"
GEN_DIR="$REPO_ROOT/bench/generator"

SCENARIO="${1:-${LOAD_LOCAL_SCENARIO:-sustained}}"
LEAVES="${LOAD_LOCAL_LEAVES:-2}"
WORKERS="${LOAD_LOCAL_WORKERS_PER_LEAF:-4}"
FUNCTIONS="${LOAD_LOCAL_FUNCTIONS:-4}"
SCALE_TO_ZERO="${LOAD_LOCAL_SCALE_TO_ZERO:-10s}"
SEED="${LOAD_LOCAL_SEED:-1}"
SIM_START_LATENCY="${HYPERFAAS_FAKE_SIMULATE_START_LATENCY:-0}"
LEAF_TRANSPORT="${HYPERFAAS_FAKE_LEAF_TRANSPORT:-h2c}"

# Quick-run load defaults; override any of these to stress deeper.
WARMUP_RPS="${LOAD_LOCAL_WARMUP_RPS:-50}"
WARMUP_SECONDS="${LOAD_LOCAL_WARMUP_SECONDS:-5}"
RPS="${LOAD_LOCAL_RPS:-300}"
DURATION_SECONDS="${LOAD_LOCAL_DURATION_SECONDS:-20}"
BURST_DURATION_SECONDS="${LOAD_LOCAL_BURST_DURATION_SECONDS:-30}"
BURST_QUIET_MIN_RPS="${LOAD_LOCAL_BURST_QUIET_MIN_RPS:-2}"
BURST_QUIET_MAX_RPS="${LOAD_LOCAL_BURST_QUIET_MAX_RPS:-8}"
BURST_PEAK_MIN_RPS="${LOAD_LOCAL_BURST_PEAK_MIN_RPS:-300}"
BURST_PEAK_MAX_RPS="${LOAD_LOCAL_BURST_PEAK_MAX_RPS:-500}"
SKEWED_HOT_FRACTION="${LOAD_LOCAL_SKEWED_HOT_FRACTION:-0.8}"

PLATFORM_PATHS=(
  hyperfaas-ideal-arch/pkg
  hyperfaas-ideal-arch/cmd
  hyperfaas-ideal-arch/proto
  hyperfaas-ideal-arch/functions
  hyperfaas-ideal-arch/go.mod
  hyperfaas-ideal-arch/go.sum
)

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

GEN_PID=""
cleanup() {
  if [[ -n "$GEN_PID" ]] && kill -0 "$GEN_PID" 2>/dev/null; then
    kill "$GEN_PID" 2>/dev/null || true
  fi
}
on_signal() { cleanup; exit 130; }
trap cleanup EXIT
trap on_signal INT TERM

port_open() { (echo >/dev/tcp/127.0.0.1/"$1") >/dev/null 2>&1; }
cluster_running() { [[ -f "$ENV_FILE" ]] && port_open 8081 && port_open 8080; }

CLK="$(getconf CLK_TCK 2>/dev/null || echo 100)"
NPROC="$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 1)"

COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
DIRTY=0
if [[ -n "$(git -C "$REPO_ROOT" status --porcelain -- "${PLATFORM_PATHS[@]}" 2>/dev/null)" ]]; then
  DIRTY=1
fi
SRC_FP="$({
  git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown
  git -C "$REPO_ROOT" status --porcelain -- "${PLATFORM_PATHS[@]}" 2>/dev/null || true
  git -C "$REPO_ROOT" diff HEAD -- "${PLATFORM_PATHS[@]}" 2>/dev/null || true
} | sha1sum | cut -c1-12)"

TARGETS="$LEAVES""Lx""$WORKERS""W/""$FUNCTIONS""fn"

mkdir -p "$STATE_DIR" "$BIN_DIR"

case "$SCENARIO" in
  sustained|bursty|skewed|increasing) ;;
  *) die "unknown scenario '$SCENARIO' (want sustained | bursty | skewed | increasing)" ;;
esac
if [[ ! "$LEAVES" =~ ^[1-9][0-9]*$ || ! "$WORKERS" =~ ^[1-9][0-9]*$ ]]; then
  die "LOAD_LOCAL_LEAVES / LOAD_LOCAL_WORKERS_PER_LEAF must be positive integers"
fi

# ---- cluster lifecycle -------------------------------------------------------

build_tools() {
  log "building generator and deploy tool"
  ( cd "$GEN_DIR" && go build -o "$GEN_BIN" . ) || die "go build bench/generator failed"
  ( cd "$SETUP_DIR" && go build -o "$SETUP_BIN" ./cmd/ideal-arch-setup ) || die "go build ideal-arch-setup failed"
}

write_marker() {
  printf '{"fingerprint":"%s","commit":"%s","dirty":%s,"leaves":%s,"workers_per_leaf":%s,"leaf_transport":"%s"}\n' \
    "$SRC_FP" "$COMMIT" "$DIRTY" "$LEAVES" "$WORKERS" "$LEAF_TRANSPORT" >"$CLUSTER_MARKER"
}

teardown_state() {
  local state="$1"
  [[ -f "$state" ]] || return 0
  "$SETUP_BIN" -cp-http "$HYPERFAAS_CP_HTTP" -state "$state" teardown >/dev/null 2>&1 || true
}

start_cluster() {
  log "starting local cluster (${LEAVES} leaves x ${WORKERS} workers)"
  # start-big-fake-cluster.sh stops any previous cluster and rebuilds binaries.
  HYPERFAAS_FAKE_LEAVES="$LEAVES" \
    HYPERFAAS_FAKE_WORKERS_PER_LEAF="$WORKERS" \
    HYPERFAAS_FAKE_SIMULATE_START_LATENCY="$SIM_START_LATENCY" \
    HYPERFAAS_FAKE_LEAF_TRANSPORT="$LEAF_TRANSPORT" \
    bash "$ROOT/scripts/start-big-fake-cluster.sh"
}

restart_cluster() {
  local prev="$STATE_DIR/state.prev.txt"
  if [[ -f "$STATE_FILE" ]]; then
    mv -f "$STATE_FILE" "$prev"
  fi
  if cluster_running; then
    # Old control plane still up: delete its functions before we drop it.
    teardown_state "$prev"
  fi
  start_cluster
  [[ -f "$ENV_FILE" ]] || die "cluster started but $ENV_FILE is missing"
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  if [[ -f "$prev" ]]; then
    teardown_state "$prev"
    rm -f "$prev"
  fi
  write_marker
  rm -f "$DEPLOYED_FILE"
}

ensure_cluster() {
  if cluster_running; then
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    local fp="" leaves="" workers="" transport=""
    if [[ -f "$CLUSTER_MARKER" ]]; then
      fp="$(sed -n 's/.*"fingerprint":"\([^"]*\)".*/\1/p' "$CLUSTER_MARKER")"
      leaves="$(sed -n 's/.*"leaves":\([0-9]*\).*/\1/p' "$CLUSTER_MARKER")"
      workers="$(sed -n 's/.*"workers_per_leaf":\([0-9]*\).*/\1/p' "$CLUSTER_MARKER")"
      transport="$(sed -n 's/.*"leaf_transport":"\([^"]*\)".*/\1/p' "$CLUSTER_MARKER")"
    fi
    if [[ "$fp" == "$SRC_FP" && "$leaves" == "$LEAVES" && "$workers" == "$WORKERS" && "$transport" == "$LEAF_TRANSPORT" ]]; then
      log "reusing running cluster (${leaves}L x ${workers}W, transport=${transport})"
      return 0
    fi
    log "cluster topology/source/transport changed (running ${leaves:-?}L x ${workers:-?}W transport=${transport:-?}, want ${LEAVES}L x ${WORKERS}W transport=${LEAF_TRANSPORT}); restarting"
    restart_cluster
    return 0
  fi
  if [[ -f "$ENV_FILE" ]]; then
    log "stale fake-cluster env at $ENV_FILE but ports are down; restarting"
  else
    log "no running local cluster; starting one"
  fi
  [[ -f "$STATE_FILE" ]] && mv -f "$STATE_FILE" "$STATE_DIR/state.prev.txt" || true
  start_cluster
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  if [[ -f "$STATE_DIR/state.prev.txt" ]]; then
    teardown_state "$STATE_DIR/state.prev.txt"
    rm -f "$STATE_DIR/state.prev.txt"
  fi
  write_marker
  rm -f "$DEPLOYED_FILE"
}

# ---- deploy ------------------------------------------------------------------

first_target() {
  local line
  line="$(grep -m1 '^FUNCTION_TARGETS=' "$STATE_FILE" || true)"
  line="${line#FUNCTION_TARGETS=}"
  printf '%s' "${line%%,*}"
}

probe_ok() {
  local target
  target="$(first_target)"
  [[ -n "$target" ]] || return 1
  timeout 20 "$GEN_BIN" \
    -platform hyperfaas-ideal-arch -transport ideal-arch \
    -url "http://${HYPERFAAS_INGRESS_HTTP}/invoke" \
    -targets "$target" -probe >/dev/null 2>&1
}

deploy_functions() {
  log "deploying $FUNCTIONS echo function(s) via ideal-arch-setup"
  teardown_state "$STATE_FILE"
  rm -f "$STATE_FILE"
  "$SETUP_BIN" \
    -cp-http "$HYPERFAAS_CP_HTTP" \
    -state "$STATE_FILE" \
    -profile concurrency-max \
    -scenario skewed \
    -function-count "$FUNCTIONS" \
    -user-count 1 \
    -placement-policy balanced-round-robin \
    -echo-image echo-http \
    -sleep-image sleep-http \
    -memory-image memory-http \
    -fib-image fib-http \
    -function-kind echo \
    -worker-runtime fake \
    -max-instances 0 -max-concurrency 0 -target-concurrency 0 \
    -cpu-units 0 -max-queue-depth 0 \
    -scale-to-zero-idle-timeout "$SCALE_TO_ZERO" \
    deploy \
    || die "function deploy failed (is ${HYPERFAAS_CP_HTTP} healthy?)"
  [[ -f "$STATE_FILE" ]] || die "deploy did not write $STATE_FILE"
  printf '%s\n' "$SRC_FP:$LEAVES:$WORKERS:$FUNCTIONS:$SCALE_TO_ZERO" >"$DEPLOYED_FILE"
}

ensure_functions() {
  if [[ -f "$STATE_FILE" && -f "$DEPLOYED_FILE" ]]; then
    if [[ "$(cat "$DEPLOYED_FILE")" == "$SRC_FP:$LEAVES:$WORKERS:$FUNCTIONS:$SCALE_TO_ZERO" ]] && probe_ok; then
      log "reusing deployed functions ($STATE_FILE)"
      return 0
    fi
    log "deployed functions are stale or unreachable; redeploying"
  fi
  deploy_functions
}

# ---- run ---------------------------------------------------------------------

role_ticks() {
  local pat="$1" total=0 f pid
  for f in "$PID_DIR"/$pat.pid; do
    [[ -e "$f" ]] || continue
    pid="$(cat "$f" 2>/dev/null || true)"
    [[ -n "$pid" && -r "/proc/$pid/stat" ]] || continue
    total=$(( total + $(awk '{print $14+$15}' "/proc/$pid/stat" 2>/dev/null || echo 0) ))
  done
  printf '%s' "$total"
}

snapshot_ticks() {
  printf '%s %s %s %s' \
    "$(role_ticks controlplane)" \
    "$(role_ticks ingress)" \
    "$(role_ticks 'leaf-*')" \
    "$(role_ticks 'worker-*')"
}

# role_rss_kb sums resident set size (VmRSS) of every process matching a role.
role_rss_kb() {
  local pat="$1" total=0 f pid rss
  for f in "$PID_DIR"/$pat.pid; do
    [[ -e "$f" ]] || continue
    pid="$(cat "$f" 2>/dev/null || true)"
    [[ -n "$pid" && -r "/proc/$pid/status" ]] || continue
    rss="$(awk '/^VmRSS:/{print $2}' "/proc/$pid/status" 2>/dev/null || echo 0)"
    total=$(( total + rss ))
  done
  printf '%s' "$total"
}

# snapshot_rss_kb reports <controlplane> <ingress> <leaf-sum> <worker-sum> in KiB.
snapshot_rss_kb() {
  printf '%s %s %s %s' \
    "$(role_rss_kb controlplane)" \
    "$(role_rss_kb ingress)" \
    "$(role_rss_kb 'leaf-*')" \
    "$(role_rss_kb 'worker-*')"
}

cpu_sec() { awk -v a="$1" -v b="$2" -v c="$CLK" 'BEGIN { printf "%.3f", (b - a) / c }'; }

proc_ticks() {
  local pid="$1"
  [[ -n "$pid" && -r "/proc/$pid/stat" ]] || return 0
  awk '{print $14+$15}' "/proc/$pid/stat" 2>/dev/null || true
}

proc_alive() {
  local pid="$1" state
  [[ -n "$pid" && -r "/proc/$pid/stat" ]] || return 1
  state="$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null || true)"
  [[ "$state" != "Z" ]]
}

load_key() {
  if [[ "$SCENARIO" == bursty ]]; then
    printf 'burst=%s-%s/%s-%s,dur=%ss' \
      "$BURST_QUIET_MIN_RPS" "$BURST_QUIET_MAX_RPS" \
      "$BURST_PEAK_MIN_RPS" "$BURST_PEAK_MAX_RPS" "$BURST_DURATION_SECONDS"
  elif [[ "$SCENARIO" == skewed ]]; then
    printf 'rps=%s,warm=%sx%ss,dur=%ss,hot=%s' \
      "$RPS" "$WARMUP_RPS" "$WARMUP_SECONDS" "$DURATION_SECONDS" "$SKEWED_HOT_FRACTION"
  else
    printf 'rps=%s,warm=%sx%ss,dur=%ss' \
      "$RPS" "$WARMUP_RPS" "$WARMUP_SECONDS" "$DURATION_SECONDS"
  fi
}

build_generator_args() {
  GEN_ARGS=()
  local targets
  case "$SCENARIO" in
    skewed) targets="$(sed -n 's/^FUNCTION_TARGETS=//p' "$STATE_FILE")" ;;
    *) targets="$(first_target)" ;;
  esac
  [[ -n "$targets" ]] || die "no targets found in $STATE_FILE"

  GEN_ARGS+=(
    -platform hyperfaas-ideal-arch
    -transport ideal-arch
    -url "http://${HYPERFAAS_INGRESS_HTTP}/invoke"
    -targets "$targets"
    -seed "$SEED"
    -request-timeout 30s
    -out "$GEN_CSV"
  )

  case "$SCENARIO" in
    bursty)
      GEN_ARGS+=(
        -scenario bursty
        -warmup-rps 5 -warmup-duration 1s -sustained-rps 50 -sustained-duration 1s
        -workload-duration "${BURST_DURATION_SECONDS}s"
        -burst-quiet-min-rps "$BURST_QUIET_MIN_RPS"
        -burst-quiet-max-rps "$BURST_QUIET_MAX_RPS"
        -burst-min-phase-duration 3s
        -burst-max-phase-duration 4s
        -burst-peak-min-rps "$BURST_PEAK_MIN_RPS"
        -burst-peak-max-rps "$BURST_PEAK_MAX_RPS"
        -burst-ramp-every 1s
      )
      ;;
    skewed)
      GEN_ARGS+=(
        -scenario skewed
        -warmup-rps "$WARMUP_RPS" -warmup-duration "${WARMUP_SECONDS}s"
        -sustained-rps "$RPS" -sustained-duration "${DURATION_SECONDS}s"
        -skewed-hot-fraction "$SKEWED_HOT_FRACTION"
      )
      ;;
    *)
      GEN_ARGS+=(
        -scenario "$SCENARIO"
        -warmup-rps "$WARMUP_RPS" -warmup-duration "${WARMUP_SECONDS}s"
        -sustained-rps "$RPS" -sustained-duration "${DURATION_SECONDS}s"
      )
      ;;
  esac
}

run_generator() {
  build_generator_args
  local load_desc
  if [[ "$SCENARIO" == bursty ]]; then
    load_desc="burst peak ${BURST_PEAK_MIN_RPS}-${BURST_PEAK_MAX_RPS}rps/${BURST_DURATION_SECONDS}s"
  else
    load_desc="${RPS}rps/${DURATION_SECONDS}s"
  fi
  log "running generator: scenario=$SCENARIO $load_desc targets=$( [[ "$SCENARIO" == skewed ]] && echo "$FUNCTIONS" || echo 1 )"
  : >"$GEN_LOG"

  local before after cp0 ing0 leaf0 work0 cp1 ing1 leaf1 work1
  local cp ing leaf work total gen_s
  local r_cp r_ing r_leaf r_work e_cp e_ing e_leaf e_work
  local p_cp p_ing p_leaf p_work
  before="$(snapshot_ticks)"
  read -r cp0 ing0 leaf0 work0 <<<"$before"
  read -r r_cp r_ing r_leaf r_work <<<"$(snapshot_rss_kb)"
  p_cp=$r_cp; p_ing=$r_ing; p_leaf=$r_leaf; p_work=$r_work

  # Run the generator directly in the background so we can sample its own
  # /proc/<pid>/stat CPU while it runs (portable; no GNU time dependency).
  "$GEN_BIN" "${GEN_ARGS[@]}" >>"$GEN_LOG" 2>&1 &
  local pid=$! g_start g_last g_now elapsed=0 rc=0 timed_out=0
  GEN_PID="$pid"
  g_start="$(proc_ticks "$pid")"
  g_last="$g_start"
  while proc_alive "$pid"; do
    sleep 0.25
    g_now="$(proc_ticks "$pid")"
    [[ -n "$g_now" ]] && g_last="$g_now"
    # Sample platform RSS every 0.5s; track per-role peak over the load window.
    if (( elapsed % 2 == 0 )); then
      read -r r_cp r_ing r_leaf r_work <<<"$(snapshot_rss_kb)"
      if (( r_cp > p_cp )); then p_cp=$r_cp; fi
      if (( r_ing > p_ing )); then p_ing=$r_ing; fi
      if (( r_leaf > p_leaf )); then p_leaf=$r_leaf; fi
      if (( r_work > p_work )); then p_work=$r_work; fi
    fi
    elapsed=$((elapsed + 1))
    if (( elapsed >= 1200 )); then
      timed_out=1
      kill "$pid" 2>/dev/null || true
      break
    fi
  done
  set +e
  wait "$pid"
  rc=$?
  set -e
  GEN_PID=""
  (( timed_out )) && rc=124

  after="$(snapshot_ticks)"
  read -r cp1 ing1 leaf1 work1 <<<"$after"
  read -r e_cp e_ing e_leaf e_work <<<"$(snapshot_rss_kb)"

  if [[ "$rc" -ne 0 ]]; then
    log "generator exited $rc"
    tail -n 15 "$GEN_LOG" >&2 || true
    die "load run failed (see $GEN_LOG)"
  fi

  cp="$(cpu_sec "$cp0" "$cp1")"
  ing="$(cpu_sec "$ing0" "$ing1")"
  leaf="$(cpu_sec "$leaf0" "$leaf1")"
  work="$(cpu_sec "$work0" "$work1")"
  gen_s="$(cpu_sec "$g_start" "$g_last")"
  total="$(awk -v a="$cp" -v b="$ing" -v c="$leaf" -v d="$work" 'BEGIN { printf "%.3f", a + b + c + d }')"

  printf '{"nproc":%s,"clock_ticks":%s,"platform_s":%s,"generator_s":%s,"roles":{"controlplane":%s,"ingress":%s,"leaf":%s,"worker":%s},"rss_kb":{"peak":{"controlplane":%s,"ingress":%s,"leaf":%s,"worker":%s},"end":{"controlplane":%s,"ingress":%s,"leaf":%s,"worker":%s}}}\n' \
    "$NPROC" "$CLK" "$total" "$gen_s" "$cp" "$ing" "$leaf" "$work" \
    "$p_cp" "$p_ing" "$p_leaf" "$p_work" "$e_cp" "$e_ing" "$e_leaf" "$e_work" >"$CPU_JSON"
}

# ---- main --------------------------------------------------------------------

log "local load gate: scenario=$SCENARIO topology=$TARGETS commit=$COMMIT$([[ $DIRTY == 1 ]] && echo ' (platform dirty)')"
ensure_cluster
build_tools
ensure_functions
run_generator

report_args=(
  --csv "$GEN_CSV"
  --log "$GEN_LOG"
  --cpu "$CPU_JSON"
  --scenario "$SCENARIO"
  --load-key "$(load_key)"
  --commit "$COMMIT"
  --topology "$TARGETS"
  --out "$LATEST_JSON"
  --baseline "$BASELINE"
)
[[ "$DIRTY" == 1 ]] && report_args+=(--dirty 1)
[[ -n "${LOAD_LOCAL_WRITE_BASELINE:-}" ]] && report_args+=(--write-baseline)

python3 "$REPORT" "${report_args[@]}"
