package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/coordinator"
)

var cmd Cmd

// Cmd is the command line arguments.
type Cmd struct {
	// ConfigPath is the path to the configuration file.
	ConfigPath string
}

var rootCmd = &cobra.Command{
	Use:   "yanet-coordinator",
	Short: "YANET Coordinator for orchestrating modules",
	Run: func(rawCmd *cobra.Command, _ []string) {
		if err := run(cmd); err != nil {
			if errors.Is(err, Interrupted{}) {
				return
			}

			fmt.Printf("ERROR: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.Flags().StringVarP(&cmd.ConfigPath, "config", "c", "", "Path to the configuration file (required)")
	rootCmd.MarkFlagRequired("config")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd Cmd) error {
	config := zap.NewDevelopmentConfig()
	config.Development = false
	config.Level.SetLevel(zap.DebugLevel)

	logger, err := config.Build()
	if err != nil {
		return fmt.Errorf("failed to build logger: %w", err)
	}
	defer logger.Sync()

	log := logger.Sugar()

	cfg, err := coordinator.LoadConfig(cmd.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	c, err := coordinator.NewCoordinator(
		cfg,
		coordinator.WithLog(log),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}
	defer c.Close()

	ctx := context.Background()
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return c.Run(ctx)
	})
	wg.Go(func() error {
		err := WaitInterrupted(ctx)
		log.Infof("caught signal: %v", err)
		return err
	})

	return wg.Wait()
}

type Interrupted struct {
	os.Signal
}

func (m Interrupted) Error() string {
	return m.String()
}

// WaitInterrupted blocks until either SIGINT or SIGTERM signal is received or
// the provided context is canceled.
func WaitInterrupted(ctx context.Context) error {
	ch := make(chan os.Signal, 1)

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	select {
	case v := <-ch:
		return Interrupted{Signal: v}
	case <-ctx.Done():
		return ctx.Err()
	}
}
