package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xcmd"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	birdAdapter "github.com/yanet-platform/yanet2/operators/bird-adapter"
	adapterpb "github.com/yanet-platform/yanet2/operators/bird-adapter/adapterpb/v1"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/bird"
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
	// RouteOperatorEndpoint is the gRPC endpoint serving the route operator's
	// RouteService for RIB updates — either the route operator directly or the
	// gateway that proxies it.
	RouteOperatorEndpoint string `yaml:"route_operator_endpoint"`
	// BIRD configures the BIRD import applied at startup.
	BIRD xcfg.Optional[BIRDConfig] `yaml:"bird"`
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
		ListenAddr:            "localhost:50051",
		RouteOperatorEndpoint: "[::1]:8080",
	}
}

// BIRDConfig describes the BIRD import to set up when the server starts.
//
// Set name to "" (not a bare `name:`, which stays at its default) to
// disable the startup import — the server then starts bare and expects the
// client subcommand to configure it later at runtime.
type BIRDConfig struct {
	Name     string     `yaml:"name"`
	Sockets  []string   `yaml:"sockets"`
	SourceV4 netip.Addr `yaml:"source_v4"`
	SourceV6 netip.Addr `yaml:"source_v6"`
}

func (m *BIRDConfig) Default() {
	*m = BIRDConfig{
		Name: "route0",
		Sockets: []string{
			"/var/run/bird/yanet-master4.sock",
			"/var/run/bird/yanet-master6.sock",
		},
		SourceV4: netip.MustParseAddr("127.0.0.1"),
		SourceV6: netip.MustParseAddr("::1"),
	}
}

// Validate checks that the config is structurally sound.
func (m *BIRDConfig) Validate() error {
	if m.Name == "" {
		return nil
	}

	if len(m.Sockets) == 0 {
		return errors.New("at least one socket must be configured")
	}
	if !m.SourceV4.Is4() {
		return fmt.Errorf("source_v4 %q is not an IPv4 address", m.SourceV4)
	}
	if !m.SourceV6.Is6() || m.SourceV6.Is4In6() {
		return fmt.Errorf("source_v6 %q is not a pure IPv6 address", m.SourceV6)
	}

	return nil
}

func runServer() error {
	cfg, err := xcfg.LoadConfig[ServerConfig](serverCmdArgs.ConfigPath, xcfg.WithKnownFields(), xcfg.WithEnv())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, _, err := logging.Init(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer log.Sync()

	log.Info("starting BIRD adapter service",
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("route_operator_endpoint", cfg.RouteOperatorEndpoint),
	)

	// Create the adapter service
	adapterService := birdAdapter.NewAdapterService(cfg.RouteOperatorEndpoint, birdAdapter.WithAdapterServiceLog(log))

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
		log.Info("gRPC server listening", zap.String("addr", cfg.ListenAddr))
		if err := grpcServer.Serve(listener); err != nil {
			return fmt.Errorf("gRPC server failed: %w", err)
		}
		return nil
	})

	if startupConfig := cfg.BIRD.Unwrap(); startupConfig != nil {
		// Apply the configured startup BIRD import.
		wg.Go(func() error {
			runStartupImport(ctx, log, adapterService, *startupConfig, cfg.Logging.Level.String())
			return nil
		})
	}

	// Wait for interrupt signal
	wg.Go(func() error {
		err := xcmd.WaitInterrupted(ctx)
		log.Info("caught signal", zap.Error(err))
		log.Info("shutting down gRPC server")

		xgrpc.StopGracefully(grpcServer, xgrpc.GracefulStopTimeout, func() {
			log.Warn("graceful stop timed out, forcing shutdown",
				zap.Duration("grace_period", xgrpc.GracefulStopTimeout),
			)
		})

		return err
	})

	return wg.Wait()
}

// runStartupImport applies the configured startup BIRD import, retrying
// with exponential backoff until it succeeds or ctx is cancelled.
//
// An empty cfg.Name means no startup import is configured. The route
// operator SetupImport talks to may not be reachable yet when the server
// starts, so a single failed attempt must not abort the process — the
// listener has to come up regardless, and the import catches up once the
// operator answers.
func runStartupImport(ctx context.Context, log *zap.Logger, svc *birdAdapter.AdapterService, cfg BIRDConfig, logLevel string) {
	if cfg.Name == "" {
		log.Info("no startup BIRD import configured")
		return
	}

	importCfg := bird.DefaultConfig()
	importCfg.Sockets = cfg.Sockets

	params := birdAdapter.ImportParams{
		Name:     cfg.Name,
		Config:   importCfg,
		SourceV4: cfg.SourceV4,
		SourceV6: cfg.SourceV6,
		LogLevel: logLevel,
	}

	runBackoff := backoff.ExponentialBackOff{
		InitialInterval:     backoff.DefaultInitialInterval,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         30 * time.Second,
	}
	runBackoff.Reset()

	for {
		err := svc.SetupImport(params)
		if err == nil {
			log.Info("startup BIRD import configured", zap.String("name", cfg.Name))
			return
		}

		log.Warn("startup BIRD import attempt failed, retrying",
			zap.String("name", cfg.Name),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(runBackoff.NextBackOff()):
		}
	}
}
