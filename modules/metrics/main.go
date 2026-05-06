package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcmd"
	"github.com/yanet-platform/yanet2/modules/metrics/adapter"
	"github.com/yanet-platform/yanet2/modules/metrics/config"
	"github.com/yanet-platform/yanet2/modules/metrics/controller"
	"github.com/yanet-platform/yanet2/modules/metrics/format"
	grpchandler "github.com/yanet-platform/yanet2/modules/metrics/grpc"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "metrics.yaml", "Path to the configuration file")
	flag.Parse()

	if err := runServer(configPath); err != nil {
		if errors.Is(err, xcmd.Interrupted{}) {
			return
		}

		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func runServer(configPath string) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, _, err := logging.Init(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer log.Sync()

	log.Info("starting metric adapter server")

	formatKind, err := format.ParseFormat(cfg.Format)
	if err != nil {
		return fmt.Errorf("parse format: %w", err)
	}

	formatter, err := format.NewFormatter(formatKind, log.Named("formatter"))
	if err != nil {
		return fmt.Errorf("create formatter: %w", err)
	}

	clientConn, err := grpc.NewClient(
		cfg.ModulesEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create modules client: %w", err)
	}
	defer func() { _ = clientConn.Close() }()

	collector := adapter.NewCollector(clientConn, cfg.Modules, log.Named("collector"))
	ctrl := controller.NewController(formatter, collector, log.Named("controller"))

	grpcServer := grpc.NewServer()
	grpchandler.Register(grpcServer, ctrl, log.Named("grpc"))

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("listen on :%d: %w", cfg.Port, err)
	}

	wg, ctx := errgroup.WithContext(context.Background())

	wg.Go(func() error {
		if err := grpcServer.Serve(lis); err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
		return nil
	})

	wg.Go(func() error {
		err := xcmd.WaitInterrupted(ctx)
		log.Info("caught signal, shutting down gRPC server", zap.Error(err))
		grpcServer.GracefulStop()
		return err
	})

	return wg.Wait()
}
