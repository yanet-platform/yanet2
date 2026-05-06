package grpc

import (
	"context"

	"github.com/yanet-platform/yanet2/modules/metrics/metricspb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type (
	MetricsAdapter interface {
		GetMetrics(context.Context, *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error)
	}

	Handler struct {
		metricspb.UnimplementedMetricsAdapterServer
		service MetricsAdapter
		log     *zap.Logger
	}
)

func New(service MetricsAdapter, log *zap.Logger) *Handler {
	return &Handler{
		service: service,
		log:     log,
	}
}

func (h *Handler) GetMetrics(ctx context.Context, req *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error) {
	resp, err := h.service.GetMetrics(ctx, req)
	if err != nil {
		h.log.Error("GetMetrics RPC failed", zap.Error(err))
		return nil, err
	}
	return resp, nil
}

func Register(gRPC *grpc.Server, service MetricsAdapter, log *zap.Logger) {
	metricspb.RegisterMetricsAdapterServer(gRPC, New(service, log))
}
