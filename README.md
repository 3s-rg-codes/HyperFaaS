# HyperFaaS

HyperFaaS is a serverless platform with a tree-like load balancing structure. It consists of load balancer nodes that forward calls to worker nodes, which execute serverless functions.
The load balancer nodes that forward calls to worker nodes are called "leaf nodes".
## Architecture

HyperFaaS consists of three main components:

- **Load Balancer Nodes**: Schedule function calls to other nodes or workers and handle load balancing
- **Worker Nodes**: Execute the serverless functions in containers
- **Database**: Manages function metadata and configurations like resource limits

The platform can be run in two modes:
- **Containerized Mode**: All components (load balancer nodes, workers and database) run in Docker containers
- **Native Mode**: All components run directly on the host

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

Run database, leaf, and worker components separately:

1. Start the database:
   ```
   cd ./cmd/database && go run .
   ```

2. Start a leaf node:
   ```
   just run-local-leaf
   ```

3. Start a worker node:
   ```
   just run-local-worker
   ```

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

## Testing

Run all tests:
```
just test-all
```

Run integration tests:
```
just test-integration-containerized-all
```

## Cleanup

Remove all Docker containers/images and logs:
```
just clean
```


## Benchmarks and Metrics

To generate preliminary data and test metric generation, you can do the following:

```
# Build all images if you haven't already
just build

# Start HyperFaaS
just start

# Start the metrics client
just metrics-client

# Run preliminary load generation
cd load-generator
# Configure the load generation via environment variables in the justfile
just run-seeded

# When the load generation is done, you can stop the metrics client
# Export the metrics
just export

# Analyse metrics
cd ..
just metrics-analyse
```

## hyperFake

### [hyperFake Model](./hyperFakeModel/README.md)
