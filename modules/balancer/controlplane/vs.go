package balancer

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

type VsProto string

const (
	VsProtoUdp VsProto = "UDP"
	VsProtoTcp VsProto = "TCP"
)

// Virtual service description
type VirtualService struct {
	Address    netip.Addr
	Port       uint16
	Proto      VsProto
	AllowedSrc []netip.Prefix
	Reals      []Real
	Flags      VsFlags
}

func NewVirtualServiceFromProto(proto *balancerpb.VirtualService) (*VirtualService, error) {
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
	protocol := VsProtoTcp
	if proto.Proto == string(VsProtoTcp) {

	} else if proto.Proto == string(VsProtoUdp) {
		protocol = VsProtoUdp
	} else {
		return nil, fmt.Errorf("incorrect proto: '%s', only '%s' and '%s' allowed", proto.Proto, VsProtoUdp, VsProtoTcp)
	}

	// Get flags
	flags := VsFlags{
		GRE:    proto.Gre,
		OPS:    proto.Ops,
		PureL3: proto.PureL3,
		FixMSS: proto.FixMss,
	}

	// Get allowed src
	allowedSrc := make([]netip.Prefix, 0)
	for idx, subnet := range proto.AllowedSrcs {
		allowedPrefix, err := netip.ParsePrefix(subnet)
		if err != nil {
			return nil, fmt.Errorf("failed to parse allowed subnet no. %d: %w", idx+1, err)
		}
		allowedSrc = append(allowedSrc, allowedPrefix)
	}

	// Get reals
	reals := make([]Real, 0)
	for idx, real := range proto.Reals {
		r, err := NewRealFromProto(real)
		if err != nil {
			return nil, fmt.Errorf("incorrect real no. %d: %w", idx, err)
		}
		reals = append(reals, *r)
	}

	return &VirtualService{
		Address:    addr,
		Port:       port,
		Proto:      protocol,
		AllowedSrc: allowedSrc,
		Reals:      reals,
		Flags:      flags,
	}, nil
}

func (vs *VirtualService) IntoProto() *balancerpb.VirtualService {
	// Make allowed src
	allowedSrc := make([]string, 0)
	for _, prefix := range vs.AllowedSrc {
		allowedSrc = append(allowedSrc, prefix.String())
	}

	// Make reals
	reals := make([]*balancerpb.Real, 0)
	for _, real := range vs.Reals {
		reals = append(reals, real.IntoProto())
	}

	return &balancerpb.VirtualService{
		Addr:        []byte(vs.Address.String()),
		Port:        uint32(vs.Port),
		Proto:       string(vs.Proto),
		AllowedSrcs: allowedSrc,
		Reals:       reals,
		Gre:         vs.Flags.GRE,
		FixMss:      vs.Flags.FixMSS,
		Ops:         vs.Flags.OPS,
		PureL3:      vs.Flags.PureL3,
	}
}
