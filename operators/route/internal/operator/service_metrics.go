package operator

import (
	"context"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// MetricsCollector renders the current state of the operator metrics
// as a flat slice of commonpb.Metric values.
type MetricsCollector interface {
	Collect() []*commonpb.Metric
}

// MetricsService exposes operator runtime metrics over gRPC.
type MetricsService struct {
	operatorpb.UnimplementedMetricsServiceServer

	metrics MetricsCollector
}

// NewMetricsService constructs a MetricsService bound to the supplied
// collector.
func NewMetricsService(options ...MetricsServiceOption) *MetricsService {
	opts := newMetricsServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &MetricsService{
		metrics: opts.Metrics,
	}
}

// GetMetrics returns the current snapshot of all operator metrics.
//
// When no metrics sink is wired in, the response is empty rather than
// an error.
func (m *MetricsService) GetMetrics(
	ctx context.Context,
	req *operatorpb.GetMetricsRequest,
) (*operatorpb.GetMetricsResponse, error) {
	return &operatorpb.GetMetricsResponse{
		Metrics: m.metrics.Collect(),
	}, nil
}
