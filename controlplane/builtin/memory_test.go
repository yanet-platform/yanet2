package builtin_test

import (
	"math"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

func findMetric(t *testing.T, metrics []*commonpb.Metric, name string, labels map[string]string) *commonpb.Metric {
	t.Helper()

	for _, metric := range metrics {
		if metric.GetName() != name {
			continue
		}

		match := true
		for _, label := range metric.GetLabels() {
			if want, ok := labels[label.GetName()]; ok && want != label.GetValue() {
				match = false
				break
			}
		}
		if match && len(metric.GetLabels()) == len(labels) {
			return metric
		}
	}

	return nil
}

func gaugeValue(t *testing.T, metric *commonpb.Metric) float64 {
	t.Helper()

	require.NotNil(t, metric)
	return metric.GetGauge()
}

// TestMemoryMetricsContextPathResolution verifies that the root node of
// the memory-context tree resolves to a path of just its own name and
// that a child node resolves to its full ancestry chain.
func TestMemoryMetricsContextPathResolution(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "acl", ParentIdx: math.MaxUint32, BAllocSize: 100},
						{Name: "acl-in", ParentIdx: 0, BAllocSize: 60},
						{Name: "filter", ParentIdx: 1, BAllocSize: 40},
						{Name: "net6-nets", ParentIdx: 2, BAllocSize: 10},
					},
				},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	root := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/acl"})
	require.Equal(t, float64(100), gaugeValue(t, root))

	leaf := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/acl/acl-in/filter/net6-nets"})
	require.Equal(t, float64(10), gaugeValue(t, leaf))
}

// TestMemoryMetricsContextDistinguishesSiblingSubtrees verifies that
// same-named leaves under different module-config subtrees produce
// distinct series.
func TestMemoryMetricsContextDistinguishesSiblingSubtrees(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "acl", ParentIdx: math.MaxUint32},
						{Name: "acl-in", ParentIdx: 0},
						{Name: "filter", ParentIdx: 1},
						{Name: "net6-nets", ParentIdx: 2, BAllocSize: 10},
						{Name: "acl-out", ParentIdx: 0},
						{Name: "filter", ParentIdx: 4},
						{Name: "net6-nets", ParentIdx: 5, BAllocSize: 25},
					},
				},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	inLeaf := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/acl/acl-in/filter/net6-nets"})
	require.Equal(t, float64(10), gaugeValue(t, inLeaf))

	outLeaf := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/acl/acl-out/filter/net6-nets"})
	require.Equal(t, float64(25), gaugeValue(t, outLeaf))
}

// TestMemoryMetricsContextClampsUnderflow verifies that a torn snapshot
// where BFreeSize exceeds BAllocSize publishes a zero gauge instead of the
// unsigned-subtraction wraparound value.
func TestMemoryMetricsContextClampsUnderflow(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "root", ParentIdx: math.MaxUint32, BAllocSize: 10, BFreeSize: 20},
					},
				},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	used := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/root"})
	require.Equal(t, float64(0), gaugeValue(t, used))
}

// TestMemoryMetricsRetiredArenaBytes verifies that only the live generation
// feeds the arena limit gauge, that retired generations are summed into
// their own gauge, and that a single-generation agent still emits a
// present, zero-valued retired gauge.
func TestMemoryMetricsRetiredArenaBytes(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{MemoryLimit: 1000, FreeBytes: 700},
				{MemoryLimit: 200},
				{MemoryLimit: 300},
			},
		},
		{
			Name: "decap",
			Instances: []ffi.AgentInstanceInfo{
				{MemoryLimit: 500, FreeBytes: 500},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	limit := findMetric(t, metrics, "memory_arena_limit_bytes", map[string]string{"agent": "acl"})
	require.Equal(t, float64(1000), gaugeValue(t, limit))

	free := findMetric(t, metrics, "memory_arena_free_bytes", map[string]string{"agent": "acl"})
	require.Equal(t, float64(700), gaugeValue(t, free))

	retired := findMetric(t, metrics, "memory_retired_arena_bytes", map[string]string{"agent": "acl"})
	require.Equal(t, float64(500), gaugeValue(t, retired))

	singleGenRetired := findMetric(t, metrics, "memory_retired_arena_bytes", map[string]string{"agent": "decap"})
	require.Equal(t, float64(0), gaugeValue(t, singleGenRetired))
}

// TestMemoryMetricsNonDecreasingParentResolvesToRoot verifies that a node
// whose ParentIdx is not strictly less than its own index -- out of
// range, equal to its own index, greater than its own index, or the
// math.MaxUint32 sentinel -- is treated as a root instead of panicking
// or hanging, guarding against a torn best-effort snapshot.
func TestMemoryMetricsNonDecreasingParentResolvesToRoot(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "out-of-range", ParentIdx: 99, BAllocSize: 10},
						{Name: "self-ref", ParentIdx: 1, BAllocSize: 20},
						{Name: "forward-ref", ParentIdx: 3, BAllocSize: 30},
						{Name: "target", ParentIdx: math.MaxUint32, BAllocSize: 40},
					},
				},
			},
		},
	}

	var metrics []*commonpb.Metric
	require.NotPanics(t, func() {
		metrics = builtin.MemoryMetrics(agents)
	})

	outOfRange := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/out-of-range"})
	require.Equal(t, float64(10), gaugeValue(t, outOfRange))

	selfRef := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/self-ref"})
	require.Equal(t, float64(20), gaugeValue(t, selfRef))

	forwardRef := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/forward-ref"})
	require.Equal(t, float64(30), gaugeValue(t, forwardRef))

	target := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/target"})
	require.Equal(t, float64(40), gaugeValue(t, target))
}

