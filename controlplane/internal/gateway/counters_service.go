package gateway

import (
	"context"

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
