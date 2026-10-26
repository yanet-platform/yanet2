package fwstate_test

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

// Test_FWStateCursor_KeyDataCorrectness verifies that handler-created IPv4
// entries retain every protocol, port and address byte through the cursor.
func Test_FWStateCursor_KeyDataCorrectness(t *testing.T) {
	config := newFWStateFixture(t, 0)
	frame := createSyncFrame(layers.IPProtocolTCP, 4, 12345, 8080, nil, nil, WithFrameFlags(0x02))
	source := netip.MustParseAddr("172.16.5.10").As4()
	destination := netip.MustParseAddr("10.20.30.40").As4()
	copy(frame[4:8], source[:])
	copy(frame[0:4], destination[:])
	worker := config.Worker(0)
	nextRound(worker)
	stashEvents(config, worker, localEvent{frame})
	runConfig(config, worker, false)
	entries, _, _, err := config.Fixture.Maps[0].ReadForward(0, 0, true, uint64(config.Fixture.Harness.CurrentTime().UnixNano()), 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	key := entries[0].Key
	require.Equal(t, uint32(6), key.Proto)
	require.Equal(t, uint32(12345), key.SrcPort)
	require.Equal(t, uint32(8080), key.DstPort)
	require.Equal(t, binary.NativeEndian.Uint32(source[:]), binary.NativeEndian.Uint32(key.SrcAddr))
	require.Equal(t, binary.NativeEndian.Uint32(destination[:]), binary.NativeEndian.Uint32(key.DstAddr))

	state := GetStateValue(config, layers.IPProtocolTCP, 12345, 8080, "172.16.5.10", "10.20.30.40")
	require.True(t, state.Found)
	require.False(t, state.External)
	require.Equal(t, uint32(0x02), state.Flags)
	require.Equal(t, uint64(1), state.PacketsForward)
	require.Equal(t, uint64(config.Fixture.Harness.CurrentTime().UnixNano())+config.Sync.TcpSyn, state.Deadline)
}

// Test_FWStateCursor_ValueDataCorrectness verifies that real handler updates
// expose their exact timestamp and forward/backward packet totals.
func Test_FWStateCursor_ValueDataCorrectness(t *testing.T) {
	config := newFWStateFixture(t, 0)
	frame := createSyncFrame(layers.IPProtocolTCP, 4, 9000, 80, nil, nil, WithFrameFlags(0x88))
	source := netip.MustParseAddr("10.0.0.1").As4()
	destination := netip.MustParseAddr("192.168.0.1").As4()
	copy(frame[4:8], source[:])
	copy(frame[0:4], destination[:])
	worker := config.Worker(0)
	nextRound(worker)
	stashEvents(config, worker, localEvent{frame})
	runConfig(config, worker, false)
	now := uint64(config.Fixture.Harness.CurrentTime().UnixNano())
	entries, _, _, err := config.Fixture.Maps[0].ReadForward(0, 0, true, now, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, now, entries[0].Value.UpdatedAt)
	require.Equal(t, uint64(1), entries[0].Value.PacketsForward)
	require.Equal(t, uint64(0), entries[0].Value.PacketsBackward)

	config.Fixture.Harness.AdvanceTime(999 * time.Nanosecond)
	nextRound(worker)
	stashEvents(config, worker, localEvent{frame})
	runConfig(config, worker, false)
	entries, _, _, err = config.Fixture.Maps[0].ReadForward(0, 0, true, now+1000, 1)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, now, entries[0].Value.CreatedAt)
	require.Equal(t, now+1000, entries[0].Value.UpdatedAt)
	require.Equal(t, uint64(2), entries[0].Value.PacketsForward)
	require.Zero(t, entries[0].Value.PacketsBackward)
}

// Test_FWStateCursor_InvalidLayer verifies that a missing layer is rejected.
func Test_FWStateCursor_InvalidLayer(t *testing.T) {
	config := newFWStateFixture(t, 0)
	_, _, _, err := config.Fixture.Maps[0].ReadForward(99, 0, true, uint64(config.Fixture.Harness.CurrentTime().UnixNano()), 10)
	require.Error(t, err)
}
