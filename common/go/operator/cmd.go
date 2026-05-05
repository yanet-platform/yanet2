package operator

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xcmd"
)

// Runnable is the minimal lifecycle surface the generic CLI helper drives.
//
// Operators satisfy it via *Operator[T].
type Runnable interface {
	Run(ctx context.Context) error
	Close() error
}

// loggingConfig is implemented by config types that need to initialise
// logging.
type loggingConfig interface {
	LoggingConfig() *logging.Config
}

// Run is the generic entry point for an operator binary.
//
// It wires up:
//   - CLI command setup with required --config flag.
//   - Loading and validating the YAML config.
//   - Initializing logging from the config.
//   - Constructing the Runnable from the build callback.
//   - Running the Runnable in an errgroup alongside xcmd.WaitInterrupted.
//
// Note that xcmd.Interrupted is treated as a clean shutdown.
func Run[C any](
	use string,
	short string,
	factory func(*C, *zap.Logger) (Runnable, error),
) error {
	var path string

	root := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := RunOperator(path, factory)
			if errors.Is(err, xcmd.Interrupted{}) {
				return nil
			}

			return err
		},
	}
	root.Flags().StringVarP(
		&path, "config", "c", "",
		"Path to the configuration file (required)",
	)
	if err := root.MarkFlagRequired("config"); err != nil {
		return fmt.Errorf("failed to mark --config required: %w", err)
	}

	return root.Execute()
}

// RunOperator loads the config specified by path and runs the Runnable
// returned by build until the process is interrupted or any goroutine
// returns an error.
func RunOperator[C any](
	path string,
	factory func(*C, *zap.Logger) (Runnable, error),
) error {
	cfg, err := xcfg.LoadConfig[C](path)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, err := initLogging(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer func() {
		// Don't know what to do if this fails, so just ignore it.
		_ = log.Sync()
	}()

	log.Debug("parsed config", zap.String("path", path), zap.Any("config", cfg))

	runnable, err := factory(cfg, log)
	if err != nil {
		return fmt.Errorf("failed to construct operator: %w", err)
	}
	defer func() {
		if err := runnable.Close(); err != nil {
			log.Warn("failed to close operator", zap.Error(err))
		}
	}()

	wg, ctx := errgroup.WithContext(context.Background())
	wg.Go(func() error {
		return runnable.Run(ctx)
	})
	wg.Go(func() error {
		return xcmd.WaitInterrupted(ctx)
	})

	return wg.Wait()
}

func initLogging(cfg any) (*zap.Logger, error) {
	provider, ok := cfg.(loggingConfig)
	if !ok {
		return zap.NewNop(), nil
	}

	log, _, err := logging.Init(provider.LoggingConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logging: %w", err)
	}

	return log.Desugar(), nil
}
