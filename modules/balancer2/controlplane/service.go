package balancer2

import "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"

type Service struct {
	balancerpb.UnimplementedBalancerServer
}

// Implement protobuf methods here.
