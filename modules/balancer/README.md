# Balancer

- Balancer Module:
   - C API for controlplane
   - Application (useful for tests, not used in the final build)
   - CLI
   - Go controlplane with gRPC service
   - Dataplane responsible for packets processing
   - Unit tests
   - Regression tests using Testing Framework (`../../tests/functional/balancer_test.go`)

## TODO
- WLC (weighted least connections)
- ICMP
- Counters
- Integration with monolive
- More tests
