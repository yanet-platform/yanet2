package framework

import (
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestSerialBufferCapsAndRetainsTail(t *testing.T) {
	q := &QEMUManager{}
	marker := "MARKER-XYZ-12345"
	// Feed enough chunks to exceed the buffer cap + margin (8 MiB + 2 MiB).
	const chunk = 64 << 10
	totalChunks := int((maxSerialBufferSize+serialTrimMargin)/chunk) + 10
	for i := range totalChunks {
		block := strings.Repeat("x", chunk)
		if i == totalChunks-1 {
			block = marker + block[len(marker):]
		}
		q.serialBuffer.WriteString(block)
		if q.serialBuffer.Len() > maxSerialBufferSize+serialTrimMargin {
			data := q.serialBuffer.Bytes()
			keep := append([]byte(nil), data[len(data)-maxSerialBufferSize/2:]...)
			q.serialBuffer.Reset()
			q.serialBuffer.Write(keep)
		}
	}
	if got := q.serialBuffer.Len(); got > maxSerialBufferSize+serialTrimMargin {
		t.Fatalf("buffer exceeded cap+margin: got %d bytes", got)
	}
	if !q.serialBufferContains(marker) {
		t.Fatal("most recently written marker not found in buffer")
	}
}

func TestSerialBufferDiscardThrough(t *testing.T) {
	q := &QEMUManager{}
	q.serialBuffer.WriteString("prefix-marker-tail")
	q.discardSerialThrough("marker")
	got := q.serialBufferSnapshot()
	if got != "-tail" {
		t.Fatalf("after discard, buffer = %q, want %q", got, "-tail")
	}
}

func TestSerialBufferContains(t *testing.T) {
	q := &QEMUManager{}
	q.serialBuffer.WriteString("hello world")
	if !q.serialBufferContains("world") {
		t.Fatal("expected contains to find marker")
	}
	if q.serialBufferContains("missing") {
		t.Fatal("expected contains to miss absent marker")
	}
}

func TestNewQEMUManagerUsesProvidedProjectRoot(t *testing.T) {
	root := t.TempDir()
	manager, err := newQEMUManager("root-test", "image.qcow2", zap.NewNop().Sugar(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Stop() })

	if manager.ProjectDir != root {
		t.Fatalf("project directory = %q, want %q", manager.ProjectDir, root)
	}
	if manager.BuildDir != filepath.Join(root, "build") {
		t.Fatalf("build directory = %q, want %q", manager.BuildDir, filepath.Join(root, "build"))
	}
	if manager.TargetDir != filepath.Join(root, "target") {
		t.Fatalf("target directory = %q, want %q", manager.TargetDir, filepath.Join(root, "target"))
	}
}
