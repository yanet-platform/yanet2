package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// CountersService is a gRPC service for retrieving counters.
type CountersService struct {
	ynpb.UnimplementedCountersServiceServer

	instanceID uint32
	shm        *ffi.SharedMemory
}

// NewCountersService creates a new CountersService.
func NewCountersService(instanceID uint32, shm *ffi.SharedMemory) *CountersService {
	return &CountersService{
		instanceID: instanceID,
		shm:        shm,
	}
}

func (m *CountersService) encodeCounters(
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

func (m *CountersService) Device(
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

func (m *CountersService) Pipeline(
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

func (m *CountersService) Function(
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

func (m *CountersService) Chain(
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

func (m *CountersService) Module(
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

// ModuleAggregate aggregates module counters into a structured response.
func (m *CountersService) ModuleAggregate(
	ctx context.Context,
	request *ynpb.ModuleAggregateCountersRequest,
) (*ynpb.ModuleAggregateCountersResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	counterValues := dpConfig.ModuleCounters(
		request.GetDevice(),
		request.GetPipeline(),
		request.GetFunction(),
		request.GetChain(),
		request.GetModuleType(),
		request.GetModuleName(),
		nil, // Get all counters
	)

	// Aggregate counters across all instances
	aggregated := m.aggregateModuleCounters(counterValues)

	response := &ynpb.ModuleAggregateCountersResponse{
		Counters: []*ynpb.ModuleAggregateCounters{aggregated},
	}

	return response, nil
}

// aggregateModuleCounters processes raw counter data and produces aggregated statistics.
func (m *CountersService) aggregateModuleCounters(
	counterValues []ffi.CounterInfo,
) *ynpb.ModuleAggregateCounters {
	result := &ynpb.ModuleAggregateCounters{
		PacketBatches: make([]*ynpb.ModulePerfCounters, 0, 6),
	}

	// Maps to store histogram data by index (0-5)
	histograms := make(map[int][]uint64)

	// Process each counter
	for _, counter := range counterValues {
		// Aggregate values across all instances
		aggregatedValues := m.aggregateCounterValues(counter.Values)

		switch counter.Name {
		case "rx":
			result.RxCount = aggregatedValues[0]
		case "tx":
			result.TxCount = aggregatedValues[0]
		case "rx_bytes":
			result.RxBytes = aggregatedValues[0]
		case "tx_bytes":
			result.TxBytes = aggregatedValues[0]
		default:
			// Check if it's a histogram counter (hist_0 through hist_5)
			if strings.HasPrefix(counter.Name, "hist_") {
				var histIdx int
				if _, err := fmt.Sscanf(counter.Name, "hist_%d", &histIdx); err == nil {
					if histIdx >= 0 && histIdx < 6 {
						histograms[histIdx] = aggregatedValues
					}
				}
			}
		}
	}

	// Process histograms in order (0-5)
	for histIdx := range 6 {
		if histValues, ok := histograms[histIdx]; ok {
			batchCounters := m.processHistogram(histIdx, histValues)
			result.PacketBatches = append(result.PacketBatches, batchCounters)
		}
	}

	return result
}

// aggregateCounterValues sums counter values across all instances.
func (m *CountersService) aggregateCounterValues(values [][]uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}

	// Initialize result with the size of the first instance
	result := make([]uint64, len(values[0]))

	// Sum values from all instances
	for _, instanceValues := range values {
		for i, val := range instanceValues {
			result[i] += val
		}
	}

	return result
}

// processHistogram converts histogram counter values into ModulePerfCounters.
func (m *CountersService) processHistogram(
	histIdx int,
	values []uint64,
) *ynpb.ModulePerfCounters {
	// Batch size is 2^histIdx
	batchSize := uint32(1 << histIdx)

	result := &ynpb.ModulePerfCounters{
		MinBatchSize: batchSize,
		Latencies:    make([]*ynpb.ModulePerfLatency, 0),
	}

	// values[0] contains the sum of all latencies
	totalLatencySum := values[0]

	// values[1-30] contain the latency distribution buckets
	// Calculate total number of batches
	var totalBatches uint64
	for i := 1; i < 31; i++ {
		totalBatches += values[i]
	}

	// Calculate mean latency
	if totalBatches > 0 {
		result.MeanLatency = totalLatencySum / totalBatches
	}

	// Build latency distribution
	// Histogram configuration from cp_module.h:
	// - min_value = 10 ns
	// - linear_step = 50 ns
	// - linear_hists = 20 (buckets 1-20)
	// - exp_hists = 10 (buckets 21-30)

	const minValue = 10   // ns
	const linearStep = 50 // ns
	const linearBuckets = 20

	// Process linear buckets (1-20)
	for i := 1; i <= linearBuckets && i < len(values); i++ {
		minLatency := uint32(minValue + (i-1)*linearStep)
		result.Latencies = append(result.Latencies, &ynpb.ModulePerfLatency{
			MinLatency: minLatency,
			Batches:    values[i],
		})
	}

	// Process exponential buckets (21-30)
	// These represent exponentially increasing ranges
	maxLinearValue := minValue + linearStep*linearBuckets
	for i := linearBuckets + 1; i < 31 && i < len(values); i++ {
		expIdx := i - linearBuckets - 1
		// Each exponential bucket doubles the range
		minLatency := uint32(maxLinearValue * (1 << expIdx))
		result.Latencies = append(result.Latencies, &ynpb.ModulePerfLatency{
			MinLatency: minLatency,
			Batches:    values[i],
		})
	}

	return result
}
