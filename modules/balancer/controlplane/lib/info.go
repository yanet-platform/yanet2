package lib

import (
	"fmt"
	"math"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

////////////////////////////////////////////////////////////////////////////////

// Config-independent info about real.
type RealInfo struct {
	// Index of the real in balancer module state registry.
	RealRegistryIdx uint

	// Real of the virtual service
	// for which real is related to.
	RealIdentifier RealIdentifier

	// Number of active sessions with this real.
	ActiveSessions AsyncInfo

	// Timestamp of the last packet.
	LastPacketTimestamp time.Time

	// Config-independent statistics of real.
	Stats RealStats
}

// IntoProto converts RealInfo to protobuf message with flattened identifier fields.
func (i RealInfo) IntoProto() *balancerpb.RealInfo {
	return &balancerpb.RealInfo{
		RealRegistryIdx:     uint32(i.RealRegistryIdx),
		VsIp:                i.RealIdentifier.Vs.Ip.AsSlice(),
		VsPort:              uint32(i.RealIdentifier.Vs.Port),
		VsProto:             i.RealIdentifier.Vs.Proto.IntoProto(),
		RealIp:              i.RealIdentifier.Ip.AsSlice(),
		ActiveSessions:      i.ActiveSessions.IntoProto(),
		LastPacketTimestamp: timestamppb.New(i.LastPacketTimestamp),
		Stats:               i.Stats.IntoProto(),
	}
}

// NewRealInfoFromProto creates RealInfo from protobuf message with validation.
func NewRealInfoFromProto(pb *balancerpb.RealInfo) (*RealInfo, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil RealInfo message")
	}

	vsIp, ok := netip.AddrFromSlice(pb.VsIp)
	if !ok {
		return nil, fmt.Errorf("invalid vs_ip bytes")
	}

	if pb.VsPort > math.MaxUint16 {
		return nil, fmt.Errorf("vs_port out of range: %d", pb.VsPort)
	}

	realIp, ok := netip.AddrFromSlice(pb.RealIp)
	if !ok {
		return nil, fmt.Errorf("invalid real_ip bytes")
	}

	vsId := VsIdentifier{
		Ip:    vsIp,
		Port:  uint16(pb.VsPort),
		Proto: NewProtoFromProto(pb.VsProto),
	}

	realId := RealIdentifier{
		Vs: vsId,
		Ip: realIp,
	}

	var lastPacketTime time.Time
	if pb.LastPacketTimestamp != nil {
		lastPacketTime = pb.LastPacketTimestamp.AsTime()
	}

	return &RealInfo{
		RealRegistryIdx:     uint(pb.RealRegistryIdx),
		RealIdentifier:      realId,
		ActiveSessions:      *NewAsyncInfoFromProto(pb.ActiveSessions),
		LastPacketTimestamp: lastPacketTime,
		Stats:               *NewRealStatsFromProto(pb.Stats),
	}, nil
}

// NewRealStatsFromProto creates RealStats from protobuf message.
func NewRealStatsFromProto(pb *balancerpb.RealStats) *RealStats {
	if pb == nil {
		return &RealStats{}
	}
	return &RealStats{
		PacketsRealDisabled:   pb.PacketsRealDisabled,
		PacketsRealNotPresent: pb.PacketsRealNotPresent,
		OpsPackets:            pb.OpsPackets,
		ErrorIcmpPackets:      pb.ErrorIcmpPackets,
		CreatedSessions:       pb.CreatedSessions,
		Packets:               pb.Packets,
		Bytes:                 pb.Bytes,
	}
}

// Config-independent info about virtual service.
type VsInfo struct {
	// Index of the virtual service in balancer module state registry.
	VsRegistryIdx uint

	// Identifier of the virtual service.
	VsIdentifier VsIdentifier

	// Number of active sessions established with
	// virtual service.
	ActiveSessions AsyncInfo

	// Timestamp of the last packet.
	LastPacketTimestamp time.Time

	// Config-independent statistics of virtual service.
	Stats VsStats
}

// IntoProto converts VsInfo to protobuf message with flattened identifier fields.
func (i VsInfo) IntoProto() *balancerpb.VsInfo {
	return &balancerpb.VsInfo{
		VsRegistryIdx:       uint32(i.VsRegistryIdx),
		VsIp:                i.VsIdentifier.Ip.AsSlice(),
		VsPort:              uint32(i.VsIdentifier.Port),
		VsProto:             i.VsIdentifier.Proto.IntoProto(),
		ActiveSessions:      i.ActiveSessions.IntoProto(),
		LastPacketTimestamp: timestamppb.New(i.LastPacketTimestamp),
		Stats:               i.Stats.IntoProto(),
	}
}

