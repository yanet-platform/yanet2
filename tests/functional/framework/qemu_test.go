package framework

import (
	"strings"
	"testing"
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
