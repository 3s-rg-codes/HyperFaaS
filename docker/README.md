# HyperFaaS Docker Setup

This directory contains all Docker-related files and configurations for HyperFaaS.

## Files Overview

### Compose Files
- **`small.yaml`** - Minimal setup: 1 worker, 1 leaf, 1 haproxy, 1 routing controller
- **`medium.yaml`** - Medium setup: 4 workers, 2 leafs, 1 haproxy, 1 routing controller  
- **`large.yaml`** - Large setup: 8 workers, 4 leafs, 2 haproxy levels, 3 routing controllers

#### Note:
The convention across all setups is that the outer most haproxy is reachable on port 9999, etcd is reachable on port 2379 always.
Leaf servers are reachable on port 5005X .
Leaf grpc proxies are reachable on port 6005X .

### Dockerfiles
- **`Dockerfile`** - Main application Dockerfile for building worker, leaf, and routing controller components
- **`function.Dockerfile`** - Function-specific Dockerfile for building individual functions in go

### Configuration
- **`config/`** - HAProxy configuration files
  - `hyperfaas-haproxy-grpc-compose-1-child.cfg` - use if haproxy only has 1 downstream node
  - `hyperfaas-haproxy-grpc-compose-2-child.cfg` - use if haproxy has 2 downstream nodes
  - `hyperfaas-spoe-grpc.cfg` - gRPC SPOE (Stream Processing Offload Engine) configuration (routing controller)
  - `hyperfaas-spoe-http.cfg` - HTTP SPOE configuration

## Important Commands

###  just
Remember you can also use the justfile from the root directory via `just docker/<command>` instead of using the docker justfile directly.
The following commands are listed as executed from this directory (docker/):

```bash
# Start small setup
just ss

# Start medium setup  
just sm

# Start large setup
just sl

# Start and rebuild small setup
just ss docker info --build # 'docker' and 'info' are needed here to be able to pass --build correctly

# View logs for specific component
just logs worker

```

## Configuration Notes

- All Docker build contexts point to the project root (`..`)
- HAProxy configurations are mounted as volumes
    in each setup, we pass in the addresses of the leaf proxies via environment variables.
    for example: CHILD_1: leaf:60060
- The routing controllers receive the addresses of the leaf API servers, not the proxies.
    The proxies are only used for request routing.
- Function logs are stored in Docker volumes
- Containers communicate using the `hyperfaas-network` docker network