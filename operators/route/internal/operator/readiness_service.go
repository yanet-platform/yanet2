package operator

import (
	"context"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// ReadinessService implements operatorpb.ReadinessServiceServer.
//
// All state is delegated to a Readiness tracker that records per-scope
// readiness outcomes.
type ReadinessService struct {
	operatorpb.UnimplementedReadinessServiceServer

	tracker *readiness.Tracker
}

// NewReadinessService constructs a ReadinessService backed by tracker.
func NewReadinessService(tracker *readiness.Tracker) *ReadinessService {
	return &ReadinessService{tracker: tracker}
}

// Ready returns the current per-scope readiness state.
func (m *ReadinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Ready(req), nil
}

// Watch streams readiness changes for the route operator scopes.
func (m *ReadinessService) Watch(
	req *readinesspb.ReadyRequest,
	stream grpc.ServerStreamingServer[readinesspb.ReadyResponse],
) error {
	return m.tracker.Watch(stream.Context(), req, stream.Send)
}
