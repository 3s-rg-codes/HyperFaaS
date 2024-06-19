# E2E Test implemenation [WIP]

## Idea:
- we want to implement flags to set test params to test certain scenarios



## Test-Parameters (Flags)
- **maybe implement with yaml, json file for convenience**
- bool executionStop
- string timeEndExecution
- bool killDocker
- string timeEndDocker
- bool killContainer (?)
- string timeKillContainer (?)
- string functionCallParams (?) -> provide wrong params and see what happens
- 


## Different Configs
- normal program execution (standard implementation / average case)
- killing the docker container during execution (at different times during execution)
- deploying different kinds of functions with different behaviours (e.g.: function that never returns to test timeouts etc., ...)
- stopping a function during execution (at different times during execution)
- deploying multiple functions sequentially
- deploying multiple functions at a time
- deploying a function and instantly stopping it
- hardware malfunction (e.g. power outage -> how to simulate)

## Thoughts (ignore please)
- how to test start call stop differently -> what to pay attention to
- how do we compare errors to what they should be 
- different test configs execute one-after-another as array
- How do we automate the testing for different scenarios
  - create a program that manages the tests and runs each test on a different worker in a different docker runtime?


## RunTests (maybe put this into a script)
- 