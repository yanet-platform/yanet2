//go:build sdk_rust_fixtures

package cfixture_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// poolBed routes passed fixture packets through a forwarding instance to the
// transmit list.
func poolBed(t *testing.T, mode cfixture.Mode, arg uint64) *bed {
	t.Helper()

	bed := newBed(t)
	options := fixtureOf(mode)
	options.arg = arg
	bed.serveBeforeForward(bed.fixtureInstance(options))
	return bed
}

// setBudget bounds the packets the bed's pool still gives out.
func setBudget(t *testing.T, bed *bed, budget int) {
	t.Helper()

	require.NoError(t, bed.harness.SetAllocationBudget(budget))
}

// Test_Emit_AllocatedPackets_EndByVerdict verifies that allocated packets
// follow their verdict to the transmit or drop list.
func Test_Emit_AllocatedPackets_EndByVerdict(t *testing.T) {
	bed := poolBed(t, cfixture.ModeEmit, 0)
	source := ipv4UDP()

	result := bed.run(0, source)

	require.ElementsMatch(t, [][]byte{source, {0x5a}}, result.Output)
	require.Equal(t, [][]byte{{}}, result.Drop)
}

// Test_Emit_AllocatedPackets_LeaveNothingOutstanding verifies that once the
// round is over, every packet a module made from the pool is back in it.
func Test_Emit_AllocatedPackets_LeaveNothingOutstanding(t *testing.T) {
	bed := poolBed(t, cfixture.ModeEmit, 0)

	bed.run(0, ipv4UDP())

	require.Zero(t, bed.harness.OutstandingMbufs())
}

// Test_Emit_ExhaustedPool_MakesNoPacket verifies that a module finds the pool
// empty when it asks for a packet, and makes none.
func Test_Emit_ExhaustedPool_MakesNoPacket(t *testing.T) {
	bed := poolBed(t, cfixture.ModeEmit, 0)
	setBudget(t, bed, 0)

	result := bed.run(0, ipv4UDP())

	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
}

// Test_Emit_ExhaustedPool_StillPassesTheInput verifies that a module whose
// allocation fails still passes the packet it was given.
func Test_Emit_ExhaustedPool_StillPassesTheInput(t *testing.T) {
	bed := poolBed(t, cfixture.ModeEmit, 0)
	setBudget(t, bed, 0)
	source := ipv4UDP()

	result := bed.run(0, source)

	require.Equal(t, [][]byte{source}, result.Output)
}

// Test_Emit_PoolGivenBack_MakesPacketsAgain verifies that a module allocates
// again once the pool has packets to give.
func Test_Emit_PoolGivenBack_MakesPacketsAgain(t *testing.T) {
	bed := poolBed(t, cfixture.ModeEmit, 0)
	setBudget(t, bed, 0)
	bed.run(0, ipv4UDP())
	setBudget(t, bed, 2)
	source := ipv4UDP()

	result := bed.run(0, source)

	require.ElementsMatch(t, [][]byte{source, {0x5a}}, result.Output)
	require.Equal(t, [][]byte{{}}, result.Drop)
}

// Test_Clone_ExhaustedPool_MakesNoCopy verifies that a module finds the pool
// empty when it asks for a copy of a packet, and gets none.
func Test_Clone_ExhaustedPool_MakesNoCopy(t *testing.T) {
	bed := poolBed(t, cfixture.ModeClone, 0)
	setBudget(t, bed, 0)

	result := bed.run(0, ipv4UDP())

	require.Len(t, result.Output, 1)
}

// Test_Clone_PoolGivenBack_MakesACopyAgain verifies that a module gets a copy
// once the pool has a packet to give.
func Test_Clone_PoolGivenBack_MakesACopyAgain(t *testing.T) {
	bed := poolBed(t, cfixture.ModeClone, 0)
	setBudget(t, bed, 0)
	bed.run(0, ipv4UDP())
	setBudget(t, bed, 1)

	result := bed.run(0, ipv4UDP())

	require.Len(t, result.Output, 2)
}

// Test_Clone_Copy_CarriesTheSourcesFrame verifies that the copy of a packet has
// the frame of the packet, but for the byte the module sets.
func Test_Clone_Copy_CarriesTheSourcesFrame(t *testing.T) {
	bed := poolBed(t, cfixture.ModeClone, 0)
	expected := ipv4UDP()
	expected[0] = 0xff

	result := bed.run(0, ipv4UDP())

	require.Contains(t, result.Output, expected)
}

