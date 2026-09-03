package operator

import (
	"context"

	"go.uber.org/zap"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/pipeline/operatorpb/v1"
)

// Service implements the PipelineOperatorService gRPC API.
type Service struct {
	operatorpb.UnimplementedPipelineOperatorServiceServer
	operatorpb.UnimplementedMetricsServiceServer

	metrics commonoperator.MetricsCollector
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
	req *commonpb.GetMetricsRequest,
) (*commonpb.GetMetricsResponse, error) {
	all := m.metrics.Collect()

	return &commonpb.GetMetricsResponse{
		Metrics: metrics.Filter(all, req.GetTags()),
	}, nil
}
