# E2E Test implemenation [WIP]

## Idea:
- we want to implement flags to set test params to test certain scenarios
- flags: testType, Runtime, imageTag, Config
- Introduce own errors to improve logging and error handling (especially in tests)


## Different Configs
- normal program execution (standard implementation / average case)
- killing the docker container during execution (at different times during execution)
- killing the container not during execution
- deploying different kinds of functions with different behaviours (e.g.: function that never returns to test timeouts etc., ...)
- stopping a function during execution (at different times during execution)
- deploying multiple functions sequentially
- deploying multiple functions at a time
- deploying a function and instantly stopping it
- hardware malfunction (e.g. power outage -> how to simulate)

## Thoughts (ignore pwease)
- how do we compare errors to what they should be --> own error types, errorsIs() and errorAs() 
- different test configs execute one-after-another as array
- How do we automate the testing for different scenarios
  - create a program that manages the tests and runs each test on a different worker in a different docker runtime?

## Test Procedure
1. Start main_test to initiate server (controller, caller), docker runtime
   - pass flags testType and Runtime
2. Start mockClient_test to create a mock client
   - implemented like this to have more control over testing process
   - pass flag imageTag, config



## RunTests (maybe put this into a script)
- 