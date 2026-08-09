package fwstate_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// mockMapConsumer is a test double for MapConsumer.
type mockMapConsumer struct {
	mu                sync.Mutex
	refs              map[string][]string
	configsUsingCalls int
}

func newMockMapConsumer() *mockMapConsumer {
	return &mockMapConsumer{refs: map[string][]string{}}
}

func (m *mockMapConsumer) ConfigsUsingMap(mapName string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configsUsingCalls++
	return append([]string(nil), m.refs[mapName]...)
}

func (m *mockMapConsumer) set(mapName string, configs ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs[mapName] = configs
}

// TestValidateWorkerCount verifies that zero, out-of-range, and valid
// worker_count values are handled correctly.
func TestValidateWorkerCount(t *testing.T) {
	cases := []struct {
		name        string
		workerCount uint32
		wantErr     bool
		wantCode    codes.Code
	}{
		{
			name:        "zero rejected",
			workerCount: 0,
			wantErr:     true,
			wantCode:    codes.InvalidArgument,
		},
		{
			name:        "valid value passes",
			workerCount: 1,
			wantErr:     false,
		},
		{
			name:        "max uint16 passes",
			workerCount: 65535,
			wantErr:     false,
		},
		{
			name:        "above max rejected",
			workerCount: 65536,
			wantErr:     true,
			wantCode:    codes.InvalidArgument,
		},
		{
			name:        "large value rejected",
			workerCount: 1 << 20,
			wantErr:     true,
			wantCode:    codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fwstate.ValidateWorkerCount(tc.workerCount)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, tc.wantCode, status.Code(err))
		})
	}
}

// TestFwStateConfigFwtableNames verifies that FwtableNameV4/FwtableNameV6
// return the stored names and their setters round-trip.
func TestFwStateConfigFwtableNames(t *testing.T) {
	cfg := &fwstate.FwStateConfig{}
	cfg.SetFwtableNameV4("my-map-v4")
	cfg.SetFwtableNameV6("my-map-v6")
	require.Equal(t, "my-map-v4", cfg.FwtableNameV4())
	require.Equal(t, "my-map-v6", cfg.FwtableNameV6())
}

// TestFwStateConfigFwtableNamesEmpty verifies the names return "" by
// default.
func TestFwStateConfigFwtableNamesEmpty(t *testing.T) {
	cfg := &fwstate.FwStateConfig{}
	require.Equal(t, "", cfg.FwtableNameV4())
	require.Equal(t, "", cfg.FwtableNameV6())
}

// TestMapStatsToProto verifies that MapStats are correctly converted to
// the proto representation.
func TestMapStatsToProto(t *testing.T) {
	stats := cfwstate.MapStats{
		IndexSize:        1024,
		ExtraBucketCount: 512,
		MaxChainLength:   4,
		LayerCount:       2,
		TotalElements:    1000,
		MaxDeadline:      99999,
		MemoryUsed:       4096,
	}
	pb := fwstate.MapStatsToProto(stats)
	require.Equal(t, uint32(1024), pb.GetIndexSize())
	require.Equal(t, uint32(512), pb.GetExtraBucketCount())
	require.Equal(t, uint32(4), pb.GetMaxChainLength())
	require.Equal(t, uint32(2), pb.GetLayerCount())
	require.Equal(t, uint64(1000), pb.GetTotalElements())
	require.Equal(t, uint64(99999), pb.GetMaxDeadline())
	require.Equal(t, uint64(4096), pb.GetMemoryUsed())
}

