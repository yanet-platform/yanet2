package gateway

import (
	"context"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// gatewayReadinessScope is the single scope name used by the gateway tracker.
const gatewayReadinessScope = "gateway"

// ReadinessService implements ynpb.ReadinessServiceServer by delegating to a
// shared readiness.Tracker constructed with WithDrainLatch.
type ReadinessService struct {
	ynpb.UnimplementedReadinessServiceServer

	tracker *readiness.Tracker
}

// NewReadinessService constructs a ReadinessService backed by tracker.
func NewReadinessService(tracker *readiness.Tracker) *ReadinessService {
	return &ReadinessService{tracker: tracker}
}

// Ready returns the current readiness state of the gateway scope.
func (m *ReadinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Ready(req), nil
}
