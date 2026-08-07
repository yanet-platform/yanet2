package lab_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/lab"
)

func TestParseManifest(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "packet.pcap"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := lab.ParseManifest([]byte(`
version: 1
name: smoke
steps:
  - name: inspect
    argv: [yanet-cli, inspect]
probes:
  - name: packet
    ingress: 0
    egress: 1
    send: {pcap: packet.pcap}
    expect: {drop: true}
`), directory)
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if manifest.Name != "smoke" || len(manifest.Probes) != 1 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestParseManifestRejectsUnknownFields(t *testing.T) {
	_, err := lab.ParseManifest([]byte("version: 1\nname: smoke\nunknown: true\n"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected schema error, got %v", err)
	}
}

func TestParseManifestRejectsTraversal(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		errText  string
	}{
		{name: "relative", manifest: "version: 1\nname: smoke\nboot:\n  dataplane: ../secret\n", errText: "escapes"},
		{name: "absolute", manifest: "version: 1\nname: smoke\nboot:\n  dataplane: /etc/passwd\n", errText: "relative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lab.ParseManifest([]byte(tc.manifest), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.errText) {
				t.Fatalf("expected %q error, got %v", tc.errText, err)
			}
		})
	}
}

func TestParseManifestRejectsSymlinkTraversal(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "packet.pcap")
	if err := os.WriteFile(outside, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "packet.pcap")); err != nil {
		t.Fatal(err)
	}
	_, err := lab.ParseManifest([]byte("version: 1\nname: smoke\nprobes:\n  - name: packet\n    ingress: 0\n    egress: 1\n    send: {pcap: packet.pcap}\n    expect: {drop: true}\n"), directory)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected symlink traversal error, got %v", err)
	}
}

func TestParseManifestRejectsDuplicateNames(t *testing.T) {
	_, err := lab.ParseManifest([]byte(`
version: 1
name: smoke
steps:
  - name: duplicate
    argv: ["true"]
  - name: duplicate
    argv: ["false"]
`), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate name error, got %v", err)
	}
}

func TestParseManifestRejectsNonPositiveDurations(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			_, err := lab.ParseManifest([]byte("version: 1\nname: smoke\nsteps:\n  - name: wait\n    argv: [\"true\"]\n    timeout: "+value+"\n"), t.TempDir())
			require.Error(t, err)
		})
	}
}

func TestBuiltInManifestsLoad(t *testing.T) {
	for _, name := range []string{"forward-route", "decap", "nat64"} {
		t.Run(name, func(t *testing.T) {
			if _, err := lab.LoadManifest(filepath.Join("scenarios", name, "manifest.yaml")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTransformationScenariosUseLabExtension(t *testing.T) {
	for _, name := range []string{"decap", "nat64"} {
		t.Run(name, func(t *testing.T) {
			manifest, err := lab.LoadManifest(filepath.Join("scenarios", name, "manifest.yaml"))
			require.NoError(t, err)
			require.NotEmpty(t, manifest.Probes)
			var arguments []string
			for _, step := range manifest.Steps {
				arguments = append(arguments, step.Argv...)
			}
			joined := strings.Join(arguments, " ")
			require.Contains(t, joined, "--name=fn:lab")
			require.NotContains(t, joined, "--name=test")
		})
	}
}