// TestMemoryMetricsContextAggregatesDuplicateLabels verifies that sibling
// nodes sharing a name under the same parent resolve to an identical
// path and are summed into a single series instead of being emitted as
// duplicates under the same label set.
func TestMemoryMetricsContextAggregatesDuplicateLabels(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "root", ParentIdx: math.MaxUint32, BAllocSize: 100},
						{Name: "filter", ParentIdx: 0, BAllocSize: 30},
						{Name: "filter", ParentIdx: 0, BAllocSize: 20},
					},
				},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	var seriesCount int
	for _, metric := range metrics {
		if metric.GetName() == "memory_context_used_bytes" {
			seriesCount++
		}
	}
	require.Equal(t, 2, seriesCount)

	filter := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/root/filter"})
	require.Equal(t, float64(50), gaugeValue(t, filter))
}

// TestMemoryMetricsContextClampsBeforeAggregating verifies that each
// sibling is clamped to zero on underflow before the sum, so an
// underflowing sibling cannot cancel a healthy one's contribution.
func TestMemoryMetricsContextClampsBeforeAggregating(t *testing.T) {
	agents := []ffi.AgentInfo{
		{
			Name: "acl",
			Instances: []ffi.AgentInstanceInfo{
				{
					MemoryTree: []ffi.AgentMemoryNode{
						{Name: "root", ParentIdx: math.MaxUint32, BAllocSize: 100},
						{Name: "filter", ParentIdx: 0, BAllocSize: 100, BFreeSize: 0},
						{Name: "filter", ParentIdx: 0, BAllocSize: 10, BFreeSize: 40},
					},
				},
			},
		},
	}

	metrics := builtin.MemoryMetrics(agents)

	filter := findMetric(t, metrics, "memory_context_used_bytes",
		map[string]string{"agent": "acl", "path": "/root/filter"})
	require.Equal(t, float64(100), gaugeValue(t, filter))
}

// TestMemoryMetricsSkipsAgentWithNoInstances verifies that an agent whose
// Instances slice is empty is skipped rather than indexed into.
func TestMemoryMetricsSkipsAgentWithNoInstances(t *testing.T) {
	agents := []ffi.AgentInfo{
		{Name: "empty"},
	}

	require.NotPanics(t, func() {
		metrics := builtin.MemoryMetrics(agents)
		require.Empty(t, metrics)
	})
}

// moduleAgentSize is the arena a test module agent is attached with, far
// below the controlplane pool of newMemoryHarness so it can grow.
const moduleAgentSize = 4 * datasize.MB

// newMemoryHarness returns a single-instance dataplane harness with a
// 32 MiB controlplane pool, freed when the test ends.
func newMemoryHarness(t *testing.T) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		Devices:       []string{"d0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	return harness
}

// attachModuleAgent attaches an agent with its own arena of
// moduleAgentSize under the given name, as a module control plane does.
func attachModuleAgent(t *testing.T, shm *ffi.SharedMemory, name string) {
	t.Helper()

	_, err := shm.AgentAttach(name, 0, moduleAgentSize)
	require.NoError(t, err)
}

// liveArenaLimit returns the memory limit ListArenas reports for the live
// generation of the named agent.
func liveArenaLimit(t *testing.T, svc *builtin.Memory, name string) uint64 {
	t.Helper()

	response, err := svc.ListArenas(t.Context(), &ynpb.ListArenasRequest{})
	require.NoError(t, err)

	for _, arena := range response.GetArenas() {
		if arena.GetAgent() == name && !arena.GetRetired() {
			return arena.GetMemoryLimit()
		}
	}

	require.FailNow(t, "no live arena listed", "agent %q", name)
	return 0
}

// Test_Memory_ExtendAgent_GrowsModuleAgent verifies that growing a module
// agent by an aligned size reports the attached size plus exactly that
// size.
func Test_Memory_ExtendAgent_GrowsModuleAgent(t *testing.T) {
	shm := newMemoryHarness(t).SharedMemory()
	attachModuleAgent(t, shm, "route")
	svc := builtin.NewMemory(0, shm)

	response, err := svc.ExtendAgent(t.Context(), &ynpb.ExtendAgentRequest{
		Agent: "route",
		Size:  uint64(datasize.MB),
	})
	require.NoError(t, err)
	require.Equal(t, uint64(moduleAgentSize+datasize.MB), response.GetMemoryLimit())
}

