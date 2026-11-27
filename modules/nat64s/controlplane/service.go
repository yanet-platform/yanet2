package nat64s

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// ServiceOption configures the NAT64sService constructor.
type ServiceOption func(*serviceOptions)

type serviceOptions struct {
	Log *zap.Logger
}

func newServiceOptions() *serviceOptions {
	return &serviceOptions{
		Log: zap.NewNop(),
	}
}

// WithServiceLog sets the logger for the NAT64sService.
func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

// NAT64sService implements the stateful NAT64 gRPC service.
type NAT64sService struct {
	nat64spb.UnimplementedNAT64SServiceServer

	core *crud.Core[*NAT64sConfig]
	log  *zap.Logger
}

// NAT64sConfig is the decoded, whole configuration of one named config.
type NAT64sConfig struct {
	// Prefixes are the 12-byte NAT64 prefixes for v4->v6
	// destinations.
	Prefixes [][]byte
	// Pool is the public IPv4 pool, each address the integer whose bytes
	// in memory are its network-order ones.
	Pool []uint32
	// Timeout is the single session timeout in seconds.
	Timeout   SessionTimeout
	MTU       MTUConfig
	PortRange PortRange
	// MapName names the linked nat64state_map object.
	MapName string
}

// SessionTimeout carries the two session timeouts in seconds: one for
// every session, and a longer one for an established TCP session.
type SessionTimeout struct {
	Timeout        uint16
	TCPEstablished uint16
}

// MTUConfig carries the MTU limits of the translation.
type MTUConfig struct {
	IPv4MTU uint32
	IPv6MTU uint32
}

// PortRange bounds the translated source-port window [Lo, Hi).
type PortRange struct {
	Lo uint32
	Hi uint32
}

const nat64sPrefixBits = 96

// NewNAT64sService creates a NAT64sService over the backend.
func NewNAT64sService(backend Backend, options ...ServiceOption) *NAT64sService {
	opts := newServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &NAT64sService{
		core: crud.NewCore(backend),
		log:  opts.Log,
	}
}

func (m *NAT64sService) ListConfigs(
	ctx context.Context,
	req *nat64spb.ListConfigsRequest,
) (*nat64spb.ListConfigsResponse, error) {
	return &nat64spb.ListConfigsResponse{Configs: m.core.List()}, nil
}

func (m *NAT64sService) ShowConfig(
	ctx context.Context,
	req *nat64spb.ShowConfigRequest,
) (*nat64spb.ShowConfigResponse, error) {
	cfg, ok := m.core.Get(req.GetName())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", req.GetName())
	}
	return &nat64spb.ShowConfigResponse{Config: encodeConfig(cfg)}, nil
}

