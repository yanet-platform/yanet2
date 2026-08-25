package lab_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yanet-platform/yanet2/lab"
)

func TestParseManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "packet.pcap"), []byte("fixture"), 0o600); err != nil {
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
`), dir)
	if err != nil {
		t.Fatalf("lab.ParseManifest() error = %v", err)
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
	_, err := lab.ParseManifest([]byte("version: 1\nname: smoke\nboot:\n  dataplane: ../secret\n"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestParseManifestRejectsSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "packet.pcap")
	if err := os.WriteFile(outside, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "packet.pcap")); err != nil {
		t.Fatal(err)
	}
	_, err := lab.ParseManifest([]byte("version: 1\nname: smoke\nprobes:\n  - name: packet\n    ingress: 0\n    egress: 1\n    send: {pcap: packet.pcap}\n    expect: {drop: true}\n"), dir)
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
