package builtin

import (
	"context"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// Counters is an in-process gRPC service for retrieving counters.
type Counters struct {
	ynpb.UnimplementedCountersServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
}

// NewCounters creates a new Counters service.
func NewCounters(instanceID uint32, shm *ffi.SharedMemory) *Counters {
	return &Counters{
		instanceID: instanceID,
		shm:        shm,
	}
}

// Name returns the service name.
func (m *Counters) Name() string { return "counters" }

// Endpoint returns empty string indicating in-process service.
func (m *Counters) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Counters) ServicesNames() []string { return []string{"ynpb.CountersService"} }

// RegisterService registers the service on the given gRPC server.
func (m *Counters) RegisterService(server *grpc.Server) {
	ynpb.RegisterCountersServiceServer(server, m)
}

func (m *Counters) encodeCounters(
	counterValues []ffi.CounterInfo,
) []*ynpb.CounterInfo {
	res := make([]*ynpb.CounterInfo, 0, len(counterValues))

	for _, counter := range counterValues {
		out := &ynpb.CounterInfo{
			Name: counter.Name,
		}

		for iidx := range counter.Values {
			out.Instances = append(
				out.Instances,
				&ynpb.CounterInstanceInfo{
					Values: counter.Values[iidx],
				},
			)
		}

		res = append(res, out)
	}

	return res
}

// Device returns device counters.
func (m *Counters) Device(
	ctx context.Context,
	request *ynpb.DeviceCountersRequest,
) (*ynpb.CountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.DeviceCounters(request.Device)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

// Pipeline returns pipeline counters.
func (m *Counters) Pipeline(
	ctx context.Context,
	request *ynpb.PipelineCountersRequest,
) (*ynpb.CountersResponse, error) {
	device := request.GetDevice()
	pipeline := request.GetPipeline()

	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.PipelineCounters(device, pipeline)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

// Function returns function counters.
func (m *Counters) Function(
	ctx context.Context,
	request *ynpb.FunctionCountersRequest,
) (*ynpb.CountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.FunctionCounters(request.Device, request.Pipeline, request.Function)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

// Chain returns chain counters.
func (m *Counters) Chain(
	ctx context.Context,
	request *ynpb.ChainCountersRequest,
) (*ynpb.CountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.ChainCounters(request.Device, request.Pipeline, request.Function, request.Chain)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

// Module returns module counters.
func (m *Counters) Module(
	ctx context.Context,
	request *ynpb.ModuleCountersRequest,
) (*ynpb.CountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.ModuleCounters(
		request.GetDevice(),
		request.GetPipeline(),
		request.GetFunction(),
		request.GetChain(),
		request.GetModuleType(),
		request.GetModuleName(),
		request.GetCounterQuery(),
	)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

// Perf returns performance counters.
func (m *Counters) Perf(
	ctx context.Context,
	request *ynpb.PerfCountersRequest,
) (*ynpb.PerfCountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	perfCounters, err := dpConfig.PerformanceCounters(
		request.GetDevice(),
		request.GetPipeline(),
		request.GetFunction(),
		request.GetChain(),
		request.GetModuleType(),
		request.GetModuleName(),
	)
	if err != nil {
		return nil, err
	}

	response := &ynpb.PerfCountersResponse{
		Counters: make([]*ynpb.PerfCounter, 0, len(perfCounters.Counters)),
		Tx:       perfCounters.Tx,
		Rx:       perfCounters.Rx,
		TxBytes:  perfCounters.TxBytes,
		RxBytes:  perfCounters.RxBytes,
	}

	for _, counter := range perfCounters.Counters {
		latencies := make([]*ynpb.LatencyRangeCounter, 0, len(counter.LatencyRanges))
		for _, latencyRange := range counter.LatencyRanges {
			latencies = append(latencies, &ynpb.LatencyRangeCounter{
				MinLatency: uint32(latencyRange.MinLatency),
				Batches:    latencyRange.Batches,
			})
		}

		response.Counters = append(response.Counters, &ynpb.PerfCounter{
			MinBatchSize:   uint32(counter.MinBatchSize),
			SummaryLatency: uint64(counter.SummaryLatency),
			Packets:        uint64(counter.Packets),
			Bytes:          uint64(counter.Bytes),
			Latencies:      latencies,
		})
	}

	return response, nil
}
