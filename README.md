# HyperFaaS

HyperFaaS is a Function-as-a-Service platform for VMs and bare-metal hosts, written in Go.
It partitions the cluster into leaves, each managing its own pool of workers, so that routing, admission, and sandbox placement run without global coordination.
Routing and placement algorithms are pluggable policies that can be switched at runtime.

Functions are OCI images (or Firecracker boot images) that serve plain HTTP or gRPC and link a small runtime library to signal readiness.

## Architecture

```text
                       ┌───────────────┐      etcd
                       │ Control plane │◄───► (users, functions,
                       └───────┬───────┘       PlatformConfig)
            WatchFunctions,    │
            WatchPlatformConfig│
          ┌────────────────────┼─────────────────────┐
          ▼                    ▼                     ▼
Client ─► Ingress ──────► Leaf 1 ──────────► Sandbox on Worker 1a
 HTTP/     (routing       (admission,        Sandbox on Worker 1b
 gRPC       policy)        queueing,
             ▲             autoscaling,   ─► Leaf 2 ─► Workers 2a, 2b, ...
             │             placement)
             └── WatchRoutingState (aggregated leaf state)
```

| Component | Binary | Responsibility |
|---|---|---|
| Control plane | `cmd/controlplane` | Stores users, functions, and the `PlatformConfig` policy document in etcd. Streams changes to leaves and ingresses. Not on the invocation path. |
| Ingress | `cmd/ingress` | Receives invocations, selects a leaf with the active routing policy, and proxies the request. Keeps an in-memory, eventually consistent cache of leaf state. |
| Leaf | `cmd/leaf` | Owns a disjoint pool of workers. Admits and queues requests per function, autoscales, places new sandboxes on its workers, and proxies requests to sandboxes. |
| Worker | `cmd/worker` | Creates and stops sandboxes through a pluggable runtime and reports health, load, cached images, and sandbox state to its leaf. |

Workers are never shared between leaves.
Each leaf therefore schedules on a complete local view of its pool, and leaves never talk to each other.
Detailed worker and sandbox state stays in the leaf; the ingress only receives aggregated per-leaf signals.
Nothing is written to etcd on the invocation path.

### Invocation path

1. The ingress reads the function id from the request, picks a leaf with the routing policy, and proxies the request to it. It never retries on another leaf.
2. The leaf's per-function throttler leases a concurrency slot on a ready sandbox and proxies the request directly to the sandbox address. The worker server is not on this path.
3. If no ready sandbox has a free slot, the request waits in a bounded FIFO queue. When the function has no ready sandbox, the leaf picks a worker with the placement policy and calls `CreateSandbox`.
4. `CreateSandbox` blocks until the function calls `SignalReady` on the worker. Nothing polls the sandbox. The worker returns the sandbox address, and the leaf dispatches queued requests to it.
5. When the queue is full, the leaf rejects the request with HTTP 429. When the request or cold-start timeout expires, it returns 504.

A warm invocation touches only the ingress, the leaf, and the sandbox.

### State propagation

| Stream | From → To | Content |
|---|---|---|
| `WatchFunctions` | control plane → leaves | Function create, update, and delete events. Every leaf caches all function specs. |
| `WatchPlatformConfig` | control plane → ingresses, leaves | The active routing policy, placement policy, and state refresh interval. |
| `WatchState` | worker → leaf | Health, schedulability, normalized load average, cached images, sandbox readiness, and in-flight counts. |
| `WatchRoutingState` | leaf → ingress | Only the fields that the active routing policy needs: health always; aggregate in-flight, `leaf_load`, and per-function available concurrency on request. A full snapshot is sent first, then updates. |

Each ingress subscribes to every leaf.
The routing request path reads the current picker through an atomic pointer and takes no lock.

### Autoscaling and admission

Every leaf runs one autoscaler per function, using only local signals.
There is no global instance count: `min_instances` and `max_instances` apply per leaf.

- `max_concurrency` is the hard per-sandbox request limit enforced by the throttler.
- `target_concurrency` × `target_utilization` is the autoscaler's soft target. The desired sandbox count is `ceil(in-flight / target)`.
- The autoscaler uses a Knative-style stable window and panic window. Scale-down only uses the stable window.
- `start_tokens_per_worker` caps concurrent sandbox starts per worker. A worker at the cap is skipped by every placement policy until a start completes.
- Idle functions scale to zero after `scale_to_zero_idle_timeout` (leaf default: `dataplane.scale_to_zero_after`).

## Policies