// Test_Clone_Copy_CarriesTheSourcesParsedOffsets verifies that a packet copy
// preserves parsed network and transport offsets at bytes 14 and 34.
func Test_Clone_Copy_CarriesTheSourcesParsedOffsets(t *testing.T) {
	bed := poolBed(t, cfixture.ModeClone, 0)

	result := bed.run(0, ipv4UDP())

	offsets := make([][2]uint16, 0, len(result.OutputDescriptors))
	for _, descriptor := range result.OutputDescriptors {
		offsets = append(offsets, [2]uint16{descriptor.NetworkOffset, descriptor.TransportOffset})
	}
	require.Equal(t, [][2]uint16{{14, 34}, {14, 34}}, offsets)
}

// Test_Clone_Source_IsLeftUnchanged verifies that copying a packet leaves the
// frame of the packet as it was.
func Test_Clone_Source_IsLeftUnchanged(t *testing.T) {
	bed := poolBed(t, cfixture.ModeClone, 0)
	source := ipv4UDP()

	result := bed.run(0, source)

	require.Contains(t, result.Output, source)
}

func withRecirculationLimit(limit uint16) func(*dataplaneut.Config) {
	return func(config *dataplaneut.Config) {
		config.PacketRecircLimit = limit
	}
}

func droppedDescriptor(t *testing.T, result *dataplaneut.RawResult, frame []byte) dataplaneut.PacketDescriptor {
	t.Helper()

	for idx, dropped := range result.Drop {
		if bytes.Equal(dropped, frame) {
			return result.DropDescriptors[idx]
		}
	}
	require.FailNow(t, "the frame is not on the drop list")
	return dataplaneut.PacketDescriptor{}
}

// cloneWithoutForward runs a packet through a module that copies it, on a bed
// whose lineages may recirculate seven times.
//
// Nothing after the module sends a packet out, so both are on the drop list.
func cloneWithoutForward(t *testing.T) (*dataplaneut.RawResult, []byte, []byte) {
	t.Helper()

	bed := newBed(t, withRecirculationLimit(7))
	bed.serve([]*instance{bed.fixtureInstance(fixtureOf(cfixture.ModeClone))}, nil)
	source := ipv4UDP()
	copied := ipv4UDP()
	copied[0] = 0xff
	return bed.run(0, source), source, copied
}

// Test_Clone_Source_KeepsTheCeilingOfTheRecirculationBudget verifies that the
// source keeps four of seven recirculations.
func Test_Clone_Source_KeepsTheCeilingOfTheRecirculationBudget(t *testing.T) {
	result, source, _ := cloneWithoutForward(t)

	require.Equal(t, uint16(4), droppedDescriptor(t, result, source).RecircRemaining)
}

// Test_Clone_Copy_TakesTheFloorOfTheRecirculationBudget verifies that the copy
// receives three of seven recirculations.
func Test_Clone_Copy_TakesTheFloorOfTheRecirculationBudget(t *testing.T) {
	result, _, copied := cloneWithoutForward(t)

	require.Equal(t, uint16(3), droppedDescriptor(t, result, copied).RecircRemaining)
}

// Test_AllocPrepend_UpToTheHeadroomBeyondTheDescriptor_Succeeds verifies that
// prepend accepts all headroom remaining beyond the shared packet descriptor.
func Test_AllocPrepend_UpToTheHeadroomBeyondTheDescriptor_Succeeds(t *testing.T) {
	bed := poolBed(t, cfixture.ModeAllocPrepend, uint64(cfixture.PrependRoom))

	result := bed.run(0, ipv4UDP())

	require.Contains(t, result.Output, bytes.Repeat([]byte{0xbb}, cfixture.PrependRoom))
}

// Test_AllocPrepend_OneByteBeyondTheHeadroom_IsRefused verifies that excess
// prepend length drops the allocated packet.
func Test_AllocPrepend_OneByteBeyondTheHeadroom_IsRefused(t *testing.T) {
	bed := poolBed(t, cfixture.ModeAllocPrepend, uint64(cfixture.PrependRoom+1))

	result := bed.run(0, ipv4UDP())

	require.Equal(t, [][]byte{{}}, result.Drop)
}
