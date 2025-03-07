# WorkerStateMap

## Configuration

The program accepts both command-line arguments and environment variables for configuration.

### Command-Line Arguments

- `-id`: WorkerStateMap ID (equivalent to `WORKER_ID`).
- `-runtime`: Container runtime type (equivalent to `RUNTIME_TYPE`).
- `-log-level`: Log level (equivalent to `LOG_LEVEL`).
- `-log-handler`: Log handler mode (`dev` for development, `prod` for production) (equivalent to `LOG_HANDLER`).

### Environment Variables

- `WORKER_ID`: Unique identifier for the worker.
- `RUNTIME_TYPE`: Specifies the container runtime (e.g., `docker`).
- `LOG_LEVEL`: Logging level (`debug`, `info`, `warn`, `error`, `fatal`, `panic`).
- `LOG_HANDLER`: Log handler mode (`dev` or `prod`).

## Usage

### Running with Command-Line Arguments

```bash
./worker -id=my_worker_id -runtime=docker -log-level=debug -log-handler=dev
```

### Running with Environment Variables

Set environment variables in your shell:

```bash
export WORKER_ID=my_worker_id
export RUNTIME_TYPE=docker
export LOG_LEVEL=debug
export LOG_HANDLER=dev
```
