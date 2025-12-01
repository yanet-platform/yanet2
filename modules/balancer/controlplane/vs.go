package mbalancer

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

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

func (flags *VsFlags) IntoProto() balancerpb.VsFlags {
	return balancerpb.VsFlags{
		Gre:    flags.GRE,
		FixMss: flags.FixMSS,
		Ops:    flags.OPS,
		PureL3: flags.PureL3,
	}
}

func VsFlagsFromProto(flags *balancerpb.VsFlags) VsFlags {
	return VsFlags{
		GRE:    flags.Gre,
		OPS:    flags.Ops,
		PureL3: flags.PureL3,
		FixMSS: flags.FixMss,
	}
}

////////////////////////////////////////////////////////////////////////////////

type TransportProto balancerpb.TransportProto

const (
	Udp TransportProto = TransportProto(balancerpb.TransportProto_UDP)
	Tcp TransportProto = TransportProto(balancerpb.TransportProto_TCP)
)

func TransportProtoFromProto(p balancerpb.TransportProto) TransportProto {
	return TransportProto(p)
}

func (p TransportProto) IntoProto() balancerpb.TransportProto {
	return balancerpb.TransportProto(p)
}

////////////////////////////////////////////////////////////////////////////////

type VsScheduler balancerpb.VsScheduler

const (
	VsSchedulerWRR VsScheduler = VsScheduler(balancerpb.VsScheduler_WRR)
	VsSchedulerPRR VsScheduler = VsScheduler(balancerpb.VsScheduler_PRR)
	VsSchedulerWLC VsScheduler = VsScheduler(balancerpb.VsScheduler_WLC)
)

func VsSchedulerFromProto(p balancerpb.VsScheduler) VsScheduler {
	return VsScheduler(p)
}

func (p VsScheduler) IntoProto() balancerpb.VsScheduler {
	return balancerpb.VsScheduler(p)
}

////////////////////////////////////////////////////////////////////////////////å

// Description of the Virtual Service related to the
// current module configuration.
type VirtualService struct {
	// Info about virtual service
	Info VirtualServiceInfo

	// Reals
	Reals []Real

	// Index in the virtual service registry
	RegistryIdx uint64

	// Wlc info if service used with WLC scheduler (and `nil` else)
	Wlc *Wlc
}

// Info of the virtual service
type VirtualServiceInfo struct {
	Address    netip.Addr
	Port       uint16
	Proto      TransportProto
	AllowedSrc []netip.Prefix
	Flags      VsFlags
	Scheduler  VsScheduler
}

// Config of the virtual service
type VirtualServiceConfig struct {
	Info  VirtualServiceInfo
	Reals []RealConfig
}

func NewVirtualServiceConfigFromProto(
	proto *balancerpb.VirtualService,
) (*VirtualServiceConfig, error) {
	// Get address
	addr, err := netip.ParseAddr(string(proto.Addr))
	if err != nil {
		return nil, fmt.Errorf("incorrect address: %w", err)
	}

	// Get port
	if proto.Port > math.MaxUint16 {
		return nil, fmt.Errorf(
			"incorrect port %d: only values from %d to %d allowed",
			proto.Port,
			0,
			math.MaxInt16,
		)
	}
	port := uint16(proto.Port)

	// Get protocol
	protocol := TransportProtoFromProto(proto.Proto)

	// Get flags
	flags := VsFlagsFromProto(proto.Flags)

	// Get allowed src
	allowedSrc := make([]netip.Prefix, 0)
	for idx, subnet := range proto.AllowedSrcs {
		addr, ok := netip.AddrFromSlice(subnet.Addr)
		if !ok {
			return nil, fmt.Errorf("failed to parse subnet address no. %d", idx+1)
		}
		allowedPrefix := netip.PrefixFrom(addr, int(subnet.Size))
		allowedSrc = append(allowedSrc, allowedPrefix)
	}

	// Get reals
	reals := make([]RealConfig, 0)
	for idx, real := range proto.Reals {
		r, err := NewRealFromProto(real)
		if err != nil {
			return nil, fmt.Errorf("incorrect real no. %d: %w", idx, err)
		}
		reals = append(reals, *r)
	}

	scheduler := VsSchedulerFromProto(proto.Scheduler)

	info := VirtualServiceInfo{
		Address:    addr,
		Port:       port,
		Proto:      protocol,
		AllowedSrc: allowedSrc,
		Flags:      flags,
		Scheduler:  scheduler,
	}

	return &VirtualServiceConfig{
		Info:  info,
		Reals: reals,
	}, nil
}

func (vs *VirtualServiceConfig) IntoProto() *balancerpb.VirtualService {
	// Make allowed src
	allowedSrc := make([]*balancerpb.Subnet, 0)
	for _, subnet := range vs.Info.AllowedSrc {
		allowedSrc = append(allowedSrc, &balancerpb.Subnet{
			Addr: subnet.Addr().AsSlice(),
			Size: uint32(subnet.Bits()),
		})
	}

	// Make reals
	reals := make([]*balancerpb.Real, 0)
	for _, real := range vs.Reals {
		reals = append(reals, real.IntoProto())
	}

	flags := vs.Info.Flags.IntoProto()

	return &balancerpb.VirtualService{
		Addr:        []byte(vs.Info.Address.String()),
		Port:        uint32(vs.Info.Port),
		Proto:       vs.Info.Proto.IntoProto(),
		AllowedSrcs: allowedSrc,
		Reals:       reals,
		Flags:       &flags,
		Scheduler:   vs.Info.Scheduler.IntoProto(),
	}
}
