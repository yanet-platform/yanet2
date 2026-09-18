package unrdup

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

const (
	ipprotoTCP uint8 = 6
	ipprotoUDP uint8 = 17
)

type endpointKey struct {
	vip  netip.Addr
	port uint16
}

func sourceIsSet(source xnetip.Network) bool {
	addr := source.Addr()
	return addr.IsValid() && !addr.IsUnspecified()
}

func configFromProto(request *unrduppb.Config) (*config, error) {
	sourceV4, err := sourceV4FromProto(request.GetSourceV4())
	if err != nil {
		return nil, err
	}

	sourceV6, err := sourceV6FromProto(request.GetSourceV6())
	if err != nil {
		return nil, err
	}

	served := map[endpointKey]int{}

	services := make([]cunrdup.Service, 0, len(request.GetServices()))
	for idx, service := range request.GetServices() {
		converted, err := serviceFromProto(service, sourceV4, sourceV6)
		if err != nil {
			return nil, fmt.Errorf("service %d: %w", idx, err)
		}

		for _, endpoint := range converted.Endpoints {
			key := endpointKey{
				vip:  converted.VIP,
				port: endpoint.Port,
			}

			if owner, ok := served[key]; ok && owner != idx {
				return nil, fmt.Errorf(
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
		return cunrdup.Service{}, fmt.Errorf("vip: %w", err)
	}

	vip = vip.Unmap()
	if vip.IsUnspecified() {
		return cunrdup.Service{}, errors.New("vip must not be unspecified")
	}

	peers := make([]netip.Addr, 0, len(service.GetPeers()))
	seen := map[netip.Addr]struct{}{}
	for _, peer := range service.GetPeers() {
		addr, err := peer.ToAddr()
		if err != nil {
			return cunrdup.Service{}, fmt.Errorf("peer: %w", err)
		}

		addr = addr.Unmap()
		if addr.IsUnspecified() {
			return cunrdup.Service{}, errors.New("peer address must not be unspecified")
		}

		if _, ok := seen[addr]; ok {
			return cunrdup.Service{}, fmt.Errorf("peer %s is listed twice", addr)
		}
		seen[addr] = struct{}{}

		if addr.Is4() && !sourceIsSet(sourceV4) {
			return cunrdup.Service{}, fmt.Errorf("peer %s needs source_v4 to be set", addr)
		}
		if addr.Is6() && !sourceIsSet(sourceV6) {
			return cunrdup.Service{}, fmt.Errorf("peer %s needs source_v6 to be set", addr)
		}

		peers = append(peers, addr)
	}

	endpoints := make([]cunrdup.Endpoint, 0, len(service.GetEndpoints()))
	seenEndpoints := map[cunrdup.Endpoint]struct{}{}
	for _, endpoint := range service.GetEndpoints() {
		converted := endpointFromProto(endpoint)

		if _, ok := seenEndpoints[converted]; ok {
			return cunrdup.Service{}, fmt.Errorf("endpoint %d is listed twice", converted.Port)
		}
		seenEndpoints[converted] = struct{}{}

		endpoints = append(endpoints, converted)
	}

	return cunrdup.Service{
		VIP:       vip,
		Peers:     peers,
		Endpoints: endpoints,
	}, nil
}

func endpointFromProto(endpoint *unrduppb.Endpoint) cunrdup.Endpoint {
	var proto uint8
	switch endpoint.GetProtocol() {
	case unrduppb.Protocol_PROTOCOL_TCP:
		proto = ipprotoTCP
	case unrduppb.Protocol_PROTOCOL_UDP:
		proto = ipprotoUDP
	}

	return cunrdup.Endpoint{
		Port:  uint16(endpoint.GetPort()),
		Proto: proto,
	}
}

func sourceV4FromProto(source *commonpb.IPv4Network) (xnetip.Network, error) {
	if source == nil {
		return xnetip.Network{}, nil
	}

	net, err := source.ToNetwork4()
	if err != nil {
		return xnetip.Network{}, fmt.Errorf("source: %w", err)
	}

	return checkSource(xnetip.NetworkFrom4(net))
}

func sourceV6FromProto(source *commonpb.IPv6Network) (xnetip.Network, error) {
	if source == nil {
		return xnetip.Network{}, nil
	}

	net, err := source.ToNetwork6()
	if err != nil {
		return xnetip.Network{}, fmt.Errorf("source: %w", err)
	}
	if net.Addr().Is4In6() {
		return xnetip.Network{}, fmt.Errorf("source %s must not be IPv4-mapped", net)
	}

	return checkSource(xnetip.NetworkFrom6(net))
}

func checkSource(source xnetip.Network) (xnetip.Network, error) {
	prefix, ok := source.Prefix()
	if !ok {
		return xnetip.Network{}, errors.New("source mask must be contiguous")
	}
	if prefix.Bits() == 0 {
		return xnetip.Network{}, errors.New("source mask must not leave the whole address free")
	}
	if source.Addr().IsUnspecified() {
		return xnetip.Network{}, errors.New("source address must not be unspecified")
	}

	return source, nil
}

func (m *config) ToProto() *unrduppb.Config {
	services := make([]*unrduppb.Service, 0, len(m.Services))
	for idx := range m.Services {
		services = append(services, serviceToProto(&m.Services[idx]))
	}

	return &unrduppb.Config{
		SourceV4: sourceV4ToProto(m.SourceV4),
		SourceV6: sourceV6ToProto(m.SourceV6),
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

func sourceV4ToProto(source xnetip.Network) *commonpb.IPv4Network {
	net, ok := source.IPv4()
	if !ok || !sourceIsSet(source) {
		return nil
	}

	return commonpb.NewIPv4NetworkFrom4(net)
}

func sourceV6ToProto(source xnetip.Network) *commonpb.IPv6Network {
	net, ok := source.IPv6()
	if !ok || !sourceIsSet(source) {
		return nil
	}

	return commonpb.NewIPv6NetworkFrom6(net)
}
