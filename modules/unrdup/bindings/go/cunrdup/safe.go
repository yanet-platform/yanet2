package cunrdup

//#include "controlplane/config/defines.h"
//#include "modules/unrdup/api/controlplane.h"
import "C"

import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/xnetip"
)

// ModuleNameMaxLen is the longest module name the controlplane registry keeps.
const ModuleNameMaxLen = C.CP_MODULE_NAME_LEN - 1

// Endpoint is a served transport endpoint; its port is in host byte order.
type Endpoint struct {
	Port  uint16
	Proto uint8
}

type Service struct {
	VIP       netip.Addr
	Peers     []netip.Addr
	Endpoints []Endpoint
}

// SetSource sets the outer tunnel source for the network's family.
func (m *ModuleConfig) SetSource(source xnetip.Network) error {
	addr := source.Addr().Unmap()
	mask := source.Mask().Unmap().AsSlice()

	if addr.Is4() {
		if len(mask) != 4 {
			return fmt.Errorf("mask length %d does not match an IPv4 source", len(mask))
		}

		addrBytes := addr.As4()
		maskBytes := [4]byte(mask)

		return m.setSource(
			C.ip_family_ip4,
			(*C.uint8_t)(&addrBytes[0]),
			(*C.uint8_t)(&maskBytes[0]),
		)
	}

	if addr.Is6() {
		if len(mask) != 16 {
			return fmt.Errorf("mask length %d does not match an IPv6 source", len(mask))
		}

		addrBytes := addr.As16()
		maskBytes := [16]byte(mask)

		return m.setSource(
			C.ip_family_ip6,
			(*C.uint8_t)(&addrBytes[0]),
			(*C.uint8_t)(&maskBytes[0]),
		)
	}

	return fmt.Errorf("invalid source address: %s", source.Addr())
}

// UpdateServices replaces the whole service table.
func (m *ModuleConfig) UpdateServices(services []Service) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cServices := make([]C.struct_unrdup_service_config, len(services))
	for idx := range services {
		if err := services[idx].cBuild(&cServices[idx], pinner); err != nil {
			return fmt.Errorf("service %d: %w", idx, err)
		}
	}

	var cServicesPtr *C.struct_unrdup_service_config
	if len(cServices) > 0 {
		cServicesPtr = &cServices[0]
	}

	return m.updateServices(cServicesPtr, C.uint64_t(len(cServices)))
}

func (m *Service) cBuild(cService *C.struct_unrdup_service_config, pinner *runtime.Pinner) error {
	family, err := setAddr(&cService.vip, m.VIP)
	if err != nil {
		return fmt.Errorf("vip: %w", err)
	}
	cService.family = family

	if len(m.Peers) > 0 {
		cPeers := make([]C.struct_unrdup_peer_config, len(m.Peers))
		for idx := range m.Peers {
			family, err := setAddr(&cPeers[idx].addr, m.Peers[idx])
			if err != nil {
				return fmt.Errorf("peer %d: %w", idx, err)
			}
			cPeers[idx].family = family
		}

		pinner.Pin(&cPeers[0])
		cService.peers = &cPeers[0]
		cService.peer_count = C.uint64_t(len(cPeers))
	}

	if len(m.Endpoints) > 0 {
		cEndpoints := make([]C.struct_unrdup_port_config, len(m.Endpoints))
		for idx := range m.Endpoints {
			cEndpoints[idx].port = C.uint16_t(m.Endpoints[idx].Port)
			cEndpoints[idx].proto = C.uint8_t(m.Endpoints[idx].Proto)
		}

		pinner.Pin(&cEndpoints[0])
		cService.ports = &cEndpoints[0]
		cService.port_count = C.uint64_t(len(cEndpoints))
	}

	return nil
}

func setAddr(dst *C.struct_net_addr, addr netip.Addr) (C.enum_ip_family, error) {
	addr = addr.Unmap()
	bytes := (*[16]byte)(unsafe.Pointer(dst))[:]

	if addr.Is4() {
		addrBytes := addr.As4()
		copy(bytes, addrBytes[:])

		return C.ip_family_ip4, nil
	}

	if addr.Is6() {
		addrBytes := addr.As16()
		copy(bytes, addrBytes[:])

		return C.ip_family_ip6, nil
	}

	return 0, fmt.Errorf("invalid address: %s", addr)
}
