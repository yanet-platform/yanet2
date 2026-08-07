package builtin

import (
	"context"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Memory is an in-process gRPC service for reporting per-agent
// shared-memory arena utilization.
type Memory struct {
	ynpb.UnimplementedMemoryServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
}

// NewMemory creates a new Memory service.
func NewMemory(instanceID uint32, shm *ffi.SharedMemory) *Memory {
	return &Memory{
		instanceID: instanceID,
		shm:        shm,
	}
}

// Name returns the service name.
func (m *Memory) Name() string { return "memory" }

// Endpoint returns empty string indicating in-process service.
func (m *Memory) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Memory) ServicesNames() []string { return []string{"controlplane.ynpb.v1.MemoryService"} }

// RegisterService registers the service on the given gRPC server.
func (m *Memory) RegisterService(server *grpc.Server) {
	ynpb.RegisterMemoryServiceServer(server, m)
}

// ListArenas returns arena utilization for every agent generation.
func (m *Memory) ListArenas(
	ctx context.Context,
	request *ynpb.ListArenasRequest,
) (*ynpb.ListArenasResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	agents := dpConfig.Agents()

	arenas := make([]*ynpb.ArenaInfo, 0)
	for _, agent := range agents {
		for genIdx, instance := range agent.Instances {
			arenas = append(arenas, &ynpb.ArenaInfo{
				Agent:       agent.Name,
				Generation:  instance.Gen,
				Pid:         instance.PID,
				MemoryLimit: instance.MemoryLimit,
				FreeBytes:   instance.FreeBytes,
				Retired:     genIdx > 0,
			})
		}
	}

	return &ynpb.ListArenasResponse{Arenas: arenas}, nil
}

// Collect returns arena occupancy and memory-context attribution gauges
// for every agent.
//
// The free-byte count is the authoritative measure of how full an arena
// is: the buddy allocator behind the memory-context tree rounds each
// request up to a pool size, and an out-of-order module teardown can
// drop a live subtree from the tree walk, so the tree is attribution
// only.
func (m *Memory) Collect() []*commonpb.Metric {
	dpConfig := m.shm.DPConfig(m.instanceID)
	return MemoryMetrics(dpConfig.Agents())
}

// MemoryMetrics converts a shared-memory agent snapshot into gauges.
func MemoryMetrics(agents []ffi.AgentInfo) []*commonpb.Metric {
	metrics := make([]*commonpb.Metric, 0)

	for _, agent := range agents {
		if len(agent.Instances) == 0 {
			continue
		}

		live := agent.Instances[0]
		agentLabel := commonpb.NewLabel("agent", agent.Name)

		var retiredBytes uint64
		for _, instance := range agent.Instances[1:] {
			retiredBytes += instance.MemoryLimit
		}

		metrics = append(metrics,
			commonpb.NewMetricGauge("memory_arena_limit_bytes", float64(live.MemoryLimit), agentLabel),
			commonpb.NewMetricGauge("memory_arena_free_bytes", float64(live.FreeBytes), agentLabel),
			commonpb.NewMetricGauge("memory_retired_arena_bytes", float64(retiredBytes), agentLabel),
		)
		metrics = append(metrics, contextUsedMetrics(agent.Name, live.MemoryTree)...)
	}

	return metrics
}

// contextUsedMetrics builds the per-context-path attribution gauge from
// one agent generation's memory-context tree.
//
// Each series is keyed by a node's full path from the root, not by name
// and parent alone, so same-named leaves under different module configs
// stay distinguishable. Nodes that still resolve to an identical path
// are summed into one series instead of being emitted as duplicates,
// which a scraper would otherwise reject.
func contextUsedMetrics(agent string, tree []ffi.AgentMemoryNode) []*commonpb.Metric {
	paths := make([]string, len(tree))
	used := make(map[string]uint64, len(tree))
	order := make([]string, 0, len(tree))

	for idx, node := range tree {
		path := "/" + node.Name
		if int(node.ParentIdx) < idx {
			path = paths[node.ParentIdx] + "/" + node.Name
		}
		paths[idx] = path

		var nodeUsed uint64
		if node.BAllocSize > node.BFreeSize {
			nodeUsed = node.BAllocSize - node.BFreeSize
		}

		if _, ok := used[path]; !ok {
			order = append(order, path)
		}
		used[path] += nodeUsed
	}

	metrics := make([]*commonpb.Metric, 0, len(order))
	for _, path := range order {
		metrics = append(metrics, commonpb.NewMetricGauge(
			"memory_context_used_bytes",
			float64(used[path]),
			commonpb.NewLabel("agent", agent),
			commonpb.NewLabel("path", path),
		))
	}

	return metrics
}
