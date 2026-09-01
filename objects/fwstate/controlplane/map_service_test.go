package fwstatemap_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
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

// TestValidateMapName verifies that names the fixed-size C registry cannot store are rejected.
func TestValidateMapName(t *testing.T) {
	cases := []struct {
		name    string
		mapName string
		wantErr bool
	}{
		{name: "empty rejected", mapName: "", wantErr: true},
		{name: "normal name passes", mapName: "fwstate0-v4"},
		{name: "longest name passes", mapName: strings.Repeat("a", 79)},
		{name: "name at limit rejected", mapName: strings.Repeat("a", 80), wantErr: true},
		{name: "far over limit rejected", mapName: strings.Repeat("a", 200), wantErr: true},
		{name: "embedded NUL rejected", mapName: "a\x00b", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fwstatemap.ValidateMapName(tc.mapName)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestResolveCreateWorkerCount verifies that the per-worker sizing is derived from, and matched against, the dataplane worker count.
func TestResolveCreateWorkerCount(t *testing.T) {
	dpCount := func() uint32 { return 4 }

	cases := []struct {
		name         string
		workerCount  uint32
		dpWorkerFunc func() uint32
		want         uint16
		wantErr      bool
	}{
		{
			name:        "zero derives the dataplane count",
			workerCount: 0, dpWorkerFunc: dpCount, want: 4,
		},
		{
			name:        "matching explicit value passes",
			workerCount: 4, dpWorkerFunc: dpCount, want: 4,
		},
		{
			name:        "mismatching explicit value rejected",
			workerCount: 1, dpWorkerFunc: dpCount, wantErr: true,
		},
		{
			name:        "above dataplane count rejected",
			workerCount: 8, dpWorkerFunc: dpCount, wantErr: true,
		},
		{
			name:        "agentless zero still rejected",
			workerCount: 0, dpWorkerFunc: nil, wantErr: true,
		},
		{
			name:        "agentless explicit value passes",
			workerCount: 2, dpWorkerFunc: nil, want: 2,
		},
		{
			name:        "agentless out of range rejected",
			workerCount: 65536, dpWorkerFunc: nil, wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fwstatemap.ResolveCreateWorkerCount(tc.workerCount, tc.dpWorkerFunc)
			if !tc.wantErr {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
				return
			}
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestResolveReadIndex verifies the forward-cursor rejection and the backward start-cursor translation.
func TestResolveReadIndex(t *testing.T) {
	cases := []struct {
		name     string
		backward bool
		index    int64
		want     int64
		wantErr  bool
	}{
		{name: "forward zero passes", index: 0, want: 0},
		{name: "forward positive passes", index: 42, want: 42},
		{name: "forward negative rejected", index: -1, wantErr: true},
		{name: "forward min rejected", index: math.MinInt64, wantErr: true},
		{name: "backward zero becomes upper bound", backward: true, index: 0, want: math.MaxInt64},
		{name: "backward continuation passes", backward: true, index: 7, want: 7},
		{name: "backward exhausted sentinel passes", backward: true, index: -1, want: -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fwstatemap.ResolveReadIndex(tc.backward, tc.index)
			if !tc.wantErr {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
				return
			}
			require.Error(t, err)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
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

// recordingReclaimer is a MapLayerReclaimer test double that records
// unlink and free calls so stale-layer reclamation can be asserted
// without a live shared-memory handle.
type recordingReclaimer struct {
	mu        sync.Mutex
	unlinkAt  []uint64
	unlinkErr error
	freeCalls int
	onUnlink  func()
	onFree    func()
}

func (m *recordingReclaimer) UnlinkStaleLayers(now uint64) error {
	m.mu.Lock()
	m.unlinkAt = append(m.unlinkAt, now)
	m.mu.Unlock()
	if m.onUnlink != nil {
		m.onUnlink()
	}
	return m.unlinkErr
}

func (m *recordingReclaimer) FreeStaleLayers() error {
	m.mu.Lock()
	m.freeCalls++
	m.mu.Unlock()
	if m.onFree != nil {
		m.onFree()
	}
	return nil
}

func (m *recordingReclaimer) unlinkCalls() []uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]uint64(nil), m.unlinkAt...)
}

func (m *recordingReclaimer) frees() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.freeCalls
}

// recordingBarrier captures generation-barrier invocations so the RCU
// grace periods around layer unlink can be asserted without a live
// agent. invoke returns the error at the matching index of errs (nil
// for missing entries), letting tests fail only the pre- or post-unlink
// barrier.
type recordingBarrier struct {
	mu    sync.Mutex
	calls int
	errs  []error
}

func (m *recordingBarrier) invoke(cfwstate.MapObjectConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls-1 < len(m.errs) {
		return m.errs[m.calls-1]
	}
	return nil
}

func (m *recordingBarrier) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// TestReclaimStaleLayers verifies that ReclaimStaleLayers runs the
// grace barrier, unlinks stale layers, barriers again, then frees the
// parked layers.
func TestReclaimStaleLayers(t *testing.T) {
	reclaimer := &recordingReclaimer{}
	barrier := &recordingBarrier{}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(reclaimer, cfwstate.MapObjectConfig{}, 123456)

	require.Equal(t, []uint64{123456}, reclaimer.unlinkCalls())
	require.Equal(t, 2, barrier.count(), "one barrier must run before unlink and one after it")
	require.Equal(t, 1, reclaimer.frees(), "parked layers must be freed after the second barrier")
}

// TestReclaimStaleLayersUnlinkFailureSkipsFree verifies that an unlink
// failure runs no second barrier and frees nothing, without panicking.
func TestReclaimStaleLayersUnlinkFailureSkipsFree(t *testing.T) {
	reclaimer := &recordingReclaimer{unlinkErr: errors.New("unlink failed")}
	barrier := &recordingBarrier{}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(reclaimer, cfwstate.MapObjectConfig{}, 0)

	require.Equal(t, []uint64{0}, reclaimer.unlinkCalls())
	require.Equal(t, 1, barrier.count(), "only the grace barrier runs when unlink fails")
	require.Equal(t, 0, reclaimer.frees(), "nothing was parked, so nothing is freed")
}

// TestReclaimStaleLayersBarrierOrder verifies that the rotation grace
// barrier precedes the unlink decision and the release barrier elapses
// between unlink and free.
//
// Regression guards for lost updates and a use-after-free: a worker
// that loaded the previous head can still be mid-fwtable_insert on it,
// before its deadline is visible — unlinking by that snapshot parks a
// layer the worker then completes into, silently losing the state. And
// the fwmap layer chain is shared memory walked across all config
// generations for fallback lookups, so a worker can be mid-walk on a
// just-unlinked layer at the moment UnlinkStaleLayers parks it; freeing
// before the grace period would release memory a worker still reads.
func TestReclaimStaleLayersBarrierOrder(t *testing.T) {
	var eventsMu sync.Mutex
	var events []string
	record := func(event string) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}

	reclaimer := &recordingReclaimer{
		onUnlink: func() { record("unlink") },
		onFree:   func() { record("free") },
	}

	svc := fwstatemap.NewFWStateMapServiceForTest(func(cfwstate.MapObjectConfig) error {
		record("barrier")
		return nil
	})

	svc.ReclaimStaleLayers(reclaimer, cfwstate.MapObjectConfig{}, 1)

	eventsMu.Lock()
	require.Equal(
		t,
		[]string{"barrier", "unlink", "barrier", "free"},
		events,
		"a barrier must wait out in-flight inserts before unlink, and free must follow the post-unlink barrier",
	)
	eventsMu.Unlock()
}

// TestReclaimStaleLayersGraceBarrierFailureSkipsUnlink verifies that a
// failed grace barrier leaves every layer on the active chain: deciding
// expiry while a worker is still mid-insert into the previous head
// could unlink a layer that insert is about to complete into.
func TestReclaimStaleLayersGraceBarrierFailureSkipsUnlink(t *testing.T) {
	reclaimer := &recordingReclaimer{}
	barrier := &recordingBarrier{errs: []error{errors.New("generation barrier failed")}}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(reclaimer, cfwstate.MapObjectConfig{}, 1)

	require.Empty(t, reclaimer.unlinkCalls(), "unlink must not run when the grace barrier fails")
	require.Equal(t, 1, barrier.count())
	require.Equal(t, 0, reclaimer.frees())
}

// TestReclaimStaleLayersBarrierFailureRetainsLayers verifies that a
// failed post-unlink barrier leaves the parked layers allocated.
//
// Freeing after a failed barrier could release memory a worker still
// walks; the layers must survive for a later round with a successful
// barrier.
func TestReclaimStaleLayersBarrierFailureRetainsLayers(t *testing.T) {
	reclaimer := &recordingReclaimer{}
	barrier := &recordingBarrier{
		errs: []error{nil, errors.New("generation barrier failed")},
	}

	svc := fwstatemap.NewFWStateMapServiceForTest(barrier.invoke)

	svc.ReclaimStaleLayers(reclaimer, cfwstate.MapObjectConfig{}, 1)

	require.Equal(t, []uint64{1}, reclaimer.unlinkCalls(), "unlink runs after a successful grace barrier")
	require.Equal(t, 2, barrier.count(), "the grace barrier succeeds and the release barrier fails")
	require.Equal(t, 0, reclaimer.frees(), "parked layers must be retained when the release barrier fails")
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

// TestCreateMapRejectsUnknownKind verifies that an unknown Kind
// discriminant from a direct gRPC or HTTP client is rejected as
// InvalidArgument instead of silently provisioning an IPv4 object of
// the wrong family, mirroring the unknown-direction rejection.
func TestCreateMapRejectsUnknownKind(t *testing.T) {
	svc := fwstatemap.NewFWStateMapServiceForTest(nil)

	_, err := svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
		Name:        "bad-kind",
		Kind:        fwstatemappb.Kind(2),
		WorkerCount: 1,
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	resp, listErr := svc.ListMaps(t.Context(), &fwstatemappb.ListMapsRequest{})
	require.NoError(t, listErr)
	require.Empty(t, resp.GetMaps(), "a rejected create must not register a map")
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

// TestListEntriesEmptyMapTerminates verifies that both dump directions end on a map with no entries.
func TestListEntriesEmptyMapTerminates(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstatemap-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := fwstatemap.NewFWStateMapService(agent)
	_, err = svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
		Name:             "empty-v4",
		Kind:             fwstatemappb.Kind_V4,
		IndexSize:        1024,
		ExtraBucketCount: 64,
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name      string
		direction fwstatemappb.Direction
	}{
		{name: "backward", direction: fwstatemappb.Direction_BACKWARD},
		{name: "forward", direction: fwstatemappb.Direction_FORWARD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ListEntries(t.Context(), &fwstatemappb.ListEntriesRequest{
				MapName:   "empty-v4",
				Direction: tc.direction,
				BatchSize: 10,
			})
			require.NoError(t, err)
			require.Empty(t, resp.GetEntries())
			require.False(t, resp.GetHasMore())
		})
	}
}

// Test_ListMapsCarriesKinds verifies that the listing names each map's
// address family, so callers can scope a name to a family without a
// per-map lookup.
func Test_ListMapsCarriesKinds(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstatemap-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := fwstatemap.NewFWStateMapService(agent)
	for name, kind := range map[string]fwstatemappb.Kind{
		"kinds-v4": fwstatemappb.Kind_V4,
		"kinds-v6": fwstatemappb.Kind_V6,
	} {
		_, err := svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
			Name:             name,
			Kind:             kind,
			IndexSize:        1024,
			ExtraBucketCount: 64,
		})
		require.NoError(t, err)
	}

	resp, err := svc.ListMaps(t.Context(), &fwstatemappb.ListMapsRequest{})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"kinds-v4", "kinds-v6"}, resp.GetMaps())
	require.Equal(t, fwstatemappb.Kind_V4, resp.GetKinds()["kinds-v4"])
	require.Equal(t, fwstatemappb.Kind_V6, resp.GetKinds()["kinds-v6"])
}

