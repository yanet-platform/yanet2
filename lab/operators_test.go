package lab_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/lab/internal/operatorwait"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func TestMatchesForwardingProbe(t *testing.T) {
	expected := lab.ForwardingExpectedBytes()

	cases := []struct {
		name   string
		packet []byte
		want   bool
	}{
		{name: "exact match", packet: append([]byte(nil), expected...), want: true},
		{name: "zero-padded to 60 bytes", packet: append(append([]byte(nil), expected...), make([]byte, 60-len(expected))...), want: true},
		{name: "short packet", packet: expected[:len(expected)-5], want: false},
		{name: "empty packet", packet: []byte{}, want: false},
		{name: "nil packet", packet: nil, want: false},
		{name: "mismatched content", packet: make([]byte, len(expected)), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lab.MatchesForwardingProbe(tc.packet)
			if got != tc.want {
				t.Errorf("MatchesForwardingProbe() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequiredArtifacts(t *testing.T) {
	root := t.TempDir()

	artifacts := lab.RequiredArtifacts(root)
	if len(artifacts) == 0 {
		t.Fatal("RequiredArtifacts returned no paths")
	}

	for _, path := range artifacts {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	artifacts = lab.RequiredArtifacts(root)
	for _, path := range artifacts {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("RequiredArtifacts path %q does not exist: %v", path, err)
		}
	}

	expected := []string{
		filepath.Join(root, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(root, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(root, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
		filepath.Join(root, "build", "operators", "route", "yanet-route-operator"),
		filepath.Join(root, "build", "operators", "forward", "yanet-forward-operator"),
		filepath.Join(root, "build", "operators", "decap", "yanet-decap-operator"),
		filepath.Join(root, "build", "operators", "pipeline", "yanet-pipeline-operator"),
		filepath.Join(root, "build", "operators", "bird-adapter", "yanet-bird-adapter"),
	}
	for _, name := range framework.CLIBinaryNames {
		expected = append(expected, filepath.Join(root, "target", "release", name))
	}
	for _, name := range []string{"yanet-cli-ready", "yanet-cli-operator-route", "yanet-cli-operator-neighbour", "yanet-cli-operator-pipeline"} {
		expected = append(expected, filepath.Join(root, "target", "release", name))
	}
	require.Equal(t, expected, artifacts)
}

// Test_OperatorFingerprintFiles_IncludesListenerWait verifies that readiness
// changes invalidate the cached operator baseline.
func Test_OperatorFingerprintFiles_IncludesListenerWait(t *testing.T) {
	require.Contains(t, lab.OperatorFingerprintFiles(), "lab/internal/operatorwait/bird.go")
}

// Test_PrepareOperators_StagingFailureNamesCommand verifies that a failed
// guest staging command remains identifiable without a running VM.
func Test_PrepareOperators_StagingFailureNamesCommand(t *testing.T) {
	root := t.TempDir()
	instance, err := framework.New(&framework.Config{
		Name:        "operator-staging-error",
		QEMUImage:   filepath.Join(root, "image.qcow2"),
		ProjectRoot: root,
	})
	require.NoError(t, err)
	fw := instance.Global()
	t.Cleanup(func() { _ = fw.Stop() })

	err = lab.PrepareOperators(fw)

	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir -p /tmp/yanet/operators")
	require.Contains(t, err.Error(), "QEMU VM is not running")
	require.EqualError(t, errors.Unwrap(err), "QEMU VM is not running")
}

func TestScopeNamesAD11Membership(t *testing.T) {
	require.Equal(t, []string{
		"dataplane",
		"controlplane",
		"route-operator",
		"forward-operator",
		"decap-operator",
		"pipeline-operator",
		"bird",
		"bird-adapter",
		"ready-route",
		"ready-forward",
		"ready-decap",
		"ready-pipeline",
		"route0-session",
		"imported-route-v4",
		"imported-route-v6",
	}, lab.ScopeNames())
}

func TestParseScopeStatus(t *testing.T) {
	cases := []struct {
		name   string
		output string
		assert func(*testing.T, []lab.ScopeResult)
	}{
		{
			name:   "ready matrix",
			output: statusStatusLines(nil),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Len(t, scopes, len(lab.ScopeNames()))
				for _, scope := range scopes {
					require.Equal(t, lab.StateReady, scope.State)
					require.Empty(t, scope.Reason)
				}
			},
		},
		{
			name:   "single scope failure",
			output: statusStatusLines(map[string]string{"bird": "not_ready"}),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "bird").State)
				require.Equal(t, "bird process is not running", scopeResultByName(scopes, "bird").Reason)
				require.Equal(t, lab.StateReady, scopeResultByName(scopes, "dataplane").State)
			},
		},
		{
			name:   "multi scope failure",
			output: statusStatusLines(map[string]string{"route-operator": "not_ready", "imported-route-v6": "not_ready"}),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "route-operator").State)
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "imported-route-v6").State)
			},
		},
		{
			name:   "degraded maps to not ready",
			output: statusStatusLines(map[string]string{"ready-decap": "degraded"}),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "ready-decap").State)
				require.Equal(t, "readiness degraded", scopeResultByName(scopes, "ready-decap").Reason)
			},
		},
		{
			name:   "unknown state is malformed",
			output: statusStatusLines(map[string]string{"ready-route": "unknown"}),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "ready-route").State)
				require.Equal(t, "malformed status output", scopeResultByName(scopes, "ready-route").Reason)
			},
		},
		{
			name:   "unknown scope fails closed",
			output: statusStatusLines(nil) + "\nYANET2_SCOPE forged ready",
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Len(t, scopes, len(lab.ScopeNames())+1)
				require.Equal(t, lab.ScopeResult{Name: "forged", State: lab.StateNotReady, Reason: "unknown scope"}, scopes[len(scopes)-1])
			},
		},
		{
			name:   "missing scope line",
			output: strings.Replace(statusStatusLines(nil), "YANET2_SCOPE bird ready\n", "", 1),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "bird").State)
				require.Equal(t, "status output missing", scopeResultByName(scopes, "bird").Reason)
			},
		},
		{
			name:   "truncated scope line",
			output: strings.Replace(statusStatusLines(nil), "YANET2_SCOPE bird ready", "YANET2_SCOPE bird", 1),
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "bird").State)
				require.Equal(t, "malformed status output", scopeResultByName(scopes, "bird").Reason)
			},
		},
		{
			name:   "duplicate scope line",
			output: statusStatusLines(nil) + "\nYANET2_SCOPE bird ready",
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Equal(t, lab.StateNotReady, scopeResultByName(scopes, "bird").State)
				require.Equal(t, "duplicate status output", scopeResultByName(scopes, "bird").Reason)
			},
		},
		{
			name:   "empty output fails closed",
			output: "",
			assert: func(t *testing.T, scopes []lab.ScopeResult) {
				require.Len(t, scopes, len(lab.ScopeNames()))
				for _, scope := range scopes {
					require.Equal(t, lab.StateNotReady, scope.State)
					require.Equal(t, "status output missing", scope.Reason)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, lab.ParseScopeStatus(tc.output))
		})
	}
}

