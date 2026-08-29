package acl

import (
	"context"

	"github.com/c2h5oh/datasize"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// ExtendService grows the ACL agent under the module's own service name,
// which is what makes a call reach this module's agent and not another's.
type ExtendService struct {
	aclpb.UnimplementedExtendServiceServer

	agent *ffi.Agent
	log   *zap.Logger
}

func NewExtendService(agent *ffi.Agent, options ...Option) *ExtendService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	return &ExtendService{
		agent: agent,
		log:   opts.Log,
	}
}

// Extend grows the agent by the requested size and reports the limit it
// reached, leaving the module on its current configuration when refused.
func (m *ExtendService) Extend(
	ctx context.Context,
	req *commonpb.ExtendRequest,
) (*commonpb.ExtendResponse, error) {
	size := datasize.ByteSize(req.GetSize())
	if size == 0 {
		return nil, status.Error(codes.InvalidArgument, "requested size is required")
	}

	if err := m.agent.Extend(size); err != nil {
		m.log.Error("failed to grow agent memory",
			zap.Stringer("requested", size),
			zap.Error(err),
		)

		return nil, status.Errorf(
			codes.ResourceExhausted,
			"failed to grow agent memory by %s: %v",
			size,
			err,
		)
	}

	limit := m.agent.MemoryLimit()

	m.log.Info("grew agent memory",
		zap.Stringer("requested", size),
		zap.Stringer("memory_limit", limit),
	)

	return &commonpb.ExtendResponse{MemoryLimit: uint64(limit)}, nil
}
