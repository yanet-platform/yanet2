package grpc

import (
	"context"

	"github.com/yanet-platform/yanet2/modules/metrics/metricspb"
	"google.golang.org/grpc"
)

type MetricsService interface {
	GetMetrics(context.Context, *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error)
}

type Handler struct {
	metricspb.UnimplementedMetricsServiceServer
	service MetricsService
}

func New(service MetricsService) *Handler {
	return &Handler{
		service: service,
	}
}

func Register(gRPC *grpc.Server, service MetricsService) {
	metricspb.RegisterMetricsServiceServer(gRPC, New(service))
}
