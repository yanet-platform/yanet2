package balancer_test

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type activeSessionsExpected struct {
	total        uint64
	lastPacketTS time.Time
	vs           map[string]activeSessionsVsExpected
}

type activeSessionsVsExpected struct {
	activeSessions uint64
	lastPacketTS   time.Time
	reals          map[string]activeSessionsRealExpected
}

type activeSessionsRealExpected struct {
	activeSessions uint64
	lastPacketTS   time.Time
}

type activeSessionsPacketPlan struct {
	vsIP   netip.Addr
	vsPort uint16
	count  int
	start  int
}

func TestActiveSessions(t *testing.T) {
	vs1IP := netip.MustParseAddr("1.1.1.1")
	vs2IP := netip.MustParseAddr("1.1.1.2")
	vs1Port := uint16(80)
	vs2Port := uint16(8080)

	vs1Real1 := netip.MustParseAddr("2.2.2.1")
	vs1Real2 := netip.MustParseAddr("2.2.2.2")
	vs1Real3 := netip.MustParseAddr("2.2.2.3")
	vs2Real1 := netip.MustParseAddr("3.3.3.1")
	vs2Real2 := netip.MustParseAddr("3.3.3.2")
	vs2Real3 := netip.MustParseAddr("3.3.3.3")

	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				makeActiveSessionsVS(vs1IP, vs1Port, []netip.Addr{vs1Real1, vs1Real2, vs1Real3}),
				makeActiveSessionsVS(vs2IP, vs2Port, []netip.Addr{vs2Real1, vs2Real2, vs2Real3}),
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 30,
				TcpSyn:    30,
				TcpFin:    30,
				Tcp:       30,
				Udp:       30,
				Default:   30,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1024); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.5); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	utils.EnableAllReals(t, ts)

	initialPlans := []activeSessionsPacketPlan{
		{vsIP: vs1IP, vsPort: vs1Port, count: 18, start: 0},
		{vsIP: vs2IP, vsPort: vs2Port, count: 18, start: 1000},
	}

	t.Run("Initial_Send_And_Check", func(t *testing.T) {
		sendActiveSessionsPackets(t, ts, initialPlans)
		currentTime := ts.Mock.CurrentTime()
		expected := buildActiveSessionsExpected(t, ts, currentTime)
		checkActiveSessionsState(t, ts, currentTime, expected)
	})

	t.Run("Advance_29s_And_Check", func(t *testing.T) {
		currentTime := ts.Mock.AdvanceTime(29 * time.Second)
		expected := buildActiveSessionsExpected(t, ts, currentTime)
		checkActiveSessionsState(t, ts, currentTime, expected)
	})

	t.Run("Advance_100s_Send_Again_And_Check", func(t *testing.T) {
		ts.Mock.AdvanceTime(100 * time.Second)
		sendActiveSessionsPackets(t, ts, initialPlans)
		currentTime := ts.Mock.CurrentTime()
		expected := buildActiveSessionsExpected(t, ts, currentTime)
		checkActiveSessionsState(t, ts, currentTime, expected)
	})
}

func makeActiveSessionsVS(
	vsIP netip.Addr,
	vsPort uint16,
	reals []netip.Addr,
) *balancerpb.VirtualService {
	vs := &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: vsIP.AsSlice()},
			Port:  uint32(vsPort),
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
		AllowedSrcs: []*balancerpb.AllowedSources{
			{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.0").AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("255.0.0.0").AsSlice(),
					},
				}},
			},
		},
		Flags: &balancerpb.VsFlags{
			Gre:    false,
			FixMss: false,
			Ops:    false,
			PureL3: false,
			Wlc:    false,
		},
		Peers: []*balancerpb.Addr{},
	}

	for _, realIP := range reals {
		vs.Reals = append(vs.Reals, &balancerpb.Real{
			Id: &balancerpb.RelativeRealIdentifier{
				Ip:   &balancerpb.Addr{Bytes: realIP.AsSlice()},
				Port: 0,
			},
			Weight: 1,
			SrcAddr: &balancerpb.Addr{
				Bytes: realIP.AsSlice(),
			},
			SrcMask: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
			},
		})
	}

	return vs
}

func sendActiveSessionsPackets(
	t *testing.T,
	ts *utils.TestSetup,
	plans []activeSessionsPacketPlan,
) {
	t.Helper()

	packets := make([]gopacket.Packet, 0)
	for _, plan := range plans {
		for i := range plan.count {
			clientIP := activeSessionsClientIP(plan.start + i)
			clientPort := uint16(10000 + plan.start + i)
			packetLayers := utils.MakeTCPPacket(
				clientIP,
				clientPort,
				plan.vsIP,
				plan.vsPort,
				&layers.TCP{SYN: true},
			)
			packets = append(packets, xpacket.LayersToPacket(t, packetLayers...))
		}
	}

	result, err := ts.Mock.HandlePackets(packets...)
	require.NoError(t, err)
	require.Equal(t, len(packets), len(result.Output), "all packets should be forwarded")
	require.Empty(t, result.Drop, "no packets should be dropped")

	for i, outPacket := range result.Output {
		utils.ValidatePacket(t, ts.Balancer.Config(), packets[i], outPacket)
	}
}

func activeSessionsClientIP(index int) netip.Addr {
	return netip.MustParseAddr(
		fmt.Sprintf("10.%d.%d.%d", index/(256*256)%256, (index/256)%256, index%256),
	)
}

