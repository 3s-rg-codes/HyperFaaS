# HyperFaaS

HyperFaaS is a highly-scalable, research serverless platform designed for bare VM deployments. 
It leverages a decentralized, tiered routing and autoscaling architecture to optimize resource usage, minimize latency, and prevent resource bottlenecks.

HyperFaaS is composed of four decoupled layers:

- **Ingress**: Where requests enter the system. Evaluates routing policies, and forwards traffic to a leaf server. Ingress relies on a local, eventually consistent routing cache.
- **Control Plane**: Manages users and function metadata registration, publishing specifications via `etcd`.
- **Leaf**: Sits between the ingress and the worker nodes. It is responsible for request admission (throttling, rate limiting, etc.) and local autoscaling.
- **Worker**: Provides a sandbox lifecycle API with support for multiple runtimes. We aim to support containerd and firecracker.

---

## Architecture Overview

Sync invocations traverse a two-hop path:

```text
Client → Ingress → Leaf → Function Instance (on a Worker)
```

This way, the cluster can be partitioned into smaller units, and each unit can be managed independently.
Each leaf manages its own worker pool, and there is no cross-leaf communication.

---

## Cold Starts

Cold starts are resolved entirely at the leaf level. Ingress only selects the best leaf using cached routing hints (e.g., active capacity or cold instance flags) but does not buffer requests or orchestrate sandbox creation.

When a synchronous request arrives at a leaf and no warm instance is available:

1. The leaf dataplane enqueues the request in a local in-memory queue (a Knative-style breaker/throttler, bounded by `max_queue_depth` and `queue_timeout`).
2. The leaf control plane invokes the worker runtime to provision a new container.
3. Once the sandbox signals readiness, the queued requests compete for capacity via the same throttler/lease path as warm requests.
4. If the cold start timeout is exceeded before a sandbox is ready, the request is rejected with a gateway error.

Neither the worker server nor the leaf poll sandboxes for readiness. All applications need to use the (very small) hyperfaas runtime SDK in their code to signal readiness.
The runtime SDK simply sends a gRPC request to the worker server to signal readiness.
---

## Placement Schedulers

Leaves evaluate a pluggable placement policy per function when scaling up sandboxes:

- **Balanced Round-Robin**: Spreads container starts sequentially across the workers assigned to the leaf, tracking instance count limits.
- **Resource-Aware**: Scores workers using real-time host-level telemetry (CPU, memory, disk utilization) combined with the declared resource requests of the function. It schedules new placements onto the worker with the lowest projected resource pressure, balancing system load and preventing CPU starvation or OOM thrashing during intense cold start bursts.

---

## Sandbox Runtimes

HyperFaaS supports pluggable worker-level sandbox backends via the `Runtime` interface:

- **Docker**: Launches container instances by invoking the local Docker daemon API. Each container is configured with resource limits (CPU and Memory) based on the function spec, uses the host bridge network, and publishes container port `50052` to a dynamic host port.
- **RunC**: Launches containers using direct OCI/runc configurations. RunC containers enforce hard Memory limits and hard CPU limits via cgroup CFS bandwidth control (quota and period).
- **Firecracker (Direct VM)**: Boots a dedicated microVM for each function instance. It builds a static binary of the function, packages it along with `firecracker-init` as PID 1 into an `initrd` (or rootfs ext4 image), and boots the VM with direct TAP networking. The function inside the VM binds to `50052` and signals readiness to the worker over the VM network interface.
  - *Resource limits note:* Firecracker allocates CPU limits solely by assigning integer vCPU counts. Consequently, fine-grained fractional CPU allocations (e.g., 250 or 500 millicores) are rounded up to the nearest whole integer of vCPUs (minimum 1). Within the VM guest, the process has full access to the allocated vCPU threads without CFS bandwidth limits on the host unless jailer/cgroups are externally configured. currently we dont support that configuration.

### Function Filesystem Isolation

Function filesystem access is isolated by the selected sandbox backend, but the isolation strength differs by runtime:

- **Docker** uses Docker's standard container filesystem isolation. The function sees the container image filesystem, not the host root filesystem.
- **RunC** creates a per-instance root filesystem under the worker runtime directory and starts the function with that directory as `/`. A function launched from a single executable artifact receives a rootfs containing `/function`; a directory artifact is copied as the full instance rootfs and must contain an executable `/function` wrapper. The function can write inside its own copied rootfs because the root is currently mounted read-write.
- **Firecracker** provides stronger filesystem isolation by running the function inside a dedicated microVM using the configured initrd or rootfs image.

RunC filesystem isolation uses Linux namespaces and cgroups, not VM-level isolation. Functions run as UID 0 inside the container with a limited capability set, `noNewPrivileges`, masked `/proc` paths, and read-only kernel/system paths. They cannot directly see the host filesystem unless it is included in the artifact/rootfs or explicitly mounted, but sensitive files included in an artifact are visible to the function.

### Sandbox Pool Ideas (Agnostic vs Image-Dependent)

Pooling here means reusing **infrastructure artifacts** to shorten cold-start creation — not keeping pre-warmed function instances alive. Instance pools (sandboxes already serving traffic) are a separate scale-to-zero / capacity policy.

**Image-agnostic pools** apply to every cold start regardless of function image. Size is fixed per worker; cost does not grow with the number of registered functions.

