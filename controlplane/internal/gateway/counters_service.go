package gateway

import (
	"context"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type CountersService struct {
	ynpb.UnimplementedCountersServiceServer

	shm *ffi.SharedMemory
}

func NewCountersService(shm *ffi.SharedMemory) *CountersService {
	return &CountersService{
		shm: shm,
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

func (m *CountersService) Pipeline(
	ctx context.Context,
	request *ynpb.PipelineCountersRequest,
) (*ynpb.CountersResponse, error) {
	instance := request.GetDpInstance()
	pipeline := request.GetPipeline()

	dpConfig := m.shm.DPConfig(instance)
	counterValues := dpConfig.PipelineCounters(pipeline)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}

func (m *CountersService) PipelineModule(
	ctx context.Context,
	request *ynpb.PipelineModuleCountersRequest,
) (*ynpb.CountersResponse, error) {
	instance := request.GetDpInstance()
	pipeline := request.GetPipeline()
	module_type := request.GetModuleType()
	module_name := request.GetModuleName()

	dpConfig := m.shm.DPConfig(instance)
	counterValues := dpConfig.PipelineModuleCounters(pipeline, module_type, module_name)

	response := &ynpb.CountersResponse{
		Counters: m.encodeCounters(counterValues),
	}

	return response, nil
}