// NewVsInfoFromProto creates VsInfo from protobuf message with validation.
func NewVsInfoFromProto(pb *balancerpb.VsInfo) (*VsInfo, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil VsInfo message")
	}

	vsIp, ok := netip.AddrFromSlice(pb.VsIp)
	if !ok {
		return nil, fmt.Errorf("invalid vs_ip bytes")
	}

	if pb.VsPort > math.MaxUint16 {
		return nil, fmt.Errorf("vs_port out of range: %d", pb.VsPort)
	}

	vsId := VsIdentifier{
		Ip:    vsIp,
		Port:  uint16(pb.VsPort),
		Proto: NewProtoFromProto(pb.VsProto),
	}

	var lastPacketTime time.Time
	if pb.LastPacketTimestamp != nil {
		lastPacketTime = pb.LastPacketTimestamp.AsTime()
	}

	return &VsInfo{
		VsRegistryIdx:       uint(pb.VsRegistryIdx),
		VsIdentifier:        vsId,
		ActiveSessions:      *NewAsyncInfoFromProto(pb.ActiveSessions),
		LastPacketTimestamp: lastPacketTime,
		Stats:               *NewVsStatsFromProto(pb.Stats),
	}, nil
}

// NewVsStatsFromProto creates VsStats from protobuf message.
func NewVsStatsFromProto(pb *balancerpb.VsStats) *VsStats {
	if pb == nil {
		return &VsStats{}
	}
	return &VsStats{
		IncomingPackets:        pb.IncomingPackets,
		IncomingBytes:          pb.IncomingBytes,
		PacketSrcNotAllowed:    pb.PacketSrcNotAllowed,
		NoReals:                pb.NoReals,
		OpsPackets:             pb.OpsPackets,
		SessionTableOverflow:   pb.SessionTableOverflow,
		EchoIcmpPackets:        pb.EchoIcmpPackets,
		ErrorIcmpPackets:       pb.ErrorIcmpPackets,
		RealIsDisabled:         pb.RealIsDisabled,
		RealIsRemoved:          pb.RealIsRemoved,
		NotRescheduledPackets:  pb.NotRescheduledPackets,
		BroadcastedIcmpPackets: pb.BroadcastedIcmpPackets,
		CreatedSessions:        pb.CreatedSessions,
		OutgoingPackets:        pb.OutgoingPackets,
		OutgoingBytes:          pb.OutgoingBytes,
	}
}

// Config-independent state of the balancer.
type BalancerInfo struct {
	ActiveSessions AsyncInfo
	Module         ModuleStats
	VsInfo         []VsInfo
	RealInfo       []RealInfo
}

// IntoProto converts BalancerInfo to protobuf message.
func (i BalancerInfo) IntoProto() *balancerpb.BalancerInfo {
	vsInfo := make([]*balancerpb.VsInfo, 0, len(i.VsInfo))
	for idx := range i.VsInfo {
		vsInfo = append(vsInfo, i.VsInfo[idx].IntoProto())
	}

	realInfo := make([]*balancerpb.RealInfo, 0, len(i.RealInfo))
	for idx := range i.RealInfo {
		realInfo = append(realInfo, i.RealInfo[idx].IntoProto())
	}

	return &balancerpb.BalancerInfo{
		ActiveSessions: i.ActiveSessions.IntoProto(),
		Module:         i.Module.IntoProto(),
		VsInfo:         vsInfo,
		RealInfo:       realInfo,
	}
}

// NewBalancerInfoFromProto creates BalancerInfo from protobuf message with validation.
func NewBalancerInfoFromProto(
	pb *balancerpb.BalancerInfo,
) (*BalancerInfo, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil BalancerInfo message")
	}

	moduleStats := NewModuleStatsFromProto(pb.Module)

	vsInfo := make([]VsInfo, 0, len(pb.VsInfo))
	for idx, vsPb := range pb.VsInfo {
		vs, err := NewVsInfoFromProto(vsPb)
		if err != nil {
			return nil, fmt.Errorf("invalid vs_info[%d]: %w", idx, err)
		}
		vsInfo = append(vsInfo, *vs)
	}

	realInfo := make([]RealInfo, 0, len(pb.RealInfo))
	for idx, realPb := range pb.RealInfo {
		real, err := NewRealInfoFromProto(realPb)
		if err != nil {
			return nil, fmt.Errorf("invalid real_info[%d]: %w", idx, err)
		}
		realInfo = append(realInfo, *real)
	}

	return &BalancerInfo{
		Module:   *moduleStats,
		VsInfo:   vsInfo,
		RealInfo: realInfo,
	}, nil
}

// NewModuleStatsFromProto creates ModuleStats from protobuf message.
func NewModuleStatsFromProto(pb *balancerpb.ModuleStats) *ModuleStats {
	if pb == nil {
		return &ModuleStats{}
	}
	return &ModuleStats{
		L4:     *NewL4StatsFromProto(pb.L4),
		ICMPv4: *NewICMPStatsFromProto(pb.Icmpv4),
		ICMPv6: *NewICMPStatsFromProto(pb.Icmpv6),
		Common: *NewCommonStatsFromProto(pb.Common),
	}
}

