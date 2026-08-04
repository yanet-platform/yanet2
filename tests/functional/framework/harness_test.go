package framework

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"
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
