package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	forwardop "github.com/yanet-platform/yanet2/operators/forward/internal/operator"
)

func main() {
	var (
		gateways    []string
		interval    time.Duration
		prefix      string
		ignorePdump bool
		logLevel    string
	)

	cmd := &cobra.Command{
		Use:   "yanet-forward-operator RULES_FILE",
		Short: "Reconciler that applies a forward function to one or more gateways",
		Long: `Manages exactly ONE forward function on the gateway. Run multiple
instances (e.g. via systemd template yanet2-forward-operator@) to
manage multiple functions.

RULES_FILE must be a path to a *.yaml forward-rules file. The function
is named <prefix><basename> where <basename> is the filename without .yaml.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return run(args[0], gateways, interval, prefix, ignorePdump, logLevel)
		},
	}

	cmd.Flags().StringArrayVarP(&gateways, "gateway", "g", nil, "gRPC endpoint, repeatable (default: grpc://[::1]:8080)")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 30*time.Second, "pause between iterations")
	cmd.Flags().StringVarP(&prefix, "function-prefix", "p", "fn:forward-", "function name prefix")
	cmd.Flags().BoolVar(&ignorePdump, "ignore-pdump", false, "preserve pdump modules already present in the function's default chain")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, error")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// run validates flags and drives the reconcile loop until interrupted.
func run(
	rulesFile string,
	gateways []string,
	interval time.Duration,
	prefix string,
	ignorePdump bool,
	logLevel string,
) error {
	if err := validateRulesFile(rulesFile); err != nil {
		return err
	}
	if len(gateways) == 0 {
		gateways = []string{"grpc://[::1]:8080"}
	}
	cfgName := strings.TrimSuffix(filepath.Base(rulesFile), ".yaml")
	functionName := prefix + cfgName

	var level zapcore.Level
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", logLevel, err)
	}

	log, _, err := logging.Init(&logging.Config{Level: level})
	if err != nil {
		return fmt.Errorf("failed to initialize logging: %w", err)
	}
	defer func() { _ = log.Sync() }()

	rules, err := forwardop.LoadForwardRules(rulesFile)
	if err != nil {
		return fmt.Errorf("failed to load rules: %w", err)
	}

	// Build per-gateway actuators. Each one dials once and holds the
	// connection for its lifetime.
	actuators := make([]operator.Actuator[forwardop.State], 0, len(gateways))
	closeActuators := func() {
		for _, a := range actuators {
			if err := a.Close(); err != nil {
				log.Warn("failed to close gateway actuator", zap.Error(err))
			}
		}
	}
	for _, gw := range gateways {
		a, err := forwardop.NewGatewayActuator(gw, cfgName, functionName, ignorePdump, forwardop.WithLog(log))
		if err != nil {
			closeActuators()
			return err
		}
		actuators = append(actuators, a)
	}

	fanOut := operator.NewFanOutActuator(actuators, operator.WithFanOutLog(log))

	source := forwardop.NewStaticSource(rules, forwardop.WithSourceLog(log))

	op := operator.NewOperator(
		fanOut,
		source,
		operator.WithLog(log),
		operator.WithReconcile(operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(interval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		}),
	)
	defer func() {
		if err := op.Close(); err != nil {
			log.Warn("failed to close operator", zap.Error(err))
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := op.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// validateRulesFile rejects paths that do not end in .yaml or that do
// not exist on disk.
func validateRulesFile(path string) error {
	if !strings.HasSuffix(path, ".yaml") {
		return fmt.Errorf("RULES_FILE must end with .yaml: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("not a file: %s", path)
	}
	return nil
}
