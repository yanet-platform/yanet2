package main

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
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
	// Names that would push supervisor.sock past sun_path on darwin.
	longName := strings.Repeat("a", maxSessionNameLen+1)
	if validSessionName(longName) {
		t.Errorf("validSessionName(%d-char) = true, want false for sun_path", len(longName))
	}
}

func TestRootDigest(t *testing.T) {
	// First 12 hex chars of sha256 are an AC contract: the runtime
	// directory namespace is keyed by this prefix.
	const root = "/yanet2-fixture-root"
	got := rootDigest(root)
	sum := sha256.Sum256([]byte(root))
	want := fmt.Sprintf("%x", sum[:6])
	if got != want {
		t.Errorf("rootDigest(%q) = %q, want sha256 prefix %q", root, got, want)
	}
	if len(got) != 12 {
		t.Errorf("rootDigest length = %d, want 12", len(got))
	}
}

func TestSSHKeygenCleanErrorWhenMissing(t *testing.T) {
	prev := lookupKeygen
	t.Cleanup(func() { lookupKeygen = prev })
	lookupKeygen = func() (string, error) {
		return "", errors.New("ssh-keygen: not in PATH")
	}
	_, err := ensureSSHKey(t.TempDir())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ssh-keygen not found") {
		t.Errorf("error %q does not mention missing ssh-keygen", err.Error())
	}
}

