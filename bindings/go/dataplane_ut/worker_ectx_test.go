package dataplaneut_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// workerEctxWorkerCount is the worker population every test below uses:
// two workers are the minimum that separates the worker that ran a round
// from a worker that never ran.
const workerEctxWorkerCount = 2

// newWorkerEctxHarness builds a two-worker harness with an otherwise empty
// topology. The generation-assignment protocol under test needs only
// generation switches, not packet-routing modules or device wiring.
func newWorkerEctxHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: workerEctxWorkerCount,
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	return harness
}

// Test_WorkerEctx_Bootstrap_FieldIsNullAndNothingAcked verifies that a
// fresh harness with no installs reports a zero published generation, no
// per-worker context in any worker's field, and no acknowledged generation
// on any worker.
func Test_WorkerEctx_Bootstrap_FieldIsNullAndNothingAcked(t *testing.T) {
	harness := newWorkerEctxHarness(t)

	require.Zero(t, harness.PublishedGen(), "no install ran, so no generation is published")
	for workerIdx := range workerEctxWorkerCount {
		require.Zero(
			t,
			harness.PublishedEctx(workerIdx),
			"worker %d must have no published context before any install",
			workerIdx,
		)
		require.Zero(
			t,
			harness.WorkerEctx(workerIdx),
			"worker %d must hold no context before any install",
			workerIdx,
		)
		require.Zero(
			t,
			harness.WorkerGen(workerIdx),
			"worker %d must acknowledge nothing before any install",
			workerIdx,
		)
	}
}

// Test_WorkerEctx_Install_AssignsPublishedEctxToEveryWorker verifies that
// one empty-pipeline install assigns every worker, synchronously on return,
// the per-worker execution context of the newly published generation.
//
// Every acknowledgement must stay zero: an install switches contexts but
// only a round acknowledges, and no round has run.
func Test_WorkerEctx_Install_AssignsPublishedEctxToEveryWorker(t *testing.T) {
	harness := newWorkerEctxHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("assigned"))

	require.NotZero(t, harness.PublishedGen(), "an install must publish a new generation")
	for workerIdx := range workerEctxWorkerCount {
		published := harness.PublishedEctx(workerIdx)
		require.NotZero(
			t,
			published,
			"worker %d must have a published context after an install",
			workerIdx,
		)
		require.Equal(
			t,
			published,
			harness.WorkerEctx(workerIdx),
			"worker %d's field must already hold the published context on return from the install",
			workerIdx,
		)
		require.Zero(
			t,
			harness.WorkerGen(workerIdx),
			"worker %d must not acknowledge a generation without a round",
			workerIdx,
		)
	}
}

// Test_WorkerEctx_Reinstall_ReassignsNewEctxToEveryWorker verifies that a
// second install publishes a fresh per-worker context and every worker's
// field follows it, pinning re-assignment on every generation switch
// rather than a one-time bootstrap stamp.
func Test_WorkerEctx_Reinstall_ReassignsNewEctxToEveryWorker(t *testing.T) {
	harness := newWorkerEctxHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("first"))
	firstGen := harness.PublishedGen()
	firstEctx := [workerEctxWorkerCount]uintptr{}
	for workerIdx := range workerEctxWorkerCount {
		firstEctx[workerIdx] = harness.PublishedEctx(workerIdx)
	}

	require.NoError(t, harness.InstallEmptyPipeline("second"))
	require.Greater(
		t,
		harness.PublishedGen(),
		firstGen,
		"a reinstall must advance the published generation",
	)

	for workerIdx := range workerEctxWorkerCount {
		second := harness.PublishedEctx(workerIdx)
		require.NotZero(
			t,
			second,
			"worker %d must have a published context after the second install",
			workerIdx,
		)
		require.NotEqual(
			t,
			firstEctx[workerIdx],
			second,
			"worker %d's second generation must publish a fresh context",
			workerIdx,
		)
		require.Equal(
			t,
			second,
			harness.WorkerEctx(workerIdx),
			"worker %d's field must follow the second generation",
			workerIdx,
		)
	}
}

// Test_WorkerEctx_Round_AcksAssignedGenOnThatWorkerOnly verifies that one
// round run on a single worker acknowledges exactly the published
// generation on that worker, while a worker that never ran keeps
// acknowledging zero.
func Test_WorkerEctx_Round_AcksAssignedGenOnThatWorkerOnly(t *testing.T) {
	harness := newWorkerEctxHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("round"))

	ethernet := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ipv4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	packet := xpacket.LayersToPacket(t, &ethernet, &ipv4, &icmp)

	// The topology wires no device, so the round drops the packet; the
	// dropped packet proves the round processed input on worker 0.
	result, err := harness.HandlePacketsOnWorker(0, packet)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)

	require.Equal(
		t,
		harness.PublishedGen(),
		harness.WorkerGen(0),
		"worker 0 must acknowledge the published generation after its round",
	)
	require.Zero(
		t,
		harness.WorkerGen(1),
		"worker 1 never ran a round and must keep acknowledging zero",
	)
}

// Test_WorkerEctx_ConcurrentPublish_WaitsForInFlightRound verifies that a
// publish running concurrently with rounds on another thread completes
// only across them, so every worker ends up holding the newly published
// context and no round can still touch the retired one.
func Test_WorkerEctx_ConcurrentPublish_WaitsForInFlightRound(t *testing.T) {
	harness := newWorkerEctxHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("before"))

	publishDone := make(chan error, 1)
	go func() {
		publishDone <- harness.InstallEmptyPipeline("during")
	}()

	publishing := true
	for publishing {
		select {
		case err := <-publishDone:
			require.NoError(t, err)
			publishing = false
		default:
			_, err := harness.HandlePacketsOnWorker(0)
			require.NoError(t, err)
		}
	}

	for workerIdx := range workerEctxWorkerCount {
		require.Equal(
			t,
			harness.PublishedEctx(workerIdx),
			harness.WorkerEctx(workerIdx),
			"worker %d's field must hold the concurrently published context",
			workerIdx,
		)
	}
}
