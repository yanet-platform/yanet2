package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/pkg/yncp"
)

var cmd Cmd

// Cmd is the command line arguments.
type Cmd struct {
	// ConfigPath is the path to the configuration file.
	ConfigPath string
}

var rootCmd = &cobra.Command{
	Use:   "yanet-controlplane-director",
	Short: "YANET API Gateway and director",
	Run: func(rawCmd *cobra.Command, args []string) {
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
	cfg, err := yncp.LoadConfig(cmd.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log, atomicLevel, err := yncp.InitLogging(&cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer log.Sync()

	director, err := yncp.NewDirector(cfg, yncp.WithLog(log), yncp.WithAtomicLogLevel(&atomicLevel))
	if err != nil {
		return fmt.Errorf("failed to create director: %w", err)
	}

	ctx := context.Background()
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return director.Run(ctx)
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