// Test_Memory_ExtendAgent_ListsGrownLimit verifies that the limit a growth
// reports is the one the arena listing shows for the live generation.
func Test_Memory_ExtendAgent_ListsGrownLimit(t *testing.T) {
	shm := newMemoryHarness(t).SharedMemory()
	attachModuleAgent(t, shm, "route")
	svc := builtin.NewMemory(0, shm)

	response, err := svc.ExtendAgent(t.Context(), &ynpb.ExtendAgentRequest{
		Agent: "route",
		Size:  uint64(datasize.MB),
	})
	require.NoError(t, err)
	require.Equal(t, response.GetMemoryLimit(), liveArenaLimit(t, svc, "route"))
}

// Test_Memory_ExtendAgent_ExportsGrownLimit verifies that the limit a
// growth reports is the one the arena limit gauge exports for the agent.
func Test_Memory_ExtendAgent_ExportsGrownLimit(t *testing.T) {
	shm := newMemoryHarness(t).SharedMemory()
	attachModuleAgent(t, shm, "route")
	svc := builtin.NewMemory(0, shm)

	response, err := svc.ExtendAgent(t.Context(), &ynpb.ExtendAgentRequest{
		Agent: "route",
		Size:  uint64(datasize.MB),
	})
	require.NoError(t, err)

	limit := findMetric(t, svc.Collect(), "memory_arena_limit_bytes",
		map[string]string{"agent": "route"})
	require.Equal(t, float64(response.GetMemoryLimit()), gaugeValue(t, limit))
}

// Test_Memory_ExtendAgent_RoundsUpUnalignedSize verifies that a size below
// the allocator granularity still grows the agent, by more than was asked.
func Test_Memory_ExtendAgent_RoundsUpUnalignedSize(t *testing.T) {
	shm := newMemoryHarness(t).SharedMemory()
	attachModuleAgent(t, shm, "route")
	svc := builtin.NewMemory(0, shm)

	response, err := svc.ExtendAgent(t.Context(), &ynpb.ExtendAgentRequest{
		Agent: "route",
		Size:  1,
	})
	require.NoError(t, err)
	require.Greater(t, response.GetMemoryLimit(), uint64(moduleAgentSize)+1)
}

// Test_Memory_ExtendAgent_Rejects verifies that each rejected growth
// reports its cause through the gRPC status code alone and leaves every
// attached agent at the limit it had.
func Test_Memory_ExtendAgent_Rejects(t *testing.T) {
	cases := []struct {
		name string
		// instanceID is the dataplane instance the service is bound to.
		instanceID uint32
		// agent attaches the agent under test and returns its name.
		agent func(t *testing.T, shm *ffi.SharedMemory) string
		size  uint64
		code  codes.Code
	}{
		{
			name:       "agent nobody attached",
			instanceID: 0,
			agent:      func(*testing.T, *ffi.SharedMemory) string { return "missing" },
			size:       uint64(datasize.MB),
			code:       codes.NotFound,
		},
		{
			name:       "dataplane instance not initialised",
			instanceID: 1,
			agent: func(t *testing.T, shm *ffi.SharedMemory) string {
				attachModuleAgent(t, shm, "route")
				return "route"
			},
			size: uint64(datasize.MB),
			code: codes.Unavailable,
		},
		{
			name:       "size overflows once rounded up",
			instanceID: 0,
			agent: func(t *testing.T, shm *ffi.SharedMemory) string {
				attachModuleAgent(t, shm, "route")
				return "route"
			},
			size: math.MaxUint64,
			code: codes.InvalidArgument,
		},
		{
			name:       "controlplane pool too small",
			instanceID: 0,
			agent: func(t *testing.T, shm *ffi.SharedMemory) string {
				attachModuleAgent(t, shm, "route")
				return "route"
			},
			size: uint64(datasize.GB),
			code: codes.ResourceExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shm := newMemoryHarness(t).SharedMemory()
			name := tc.agent(t, shm)

			before := builtin.NewMemory(0, shm).Collect()

			_, err := builtin.NewMemory(tc.instanceID, shm).ExtendAgent(
				t.Context(),
				&ynpb.ExtendAgentRequest{Agent: name, Size: tc.size},
			)
			require.Equal(t, tc.code, status.Code(err), "error: %v", err)

			require.Equal(t,
				limitGauges(before),
				limitGauges(builtin.NewMemory(0, shm).Collect()),
				"a rejected growth must leave every arena limit as it was",
			)
		})
	}
}

// limitGauges returns the arena limit gauge of every agent, keyed by the
// agent name.
func limitGauges(metrics []*commonpb.Metric) map[string]float64 {
	limits := map[string]float64{}
	for _, metric := range metrics {
		if metric.GetName() != "memory_arena_limit_bytes" {
			continue
		}
		for _, label := range metric.GetLabels() {
			if label.GetName() == "agent" {
				limits[label.GetValue()] = metric.GetGauge()
			}
		}
	}

	return limits
}