// TestStatusCommandFormatContract pins the generated status command to its
// parser: the script BuildOperatorStatusCommand emits must, when run under sh
// with synthetic probes, produce output that ParseScopeStatusFor turns back
// into the configured outcomes. A drift in the marker, field count, or state
// token would otherwise ship an all-scopes-failed live status with green unit
// tests.
func TestStatusCommandFormatContract(t *testing.T) {
	scopes := []lab.OperatorScope{
		{Name: "a-ready", Command: "true", Reason: "r-a"},
		{Name: "b-down", Command: "false", Reason: "r-b"},
	}
	script := lab.BuildOperatorStatusCommand(scopes)
	require.NotContains(t, script, "\n")
	output, err := exec.Command("sh", "-c", "echo start; "+script+"; echo end").CombinedOutput()
	require.NoError(t, err)
	require.Contains(t, string(output), "end")
	results := lab.ParseScopeStatusFor(string(output), scopes)
	require.Len(t, results, 2)
	require.Equal(t, lab.StateReady, results[0].State)
	require.Empty(t, results[0].Reason)
	require.Equal(t, lab.StateNotReady, results[1].State)
	require.Equal(t, "r-b", results[1].Reason)
}

// statusStatusLines renders one marked status line per scope, overriding the
// state of named scopes.
func statusStatusLines(overrides map[string]string) string {
	lines := make([]string, 0, len(lab.ScopeNames()))
	for _, name := range lab.ScopeNames() {
		state := "ready"
		if override, present := overrides[name]; present {
			state = override
		}
		lines = append(lines, fmt.Sprintf("YANET2_SCOPE %s %s", name, state))
	}
	return strings.Join(lines, "\n")
}

