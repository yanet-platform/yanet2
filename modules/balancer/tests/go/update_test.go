package balancer

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

type vsBuildParams struct {
	Flags            *balancerpb.VsFlags
	minRealsCnt      int
	maxRealsCnt      int
	minAllowedSrcCnt int
	maxAllowedSrcCnt int
}

func buildVS(
	cntNew int,
	cntReuse int,
	params vsBuildParams,
	prevVS []*balancerpb.VirtualService,
	rng *rand.Rand,
) ([]*balancerpb.VirtualService, []*balancerpb.VirtualService) {
	result := make([]*balancerpb.VirtualService, 0, cntNew+cntReuse)
	var reused []*balancerpb.VirtualService
	if prevVS != nil && cntReuse > 0 {
		reused = utils.SelectVS(cntReuse, prevVS, rng)
		for _, vs := range reused {
			result = append(result, utils.VSUpdateSomeReals(vs, rng))
		}
	}
	for range cntNew {
		realsCnt := rng.IntN(params.maxRealsCnt-params.minRealsCnt+1) + params.minRealsCnt
		allowedSrcCnt := rng.IntN(
			params.maxAllowedSrcCnt-params.minAllowedSrcCnt+1,
		) + params.minAllowedSrcCnt
		result = append(result, utils.GenerateVS(rng, realsCnt, allowedSrcCnt, params.Flags))
	}
	if len(result) != cntNew+cntReuse {
		panic(
			fmt.Sprintf(
				"buildVS: result count mismatch: %d != %d + %d",
				len(result),
				cntNew,
				cntReuse,
			),
		)
	}
	return result, reused
}

