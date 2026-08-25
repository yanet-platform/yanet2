package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
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

func TestCollectDoctorReportChecksRequiredToolsSeparately(t *testing.T) {
	root := prepareDoctorFiles(t)
	image := filepath.Join(root, "yanet-test.qcow2")
	toolPaths := map[string]string{
		"go":                 "/usr/bin/go",
		"just":               "/usr/bin/just",
		"qemu-system-x86_64": "/usr/bin/qemu-system-x86_64",
		"qemu-img":           "/usr/bin/qemu-img",
		"ssh":                "/usr/bin/ssh",
		"ssh-keygen":         "/usr/bin/ssh-keygen",
	}
	report := collectDoctorReport(doctorConfig{
		Root:       root,
		Image:      image,
		ImageCheck: readableRegularFile,
		LookPath: func(name string) (string, error) {
			if name == "qemu-system-x86_64" {
				return "", errors.New("tool missing")
			}
			return toolPaths[name], nil
		},
		Platform:     "darwin",
		KVMAvailable: func() bool { return false },
	})

	require.False(t, report.OK)
	require.Equal(t, doctorStatusFailed, doctorCheckStatus(report, "tool:qemu-system-x86_64"))
	require.Equal(t, doctorStatusOK, doctorCheckStatus(report, "tool:qemu-img"))
	require.Equal(t, doctorStatusOK, doctorCheckStatus(report, "qemu-image"))
	kvm := doctorCheckFor(report, "kvm")
	require.Equal(t, doctorStatusWarning, kvm.Status)
	require.Contains(t, kvm.Reason, "TCG fallback")
}

func TestCollectDoctorReportReportsImageAndArtifactFailures(t *testing.T) {
	root := prepareDoctorFiles(t)
	image := filepath.Join(root, "yanet-test.qcow2")
	require.NoError(t, os.Chmod(image, 0o000))
	operatorArtifact := filepath.Join(root, "build", "operators", "route", "yanet-route-operator")
	operatorCLI := filepath.Join(root, "target", "release", "yanet-cli-ready")
	emptyArtifact := filepath.Join(root, "target", "release", "yanet-cli")
	require.NoError(t, os.Remove(operatorArtifact))
	require.NoError(t, os.WriteFile(operatorCLI, []byte("not an executable"), 0o755))
	require.NoError(t, os.WriteFile(emptyArtifact, nil, 0o755))

	report := collectDoctorReport(doctorConfig{
		Root:         root,
		Image:        image,
		ImageCheck:   readableRegularFile,
		LookPath:     func(string) (string, error) { return "/usr/bin/tool", nil },
		Platform:     "linux",
		KVMAvailable: func() bool { return false },
	})

	require.False(t, report.OK)
	imageCheck := doctorCheckFor(report, "qemu-image")
	require.Equal(t, doctorStatusFailed, imageCheck.Status)
	require.Contains(t, imageCheck.Reason, image)
	operatorCheck := doctorCheckFor(report, "artifact:build/operators/route/yanet-route-operator")
	require.Equal(t, doctorStatusFailed, operatorCheck.Status)
	require.Contains(t, operatorCheck.Reason, "candidate source: build/operators/")
	cliCheck := doctorCheckFor(report, "artifact:target/release/yanet-cli-ready")
	require.Equal(t, doctorStatusFailed, cliCheck.Status)
	require.Contains(t, cliCheck.Reason, "file is not an ELF executable")
	emptyCheck := doctorCheckFor(report, "artifact:target/release/yanet-cli")
	require.Equal(t, doctorStatusFailed, emptyCheck.Status)
	require.Contains(t, emptyCheck.Reason, "file is empty")
	require.Equal(t, doctorStatusWarning, doctorCheckStatus(report, "kvm"))
}

func TestValidateQEMUImageIncludesCommandDiagnostics(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "yanet-test.qcow2")
	tool := filepath.Join(root, "qemu-img")
	require.NoError(t, os.WriteFile(image, []byte("image"), 0o600))
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'invalid image metadata\\n' >&2\nexit 1\n"), 0o755))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := validateQEMUImage(image)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid image metadata")
}

func TestReadableExecutableFileRequiresGuestArchitecture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	header := validELFHeader()
	header[4] = byte(elf.ELFCLASS32)
	require.NoError(t, os.WriteFile(path, header, 0o755))
	err := readableExecutableFile(path)
	require.ErrorContains(t, err, "64-bit")

	header = validELFHeader()
	binary.LittleEndian.PutUint16(header[18:], uint16(elf.EM_AARCH64))
	require.NoError(t, os.WriteFile(path, header, 0o755))
	err = readableExecutableFile(path)
	require.ErrorContains(t, err, "x86_64")

	header = validELFHeader()
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_REL))
	require.NoError(t, os.WriteFile(path, header, 0o755))
	err = readableExecutableFile(path)
	require.ErrorContains(t, err, "runnable")

	header = validELFHeader()
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_DYN))
	require.NoError(t, os.WriteFile(path, header, 0o755))
	require.NoError(t, readableExecutableFile(path))

	header = validELFHeader()
	header[5] = byte(elf.ELFDATA2MSB)
	binary.BigEndian.PutUint16(header[16:], uint16(elf.ET_EXEC))
	binary.BigEndian.PutUint16(header[18:], uint16(elf.EM_X86_64))
	binary.BigEndian.PutUint32(header[20:], uint32(elf.EV_CURRENT))
	require.NoError(t, os.WriteFile(path, header, 0o755))
	err = readableExecutableFile(path)
	require.ErrorContains(t, err, "little-endian")

	scriptPath := filepath.Join(t.TempDir(), "dpdk-devbind.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!\n"), 0o755))
	err = readableExecutableFile(scriptPath)
	require.ErrorContains(t, err, "no interpreter")
}