func TestResolveProjectRootWalksToCanonicalGoMod(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested", "work")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lab\n"), 0o644))

	got, err := resolveProjectRoot(nested)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolveProjectRootAcceptsSystemPrefixAlias(t *testing.T) {
	resolvedPrefix, err := filepath.EvalSymlinks(string(filepath.Separator) + "var")
	if err != nil || resolvedPrefix == string(filepath.Separator)+"var" {
		t.Skip("host has no /var prefix alias")
	}
	canonicalRoot, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	privateVar := string(filepath.Separator) + "private" + string(filepath.Separator) + "var" + string(filepath.Separator)
	if !strings.HasPrefix(canonicalRoot, privateVar) {
		t.Skip("temporary directory is not under /private/var")
	}
	logicalRoot := string(filepath.Separator) + "var" + string(filepath.Separator) + strings.TrimPrefix(canonicalRoot, privateVar)
	require.NoError(t, os.WriteFile(filepath.Join(canonicalRoot, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	logicalStart := filepath.Join(logicalRoot, "nested")
	require.NoError(t, os.Mkdir(filepath.Join(canonicalRoot, "nested"), 0o755))

	got, err := resolveProjectRoot(logicalStart)
	require.NoError(t, err)
	require.Equal(t, canonicalRoot, got)
}

func TestResolveProjectRootReportsMissingGoMod(t *testing.T) {
	start, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	_, err = resolveProjectRoot(start)
	require.EqualError(t, err, "cannot find go.mod walking up from "+start)
}

func TestResolveProjectRootRejectsIntermediateSymlinkAcrossRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.com/outside\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(outside, "nested"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsSymlinkEscapeAndReentry(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real", "nested"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "real", "nested"), filepath.Join(outside, "nested")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsExternalSymlinkWithoutLogicalGoMod(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.com/outside\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(outside, "nested"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsGoModBelowExternalSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.Mkdir(filepath.Join(outside, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "nested", "go.mod"), []byte("module example.com/outside\n"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsSymlinkWithoutGoMod(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.Mkdir(filepath.Join(outside, "nested"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsSymlinkedGoModThroughExternalSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	marker := t.TempDir()
	start := filepath.Join(root, "link", "nested")
	require.NoError(t, os.WriteFile(filepath.Join(marker, "go.mod"), []byte("module example.com/outside\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(marker, "go.mod"), filepath.Join(outside, "go.mod")))
	require.NoError(t, os.Mkdir(filepath.Join(outside, "nested"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsDanglingGoModSymlink(t *testing.T) {
	root := t.TempDir()
	start := filepath.Join(root, "nested")
	require.NoError(t, os.Mkdir(start, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "missing-go.mod"), filepath.Join(root, "go.mod")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootRejectsSymlinkedGoMod(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	start := filepath.Join(root, "nested")
	require.NoError(t, os.Mkdir(start, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "go.mod"), filepath.Join(root, "go.mod")))

	_, err := resolveProjectRoot(start)
	require.EqualError(t, err, "project root is not canonical; resolve symlinks before running")
}

func TestResolveProjectRootCachesResult(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(firstRoot, "go.mod"), []byte("module example.com/first\n"), 0o644))

	projectRootCache = projectRootState{}
	t.Cleanup(func() { projectRootCache = projectRootState{} })

	t.Chdir(firstRoot)
	got, err := projectRoot()
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(firstRoot)
	require.NoError(t, err)
	require.Equal(t, want, got)

	t.Chdir(secondRoot)
	got, err = projectRoot()
	require.NoError(t, err)
	require.Equal(t, want, got)
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
		if sub.Name() == "up" {
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

func TestStartupStatusErrorDropsBusy(t *testing.T) {
	require.Equal(t, "", startupStatusError("", labBusyError))
	require.Equal(t, "operator profile is not ready", startupStatusError("", "operator profile is not ready"))
	require.Equal(t, "operator profile is not ready", startupStatusError("operator profile is not ready", labBusyError))
	require.Equal(t, "baseline snapshot missing; run up again", startupStatusError("operator profile is not ready", "baseline snapshot missing; run up again"))
}

func TestExitedDuringStartupError(t *testing.T) {
	processErr := errors.New("exit status 1")
	logPath := "/tmp/supervisor.log"

	t.Run("free lock produces generic error", func(t *testing.T) {
		dir := t.TempDir()
		err := exitedDuringStartupError(dir, nil, processErr, logPath)
		require.EqualError(t, err, "lab supervisor exited during startup: exit status 1; see /tmp/supervisor.log")
	})

	t.Run("held lock produces exact busy error", func(t *testing.T) {
		dir := t.TempDir()
		holder, err := os.OpenFile(filepath.Join(dir, "supervisor.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		require.NoError(t, err)
		defer holder.Close()
		require.NoError(t, syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
		err = exitedDuringStartupError(dir, nil, processErr, logPath)
		require.EqualError(t, err, labBusyError)
	})

	t.Run("session path error bypasses probe", func(t *testing.T) {
		err := exitedDuringStartupError("", errors.New("invalid session path"), processErr, logPath)
		require.EqualError(t, err, "lab supervisor exited during startup: exit status 1; see /tmp/supervisor.log")
	})
}

func TestHandleRuntimeConnectionReturnsBusyWhileStarting(t *testing.T) {
	for _, action := range []string{"status", "reset", "exec", "shell", "serial", "report", "manifest"} {
		t.Run(action, func(t *testing.T) {
			runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}}
			server, client := net.Pipe()
			defer client.Close()
			go handleRuntimeConnection(server, t.TempDir(), runtime, func() {})
			require.NoError(t, json.NewEncoder(client).Encode(request{Action: action}))
			var reply response
			require.NoError(t, json.NewDecoder(client).Decode(&reply))
			require.False(t, reply.OK)
			require.Equal(t, labBusyError, reply.Error)
		})
	}
}

func TestHandleRuntimeConnectionIncludesProtocolVersion(t *testing.T) {
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}}
	server, client := net.Pipe()
	defer client.Close()
	go handleRuntimeConnection(server, t.TempDir(), runtime, func() {})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "status"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
	require.Equal(t, 0, reply.Protocol)
}

func TestRequestTimeoutMatchesActionBudget(t *testing.T) {
	assert.Equal(t, 5*time.Minute, supervisorExecTimeout)
	fast := []string{"shell", "report", "serial"}
	for _, action := range fast {
		require.Equal(t, supervisorRequestTimeout, requestTimeout(action), "fast action %q", action)
	}
	assert.Equal(t, supervisorStatusTimeout, requestTimeout("status"))
	assert.Greater(t, requestTimeout("status"), 30*time.Second)
	assert.Equal(t, supervisorManifestTimeout+30*time.Second, requestTimeout("manifest"))
	assert.Equal(t, supervisorExecTimeout+30*time.Second, requestTimeout("exec"))
	assert.Equal(t, supervisorResetTimeout, requestTimeout("reset"))
	assert.Equal(t, supervisorShutdownTimeout, requestTimeout("down"))
}

// Test_HandleConnection_ExecUsesActionBudget verifies that exec requests pass
// the full guest action budget while retaining time to return the reply.
func Test_HandleConnection_ExecUsesActionBudget(t *testing.T) {
	original := executeSupervisorCommand
	t.Cleanup(func() { executeSupervisorCommand = original })

	var command string
	var timeout time.Duration
	executeSupervisorCommand = func(_ *framework.TestFramework, value string, valueTimeout time.Duration) (string, error) {
		command = value
		timeout = valueTimeout
		return "complete", nil
	}

	server, client := net.Pipe()
	defer client.Close()
	go handleConnection(server, nil, t.TempDir(), &supervisor{}, nil, nil, func() {})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "exec", Argv: []string{"sleep", "31"}}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))

	require.Equal(t, "'sleep' '31'", command)
	require.Equal(t, supervisorExecTimeout, timeout)
	require.Equal(t, "complete", reply.Output)
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
	for _, action := range []string{"status", "reset", "exec", "shell", "serial", "report", "manifest"} {
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
			require.Equal(t, labBusyError, reply.Error)
			require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
			require.NoError(t, handlers.Wait())
		})
	}
}

func TestDownWaitsForActiveOperation(t *testing.T) {
	state := &supervisor{}
	require.True(t, state.TryOperation())
	operationReleased := false
	t.Cleanup(func() {
		if !operationReleased {
			state.ReleaseOperation()
		}
	})

	shutdownStarted := make(chan struct{})
	server, client := net.Pipe()
	defer client.Close()
	directory := t.TempDir()
	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, nil, func() error {
			close(shutdownStarted)
			return nil
		}, func() {})
		return nil
	})

	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "down"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)
	require.Equal(t, "lab stopped", reply.Output)
	require.Eventually(t, func() bool {
		state.serialMutex.Lock()
		defer state.serialMutex.Unlock()
		return state.stopping
	}, time.Second, time.Millisecond)

	busyServer, busyClient := net.Pipe()
	var busyHandlers errgroup.Group
	busyHandlers.Go(func() error {
		handleConnection(busyServer, nil, directory, state, nil, nil, func() {})
		return nil
	})
	require.NoError(t, json.NewEncoder(busyClient).Encode(request{Action: "status"}))
	var busyReply response
	require.NoError(t, json.NewDecoder(busyClient).Decode(&busyReply))
	require.False(t, busyReply.OK)
	require.Equal(t, labBusyError, busyReply.Error)
	require.NoError(t, busyHandlers.Wait())
	require.NoError(t, busyClient.Close())
	require.NoError(t, busyServer.Close())
	select {
	case <-shutdownStarted:
		t.Fatal("shutdown started before active operation released")
	default:
	}

	state.ReleaseOperation()
	operationReleased = true
	select {
	case <-shutdownStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not start after active operation released")
	}
	require.NoError(t, handlers.Wait())
}

func TestSupervisorOperationCanRetryAfterRelease(t *testing.T) {
	savedProbe := baselineReadyProbe
	t.Cleanup(func() { baselineReadyProbe = savedProbe })
	baselineReadyProbe = func() bool { return true }

	state := &supervisor{}
	require.True(t, state.TryOperation())
	busyServer, busyClient := net.Pipe()
	directory := t.TempDir()
	restoreRanWhileBusy := make(chan struct{}, 1)
	var busyHandlers errgroup.Group
	busyHandlers.Go(func() error {
		handleConnection(busyServer, nil, directory, state, func() error {
			restoreRanWhileBusy <- struct{}{}
			return nil
		}, nil, func() {})
		return nil
	})
	require.NoError(t, json.NewEncoder(busyClient).Encode(request{Action: "reset"}))
	var busyReply response
	require.NoError(t, json.NewDecoder(busyClient).Decode(&busyReply))
	require.False(t, busyReply.OK)
	require.Equal(t, labBusyError, busyReply.Error)
	require.NoError(t, busyHandlers.Wait())
	require.NoError(t, busyClient.Close())
	require.NoError(t, busyServer.Close())
	select {
	case <-restoreRanWhileBusy:
		t.Fatal("restore ran while reset was busy")
	default:
	}

	state.ReleaseOperation()

	restoreRan := 0
	server, client := net.Pipe()
	defer client.Close()
	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, func() error {
			restoreRan++
			return nil
		}, nil, func() {})
		return nil
	})
	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "reset"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)
	require.Equal(t, "baseline restored", reply.Output)
	require.NoError(t, handlers.Wait())
	require.Equal(t, 1, restoreRan)
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
	require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
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

