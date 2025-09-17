This package is reserved for end to end testing of HyperFaaS.

Other tests, such as unit or integration tests, should always be placed right next to the code that is being tested.

This package is used for testing the HyperFaaS platform as a whole, including the load balancer, worker, and database.
Having a running docker compose version of HyperFaaS is required for these tests to run.

Requirements:

- running docker compose version of HyperFaaS
- environment variables set for the addresses of the load balancer, worker, and database
- Images:
  - "hyperfaas-hello:latest"
  - "hyperfaas-echo:latest"
  - "hyperfaas-simul:latest"
  - "hyperfaas-crash:latest"