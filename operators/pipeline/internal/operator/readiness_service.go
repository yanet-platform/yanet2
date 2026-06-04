package operator

import (
	"context"

	"github.com/yanet-platform/yanet2/common/go/operator"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	operatorpb "github.com/yanet-platform/yanet2/operators/pipeline/operatorpb/v1"
)

// ReadinessService implements operatorpb.ReadinessServiceServer by delegating
// to a Readiness tracker that records per-gateway apply outcomes under
// pipeline:<gateway> scopes.
type ReadinessService struct {
	operatorpb.UnimplementedReadinessServiceServer

	tracker *operator.Readiness
}

// NewReadinessService constructs a ReadinessService backed by tracker.
func NewReadinessService(tracker *operator.Readiness) *ReadinessService {
	return &ReadinessService{tracker: tracker}
}

// Ready returns the current readiness state for all pipeline:<gateway> scopes.
func (m *ReadinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Ready(req), nil
}
