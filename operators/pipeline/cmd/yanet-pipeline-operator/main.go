package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	op "github.com/yanet-platform/yanet2/operators/pipeline/internal/operator"
)

func main() {
	err := operator.Run(
		"yanet-pipeline-operator",
		"YANET pipeline operator — manages pipelines and device bindings",
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
