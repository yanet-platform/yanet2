package framework

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestRestartYANETGuardsMissingConfig verifies that RestartYANET rejects a
// framework with no recorded dataplane configuration.
//
// The guard must return before touching anything that requires a live
// guest, so the call returns an error instead of panicking on a nil logger.
// It covers both a framework that never recorded anything and one that
// recorded a controlplane config but not a dataplane one, so the guard is
// pinned to the dataplane field rather than to either field being
// non-empty.
func TestRestartYANETGuardsMissingConfig(t *testing.T) {
	testCases := []struct {
		name string
		fw   *TestFramework
	}{
		{
			name: "nothing recorded",
			fw:   &TestFramework{},
		},
		{
			name: "controlplane config recorded but dataplane config still empty",
			fw:   &TestFramework{lastControlplaneConfig: "logging:\n  level: info\n"},
		},
	}

	for idx, testCase := range testCases {
		err := testCase.fw.RestartYANET()
		if err == nil {
			t.Fatalf("case %d (%s): RestartYANET() error = nil, want non-nil", idx, testCase.name)
		}

		if !strings.Contains(err.Error(), "no recorded configuration") {
			t.Errorf("case %d (%s): RestartYANET() error = %q, want it to describe the no-recorded-configuration case", idx, testCase.name, err.Error())
		}
	}
}

func TestReceiveAllPacketsUnfilteredReportsPartialFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	clientSocket := &SocketClient{inner: &socketClientInner{conn: client}}
	go func() {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], 4)
		_, _ = server.Write(length[:])
		_, _ = server.Write([]byte{1})
	}()

	_, err := clientSocket.ReceiveAllPacketsUnfiltered(20*time.Millisecond, "")

	if err == nil || !strings.Contains(err.Error(), "packet data") {
		t.Fatalf("error = %v, want partial packet error", err)
	}
}

// TestReceiveAllPacketsUnfilteredReturnsEmptyOnIdleTimeout pins the idle-window
// contract for drop probes: when the link stays quiet for the whole capture
// window, the receive returns an empty list with no error. This is what backs
// `expect: {drop: true}` in manifests.
func TestReceiveAllPacketsUnfilteredReturnsEmptyOnIdleTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	clientSocket := &SocketClient{inner: &socketClientInner{conn: client}}

	packets, err := clientSocket.ReceiveAllPacketsUnfiltered(20*time.Millisecond, "")
	if err != nil {
		t.Fatalf("idle capture returned error: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("idle capture returned %d packets, want 0", len(packets))
	}
}

// TestAdoptRunningConfigRecordsConfig verifies that AdoptRunningConfig
// records both configuration YAML documents.
//
// Recording both is what lets a later RestartYANET call get past its
// no-recorded-configuration guard.
func TestAdoptRunningConfigRecordsConfig(t *testing.T) {
	dataplaneConfig := "dataplane:\n  storage: /dev/hugepages/yanet\n"
	controlplaneConfig := "logging:\n  level: info\n"

	fw := &TestFramework{}

	fw.AdoptRunningConfig(dataplaneConfig, controlplaneConfig)

	if fw.lastDataplaneConfig != dataplaneConfig {
		t.Errorf("lastDataplaneConfig = %q, want %q", fw.lastDataplaneConfig, dataplaneConfig)
	}
	if fw.lastControlplaneConfig != controlplaneConfig {
		t.Errorf("lastControlplaneConfig = %q, want %q", fw.lastControlplaneConfig, controlplaneConfig)
	}
}

