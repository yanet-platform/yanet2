package fwstate

import (
	"google.golang.org/protobuf/proto"

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
	sourceAddress := syncConfig.GetSrcAddr().GetAddr()
	copy(nativeConfig.SrcAddr[:], sourceAddress)
	destinationEther := syncConfig.GetDstEther().EUI48()
	copy(nativeConfig.DstEther[:], destinationEther[:])
	copy(nativeConfig.DstAddrMulticast[:], syncConfig.GetDstAddrMulticast().GetAddr())
	nativeConfig.PortMulticast = uint16(syncConfig.GetPortMulticast())
	copy(nativeConfig.DstAddrUnicast[:], syncConfig.GetDstAddrUnicast().GetAddr())
	nativeConfig.PortUnicast = uint16(syncConfig.GetPortUnicast())
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

// SyncConfigFromC makes every scalar present and retains destination addresses
// only when their ports are nonzero.
func SyncConfigFromC(nativeConfig cfwstate.SyncConfig) *fwstatepb.SyncConfig {
	syncConfig := &fwstatepb.SyncConfig{
		SrcAddr:             &commonpb.IPAddress{Addr: append([]byte(nil), nativeConfig.SrcAddr[:]...)},
		DstEther:            commonpb.NewMACAddressEUI48(nativeConfig.DstEther),
		PortMulticast:       proto.Uint32(uint32(nativeConfig.PortMulticast)),
		PortUnicast:         proto.Uint32(uint32(nativeConfig.PortUnicast)),
		TcpSynAck:           proto.Uint64(nativeConfig.TcpSynAck),
		TcpSyn:              proto.Uint64(nativeConfig.TcpSyn),
		TcpFin:              proto.Uint64(nativeConfig.TcpFin),
		Tcp:                 proto.Uint64(nativeConfig.Tcp),
		Udp:                 proto.Uint64(nativeConfig.Udp),
		Default:             proto.Uint64(nativeConfig.Default),
		SyncSuppressTimeout: proto.Uint64(nativeConfig.SyncSuppressTimeout),
		SyncMtu:             proto.Uint32(uint32(nativeConfig.SyncMTU)),
	}
	if nativeConfig.PortMulticast != 0 {
		syncConfig.DstAddrMulticast = &commonpb.IPAddress{Addr: append([]byte(nil), nativeConfig.DstAddrMulticast[:]...)}
	}
	if nativeConfig.PortUnicast != 0 {
		syncConfig.DstAddrUnicast = &commonpb.IPAddress{Addr: append([]byte(nil), nativeConfig.DstAddrUnicast[:]...)}
	}
	return syncConfig
}