// NewL4StatsFromProto creates L4Stats from protobuf message.
func NewL4StatsFromProto(pb *balancerpb.L4Stats) *L4Stats {
	if pb == nil {
		return &L4Stats{}
	}
	return &L4Stats{
		IncomingPackets:  pb.IncomingPackets,
		SelectVSFailed:   pb.SelectVsFailed,
		InvalidPackets:   pb.InvalidPackets,
		SelectRealFailed: pb.SelectRealFailed,
		OutgoingPackets:  pb.OutgoingPackets,
	}
}

// NewICMPStatsFromProto creates ICMPStats from protobuf message.
func NewICMPStatsFromProto(pb *balancerpb.IcmpStats) *ICMPStats {
	if pb == nil {
		return &ICMPStats{}
	}
	return &ICMPStats{
		IncomingPackets:           pb.IncomingPackets,
		EchoResponses:             pb.EchoResponses,
		PayloadTooShortIP:         pb.PayloadTooShortIp,
		UnmatchingSrcFromOriginal: pb.UnmatchingSrcFromOriginal,
		PayloadTooShortPort:       pb.PayloadTooShortPort,
		UnexpectedTransport:       pb.UnexpectedTransport,
		UnrecognizedVS:            pb.UnrecognizedVs,
		ForwardedPackets:          pb.ForwardedPackets,
		BroadcastedPackets:        pb.BroadcastedPackets,
		PacketClonesSent:          pb.PacketClonesSent,
		PacketClonesReceived:      pb.PacketClonesReceived,
		PacketCloneFailures:       pb.PacketCloneFailures,
	}
}

// NewCommonStatsFromProto creates CommonStats from protobuf message.
func NewCommonStatsFromProto(pb *balancerpb.CommonStats) *CommonStats {
	if pb == nil {
		return &CommonStats{}
	}
	return &CommonStats{
		IncomingPackets:        pb.IncomingPackets,
		IncomingBytes:          pb.IncomingBytes,
		UnexpectedNetworkProto: pb.UnexpectedNetworkProto,
		DecapSuccessful:        pb.DecapSuccessful,
		DecapFailed:            pb.DecapFailed,
		OutgoingPackets:        pb.OutgoingPackets,
		OutgoingBytes:          pb.OutgoingBytes,
	}
}

////////////////////////////////////////////////////////////////////////////////

// Represents info about session
type SessionInfo struct {
	ClientAddr          netip.Addr
	ClientPort          uint16
	Real                RealIdentifier
	CreateTimestamp     time.Time
	LastPacketTimestamp time.Time
	Timeout             time.Duration
}

// IntoProto converts SessionInfo to protobuf message
func (s SessionInfo) IntoProto() *balancerpb.SessionInfo {
	return &balancerpb.SessionInfo{
		ClientAddr:          s.ClientAddr.AsSlice(),
		ClientPort:          uint32(s.ClientPort),
		VsAddr:              s.Real.Vs.Ip.AsSlice(),
		VsPort:              uint32(s.Real.Vs.Port),
		RealAddr:            s.Real.Ip.AsSlice(),
		RealPort:            uint32(s.Real.Vs.Port),
		CreateTimestamp:     timestamppb.New(s.CreateTimestamp),
		LastPacketTimestamp: timestamppb.New(s.LastPacketTimestamp),
		Timeout:             durationpb.New(s.Timeout),
	}
}

// Info about active sessions
type SessionsInfo struct {
	// Number of active sessions
	SessionsCount uint

	// May be empty if only sessions count
	// was requested. Else, its len equals
	// to `SessionsCount`.
	Sessions []SessionInfo
}

////////////////////////////////////////////////////////////////////////////////

// Info about value which we update asynchronously
type AsyncInfo struct {
	Value     uint
	UpdatedAt time.Time
}

func (info AsyncInfo) IntoProto() *balancerpb.AsyncInfo {
	return &balancerpb.AsyncInfo{
		Value:     uint64(info.Value),
		UpdatedAt: timestamppb.New(info.UpdatedAt),
	}
}

// NewAsyncInfoFromProto creates AsyncInfo from protobuf message.
func NewAsyncInfoFromProto(pb *balancerpb.AsyncInfo) *AsyncInfo {
	if pb == nil {
		return &AsyncInfo{}
	}
	var updatedAt time.Time
	if pb.UpdatedAt != nil {
		updatedAt = pb.UpdatedAt.AsTime()
	}
	return &AsyncInfo{
		Value:     uint(pb.Value),
		UpdatedAt: updatedAt,
	}
}
