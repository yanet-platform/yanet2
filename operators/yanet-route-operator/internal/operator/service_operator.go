package operator

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/operatorpb"
)

// RouteOperatorService is the intent surface for the route operator.
// Phase 1: Switch / Status return codes.Unimplemented.
type RouteOperatorService struct {
	operatorpb.UnimplementedRouteOperatorServiceServer
}

// NewRouteOperatorService constructs a RouteOperatorService.
func NewRouteOperatorService(options ...OperatorServiceOption) *RouteOperatorService {
	opts := newOperatorServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &RouteOperatorService{}
}

func (m *RouteOperatorService) Switch(
	ctx context.Context,
	req *operatorpb.SwitchRequest,
) (*operatorpb.SwitchResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *RouteOperatorService) Status(
	ctx context.Context,
	req *operatorpb.StatusRequest,
) (*operatorpb.StatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
