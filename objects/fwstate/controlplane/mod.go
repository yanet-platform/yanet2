package fwstatemap

import (
	"google.golang.org/grpc"

	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// ServiceName is the fully-qualified gRPC service name for the fwstate-map
// management service, derived from the generated service descriptor.
var ServiceName = fwstatemappb.FWStateMapService_ServiceDesc.ServiceName

// ServicesNames returns the gRPC service names this controlplane serves.
func (m *FWStateMapService) ServicesNames() []string {
	return []string{ServiceName}
}

// Register registers the map service on the given gRPC server.
func (m *FWStateMapService) Register(server *grpc.Server) {
	fwstatemappb.RegisterFWStateMapServiceServer(server, m)
}