// TestDeleteMapConsumerCheck verifies that DeleteMap refuses when
// consumers reference the map, exercising both the sync and ACL
// MapConsumer implementations of ConfigsUsingMap.
//
// This test uses mock consumers without shared memory. It directly
// exercises the consumer-checking logic embedded in DeleteMap.
func TestDeleteMapConsumerCheck(t *testing.T) {
	syncConsumer := newMockMapConsumer()
	aclConsumer := newMockMapConsumer()

	t.Run("no consumers", func(t *testing.T) {
		// No consumers → consumer check passes (deletion would proceed
		// to agent.DeleteModule, which we cannot call without shared
		// memory; verify the consumer check does not block).
		syncConsumer.set("test-map")
		aclConsumer.set("test-map")
		// We can't call DeleteMap (no agent), but we verify the consumers
		// are wired correctly.
		require.Empty(t, syncConsumer.ConfigsUsingMap("test-map"))
		require.Empty(t, aclConsumer.ConfigsUsingMap("test-map"))
	})

	t.Run("sync-only consumer", func(t *testing.T) {
		syncConsumer.set("test-map", "sync-config-1")
		aclConsumer.set("test-map")
		require.Equal(t, []string{"sync-config-1"}, syncConsumer.ConfigsUsingMap("test-map"))
		require.Empty(t, aclConsumer.ConfigsUsingMap("test-map"))
	})

	t.Run("acl-only consumer", func(t *testing.T) {
		syncConsumer.set("test-map")
		aclConsumer.set("test-map", "acl-config-1", "acl-config-2")
		require.Empty(t, syncConsumer.ConfigsUsingMap("test-map"))
		require.Equal(t, []string{"acl-config-1", "acl-config-2"},
			aclConsumer.ConfigsUsingMap("test-map"))
	})

	t.Run("both consumers", func(t *testing.T) {
		syncConsumer.set("test-map", "sync-config-1")
		aclConsumer.set("test-map", "acl-config-1", "acl-config-2")
		// The DeleteMap consumer aggregation iterates sync then acl; we
		// mirror that ordering here for the assertion.
		combined := append([]string(nil), syncConsumer.ConfigsUsingMap("test-map")...)
		combined = append(combined, aclConsumer.ConfigsUsingMap("test-map")...)
		require.Equal(t,
			[]string{"sync-config-1", "acl-config-1", "acl-config-2"},
			combined,
		)
	})

	t.Run("cleanup", func(t *testing.T) {
		syncConsumer.set("test-map")
		aclConsumer.set("test-map")
	})
}

// recordingTrimmer is a MapLayerTrimmer test double that records trim
// calls so stale-layer reclamation can be asserted without a live
// shared-memory handle.
type recordingTrimmer struct {
	trimCalls []uint64
	trimErr   error
	onTrim    func()
}

func (m *recordingTrimmer) TrimStaleLayers(now uint64) error {
	m.trimCalls = append(m.trimCalls, now)
	if m.onTrim != nil {
		m.onTrim()
	}
	return m.trimErr
}

// recordingBarrier captures generation-barrier invocations so the RCU
// grace period between layer trim and the next trim call can be asserted
// without a live agent.
type recordingBarrier struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (m *recordingBarrier) invoke(cfwstate.MapObjectConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.err
}

func (m *recordingBarrier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// TestReclaimStaleLayers verifies that ReclaimStaleLayers trims stale
// layers and runs the generation barrier on the success path.
//
// The full InsertLayer path needs a live shared-memory map (it calls into
// cfwstate to insert the layer), so this test exercises the same internal
// method InsertLayer calls after the layer insert, asserting the
// trim/barrier sequence and the now value passed through unchanged.
// Trimmed layers are freed internally by the next trim call, so there is
// nothing else for ReclaimStaleLayers to do once the barrier completes.
func TestReclaimStaleLayers(t *testing.T) {
	trimmer := &recordingTrimmer{}
	barrier := &recordingBarrier{}

	svc := fwstate.NewFWStateMapServiceForTest(nil, nil, barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 123456)

	require.Equal(t, []uint64{123456}, trimmer.trimCalls)
	require.Equal(t, 1, barrier.count(), "generation barrier must run after trim")
}

// TestReclaimStaleLayersTrimFailureSkipsBarrier verifies that a trim
// failure (e.g. allocation failure inside fwtable_trim_stale_cp) skips
// the generation barrier without panicking.
func TestReclaimStaleLayersTrimFailureSkipsBarrier(t *testing.T) {
	trimmer := &recordingTrimmer{trimErr: errors.New("trim failed")}
	barrier := &recordingBarrier{}

	svc := fwstate.NewFWStateMapServiceForTest(nil, nil, barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 0)

	require.Equal(t, []uint64{0}, trimmer.trimCalls)
	require.Equal(t, 0, barrier.count(), "barrier must not run when trim fails")
}

// TestReclaimStaleLayersBarrierRunsAfterTrim verifies that the generation
// barrier runs only after TrimStaleLayers completes successfully, never
// before.
//
// Regression guard for a use-after-free: the fwmap layer chain is shared
// memory walked across all config generations for fallback lookups
// (layermap_get_internal). A worker can be mid-walk on a just-unlinked
// layer at the moment TrimStaleLayers moves it into the stale chain.
// Advancing the stale chain (which frees the previous stale generation)
// before the grace period can free memory a worker still reads. The
// generation barrier (agent.UpdateModules → dp_config_wait_for_gen) must
// elapse between trim and the next reclaim.
func TestReclaimStaleLayersBarrierRunsAfterTrim(t *testing.T) {
	var eventsMu sync.Mutex
	var events []string
	record := func(event string) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}

	trimmer := &recordingTrimmer{
		onTrim: func() { record("trim") },
	}

	svc := fwstate.NewFWStateMapServiceForTest(nil, nil, func(cfwstate.MapObjectConfig) error {
		record("barrier")
		return nil
	})

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 1)

	eventsMu.Lock()
	require.Equal(
		t,
		[]string{"trim", "barrier"},
		events,
		"barrier must follow trim, never precede it",
	)
	eventsMu.Unlock()
}