// Test_FWStateMapService_MetricsExposesGRPCSeries verifies that the map
// service's metrics collection returns the gRPC series its interceptor
// records, labelled with the map service's own service name.
func Test_FWStateMapService_MetricsExposesGRPCSeries(t *testing.T) {
	svc := fwstatemap.NewFWStateMapService(
		nil,
		fwstatemap.WithMetrics(fwstatemap.NewMetricsFactory()),
	)
	interceptor := svc.UnaryServerInterceptor()
	require.NotNil(t, interceptor)

	info := &grpc.UnaryServerInfo{
		FullMethod: "/" + fwstatemap.ServiceName + "/ListMaps",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return &fwstatemappb.ListMapsResponse{}, nil
	}
	_, err := interceptor(t.Context(), &fwstatemappb.ListMapsRequest{}, info, handler)
	require.NoError(t, err)

	collected, err := svc.Metrics()
	require.NoError(t, err)

	var started *commonpb.Metric
	for _, metric := range collected {
		if metric.Name == "grpc_server_started_total" {
			started = metric
		}
	}
	require.NotNil(t, started, "map service metrics must include the gRPC started counter")

	serviceLabel := ""
	for _, label := range started.Labels {
		if label.Name == "grpc_service" {
			serviceLabel = label.Value
		}
	}
	require.Equal(t, fwstatemap.ServiceName, serviceLabel)
}

