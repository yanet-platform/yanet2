package balancer

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

type VsBuildParams struct {
	Flags            *balancerpb.VsFlags
	minRealsCnt      int
	maxRealsCnt      int
	minAllowedSrcCnt int
	maxAllowedSrcCnt int
}

func selectVS(
	cnt int,
	vs []*balancerpb.VirtualService,
	rng *rand.Rand,
) []*balancerpb.VirtualService {
	if cnt >= len(vs) {
		panic(fmt.Sprintf("selectVS: cnt >= len(vs): %d >= %d", cnt, len(vs)))
	}
	indices := make([]int, len(vs))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := rng.IntN(i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}
	result := make([]*balancerpb.VirtualService, cnt)
	for i := range cnt {
		result[i] = vs[indices[i]]
	}
	return result
}

func buildVS(
	cntNew int,
	cntReuse int,
	params VsBuildParams,
	prevVS []*balancerpb.VirtualService,
	rng *rand.Rand,
) []*balancerpb.VirtualService {
	result := make([]*balancerpb.VirtualService, 0, cntNew+cntReuse)
	if prevVS != nil && cntReuse > 0 {
		result = append(result, selectVS(cntReuse, prevVS, rng)...)
	}
	for range cntNew {
		realsCnt := rng.IntN(params.maxRealsCnt-params.minRealsCnt+1) + params.minRealsCnt
		allowedSrcCnt := rng.IntN(
			params.maxAllowedSrcCnt-params.minAllowedSrcCnt+1,
		) + params.minAllowedSrcCnt
		result = append(result, utils.GenerateVS(rng, realsCnt, allowedSrcCnt, params.Flags))
	}
	if len(result) != cntNew+cntReuse {
		panic(fmt.Sprintf("buildVS: result count mismatch: %d != %d + %d", len(result), cntNew, cntReuse))
	}
	return result
}

func selectDeletedVS(
	cntDeleted int,
	vs []*balancerpb.VirtualService,
	rng *rand.Rand,
) []*balancerpb.VirtualService {
	return selectVS(cntDeleted, vs, rng)
}

func buildInitialConfig(
	rng *rand.Rand,
	cntVS int,
	vsParams VsBuildParams,
) *balancerpb.BalancerConfig {
	stCapacity := uint64(200000)
	stMaxLoadFactor := float32(1.0)
	wlcPower := uint64(10)
	maxWeight := uint32(100)
	wlc := &balancerpb.WlcConfig{
		Power:     &wlcPower,
		MaxWeight: &maxWeight,
	}
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: utils.GenerateIPv4Address(rng).AsSlice(),
			SourceAddressV6: utils.GenerateIPv6Address(rng).AsSlice(),
			DecapAddresses:  [][]byte{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
			},
			Vs: buildVS(cntVS, 0, vsParams, nil, rng),
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &stCapacity,
			SessionTableMaxLoadFactor: &stMaxLoadFactor,
			Wlc:                       wlc,
			RefreshPeriod: &durationpb.Duration{
				Nanos: 25 * 1000 * 1000,
			},
		},
	}
	return config
}

func buildRealUpdates(
	vs []*balancerpb.VirtualService,
	vsCnt int,
	rng *rand.Rand,
) []*balancerpb.RealUpdate {
	selectedVS := selectVS(vsCnt, vs, rng)
	updates := make([]*balancerpb.RealUpdate, 0)
	for _, vs := range selectedVS {
		upd := utils.GenerateRealUpdates(vs, rng)
		updates = append(updates, upd...)
	}
	return updates
}

func makeTestSetup(balancerConfig *balancerpb.BalancerConfig) (*utils.TestSetup, error) {
	testConfig := &utils.TestConfig{
		Mock: &mock.YanetMockConfig{
			AgentsMemory: 2048 * datasize.MB,
			DpMemory:     128 * datasize.MB,
			Workers:      1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					ID:   0,
					Name: "device0",
				},
			},
		},
		Balancer:    balancerConfig,
		AgentMemory: 1024 * datasize.MB,
	}
	return utils.Make(testConfig)
}

func sendAndValidate(t *testing.T, ts *utils.TestSetup, rng *rand.Rand) {
	config := ts.Balancer.Config()
	for _, vs := range config.PacketHandler.Vs {
		vsID := utils.VsIDFromPb(vs.Id)
		srcIP, srcPort := utils.GenerateAllowedSrcForVS(rng, vs)

		// No ops VS
		dstIP, ok := netip.AddrFromSlice(vs.Id.Addr)
		assert.True(t, ok, "failed to parse destination IP for vs %s", vs.Id)
		dstPort := uint16(vs.Id.Port)

		var tcp *layers.TCP
		if vs.Id.Proto == balancerpb.TransportProto_TCP {
			tcp = &layers.TCP{
				SrcPort: layers.TCPPort(srcPort),
				DstPort: layers.TCPPort(dstPort),
				SYN:     true,
				ACK:     false,
			}
		}

		_, err := utils.SendAndValidate(ts, srcIP, srcPort, dstIP, dstPort, tcp)
		assert.NoError(t, err, "failed to send and validate packets for vs %s", &vsID)

		if err != nil {
			state, err := ts.Balancer.GetState(utils.PacketHandlerRef(), nil, true, ts.Mock.CurrentTime())
			assert.NoError(t, err)
			t.Logf("balancer common stats: %v", state[0].CommonStats)
			t.Logf("balancer l4 stats: %v", state[0].L4Stats)
			for _, vsState := range state[0].VirtualServices {
				vsStateID := utils.VsIDFromPb(vsState.Id)
				if vsID.Compare(&vsStateID) == 0 {
					t.Logf("vs stats: %v", vsState.Stats)
				}
			}
		}
	}
}