func TestSupervisorShutdownClosesAttachedSerial(t *testing.T) {
	state := &supervisor{}
	server, client := net.Pipe()
	defer client.Close()
	require.True(t, state.TrySerial(server))

	shutdownDone := make(chan struct{})
	go func() {
		_ = state.Shutdown(func() error { return nil })
		close(shutdownDone)
	}()

	// Stop must close the attached serial connection so the serial io.Copy
	// returns and handleConnection's deferred ReleaseSerial can release the
	// operation mutex the shutdown is waiting on. Once stopping is visible
	// the close already happened (both live under the same serialMutex
	// critical section), so a blocking read on the peer must fail with EOF.
	require.Eventually(t, func() bool {
		state.serialMutex.Lock()
		defer state.serialMutex.Unlock()
		return state.stopping
	}, time.Second, time.Millisecond)
	_, err := client.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)

	state.ReleaseSerial(server)
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete after the attached serial was closed")
	}
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

func TestSupervisorSignalWaitsForActiveOperation(t *testing.T) {
	state := &supervisor{}
	require.True(t, state.TryOperation())

	interrupted := &atomic.Bool{}
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: state, Interrupted: interrupted}
	listenerClosed := make(chan struct{})
	close(runtime.Ready)

	signalReturned := make(chan struct{})
	go func() {
		handleTerminationSignal(runtime, func() { close(listenerClosed) })
		close(signalReturned)
	}()
	require.Eventually(t, func() bool {
		state.serialMutex.Lock()
		defer state.serialMutex.Unlock()
		return state.stopping
	}, time.Second, time.Millisecond)
	select {
	case <-signalReturned:
		t.Fatal("signal shutdown returned before active operation released")
	default:
	}
	require.Eventually(t, func() bool {
		select {
		case <-listenerClosed:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	state.ReleaseOperation()
	require.Eventually(t, func() bool {
		select {
		case <-signalReturned:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		select {
		case <-listenerClosed:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.True(t, interrupted.Load())
}

func TestSupervisorShutdownHooksAreSerialized(t *testing.T) {
	state := &supervisor{}

	var order []string
	var orderMutex sync.Mutex
	record := func(name string) {
		orderMutex.Lock()
		defer orderMutex.Unlock()
		order = append(order, name)
	}
	orderSnapshot := func() []string {
		orderMutex.Lock()
		defer orderMutex.Unlock()
		return append([]string(nil), order...)
	}

	downHookStarted := make(chan struct{})
	releaseDownHook := make(chan struct{})
	resultHookStarted := make(chan struct{})
	releaseResultHook := make(chan struct{})
	go func() {
		_ = state.ShutdownWithBeforeWaitAndResult(func() {
			record("down-before-wait")
			close(downHookStarted)
			<-releaseDownHook
		}, func() error {
			record("down-cleanup")
			return nil
		}, func(error) {
			record("down-result")
			close(resultHookStarted)
			<-releaseResultHook
		})
	}()
	<-downHookStarted

	signalAttempted := make(chan struct{})
	go func() {
		close(signalAttempted)
		_ = state.ShutdownWithBeforeWait(func() {
			record("signal-before-wait")
		}, func() error { return nil })
	}()
	<-signalAttempted

	close(releaseDownHook)
	require.Eventually(t, func() bool {
		return len(orderSnapshot()) == 3
	}, time.Second, time.Millisecond)
	// The second shutdown must be blocked on shutdownMutex until the first
	// completes: its hook cannot have run while the result hook is held.
	require.Equal(t, []string{"down-before-wait", "down-cleanup", "down-result"}, orderSnapshot())
	close(releaseResultHook)
	require.Eventually(t, func() bool {
		return len(orderSnapshot()) == 4
	}, time.Second, time.Millisecond)
	require.Equal(t, []string{"down-before-wait", "down-cleanup", "down-result", "signal-before-wait"}, orderSnapshot())
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
		{name: "current version", resp: &response{SupervisorProtocolVersion: supervisorProtocolVersion}, callErr: nil, expected: staleSupervisorCurrent},
		{name: "stale version", resp: &response{SupervisorProtocolVersion: supervisorProtocolVersion + 1}, callErr: nil, expected: staleSupervisorStale},
		{name: "old supervisor (protocol 0)", resp: &response{SupervisorProtocolVersion: 0}, callErr: nil, expected: staleSupervisorStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, classifyStaleSupervisor(tc.resp, tc.callErr))
		})
	}
}

func TestResponseEnvelopeCarriesBothProtocolVersions(t *testing.T) {
	data, err := json.Marshal(response{
		OK:                        true,
		Output:                    "VM: running; YANET: ready; operators: ready",
		Protocol:                  cliProtocolVersion,
		SupervisorProtocolVersion: supervisorProtocolVersion,
	})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.EqualValues(t, cliProtocolVersion, decoded["protocol"])
	require.EqualValues(t, supervisorProtocolVersion, decoded["supervisorProtocolVersion"])
	require.Equal(t, true, decoded["ok"])
}

func TestStatusVerdict(t *testing.T) {
	require.Equal(t, statusNotReady, statusVerdict(nil))
	require.Equal(t, statusReady, statusVerdict(readyScopes()))
	require.Equal(t, statusNotReady, statusVerdict(failingScopes("bird", "bird process is not running")))
}

func TestStatusVerdictRejectsUnknownScopeName(t *testing.T) {
	scopes := readyScopes()
	scopes[0].Name = "ghost"
	require.Equal(t, statusNotReady, statusVerdict(scopes))
}

func TestStatusVerdictRejectsDuplicateScope(t *testing.T) {
	scopes := readyScopes()
	scopes[len(scopes)-1] = scopes[0]
	require.Equal(t, statusNotReady, statusVerdict(scopes))
}

func TestStatusVerdictRejectsShortScopeCount(t *testing.T) {
	scopes := readyScopes()[:len(readyScopes())-1]
	require.Equal(t, statusNotReady, statusVerdict(scopes))
}

func TestStatusReadyJSONListsEveryScope(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion, Scopes: readyScopes()}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.NoError(t, err)
	var decoded struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Scopes []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Equal(t, true, decoded.OK)
	require.Equal(t, statusReady, decoded.Status)
	require.Len(t, decoded.Scopes, len(lab.ScopeNames()))
	for _, scope := range decoded.Scopes {
		require.Equal(t, string(lab.StateReady), scope.State)
	}
}

func TestStatusNotReadyJSONListsReasonsAndFails(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{
			OK:                        false,
			Error:                     "operator profile is not ready: bird",
			Status:                    statusNotReady,
			SupervisorProtocolVersion: supervisorProtocolVersion,
			Scopes:                    failingScopes("bird", "bird process is not running"),
		}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "operator profile is not ready")
	var decoded struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Error  string `json:"error"`
		Scopes []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Equal(t, false, decoded.OK)
	require.Equal(t, statusNotReady, decoded.Status)
	require.Contains(t, decoded.Error, "bird")
	for _, scope := range decoded.Scopes {
		if scope.Name == "bird" {
			require.Equal(t, string(lab.StateNotReady), scope.State)
			require.Equal(t, "bird process is not running", scope.Reason)
		} else {
			require.Equal(t, string(lab.StateReady), scope.State)
		}
	}
}

func TestStatusHumanSummaryHealthyExitsZero(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion, Scopes: readyScopes()}, nil
	}, nil)
	application := newApplication()
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.NoError(t, err)
	require.Contains(t, string(stdout), statusReady)
}

