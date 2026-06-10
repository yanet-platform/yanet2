package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/common/go/operator"
	op "github.com/yanet-platform/yanet2/operators/decap/internal/operator"
)

func main() {
	if err := operator.Run(
		"yanet-decap-operator",
		"YANET decap operator — owns N decap functions across gateways",
		factory,
	); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func factory(cfg *op.Config, log *zap.Logger) (operator.Runnable, error) {
	return op.NewOperator(cfg, op.WithLog(log))
}
