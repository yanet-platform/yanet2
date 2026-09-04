package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/common/go/operator"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

func main() {
	err := operator.Run(
		"yanet-netlink-dataplane-sidecar",
		"YANET host-network netlink dataplane sidecar",
		factory,
	)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func factory(
	cfg *sidecaroperator.Config,
	log *zap.Logger,
) (operator.Runnable, error) {
	return sidecaroperator.NewOperator(cfg, sidecaroperator.WithLog(log))
}