| Pool | What it caches | Notes |
|------|----------------|-------|
| CNI network namespace | Pre-created netns + veth/IP | Skips CNI setup on the hot path; needs background refill under burst |
| Host port | Pre-allocated DNAT ports | Avoids port-allocation races when netns pooling is enabled |
| Async teardown | Non-blocking stop/delete | Returns pooled resources faster; pairs with the pools above |

These are the primary levers for platforms with thousands of distinct images.

**Image-dependent caching** reuses work specific to one function image. It does not scale if every image is pre-provisioned on every worker.

| Mechanism | What it caches | Scalability |
|-----------|----------------|-------------|
| Image pull dedup (`single_flight_pull`) | One in-flight pull per image ref | Essential for cold storms on a single image |
| Image prefetch | Layers in the local content store at worker boot | Useful for known-hot images only |
| Snapshot pool | Pre-created containerd writable snapshot | Viable for a **small hot set** per worker; not for all functions |

Containerd already deduplicates **read-only image layers** in the content store. The expensive part under burst is usually creating the writable snapshot, container, task, and network plumbing — not storing another full copy of the image.

**Tiered strategy for many functions:**

```text
Tier 0 (global, fixed):     netns pool, port pool, async teardown
Tier 1 (per worker, LRU):   recently used images in local content store
Tier 2 (per image, tiny):   0–2 snapshot slots only while image is in Tier 1
Tier 3 (cold):              on-demand create; pull + snapshot + start
```

For multi-image workloads, combine Tier 0 with **scheduling for image locality** (prefer workers that already pulled image *X*) rather than pre-snapshotting every function. Snapshot pooling is a micro-optimization for hot images on a given node, not a platform-wide pre-warm of all registered functions.

---

## Routing State & Delta Dissemination

Leaves stream real-time capacity and state updates to Ingress nodes. To minimize network overhead, updates are dispatched as **deltas**: only function entries with changed metrics (such as active/in-flight counts) are transmitted. Ingress merges these delta updates into its local, in-memory routing cache. Complete snapshots are fetched only during bootstrap or recovery.

---

## Scaling Limits & Decentralized Autoscaling

Autoscaling is completely decentralized. Leaf nodes scale up and down independently based on local demand and queue signals, operating under per-leaf instance limits. HyperFaaS deliberately avoids a centralized, global view of instance counts to prevent routing bottlenecks.

The leaf scales from admitted execution plus queued demand. Lifecycle decisions
include pending starts and pending stops, so worker removal updates cannot apply
the same downscale twice. Optional sustained CPU and memory rejection is disabled
by default and can be enabled only for functions configured to reject overload.
See [admission and scaling experiment profiles](docs/admission-scaling-experiments.md).

---

## Deterministic Simulation Testing (DST)

HyperFaaS includes black-box DST tests under `test/dst` that drive a locally running cluster (control plane, ingress, leaf, worker) to validate correctness, scheduling edge cases, and scale-to-zero.

### Clusters

**Docker (1 leaf × 1 worker)** — default local path; uses the real Docker runtime and `echo-http` / `echo-grpc` images:

```bash
just stop && just start && just test-dst
```

**Fake multi-node** — in-process worker runtime (`runtime.type: fake`) with echo/sleep images (same image strings; no Docker sandboxes). Default topology is **4 leaves × 10 workers**:

```bash
just stop && just start-big-fake && just test-dst
# optional topology / slow CreateSandbox:
# HYPERFAAS_FAKE_LEAVES=1 HYPERFAAS_FAKE_WORKERS_PER_LEAF=8 \
#   HYPERFAAS_FAKE_SIMULATE_START_LATENCY=1 just start-big-fake
```

`just test-dst` auto-sources `.run/fake-cluster/env.sh` when present (worker address list for scale-to-zero checks). `just stop` tears down both topologies.

### Tests

- **Full Workload** (`TestDSTFullWorkload`): seeded concurrent user/function create, update, and invoke plan over a time window, then quiesce and assert scale-to-zero across all configured workers.
- **Lifecycle** (`TestDSTLifecycle`): create → HTTP/gRPC invoke → update → delete smoke path.

Defaults: 5 users × 3 functions × 1500 invokes over 60s (`MaxConcurrency=0`, `MaxInstances=5`). One client goroutine per planned op.

### Useful knobs

| Variable | Role |
|----------|------|
| `HYPERFAAS_DST_RPS` | Size invoke count for ~RPS over `HYPERFAAS_DST_WORKLOAD_DURATION` |
| `HYPERFAAS_DST_HTTP_IMAGE` / `_GRPC_IMAGE` | e.g. `sleep-http` for slow HTTP invokes |
| `HYPERFAAS_DST_MAX_CONCURRENCY` / `_MAX_INSTANCES` | Per-function scale policy (default CC=0 / instances=5) |
| `HYPERFAAS_DST_USERS`, `_FUNCTIONS_PER_USER`, `_INVOKES_PER_FUNCTION`, `_SEED`, … | Workload shape |
| `HYPERFAAS_FAKE_SIMULATE_START_LATENCY` | Heavy-tailed CreateSandbox delay (p50≈100ms, p90≈3s, p99≈20s) on fake workers |
| `HYPERFAAS_WORKER_GRPC` | Comma-separated worker list (set automatically by `start-big-fake`) |
