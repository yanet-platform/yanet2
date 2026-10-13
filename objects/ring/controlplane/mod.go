package ring

import (
	"google.golang.org/grpc"

	ringpb "github.com/yanet-platform/yanet2/objects/ring/controlplane/ringpb/v1"
)

// ServiceName is the full gRPC name of the ring management service.
//
// It comes from the generated service descriptor.
var ServiceName = ringpb.RingService_ServiceDesc.ServiceName

// ServicesNames returns the gRPC service names that this service serves.
func (m *RingService) ServicesNames() []string {
	return []string{ServiceName}
}

// Register registers the ring service on the given gRPC server.
func (m *RingService) Register(server *grpc.Server) {
	ringpb.RegisterRingServiceServer(server, m)
}
