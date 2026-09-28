default:
    @just --list

# Regenerate Go protobuf and gRPC stubs from proto/*.proto into pkg/* (per go_package).
gen-proto:
	protoc -I proto \
		--go_out=. --go_opt=module=hyperfaas-ideal-arch \
		--go-grpc_out=. --go-grpc_opt=module=hyperfaas-ideal-arch \
		proto/core.proto proto/controlplane.proto proto/leaf.proto proto/worker.proto

gen-echo-proto:
    protoc -I functions/go/echo-grpc/proto \
        --go_out=. --go_opt=module=hyperfaas-ideal-arch \
        --go-grpc_out=. --go-grpc_opt=module=hyperfaas-ideal-arch \
        functions/go/echo-grpc/proto/echo.proto

build:
    go build -o bin/controlplane ./cmd/controlplane
    go build -o bin/ingress ./cmd/ingress
    go build -o bin/leaf ./cmd/leaf
    go build -o bin/worker ./cmd/worker

# --network=host: campus DNS (130.149.7.7) often does not answer from docker bridge on the eval VM.
build-echo-grpc-image:
    @if docker image inspect echo-grpc >/dev/null 2>&1; then \
        echo "echo-grpc image already present, skipping build"; \
    else \
        DOCKER_BUILDKIT=1 docker build --network=host -t echo-grpc:latest -f docker/go-function.Dockerfile --build-arg FUNCTION_PATH=./functions/go/echo-grpc . && \
        docker tag echo-grpc:latest echo-grpc; \
    fi

build-echo-http-image:
    @if docker image inspect echo-http >/dev/null 2>&1; then \
        echo "echo-http image already present, skipping build"; \
    else \
        DOCKER_BUILDKIT=1 docker build --network=host -t echo-http:latest -f docker/go-function.Dockerfile --build-arg FUNCTION_PATH=./functions/go/echo-http . && \
        docker tag echo-http:latest echo-http; \
    fi

build-sleep-http-image:
    @if docker image inspect sleep-http >/dev/null 2>&1; then \
        echo "sleep-http image already present, skipping build"; \
    else \
        DOCKER_BUILDKIT=1 docker build --network=host -t sleep-http:latest -f docker/go-function.Dockerfile --build-arg FUNCTION_PATH=./functions/go/sleep-http . && \
        docker tag sleep-http:latest sleep-http; \
    fi

build-echo-images: build-echo-grpc-image build-echo-http-image

# Images required by the Docker-backed local cluster and its DST suite.
build-local-function-images: build-echo-images build-sleep-http-image

build-echo-grpc-firecracker-rootfs:
    bash scripts/build-firecracker-rootfs.sh ./functions/go/echo-grpc ./bin/firecracker/echo-grpc.ext4

build-echo-grpc-firecracker-initrd:
    sudo env "PATH=$PATH" bash scripts/build-firecracker-initrd.sh ./functions/go/echo-grpc ./bin/firecracker/echo-grpc.cpio.gz

test-worker-integration-firecracker:
    go test ./pkg/worker/runtime/firecracker/... -tags=integration -count=1 -v

test-leaf:
    go test ./pkg/leaf/... -count=1 -v

test-ingress:
    go test ./pkg/ingress/... -count=1 -v

test-leaf-integration-docker:
    go test ./pkg/leaf/... -tags=integration -count=1 -v

test-worker-integration:
    go test ./pkg/worker/runtime/docker/... -tags=integration -count=1 -v

test-dst:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -f .run/fake-cluster/env.sh ]]; then
      # shellcheck disable=SC1091
      source .run/fake-cluster/env.sh
      echo "==> using fake-cluster env (.run/fake-cluster/env.sh)"
    fi
    go test ./test/dst/... -v -count=1 -timeout 25m

