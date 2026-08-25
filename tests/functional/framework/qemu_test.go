package framework

import (
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

func TestExistingVMPattern_DetectsRealQEMU(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.True(
		t,
		findPid(t, pattern, qemuCmdline(vmName)),
		"pattern must match a real qemu-system-x86_64 argv containing -name <vm>",
	)
}

func TestExistingVMPattern_RejectsOtherQEMU(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.False(
		t,
		findPid(t, pattern, "12345 qemu-system-x86_64 -name other-vm -smp 2"),
		"pattern must not match a qemu running a different VM",
	)
}

func TestExistingVMPattern_RejectsSimilarName(t *testing.T) {
	const vmName = "yanet-test-vm-suite"
	pattern := existingVMPattern(vmName)

	require.False(
		t,
		findPid(t, pattern, qemuCmdline("yanet-test-vm-suite-other")),
		"trailing boundary must stop shorter-name matches on longer names",
	)
}

func TestExistingVMPattern_RejectsPgrepSelf(t *testing.T) {
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

func TestCheckForExistingVMRun_EmptyOutputIsOK(t *testing.T) {
	q := &QEMUManager{
		Name: "main",
		log:  zap.NewNop().Sugar(),
	}
	err := checkForExistingVMRun(q, "yanet-test-vm-suite", func(string) string {
		return ""
	})
	require.NoError(t, err, "an empty pgrep result must mean no conflict")
}

func TestCheckForExistingVMRun_PopulatedOutputIsError(t *testing.T) {
	q := &QEMUManager{
		Name: "main",
		log:  zap.NewNop().Sugar(),
	}
	err := checkForExistingVMRun(
		q,
		"yanet-test-vm-suite",
		func(string) string {
			return "12345 qemu-system-x86_64 -name yanet-test-vm-suite"
		},
	)
	require.Error(t, err, "non-empty pgrep result must surface as an error")
	require.True(
		t,
		strings.Contains(err.Error(), "yanet-test-vm-suite"),
		"error must mention the conflicting VM name",
	)
}
