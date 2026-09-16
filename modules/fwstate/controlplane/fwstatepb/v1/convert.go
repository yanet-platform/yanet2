package fwstatepb

import (
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

func (m *SyncConfig) ToC() cfwstate.SyncConfig {
	if m == nil {
		return cfwstate.SyncConfig{}
	}

	var cfg cfwstate.SyncConfig
	src := m.GetSrcAddr().GetAddr()
	copy(cfg.SrcAddr[:], src)
	dstEther := m.GetDstEther().EUI48()
	copy(cfg.DstEther[:], dstEther[:])
	copy(cfg.DstAddrMulticast[:], m.GetDstAddrMulticast().GetAddr())
	cfg.PortMulticast = uint16(m.GetPortMulticast())
	copy(cfg.DstAddrUnicast[:], m.GetDstAddrUnicast().GetAddr())
	cfg.PortUnicast = uint16(m.GetPortUnicast())
	cfg.TcpSynAck = m.GetTcpSynAck()
	cfg.TcpSyn = m.GetTcpSyn()
	cfg.TcpFin = m.GetTcpFin()
	cfg.Tcp = m.GetTcp()
	cfg.Udp = m.GetUdp()
	cfg.Default = m.GetDefault()
	cfg.SyncSuppressTimeout = m.GetSyncSuppressTimeout()
	return cfg
}

// Merge overwrites the settings the update carries and keeps the rest.
//
// A zero port disables its endpoint, so the endpoint address is cleared too.
func (m *SyncConfig) Merge(update *SyncConfig) {
	if update == nil {
		return
	}

	proto.Merge(m, update)
	if update.PortMulticast != nil && update.GetPortMulticast() == 0 {
		m.DstAddrMulticast = nil
	}
	if update.PortUnicast != nil && update.GetPortUnicast() == 0 {
		m.DstAddrUnicast = nil
	}
}

func FromCSyncConfig(cfg cfwstate.SyncConfig) *SyncConfig {
	pb := &SyncConfig{
		SrcAddr:             &commonpb.IPAddress{Addr: append([]byte(nil), cfg.SrcAddr[:]...)},
		DstEther:            commonpb.NewMACAddressEUI48(cfg.DstEther),
		PortMulticast:       proto.Uint32(uint32(cfg.PortMulticast)),
		PortUnicast:         proto.Uint32(uint32(cfg.PortUnicast)),
		TcpSynAck:           proto.Uint64(cfg.TcpSynAck),
		TcpSyn:              proto.Uint64(cfg.TcpSyn),
		TcpFin:              proto.Uint64(cfg.TcpFin),
		Tcp:                 proto.Uint64(cfg.Tcp),
		Udp:                 proto.Uint64(cfg.Udp),
		Default:             proto.Uint64(cfg.Default),
		SyncSuppressTimeout: proto.Uint64(cfg.SyncSuppressTimeout),
	}
	if cfg.PortMulticast != 0 {
		pb.DstAddrMulticast = &commonpb.IPAddress{Addr: append([]byte(nil), cfg.DstAddrMulticast[:]...)}
	}
	if cfg.PortUnicast != 0 {
		pb.DstAddrUnicast = &commonpb.IPAddress{Addr: append([]byte(nil), cfg.DstAddrUnicast[:]...)}
	}
	return pb
}
