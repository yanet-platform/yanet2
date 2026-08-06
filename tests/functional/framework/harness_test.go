package framework

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
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
			want:        "/tmp/yanet-test-baseline-v2.qcow2",
		},
		{
			name:        "custom baseline",
			qemuImage:   "/tmp/yanet-test.qcow2",
			baselineTag: "nat64",
			want:        "/tmp/yanet-test-nat64-v2.qcow2",
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