Routing and placement policies are selected in the `PlatformConfig` document on the control plane and apply to the whole deployment.
Changing the document reconfigures running ingresses and leaves without a restart.
A fresh deployment starts with `availableCapacity` routing, `balancedRoundRobin` placement, and a 500 ms state refresh interval.

### Ingress routing

| Policy | Config key | Inputs |
|---|---|---|
| Round-robin | `roundRobin` | Leaf health |
| Random | `random` | Leaf health |
| Least-loaded | `leastLoaded` | Aggregate in-flight per leaf |
| Consistent hashing | `consistentHashing` | Function id, leaf health |
| CH with bounded loads | `boundedLoads` | Function id, `leaf_load` |
| Random-jump CH | `rjCh` | Function id, `leaf_load` |
| CH with random load updates | `chRlu` | Function id, `leaf_load`, sampled inter-arrival times |
| Available capacity | `availableCapacity` | Available concurrency per function per leaf |

`leaf_load` is the highest normalized 1-minute load average among a leaf's workers.

### Leaf placement

| Policy | Config key | Chooses the worker with |
|---|---|---|
| Balanced round-robin | `balancedRoundRobin` | The next schedulable worker in rotation |
| Resource-aware | `resourceAware` | The lowest normalized CPU load average |
| Cold-start-aware | `coldStartAware` | The fewest in-flight sandbox starts |
| Reservation-aware | `reservationAware` | Fewer active plus pending sandboxes among two random workers |
| Image-aware | `imageAware` | The function image already in its local cache |
| Bounded loads | `boundedLoads` | Consistent hash of the function, skipping workers above the load bound |

### Switching policies

```bash
curl -X PUT http://127.0.0.1:8081/v1/platform/config \
  -H 'Content-Type: application/json' \
  -d '{
        "config": {
          "routing":   {"boundedLoads": {"bound": 1.0}},
          "placement": {"coldStartAware": {}},
          "stateRefreshInterval": "0.5s"
        },
        "expectedVersion": 0
      }'
```

With `expectedVersion: 0` the write is unconditional; a non-zero value rejects the write unless the stored document has that version (read it with `GET /v1/platform/config`).
To add a new policy, see [docs/ADDING_ROUTING_AND_PLACEMENT_POLICIES.md](docs/ADDING_ROUTING_AND_PLACEMENT_POLICIES.md).

## Sandbox runtimes

Each worker selects one runtime with `runtime.type` in its config.

| Runtime | `runtime.type` | Function image | Notes |
|---|---|---|---|
| containerd | `containerd` | OCI image reference | CNI networking. Optional network namespace pool (`use_pool`), async teardown, single-flight image pulls, and image prefetch. |
| Firecracker | `firecracker` | Path to an ext4 rootfs or initrd on the worker | Pool of pre-created networks with non-blocking refill, and optional snapshot restore (`use_snapshots`). Downloads missing boot images from `artifacts_bucket` if set. vCPUs are whole numbers, so fractional CPU requests round up. |
| Docker | `docker` | Docker image name | Used by the local development cluster. |
| runc | `runc` | Executable or rootfs directory | Direct OCI runtime with cgroup CPU and memory limits. |
| fake | `fake` | Any string | In-process sandboxes for large local topologies and tests. |

Build Firecracker boot images from a function package with `scripts/build-firecracker-rootfs.sh` or `scripts/build-firecracker-initrd.sh`; the initrd uses `cmd/firecracker-init` as PID 1.

## Writing a function

A function is a normal HTTP or gRPC server that follows this contract:

- read `FUNCTION_PORT` (default `50052`), `CONTROLLER_ADDRESS`, `INSTANCE_ID`, and `FUNCTION_ID` from the environment,
- listen on `0.0.0.0:$FUNCTION_PORT`,
- call `hyperfaas.SandboxService/SignalReady` on the worker once it is ready to serve.

The runtime libraries do the last two steps. In Go:

```go
func main() {
	fn := functionruntime.NewHTTP()
	fn.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
}
```

Runtime libraries exist for Go (`pkg/functionruntime`), Node.js, Python, and Rust (`functions/<lang>/runtime`).
Example functions are in `functions/`, and `docker/<lang>-function.Dockerfile` builds their images.
See [functions/README.md](functions/README.md) for language versions.

## Running locally