// UpdateConfig replaces the whole named config with the one the request
// carries, publishing nothing when that config is invalid.
func (m *NAT64sService) UpdateConfig(
	ctx context.Context,
	req *nat64spb.UpdateConfigRequest,
) (*nat64spb.UpdateConfigResponse, error) {
	config, err := decodeConfig(req.GetConfig())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	name := req.GetName()
	if err := m.core.Replace(name, config); err != nil {
		return nil, crud.PublishError(name, err)
	}

	return &nat64spb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config when it is no longer referenced
// by any pipeline.
func (m *NAT64sService) DeleteConfig(
	ctx context.Context,
	req *nat64spb.DeleteConfigRequest,
) (*nat64spb.DeleteConfigResponse, error) {
	if err := m.core.Delete(req.GetName()); err != nil {
		return nil, crud.DeleteError(req.GetName(), err)
	}
	return &nat64spb.DeleteConfigResponse{}, nil
}

// decodeConfig converts a whole wire config into its stored form,
// rejecting anything the dataplane could not translate with.
func decodeConfig(wire *nat64spb.Config) (*NAT64sConfig, error) {
	if wire == nil {
		return nil, fmt.Errorf("config is required")
	}

	config := &NAT64sConfig{}

	seenPrefixes := make(map[[12]byte]struct{}, len(wire.GetPrefixes()))
	for idx, wirePrefix := range wire.GetPrefixes() {
		prefix, err := wirePrefix.ToPrefix()
		if err != nil {
			return nil, fmt.Errorf("prefixes[%d]: invalid prefix: %w", idx, err)
		}
		if prefix.Bits() != nat64sPrefixBits {
			return nil, fmt.Errorf(
				"prefixes[%d]: prefix must be a /%d, got /%d",
				idx, nat64sPrefixBits, prefix.Bits(),
			)
		}
		address := prefix.Masked().Addr().As16()
		key := [12]byte(address[:nat64sPrefixBits/8])
		if _, ok := seenPrefixes[key]; ok {
			return nil, fmt.Errorf("prefixes[%d]: prefix is listed twice", idx)
		}
		seenPrefixes[key] = struct{}{}
		config.Prefixes = append(config.Prefixes, slices.Clone(address[:nat64sPrefixBits/8]))
	}

	seen := make(map[uint32]struct{}, len(wire.GetPool()))
	for idx, wireAddr := range wire.GetPool() {
		if wireAddr == nil {
			return nil, fmt.Errorf("pool[%d]: address is required", idx)
		}
		if !isPoolAddress(wireAddr.ToAddr()) {
			return nil, fmt.Errorf(
				"pool[%d]: %s is not a unicast address of a single host",
				idx,
				wireAddr.ToAddr(),
			)
		}
		addr := networkOrderIPv4(wireAddr)
		if _, ok := seen[addr]; ok {
			return nil, fmt.Errorf("pool[%d]: address is listed twice", idx)
		}
		seen[addr] = struct{}{}
		config.Pool = append(config.Pool, addr)
	}

	if err := nat64spb.ValidateTimeout("timeout", wire.GetTimeout()); err != nil {
		return nil, err
	}
	if err := nat64spb.ValidateTimeout(
		"tcp_established_timeout", wire.GetTcpEstablishedTimeout(),
	); err != nil {
		return nil, err
	}
	config.Timeout.Timeout = uint16(wire.GetTimeout())
	config.Timeout.TCPEstablished = uint16(wire.GetTcpEstablishedTimeout())

	if wireMTU := wire.GetMtu(); wireMTU != nil {
		if wireMTU.GetIpv4Mtu() > nat64spb.MaxIPv4MTU ||
			wireMTU.GetIpv6Mtu() > nat64spb.MaxIPv6MTU {
			return nil, fmt.Errorf(
				"mtu limits must be at most %d for IPv4 and %d for IPv6, "+
					"got ipv4=%d ipv6=%d",
				nat64spb.MaxIPv4MTU,
				nat64spb.MaxIPv6MTU,
				wireMTU.GetIpv4Mtu(),
				wireMTU.GetIpv6Mtu(),
			)
		}
		config.MTU = MTUConfig{
			IPv4MTU: wireMTU.GetIpv4Mtu(),
			IPv6MTU: wireMTU.GetIpv6Mtu(),
		}
	}

	if wireRange := wire.GetPortRange(); !isDefaultPortRange(wireRange) {
		if wireRange.GetLo() >= nat64spb.PortRangeHiMax ||
			wireRange.GetHi() > nat64spb.PortRangeHiMax {
			return nil, fmt.Errorf(
				"port range must lie within [0, %d), got [%d, %d)",
				nat64spb.PortRangeHiMax, wireRange.GetLo(), wireRange.GetHi(),
			)
		}
		lo, hi := wireRange.GetLo(), wireRange.GetHi()
		if hi <= lo {
			return nil, fmt.Errorf(
				"port range must be non-empty, got [%d, %d)", lo, hi,
			)
		}
		config.PortRange = PortRange{Lo: lo, Hi: hi}
	}

	config.MapName = wire.GetMap()
	// The request boundary applies the same rule; a config decoded for
	// an in-process caller gets it here.
	if err := fwstatemappb.ValidateMapNameField("map", config.MapName); err != nil {
		return nil, err
	}

	return config, nil
}

func encodeConfig(config *NAT64sConfig) *nat64spb.Config {
	wire := &nat64spb.Config{
		Pool:                  make([]*commonpb.IPv4Address, 0, len(config.Pool)),
		Timeout:               uint32(config.Timeout.Timeout),
		TcpEstablishedTimeout: uint32(config.Timeout.TCPEstablished),
		Mtu: &nat64spb.MTUConfig{
			Ipv4Mtu: config.MTU.IPv4MTU,
			Ipv6Mtu: config.MTU.IPv6MTU,
		},
		PortRange: &nat64spb.PortRange{
			Lo: config.PortRange.Lo,
			Hi: config.PortRange.Hi,
		},
		Map: config.MapName,
	}

	for _, prefix := range config.Prefixes {
		var address [16]byte
		copy(address[:], prefix)
		wire.Prefixes = append(wire.Prefixes, &commonpb.IPv6Prefix{
			Addr:      commonpb.NewIPv6Address(address),
			PrefixLen: nat64sPrefixBits,
		})
	}
	for _, addr := range config.Pool {
		wire.Pool = append(wire.Pool, wireIPv4Address(addr))
	}

	return wire
}

// isDefaultPortRange reports a window that selects the default one: an
// absent message, or a message left at zeroes as a shown default carries
// it.
func isDefaultPortRange(wireRange *nat64spb.PortRange) bool {
	return wireRange == nil ||
		(wireRange.GetLo() == 0 && wireRange.GetHi() == 0)
}

// isPoolAddress reports whether an address can be one of the translator's
// own on the IPv4 side.
//
// It has to be a unicast address that names a host — neither unspecified,
// loopback nor link-local, and not multicast — outside the blocks held
// for a whole network or for the future, the limited broadcast among
// them: a translated packet leaves with a pool address as its source, and
// nothing could answer for such an address.
func isPoolAddress(address netip.Addr) bool {
	if !address.IsGlobalUnicast() {
		return false
	}
	first := address.As4()[0]
	return first != 0 && first < 240
}

// networkOrderIPv4 is the integer the shared memory carries for a wire IPv4
// address: the one whose bytes in memory are the address's own, in network
// order, whatever the host's byte order.
func networkOrderIPv4(address *commonpb.IPv4Address) uint32 {
	raw := address.ToAddr().As4()
	return binary.NativeEndian.Uint32(raw[:])
}

// wireIPv4Address is networkOrderIPv4's inverse.
func wireIPv4Address(address uint32) *commonpb.IPv4Address {
	var raw [4]byte
	binary.NativeEndian.PutUint32(raw[:], address)
	return commonpb.NewIPv4Address(raw)
}
