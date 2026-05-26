// Command balancer2-fuzzer is the CLI entrypoint for the balancer2
// fuzzing/load application. It is intentionally a thin shim around
// fuzzing.RunMain so the full CLI flow can be exercised from unit
// tests in the fuzzing package without spawning a subprocess.
//
// Usage:
//
//	balancer2-fuzzer -config <path-to-runtime-config.yaml>
//
// See modules/balancer2/fuzzing/example.yaml for an annotated config.
package main

import (
	"os"

	"github.com/yanet-platform/yanet2/modules/balancer2/fuzzing"
)

func main() {
	os.Exit(fuzzing.RunMain(os.Args[1:], os.Stdout, os.Stderr))
}
