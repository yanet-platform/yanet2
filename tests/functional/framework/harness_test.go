package framework

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBaselineTemplatePath(t *testing.T) {
	testCases := []struct {
		name        string
		qemuImage   string
		baselineTag string
		want        string
	}{
		{
			name:        "default baseline",
			qemuImage:   "/tmp/yanet-test.qcow2",
			baselineTag: baselineSnapshotName,
			want:        "/tmp/yanet-test-baseline-v8.qcow2",
		},
		{
			name:        "custom baseline",
			qemuImage:   "/tmp/yanet-test.qcow2",
			baselineTag: "nat64",
			want:        "/tmp/yanet-test-nat64-v8.qcow2",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := baselineTemplatePath(testCase.qemuImage, testCase.baselineTag); got != testCase.want {
				t.Errorf("baselineTemplatePath() = %q, want %q", got, testCase.want)
			}
		})
	}

	if baselineSnapshotName != "baseline" {
		t.Errorf("baseline snapshot name = %q, want %q", baselineSnapshotName, "baseline")
	}
}

func TestBootedTemplatePath(t *testing.T) {
	want := "/tmp/yanet-test-booted-v3.qcow2"
	if got := BootedImagePath("/tmp/yanet-test.qcow2"); got != want {
		t.Errorf("BootedImagePath() = %q, want %q", got, want)
	}
}

// Test_BaselineTemplatePathFor_SeparatesVMSizes verifies that a non-default
// size names its own baseline file.
//
// The suffix sits outside the fingerprint segment that pruning wildcards,
// while the default size keeps today's exact name.
func Test_BaselineTemplatePathFor_SeparatesVMSizes(t *testing.T) {
	custom := VMSize{CPUs: 4, Memory: "2G"}

	require.Equal(t, "/tmp/yanet-test-baseline-1111111111111111-v8-c4-m2G.qcow2",
		baselineTemplatePathFor("/tmp/yanet-test.qcow2", baselineSnapshotName+"-1111111111111111", custom))
	require.Equal(t, "/tmp/yanet-test-baseline-1111111111111111-v8.qcow2",
		baselineTemplatePathFor("/tmp/yanet-test.qcow2", baselineSnapshotName+"-1111111111111111", DefaultVMSize()))
}

// Test_PruneSupersededBaselines_KeepsOtherVMSizes verifies that pruning
// one VM size's stale baselines never removes another size's cached one.
//
// A resized "up" must not evict the default-size baseline, and a default
// "up" must not evict one built for a resized VM, since each loads only
// into a machine of the vCPU and RAM shape it was taken on.
func Test_PruneSupersededBaselines_KeepsOtherVMSizes(t *testing.T) {
	dir := t.TempDir()
	qemuImage := filepath.Join(dir, "yanet-test.qcow2")
	log := zap.NewNop().Sugar()
	customSize := VMSize{CPUs: 4, Memory: "2G"}

	staleDefault := baselineTemplatePathFor(qemuImage, baselineSnapshotName+"-1111111111111111", DefaultVMSize())
	staleCustom := baselineTemplatePathFor(qemuImage, baselineSnapshotName+"-2222222222222222", customSize)
	writeFingerprintTestFile(t, staleDefault)
	writeFingerprintTestFile(t, staleCustom)

	freshDefault := baselineTemplatePathFor(qemuImage, baselineSnapshotName+"-3333333333333333", DefaultVMSize())
	writeFingerprintTestFile(t, freshDefault)
	pruneSupersededBaselines(freshDefault, "3333333333333333", DefaultVMSize(), log)

	require.NoFileExists(t, staleDefault)
	require.FileExists(t, staleCustom)

	freshCustom := baselineTemplatePathFor(qemuImage, baselineSnapshotName+"-4444444444444444", customSize)
	writeFingerprintTestFile(t, freshCustom)
	pruneSupersededBaselines(freshCustom, "4444444444444444", customSize, log)

	require.NoFileExists(t, staleCustom)
	require.FileExists(t, freshDefault)
}

// TestDefaultRouteConfig verifies that the baseline route0.yaml uses the
// wire's range-native "range: {start, end}" shape, not the retired "prefix"
// key, and that the IPv6 default route's "::" start is quoted -- an
// unquoted bare colon parses as a YAML mapping indicator, not string
// content.
func TestDefaultRouteConfig(t *testing.T) {
	config := DefaultRouteConfig()

	if strings.Contains(config, "prefix:") {
		t.Errorf("DefaultRouteConfig() must not use the retired prefix key: %s", config)
	}
	if !strings.Contains(config, `start: "0.0.0.0"`) || !strings.Contains(config, `end: "255.255.255.255"`) {
		t.Errorf("DefaultRouteConfig() missing IPv4 default range: %s", config)
	}
	if !strings.Contains(config, `start: "::"`) {
		t.Errorf("DefaultRouteConfig() must quote the IPv6 :: start: %s", config)
	}
	if !strings.Contains(config, `end: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"`) {
		t.Errorf("DefaultRouteConfig() missing IPv6 default range end: %s", config)
	}
}

func TestStatFingerprintDetectsChangedSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	stamp := time.Unix(1, 0)

	if err := os.WriteFile(path, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	first := sha256.New()
	if err := statFingerprint(first, path); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("large!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	second := sha256.New()
	if err := statFingerprint(second, path); err != nil {
		t.Fatal(err)
	}
	if string(first.Sum(nil)) == string(second.Sum(nil)) {
		t.Fatal("different-size artifact did not change fingerprint")
	}
}

func TestStatFingerprintDetectsChangedMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("same-size"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := sha256.New()
	if err := statFingerprint(first, path); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	second := sha256.New()
	if err := statFingerprint(second, path); err != nil {
		t.Fatal(err)
	}
	if string(first.Sum(nil)) == string(second.Sum(nil)) {
		t.Fatal("same-size artifact with fresh mtime did not change fingerprint")
	}
}

// Test_InvalidateFingerprint_MakesCachedBaselineInvalid verifies that a failed
// preferred-template startup forces the next run to rebuild the baseline.
func Test_InvalidateFingerprint_MakesCachedBaselineInvalid(t *testing.T) {
	baselineTemplate := filepath.Join(t.TempDir(), "baseline.qcow2")
	require.NoError(t, writeFingerprint(baselineTemplate, "fingerprint"))
	require.True(t, fingerprintMatches(baselineTemplate, "fingerprint"))

	require.NoError(t, invalidateFingerprint(baselineTemplate))
	require.False(t, fingerprintMatches(baselineTemplate, "fingerprint"))
	require.NoError(t, invalidateFingerprint(baselineTemplate))
}

// Test_BaselineFingerprint_IncludesVMSize verifies that a non-default VM
// size changes the baseline fingerprint.
//
// The lab must never reuse a cached baseline snapshot taken on a machine of
// a different shape; a caller that never set a VM size override must still
// see the same fingerprint it saw before the override existed, checked
// against a hash computed with no VM size input at all.
func Test_BaselineFingerprint_IncludesVMSize(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "image.qcow2")
	for _, path := range []string{
		image,
		filepath.Join(root, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(root, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(root, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
	} {
		writeFingerprintTestFile(t, path)
	}
	for _, name := range CLIBinaryNames {
		writeFingerprintTestFile(t, filepath.Join(root, "target", "release", name))
	}
	fingerprint := func(size VMSize) string {
		value, err := baselineFingerprint(root, image, "dp", "cp", "fwd", "route", nil, false, size)
		require.NoError(t, err)
		return value
	}
	legacyFingerprint, err := legacyBaselineFingerprint(root, image, "dp", "cp", "fwd", "route", nil, false)
	require.NoError(t, err)

	require.Equal(t, legacyFingerprint, fingerprint(DefaultVMSize()))
	require.NotEqual(t, legacyFingerprint, fingerprint(VMSize{CPUs: 8, Memory: "16G"}))
}

// legacyBaselineFingerprint reproduces baselineFingerprint's hash with no
// VM size input at all, exactly as it was before the size became
// configurable.
//
// Test_BaselineFingerprint_IncludesVMSize pins the default size to this,
// since a diverging hash would silently invalidate every cache key a
// caller relied on before the override existed.
func legacyBaselineFingerprint(projectRoot, qemuImage, dataplane, controlplane, forward, route string, extraFiles []string, skipCommonConfig bool) (string, error) {
	hash := sha256.New()
	for _, value := range []string{"dataplane", dataplane, "controlplane", controlplane, "forward", forward, "route", route, "skipCommonConfig", strconv.FormatBool(skipCommonConfig)} {
		_, _ = io.WriteString(hash, value)
		_, _ = io.WriteString(hash, "\x00")
	}

	image, err := os.Stat(qemuImage)
	if err != nil {
		return "", err
	}
	imagePath, err := filepath.EvalSymlinks(qemuImage)
	if err != nil {
		return "", err
	}
	_, _ = io.WriteString(hash, imagePath)
	_, _ = io.WriteString(hash, fmt.Sprintf("\x00%d\x00%d", image.Size(), image.ModTime().UnixNano()))

	paths := []string{
		filepath.Join(projectRoot, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(projectRoot, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(projectRoot, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
	}
	for _, path := range extraFiles {
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectRoot, path)
		}
		paths = append(paths, path)
	}
	for _, name := range CLIBinaryNames {
		paths = append(paths, filepath.Join(projectRoot, "target", "release", name))
	}
	plugins, err := filepath.Glob(filepath.Join(projectRoot, "build", "modules", "*", "dataplane", "*_dp_plugin.so"))
	if err != nil {
		return "", err
	}
	paths = append(paths, plugins...)
	sort.Strings(paths)
	for _, path := range paths {
		if err := statFingerprint(hash, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// writeFingerprintTestFile creates a small file at path, including its
// parent directories, as a stat-based fingerprint input.
func writeFingerprintTestFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
}

func TestRunProfileHooks(t *testing.T) {
	startError := errors.New("start failed")
	readyError := errors.New("not ready")
	testCases := []struct {
		name       string
		startError error
		readyError error
		wantCalls  []string
		wantError  string
	}{
		{name: "success", wantCalls: []string{"start", "ready"}},
		{name: "start failure", startError: startError, wantCalls: []string{"start"}, wantError: "start profile: start failed"},
		{name: "readiness failure", readyError: readyError, wantCalls: []string{"start", "ready"}, wantError: "wait for profile readiness: not ready"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var calls []string
			err := runProfileHooks(
				&TestFramework{},
				func(*TestFramework) error {
					calls = append(calls, "start")
					return testCase.startError
				},
				func(*TestFramework) error {
					calls = append(calls, "ready")
					return testCase.readyError
				},
			)

			require.Equal(t, testCase.wantCalls, calls)
			if testCase.wantError == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, testCase.wantError)
			}
		})
	}
}
