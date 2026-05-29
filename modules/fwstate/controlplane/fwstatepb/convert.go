package fwstatepb

import (
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

func (m *MapConfig) ToC() cfwstate.MapConfig {
	if m == nil {
		return cfwstate.MapConfig{}
	}
	return cfwstate.MapConfig{
		IndexSize:        m.GetIndexSize(),
		ExtraBucketCount: m.GetExtraBucketCount(),
	}
}

func FromCMapConfig(cfg cfwstate.MapConfig) *MapConfig {
	return &MapConfig{
		IndexSize:        cfg.IndexSize,
		ExtraBucketCount: cfg.ExtraBucketCount,
	}
}

func (m *SyncConfig) ToC() cfwstate.SyncConfig {
	if m == nil {
		return cfwstate.SyncConfig{}
	}

	var cfg cfwstate.SyncConfig
	src := m.GetSrcAddr().GetAddr()
	copy(cfg.SrcAddr[:], src)
	copy(cfg.DstEther[:], m.GetDstEther())
	copy(cfg.DstAddrMulticast[:], m.GetDstAddrMulticast().GetAddr())
	copy(cfg.DstAddrUnicast[:], m.GetDstAddrUnicast().GetAddr())
	cfg.PortMulticast = uint16(m.GetPortMulticast())
	cfg.PortUnicast = uint16(m.GetPortUnicast())
	cfg.TcpSynAck = m.GetTcpSynAck()
	cfg.TcpSyn = m.GetTcpSyn()
	cfg.TcpFin = m.GetTcpFin()
	cfg.Tcp = m.GetTcp()
	cfg.Udp = m.GetUdp()
	cfg.Default = m.GetDefault()
	return cfg
}

func (m *SyncConfig) ToCWithDefaults(current cfwstate.SyncConfig) cfwstate.SyncConfig {
	cfg := m.ToC()
	if m == nil {
		return cfg
	}
	pbPortMulticast := m.GetPortMulticast()
	pbPortUnicast := m.GetPortUnicast()

	if len(m.GetSrcAddr().GetAddr()) == 0 {
		cfg.SrcAddr = current.SrcAddr
	}
	if len(m.GetDstEther()) == 0 {
		cfg.DstEther = current.DstEther
	}
	if len(m.GetDstAddrMulticast().GetAddr()) == 0 {
		cfg.DstAddrMulticast = current.DstAddrMulticast
	}
	if len(m.GetDstAddrUnicast().GetAddr()) == 0 {
		cfg.DstAddrUnicast = current.DstAddrUnicast
	}
	if pbPortMulticast == 0 {
		cfg.PortMulticast = current.PortMulticast
	}
	if pbPortUnicast == 0 {
		cfg.PortUnicast = current.PortUnicast
	}
	if cfg.TcpSynAck == 0 {
		cfg.TcpSynAck = current.TcpSynAck
	}
	if cfg.TcpSyn == 0 {
		cfg.TcpSyn = current.TcpSyn
	}
	if cfg.TcpFin == 0 {
		cfg.TcpFin = current.TcpFin
	}
	if cfg.Tcp == 0 {
		cfg.Tcp = current.Tcp
	}
	if cfg.Udp == 0 {
		cfg.Udp = current.Udp
	}
	if cfg.Default == 0 {
		cfg.Default = current.Default
	}

	return cfg
}

func FromCSyncConfig(cfg cfwstate.SyncConfig) *SyncConfig {
	return &SyncConfig{
		SrcAddr:          &commonpb.IPAddress{Addr: append([]byte(nil), cfg.SrcAddr[:]...)},
		DstEther:         append([]byte(nil), cfg.DstEther[:]...),
		DstAddrMulticast: &commonpb.IPAddress{Addr: append([]byte(nil), cfg.DstAddrMulticast[:]...)},
		PortMulticast:    uint32(cfg.PortMulticast),
		DstAddrUnicast:   &commonpb.IPAddress{Addr: append([]byte(nil), cfg.DstAddrUnicast[:]...)},
		PortUnicast:      uint32(cfg.PortUnicast),
		TcpSynAck:        cfg.TcpSynAck,
		TcpSyn:           cfg.TcpSyn,
		TcpFin:           cfg.TcpFin,
		Tcp:              cfg.Tcp,
		Udp:              cfg.Udp,
		Default:          cfg.Default,
	}
}

func FromCursorKey(key cfwstate.StateKey) *FwStateKey {
	return &FwStateKey{
		Proto:   key.Proto,
		SrcPort: key.SrcPort,
		DstPort: key.DstPort,
		SrcAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.SrcAddr...)},
		DstAddr: &commonpb.IPAddress{Addr: append([]byte(nil), key.DstAddr...)},
	}
}

func FromCursorValue(value cfwstate.StateValue) *FwStateValue {
	return &FwStateValue{
		External:        value.External,
		Flags:           value.Flags,
		CreatedAt:       value.CreatedAt,
		UpdatedAt:       value.UpdatedAt,
		PacketsBackward: value.PacketsBackward,
		PacketsForward:  value.PacketsForward,
	}
}

func FromCursorEntry(entry cfwstate.CursorEntry) *FwStateEntry {
	return &FwStateEntry{
		Key:     FromCursorKey(entry.Key),
		Value:   FromCursorValue(entry.Value),
		Idx:     entry.Idx,
		Expired: entry.Expired,
	}
}