// Test_FWStateMapService_ListEntriesWithoutInstanceTime verifies that a
// listing is refused while the instance has published no time.
//
// Every entry a listing returns is labelled with whether it has expired,
// and that label is derived from the instance's clock. Answering without
// one would report every entry as live, expired ones included.
func Test_FWStateMapService_ListEntriesWithoutInstanceTime(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	// Wind the instance back to where a worker that has never run leaves
	// it, which is how it reports having no time at all.
	h.SetCurrentTime(time.Unix(0, 0))

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstatemap-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := fwstatemap.NewFWStateMapService(agent)
	_, err = svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
		Name:             "no-time-list-v4",
		Kind:             fwstatemappb.Kind_V4,
		IndexSize:        1024,
		ExtraBucketCount: 64,
	})
	require.NoError(t, err)

	_, err = svc.ListEntries(t.Context(), &fwstatemappb.ListEntriesRequest{
		MapName:   "no-time-list-v4",
		Direction: fwstatemappb.Direction_FORWARD,
		BatchSize: 10,
	})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

// Test_FWStateMapService_MetricsOmitLifetimeWithoutInstanceTime verifies
// that the remaining-lifetime gauge is left out while the instance has
// published no time, and that the rest of the set is still exported.
//
// The furthest deadline in a map is on the instance's clock. With no time
// from that clock there is nothing to measure it against, and a zero would
// read on a dashboard as an expiry that has already happened.
func Test_FWStateMapService_MetricsOmitLifetimeWithoutInstanceTime(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	// Wind the instance back to where a worker that has never run leaves
	// it, which is how it reports having no time at all.
	h.SetCurrentTime(time.Unix(0, 0))

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstatemap-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := fwstatemap.NewFWStateMapService(agent)
	_, err = svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
		Name:             "no-time-v4",
		Kind:             fwstatemappb.Kind_V4,
		IndexSize:        1024,
		ExtraBucketCount: 64,
	})
	require.NoError(t, err)

	collected, err := svc.Metrics()
	require.NoError(t, err)

	gauges := map[string]*commonpb.Metric{}
	for _, metric := range collected {
		gauges[metric.Name] = metric
	}
	require.NotContains(t, gauges, "fwstate_max_deadline_ns",
		"a lifetime must not be reported without a clock to measure it")
	require.Contains(t, gauges, "fwstate_total_elements",
		"the rest of the gauge set must still be exported")
}