func TestStatusHumanSummaryNotReadyFailsAndShowsFailingScope(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{
			OK:                        false,
			Error:                     "operator profile is not ready: route0-session",
			Status:                    statusNotReady,
			SupervisorProtocolVersion: supervisorProtocolVersion,
			Scopes:                    failingScopes("route0-session", "route0 adapter session is not connected"),
		}, nil
	}, nil)
	application := newApplication()
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.Error(t, err)
	require.Contains(t, string(stdout), statusNotReady)
	require.Contains(t, string(stdout), "route0-session")
	require.Contains(t, string(stdout), "route0 adapter session is not connected")
	require.NotContains(t, string(stdout), "\n"+statusReady)
}

func TestStatusProtocolMismatchWithoutStateChange(t *testing.T) {
	calls := make([]request, 0)
	stubCallAndServe(t, func(_ *application, value request) (*response, error) {
		calls = append(calls, value)
		return &response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion - 1}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "protocol mismatch")
	require.Len(t, calls, 1)
	require.Equal(t, "status", calls[0].Action)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.EqualValues(t, supervisorProtocolVersion-1, decoded["supervisorProtocolVersion"])
	require.Equal(t, statusNotReady, decoded["status"])
	require.Equal(t, false, decoded["ok"])
	require.Contains(t, decoded["error"], "protocol mismatch")
}

func TestStatusProtocolZeroIsPreservedInJSON(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{OK: true}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.Error(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Contains(t, decoded, "supervisorProtocolVersion")
	require.EqualValues(t, 0, decoded["supervisorProtocolVersion"])
}

func TestStatusCurrentProtocolHealthy(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion, Scopes: readyScopes()}, nil
	}, nil)
	application := newApplication()
	require.NoError(t, application.status())
}

// TestStatusTrustsNoReplyStatus pins the fail-closed CLI boundary: a reply
// that claims OK and Status READY while a scope is failing must exit non-zero
// and report NOT_READY, never trust the supervisor's verdict verbatim.
func TestStatusTrustsNoReplyStatus(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{
			OK:                        true,
			Status:                    statusReady,
			SupervisorProtocolVersion: supervisorProtocolVersion,
			Scopes:                    failingScopes("bird", "bird process is not running"),
		}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bird")
	var decoded struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Equal(t, false, decoded.OK)
	require.Equal(t, statusNotReady, decoded.Status)
}

// TestStatusErrorMessageAlwaysIncludesFailingScopeNames pins the shared
// NOT_READY message producer on the CLI fallback: an error-only-inconsistent
// reply must carry the failing scope names, matching the handler's shape.
func TestStatusErrorMessageAlwaysIncludesFailingScopeNames(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{
			OK:                        false,
			SupervisorProtocolVersion: supervisorProtocolVersion,
			Scopes:                    failingScopes("route0-session", "route0 adapter session is not connected"),
		}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.EqualError(t, err, "operator profile is not ready: route0-session")
	var decoded struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Equal(t, "operator profile is not ready: route0-session", decoded.Error)
}

func TestStatusFailedReplyNeverRendersReady(t *testing.T) {
	stubCallAndServe(t, func(*application, request) (*response, error) {
		return &response{
			OK:                        false,
			Error:                     "status probe failed",
			Status:                    statusReady,
			SupervisorProtocolVersion: supervisorProtocolVersion,
			Scopes:                    readyScopes(),
		}, nil
	}, nil)
	application := newApplication()
	application.json = true
	stdout, err := captureStdout(t, func() error {
		return application.status()
	})
	require.EqualError(t, err, "status probe failed")
	var decoded struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.False(t, decoded.OK)
	require.Equal(t, statusNotReady, decoded.Status)
}