func buildActiveSessionsExpected(
	t *testing.T,
	ts *utils.TestSetup,
	currentTime time.Time,
) activeSessionsExpected {
	t.Helper()

	sessions, err := ts.Balancer.Sessions(currentTime)
	require.NoError(t, err)

	expected := activeSessionsExpected{
		total:        uint64(len(sessions)),
		lastPacketTS: time.Time{},
		vs:           map[string]activeSessionsVsExpected{},
	}

	for _, session := range sessions {
		vsAddr, ok := netip.AddrFromSlice(session.RealId.Vs.Addr.Bytes)
		require.True(t, ok, "failed to decode VS addr from session.RealId.Vs")
		realAddr, ok := netip.AddrFromSlice(session.RealId.Real.Ip.Bytes)
		require.True(t, ok, "failed to decode real addr from session.RealId.Real.Ip")

		vsKey := activeSessionsVSKey(vsAddr, uint16(session.RealId.Vs.Port))
		realKey := realAddr.String()
		lastPacketTS := session.LastPacketTimestamp.AsTime()

		if lastPacketTS.After(expected.lastPacketTS) {
			expected.lastPacketTS = lastPacketTS
		}

		vsExpected := expected.vs[vsKey]
		if vsExpected.reals == nil {
			vsExpected.reals = map[string]activeSessionsRealExpected{}
		}
		vsExpected.activeSessions++
		if lastPacketTS.After(vsExpected.lastPacketTS) {
			vsExpected.lastPacketTS = lastPacketTS
		}

		realExpected := vsExpected.reals[realKey]
		realExpected.activeSessions++
		if lastPacketTS.After(realExpected.lastPacketTS) {
			realExpected.lastPacketTS = lastPacketTS
		}
		vsExpected.reals[realKey] = realExpected
		expected.vs[vsKey] = vsExpected
	}

	return expected
}

func checkActiveSessionsState(
	t *testing.T,
	ts *utils.TestSetup,
	currentTime time.Time,
	expected activeSessionsExpected,
) {
	t.Helper()

	activeInfo := ts.Balancer.ActiveSessions()
	require.NotNil(t, activeInfo)

	sessions, err := ts.Balancer.Sessions(currentTime)
	require.NoError(t, err)
	require.Equal(t, int(expected.total), len(sessions), "sessions count should match expected")

	assert.Equal(t, expected.total, activeInfo.ActiveSessions, "total active sessions mismatch")
	assert.Equal(
		t,
		timestamppb.New(expected.lastPacketTS),
		activeInfo.LastPacketTimestamp,
		"balancer last packet timestamp mismatch",
	)

	actualTotalFromVS := uint64(0)
	actualTotalFromReals := uint64(0)
	require.Len(t, activeInfo.Vs, 2, "should have exactly two VS entries")

	for _, vsInfo := range activeInfo.Vs {
		vsAddr, ok := netip.AddrFromSlice(vsInfo.Id.Addr.Bytes)
		require.True(t, ok, "failed to decode VS addr from ActiveSessions info")
		vsKey := activeSessionsVSKey(vsAddr, uint16(vsInfo.Id.Port))
		vsExpected, ok := expected.vs[vsKey]
		require.True(t, ok, "unexpected VS in ActiveSessions: %s", vsKey)

		assert.Equal(
			t,
			vsExpected.activeSessions,
			vsInfo.ActiveSessions,
			"VS active sessions mismatch for %s",
			vsKey,
		)
		assert.Equal(
			t,
			timestamppb.New(vsExpected.lastPacketTS),
			vsInfo.LastPacketTimestamp,
			"VS last packet timestamp mismatch for %s",
			vsKey,
		)
		actualTotalFromVS += vsInfo.ActiveSessions

		actualVSRealTotal := uint64(0)
		require.Len(t, vsInfo.Reals, 3, "VS %s should have exactly three reals", vsKey)
		for _, realInfo := range vsInfo.Reals {
			realAddr, ok := netip.AddrFromSlice(realInfo.Id.Real.Ip.Bytes)
			require.True(t, ok, "failed to decode real addr from ActiveSessions info")
			realKey := realAddr.String()
			realExpected, ok := vsExpected.reals[realKey]
			require.True(t, ok, "unexpected real %s in VS %s", realKey, vsKey)

			assert.NotZero(
				t,
				realInfo.ActiveSessions,
				"real %s in VS %s should have active sessions",
				realKey,
				vsKey,
			)
			assert.Equal(
				t,
				realExpected.activeSessions,
				realInfo.ActiveSessions,
				"real active sessions mismatch for %s in %s",
				realKey,
				vsKey,
			)
			assert.Equal(
				t,
				timestamppb.New(realExpected.lastPacketTS),
				realInfo.LastPacketTimestamp,
				"real last packet timestamp mismatch for %s in %s",
				realKey,
				vsKey,
			)
			actualVSRealTotal += realInfo.ActiveSessions
			actualTotalFromReals += realInfo.ActiveSessions
		}

		assert.Equal(
			t,
			vsInfo.ActiveSessions,
			actualVSRealTotal,
			"sum of real sessions should match VS total for %s",
			vsKey,
		)
	}

	assert.Equal(
		t,
		activeInfo.ActiveSessions,
		actualTotalFromVS,
		"sum of VS sessions should match balancer total",
	)
	assert.Equal(
		t,
		activeInfo.ActiveSessions,
		actualTotalFromReals,
		"sum of real sessions should match balancer total",
	)
}

func activeSessionsVSKey(vsIP netip.Addr, vsPort uint16) string {
	return fmt.Sprintf("%s:%d", vsIP, vsPort)
}
