package fwstatemap_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

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
			err := fwstatemap.ValidateWorkerCount(tc.workerCount)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, tc.wantCode, status.Code(err))
		})
	}
}

// TestClampBatchSize verifies the default-substitution and clamping
// behaviour of ClampBatchSize.
func TestClampBatchSize(t *testing.T) {
	cases := []struct {
		name string
		in   uint32
		want uint32
	}{
		{name: "zero defaults", in: 0, want: fwstatemap.DefaultListEntriesBatchSize},
		{name: "small passes", in: 1, want: 1},
		{name: "mid passes", in: 500, want: 500},
		{name: "max clamped", in: 1_000_000, want: fwstatemap.MaxListEntriesBatchSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, fwstatemap.ClampBatchSize(tc.in))
		})
	}
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
	pb := fwstatemap.MapStatsToProto(stats)
	require.Equal(t, uint32(1024), pb.GetIndexSize())
	require.Equal(t, uint32(512), pb.GetExtraBucketCount())
	require.Equal(t, uint32(4), pb.GetMaxChainLength())
	require.Equal(t, uint32(2), pb.GetLayerCount())
	require.Equal(t, uint64(1000), pb.GetTotalElements())
	require.Equal(t, uint64(99999), pb.GetMaxDeadline())
	require.Equal(t, uint64(4096), pb.GetMemoryUsed())
}

// TestMapLabeler verifies that the gRPC metrics labeler extracts the map
// name for the relevant request types.
func TestMapLabeler(t *testing.T) {
	require.Equal(t, metrics.Labels{"map": "m1"}, fwstatemap.MapLabeler("", &fwstatemappb.CreateMapRequest{Name: "m1"}))
	require.Equal(t, metrics.Labels{"map": "m2"}, fwstatemap.MapLabeler("", &fwstatemappb.DeleteMapRequest{Name: "m2"}))
	require.Equal(t, metrics.Labels{"map": "m3"}, fwstatemap.MapLabeler("", &fwstatemappb.GetMapStatsRequest{Name: "m3"}))
	require.Equal(t, metrics.Labels{"map": "m4"}, fwstatemap.MapLabeler("", &fwstatemappb.InsertLayerRequest{Name: "m4"}))
	require.Nil(t, fwstatemap.MapLabeler("", &fwstatemappb.ListMapsRequest{}))
}

// recordingTrimmer is a MapLayerTrimmer test double that records trim
// calls so stale-layer reclamation can be asserted without a live
// shared-memory handle.
type recordingTrimmer struct {
	mu       sync.Mutex
	trimCall []uint64
	trimErr  error
	onTrim   func()
}

func (m *recordingTrimmer) TrimStaleLayers(now uint64) error {
	m.mu.Lock()
	m.trimCall = append(m.trimCall, now)
	m.mu.Unlock()
	if m.onTrim != nil {
		m.onTrim()
	}
	return m.trimErr
}

func (m *recordingTrimmer) trimCalls() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]uint64(nil), m.trimCall...)
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
func TestReclaimStaleLayers(t *testing.T) {
	trimmer := &recordingTrimmer{}
	barrier := &recordingBarrier{}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 123456)

	require.Equal(t, []uint64{123456}, trimmer.trimCalls())
	require.Equal(t, 1, barrier.count(), "generation barrier must run after trim")
}

// TestReclaimStaleLayersTrimFailureSkipsBarrier verifies that a trim
// failure skips the generation barrier without panicking.
func TestReclaimStaleLayersTrimFailureSkipsBarrier(t *testing.T) {
	trimmer := &recordingTrimmer{trimErr: errors.New("trim failed")}
	barrier := &recordingBarrier{}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 0)

	require.Equal(t, []uint64{0}, trimmer.trimCalls())
	require.Equal(t, 0, barrier.count(), "barrier must not run when trim fails")
}

// TestReclaimStaleLayersBarrierRunsAfterTrim verifies that the generation
// barrier runs only after TrimStaleLayers completes successfully.
//
// Regression guard for a use-after-free: the fwmap layer chain is shared
// memory walked across all config generations for fallback lookups. A
// worker can be mid-walk on a just-unlinked layer at the moment
// TrimStaleLayers moves it into the stale chain. Advancing the stale chain
// before the grace period can free memory a worker still reads. The
// generation barrier must elapse between trim and the next reclaim.
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

	svc := fwstatemap.NewFWStateMapServiceForTest(func(cfwstate.MapObjectConfig) error {
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
func TestReclaimStaleLayersBarrierFailureIsLogged(t *testing.T) {
	trimmer := &recordingTrimmer{}
	barrier := &recordingBarrier{err: errors.New("generation barrier failed")}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(trimmer, cfwstate.MapObjectConfig{}, 1)

	require.Equal(t, []uint64{1}, trimmer.trimCalls(), "trim runs unconditionally")
	require.Equal(t, 1, barrier.count(), "barrier must run after trim")
}

// TestListMapsEmpty verifies that a fresh service reports no maps.
func TestListMapsEmpty(t *testing.T) {
	svc := fwstatemap.NewFWStateMapServiceForTest(nil)

	resp, err := svc.ListMaps(t.Context(), &fwstatemappb.ListMapsRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.GetMaps())
}

// TestDeleteMapNotFound verifies that deleting a missing map returns
// NotFound without touching the agent.
func TestDeleteMapNotFound(t *testing.T) {
	svc := fwstatemap.NewFWStateMapServiceForTest(nil)

	_, err := svc.DeleteMap(t.Context(), &fwstatemappb.DeleteMapRequest{Name: "absent"})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteMapEmptyNameRejected verifies that an empty name is rejected
// before the agent is consulted.
func TestDeleteMapEmptyNameRejected(t *testing.T) {
	svc := fwstatemap.NewFWStateMapServiceForTest(nil)

	_, err := svc.DeleteMap(t.Context(), &fwstatemappb.DeleteMapRequest{Name: ""})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestListMapsConcurrent verifies that concurrent ListMaps calls on a
// fresh service do not race.
func TestListMapsConcurrent(t *testing.T) {
	svc := fwstatemap.NewFWStateMapServiceForTest(nil)

	group := errgroup.Group{}
	for range 50 {
		group.Go(func() error {
			resp, err := svc.ListMaps(t.Context(), &fwstatemappb.ListMapsRequest{})
			if err != nil {
				return err
			}
			require.Empty(t, resp.GetMaps())
			return nil
		})
	}
	require.NoError(t, group.Wait())
}
