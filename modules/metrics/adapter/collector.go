package adapter

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type MetricsClient interface {
	GetMetrics(ctx context.Context, in *commonpb.GetMetricsRequest, opts ...grpc.CallOption) (*commonpb.GetMetricsResponse, error)
}

type (
	Collector interface {
		Collect(ctx context.Context) ([]*commonpb.Metric, error)
	}

	ModuleAdapter struct {
		modules []string
		client  MetricsClient
		log     *zap.Logger
	}
)

func NewCollector(client MetricsClient, modules []string, log *zap.Logger) Collector {
	if log == nil {
		log = zap.NewNop()
	}
	return &ModuleAdapter{
		modules: modules,
		client:  client,
		log:     log,
	}
}

func (m *ModuleAdapter) Collect(ctx context.Context) ([]*commonpb.Metric, error) {
	if len(m.modules) == 0 {
		return nil, nil
	}

	type chunk struct {
		module  string
		metrics []*commonpb.Metric
	}

	chunks := make([]chunk, len(m.modules))
	g, gctx := errgroup.WithContext(ctx)

	for i, module := range m.modules {
		g.Go(func() error {
			mtcs, err := m.collectModule(gctx, module)
			if err != nil {
				m.log.Warn("failed to collect from module",
					zap.String("module", module),
					zap.Error(err),
				)
				return fmt.Errorf("module %q: %w", module, err)
			}

			chunks[i] = chunk{module: module, metrics: mtcs}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	total := 0
	for _, c := range chunks {
		total += len(c.metrics)
	}

	metrics := make([]*commonpb.Metric, 0, total)
	for _, c := range chunks {
		metrics = append(metrics, c.metrics...)
	}
	return metrics, nil
}

func (m *ModuleAdapter) collectModule(ctx context.Context, _ string) ([]*commonpb.Metric, error) {
	resp, err := m.client.GetMetrics(ctx, &commonpb.GetMetricsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetMetrics(), nil
}
