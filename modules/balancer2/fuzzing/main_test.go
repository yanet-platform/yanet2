package fuzzing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDialSeam swaps the dial seam for the duration of t and restores
// the production wiring on cleanup. fn observes every dial attempt.
func withDialSeam(
	t *testing.T,
	fn func(cfg *RuntimeConfig, stats *LatencyStats) (BalancerRPC, func() error, error),
) {
	t.Helper()
	prev := dialRPCClient
	dialRPCClient = fn
	t.Cleanup(func() { dialRPCClient = prev })
}

// withRunnerSeam swaps the runner-construction seam for the duration
// of t. fn returns a fake runner whose Run method drives the CLI exit
// path under test.
func withRunnerSeam(
	t *testing.T,
	fn func(cfg *RuntimeConfig, corpus *Corpus, rpc BalancerRPC, stats *LatencyStats) (runnerLike, error),
) {
	t.Helper()
	prev := newRunner
	newRunner = fn
	t.Cleanup(func() { newRunner = prev })
}

// fakeRunner is a runnerLike whose Run method returns a pre-programmed
// error. It is used by RunMain tests to exercise the graceful-stop and
// error-propagation paths without standing up a real runner.
type fakeRunner struct {
	err   error
	calls int
}

func (m *fakeRunner) Run(_ context.Context) error {
	m.calls++
	return m.err
}

// writeConfigFile drops a minimal valid runtime config alongside the
// in-repo corpus so RunMain has everything it needs to reach the dial
// step. The corpus path is computed relative to the package directory
// so the test does not depend on the test working directory.
func writeConfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	corpusPath := filepath.Join(corpusFixtureDir(t), "taxi.services.conf")
	body := strings.Join([]string{
		`endpoint: "127.0.0.1:0"`,
		`corpus_path: "` + corpusPath + `"`,
		`config_name: "balancer2-fuzz"`,
		`operation_interval: "1ms"`,
		`update_vs_every: 1`,
		`stats_interval: "1s"`,
		`request_timeout: "500ms"`,
		`seed: 42`,
		``,
	}, "\n")
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o644))
	return configPath
}

// corpusFixtureDir returns the absolute path to the in-repo corpus
// directory. ParseServicesCorpus is filesystem-bound, so the test must
// hand it a path that exists at test time.
func corpusFixtureDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

// TestMainHelp confirms that -help prints usage on stdout, names the
// -config flag, returns exit code 0, and does NOT dial the
// controlplane. Operators rely on -help to inspect the binary on hosts
// where no controlplane is reachable.
func TestMainHelp(t *testing.T) {
	var dialed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		dialed++
		return nil, nil, errors.New("dial must not happen during -help")
	})

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-help"}, &stdout, &stderr)
	require.Equal(t, 0, code, "help must exit 0, stderr=%q", stderr.String())
	assert.Equal(t, 0, dialed, "help must not dial the controlplane")

	out := stdout.String()
	assert.Contains(t, out, "-config", "help output must document -config")
}

// TestMainMissingConfigFlag confirms that running the binary with no
// arguments fails fast (non-zero exit), emits a diagnostic mentioning
// -config, and never dials the controlplane.
func TestMainMissingConfigFlag(t *testing.T) {
	var dialed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		dialed++
		return nil, nil, errors.New("dial must not happen without -config")
	})

	var stdout, stderr bytes.Buffer
	code := RunMain(nil, &stdout, &stderr)
	require.NotEqual(t, 0, code, "missing -config must produce a non-zero exit")
	assert.Equal(t, 0, dialed, "missing -config must not dial the controlplane")
	assert.Contains(t, stderr.String(), "-config")
}

// TestMainMissingConfigFile asserts that a non-existent config path
// produces a non-zero exit and never reaches the dial step.
func TestMainMissingConfigFile(t *testing.T) {
	var dialed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		dialed++
		return nil, nil, errors.New("dial must not happen when config file is missing")
	})

	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", missing}, &stdout, &stderr)
	require.NotEqual(t, 0, code, "missing config file must produce a non-zero exit")
	assert.Equal(t, 0, dialed, "missing config file must not dial the controlplane")
	assert.Contains(t, stderr.String(), "load config")
}

