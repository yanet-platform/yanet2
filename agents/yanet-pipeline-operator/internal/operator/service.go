package operator

import (
	"context"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
)

// Service implements the PipelineOperatorService gRPC API.
type Service struct {
	operatorpb.UnimplementedPipelineOperatorServiceServer
	operatorpb.UnimplementedMetricsServiceServer

	metrics MetricsCollector
	log     *zap.Logger
}

func NewService(options ...ServiceOption) *Service {
	opts := newServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &Service{
		metrics: opts.Metrics,
		log:     opts.Log,
	}
}

// GetMetrics returns the current snapshot of all operator metrics.
//
// When no metrics sink is wired in, the response is empty rather than an
// error.
func (m *Service) GetMetrics(
	ctx context.Context,
	req *operatorpb.GetMetricsRequest,
) (*operatorpb.GetMetricsResponse, error) {
	metrics := m.metrics.Collect()

	return &operatorpb.GetMetricsResponse{
		Metrics: metrics,
	}, nil
}
