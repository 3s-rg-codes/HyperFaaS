# Tests

## Params
- **server-address**
  - default: "localhost:50051"
- **requested-runtime**: specify runtime for containers
  - default: "docker"
  - currently this flag doesn't actually do anything and docker is the default
- **log-level**: specify the log level for the test logger
  - currently: debug, info, warning, error
- **auto-remove**: specify if containers should automatically be removed after they terminate
  - currently: true, false 
  - if this is set to false, the normla execution tests will fail, since the container will still be there after stopping it
- **environment**
  - currently: compose, local
  - specify the environment for the tests to be run
- **test-cases**
  - specify all cases to be run
  - currently: all OR 123 OR 2571 OR ...
- **docker-tolerance**: Tolerance for container to shut down after we call *Stop* 
  - (in seconds)
  - default: 4 (pretty much arbitrarily chosen number)
- **listener-timeout**
  - after how many seconds of not reconnecting is a container removed from the listeners map
- **cpu-period**
- **cpu-quota**
- **memory-limit**

## Test Cases

- names are self-explanatory