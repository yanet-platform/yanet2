package lab_test

import (
	"os"
	"path/filepath"
	"testing"

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
}