// TestPromptAwareSplit verifies the serial scanner split function emits
// newline-terminated lines normally, but also emits the unterminated shell
// prompt so readiness detection works without writing into the console
// during boot.
func TestPromptAwareSplit(t *testing.T) {
	prompt := "root@yanet-vm:~#"
	scan := func(input string) func(data []byte, atEOF bool) (int, []byte, error) {
		return func(data []byte, atEOF bool) (int, []byte, error) {
			return promptAwareSplit(data, atEOF)
		}
	}
	type token struct {
		input  string
		atEOF  bool
		expect string
	}
	testCases := []struct {
		name   string
		tokens []token
	}{
		{
			name: "normal lines",
			tokens: []token{
				{input: "Linux version 6.8.0\nmore stuff\n", atEOF: true, expect: "Linux version 6.8.0"},
				{input: "more stuff\n", atEOF: false, expect: "more stuff"},
			},
		},
		{
			name: "fragmented prompt at end without newline",
			tokens: []token{
				{input: "some boot log\nroot@yanet-vm:~#", atEOF: true, expect: "some boot log\nroot@yanet-vm:~#"},
			},
		},
		{
			name: "incomplete non-prompt data waits for more",
			tokens: []token{
				{input: "partial line without newline", atEOF: false, expect: ""},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, tok := range testCase.tokens {
				data := []byte(tok.input)
				advance, tokenBytes, err := scan("")(data, tok.atEOF)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tok.expect == "" {
					if advance != 0 || tokenBytes != nil {
						t.Fatalf("expected no token, got advance=%d token=%q", advance, string(tokenBytes))
					}
					continue
				}
				if string(tokenBytes) != tok.expect {
					t.Fatalf("token = %q, want %q (advance=%d)", string(tokenBytes), tok.expect, advance)
				}
			}
		})
	}

	// Verify the prompt is detected even mid-buffer.
	data := []byte("boot line\n" + prompt)
	advance, tokenBytes, err := promptAwareSplit(data, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(tokenBytes), prompt) {
		t.Fatalf("expected token to contain prompt, got %q (advance=%d)", string(tokenBytes), advance)
	}
}

// TestClassifyProcessExit verifies the three exit paths: clean exit,
// non-zero exit code, and signal death.
func TestClassifyProcessExit(t *testing.T) {
	// Clean exit (code 0).
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	msg := classifyProcessExit(cmd, nil)
	if !strings.Contains(msg, "exit code 0") {
		t.Fatalf("clean exit: got %q", msg)
	}

	// Non-zero exit code.
	cmd = exec.Command("false")
	err := cmd.Run()
	msg = classifyProcessExit(cmd, err)
	if !strings.Contains(msg, "exit code 1") {
		t.Fatalf("non-zero exit: got %q", msg)
	}

	// Signal death.
	cmd = exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	waitErr := cmd.Process.Kill()
	if waitErr != nil {
		t.Fatalf("kill: %v", waitErr)
	}
	err = cmd.Wait()
	msg = classifyProcessExit(cmd, err)
	if !strings.Contains(msg, "signal") {
		t.Fatalf("signal death: got %q", msg)
	}
}

