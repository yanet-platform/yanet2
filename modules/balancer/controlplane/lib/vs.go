package lib

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

////////////////////////////////////////////////////////////////////////////////

// This file describes definitions related to virtual service

// Virtual service transport proto (TCP or UDP).
type Proto balancerpb.TransportProto

const (
	ProtoUdp Proto = Proto(balancerpb.TransportProto_UDP)
	ProtoTcp Proto = Proto(balancerpb.TransportProto_TCP)
)

func NewProtoFromProto(p balancerpb.TransportProto) Proto {
	// maybe change name? :)
	return Proto(p)
}

func (p Proto) IntoProto() balancerpb.TransportProto {
	return balancerpb.TransportProto(p)
}

////////////////////////////////////////////////////////////////////////////////

// Virtual service flags.
type VsFlags struct {
	// Use GRE for encapsulation
	GRE bool

	// One packet scheduler
	OPS bool

	// Use pure L3 scheduling, which means
	// service listens to all ports (but transport protocol is fixed)
	PureL3 bool

	// Fix MSS TCP option
	FixMSS bool
}

func (flags VsFlags) IntoProto() *balancerpb.VsFlags {
	return &balancerpb.VsFlags{
		Gre:    flags.GRE,
		FixMss: flags.FixMSS,
		Ops:    flags.OPS,
		PureL3: flags.PureL3,
	}
}

func NewFlagsFromProto(flags *balancerpb.VsFlags) VsFlags {
	if flags == nil {
		return VsFlags{
			GRE:    false,
			OPS:    false,
			PureL3: false,
			FixMSS: false,
		}
	}
	return VsFlags{
		GRE:    flags.Gre,
		OPS:    flags.Ops,
		PureL3: flags.PureL3,
		FixMSS: flags.FixMss,
	}
}

////////////////////////////////////////////////////////////////////////////////

// Scheduler of the virtual service.
type Scheduler balancerpb.VsScheduler

const (
	// Weighted Round Robin selects reals according to their weight and
	// 5-tuple hash.
	SchedulerWRR Scheduler = Scheduler(balancerpb.VsScheduler_WRR)

	// Pure Round Robin selects reals according to their weight and
	// monotonic counter.
	SchedulerPRR Scheduler = Scheduler(balancerpb.VsScheduler_PRR)

	// Weighted Least Connection selects reals according to their weight and
	// number of real connections.
	SchedulerWLC Scheduler = Scheduler(balancerpb.VsScheduler_WLC)
)

func NewSchedulerFromProto(p balancerpb.VsScheduler) Scheduler {
	return Scheduler(p)
}

func (p Scheduler) IntoProto() balancerpb.VsScheduler {
	return balancerpb.VsScheduler(p)
}

////////////////////////////////////////////////////////////////////////////////

// VsIdentifier of the virtual service.
// Every virtual service is composed of
// Ip, Port and Proto. If there are two virtual services
// with different ip, port or proto, they are
// different virtual services.
type VsIdentifier struct {
	// IpV4 or IpV6 address of the virtual service.
	Ip netip.Addr

	// L4 port of the virtual service.
	// If virtual service is L3 only, port is null.
	// If port equals zero, there can be only one
	// virtual services with such IP and Proto,
	Port uint16

	// TCP or UDP proto.
	Proto Proto
}

// Balancer virtual service.
// Also see config.VirtualService for verbose docs.
type VirtualService struct {
	// Index of the virtual service
	// in the module state registry.
	RegistryIdx uint

	Identifier     VsIdentifier
	Flags          VsFlags
	Reals          []Real
	Peers          []netip.Addr
	AllowedSources []netip.Prefix
	Scheduler      Scheduler
}

func (vs *VirtualService) PeersCount() (ipv4 uint64, ipv6 uint64) {
	ipv4 = 0
	ipv6 = 0
	for _, peer := range vs.Peers {
		if peer.Is4() {
			ipv4++
		} else {
			ipv6++
		}
	}
	return ipv4, ipv6
}

// Get weight of real which will be used in config.
func (vs *VirtualService) RealConfigWeight(realIdx int) uint16 {
	if vs.Scheduler == SchedulerWLC {
		return vs.Reals[realIdx].EffectiveWeight
	} else {
		return vs.Reals[realIdx].Weight
	}
}
