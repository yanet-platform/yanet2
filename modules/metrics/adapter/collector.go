package adapter

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"google.golang.org/grpc"
)

type (
	Collector interface {
		Collect(ctx context.Context) ([]*commonpb.Metric, error)
	}

	ModuleAdapter struct {
		modules []string
		server  *grpc.ClientConn
	}
)

func NewCollector(server *grpc.ClientConn, modules []string) Collector {
	return &ModuleAdapter{
		modules: modules,
		server:  server,
	}
}

func (m *ModuleAdapter) Collect(ctx context.Context) ([]*commonpb.Metric, error) {
	metrics := make([]*commonpb.Metric, 0)
	for _, module := range m.modules {
		mtcs, err := m.collectModule(ctx, module)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, mtcs...)
	}
	return metrics, nil
}

func (m *ModuleAdapter) collectModule(ctx context.Context, module string) ([]*commonpb.Metric, error) {
	resp := &commonpb.GetMetricsResponse{}
	m.server.Invoke(ctx, fmt.Sprintf("yanet.%s.MetricsService/Collect", module), &commonpb.GetMetricsRequest{}, resp)
	return resp.Metrics, nil
}
