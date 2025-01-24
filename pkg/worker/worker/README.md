# Tests

## Params
- dockerTolerance: Tolerance for container to shut down after we call *Stop* 
  - (in seconds)
  - default: 4 (pretty much arbitrarily chosen number)
- ServerAdress: address of controller server
  - default: "localhost:50051"
- specifyRuntime: specify runtime for containers
- logLevel: log level for logger
  - currently only *docker* is implemented
  - default: "docker"

## Test Cases

*To test we run, call and stop the container with different images. We do this through the functions that are also used during normal
program execution.*
1. Normal execution
    1. Hello Image
   2. Echo Image

2. Stop non-existing container

3. Call non-existing container

4. Start container with certain image
    1. non-existent image
   2. non-local image --> has to be pulled from remote repo (right now docker hub)