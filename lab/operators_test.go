package lab_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
)

func TestMatchesForwardingProbe(t *testing.T) {
	expected := lab.ForwardingExpectedBytes()

	cases := []struct {
		name   string
		packet []byte
		want   bool
	}{
		{name: "exact match", packet: append([]byte(nil), expected...), want: true},
		{name: "zero-padded to 60 bytes", packet: append(append([]byte(nil), expected...), make([]byte, 60-len(expected))...), want: true},
		{name: "short packet", packet: expected[:len(expected)-5], want: false},
		{name: "empty packet", packet: []byte{}, want: false},
		{name: "nil packet", packet: nil, want: false},
		{name: "mismatched content", packet: make([]byte, len(expected)), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lab.MatchesForwardingProbe(tc.packet)
			if got != tc.want {
				t.Errorf("MatchesForwardingProbe() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequiredArtifacts(t *testing.T) {
	root := t.TempDir()

	artifacts := lab.RequiredArtifacts(root)
	if len(artifacts) == 0 {
		t.Fatal("RequiredArtifacts returned no paths")
	}

	for _, path := range artifacts {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	artifacts = lab.RequiredArtifacts(root)
	for _, path := range artifacts {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("RequiredArtifacts path %q does not exist: %v", path, err)
		}
	}

	required := []string{
		filepath.Join(root, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(root, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(root, "build", "operators", "route", "yanet-route-operator"),
		filepath.Join(root, "build", "operators", "forward", "yanet-forward-operator"),
		filepath.Join(root, "build", "operators", "decap", "yanet-decap-operator"),
		filepath.Join(root, "build", "operators", "pipeline", "yanet-pipeline-operator"),
		filepath.Join(root, "build", "operators", "bird-adapter", "yanet-bird-adapter"),
		filepath.Join(root, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
	}
	for _, path := range required {
		require.Contains(t, artifacts, path, "RequiredArtifacts must include %s", path)
	}
}
