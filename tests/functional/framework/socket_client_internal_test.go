package framework

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestReceivePacketClassifiesLengthPrefixTimeout(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		peerConn.Close()
	})
	client := &SocketClient{
		inner: &socketClientInner{conn: clientConn},
		log:   zap.NewNop().Sugar(),
	}

	_, err := client.ReceivePacket(10*time.Millisecond, "")
	if !errors.Is(err, ErrCaptureTimeout) {
		t.Fatalf("error = %v, want ErrCaptureTimeout", err)
	}
}

func TestReceivePacketDoesNotClassifyPacketDataTimeoutAsCaptureTimeout(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		peerConn.Close()
	})
	client := &SocketClient{
		inner: &socketClientInner{conn: clientConn},
		log:   zap.NewNop().Sugar(),
	}

	partialPacket := make([]byte, 6)
	binary.BigEndian.PutUint32(partialPacket, 8)
	go func() {
		_, _ = peerConn.Write(partialPacket)
	}()

	_, err := client.ReceivePacket(10*time.Millisecond, "")
	if err == nil {
		t.Fatal("expected packet data timeout")
	}
	if errors.Is(err, ErrCaptureTimeout) {
		t.Fatalf("error = %v, must not be ErrCaptureTimeout", err)
	}
}

func TestReceivePacketDoesNotClassifyPartialLengthPrefixAsCaptureTimeout(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		peerConn.Close()
	})
	client := &SocketClient{
		inner: &socketClientInner{conn: clientConn},
		log:   zap.NewNop().Sugar(),
	}

	go func() {
		_, _ = peerConn.Write([]byte{0, 0})
	}()

	_, err := client.ReceivePacket(10*time.Millisecond, "")
	if err == nil {
		t.Fatal("expected partial packet length prefix timeout")
	}
	if errors.Is(err, ErrCaptureTimeout) {
		t.Fatalf("error = %v, must not be ErrCaptureTimeout", err)
	}
}
