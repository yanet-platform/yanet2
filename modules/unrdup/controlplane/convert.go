package unrdup

import (
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

const (
	ipprotoTCP uint8 = 6
	ipprotoUDP uint8 = 17
)

const maxPort = 65535

type endpointKey struct {
	vip  netip.Addr
	port uint16
}

func sourceIsSet(source xnetip.Network) bool {
	addr := source.Addr()
	return addr.IsValid() && !addr.IsUnspecified()
}

func configFromProto(request *unrduppb.Config) (*config, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}

	sourceV4, err := netFromProto(request.GetSourceV4(), 4)
	if err != nil {
		return nil, err
	}

	sourceV6, err := netFromProto(request.GetSourceV6(), 16)
	if err != nil {
		return nil, err
	}

	served := map[endpointKey]int{}

	services := make([]cunrdup.Service, 0, len(request.GetServices()))
	for idx, service := range request.GetServices() {
		converted, err := serviceFromProto(service, sourceV4, sourceV6)
		if err != nil {
			return nil, status.Errorf(
				codes.InvalidArgument, "service %d: %s", idx, err,
			)
		}

		for _, endpoint := range converted.Endpoints {
			key := endpointKey{
				vip:  converted.VIP,
				port: endpoint.Port,
			}

			if owner, ok := served[key]; ok && owner != idx {
				return nil, status.Errorf(
					codes.InvalidArgument,
					"service %d: %s:%d is already served by service %d",
					idx, converted.VIP, endpoint.Port, owner,
				)
			}

			served[key] = idx
		}

		services = append(services, converted)
	}

	return &config{
		SourceV4: sourceV4,
		SourceV6: sourceV6,
		Services: services,
	}, nil
}

func serviceFromProto(
	service *unrduppb.Service,
	sourceV4 xnetip.Network,
	sourceV6 xnetip.Network,
) (cunrdup.Service, error) {
	vip, err := service.GetVip().ToAddr()
	if err != nil {
		return cunrdup.Service{}, status.Errorf(
			codes.InvalidArgument, "vip: %s", err,
		)
	}

	vip = vip.Unmap()
	if vip.IsUnspecified() {
		return cunrdup.Service{}, status.Error(
			codes.InvalidArgument, "vip must not be unspecified",
		)
	}

	peers := make([]netip.Addr, 0, len(service.GetPeers()))
	seen := map[netip.Addr]struct{}{}
	for _, peer := range service.GetPeers() {
		addr, err := peer.ToAddr()
		if err != nil {
			return cunrdup.Service{}, status.Errorf(
				codes.InvalidArgument, "peer: %s", err,
			)
		}

		addr = addr.Unmap()
		if addr.IsUnspecified() {
			return cunrdup.Service{}, status.Error(
				codes.InvalidArgument,
				"peer address must not be unspecified",
			)
		}

		if _, ok := seen[addr]; ok {
			return cunrdup.Service{}, status.Errorf(
				codes.InvalidArgument, "peer %s is listed twice", addr,
			)
		}
		seen[addr] = struct{}{}

		if addr.Is4() && !sourceIsSet(sourceV4) {
			return cunrdup.Service{}, status.Errorf(
				codes.InvalidArgument,
				"peer %s needs source_v4 to be set",
				addr,
			)
		}
		if addr.Is6() && !sourceIsSet(sourceV6) {
			return cunrdup.Service{}, status.Errorf(
				codes.InvalidArgument,
				"peer %s needs source_v6 to be set",
				addr,
			)
		}

		peers = append(peers, addr)
	}

	endpoints := make([]cunrdup.Endpoint, 0, len(service.GetEndpoints()))
	seenEndpoints := map[cunrdup.Endpoint]struct{}{}
	for _, endpoint := range service.GetEndpoints() {
		converted, err := endpointFromProto(endpoint)
		if err != nil {
			return cunrdup.Service{}, err
		}

		if _, ok := seenEndpoints[converted]; ok {
			return cunrdup.Service{}, status.Errorf(
				codes.InvalidArgument,
				"endpoint %d is listed twice",
				converted.Port,
			)
		}
		seenEndpoints[converted] = struct{}{}

		endpoints = append(endpoints, converted)
	}

	if len(peers) == 0 {
		return cunrdup.Service{}, status.Error(
			codes.InvalidArgument, "at least one peer is required",
		)
	}
	if len(endpoints) == 0 {
		return cunrdup.Service{}, status.Error(
			codes.InvalidArgument, "at least one endpoint is required",
		)
	}

	return cunrdup.Service{
		VIP:       vip,
		Peers:     peers,
		Endpoints: endpoints,
	}, nil
}

