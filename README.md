# HyperFaaS

HyperFaaS is a serverless platform with a tree-like load balancing structure. It consists of load balancer nodes that forward calls to worker nodes, which execute serverless functions.
The load balancer nodes that forward calls to worker nodes are called "leaf nodes".
## Architecture

HyperFaaS is composed of the following services:

- **Load Balancer Nodes (HAProxy + Routing Controller)**: receive gRPC traffic and forward calls to the appropriate downstream nodes, which can be other load balancer nodes or leaf nodes.
- **Leaf Nodes**: Scale function instances, and route calls to workers.
- **Worker Nodes**: Execute serverless functions inside containers and stream metrics back to the leaf nodes.
- **etcd**: Stores function metadata (image, resource limits, timeouts, etc.) that is consumed by both leaves and workers.

The platform can be run in two modes:
- **Containerized Mode**: All components run in Docker containers via Compose (`etcd`, workers, leaves, routing controller, HAProxy, ...)
- **Native Mode**: Each component runs directly on the host; make sure an etcd instance is available.



## Getting Started
To get started with HyperFaaS, follow these steps:

### Prerequisites

- [Go](https://go.dev/doc/install)
- [Docker](https://docs.docker.com/get-docker/)
- [Protoc](https://protobuf.dev/installation/)
- [Just](https://github.com/casey/just?tab=readme-ov-file#installation)

> **Note**
> If you are running Windows, we heavily recommend using [WSL](https://learn.microsoft.com/en-us/windows/wsl/install) to run HyperFaaS / justfile commands.
### Setup

1. Clone the repository:
   ```
   git clone https://github.com/3s-rg-codes/HyperFaaS.git
   cd HyperFaaS
   ```

2. Build components and the go functions:
   ```
   just build
   ```

### Running the Platform

#### Containerized Mode

Start all components with Docker Compose:
```
just d
```

Or with automatic rebuilding:
```
just start-rebuild
```

#### Native Mode

Run etcd, leaf, and worker components separately:

1. Start etcd (single node):
   ```
   etcd --advertise-client-urls http://localhost:2379 --listen-client-urls http://0.0.0.0:2379
   ```

2. Start a worker node (requires access to Docker):
   ```
   just run-local-worker
   ```

3. Start a leaf node pointing to your worker(s):
   ```
   just run-local-leaf --worker-addr=127.0.0.1:50051
   ```

4. (Optional) Run HAProxy / routing controller if you want the full tree locally; else the leaf gRPC endpoint can be used directly.

### Managing Functions

Function metadata is stored directly in etcd. You can register a new function image and configuration with the CLI:

```
go run ./cmd/hyperfaas-cli function create hyperfaas-hello:latest --cpu-period 100000 --cpu-quota 50000 --memory $((256*1024*1024)) --function-timeout=45s
```

By default the CLI talks to `localhost:2379`; use the global flags `--etcd-endpoint`, `--metadata-prefix`, and `--metadata-dial-timeout` to customise the etcds connection.

## Developing Functions

Currently, HyperFaaS only supports Go as a language for serverless functions. Functions are executed as Docker containers.

To build a Go function:
```
just build-function-go function_name
```

To build all Go functions:
```
just build-functions-go
```
## Development
There are three Go build tags used for testing:
- unit
- integration
- e2e

Please make sure your editor and tools (such as gopls) are configured to recognize these build tags, or your code may not compile or show errors. For example, in VSCode you can set this in `.vscode/settings.json`:

```
{
    "go.buildTags": ["unit", "integration", "e2e"]
}
```

### Linting

We use [golangci-lint](https://golangci-lint.run/) for linting.
You can run it with:
```
just lint
```


### Testing
We have unit, integration and end to end tests.
For the end to end tests, you need to have a running docker compose version of HyperFaaS.
For more information, see [test/README.md](test/README.md).
```
just test-unit

just test-integration

just test-e2e
```
If you want colored output, install gotest:
```
go install github.com/rakyll/gotest@latest
```

Then you can run the tests with a "true" parameter.
```
# prints with color
just test-e2e true
```


## Cleanup

Remove all Docker containers/images and logs:
```
just clean
```
