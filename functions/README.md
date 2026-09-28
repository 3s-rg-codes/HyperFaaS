# Function Runtimes

Function examples are grouped by implementation language:

- `go`: Go 1.26.3, using `pkg/functionruntime`.
- `node`: Node.js 22.x, using `functions/node/runtime` with `@grpc/grpc-js` 1.14.x and `@grpc/proto-loader` 0.8.x.
- `python`: Python 3.13.x, using `functions/python/runtime` with `grpcio` 1.76.0 and `protobuf` 6.33.1.
- `rust`: Rust 1.87, edition 2024, using `functions/rust/runtime` with `hyper` 1.8 and `tonic` 0.13.

All runtimes follow the same contract:

- Read `CONTROLLER_ADDRESS`, `INSTANCE_ID`, `FUNCTION_ID`, and `FUNCTION_PORT`.
- Listen on `0.0.0.0:$FUNCTION_PORT`, defaulting to `50052`.
- Signal readiness through `hyperfaas.SandboxService/SignalReady`.

For runc, `runtime.image` may be either a single executable file or a prepared rootfs directory. Directory artifacts must contain an executable `/function` wrapper. The Python and Node examples use this minimal wrapper approach and expect the artifact rootfs to contain the matching interpreter and dependencies.
