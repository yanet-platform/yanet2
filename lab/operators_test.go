package lab

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func TestMatchesForwardingProbe(t *testing.T) {
	testCases := []struct {
		name   string
		packet []byte
		want   bool
	}{
		{
			name:   "exact match",
			packet: append([]byte(nil), forwardingExpected...),
			want:   true,
		},
		{
			name:   "zero-padded to 60 bytes",
			packet: append(append([]byte(nil), forwardingExpected...), make([]byte, 60-len(forwardingExpected))...),
			want:   true,
		},
		{
			name:   "short packet",
			packet: forwardingExpected[:len(forwardingExpected)-5],
			want:   false,
		},
		{
			name:   "empty packet",
			packet: []byte{},
			want:   false,
		},
		{
			name:   "nil packet",
			packet: nil,
			want:   false,
		},
		{
			name:   "mismatched content",
			packet: make([]byte, len(forwardingExpected)),
			want:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesForwardingProbe(tc.packet)
			if got != tc.want {
				t.Errorf("matchesForwardingProbe() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequiredArtifacts(t *testing.T) {
	root := t.TempDir()

	// Create expected directories and files.
	dirs := []string{
		"build/dataplane",
		"build/controlplane",
		"build/operators/route",
		"build/operators/forward",
		"build/operators/decap",
		"build/operators/pipeline",
		"build/operators/bird-adapter",
		"subprojects/dpdk/usertools",
		"target/release",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := []string{
		"build/dataplane/yanet-dataplane",
		"build/controlplane/yanet-controlplane",
		"subprojects/dpdk/usertools/dpdk-devbind.py",
		"build/operators/route/yanet-route-operator",
		"build/operators/forward/yanet-forward-operator",
		"build/operators/decap/yanet-decap-operator",
		"build/operators/pipeline/yanet-pipeline-operator",
		"build/operators/bird-adapter/yanet-bird-adapter",
	}
	for _, name := range operatorCLIs {
		files = append(files, "target/release/"+name)
	}
	for _, name := range framework.CLIBinaryNames {
		files = append(files, "target/release/"+name)
	}
	for _, file := range files {
		path := filepath.Join(root, file)
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	paths := RequiredArtifacts(root)
	if len(paths) == 0 {
		t.Fatal("RequiredArtifacts returned no paths")
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("RequiredArtifacts path %q does not exist: %v", path, err)
		}
	}
}