// TestMainInvalidConfig confirms that a syntactically valid but
// semantically invalid runtime config (missing endpoint) aborts before
// dialling. This pins the "validate before dial" guarantee that the
// task acceptance criteria require.
func TestMainInvalidConfig(t *testing.T) {
	var dialed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		dialed++
		return nil, nil, errors.New("dial must not happen for invalid config")
	})

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	// Endpoint deliberately omitted to trip Validate().
	body := strings.Join([]string{
		`corpus_path: "/dev/null"`,
		`config_name: "x"`,
		`operation_interval: "1ms"`,
		`update_vs_every: 1`,
		`stats_interval: "1s"`,
		`request_timeout: "1s"`,
		``,
	}, "\n")
	require.NoError(t, os.WriteFile(bad, []byte(body), 0o644))

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", bad}, &stdout, &stderr)
	require.NotEqual(t, 0, code)
	assert.Equal(t, 0, dialed, "invalid config must not dial")
	assert.Contains(t, stderr.String(), "endpoint")
}

// TestMainLogsEffectiveSeed confirms that the effective seed is
// printed to stdout before the runner is invoked. Replay support
// depends on this output being captured even when the runner exits
// with an error.
func TestMainLogsEffectiveSeed(t *testing.T) {
	configPath := writeConfigFile(t)

	var closed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		return &recordingRPC{}, func() error { closed++; return nil }, nil
	})

	fake := &fakeRunner{}
	withRunnerSeam(t, func(*RuntimeConfig, *Corpus, BalancerRPC, *LatencyStats) (runnerLike, error) {
		return fake, nil
	})

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", configPath}, &stdout, &stderr)
	require.Equal(t, 0, code, "graceful runner stop must map to exit 0, stderr=%q", stderr.String())
	assert.Equal(t, 1, fake.calls, "runner must run exactly once")
	assert.Equal(t, 1, closed, "RPC closer must be invoked on shutdown")
	assert.Contains(t, stdout.String(), "effective_seed=42",
		"effective seed must be logged for replay")
}

// TestMainRunnerErrorMapsToNonZero asserts the documented "nil → 0,
// non-nil → non-zero" exit contract for runner.Run.
func TestMainRunnerErrorMapsToNonZero(t *testing.T) {
	configPath := writeConfigFile(t)

	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		return &recordingRPC{}, func() error { return nil }, nil
	})

	withRunnerSeam(t, func(*RuntimeConfig, *Corpus, BalancerRPC, *LatencyStats) (runnerLike, error) {
		return &fakeRunner{err: errors.New("synthetic runner failure")}, nil
	})

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", configPath}, &stdout, &stderr)
	require.NotEqual(t, 0, code)
	assert.Contains(t, stderr.String(), "synthetic runner failure")
}

// TestMainDialErrorMapsToNonZero confirms that dial failures abort the
// CLI with a non-zero exit and never reach the runner. The runner seam
// is wired to fail loudly so an accidental invocation surfaces.
func TestMainDialErrorMapsToNonZero(t *testing.T) {
	configPath := writeConfigFile(t)

	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		return nil, nil, errors.New("synthetic dial failure")
	})

	withRunnerSeam(t, func(*RuntimeConfig, *Corpus, BalancerRPC, *LatencyStats) (runnerLike, error) {
		t.Fatal("runner must not be constructed after dial fails")
		return nil, nil
	})

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", configPath}, &stdout, &stderr)
	require.NotEqual(t, 0, code)
	assert.Contains(t, stderr.String(), "synthetic dial failure")
}

// TestMainRunnerConstructionErrorMapsToNonZero pins the failure mode
// when NewRunner rejects its arguments. The closer must still be
// invoked even though runner.Run never runs.
func TestMainRunnerConstructionErrorMapsToNonZero(t *testing.T) {
	configPath := writeConfigFile(t)

	var closed int
	withDialSeam(t, func(*RuntimeConfig, *LatencyStats) (BalancerRPC, func() error, error) {
		return &recordingRPC{}, func() error { closed++; return nil }, nil
	})

	withRunnerSeam(t, func(*RuntimeConfig, *Corpus, BalancerRPC, *LatencyStats) (runnerLike, error) {
		return nil, errors.New("synthetic runner construction failure")
	})

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{"-config", configPath}, &stdout, &stderr)
	require.NotEqual(t, 0, code)
	assert.Equal(t, 1, closed, "closer must run even when runner construction fails")
	assert.Contains(t, stderr.String(), "synthetic runner construction failure")
}
