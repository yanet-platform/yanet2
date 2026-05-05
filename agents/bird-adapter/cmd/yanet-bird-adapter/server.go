package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"

	birdAdapter "github.com/yanet-platform/yanet2/agents/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/agents/bird-adapter/adapterpb"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xcmd"
)

var serverCmdArgs struct {
	ConfigPath string
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the BIRD adapter gRPC server",
	Run: func(cmd *cobra.Command, args []string) {
		if err := runServer(); err != nil {
			if errors.Is(err, xcmd.Interrupted{}) {
				return
			}

			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	serverCmd.Flags().StringVarP(&serverCmdArgs.ConfigPath, "config", "c", "", "Path to the configuration file (required)")
	serverCmd.MarkFlagRequired("config")
}

// ServerConfig is the configuration for the bird-adapter server.
type ServerConfig struct {
	// Logging configuration.
	Logging logging.Config `yaml:"logging"`
	// ListenAddr is the gRPC endpoint to listen on (e.g., "localhost:50051").
	ListenAddr string `yaml:"listen_addr"`
	// GatewayEndpoint is the gRPC endpoint of the RouteService gateway for RIB updates.
	GatewayEndpoint string `yaml:"gateway_endpoint"`
}

func (m *ServerConfig) Default() {
	*m = *DefaultServerConfig()
}

// DefaultServerConfig returns the default configuration.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Logging: logging.Config{
			Level: zapcore.InfoLevel,
		},
		ListenAddr:      "localhost:50051",
		GatewayEndpoint: "localhost:50052",
	}
}

func runServer() error {
	cfg, err := xcfg.LoadConfig[ServerConfig](serverCmdArgs.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, _, err := logging.Init(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer log.Sync()

	log.Infow("starting BIRD adapter service",
		"listen_addr", cfg.ListenAddr,
		"gateway_endpoint", cfg.GatewayEndpoint,
	)

	// Create the adapter service
	adapterService := birdAdapter.NewAdapterService(cfg.GatewayEndpoint, log.Desugar())

	// Create gRPC server
	grpcServer := grpc.NewServer()
	adapterpb.RegisterAdapterServiceServer(grpcServer, adapterService)

	// Listen on the configured address
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", cfg.ListenAddr, err)
	}

	wg, ctx := errgroup.WithContext(context.Background())

	// Start gRPC server
	wg.Go(func() error {
		log.Infof("gRPC server listening on %s", cfg.ListenAddr)
		if err := grpcServer.Serve(listener); err != nil {
			return fmt.Errorf("gRPC server failed: %w", err)
		}
		return nil
	})

	// Wait for interrupt signal
	wg.Go(func() error {
		err := xcmd.WaitInterrupted(ctx)
		log.Infof("caught signal: %v", err)
		log.Info("shutting down gRPC server")
		grpcServer.GracefulStop()
		return err
	})

	return wg.Wait()
}
