package balancer

import (
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestSessionTableManyRandomTCPSyns sends many random TCP SYN packets to a single
// real of a single VS, calling SyncActiveSessionsAndWlcAndResizeTableOnDemand
// every 50 packets and verifying that active sessions count matches the number
// of successfully processed packets.
func TestSessionTableManyRandomTCPSyns(t *testing.T) {
	vsIp := IpAddr("1.1.1.1")
	vsPort := uint16(80)
	realAddr := IpAddr("2.2.2.2")

	sessionTimeout := 64

	// Configure balancer with single VS and single real
	config := &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: []*balancerpb.VirtualService{
			{
				Addr:  vsIp.AsSlice(),
				Port:  uint32(vsPort),
				Proto: balancerpb.TransportProto_TCP,
				AllowedSrcs: []*balancerpb.Subnet{
					{
						Addr: IpAddr("10.0.0.0").AsSlice(),
						Size: 8, // Allow all 10.x.x.x addresses
					},
				},
				Scheduler: balancerpb.VsScheduler_PRR,
				Flags: &balancerpb.VsFlags{
					Gre:    false,
					FixMss: false,
					Ops:    false,
					PureL3: false,
				},
				Reals: []*balancerpb.Real{
					{
						DstAddr: realAddr.AsSlice(),
						Weight:  1,
						SrcAddr: IpAddr("4.4.4.4").AsSlice(),
						SrcMask: IpAddr("4.4.4.4").AsSlice(),
						Enabled: true,
					},
				},
			},
		},
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: uint32(sessionTimeout),
			TcpSyn:    uint32(sessionTimeout),
			TcpFin:    uint32(sessionTimeout),
			Tcp:       uint32(sessionTimeout),
			Udp:       uint32(sessionTimeout),
			Default:   uint32(sessionTimeout),
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      8, // Small initial capacity to trigger resizing
		SessionTableScanPeriod:    durationpb.New(0),
		SessionTableMaxLoadFactor: 0.5,
	}

	// Setup test
	setup, err := SetupTest(&TestConfig{
		moduleConfig: config,
		stateConfig:  stateConfig,
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	seconds := func(sec int) time.Time {
		return time.Unix(int64(sec), 0)
	}

	// Set time to mock
	mock.SetCurrentTime(seconds(0))

	advanceTimePerBatch := time.Duration(3) * time.Second

	// Track total output packets (not dropped)
	totalOutputPackets := 0

	// Send packets in batches of 5
	const batchSize = 5
	const numBatches = 3

	rng := rand.New(rand.NewSource(123))

	for batch := range numBatches {
		t.Logf("Processing batch %d/%d", batch+1, numBatches)

		// Generate `batchSize` random TCP SYN packets
		packets := make([]gopacket.Packet, batchSize)
		for i := range batchSize {
			// Generate random source IP in 10.x.x.x range
			srcIP := netip.AddrFrom4([4]byte{
				10,
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})

			// Generate random source port
			srcPort := uint16(1024 + rng.Intn(64511)) // 1024-65535

			// Create TCP SYN packet
			packetLayers := MakeTCPPacket(
				srcIP,
				srcPort,
				vsIp,
				vsPort,
				&layers.TCP{SYN: true},
			)
			packets[i] = xpacket.LayersToPacket(t, packetLayers...)
		}

		// Send all packets at once
		result, err := mock.HandlePackets(packets...)
		require.NoError(t, err)

		// Track output packets (successfully processed, not dropped)
		batchOutputCount := len(result.Output)
		totalOutputPackets += batchOutputCount

		t.Logf("Batch %d: sent=%d, output=%d, dropped=%d, total_output=%d",
			batch+1, batchSize, batchOutputCount, len(result.Drop), totalOutputPackets)

		// Sync active sessions and resize table on demand
		err = balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(mock.GetCurrentTime())
		require.NoError(t, err)

		// Advance time
		mock.AdvanceTime(advanceTimePerBatch)

		// Get state info
		state := balancer.GetStateInfo()

		// Verify active sessions for VS match output packets
		require.Equal(t, 1, len(state.VsInfo), "should have exactly one VS")
		vsActiveSessions := state.VsInfo[0].ActiveSessions.Value
		assert.Equal(t, uint(totalOutputPackets), vsActiveSessions,
			"VS active sessions should match total output packets after batch %d", batch+1)

		// Verify active sessions for Real match output packets
		require.Equal(t, 1, len(state.RealInfo), "should have exactly one Real")
		realActiveSessions := state.RealInfo[0].ActiveSessions.Value
		assert.Equal(t, uint(totalOutputPackets), realActiveSessions,
			"Real active sessions should match total output packets after batch %d", batch+1)

		t.Logf("Batch %d verified: VS sessions=%d, Real sessions=%d, expected=%d",
			batch+1, vsActiveSessions, realActiveSessions, totalOutputPackets)
	}

	// Final verification
	t.Logf("Test completed: total packets sent=%d, total output=%d",
		batchSize*numBatches, totalOutputPackets)

	state := balancer.GetStateInfo()
	assert.Equal(t, uint(totalOutputPackets), state.VsInfo[0].ActiveSessions.Value,
		"Final VS active sessions should match total output packets")
	assert.Equal(t, uint(totalOutputPackets), state.RealInfo[0].ActiveSessions.Value,
		"Final Real active sessions should match total output packets")
}