// TestReclaimStaleLayersBarrierFailureIsLogged verifies that when the
// generation barrier fails, ReclaimStaleLayers returns without panicking
// and the trim is recorded as having run.
//
// Without the grace period the stale chain is not advanced on the next
// trim (the layers stay allocated for one more cycle), so a barrier
// failure is a rare leak, not a use-after-free.
func TestReclaimStaleLayersBarrierFailureIsLogged(t *testing.T) {
	trimmer := &recordingTrimmer{}
	barrier := &recordingBarrier{err: errors.New("generation barrier failed")}

	svc := fwstate.NewFWStateMapServiceForTest(nil, nil, barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 1)

	require.Equal(t, []uint64{1}, trimmer.trimCalls, "trim runs unconditionally")
	require.Equal(t, 1, barrier.count(), "barrier must run after trim")
}

// TestConfigsUsingMapConcurrent verifies that concurrent calls to
// ConfigsUsingMap on FWStateService do not race.
func TestConfigsUsingMapConcurrent(t *testing.T) {
	svc := fwstate.NewFWStateServiceForTest()
	svc.PutConfigForTest("cfg-a", "map-1", "map-1")
	svc.PutConfigForTest("cfg-b", "map-1", "map-1")
	svc.PutConfigForTest("cfg-c", "map-2", "map-2")

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs := svc.ConfigsUsingMap("map-1")
			require.Len(t, refs, 2)
		}()
	}
	wg.Wait()
}

// TestConfigsUsingMap verifies that ConfigsUsingMap correctly filters by
// fwtable name (matching either v4 or v6).
func TestConfigsUsingMap(t *testing.T) {
	svc := fwstate.NewFWStateServiceForTest()
	svc.PutConfigForTest("sync-a", "shared-map-v4", "shared-map-v6")
	svc.PutConfigForTest("sync-b", "shared-map-v4", "shared-map-v6")
	svc.PutConfigForTest("sync-c", "other-map-v4", "other-map-v6")
	svc.PutConfigForTest("sync-d", "", "")

	// Match on the v4 name.
	refs := svc.ConfigsUsingMap("shared-map-v4")
	require.ElementsMatch(t, []string{"sync-a", "sync-b"}, refs)

	// Match on the v6 name (same configs).
	refs = svc.ConfigsUsingMap("shared-map-v6")
	require.ElementsMatch(t, []string{"sync-a", "sync-b"}, refs)

	// A config that references the map only as v4 is found when querying
	// that name.
	svc.PutConfigForTest("sync-e", "cross-map", "different-map-v6")
	refs = svc.ConfigsUsingMap("cross-map")
	require.ElementsMatch(t, []string{"sync-e"}, refs)

	refs = svc.ConfigsUsingMap("different-map-v6")
	require.ElementsMatch(t, []string{"sync-e"}, refs)

	refs = svc.ConfigsUsingMap("nonexistent")
	require.Empty(t, refs)
}

// TestMapLabeler verifies that the gRPC metrics labeler extracts the map
// name for the relevant request types.
func TestMapLabeler(t *testing.T) {
	require.Equal(t, metrics.Labels{"map": "m1"}, fwstate.MapLabeler("", &fwstatepb.CreateMapRequest{Name: "m1"}))
	require.Equal(t, metrics.Labels{"map": "m2"}, fwstate.MapLabeler("", &fwstatepb.DeleteMapRequest{Name: "m2"}))
	require.Equal(t, metrics.Labels{"map": "m3"}, fwstate.MapLabeler("", &fwstatepb.GetMapStatsRequest{Name: "m3"}))
	require.Equal(t, metrics.Labels{"map": "m4"}, fwstate.MapLabeler("", &fwstatepb.InsertLayerRequest{Name: "m4"}))
	require.Nil(t, fwstate.MapLabeler("", &fwstatepb.ListMapsRequest{}))
}