// Test_Framework_Run_DependencyPolicy verifies that real child test outcomes
// preserve failure, skip dependencies, and leave independent parents runnable.
func Test_Framework_Run_DependencyPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
	}{
		{name: "all steps pass in order", scenario: "pass"},
		{name: "ordinary skip allows the later step", scenario: "skip"},
		{name: "fatal child failure skips the later step", scenario: "fatal"},
		{name: "nonfatal child failure skips the later step", scenario: "error"},
		{name: "child cleanup failure skips the later step", scenario: "cleanup"},
		{name: "nested child failure skips the later step", scenario: "nested"},
		{name: "failed parent skips the later step", scenario: "parent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "tool", "test2json", "-t", executable,
				"-test.run=^Test_Framework_Run_Process$", "-test.v=test2json", "-test.timeout=45s",
			)
			command.Env = append(os.Environ(), "FRAMEWORK_RUN_CASE="+tc.scenario)
			output, runErr := command.CombinedOutput()
			require.NoError(t, ctx.Err(), "%s", output)
			failed := tc.scenario != "pass" && tc.scenario != "skip"
			if failed {
				require.Error(t, runErr, "%s", output)
			} else {
				require.NoError(t, runErr, "%s", output)
			}
			actions := map[string]string{}
			var logs strings.Builder
			decoder := json.NewDecoder(bytes.NewReader(output))
			for {
				var event struct{ Action, Test, Output string }
				err := decoder.Decode(&event)
				if err == io.EOF {
					break
				}
				require.NoError(t, err, "%s", output)
				if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
					actions[event.Test] = event.Action
				}
				logs.WriteString(event.Output)
			}
			prefix := "Test_Framework_Run_Process/sequence"
			if failed {
				require.Equal(t, "fail", actions[prefix])
				require.Equal(t, "skip", actions[prefix+"/later"])
				require.Equal(t, "fail", actions[prefix+"/snapshot"])
				require.Contains(t, logs.String(), "failed to restore snapshot")
				require.NotContains(t, logs.String(), "LATER_BODY")
				require.Contains(t, logs.String(), "RESET_DELTA=0")
				if tc.scenario != "parent" {
					require.Equal(t, "fail", actions[prefix+"/first"])
					require.Contains(t, logs.String(), "FIRST_RESULT=false")
				}
			} else {
				require.Equal(t, "pass", actions[prefix])
				require.Equal(t, "pass", actions[prefix+"/later"])
				require.Contains(t, logs.String(), "FIRST_RESULT=true")
				require.Contains(t, logs.String(), "RESET_DELTA=1")
			}
			if tc.scenario == "skip" {
				require.Equal(t, "skip", actions[prefix+"/first"])
			}
			if tc.scenario == "pass" {
				require.Equal(t, "pass", actions[prefix+"/first"])
			}
			if tc.scenario == "nested" {
				require.Equal(t, "fail", actions[prefix+"/first/nested"])
			}
			require.Equal(t, "pass", actions["Test_Framework_Run_Process/fresh/step"])
			require.Contains(t, logs.String(), "LATER_RESULT=true")
			require.Contains(t, logs.String(), "POSTLUDE")
			require.Contains(t, logs.String(), "PARENT_CLEANUP")
			require.NotContains(t, logs.String(), "SNAPSHOT_BODY")
			require.NotContains(t, logs.String(), "BAD_ORDER")
		})
	}
}

// Test_Framework_Run_Process verifies that sequential steps use their child's
// test context; intentional failures are observed by a bounded parent process.
func Test_Framework_Run_Process(t *testing.T) {
	scenario := os.Getenv("FRAMEWORK_RUN_CASE")
	if scenario == "" {
		return
	}
	core, logs := observer.New(zap.DebugLevel)
	owner, err := New(&Config{
		Name: "dependent-steps", ProjectRoot: t.TempDir(),
	}, WithLog(zap.New(core).Sugar()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Global().Stop()) })
	t.Run("sequence", func(t *testing.T) {
		t.Cleanup(func() { t.Log("PARENT_CLEANUP") })
		bound := owner.ForTest(t)
		order := 0
		if scenario == "parent" {
			t.Error("intentional parent failure")
		} else {
			result := bound.Run("first", func(child *TestFramework, t *testing.T) {
				order++
				switch scenario {
				case "fatal":
					t.Fatal("intentional fatal failure")
				case "error":
					t.Error("intentional nonfatal failure")
				case "cleanup":
					t.Cleanup(func() { t.Error("intentional cleanup failure") })
				case "nested":
					child.Run("nested", func(_ *TestFramework, t *testing.T) {
						t.Error("intentional nested failure")
					})
				case "skip":
					t.Skip("ordinary skip")
				}
			})
			t.Logf("FIRST_RESULT=%t", result)
		}
		before := logs.FilterMessageSnippet("Resetting socket connections").Len()
		result := bound.Run("later", func(child *TestFramework, t *testing.T) {
			t.Log("LATER_BODY")
			if order != 1 {
				t.Error("BAD_ORDER")
			}
			order++
		})
		t.Logf("RESET_DELTA=%d", logs.FilterMessageSnippet("Resetting socket connections").Len()-before)
		t.Logf("LATER_RESULT=%t", result)
		if t.Failed() {
			bound.RunWith("missing", "snapshot", func(_ *TestFramework, t *testing.T) {
				t.Log("SNAPSHOT_BODY")
			})
		}
		t.Log("POSTLUDE")
	})
	t.Run("fresh", func(t *testing.T) {
		owner.ForTest(t).Run("step", func(_ *TestFramework, t *testing.T) {})
	})
}