func scopeResultByName(scopes []lab.ScopeResult, name string) lab.ScopeResult {
	for _, scope := range scopes {
		if scope.Name == name {
			return scope
		}
	}
	return lab.ScopeResult{}
}

// Test_BirdAdapterWait_ListenerReadyContinues verifies that readiness is
// accepted without an additional fixed startup delay.
func Test_BirdAdapterWait_ListenerReadyContinues(t *testing.T) {
	var command string
	var timeout time.Duration
	err := operatorwait.BirdAdapter(func(value string, valueTimeout time.Duration) (string, error) {
		command = value
		timeout = valueTimeout
		return "", nil
	})

	require.NoError(t, err)
	require.Contains(t, command, "/dev/tcp/127.0.0.1/50051")
	require.Equal(t, 65*time.Second, timeout)
}

// Test_BirdAdapterWait_ListenerTimeoutIncludesDiagnostics verifies that a
// bounded readiness failure retains adapter logs for startup diagnosis.
func Test_BirdAdapterWait_ListenerTimeoutIncludesDiagnostics(t *testing.T) {
	calls := 0
	err := operatorwait.BirdAdapter(func(command string, timeout time.Duration) (string, error) {
		calls++
		if calls == 1 {
			require.Contains(t, command, "/dev/tcp/127.0.0.1/50051")
			require.Equal(t, 65*time.Second, timeout)
			return "", errors.New("listener timeout")
		}
		require.Equal(t, "tail -c 8192 /tmp/yanet/logs/yanet-bird-adapter.log", command)
		require.Equal(t, 10*time.Second, timeout)
		return "adapter failed to bind", nil
	})

	require.Equal(t, 2, calls)
	require.ErrorContains(t, err, "listener timeout")
	require.ErrorContains(t, err, "adapter failed to bind")
}

// Test_BirdAdapterWait_DiagnosticsFailurePreservesBothErrors verifies that
// readiness and bounded diagnostic failures remain visible together.
func Test_BirdAdapterWait_DiagnosticsFailurePreservesBothErrors(t *testing.T) {
	calls := 0
	err := operatorwait.BirdAdapter(func(string, time.Duration) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("listener timeout")
		}
		return "partial adapter output", errors.New("diagnostic timeout")
	})

	require.ErrorContains(t, err, "listener timeout")
	require.ErrorContains(t, err, "diagnostic timeout")
	require.ErrorContains(t, err, "partial adapter output")
}

// Test_BirdAdapterWait_DelayedListenerReceivesNoApplicationData verifies that
// the real readiness probe accepts a late listener without writing bytes.
func Test_BirdAdapterWait_DelayedListenerReceivesNoApplicationData(t *testing.T) {
	type listenerResult struct {
		data []byte
		err  error
	}
	reservedListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(reservedListener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, reservedListener.Close())
	result := make(chan listenerResult, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		listener, err := net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			result <- listenerResult{err: err}
			return
		}
		defer listener.Close()
		connection, err := listener.Accept()
		if err != nil {
			result <- listenerResult{err: err}
			return
		}
		defer connection.Close()
		_ = connection.SetReadDeadline(time.Now().Add(time.Second))
		data, err := io.ReadAll(connection)
		result <- listenerResult{data: data, err: err}
	}()

	err = operatorwait.BirdAdapter(func(command string, timeout time.Duration) (string, error) {
		command = strings.Replace(command, "127.0.0.1/50051", "127.0.0.1/"+port, 1)
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		defer cancel()
		output, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
		return string(output), err
	})
	require.NoError(t, err)

	listenerResultValue := <-result
	require.NoError(t, listenerResultValue.err)
	require.Empty(t, listenerResultValue.data)
}
