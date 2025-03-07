# Tests

ATTENTION: Note that due to docker things it is possible that the tests fail due to docker mishaps, if tht tests fail with your changes it's not necessarily the fault of your changes. Just run the tests  again...


## Functionality

The idea behind these integration tests is that they can be run using a number of different parameters. You can choose:
- container runtime (e.g. docker, fake, tbc...)
- different dev environments (locally for fast iteration cycles, containerized or more specifically dockerized using docker compose 
for more realistic prod circumstances
- which tests should be run (c.f. [TestCases](#test-cases))
- every test case is written into one function and can be executed completely independently of one another

## How configuration works
To not have to pass a mile of flags every time you want to run the tests (or having to edit a just command) there is a json file
containing the configuration for all development-related setup but also for all arguments passed to the tests.

This JSON file is parsed at runtime and will set all the params accordingly. BUT since you also don't want to change something in a 
json file every time you want to change something small, the json config can be overridden using flags passed to the binary.

Available flags are shown below. 

It is important to note that as of today (2025-02-22) only the worker is tested using the integration tests. I think ideally we want
to define tests on a higher level (e.g. Leaf-Node or Leaf) in a different file to separate concerns enable the developer to tests more
specifically.


## Params
The params are automatically set via the JSON config file but can be overridden when setting flags.
- **server-address**
- default: "localhost:50051"
- **requested-runtime**: specify runtime for containers
- default: "docker"
- currently this flag doesn't actually do anything and docker is the default
- **log-level**: specify the log level for the test logger
- currently: debug, info, warning, error
- **auto-remove**: specify if containers should automatically be removed after they terminate
- currently: true, false 
- if this is set to false, the normal execution tests will fail, since the container will still be there after stopping it
- **containerized**
- currently: true, false
- specify if the tests should be run in a containerized environment
- **test-cases** <a id="test-cases"></a>
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

Currently, 3 kinds of tests are run. 

1. Testing the core functionality of the worker
1. Normal execution
2. Stopping a non-existent container
3. Calling a non-existent container
4. Testing the metrics endpoint of the worker
5. Starting a container from a non-local image 
2. Testing if the resource configuration of containers works
3. Testing the stats endpoint of the worker using listener nodes whose only functionality is to call the Status Endpoint and then
listen to the incoming events and compare them to the ones that are expected
1. Testing the functionality with one node listening and checking if it gets all events
2. Testing the functionality with three nodes listening and checking if they all get all events
3. Testing if events are still delivered correctly even if the listener crashes (Considering the listener timeout)

## Known bugs

- there seems to be a case where the mock runtime (dead)locks itself, im not sure what causes ist, but it doesn't happen always
