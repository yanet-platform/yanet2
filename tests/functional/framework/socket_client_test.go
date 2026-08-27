package framework_test

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestSendAndReceivePacket_LateReply verifies that SendAndReceivePacket still
// reports a reply that lands after the socket protocol's old 100ms read
// timeout, as long as it arrives within ReadTimeout.
//
// The scenario this guards against is measured, not hypothetical: a
// CPU-starved CI runner delivered a reply at 114ms instead of dropping it,
// so a stub that answers past 100ms exercises exactly the case the widened
// timeout fixes.
func TestSendAndReceivePacket_LateReply(t *testing.T) {
	t.Parallel()

	reply := buildReplyFrame([]byte("late-reply"))
	client := newTestClient(t, func(conn net.Conn) {
		drainSentPacket(conn)
		time.Sleep(150 * time.Millisecond)
		writeFramedPacket(conn, reply)
	})

	budget := framework.NewReplyBudget()
	data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
	require.NoError(t, err)
	require.Equal(t, reply, data)
}

// TestSendAndReceivePacket_NoReply verifies that SendAndReceivePacket reports
// no reply, within ReadTimeout, when the peer never answers.
func TestSendAndReceivePacket_NoReply(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	client := newTestClient(t, func(conn net.Conn) {
		drainSentPacket(conn)
		<-release
	})

	budget := framework.NewReplyBudget()
	start := time.Now()
	data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
	elapsed := time.Since(start)
	close(release)

	require.NoError(t, err)
	require.Nil(t, data)
	require.GreaterOrEqual(t, elapsed, framework.ReadTimeout)
}

// TestReplyBudget_HealthyRunDoesNotDrain verifies that a reply arriving
// within ReadTimeout never debits a ReplyBudget's pool, however many times
// it happens in one subtest.
//
// The mutation this guards against drops the timeout guard around the
// existing debit, charging the pool on every read. That drains the pool
// after three healthy round trips, and the fourth call takes the early
// return and reports no reply for a packet that was in fact answered.
func TestReplyBudget_HealthyRunDoesNotDrain(t *testing.T) {
	t.Parallel()

	const roundTrips = 20 // more than the pool could absorb as timeouts (3 at ReadTimeout each)

	release := make(chan struct{})
	reply := buildReplyFrame([]byte("reply"))
	client := newTestClient(t, func(conn net.Conn) {
		for range roundTrips {
			drainSentPacket(conn)
			writeFramedPacket(conn, reply)
		}
		<-release
	})

	budget := framework.NewReplyBudget()
	for range roundTrips {
		data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
		require.NoError(t, err)
		require.Equal(t, reply, data)
	}

	// The pool must still be intact: a subsequent timeout waits out the
	// full ReadTimeout rather than returning immediately.
	start := time.Now()
	data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
	elapsed := time.Since(start)
	close(release)

	require.NoError(t, err)
	require.Nil(t, data)
	require.GreaterOrEqual(t, elapsed, framework.ReadTimeout)
}

// TestReplyBudget_ExhaustedStopsWaiting verifies that once a ReplyBudget's
// pool has been drained by timed-out reads, a further read on a peer that
// never replies still returns well under the full read timeout.
//
// Without this, a subtest whose packets are all dropped would wait a full
// ReadTimeout per packet, which is exactly what the pool exists to avoid.
func TestReplyBudget_ExhaustedStopsWaiting(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	client := newTestClient(t, func(conn net.Conn) {
		<-release
	})

	budget := framework.NewReplyBudget()

	// Drain the pool with timed-out reads.
	for range 3 {
		data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
		require.NoError(t, err)
		require.Nil(t, data)
	}

	start := time.Now()
	data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
	elapsed := time.Since(start)
	close(release)

	require.NoError(t, err)
	require.Nil(t, data)
	require.Less(t, elapsed, framework.ReadTimeout)
}

// TestReplyBudget_ExhaustedStillCollectsReply verifies that once a
// ReplyBudget's pool has been drained, a further call still reads for the
// peer rather than skipping the read outright, so a reply that arrives
// promptly is still collected.
//
// The mutation this guards against returns before that read runs: a group
// mixing designed drops with genuine replies would then stop collecting
// replies as soon as the drops exhausted the pool, however many good
// packets followed.
func TestReplyBudget_ExhaustedStillCollectsReply(t *testing.T) {
	t.Parallel()

	reply := buildReplyFrame([]byte("reply-after-exhaustion"))
	client := newTestClient(t, func(conn net.Conn) {
		for range 3 {
			drainSentPacket(conn)
		}
		drainSentPacket(conn)
		writeFramedPacket(conn, reply)
	})

	budget := framework.NewReplyBudget()

	// Drain the pool with timed-out reads.
	for range 3 {
		data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
		require.NoError(t, err)
		require.Nil(t, data)
	}

	data, err := client.SendAndReceivePacket(budget, []byte("request"), "")
	require.NoError(t, err)
	require.Equal(t, reply, data)
}

// newTestClient starts a Unix-socket listener, runs handle against the
// single connection it accepts, and returns a SocketClient connected to it.
// The listener and client are closed automatically at test cleanup.
func newTestClient(t *testing.T, handle func(conn net.Conn)) *framework.SocketClient {
	t.Helper()

	socketPath := newTestUnixSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}()

	client, err := framework.NewSocketClient(socketPath)
	require.NoError(t, err)
	require.NoError(t, client.Connect())
	t.Cleanup(func() { client.Close() })

	return client
}

// newTestUnixSocketPath returns a unique path short enough for Unix socket
// limits, independent of the configured temporary root and test name.
//
// Cleanup preserves the prior testing.TempDir behavior and runs after both
// success and ordinary test failure. The directory contains only the socket
// entry, not diagnostic state worth preserving after the test.
func newTestUnixSocketPath(t *testing.T) string {
	t.Helper()

	directory, err := os.MkdirTemp("/tmp", "y2-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(directory))
	})

	return filepath.Join(directory, "s")
}

// buildReplyFrame constructs a minimal Ethernet frame addressed to
// framework.SrcMAC, the destination address ReceivePacket filters on.
func buildReplyFrame(payload []byte) []byte {
	dstMAC, err := net.ParseMAC(framework.SrcMAC)
	if err != nil {
		panic(err)
	}
	srcMAC, err := net.ParseMAC("52:54:00:6b:ff:a5")
	if err != nil {
		panic(err)
	}

	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, dstMAC...)
	frame = append(frame, srcMAC...)
	frame = append(frame, 0x08, 0x00)
	frame = append(frame, payload...)
	return frame
}

// drainSentPacket reads and discards one length-prefixed packet sent by the
// client under test, mirroring the QEMU socket protocol's framing.
func drainSentPacket(conn net.Conn) {
	lengthPrefix := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthPrefix); err != nil {
		return
	}

	buf := make([]byte, binary.BigEndian.Uint32(lengthPrefix))
	_, _ = io.ReadFull(conn, buf)
}

// writeFramedPacket writes packet to conn using the same length-prefixed
// framing the socket client expects to read.
func writeFramedPacket(conn net.Conn, packet []byte) {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(packet)))
	_, _ = conn.Write(header)
	_, _ = conn.Write(packet)
}
