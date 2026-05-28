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

// ByTags returns counters grouped by tag set, filtered by the request's
// tag and query predicates.
func (m *Counters) ByTags(
	ctx context.Context,
	request *ynpb.CountersByTagsRequest,
) (*ynpb.CountersByTagsResponse, error) {
	reqTags := request.GetTags()
	tags := make([]ffi.CounterTag, len(reqTags))
	for idx, tag := range reqTags {
		tags[idx] = ffi.CounterTag{
			Key:   tag.GetKey(),
			Value: tag.GetValue(),
		}
	}

	dpConfig := m.shm.DPConfig(m.instanceID)
	groups, err := dpConfig.CountersByTags(tags, request.GetQuery())
	if err != nil {
		return nil, err
	}

	response := &ynpb.CountersByTagsResponse{
		Groups: make([]*ynpb.CounterGroup, 0, len(groups)),
	}
	for _, group := range groups {
		pbTags := make([]*ynpb.CounterTag, 0, len(group.Tags))
		for _, tag := range group.Tags {
			pbTags = append(pbTags, &ynpb.CounterTag{
				Key:   tag.Key,
				Value: tag.Value,
			})
		}
		response.Groups = append(response.Groups, &ynpb.CounterGroup{
			Tags:     pbTags,
			Counters: m.encodeCounters(group.Counters),
		})
	}

	return response, nil
}
