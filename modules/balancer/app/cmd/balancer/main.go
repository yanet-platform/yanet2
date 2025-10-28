package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func listen(cfg *balancer.Config, balancer *balancer.BalancerModule, log *zap.SugaredLogger) {
	lis, err := net.Listen("tcp", cfg.Endpoint)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()

	balancer.RegisterService(srv)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	reflection.Register(srv)

	log.Infof("Registered balancer and helth servers")

	go func() {
		log.Infof("gRPC server listening on %s", lis.Addr())
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Infow("shutting down...")
	srv.GracefulStop()
}

func main() {
	zapL, _ := zap.NewProduction()
	log := zapL.Sugar()
	cfg := balancer.DefaultConfig()
	log.Infof("creating new balancer module with default config: %s", cfg)
	balancer, err := balancer.NewBalancerModule(cfg, log)
	if err != nil {
		log.Fatalf("failed to create new balancer module %v", err)
		os.Exit(1)
	}
	log.Infof("created balancer service!")
	go func() {
		if err = balancer.Run(context.Background()); err != nil {
			log.Errorf("failed to run balancer background jobs %v", err)
			os.Exit(1)
		}
	}()

	log.Infof("going to listen...")

	listen(cfg, balancer, log)
}
