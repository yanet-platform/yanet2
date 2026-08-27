// Package worker_test also covers the worker's remote staging path in
// lib/dataplane/worker/tx_stage.c, reached through the
// lib/dataplane_ut/tx_stage.h fixture.
//
// A pass confirms packets bound for another worker reach the pipe their flow
// hashes to, cross in bursts, and that anything a pipe cannot take rejoins the
// caller's failure list, which the round discards.
package worker_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
)

// newTxStageFixture constructs a staging fixture and registers its teardown.
func newTxStageFixture(t *testing.T, devices, pipesPerDevice int) *dataplaneut.TxStageFixture {
	t.Helper()

	fixture, err := dataplaneut.NewTxStageFixture(devices, pipesPerDevice)
	require.NoError(t, err)
	t.Cleanup(fixture.Free)

	return fixture
}

// offer takes a packet addressed to a device with a flow hash and offers it to
// the staging path, returning it so a test can identify it later.
func offer(t *testing.T, fixture *dataplaneut.TxStageFixture, device uint16, hash uint32) *dataplaneut.TxStagePacket {
	t.Helper()

	packet, err := fixture.Packet(device, hash)
	require.NoError(t, err)
	fixture.Offer(packet)

	return packet
}

// TestStageHoldsPacketsUntilFlush verifies that a staged packet waits on its
// pipe until the round ends, then crosses in one go.
func TestStageHoldsPacketsUntilFlush(t *testing.T) {
	fixture := newTxStageFixture(t, 2, 1)

	const held = 5
	for range held {
		offer(t, fixture, 1, 0)
	}

	require.Equal(t, held, fixture.Held(1, 0), "packets must wait for the flush")
	require.Equal(t, 0, fixture.Placed(1, 0))
	require.Equal(t, 0, fixture.TxCount())

	fixture.Flush()

	require.Equal(t, 0, fixture.Held(1, 0))
	require.Equal(t, held, fixture.Placed(1, 0), "the whole burst must cross at once")
	require.Equal(t, held, fixture.TxCount())
	require.Empty(t, fixture.Failed())
}

// TestStageRoutesByDestinationDevice verifies that packets are held on the
// connection for the device they are addressed to.
func TestStageRoutesByDestinationDevice(t *testing.T) {
	fixture := newTxStageFixture(t, 3, 1)

	offer(t, fixture, 0, 0)
	offer(t, fixture, 2, 0)
	offer(t, fixture, 2, 0)

	require.Equal(t, 1, fixture.Held(0, 0))
	require.Equal(t, 0, fixture.Held(1, 0))
	require.Equal(t, 2, fixture.Held(2, 0))
}

// TestStageSpreadsFlowsAcrossPipes verifies that a device served by several
// pipes has its packets spread by flow hash, and that one flow always lands on
// one pipe so it cannot be reordered against itself.
func TestStageSpreadsFlowsAcrossPipes(t *testing.T) {
	const pipes = 4
	fixture := newTxStageFixture(t, 1, pipes)

	for hash := range uint32(pipes) {
		offer(t, fixture, 0, hash)
	}
	for pipe := range pipes {
		require.Equal(t, 1, fixture.Held(0, pipe),
			"each flow must land on its own pipe")
	}

	// One flow repeated stays on the pipe it first chose.
	for range 3 {
		offer(t, fixture, 0, 2)
	}
	require.Equal(t, 4, fixture.Held(0, 2))
}

// TestStageFlushesAFullPipeMidRound verifies that a pipe holding a full batch
// hands it over immediately, so the packet in hand has somewhere to land.
func TestStageFlushesAFullPipeMidRound(t *testing.T) {
	fixture := newTxStageFixture(t, 1, 1)

	batch := dataplaneut.TxPipeBatchSize
	for range batch {
		offer(t, fixture, 0, 0)
	}
	require.Equal(t, batch, fixture.Held(0, 0))
	require.Equal(t, 0, fixture.Placed(0, 0))

	offer(t, fixture, 0, 0)

	require.Equal(t, batch, fixture.Placed(0, 0),
		"the full batch must cross to make room")
	require.Equal(t, 1, fixture.Held(0, 0),
		"the packet in hand takes the emptied batch")
	require.Equal(t, batch, fixture.TxCount())
}

// TestStageRequeuesPacketsForAnUnreachableDevice verifies that a device this
// worker has no pipes to hands its packets to the failure list, counted as
// remote drops.
func TestStageRequeuesPacketsForAnUnreachableDevice(t *testing.T) {
	fixture := newTxStageFixture(t, 2, 0)

	first := offer(t, fixture, 0, 0)
	second := offer(t, fixture, 1, 0)

	failed := fixture.Failed()
	require.Len(t, failed, 2)
	require.True(t, failed[0].Same(first), "the failure list keeps offer order")
	require.True(t, failed[1].Same(second))
	require.Equal(t, 2, fixture.TxDrops())
	require.Equal(t, 0, fixture.TxCount())
}

// TestStageRequeuesAnOutOfRangeDeviceUncounted verifies that a packet
// addressed beyond the connection table is sent back without being counted as
// a transmit drop, which is reserved for a device that exists but could not
// take it.
//
// The first id past the end is the one that matters: it is the id a bound one
// too wide would let through, and the only one that would then be read from
// beyond the table.
func TestStageRequeuesAnOutOfRangeDeviceUncounted(t *testing.T) {
	const devices = 2
	fixture := newTxStageFixture(t, devices, 1)

	justPast := offer(t, fixture, devices, 0)
	wellPast := offer(t, fixture, devices+16, 0)

	failed := fixture.Failed()
	require.Len(t, failed, 2)
	require.True(t, failed[0].Same(justPast),
		"the first id past the end must be refused")
	require.True(t, failed[1].Same(wellPast))
	require.Equal(t, 0, fixture.TxDrops(), "an unknown device is not a transmit drop")
}

// TestStageRequeuesWhatAPipeRefuses verifies that packets a full pipe cannot
// take rejoin the failure list in the order they were offered.
func TestStageRequeuesWhatAPipeRefuses(t *testing.T) {
	fixture := newTxStageFixture(t, 1, 1)

	// A pipe holds one ring's worth; keep going until it must refuse.
	const ringSlots = 1 << 10
	const offered = ringSlots + dataplaneut.TxPipeBatchSize

	packets := make([]*dataplaneut.TxStagePacket, 0, offered)
	for range offered {
		packets = append(packets, offer(t, fixture, 0, 0))
	}
	fixture.Flush()

	failed := fixture.Failed()
	require.NotEmpty(t, failed, "a pipe that ran out of room must send packets back")
	require.Equal(t, offered, fixture.TxCount()+fixture.TxDrops(),
		"every offered packet is either placed or counted as a drop")
	require.Equal(t, len(failed), fixture.TxDrops())

	// The refused packets are a suffix of what was offered, in order.
	first := offered - len(failed)
	for idx, packet := range failed {
		require.True(t, packet.Same(packets[first+idx]),
			"refused packet %d is out of order", idx)
	}
}