func updateReals(t *testing.T, ts *utils.TestSetup, rng *rand.Rand) {
	config := ts.Balancer.Config()
	vs := config.PacketHandler.Vs
	updates := buildRealUpdates(vs, len(vs)/2, rng)
	updated, err := ts.Balancer.UpdateReals(updates, false)
	assert.NoError(t, err)
	assert.Equal(t, len(updates), updated)
	ensureAtLeastOneRealIsEnabled(t, ts)
}

func ensureAtLeastOneRealIsEnabled(t *testing.T, ts *utils.TestSetup) {
	state, err := ts.Balancer.GetState(nil, nil, false, ts.Mock.CurrentTime())
	assert.NoError(t, err)
	assert.Len(t, state, 1)
	for _, vs := range state[0].VirtualServices {
		enabled := false
		for _, real := range vs.Reals {
			if real.Enabled {
				enabled = true
			}
		}
		assert.True(t, enabled, "no enabled reals for vs %s", vs.Id)
	}
}

func vsCnt(b *balancer.Balancer) int {
	return len(b.Config().PacketHandler.Vs)
}

func TestUpdateStress(t *testing.T) {
	rng := rand.New(rand.NewPCG(uint64(100), uint64(123)))
	cntVS := 20
	vsParams := VsBuildParams{
		Flags: &balancerpb.VsFlags{
			FixMss: true,
		},
		minRealsCnt:      5,
		maxRealsCnt:      15,
		minAllowedSrcCnt: 1,
		maxAllowedSrcCnt: 3,
	}
	initialConfig := buildInitialConfig(rng, cntVS, vsParams)
	ts, err := makeTestSetup(initialConfig)
	if err != nil {
		t.Fatalf("failed to make test setup: %v", err)
	}
	defer ts.Free()

	utils.EnableAllReals(t, ts)

	balancer := ts.Balancer

	updateRealsIters := 20

	updateRealsFunc := func() {
		t.Log("updateReals")
		sendAndValidate(t, ts, rng)
		for range updateRealsIters {
			updateReals(t, ts, rng)
			sendAndValidate(t, ts, rng)
		}
	}

	updateRealsFunc()

	updateVsIters := 15

	t.Log("initial vs count:", vsCnt(balancer))

	for updateVsIter := range updateVsIters {
		newCnt := rng.IntN(5)
		prevCnt := vsCnt(balancer)
		reuseCnt := min(rng.IntN(5), prevCnt)
		t.Logf("iter=%d, updateVS: newCnt=%d", updateVsIter, newCnt)

		_, err := balancer.UpdateVS(
			buildVS(newCnt, reuseCnt, vsParams, balancer.Config().PacketHandler.Vs, rng),
		)
		assert.NoError(t, err, "failed to update VS on iter %d", updateVsIter)
		utils.EnableAllReals(t, ts)
		assert.Equal(t, newCnt+prevCnt, vsCnt(balancer), "vs count mismatch after update via UpdateVS")

		updateRealsFunc()

		prevCnt = vsCnt(balancer)
		delCnt := max(rng.IntN(prevCnt/4), 1)
		t.Logf("iter=%d, deleteVS: delCnt=%d, prevCnt=%d", updateVsIter, delCnt, prevCnt)

		vsToDelete := selectDeletedVS(delCnt, ts.Balancer.Config().PacketHandler.Vs, rng)
		t.Logf("iter=%d, deleteVS: selected %d VS for deletion:", updateVsIter, len(vsToDelete))

		_, err = balancer.DeleteVS(vsToDelete)
		assert.NoError(t, err, "failed to delete VS on iter %d", updateVsIter)

		newCnt = vsCnt(balancer)
		t.Logf("iter=%d, after deleteVS: expected=%d, actual=%d", updateVsIter, prevCnt-delCnt, newCnt)
		assert.Equal(t, prevCnt-delCnt, newCnt, "vs count mismatch after delete via DeleteVS")

		updateRealsFunc()

		newCnt = max(vsCnt(balancer)/2+2-rng.IntN(5), 10)
		reuseCnt = max(vsCnt(balancer)/2+2-rng.IntN(5), 0)

		t.Logf("iter=%d, updateVS via update config: newCnt=%d, reuseCnt=%d", updateVsIter, newCnt, reuseCnt)

		config := ts.Balancer.Config()
		config.PacketHandler.Vs = buildVS(newCnt, reuseCnt, vsParams, config.PacketHandler.Vs, rng)
		_, err = balancer.Update(config, nil)
		assert.NoError(t, err, "failed to update config on iter %d", updateVsIter)
		assert.Equal(t, newCnt+reuseCnt, vsCnt(balancer), "vs count mismatch after update via Update")

		utils.EnableAllReals(t, ts)

		updateRealsFunc()

		t.Logf("iter=%d, in the end: vs_count=%d", updateVsIter, vsCnt(balancer))
	}
}
