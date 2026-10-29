package fwstate

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// SyncConfigToC copies sync settings without applying defaults or validation.
// An absent config yields the zero native value.
func SyncConfigToC(syncConfig *fwstatepb.SyncConfig) cfwstate.SyncConfig {
	if syncConfig == nil {
		return cfwstate.SyncConfig{}
	}

	var nativeConfig cfwstate.SyncConfig
	for _, endpoint := range syncConfig.GetEndpoints() {
		var native cfwstate.SyncEndpoint
		copy(native.SrcAddr[:], endpoint.GetSrcAddr().GetAddr())
		copy(native.DstAddr[:], endpoint.GetDstAddr().GetAddr())
		native.Port = uint16(endpoint.GetPort())
		nativeConfig.Endpoints = append(nativeConfig.Endpoints, native)
	}
	nativeConfig.TcpSynAck = syncConfig.GetTcpSynAck()
	nativeConfig.TcpSyn = syncConfig.GetTcpSyn()
	nativeConfig.TcpFin = syncConfig.GetTcpFin()
	nativeConfig.Tcp = syncConfig.GetTcp()
	nativeConfig.Udp = syncConfig.GetUdp()
	nativeConfig.Default = syncConfig.GetDefault()
	nativeConfig.SyncSuppressTimeout = syncConfig.GetSyncSuppressTimeout()
	nativeConfig.SyncMTU = uint16(syncConfig.GetSyncMtu())
	return nativeConfig
}

// SyncConfigFromC makes every scalar present and lists the stored endpoints,
// skipping the entries a zero port disables.
func SyncConfigFromC(nativeConfig cfwstate.SyncConfig) *fwstatepb.SyncConfig {
	syncConfig := &fwstatepb.SyncConfig{
		Endpoints:           make([]*fwstatepb.SyncEndpoint, 0, len(nativeConfig.Endpoints)),
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
			Port:    new(uint32(uint32(endpoint.Port))),
		})
	}
	return syncConfig
}
