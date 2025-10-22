This package is reserved for end to end testing of HyperFaaS.

Other tests, such as unit or integration tests, should always be placed right next to the code that is being tested.

This package is used for testing the HyperFaaS platform as a whole, including HAProxy, the routing controller, the leaves, the workers, and etcd.
Having a running docker compose version of HyperFaaS is required for these tests to run.

Requirements:

- running docker compose version of  full HyperFaaS (full-compose.yaml in root directory)
- environment variables set for the addresses of HAProxy, a leaf, a worker, and etcd IF you want to override the default values
- Images:
  - "hyperfaas-hello:latest"
  - "hyperfaas-echo:latest"
  - "hyperfaas-simul:latest"
  - "hyperfaas-crash:latest"