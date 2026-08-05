package framework

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
			want:        "/tmp/yanet-test-baseline-v7.qcow2",
		},
		{
			name:        "custom baseline",
			qemuImage:   "/tmp/yanet-test.qcow2",
			baselineTag: "nat64",
			want:        "/tmp/yanet-test-nat64-v7.qcow2",
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

func TestHashFileDetectsChangedContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	stamp := time.Unix(1, 0)
	if err := os.WriteFile(path, []byte("one!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	first := sha256.New()
	if err := hashFile(first, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	second := sha256.New()
	if err := hashFile(second, path); err != nil {
		t.Fatal(err)
	}
	if string(first.Sum(nil)) == string(second.Sum(nil)) {
		t.Fatal("same-size artifact content change did not change fingerprint")
	}
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