# Sticky-routing DST pass (CH stickiness + image-aware). Restarts the big fake
# cluster; each test selects its routing/placement policy through the
# PlatformConfig document.
test-dst-sticky:
    #!/usr/bin/env bash
    set -euo pipefail
    HYPERFAAS_QUIET_STOP=1 bash scripts/stop.sh >/dev/null || true
    HYPERFAAS_FAKE_LEAVES="${HYPERFAAS_FAKE_LEAVES:-4}" \
      HYPERFAAS_FAKE_WORKERS_PER_LEAF="${HYPERFAAS_FAKE_WORKERS_PER_LEAF:-10}" \
      HYPERFAAS_FAKE_LEAF_ROUTING_POLICY=consistent-hashing \
      bash scripts/start-big-fake-cluster.sh
    # shellcheck disable=SC1091
    source .run/fake-cluster/env.sh
    go test ./test/dst/ -v -count=1 -timeout 10m \
      -run 'TestDSTConsistentHashing|TestDSTImageAware|TestDSTCachedImages|TestDSTPrepareImage'

# Ingress CH-BL partition DST: max(worker load) makes a leaf look full, traffic forwards.
test-dst-chbl:
    #!/usr/bin/env bash
    set -euo pipefail
    HYPERFAAS_QUIET_STOP=1 bash scripts/stop.sh >/dev/null || true
    HYPERFAAS_FAKE_LEAVES="${HYPERFAAS_FAKE_LEAVES:-2}" \
      HYPERFAAS_FAKE_WORKERS_PER_LEAF="${HYPERFAAS_FAKE_WORKERS_PER_LEAF:-3}" \
      HYPERFAAS_FAKE_LEAF_ROUTING_POLICY=bounded-loads \
      HYPERFAAS_FAKE_BOUNDED_LOADS_BOUND=1.0 \
      bash scripts/start-big-fake-cluster.sh
    # shellcheck disable=SC1091
    source .run/fake-cluster/env.sh
    go test ./test/dst/ -v -count=1 -timeout 10m -run 'TestDSTBoundedLoadsForwardsFromHotLeaf'

test-perf:
    go test ./test/perf/... -v -count=1 -timeout 5m

# Alias for test-dst. Start the cluster first with `just start` or `just start-big-fake`.
dst: test-dst

# Alias for test-perf. Start the cluster first with `just start`.
perf: test-perf

# Policy cost measurement: retained state, decision work, churn, wire bytes.
# See docs/ROUTING_POLICY_COST_RESULTS.md. Single-host measurements at equal dimensions.
policy-cost:
    HYPERFAAS_POLICY_COST=1 go test -run 'TestPolicyCost' -v ./pkg/ingress/routing/
    HYPERFAAS_POLICY_COST=1 go test -run 'TestPlacementCost' -v ./pkg/leaf/scheduler/
    go test -run '^$' -bench 'BenchmarkRoutingPick|BenchmarkRoutingChurn|BenchmarkRoutingHealthChange' -benchmem -benchtime=100ms ./pkg/ingress/routing/
    go test -run '^$' -bench 'BenchmarkPlacementPick' -benchmem -benchtime=100ms ./pkg/leaf/scheduler/

# Start the full local cluster (etcd + controlplane + worker + leaf + ingress).
# Run `just test-dst` in another terminal once start completes.
start:
    bash scripts/start.sh

# Large local cluster with in-process fake runtimes (default 4 leaves × 10 workers).
# Override with HYPERFAAS_FAKE_LEAVES / HYPERFAAS_FAKE_WORKERS_PER_LEAF.
start-big-fake:
    bash scripts/start-big-fake-cluster.sh

stop:
    bash scripts/stop.sh

# Local whole-platform load gate: real generator -> ingress -> leaf -> fake worker. Scenarios: sustained (default) | bursty | skewed | increasing. Reuses a running cluster.
load-local scenario="sustained":
    bash scripts/load-local.sh {{scenario}}

# Re-run and store the result as the checked-in local baseline for later regression deltas.
load-local-baseline scenario="sustained":
    LOAD_LOCAL_WRITE_BASELINE=1 bash scripts/load-local.sh {{scenario}}

# Stop the local cluster used by `just load-local`.
load-local-stop:
    bash scripts/stop.sh

# Start all components against configs/*.yaml (requires etcd).
run-cluster:
    @echo "Use \`just start\` to launch the cluster, then \`just test-dst\`."

run-controlplane:
    go run ./cmd/controlplane -config configs/controlplane.yaml

run-ingress:
    go run ./cmd/ingress -config configs/ingress.yaml

run-leaf:
    go run ./cmd/leaf -config configs/leaf.yaml

run-worker:
    go run ./cmd/worker -config configs/worker.yaml
