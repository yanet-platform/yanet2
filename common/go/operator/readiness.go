package operator

import (
	"context"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// readinessService is the gateway's readiness contract, served by an
// operator under its own name.
type readinessService struct {
	ynpb.UnimplementedReadinessServiceServer

	tracker *readiness.Tracker
}

func (m *readinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Ready(req), nil
}

func (m *readinessService) Watch(
	req *readinesspb.ReadyRequest,
	stream ynpb.ReadinessService_WatchServer,
) error {
	return m.tracker.Watch(stream.Context(), req, stream.Send)
}

// NewReadinessServiceRegistrar serves the shared readiness contract under
// the operator's own name.
func NewReadinessServiceRegistrar(name string, tracker *readiness.Tracker) ServiceRegistrar {
	return func(server *grpc.Server) string {
		desc := ynpb.ReadinessService_ServiceDesc
		desc.ServiceName = ReadinessServiceName(name)
		server.RegisterService(&desc, &readinessService{tracker: tracker})
		return desc.ServiceName
	}
}

// ReadinessServiceName identifies an operator's scoped readiness API.
func ReadinessServiceName(name string) string {
	return "operators." + name + ".operatorpb.v1.ReadinessService"
}
