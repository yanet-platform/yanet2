package fwstate

import (
	"fmt"
	"net/netip"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// SyncConfigToC copies sync settings without applying defaults or
// validation; a malformed scope rule fails the conversion. An absent
// config yields the zero native value.
func SyncConfigToC(syncConfig *fwstatepb.SyncConfig) (cfwstate.SyncConfig, error) {
	if syncConfig == nil {
		return cfwstate.SyncConfig{}, nil
	}

	var nativeConfig cfwstate.SyncConfig
	for _, rule := range syncConfig.GetRules() {
		native, err := syncRuleToC(rule)
		if err != nil {
			return cfwstate.SyncConfig{}, err
		}
		nativeConfig.Rules = append(nativeConfig.Rules, native)
	}
	for _, endpoint := range syncConfig.GetEndpoints() {
		var native cfwstate.SyncEndpoint
		copy(native.SrcAddr[:], endpoint.GetSrcAddr().GetAddr())
		copy(native.DstAddr[:], endpoint.GetDstAddr().GetAddr())
		native.Port = uint16(endpoint.GetPort())
		nativeConfig.Endpoints = append(nativeConfig.Endpoints, native)
	}
	for _, listener := range syncConfig.GetListeners() {
		var native cfwstate.SyncListenEndpoint
		copy(native.DstAddr[:], listener.GetDstAddr().GetAddr())
		native.Port = uint16(listener.GetPort())
		nativeConfig.Listeners = append(nativeConfig.Listeners, native)
	}
	nativeConfig.TcpSynAck = syncConfig.GetTcpSynAck()
	nativeConfig.TcpSyn = syncConfig.GetTcpSyn()
	nativeConfig.TcpFin = syncConfig.GetTcpFin()
	nativeConfig.Tcp = syncConfig.GetTcp()
	nativeConfig.Udp = syncConfig.GetUdp()
	nativeConfig.Default = syncConfig.GetDefault()
	nativeConfig.SyncSuppressTimeout = syncConfig.GetSyncSuppressTimeout()
	nativeConfig.SyncMTU = uint16(syncConfig.GetSyncMtu())
	return nativeConfig, nil
}

// syncRuleToC converts one scope rule into the flat binding form: the
// exact source addresses of both families, the port spans inclusive.
func syncRuleToC(rule *fwstatepb.SyncRule) (cfwstate.SyncRule, error) {
	native := cfwstate.SyncRule{
		Addrs4:   make([][4]byte, 0, len(rule.GetAddrs())),
		Addrs6:   make([][16]byte, 0, len(rule.GetAddrs())),
		SrcPorts: make([][2]uint16, 0, len(rule.GetSrcPorts())),
	}
	for idx, addr := range rule.GetAddrs() {
		addrBytes, ok := netip.AddrFromSlice(addr.GetAddr())
		if !ok {
			return cfwstate.SyncRule{}, fmt.Errorf(
				"addrs[%d]: invalid address bytes",
				idx,
			)
		}
		if addrBytes.Is4() {
			native.Addrs4 = append(native.Addrs4, addrBytes.As4())
			continue
		}
		if addrBytes.Is6() && !addrBytes.Is4In6() {
			native.Addrs6 = append(native.Addrs6, addrBytes.As16())
			continue
		}
		return cfwstate.SyncRule{}, fmt.Errorf(
			"addrs[%d]: not an IPv4 or IPv6 address",
			idx,
		)
	}
	for idx, span := range rule.GetSrcPorts() {
		if span.GetFrom() == 0 || span.GetFrom() > span.GetTo() || span.GetTo() > 65535 {
			return cfwstate.SyncRule{}, fmt.Errorf(
				"src_ports[%d]: from must be in 1..to and to in from..65535",
				idx,
			)
		}
		native.SrcPorts = append(native.SrcPorts, [2]uint16{
			uint16(span.GetFrom()),
			uint16(span.GetTo()),
		})
	}
	return native, nil
}

// syncRuleFromC renders one stored scope rule back into the proto form.
func syncRuleFromC(native cfwstate.SyncRule) *fwstatepb.SyncRule {
	rule := &fwstatepb.SyncRule{
		Addrs:    make([]*commonpb.IPAddress, 0, len(native.Addrs4)+len(native.Addrs6)),
		SrcPorts: make([]*filterpb.PortRange, 0, len(native.SrcPorts)),
	}
	for _, addr := range native.Addrs4 {
		rule.Addrs = append(rule.Addrs, &commonpb.IPAddress{
			Addr: append([]byte(nil), addr[:]...),
		})
	}
	for _, addr := range native.Addrs6 {
		rule.Addrs = append(rule.Addrs, &commonpb.IPAddress{
			Addr: append([]byte(nil), addr[:]...),
		})
	}
	for _, span := range native.SrcPorts {
		rule.SrcPorts = append(rule.SrcPorts, &filterpb.PortRange{
			From: uint32(span[0]),
			To:   uint32(span[1]),
		})
	}
	return rule
}

// SyncConfigFromC makes every scalar present and lists the stored endpoints
// and listeners, skipping the entries a zero port disables.
func SyncConfigFromC(nativeConfig cfwstate.SyncConfig) *fwstatepb.SyncConfig {
	syncConfig := &fwstatepb.SyncConfig{
		Endpoints:           make([]*fwstatepb.SyncEndpoint, 0, len(nativeConfig.Endpoints)),
		Listeners:           make([]*fwstatepb.SyncListenEndpoint, 0, len(nativeConfig.Listeners)),
		TcpSynAck:           new(uint64(nativeConfig.TcpSynAck)),
		TcpSyn:              new(uint64(nativeConfig.TcpSyn)),
		TcpFin:              new(uint64(nativeConfig.TcpFin)),
		Tcp:                 new(uint64(nativeConfig.Tcp)),
		Udp:                 new(uint64(nativeConfig.Udp)),
		Default:             new(uint64(nativeConfig.Default)),
		SyncSuppressTimeout: new(uint64(nativeConfig.SyncSuppressTimeout)),
		SyncMtu:             new(uint32(nativeConfig.SyncMTU)),
	}
	for _, endpoint := range nativeConfig.Endpoints {
		if endpoint.Port == 0 {
			continue
		}
		syncConfig.Endpoints = append(syncConfig.Endpoints, &fwstatepb.SyncEndpoint{
			SrcAddr: &commonpb.IPAddress{Addr: append([]byte(nil), endpoint.SrcAddr[:]...)},
			DstAddr: &commonpb.IPAddress{Addr: append([]byte(nil), endpoint.DstAddr[:]...)},
			Port:    new(uint32(endpoint.Port)),
		})
	}
	for _, rule := range nativeConfig.Rules {
		syncConfig.Rules = append(syncConfig.Rules, syncRuleFromC(rule))
	}
	for _, listener := range nativeConfig.Listeners {
		if listener.Port == 0 {
			continue
		}
		syncConfig.Listeners = append(syncConfig.Listeners, &fwstatepb.SyncListenEndpoint{
			DstAddr: &commonpb.IPAddress{Addr: append([]byte(nil), listener.DstAddr[:]...)},
			Port:    new(uint32(listener.Port)),
		})
	}
	return syncConfig
}