// Test_FWStateMapService_MetricsEmitPerMapGauges verifies that the map
// service's metrics collection exports the table-statistics gauge set
// for every live map, labelled with the map's name and address family.
func Test_FWStateMapService_MetricsEmitPerMapGauges(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstatemap-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	svc := fwstatemap.NewFWStateMapService(agent)
	_, err = svc.CreateMap(t.Context(), &fwstatemappb.CreateMapRequest{
		Name:             "gauges-v4",
		Kind:             fwstatemappb.Kind_V4,
		IndexSize:        1024,
		ExtraBucketCount: 64,
	})
	require.NoError(t, err)

	collected, err := svc.Metrics()
	require.NoError(t, err)

	gauges := map[string]*commonpb.Metric{}
	for _, metric := range collected {
		gauges[metric.Name] = metric
	}
	for _, name := range []string{
		"fwstate_index_size",
		"fwstate_extra_bucket_count",
		"fwstate_max_chain_length",
		"fwstate_layer_count",
		"fwstate_total_elements",
		"fwstate_max_deadline_ns",
		"fwstate_memory_bytes",
	} {
		require.Contains(t, gauges, name, "the per-map gauge set must include %s", name)
	}

	labels := map[string]string{}
	for _, label := range gauges["fwstate_total_elements"].Labels {
		labels[label.Name] = label.Value
	}
	require.Equal(t, "gauges-v4", labels["map"])
	require.Equal(t, "ipv4", labels["af"])
}
