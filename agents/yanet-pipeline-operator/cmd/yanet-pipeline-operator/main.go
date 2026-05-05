package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/internal/operator"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xcmd"
)

var rootArgs struct {
	ConfigPath string
}

var rootCmd = &cobra.Command{
	Use:   "yanet-pipeline-operator",
	Short: "YANET pipeline operator — manages pipelines and device bindings",
	RunE: func(cmd *cobra.Command, args []string) error {
		err := run()
		if errors.Is(err, xcmd.Interrupted{}) {
			return nil
		}

		return err
	},
}

func init() {
	rootCmd.Flags().StringVarP(
		&rootArgs.ConfigPath,
		"config", "c", "",
		"Path to the configuration file (required)",
	)
	rootCmd.MarkFlagRequired("config")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, xcmd.Interrupted{}) {
			return
		}
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := xcfg.LoadConfig[operator.Config](rootArgs.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, _, err := logging.Init(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer log.Sync()

	log.Debugw("parsed config", zap.Any("config", cfg))

	operator, err := operator.NewOperator(cfg, operator.WithLog(log.Desugar()))
	if err != nil {
		return fmt.Errorf("failed to construct operator: %w", err)
	}
	defer func() {
		if err := operator.Close(); err != nil {
			log.Warnw("failed to close operator", zap.Error(err))
		}
	}()

	wg, ctx := errgroup.WithContext(context.Background())
	wg.Go(func() error {
		return operator.Run(ctx)
	})
	wg.Go(func() error {
		return xcmd.WaitInterrupted(ctx)
	})

	return wg.Wait()
}