func TestStatusHandlerPopulatesScopes(t *testing.T) {
	cases := []struct {
		name       string
		results    []lab.ScopeResult
		checkErr   error
		wantOK     bool
		wantStatus string
		wantError  string
	}{
		{
			name:       "ready",
			results:    readyScopes(),
			wantOK:     true,
			wantStatus: statusReady,
		},
		{
			name:       "not ready",
			results:    failingScopes("bird", "bird process is not running"),
			wantOK:     false,
			wantStatus: statusNotReady,
			wantError:  "operator profile is not ready: bird",
		},
		{
			name:       "probe error",
			results:    readyScopes(),
			checkErr:   errors.New("status probe failed"),
			wantOK:     false,
			wantStatus: statusNotReady,
			wantError:  "status probe failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saved := checkOperatorStatus
			checkOperatorStatus = func(*framework.TestFramework) ([]lab.ScopeResult, error) {
				return tc.results, tc.checkErr
			}
			t.Cleanup(func() { checkOperatorStatus = saved })
			state := &supervisor{}
			stateChanges := 0
			server, client := net.Pipe()
			defer client.Close()
			var handlers errgroup.Group
			handlers.Go(func() error {
				handleConnection(server, nil, t.TempDir(), state, func() error {
					stateChanges++
					return nil
				}, func() error {
					stateChanges++
					return nil
				}, func() { stateChanges++ })
				return nil
			})
			require.NoError(t, json.NewEncoder(client).Encode(request{Action: "status"}))
			var reply response
			require.NoError(t, json.NewDecoder(client).Decode(&reply))
			require.Equal(t, tc.wantOK, reply.OK)
			require.Equal(t, tc.wantStatus, reply.Status)
			require.Equal(t, tc.wantError, reply.Error)
			require.Equal(t, tc.results, reply.Scopes)
			require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
			require.NoError(t, handlers.Wait())
			require.Zero(t, stateChanges)
		})
	}
}

// readyScopes returns the full AD-11 scope list in ready state.
func readyScopes() []lab.ScopeResult {
	scopes := make([]lab.ScopeResult, 0, len(lab.ScopeNames()))
	for _, name := range lab.ScopeNames() {
		scopes = append(scopes, lab.ScopeResult{Name: name, State: lab.StateReady})
	}
	return scopes
}

// failingScopes returns the full AD-11 scope list with one named scope failed.
func failingScopes(notReadyName, reason string) []lab.ScopeResult {
	scopes := readyScopes()
	for idx := range scopes {
		if scopes[idx].Name == notReadyName {
			scopes[idx].State = lab.StateNotReady
			scopes[idx].Reason = reason
		}
	}
	return scopes
}

func TestUpCommandPassesPositionalToRunE(t *testing.T) {
	command := newApplication().upCommand()
	require.Equal(t, "up [SESSION]", command.Use)
	var seen []string
	command.RunE = func(_ *cobra.Command, args []string) error {
		seen = args
		return nil
	}
	command.SetArgs([]string{"my-session"})
	require.NoError(t, command.Execute())
	require.Equal(t, []string{"my-session"}, seen)
	command.SetArgs([]string{"my-session", "extra"})
	err := command.Execute()
	require.Error(t, err)
}

func TestStatusCommandAcceptsPositionalSession(t *testing.T) {
	stubCallAndServe(t, func(app *application, value request) (*response, error) {
		require.Equal(t, "my-session", app.session)
		require.Equal(t, "status", value.Action)
		return &response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion, Scopes: readyScopes()}, nil
	}, nil)
	command := newApplication().statusCommand()
	require.Equal(t, "status [SESSION]", command.Use)
	command.SetArgs([]string{"my-session"})
	require.NoError(t, command.Execute())
	command.SetArgs([]string{"my-session", "extra"})
	require.Error(t, command.Execute())
}

func TestSelectSessionAssignsPositional(t *testing.T) {
	application := newApplication()
	require.Equal(t, defaultSession, application.session)
	application.selectSession([]string{"my-session"})
	require.Equal(t, "my-session", application.session)
	application.selectSession(nil)
	require.Equal(t, "my-session", application.session)
}

func TestUpRejectsShutdownMarkerBeforeSpawn(t *testing.T) {
	// Reset the cached project root so this test can pin it to the temp dir.
	projectRootCache = projectRootState{}
	t.Cleanup(func() { projectRootCache = projectRootState{} })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	t.Chdir(root)

	session := "marker-reject"
	dir, _, err := sessionPaths(session)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	require.NoError(t, os.WriteFile(filepath.Join(dir, shutdownMarkerName),
		[]byte(`{"status":"FAILED","step":"stop-vm","reason":"qemu timed out"}`), 0o600))

	stubCallAndServe(t, nil, func(*application, string) (*response, error) {
		t.Fatal("serveRunner must not run when shutdown-failed.json rejects up")
		return nil, nil
	})

	application := newApplication()
	application.session = session
	err = application.up()
	require.EqualError(t, err, `previous down failed at step "stop-vm": qemu timed out; run 'yanet-lab down' to retry cleanup`)
}

func TestPrintResponseStampsCLIProtocol(t *testing.T) {
	t.Run("ok path", func(t *testing.T) {
		application := newApplication()
		application.json = true
		value := &response{OK: true, Output: "VM: running", SupervisorProtocolVersion: supervisorProtocolVersion}
		stdout, err := captureStdout(t, func() error {
			return application.printResponse(value)
		})
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(stdout, &decoded))
		require.EqualValues(t, cliProtocolVersion, decoded["protocol"])
		require.EqualValues(t, supervisorProtocolVersion, decoded["supervisorProtocolVersion"])
		require.Equal(t, true, decoded["ok"])
		require.Equal(t, "VM: running", decoded["output"])
		// The caller's struct must not have been mutated.
		require.Equal(t, 0, value.Protocol)
	})
	t.Run("error path", func(t *testing.T) {
		application := newApplication()
		application.json = true
		value := &response{OK: false, Error: "lab stopped", SupervisorProtocolVersion: supervisorProtocolVersion}
		stdout, callErr := captureStdout(t, func() error {
			return application.printResponse(value)
		})
		require.EqualError(t, callErr, "lab stopped")
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(stdout, &decoded))
		require.EqualValues(t, cliProtocolVersion, decoded["protocol"])
		require.EqualValues(t, supervisorProtocolVersion, decoded["supervisorProtocolVersion"])
		require.Equal(t, false, decoded["ok"])
		require.Equal(t, "lab stopped", decoded["error"])
		require.Equal(t, 0, value.Protocol)
	})
}

