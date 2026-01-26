package main

import (
	"math/rand/v2"
	"net/netip"

	dataplane "github.com/yanet-platform/yanet2/lib/utils/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
)

type session struct {
	clientIp   netip.Addr
	clientPort uint16
	vsIp       netip.Addr
	vsPort     uint16
	proto      balancerpb.TransportProto
}

type Generator struct {
	config    *BenchConfig
	generated int
	rand      *rand.Rand
	balancer  *balancerpb.BalancerConfig
}

func (ctx *Generator) generateWorkerPackets() []dataplane.PacketData {
	return nil
}
