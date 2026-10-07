// Command example-controlplane is the control-plane daemon of the reference
// out-of-tree module.
//
// It shows the deployment shape an out-of-tree module takes instead of
// being compiled into yncp-director: the daemon attaches to the dataplane's
// shared memory, serves the module's gRPC service on its own endpoint, and
// heartbeats itself into the gateway's backend registry so CLIs routed
// through the gateway reach it. See docs/module-sdk.md.
package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/common/go/operator"
	example "github.com/yanet-platform/yanet2/sdk/example/controlplane"
)

func main() {
	err := operator.Run[Config](
		"yanet-example-controlplane",
		"YANET example module control plane (out-of-tree module SDK reference)",
		buildDaemon,
	)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func buildDaemon(cfg *Config, log *zap.Logger) (operator.Runnable, error) {
	module, err := example.NewExampleModule(&cfg.Module, example.WithLog(log))
	if err != nil {
		return nil, err
	}

	return newDaemon(cfg, module, WithLog(log))
}