func TestAnnotateReplacementOnlyFiresOnStaleTeardown(t *testing.T) {
	resp := &response{Output: "VM: running"}
	annotateReplacement(resp, noStaleTeardown)
	require.Equal(t, "VM: running", resp.Output)
	annotateReplacement(resp, 1)
	require.Equal(t, "replaced stale supervisor (protocol 1); VM: running", resp.Output)
	resp = &response{Output: "VM: running"}
	annotateReplacement(resp, 0)
	require.Equal(t, "replaced stale supervisor (protocol 0); VM: running", resp.Output)
	resp = &response{}
	annotateReplacement(resp, 2)
	require.Equal(t, "replaced stale supervisor (protocol 2)", resp.Output)
}

// captureStdout redirects os.Stdout during fn and returns whatever was written.
func captureStdout(t *testing.T, fn func() error) ([]byte, error) {
	t.Helper()
	original := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = original
		_ = readEnd.Close()
		_ = writeEnd.Close()
	}()
	runErr := fn()
	require.NoError(t, writeEnd.Close())
	data, readErr := io.ReadAll(readEnd)
	return data, errors.Join(runErr, readErr)
}

// stubCallAndServe swaps the package-level callSupervisor and serveRunner
// functions for the duration of a test, restoring the originals on cleanup.
// Tests that exercise up()'s wire-ups use this to drive the production flow
// without standing up a real Unix-domain socket or forking a QEMU subprocess.
func stubCallAndServe(t *testing.T, call func(*application, request) (*response, error), run func(*application, string) (*response, error)) {
	t.Helper()
	savedCall, savedRun := callSupervisor, serveRunner
	t.Cleanup(func() {
		callSupervisor = savedCall
		serveRunner = savedRun
	})
	if call != nil {
		callSupervisor = call
	}
	if run != nil {
		serveRunner = run
	}
}

