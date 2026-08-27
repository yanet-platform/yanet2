package framework

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// qemuCmdline returns a representative argv slice for pgrep -f to scan. The
// fake runner pretends pgrep appended the matching PID line; the test only
// exercises the pattern, not pgrep itself.
func qemuCmdline(vmName string) string {
	return "12345 qemu-system-x86_64 -name " + vmName +
		" -smp 2 -m 1G -enable-kvm"
}

// pgrepCmdlineForSelfMatch reproduces the argv pgrep builds when invoking
// itself, so we can verify the [q]emu-system-x86_64 opening class prevents
// pgrep from matching its own command line.
func pgrepCmdlineForSelfMatch(vmName string) string {
	// The first argv element has its first byte replaced so the pattern
	// cannot match it via the [q]emu-system-x86_64 anchor.
	return "99999 /usr/bin/pgrep -f " +
		"[q]emu-system-x86_64.*[[:space:]]-name[[:space:]]+" + vmName +
		"([[:space:]]|$)"
}

// findPid reports whether pattern (a Go regexp) finds a match in cmdline.
// We approximate pgrep's POSIX ERE matching with Go's regexp package, which
// supports the same [[:space:]] class and basic quantifiers used by the
// production pattern.
func findPid(t *testing.T, pattern, cmdline string) bool {
	t.Helper()
	matched, err := regexpMatch(pattern, cmdline)
	require.NoError(t, err)
	return matched
}

// verifies that the built pattern matches a running qemu argv for the same
// VM name.
func Test_ExistingVMPattern_DetectsRealQEMU(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.True(
		t,
		findPid(t, pattern, qemuCmdline(vmName)),
		"pattern must match a real qemu-system-x86_64 argv containing -name <vm>",
	)
}

// verifies that a qemu started for a different VM name never matches.
func Test_ExistingVMPattern_RejectsOtherQEMU(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.False(
		t,
		findPid(t, pattern, "12345 qemu-system-x86_64 -name other-vm -smp 2"),
		"pattern must not match a qemu running a different VM",
	)
}

// verifies that the trailing boundary rejects longer VM names that merely
// contain this one.
func Test_ExistingVMPattern_RejectsSimilarName(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.False(
		t,
		findPid(t, pattern, qemuCmdline("yanet-test-vm-suite-other")),
		"trailing boundary must stop shorter-name matches on longer names",
	)
}

// verifies that the bracketed first token keeps pgrep from matching its
// own argv.
func Test_ExistingVMPattern_RejectsPgrepSelf(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	// The [q]emu-system-x86_64 opening class defeats pgrep's own argv match.
	// pgrep's first argv token is /usr/bin/pgrep, which never starts with 'q'.
	require.False(
		t,
		findPid(t, pattern, pgrepCmdlineForSelfMatch(vmName)),
		"pattern must not match pgrep's own argv when its command line includes the VM name",
	)
}

// verifies that an empty pgrep result reads as no conflict.
func Test_CheckForExistingVMRun_EmptyOutputIsOK(t *testing.T) {
	q := &QEMUManager{
		Name: "main",
		log:  zap.NewNop().Sugar(),
	}
	err := checkForExistingVMRun(
		q, "yanet-test-vm-suite", func(string) (string, error) {
			return "", nil
		},
	)
	require.NoError(t, err, "an empty pgrep result must mean no conflict")
}

// verifies that a non-empty pgrep result fails the check naming the VM.
func Test_CheckForExistingVMRun_PopulatedOutputIsError(t *testing.T) {
	q := &QEMUManager{
		Name: "main",
		log:  zap.NewNop().Sugar(),
	}
	err := checkForExistingVMRun(
		q,
		"yanet-test-vm-suite",
		func(string) (string, error) {
			return "12345 qemu-system-x86_64 -name yanet-test-vm-suite", nil
		},
	)
	require.Error(t, err, "non-empty pgrep result must surface as an error")
	require.True(
		t,
		strings.Contains(err.Error(), "yanet-test-vm-suite"),
		"error must mention the conflicting VM name",
	)
}

// verifies that a failing pgrep (missing binary on the host, unexpected
// exit status) surfaces as an error instead of silently passing the
// duplicate-VM check.
func Test_CheckForExistingVMRun_RunnerErrorIsPropagated(t *testing.T) {
	q := &QEMUManager{
		Name: "main",
		log:  zap.NewNop().Sugar(),
	}
	err := checkForExistingVMRun(
		q,
		"yanet-test-vm-suite",
		func(string) (string, error) {
			return "", errors.New("exec: pgrep: executable file not found")
		},
	)
	require.Error(
		t, err, "a pgrep failure must not be treated as no conflict",
	)
	require.True(
		t,
		strings.Contains(err.Error(), "cannot check for existing VM"),
		"error must state which check failed",
	)
	require.True(
		t,
		strings.Contains(err.Error(), "executable file not found"),
		"underlying pgrep error must be wrapped, not swallowed",
	)
}

// verifies that an existing path without read-write device access disables KVM.
func Test_KVMDeviceAccessible_ReadWriteAccessRequired(t *testing.T) {
	require.False(t, isKVMDeviceAccessible(t.TempDir()))
}

// verifies that a path openable for reading and writing enables KVM.
func Test_KVMDeviceAccessible_ReadWriteAccessAvailable(t *testing.T) {
	device, err := os.CreateTemp(t.TempDir(), "kvm-device-")
	require.NoError(t, err)
	require.NoError(t, device.Close())

	require.True(t, isKVMDeviceAccessible(device.Name()))
}