func buildInitialConfig(
	rng *rand.Rand,
	cntVS int,
	vsParams vsBuildParams,
) *balancerpb.BalancerConfig {
	stCapacity := uint64(50000)
	stMaxLoadFactor := float32(1.0)
	wlcPower := uint64(10)
	maxWeight := uint32(100)
	wlc := &balancerpb.WlcConfig{
		Power:     &wlcPower,
		MaxWeight: &maxWeight,
	}
	vs, _ := buildVS(cntVS, 0, vsParams, nil, rng)
	return &balancerpb.BalancerConfig{
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
			Vs: vs,
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
}

func buildTestSetup(balancerConfig *balancerpb.BalancerConfig) (*utils.TestSetup, error) {
	testConfig := &utils.TestConfig{
		Mock: &mock.YanetMockConfig{
			AgentsMemory: 256 * datasize.MB,
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
		AgentMemory: 128 * datasize.MB,
	}
	return utils.Make(testConfig)
}

func updateReals(t *testing.T, ts *utils.TestSetup, rng *rand.Rand) {
	config := ts.Balancer.Config()
	vs := config.PacketHandler.Vs
	selected := utils.SelectVS(len(vs)/2, vs, rng)
	updates := utils.GenerateRealUpdates(selected, rng)
	updated, err := ts.Balancer.UpdateReals(updates, false)
	assert.NoError(t, err)
	assert.Equal(t, len(updates), updated)
}

// runUpdateRealsRound sends one packet per VS then repeats real-update + send iters times.
// After each send it verifies that per-VS incoming and outgoing packet counters increased.
func runUpdateRealsRound(t *testing.T, ts *utils.TestSetup, rng *rand.Rand, iters int) {
	utils.SendAndValidateMany(t, ts, rng)
	for range iters {
		updateReals(t, ts, rng)
		utils.SendAndValidateMany(t, ts, rng)
	}
}

func stepUpdateVS(
	t *testing.T,
	ts *utils.TestSetup,
	vsParams vsBuildParams,
	rng *rand.Rand,
	iter int,
	realsIters int,
) {
	b := ts.Balancer
	newCnt := rng.IntN(5)
	prevCount := utils.VsCount(b)
	reuseCnt := min(rng.IntN(5), prevCount)
	t.Logf("iter=%d, UpdateVS: newCnt=%d, reuseCnt=%d", iter, newCnt, reuseCnt)

	newVSList, reusedVS := buildVS(newCnt, reuseCnt, vsParams, b.Config().PacketHandler.Vs, rng)
	snapshots := utils.CaptureVsSnapshots(t, ts, reusedVS)
	_, err := b.UpdateVS(newVSList)
	assert.NoError(t, err, "iter=%d: failed to UpdateVS", iter)
	assert.Equal(
		t,
		newCnt+prevCount,
		utils.VsCount(b),
		"iter=%d: vs count mismatch after UpdateVS",
		iter,
	)
	utils.EnableAllReals(t, ts)
	utils.VerifyInheritedStats(t, ts, snapshots)
	runUpdateRealsRound(t, ts, rng, realsIters)
}

func stepDeleteVS(
	t *testing.T,
	ts *utils.TestSetup,
	rng *rand.Rand,
	iter int,
	realsIters int,
) {
	b := ts.Balancer
	prevCount := utils.VsCount(b)
	delCnt := max(rng.IntN(prevCount/4), 1)
	t.Logf("iter=%d, DeleteVS: delCnt=%d, prevCount=%d", iter, delCnt, prevCount)

	allVS := b.Config().PacketHandler.Vs
	snapshots := utils.CaptureVsSnapshots(t, ts, allVS)
	_, err := b.DeleteVS(utils.SelectVS(delCnt, allVS, rng))
	assert.NoError(t, err, "iter=%d: failed to DeleteVS", iter)
	assert.Equal(
		t,
		prevCount-delCnt,
		utils.VsCount(b),
		"iter=%d: vs count mismatch after DeleteVS",
		iter,
	)
	utils.VerifyInheritedStats(t, ts, snapshots)
	runUpdateRealsRound(t, ts, rng, realsIters)
}

func stepUpdateConfig(
	t *testing.T,
	ts *utils.TestSetup,
	vsParams vsBuildParams,
	rng *rand.Rand,
	iter int,
	realsIters int,
) {
	b := ts.Balancer
	newCnt := max(utils.VsCount(b)/2+2-rng.IntN(5), 10)
	reuseCnt := max(utils.VsCount(b)/2+2-rng.IntN(5), 0)
	t.Logf("iter=%d, Update: newCnt=%d, reuseCnt=%d", iter, newCnt, reuseCnt)

	config := b.Config()
	newVSList, reusedVS := buildVS(newCnt, reuseCnt, vsParams, config.PacketHandler.Vs, rng)
	config.PacketHandler.Vs = newVSList
	snapshots := utils.CaptureVsSnapshots(t, ts, reusedVS)
	_, err := b.Update(config, nil)
	assert.NoError(t, err, "iter=%d: failed to Update config", iter)
	assert.Equal(
		t,
		newCnt+reuseCnt,
		utils.VsCount(b),
		"iter=%d: vs count mismatch after Update",
		iter,
	)
	utils.EnableAllReals(t, ts)
	utils.VerifyInheritedStats(t, ts, snapshots)
	runUpdateRealsRound(t, ts, rng, realsIters)
}

func TestUpdateStress(t *testing.T) {
	rng := rand.New(rand.NewPCG(uint64(100), uint64(123)))
	vsParams := vsBuildParams{
		Flags:            &balancerpb.VsFlags{FixMss: true},
		minRealsCnt:      5,
		maxRealsCnt:      15,
		minAllowedSrcCnt: 1,
		maxAllowedSrcCnt: 3,
	}

	ts, err := buildTestSetup(buildInitialConfig(rng, 20, vsParams))
	if err != nil {
		t.Fatalf("failed to make test setup: %v", err)
	}
	defer ts.Free()

	utils.EnableAllReals(t, ts)
	runUpdateRealsRound(t, ts, rng, 20)
	t.Log("initial vs count:", utils.VsCount(ts.Balancer))

	for iter := range 15 {
		stepUpdateVS(t, ts, vsParams, rng, iter, 20)
		stepDeleteVS(t, ts, rng, iter, 20)
		stepUpdateConfig(t, ts, vsParams, rng, iter, 20)
		t.Logf("iter=%d done: vs_count=%d", iter, utils.VsCount(ts.Balancer))
	}
}
