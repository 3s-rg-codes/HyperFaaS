# HyperFaaS Functions

This directory contains different images of simple functions for testing purposes.
To build and use the images, follow the instructions for the correct container runtime.

## Docker Images

Each folder containing a Dockerfile is a different Docker image that you can build. We build a go executable and pack it inside a Docker image to run it.

> [!NOTE]
> If you are working on a Windows/MacOS host, it is important that you run `$env:GOOS = "linux"` first.

This will change the target platform of `go build` only in your current shell.

To build an executable, run `go build handler.go` in the respective directory.

To build the docker image, run `docker image build -t <tag:version> .` in the respective directory.

Example:

<div style="color: grey;">
<pre>
docker image build -t hello:latest .
</pre>
</div>
