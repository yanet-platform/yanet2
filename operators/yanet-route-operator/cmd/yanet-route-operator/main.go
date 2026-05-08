package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	_ "google.golang.org/grpc/encoding/gzip"

	op "github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/operator"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

func main() {
	err := operator.Run(
		"yanet-route-operator",
		"YANET route operator — owns RIB, neighbour tables and FIB build",
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
