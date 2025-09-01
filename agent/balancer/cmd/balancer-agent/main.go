package main

import (
	"context"
	"log"
	"os"

	"github.com/yanet-platform/yanet2/agent/balancer/internal/app"
)

func main() {
	config, err := app.LoadConfig(os.Args[1])
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	app, err := app.New(config)
	if err != nil {
		log.Fatalf("failed to init app: %v", err)
	}
	err = app.Run(context.Background())
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}
}
