package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	op "github.com/yanet-platform/yanet2/operators/neighbour-sidecar/internal/operator"
)

func main() {
	err := operator.Run(
		"yanet-neighbour-sidecar",
		"Discover kernel neighbours and publish them to the route operator",
		factory,
	)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func factory(cfg *op.Config, log *zap.Logger) (operator.Runnable, error) {
	return op.NewOperator(cfg, op.WithLog(log))
}
