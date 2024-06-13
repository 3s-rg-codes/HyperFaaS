This directory contains an implementation of a simple gRPC client that implements the `function.proto` gRPC interface.

The image just runs the executable.

To build the executable, run `go build` in this directory.

If you are working on a Windows/MacOS host, it is important that you run `$env:GOOS = "linux"` first. This will change the target platform of `go build` only in your current shell.