func endpointFromProto(endpoint *unrduppb.Endpoint) (cunrdup.Endpoint, error) {
	port := endpoint.GetPort()
	if port == 0 || port > maxPort {
		return cunrdup.Endpoint{}, status.Errorf(
			codes.InvalidArgument, "port %d out of range", port,
		)
	}

	var proto uint8
	switch endpoint.GetProtocol() {
	case unrduppb.Protocol_PROTOCOL_TCP:
		proto = ipprotoTCP
	case unrduppb.Protocol_PROTOCOL_UDP:
		proto = ipprotoUDP
	default:
		return cunrdup.Endpoint{}, status.Errorf(
			codes.InvalidArgument,
			"protocol %s is not served",
			endpoint.GetProtocol(),
		)
	}

	return cunrdup.Endpoint{
		Port:  uint16(port),
		Proto: proto,
	}, nil
}

func netFromProto(source *filterpb.IPNet, addrLen int) (xnetip.Network, error) {
	if source == nil {
		return xnetip.Network{}, nil
	}

	addr, ok := netip.AddrFromSlice(source.GetAddr())
	if !ok {
		return xnetip.Network{}, status.Errorf(
			codes.InvalidArgument,
			"source address must be 4 or 16 bytes, got %d",
			len(source.GetAddr()),
		)
	}

	addr = addr.Unmap()
	if addr.BitLen() != addrLen*8 {
		return xnetip.Network{}, status.Errorf(
			codes.InvalidArgument,
			"source %s does not match the family of its field",
			addr,
		)
	}

	if addr.IsUnspecified() {
		return xnetip.Network{}, status.Error(
			codes.InvalidArgument, "source address must not be unspecified",
		)
	}

	mask, ok := netip.AddrFromSlice(source.GetMask())
	if !ok || mask.Unmap().BitLen() != addr.BitLen() {
		return xnetip.Network{}, status.Errorf(
			codes.InvalidArgument,
			"source mask must match the address family, got %d bytes",
			len(source.GetMask()),
		)
	}

	result, err := xnetip.NetworkFrom(addr, mask.Unmap())
	if err != nil {
		return xnetip.Network{}, status.Errorf(
			codes.InvalidArgument, "source: %s", err,
		)
	}

	prefix, ok := result.Prefix()
	if !ok {
		return xnetip.Network{}, status.Error(
			codes.InvalidArgument, "source mask must be contiguous",
		)
	}
	if prefix.Bits() == 0 {
		return xnetip.Network{}, status.Error(
			codes.InvalidArgument,
			"source mask must not leave the whole address free",
		)
	}

	return result, nil
}

func (m *config) ToProto() *unrduppb.Config {
	services := make([]*unrduppb.Service, 0, len(m.Services))
	for idx := range m.Services {
		services = append(services, serviceToProto(&m.Services[idx]))
	}

	return &unrduppb.Config{
		SourceV4: netToProto(m.SourceV4),
		SourceV6: netToProto(m.SourceV6),
		Services: services,
	}
}

func serviceToProto(service *cunrdup.Service) *unrduppb.Service {
	peers := make([]*commonpb.IPAddress, 0, len(service.Peers))
	for _, peer := range service.Peers {
		peers = append(peers, commonpb.NewIPAddressFromAddr(peer))
	}

	endpoints := make([]*unrduppb.Endpoint, 0, len(service.Endpoints))
	for _, endpoint := range service.Endpoints {
		protocol := unrduppb.Protocol_PROTOCOL_UNSPECIFIED
		switch endpoint.Proto {
		case ipprotoTCP:
			protocol = unrduppb.Protocol_PROTOCOL_TCP
		case ipprotoUDP:
			protocol = unrduppb.Protocol_PROTOCOL_UDP
		}

		endpoints = append(endpoints, &unrduppb.Endpoint{
			Port:     uint32(endpoint.Port),
			Protocol: protocol,
		})
	}

	return &unrduppb.Service{
		Vip:       commonpb.NewIPAddressFromAddr(service.VIP),
		Peers:     peers,
		Endpoints: endpoints,
	}
}

func netToProto(source xnetip.Network) *filterpb.IPNet {
	if !sourceIsSet(source) {
		return nil
	}

	addr := source.Addr().Unmap()

	return &filterpb.IPNet{
		Addr: addr.AsSlice(),
		Mask: source.Mask().Unmap().AsSlice(),
	}
}