Requirements: Go, Docker, [`just`](https://github.com/casey/just), and `curl`.

```bash
just start      # etcd (Docker), control plane, 1 worker (Docker runtime), 1 leaf, 1 ingress
just stop
```

`just start` builds the binaries into `bin/`, builds the `echo-http`, `echo-grpc`, and `sleep-http` images, and writes logs to `.run/logs/`.

| Endpoint | Address |
|---|---|
| Control plane HTTP API | `127.0.0.1:8081` |
| Control plane gRPC | `127.0.0.1:50054` |
| Ingress HTTP | `127.0.0.1:8080` |
| Ingress gRPC proxy | `127.0.0.1:50055` |

Register a user and a function, then invoke it:

```bash
curl -X POST http://127.0.0.1:8081/v1/users -d '{"name": "alice"}'
# -> {"userId": "1", "name": "alice"}

curl -X POST http://127.0.0.1:8081/v1/users/1/functions -d '{
  "runtime": {"image": "echo-http", "protocol": "http", "isolation": "ISOLATION_KIND_DOCKER"},
  "scale":   {"maxInstances": "5", "maxConcurrency": "1",
              "scaleToZeroIdleTimeout": "30s", "coldStartTimeout": "45s", "requestTimeout": "30s"}
}'
# -> {"userId": "1", "functionId": "1", ...}

curl -X POST http://127.0.0.1:8080/invoke \
  -H 'X-HyperFaaS-User-ID: 1' -H 'X-HyperFaaS-Function-ID: 1' \
  -d 'hello'
```

HTTP invocations are `POST /invoke` with the user and function ids in headers.
gRPC clients call their own service through the ingress gRPC proxy and select the function with the `x-hyperfaas-function-id` metadata key, or with the function id as the host part of `:authority`.

The API also provides `GET`, `PUT`, and `DELETE` on `/v1/users/{user_id}` and `/v1/users/{user_id}/functions/{function_id}`.

### Larger topologies without sandboxes

```bash
just start-big-fake   # 4 leaves × 10 workers with the fake runtime
HYPERFAAS_FAKE_LEAVES=8 HYPERFAAS_FAKE_WORKERS_PER_LEAF=16 just start-big-fake
```

Set `HYPERFAAS_FAKE_SIMULATE_START_LATENCY=1` for heavy-tailed sandbox start delays, and `HYPERFAAS_FAKE_LEAF_ROUTING_POLICY` to publish a routing policy at startup.

## Configuration

Each binary takes `-config <file>`; the defaults for the local cluster are in `configs/`.

- `ingress.yaml`: listen addresses and the static list of leaves, including the ingress-to-leaf transport (`h2c` by default).
- `leaf.yaml`: the leaf's worker list and the dataplane and autoscaling settings (queue, start tokens, timeouts, reconcile interval, target utilization).
- `worker.yaml`: the sandbox runtime and its settings.
- `controlplane.yaml`: etcd endpoints and API addresses.

Leaf membership and worker pools are static; adding or removing leaves and workers requires a restart.
Policies are not configured in YAML; they come from the `PlatformConfig` document.

## Testing

| Command | What it runs |
|---|---|
| `go test ./...` | Unit tests |
| `just test-leaf-integration-docker`, `just test-worker-integration` | Docker-backed integration tests |
| `just test-worker-integration-firecracker` | Firecracker runtime integration tests (needs KVM) |
| `just test-dst` | Seeded end-to-end workload against a running cluster (`just start` or `just start-big-fake`), ending with a scale-to-zero check |
| `just test-dst-sticky`, `just test-dst-chbl` | End-to-end checks for consistent-hashing, image-aware, and bounded-loads policies on a fake cluster |
| `just load-local [sustained\|bursty\|skewed\|increasing]` | Load generator against a running cluster, compared with the checked-in baseline |
| `just policy-cost` | State size, decision cost, and update cost of every routing and placement policy |

`just test-dst` is configured through `HYPERFAAS_DST_*` environment variables (users, functions, invokes, RPS, images, seed); see `test/shared/config.go`.

## Repository layout

```text
cmd/            binaries: controlplane, ingress, leaf, worker, firecracker-init, pool-loadtest
pkg/controlplane  HTTP and gRPC API, etcd and in-memory stores, PlatformConfig
pkg/ingress       HTTP and gRPC proxies, routing controller and policies (routing/)
pkg/leaf          dataplane (throttler, queue), autoscaling, placement schedulers, worker client
pkg/worker        worker server, health reporting, runtimes (runtime/)
pkg/functionruntime  Go function runtime library
pkg/core          shared protobuf types and validation
proto/          protobuf definitions (regenerate with `just gen-proto`)
functions/      example functions and runtime libraries for Go, Node.js, Python, Rust
test/           end-to-end (dst/) and performance (perf/) tests
docs/           design notes and measurement results
```
