package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestValidSessionName(t *testing.T) {
	for _, name := range []string{"default", "experiment-1", "lab.v2"} {
		if !validSessionName(name) {
			t.Errorf("validSessionName(%q) = false", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "/tmp/lab", "two words"} {
		if validSessionName(name) {
			t.Errorf("validSessionName(%q) = true", name)
		}
	}
}

func TestRootCommandHasUpSubcommand(t *testing.T) {
	application := newApplication()
	command := application.command()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)

	require.NoError(t, command.Execute())
	found := false
	for _, sub := range command.Commands() {
		if sub.Use == "up" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("root command has no 'up' subcommand")
	}
}

func TestExecCommandConsumesSeparator(t *testing.T) {
	command := newApplication().execCommand()
	var got []string
	command.RunE = func(_ *cobra.Command, args []string) error {
		got = args
		return nil
	}
	command.SetArgs([]string{"--", "printf", "--value"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "printf" || got[1] != "--value" {
		t.Fatalf("args = %#v", got)
	}
}

func TestSessionPathsSeparateCheckouts(t *testing.T) {
	first, _, err := sessionPathsForRoot("/tmp/first/yanet2", "default")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := sessionPathsForRoot("/tmp/second/yanet2", "default")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("session paths collide: %q", first)
	}
}

func TestStartupTimeoutIncludesColdBootstrapBudget(t *testing.T) {
	t.Setenv("YANET_VM_READY_TIMEOUT", "5m")
	require.Equal(t, 15*time.Minute, startupTimeout())
}

func TestStartupFailureIncludesLastStatusError(t *testing.T) {
	err := startupFailure("operator profile is not ready", "/tmp/supervisor.log")
	require.EqualError(t, err, "lab did not start: operator profile is not ready; see /tmp/supervisor.log")
}

func TestHandleRuntimeConnectionReportsStarting(t *testing.T) {
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}}
	server, client := net.Pipe()
	defer client.Close()
	go handleRuntimeConnection(server, t.TempDir(), runtime, func() {})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "status"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.False(t, reply.OK)
	require.Equal(t, "lab is starting", reply.Error)
}

func TestHandleRuntimeConnectionIncludesProtocolVersion(t *testing.T) {
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}}
	server, client := net.Pipe()
	defer client.Close()
	go handleRuntimeConnection(server, t.TempDir(), runtime, func() {})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "status"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.Equal(t, supervisorProtocolVersion, reply.Protocol)
}

func TestRequestTimeoutMatchesActionBudget(t *testing.T) {
	fast := []string{"status", "shell", "report", "serial"}
	for _, action := range fast {
		require.Equal(t, supervisorRequestTimeout, requestTimeout(action), "fast action %q", action)
	}
	assert.Equal(t, supervisorManifestTimeout, requestTimeout("manifest"))
	assert.Equal(t, supervisorExecTimeout, requestTimeout("exec"))
	assert.Equal(t, supervisorResetTimeout, requestTimeout("reset"))
	assert.Equal(t, supervisorShutdownTimeout, requestTimeout("down"))
}

func TestWriteReportCreatesDistinctFiles(t *testing.T) {
	first, err := writeReport(t.TempDir(), []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeReport(filepath.Dir(first), []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("report paths collide: %q", first)
	}
	data, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("report = %q", data)
	}
}

func TestCopySerialInputDetachesOnEscape(t *testing.T) {
	var output bytes.Buffer
	err := copySerialInput(&output, bytes.NewReader([]byte("echo ok\x1dignored")))
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "echo ok" {
		t.Fatalf("serial input = %q", got)
	}
}

func TestSerialDimensionsUseTerminalHeightAndWidth(t *testing.T) {
	rows, columns := serialDimensions(80, 24)
	if rows != 24 || columns != 80 {
		t.Fatalf("rows=%d columns=%d", rows, columns)
	}
}

func TestHandleConnectionReturnsBusy(t *testing.T) {
	for _, action := range []string{"status", "shell"} {
		t.Run(action, func(t *testing.T) {
			state := &supervisor{}
			require.True(t, state.TryOperation())
			defer state.ReleaseOperation()
			server, client := net.Pipe()
			defer client.Close()
			directory := t.TempDir()
			var handlers errgroup.Group
			handlers.Go(func() error {
				handleConnection(server, nil, directory, state, nil, nil, func() {})
				return nil
			})
			require.NoError(t, json.NewEncoder(client).Encode(request{Action: action}))
			var reply response
			require.NoError(t, json.NewDecoder(client).Decode(&reply))
			require.False(t, reply.OK)
			require.Equal(t, "lab is busy", reply.Error)
			require.NoError(t, handlers.Wait())
		})
	}
}

func TestSupervisorClosesSerial(t *testing.T) {
	state := &supervisor{}
	server, client := net.Pipe()
	defer client.Close()
	require.True(t, state.TrySerial(server))
	state.CloseSerial()
	_, err := client.Write([]byte("closed"))
	require.Error(t, err)
	state.ReleaseSerial(server)
}

func TestSupervisorRejectsOperationsAfterShutdown(t *testing.T) {
	state := &supervisor{}
	require.NoError(t, state.Shutdown(func() error { return nil }))
	require.False(t, state.TryOperation())
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	require.False(t, state.TrySerial(server))
}

func TestEnsurePrivateDirectoryRejectsUnsafePaths(t *testing.T) {
	testCases := []struct {
		name    string
		prepare func(string) error
	}{
		{
			name: "wrong mode",
			prepare: func(path string) error {
				return os.Mkdir(path, 0o755)
			},
		},
		{
			name: "regular file",
			prepare: func(path string) error {
				return os.WriteFile(path, nil, 0o600)
			},
		},
		{
			name: "symbolic link",
			prepare: func(path string) error {
				return os.Symlink(t.TempDir(), path)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime")
			require.NoError(t, testCase.prepare(path))
			require.Error(t, ensurePrivateDirectory(path))
		})
	}
}

func TestValidatePublicFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_ed25519.pub")
	require.NoError(t, os.WriteFile(path, []byte("public"), 0o644))
	require.NoError(t, validatePublicFile(path))
	require.NoError(t, os.Chmod(path, 0o600))
	require.Error(t, validatePublicFile(path))
}