func TestDoctorReportJSONHasCheckStatusAndReason(t *testing.T) {
	report := doctorReport{
		OK:     true,
		Checks: []doctorCheck{{Name: "kvm", Status: doctorStatusWarning, Reason: "KVM unavailable; using TCG fallback"}},
	}
	data, err := json.Marshal(report)
	require.NoError(t, err)
	var decoded struct {
		Checks []map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Checks, 1)
	require.Equal(t, "kvm", decoded.Checks[0]["name"])
	require.Equal(t, doctorStatusWarning, decoded.Checks[0]["status"])
	require.NotEmpty(t, decoded.Checks[0]["reason"])
}

func TestDoctorAccelerationCheckMissingKVMIsWarning(t *testing.T) {
	check := doctorAccelerationCheck("linux", func() bool { return false })
	require.Equal(t, doctorStatusWarning, check.Status)
	require.Contains(t, check.Reason, "TCG fallback")

	check = doctorAccelerationCheck("darwin", func() bool { return true })
	require.Equal(t, doctorStatusWarning, check.Status)
	require.Contains(t, check.Reason, "TCG fallback")
}

func prepareDoctorFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	image := filepath.Join(root, "yanet-test.qcow2")
	require.NoError(t, os.WriteFile(image, []byte("image"), 0o600))
	for _, path := range lab.RequiredArtifacts(root) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		contents := validELFHeader()
		if strings.HasSuffix(path, ".py") {
			contents = []byte("#!/u")
		}
		require.NoError(t, os.WriteFile(path, contents, 0o755))
	}
	return root
}

func validELFHeader() []byte {
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(header[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(header[20:], uint32(elf.EV_CURRENT))
	return header
}

func doctorCheckFor(report doctorReport, name string) doctorCheck {
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	return doctorCheck{}
}

func doctorCheckStatus(report doctorReport, name string) string {
	return doctorCheckFor(report, name).Status
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
	assert.Equal(t, supervisorManifestTimeout+30*time.Second, requestTimeout("manifest"))
	assert.Equal(t, supervisorExecTimeout, requestTimeout("exec"))
	assert.Equal(t, supervisorResetTimeout, requestTimeout("reset"))
	assert.Equal(t, supervisorShutdownTimeout, requestTimeout("down"))
}

func TestWriteReportOverwritesLastReport(t *testing.T) {
	dir := t.TempDir()
	first, err := writeReport(dir, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeReport(dir, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("report paths differ: %q vs %q", first, second)
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

func TestHandleConnectionDownAcksBeforeShutdown(t *testing.T) {
	state := &supervisor{}
	shutdownStarted := make(chan struct{})
	shutdownReturned := make(chan struct{})
	shutdown := func() error {
		close(shutdownStarted)
		<-shutdownReturned
		return nil
	}
	server, client := net.Pipe()
	defer client.Close()
	directory := t.TempDir()
	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, nil, shutdown, func() {})
		return nil
	})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "down"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)
	require.Equal(t, "lab stopped", reply.Output)
	// The response was decoded while shutdown was still blocked — proving
	// the early ack-before-shutdown ordering.
	close(shutdownReturned)
	require.NoError(t, handlers.Wait())
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

func TestEnsureSSHKeyRejectsAsymmetricState(t *testing.T) {
	directory := t.TempDir()
	pubPath := filepath.Join(directory, "id_ed25519.pub")
	require.NoError(t, os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA"), 0o644))
	_, err := ensureSSHKey(directory)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private key missing")
}

func TestEnsureSSHKeyRejectsMissingPub(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, []byte("PRIVATE KEY"), 0o600))
	_, err := ensureSSHKey(directory)
	require.Error(t, err)
}

func TestClassifyStaleSupervisor(t *testing.T) {
	cases := []struct {
		name     string
		resp     *response
		callErr  error
		expected staleSupervisorDecision
	}{
		{name: "no supervisor", resp: nil, callErr: errors.New("connect: connection refused"), expected: staleSupervisorAbsent},
		{name: "current version", resp: &response{Protocol: supervisorProtocolVersion}, callErr: nil, expected: staleSupervisorCurrent},
		{name: "stale version", resp: &response{Protocol: supervisorProtocolVersion + 1}, callErr: nil, expected: staleSupervisorStale},
		{name: "old supervisor (protocol 0)", resp: &response{Protocol: 0}, callErr: nil, expected: staleSupervisorStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, classifyStaleSupervisor(tc.resp, tc.callErr))
		})
	}
}
