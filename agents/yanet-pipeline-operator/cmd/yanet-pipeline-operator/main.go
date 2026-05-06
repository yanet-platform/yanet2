package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	_ "google.golang.org/grpc/encoding/gzip"

	op "github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/internal/operator"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

func main() {
	err := operator.Run(
		"yanet-pipeline-operator",
		"YANET pipeline operator — manages pipelines and device bindings",
		build,
	)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func build(cfg *op.Config, log *zap.Logger) (operator.Runnable, error) {
	return op.NewOperator(cfg, op.WithLog(log))
}