// tempUp sets projectRootCache to a temp repo and chdir's into it, returning
// the resolved session runtime directory and a cleanup func.
func tempUp(t *testing.T, session string) (string, func()) {
	t.Helper()
	projectRootCache = projectRootState{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lab\n"), 0o644))
	t.Chdir(root)
	dir, _, err := sessionPaths(session)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return dir, func() {
		_ = os.RemoveAll(dir)
		projectRootCache = projectRootState{}
	}
}

func TestUpRejectsBusySupervisorWithoutSpawningReplacement(t *testing.T) {
	_, cleanup := tempUp(t, "unhealthy-running")
	defer cleanup()
	serveRunnerCalled := false
	stubCallAndServe(t,
		func(*application, request) (*response, error) {
			return &response{
				OK:                        false,
				Error:                     "lab is busy",
				SupervisorProtocolVersion: supervisorProtocolVersion,
			}, nil
		},
		func(*application, string) (*response, error) {
			serveRunnerCalled = true
			return nil, errors.New("serveRunner must not run for a busy Supervisor")
		},
	)
	application := newApplication()
	application.session = "unhealthy-running"
	err := application.up()
	require.EqualError(t, err, labBusyError)
	require.False(t, serveRunnerCalled)
}

func TestUpRejectsNonBusyUnhealthySupervisor(t *testing.T) {
	dir, cleanup := tempUp(t, "unhealthy-operators")
	defer cleanup()
	serveRunnerCalled := false
	stubCallAndServe(t,
		func(*application, request) (*response, error) {
			return &response{
				OK:                        false,
				Error:                     "operator profile is not ready",
				SupervisorProtocolVersion: supervisorProtocolVersion,
			}, nil
		},
		func(*application, string) (*response, error) {
			serveRunnerCalled = true
			return nil, errors.New("serveRunner must not run for an unhealthy Supervisor")
		},
	)
	application := newApplication()
	application.session = "unhealthy-operators"
	err := application.up()
	require.EqualError(t, err, "session unhealthy: operator profile is not ready; see "+filepath.Join(dir, "supervisor.log"))
	require.False(t, serveRunnerCalled)
}

func TestUpRejectsLockedButSilentSession(t *testing.T) {
	dir, cleanup := tempUp(t, "locked-silent")
	defer cleanup()
	holder, err := os.OpenFile(filepath.Join(dir, "supervisor.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	require.NoError(t, err)
	defer holder.Close()
	require.NoError(t, syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	stubCallAndServe(t,
		func(*application, request) (*response, error) {
			return nil, errors.New("connect: no such file or directory")
		},
		nil,
	)
	application := newApplication()
	application.session = "locked-silent"
	err = application.up()
	require.EqualError(t, err, labBusyError)
}

func TestUpReusesHealthyRunningSupervisor(t *testing.T) {
	_, cleanup := tempUp(t, "healthy-reuse")
	defer cleanup()
	var serveRunnerCalled bool
	stubCallAndServe(t,
		func(*application, request) (*response, error) {
			return &response{
				OK:                        true,
				Status:                    statusReady,
				Scopes:                    readyScopes(),
				SupervisorProtocolVersion: supervisorProtocolVersion,
			}, nil
		},
		func(*application, string) (*response, error) {
			serveRunnerCalled = true
			return nil, errors.New("serveRunner must not run on reuse")
		},
	)
	application := newApplication()
	application.json = true
	application.session = "healthy-reuse"
	stdout, err := captureStdout(t, func() error {
		return application.up()
	})
	require.NoError(t, err)
	require.False(t, serveRunnerCalled, "serveRunner must not run when a current-protocol Supervisor already answers OK")
	var decoded struct {
		OK                        bool   `json:"ok"`
		Status                    string `json:"status"`
		Protocol                  int    `json:"protocol"`
		SupervisorProtocolVersion int    `json:"supervisorProtocolVersion"`
		Scopes                    []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.True(t, decoded.OK)
	require.Equal(t, statusReady, decoded.Status)
	require.Len(t, decoded.Scopes, len(lab.ScopeNames()))
	for _, scope := range decoded.Scopes {
		require.Equal(t, string(lab.StateReady), scope.State)
	}
	require.EqualValues(t, cliProtocolVersion, decoded.Protocol)
	require.EqualValues(t, supervisorProtocolVersion, decoded.SupervisorProtocolVersion)
}

func TestUpRechecksShutdownMarkerBeforeSpawn(t *testing.T) {
	// Marker is absent at the first check (so up proceeds past the gate) but
	// the test stub plants it before the second check (the TOCTOU re-check
	// immediately before serveRunner). The serveRunner stub would t.Fatal
	// if invoked, proving the re-check blocked the spawn.
	_, cleanup := tempUp(t, "marker-recheck")
	defer cleanup()

	stubCallAndServe(t,
		func(*application, request) (*response, error) {
			return nil, errors.New("connect: no such file or directory")
		},
		func(*application, string) (*response, error) {
			t.Fatal("serveRunner must not run when the second marker check rejects up")
			return nil, nil
		},
	)

	session := "marker-recheck"
	dir, _, err := sessionPaths(session)
	require.NoError(t, err)
	_ = dir

	var callCount int
	savedCheck := shutdownMarkerCheck
	t.Cleanup(func() { shutdownMarkerCheck = savedCheck })
	shutdownMarkerCheck = func(directory string) error {
		callCount++
		if callCount == 1 {
			return nil
		}
		require.NoError(t, os.WriteFile(filepath.Join(directory, shutdownMarkerName),
			[]byte(`{"status":"FAILED","step":"stop-vm","reason":"qemu timed out"}`), 0o600))
		return checkShutdownMarker(directory)
	}

	application := newApplication()
	application.session = session
	err = application.up()
	require.EqualError(t, err, `previous down failed at step "stop-vm": qemu timed out; run 'yanet-lab down' to retry cleanup`)
	require.GreaterOrEqual(t, callCount, 2, "up must invoke the marker check at least twice (initial + re-check before spawn)")
}

func TestUpAnnotatesReplacementAfterStaleTeardown(t *testing.T) {
	_, cleanup := tempUp(t, "replacement-test")
	defer cleanup()
	var callIndex int
	stubCallAndServe(t,
		func(m *application, value request) (*response, error) {
			callIndex++
			switch callIndex {
			case 1:
				// shutdownStaleSupervisor's initial status: stale protocol.
				return &response{OK: true, SupervisorProtocolVersion: 1}, nil
			case 2:
				// shutdownStaleSupervisor's down: success.
				return &response{OK: true, Output: "lab stopped"}, nil
			case 3:
				// shutdownStaleSupervisor's post-teardown poll: socket gone.
				return nil, errors.New("connect: no such file or directory")
			}
			return nil, errors.New("unexpected call")
		},
		func(*application, string) (*response, error) {
			return &response{
				OK:                        true,
				Output:                    "VM: running",
				SupervisorProtocolVersion: supervisorProtocolVersion,
			}, nil
		},
	)
	application := newApplication()
	application.json = true
	application.session = "replacement-test"
	stdout, err := captureStdout(t, func() error {
		return application.up()
	})
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout, &decoded))
	require.Equal(t, "replaced stale supervisor (protocol 1); VM: running", decoded["output"])
	require.EqualValues(t, cliProtocolVersion, decoded["protocol"])
	require.EqualValues(t, supervisorProtocolVersion, decoded["supervisorProtocolVersion"])
	require.Equal(t, true, decoded["ok"])
}

func TestSessionUnhealthyIncludesReasonAndLogPath(t *testing.T) {
	err := sessionUnhealthy("lab is busy", "/tmp/yanet2-lab-0/abc/default/supervisor.log")
	require.EqualError(t, err, "session unhealthy: lab is busy; see /tmp/yanet2-lab-0/abc/default/supervisor.log")
}

func TestNoteProtocolReplacement(t *testing.T) {
	require.Equal(t, "replaced stale supervisor (protocol 1)", noteProtocolReplacement("", 1))
	require.Equal(t, "replaced stale supervisor (protocol 1); VM: running", noteProtocolReplacement("VM: running", 1))
	require.Equal(t, "replaced stale supervisor (protocol 0)", noteProtocolReplacement("", 0))
}

func TestProbeSessionLock(t *testing.T) {
	t.Run("absent lock file", func(t *testing.T) {
		require.NoError(t, probeSessionLock(t.TempDir()))
	})
	t.Run("free lock file", func(t *testing.T) {
		dir := t.TempDir()
		lock, err := os.OpenFile(filepath.Join(dir, "supervisor.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		require.NoError(t, err)
		require.NoError(t, lock.Close())
		require.NoError(t, probeSessionLock(dir))
	})
	t.Run("held lock file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "supervisor.lock")
		holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		require.NoError(t, err)
		defer holder.Close()
		require.NoError(t, syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
		err = probeSessionLock(dir)
		require.ErrorIs(t, err, errLabBusy)
		require.EqualError(t, err, labBusyError)
	})
}

func TestCheckShutdownMarkerAbsentIsNotBlocking(t *testing.T) {
	require.NoError(t, checkShutdownMarker(t.TempDir()))
}

func TestCheckShutdownMarkerAcceptsValidMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, shutdownMarkerName)
	require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"stop-vm","reason":"qemu timed out"}`), 0o600))
	err := checkShutdownMarker(dir)
	require.EqualError(t, err, `previous down failed at step "stop-vm": qemu timed out; run 'yanet-lab down' to retry cleanup`)
}

func TestCheckShutdownMarkerRejectsInvalidStates(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(string) string
	}{
		{
			name: "wrong mode",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"stop-vm","reason":"qemu timed out"}`), 0o600))
				// Open with explicit chmod after write so a restrictive umask
				// cannot silently turn this into a 0600 success case.
				require.NoError(t, os.Chmod(path, 0o644))
				return path
			},
		},
		{
			name: "symlink",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.Symlink("/etc/passwd", path))
				return path
			},
		},
		{
			name: "malformed JSON",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
				return path
			},
		},
		{
			name: "extra field",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"stop-vm","reason":"qemu timed out","extra":"x"}`), 0o600))
				return path
			},
		},
		{
			name: "missing field",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"stop-vm"}`), 0o600))
				return path
			},
		},
		{
			name: "wrong status",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"OK","step":"stop-vm","reason":"qemu timed out"}`), 0o600))
				return path
			},
		},
		{
			name: "empty step",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"","reason":"qemu timed out"}`), 0o600))
				return path
			},
		},
		{
			name: "non-string reason",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"stop-vm","reason":5}`), 0o600))
				return path
			},
		},
		{
			name: "oversize marker",
			prepare: func(dir string) string {
				path := filepath.Join(dir, shutdownMarkerName)
				padding := strings.Repeat("x", maxShutdownMarkerBytes+1)
				require.NoError(t, os.WriteFile(path, []byte(`{"status":"FAILED","step":"`+padding+`","reason":"r"}`), 0o600))
				return path
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.prepare(dir)
			err := checkShutdownMarker(dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), "shutdown state unknown")
			require.Contains(t, err.Error(), "yanet-lab down")
		})
	}
}

func TestResetHappyPath(t *testing.T) {
	savedProbe := baselineReadyProbe
	t.Cleanup(func() { baselineReadyProbe = savedProbe })
	baselineReadyProbe = func() bool { return true }

	state := &supervisor{}

	server, client := net.Pipe()
	defer client.Close()
	directory := t.TempDir()

	restoreRan := 0
	restore := func() error {
		restoreRan++
		return nil
	}

	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, restore, nil, func() {})
		return nil
	})

	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "reset"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)
	require.Equal(t, "baseline restored", reply.Output)
	require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
	require.NoError(t, handlers.Wait())
	require.Equal(t, 1, restoreRan)
}

func TestResetRejectsMissingBaseline(t *testing.T) {
	savedProbe := baselineReadyProbe
	t.Cleanup(func() { baselineReadyProbe = savedProbe })
	baselineReadyProbe = func() bool { return false }

	state := &supervisor{}

	server, client := net.Pipe()
	defer client.Close()
	directory := t.TempDir()

	restore := func() error {
		t.Fatal("restore must not run when baselineReadyProbe returns false")
		return nil
	}

	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, restore, nil, func() {})
		return nil
	})

	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "reset"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.False(t, reply.OK)
	require.Equal(t, "baseline snapshot missing; run up again", reply.Error)
	require.Equal(t, supervisorProtocolVersion, reply.SupervisorProtocolVersion)
	require.NoError(t, handlers.Wait())

	_, statErr := os.Stat(filepath.Join(directory, shutdownMarkerName))
	require.True(t, os.IsNotExist(statErr), "no shutdown marker should be written when reset is rejected, got err=%v", statErr)
}

func TestWriteShutdownMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeShutdownMarker(dir, "down", "sentinel failure"))

	path := filepath.Join(dir, shutdownMarkerName)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "marker must be a regular file")
	require.Zero(t, info.Mode()&os.ModeSymlink, "marker must not be a symlink")
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, map[string]string{"status": "FAILED", "step": "down", "reason": "sentinel failure"}, got)
}

func TestDownWritesShutdownMarkerOnError(t *testing.T) {
	state := &supervisor{}
	sentinel := errors.New("test sentinel failure")
	shutdown := func() error { return sentinel }

	server, client := net.Pipe()
	defer client.Close()
	directory := t.TempDir()
	stopCalled := make(chan struct{})

	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, nil, shutdown, func() { close(stopCalled) })
		return nil
	})

	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "down"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)
	require.Equal(t, "lab stopped", reply.Output)

	select {
	case <-stopCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("stop callback not invoked within 5s")
	}
	require.NoError(t, handlers.Wait())

	path := filepath.Join(directory, shutdownMarkerName)
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "marker must be a regular file")
	require.Zero(t, info.Mode()&os.ModeSymlink, "marker must not be a symlink")
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, map[string]string{"status": "FAILED", "step": "down", "reason": sentinel.Error()}, got)

	require.EqualError(t, checkShutdownMarker(directory),
		fmt.Sprintf("previous down failed at step \"down\": %s; run 'yanet-lab down' to retry cleanup", sentinel.Error()))
}

func TestDownRemovesPriorMarkerOnSuccess(t *testing.T) {
	state := &supervisor{}
	shutdown := func() error { return nil }

	directory := t.TempDir()
	require.NoError(t, writeShutdownMarker(directory, "down", "prior failure"))
	_, err := os.Stat(filepath.Join(directory, shutdownMarkerName))
	require.NoError(t, err)

	server, client := net.Pipe()
	defer client.Close()
	stopCalled := make(chan struct{})

	var handlers errgroup.Group
	handlers.Go(func() error {
		handleConnection(server, nil, directory, state, nil, shutdown, func() { close(stopCalled) })
		return nil
	})

	require.NoError(t, json.NewEncoder(client).Encode(request{Action: "down"}))
	var reply response
	require.NoError(t, json.NewDecoder(client).Decode(&reply))
	require.True(t, reply.OK)

	select {
	case <-stopCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("stop callback not invoked within 5s")
	}
	require.NoError(t, handlers.Wait())

	_, err = os.Stat(filepath.Join(directory, shutdownMarkerName))
	require.True(t, os.IsNotExist(err), "prior marker should be gone after successful down, got err=%v", err)
}
