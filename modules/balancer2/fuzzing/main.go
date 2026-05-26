// Package fuzzing — CLI entrypoint plumbing.
//
// RunMain wires together the YAML config loader, corpus parser, latency
// stats, gRPC client, and operation runner. It is kept in the package
// (rather than under cmd/) so it can be unit-tested without spawning a
// subprocess: the dial step and the runner constructor are exposed via
// package-private function variables so tests can replace them with
// fakes that never touch the network.
//
// The corresponding main package under cmd/balancer2-fuzzer is a thin
// shim that calls RunMain(os.Args[1:], os.Stdout, os.Stderr) and
// forwards the exit code to os.Exit.

package fuzzing

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// dialRPCClient is the dial seam used by RunMain. Tests replace it with
// a fake that returns a recording BalancerRPC without touching the
// network. The default points at the production gRPC dialer.
var dialRPCClient = defaultDialRPCClient

// newRunner is the runner-construction seam used by RunMain. Tests
// replace it to inject a runner whose Run method returns immediately
// (or returns a known error) so the full CLI flow can be exercised
// without a live controlplane.
var newRunner = defaultNewRunner

// runnerLike is the subset of *Runner that RunMain depends on. It
// exists so tests can substitute a fake runner without constructing a
// real one.
type runnerLike interface {
	Run(ctx context.Context) error
}

// defaultDialRPCClient adapts DialRPCClient to the BalancerRPC seam
// signature. It is the production wiring.
func defaultDialRPCClient(
	cfg *RuntimeConfig,
	stats *LatencyStats,
) (BalancerRPC, func() error, error) {
	return DialRPCClient(cfg, stats)
}

// defaultNewRunner adapts NewRunner to the runnerLike seam signature.
// It is the production wiring.
func defaultNewRunner(
	cfg *RuntimeConfig,
	corpus *Corpus,
	rpc BalancerRPC,
	stats *LatencyStats,
) (runnerLike, error) {
	return NewRunner(cfg, corpus, rpc, stats)
}

// RunMain is the testable CLI entrypoint. It parses args, loads the
// runtime config, prints the effective seed, parses the corpus, dials
// the controlplane, builds a runner, and runs it. It returns 0 on
// graceful stop (Runner.Run returns nil) and a non-zero code on any
// error path. stdout receives operator-facing output (effective seed,
// help text); stderr receives error diagnostics.
//
// Argument handling:
//
//   - "-config <path>" — path to the YAML runtime config (required).
//   - "-help" or "-h" — print usage to stdout and return 0.
//
// Errors are reported as a single "balancer2-fuzzer: <message>" line on
// stderr; the same writer is used for flag-parse errors so callers can
// capture the full failure mode in tests.
func RunMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("balancer2-fuzzer", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPath := fs.String("config", "", "path to the YAML runtime config")

	fs.Usage = func() {
		fmt.Fprintf(stdout, "Usage: balancer2-fuzzer -config <path>\n\n")
		fmt.Fprintf(stdout, "Flags:\n")
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		fs.SetOutput(stderr)
	}

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError causes Parse to return ErrHelp when -h
		// or -help is supplied; treat that as a successful help
		// request rather than an error.
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	if *configPath == "" {
		fmt.Fprintln(stderr, "balancer2-fuzzer: -config is required")
		fs.Usage()
		return 2
	}

	cfg, err := LoadRuntimeConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: load config: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "balancer2-fuzzer: effective_seed=%d\n", cfg.Seed)

	corpus, err := ParseServicesCorpus(cfg.CorpusPath)
	if err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: parse corpus: %v\n", err)
		return 1
	}

	stats := NewLatencyStats()

	rpc, closer, err := dialRPCClient(cfg, stats)
	if err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: dial controlplane: %v\n", err)
		return 1
	}
	defer func() {
		if closer == nil {
			return
		}
		if cerr := closer(); cerr != nil {
			fmt.Fprintf(stderr, "balancer2-fuzzer: close controlplane connection: %v\n", cerr)
		}
	}()

	if err := resetConfigFromCorpus(context.Background(), cfg, corpus, rpc); err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: bootstrap config: %v\n", err)
		return 1
	}

	runner, err := newRunner(cfg, corpus, rpc, stats)
	if err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: build runner: %v\n", err)
		return 1
	}

	if err := runner.Run(context.Background()); err != nil {
		fmt.Fprintf(stderr, "balancer2-fuzzer: runner: %v\n", err)
		return 1
	}
	return 0
}

// resetConfigFromCorpus makes the target config deterministic before fuzzing:
// it replaces VSes for cfg.ConfigName with the corpus VS list via UpdateConfig.
func resetConfigFromCorpus(
	ctx context.Context,
	cfg *RuntimeConfig,
	corpus *Corpus,
	rpc BalancerRPC,
) error {
	if _, err := rpc.UpdateConfig(ctx, &balancerpb.UpdateConfigRequest{
		ConfigName: cfg.ConfigName,
		Vs:         corpus.ToVsConfigList(),
	}); err != nil {
		return fmt.Errorf("seed corpus vs: %w", err)
	}
	return nil
}
