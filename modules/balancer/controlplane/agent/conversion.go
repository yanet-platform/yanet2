package balancer

import (
	"fmt"
	"math"
	"net/netip"

	balancerffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// Protobuf to FFI conversions

func NewRealUpdateFromProto(update *balancerpb.RealUpdate) (*balancerffi.RealUpdate, error) {
	if update.Weight > math.MaxUint16 {
		return nil, fmt.Errorf(
			"incorrect real weight: real weight cannot exceed %d",
			math.MaxUint16,
		)
	}
	vip, ok := netip.AddrFromSlice(update.VirtualIp)
	if !ok {
		return nil, fmt.Errorf("incorrect virtual service IP")
	}
	realIp, ok := netip.AddrFromSlice(update.RealIp)
	if !ok {
		return nil, fmt.Errorf("incorrect real ip")
	}

	proto := balancerffi.ProtoUdp
	if update.Proto == balancerpb.TransportProto_TCP {
		proto = balancerffi.ProtoTcp
	}

	weight := uint16(update.Weight)
	enabled := update.Enable

	return &balancerffi.RealUpdate{
		Identifier: balancerffi.RealIdentifier{
			Vs: balancerffi.VsIdentifier{
				Ip:    vip,
				Port:  uint16(update.Port),
				Proto: proto,
			},
			Ip: realIp,
		},
		Weight:  &weight,
		Enabled: &enabled,
	}, nil
}

func ProtoToFFIConfig(
	moduleConfig *balancerpb.ModuleConfig,
	moduleStateConfig *balancerpb.ModuleStateConfig,
) (balancerffi.BalancerConfig, error) {
	handlerConfig, err := ProtoToHandlerConfig(moduleConfig)
	if err != nil {
		return balancerffi.BalancerConfig{}, err
	}

	return balancerffi.BalancerConfig{
		TableSize: uint(moduleStateConfig.SessionTableCapacity),
		Handler:   handlerConfig,
	}, nil
}

func ProtoToHandlerConfig(moduleConfig *balancerpb.ModuleConfig) (balancerffi.PacketHandlerConfig, error) {
	// Convert session timeouts
	timeouts := balancerffi.SessionsTimeouts{}
	if moduleConfig.SessionsTimeouts != nil {
		timeouts = balancerffi.SessionsTimeouts{
			TcpSynAck: moduleConfig.SessionsTimeouts.TcpSynAck,
			TcpSyn:    moduleConfig.SessionsTimeouts.TcpSyn,
			TcpFin:    moduleConfig.SessionsTimeouts.TcpFin,
			Tcp:       moduleConfig.SessionsTimeouts.Tcp,
			Udp:       moduleConfig.SessionsTimeouts.Udp,
			Default:   moduleConfig.SessionsTimeouts.Default,
		}
	}

	// Convert source addresses
	var sourceV4, sourceV6 netip.Addr
	if len(moduleConfig.SourceAddressV4) == 4 {
		sourceV4 = netip.AddrFrom4([4]byte(moduleConfig.SourceAddressV4))
	}
	if len(moduleConfig.SourceAddressV6) == 16 {
		sourceV6 = netip.AddrFrom16([16]byte(moduleConfig.SourceAddressV6))
	}

	// Convert decap addresses
	decapAddrs := make([]netip.Addr, 0, len(moduleConfig.DecapAddresses))
	for _, addrBytes := range moduleConfig.DecapAddresses {
		if addr, ok := netip.AddrFromSlice(addrBytes); ok {
			decapAddrs = append(decapAddrs, addr)
		}
	}

	// Convert virtual services
	virtualServices := make([]balancerffi.VsConfig, 0, len(moduleConfig.VirtualServices))
	for _, protoVs := range moduleConfig.VirtualServices {
		vsConfig, err := protoToVsConfig(protoVs)
		if err != nil {
			return balancerffi.PacketHandlerConfig{}, fmt.Errorf("failed to convert VS: %w", err)
		}
		virtualServices = append(virtualServices, vsConfig)
	}

	return balancerffi.PacketHandlerConfig{
		SessionsTimeouts: timeouts,
		VirtualServices:  virtualServices,
		SourceIPv4:       sourceV4,
		SourceIPv6:       sourceV6,
		DecapAddresses:   decapAddrs,
	}, nil
}

func protoToVsConfig(protoVs *balancerpb.VirtualService) (balancerffi.VsConfig, error) {
	// Convert VS address
	vsAddr, ok := netip.AddrFromSlice(protoVs.Addr)
	if !ok {
		return balancerffi.VsConfig{}, fmt.Errorf("invalid VS address")
	}

	// Convert proto
	var proto balancerffi.VsProto
	if protoVs.Proto == balancerpb.TransportProto_TCP {
		proto = balancerffi.ProtoTcp
	} else {
		proto = balancerffi.ProtoUdp
	}

	// Convert flags
	flags := balancerffi.VsFlags{}
	if protoVs.Flags != nil {
		flags.GRE = protoVs.Flags.Gre
		flags.OPS = protoVs.Flags.Ops
		flags.PureL3 = protoVs.Flags.PureL3
		flags.FixMSS = protoVs.Flags.FixMss
	}

	// Convert scheduler
	var scheduler balancerffi.VsScheduler
	if protoVs.Scheduler == balancerpb.VsScheduler_PRR {
		scheduler = balancerffi.VsSchedulerRoundRobin
	} else {
		scheduler = balancerffi.VsSchedulerSourceHash
	}

	// Convert reals
	reals := make([]balancerffi.RealConfig, 0, len(protoVs.Reals))
	for _, protoReal := range protoVs.Reals {
		realConfig, err := protoToRealConfig(protoReal, vsAddr, uint16(protoVs.Port), proto)
		if err != nil {
			return balancerffi.VsConfig{}, fmt.Errorf("failed to convert real: %w", err)
		}
		reals = append(reals, realConfig)
	}

	// Convert allowed sources
	allowedSrc := make([]netip.Prefix, 0, len(protoVs.AllowedSrcs))
	for _, subnet := range protoVs.AllowedSrcs {
		addr, ok := netip.AddrFromSlice(subnet.Addr)
		if !ok {
			continue
		}
		if prefix, err := addr.Prefix(int(subnet.Size)); err == nil {
			allowedSrc = append(allowedSrc, prefix)
		}
	}

	// Convert peers
	var peersV4, peersV6 []netip.Addr
	for _, peerBytes := range protoVs.Peers {
		if peer, ok := netip.AddrFromSlice(peerBytes); ok {
			if peer.Is4() {
				peersV4 = append(peersV4, peer)
			} else {
				peersV6 = append(peersV6, peer)
			}
		}
	}

	return balancerffi.VsConfig{
		Identifier: balancerffi.VsIdentifier{
			Ip:    vsAddr,
			Port:  uint16(protoVs.Port),
			Proto: proto,
		},
		Flags:      flags,
		Scheduler:  scheduler,
		Reals:      reals,
		AllowedSrc: allowedSrc,
		PeersV4:    peersV4,
		PeersV6:    peersV6,
	}, nil
}

func protoToRealConfig(
	protoReal *balancerpb.Real,
	vsAddr netip.Addr,
	vsPort uint16,
	vsProto balancerffi.VsProto,
) (balancerffi.RealConfig, error) {
	realAddr, ok := netip.AddrFromSlice(protoReal.DstAddr)
	if !ok {
		return balancerffi.RealConfig{}, fmt.Errorf("invalid real address")
	}

	srcAddr, ok := netip.AddrFromSlice(protoReal.SrcAddr)
	if !ok {
		return balancerffi.RealConfig{}, fmt.Errorf("invalid source address")
	}

	srcMask, ok := netip.AddrFromSlice(protoReal.SrcMask)
	if !ok {
		return balancerffi.RealConfig{}, fmt.Errorf("invalid source mask")
	}

	return balancerffi.RealConfig{
		Identifier: balancerffi.RealIdentifier{
			Vs: balancerffi.VsIdentifier{
				Ip:    vsAddr,
				Port:  vsPort,
				Proto: vsProto,
			},
			Ip: realAddr,
		},
		Weight:  uint16(protoReal.Weight),
		SrcAddr: srcAddr,
		SrcMask: srcMask,
	}, nil
}

// FFI to Protobuf conversions

func ConvertFFIProtoToProto(proto balancerffi.VsProto) balancerpb.TransportProto {
	if proto == balancerffi.ProtoTcp {
		return balancerpb.TransportProto_TCP
	}
	return balancerpb.TransportProto_UDP
}

func ConvertBalancerInfoToProto(info *balancerffi.BalancerInfo) *balancerpb.BalancerInfo {
	vsInfo := make([]*balancerpb.VsInfo, 0, len(info.VsInfo))
	for i := range info.VsInfo {
		vsInfo = append(vsInfo, ConvertVsInfoToProto(&info.VsInfo[i]))
	}

	realInfo := make([]*balancerpb.RealInfo, 0, len(info.RealInfo))
	for i := range info.RealInfo {
		realInfo = append(realInfo, ConvertRealInfoToProto(&info.RealInfo[i]))
	}

	return &balancerpb.BalancerInfo{
		ActiveSessions: &balancerpb.AsyncInfo{
			Value:     uint64(info.ActiveSessions.Value),
			UpdatedAt: nil, // timestamp not available in FFI
		},
		Module:   ConvertModuleStatsToProto(&info.Module),
		VsInfo:   vsInfo,
		RealInfo: realInfo,
	}
}

func ConvertVsInfoToProto(info *balancerffi.VsInfo) *balancerpb.VsInfo {
	return &balancerpb.VsInfo{
		VsRegistryIdx: 0, // not available in FFI
		VsIp:          info.VsIdentifier.Ip.AsSlice(),
		VsPort:        uint32(info.VsIdentifier.Port),
		VsProto:       ConvertFFIProtoToProto(info.VsIdentifier.Proto),
		ActiveSessions: &balancerpb.AsyncInfo{
			Value: uint64(info.ActiveSessions.Value),
		},
		LastPacketTimestamp: nil, // convert if needed
		Stats:               ConvertVsStatsToProto(&info.Stats),
	}
}

func ConvertRealInfoToProto(info *balancerffi.RealInfo) *balancerpb.RealInfo {
	return &balancerpb.RealInfo{
		RealRegistryIdx: 0, // not available in FFI
		VsIp:            info.RealIdentifier.Vs.Ip.AsSlice(),
		VsPort:          uint32(info.RealIdentifier.Vs.Port),
		VsProto:         ConvertFFIProtoToProto(info.RealIdentifier.Vs.Proto),
		RealIp:          info.RealIdentifier.Ip.AsSlice(),
		ActiveSessions: &balancerpb.AsyncInfo{
			Value: uint64(info.ActiveSessions.Value),
		},
		LastPacketTimestamp: nil,
		Stats:               ConvertRealStatsToProto(&info.Stats),
	}
}

func ConvertSessionInfoToProto(info *balancerffi.SessionInfo) *balancerpb.SessionInfo {
	return &balancerpb.SessionInfo{
		ClientAddr:          info.ClientAddr.AsSlice(),
		ClientPort:          uint32(info.ClientPort),
		VsAddr:              info.Real.Vs.Ip.AsSlice(),
		VsPort:              uint32(info.Real.Vs.Port),
		RealAddr:            info.Real.Ip.AsSlice(),
		RealPort:            uint32(info.Real.Vs.Port),
		CreateTimestamp:     nil,
		LastPacketTimestamp: nil,
		Timeout:             nil,
	}
}

func ConvertBalancerStatsToProto(info *balancerffi.BalancerInfo) *balancerpb.BalancerStats {
	vsStats := make([]*balancerpb.VsStatsInfo, 0, len(info.VsInfo))
	for i := range info.VsInfo {
		vsStats = append(vsStats, &balancerpb.VsStatsInfo{
			VsRegistryIdx: 0,
			Ip:            info.VsInfo[i].VsIdentifier.Ip.AsSlice(),
			Port:          uint32(info.VsInfo[i].VsIdentifier.Port),
			Proto:         ConvertFFIProtoToProto(info.VsInfo[i].VsIdentifier.Proto),
			Stats:         ConvertVsStatsToProto(&info.VsInfo[i].Stats),
		})
	}

	realStats := make([]*balancerpb.RealStatsInfo, 0, len(info.RealInfo))
	for i := range info.RealInfo {
		realStats = append(realStats, &balancerpb.RealStatsInfo{
			RealRegistryIdx: 0,
			VsIp:            info.RealInfo[i].RealIdentifier.Vs.Ip.AsSlice(),
			Port:            uint32(info.RealInfo[i].RealIdentifier.Vs.Port),
			Proto:           ConvertFFIProtoToProto(info.RealInfo[i].RealIdentifier.Vs.Proto),
			RealIp:          info.RealInfo[i].RealIdentifier.Ip.AsSlice(),
			Stats:           ConvertRealStatsToProto(&info.RealInfo[i].Stats),
		})
	}

	return &balancerpb.BalancerStats{
		Module: ConvertModuleStatsToProto(&info.Module),
		Vs:     vsStats,
		Reals:  realStats,
	}
}

func ConvertModuleStatsToProto(stats *balancerffi.ModuleStats) *balancerpb.ModuleStats {
	return &balancerpb.ModuleStats{
		L4: &balancerpb.L4Stats{
			IncomingPackets:  stats.L4.IncomingPackets,
			SelectVsFailed:   stats.L4.SelectVSFailed,
			InvalidPackets:   stats.L4.InvalidPackets,
			SelectRealFailed: stats.L4.SelectRealFailed,
			OutgoingPackets:  stats.L4.OutgoingPackets,
		},
		Icmpv4: ConvertIcmpStatsToProto(&stats.ICMPv4),
		Icmpv6: ConvertIcmpStatsToProto(&stats.ICMPv6),
		Common: &balancerpb.CommonStats{
			IncomingPackets:        stats.Common.IncomingPackets,
			IncomingBytes:          stats.Common.IncomingBytes,
			UnexpectedNetworkProto: stats.Common.UnexpectedNetworkProto,
			DecapSuccessful:        stats.Common.DecapSuccessful,
			DecapFailed:            stats.Common.DecapFailed,
			OutgoingPackets:        stats.Common.OutgoingPackets,
			OutgoingBytes:          stats.Common.OutgoingBytes,
		},
	}
}

func ConvertIcmpStatsToProto(stats *balancerffi.ICMPStats) *balancerpb.IcmpStats {
	return &balancerpb.IcmpStats{
		IncomingPackets:           stats.IncomingPackets,
		SrcNotAllowed:             stats.SrcNotAllowed,
		EchoResponses:             stats.EchoResponses,
		PayloadTooShortIp:         stats.PayloadTooShortIP,
		UnmatchingSrcFromOriginal: stats.UnmatchingSrcFromOriginal,
		PayloadTooShortPort:       stats.PayloadTooShortPort,
		UnexpectedTransport:       stats.UnexpectedTransport,
		UnrecognizedVs:            stats.UnrecognizedVS,
		ForwardedPackets:          stats.ForwardedPackets,
		BroadcastedPackets:        stats.BroadcastedPackets,
		PacketClonesSent:          stats.PacketClonesSent,
		PacketClonesReceived:      stats.PacketClonesReceived,
		PacketCloneFailures:       stats.PacketCloneFailures,
	}
}

func ConvertVsStatsToProto(stats *balancerffi.VsStats) *balancerpb.VsStats {
	return &balancerpb.VsStats{
		IncomingPackets:        stats.IncomingPackets,
		IncomingBytes:          stats.IncomingBytes,
		PacketSrcNotAllowed:    stats.PacketSrcNotAllowed,
		NoReals:                stats.NoReals,
		OpsPackets:             stats.OpsPackets,
		SessionTableOverflow:   stats.SessionTableOverflow,
		EchoIcmpPackets:        stats.EchoIcmpPackets,
		ErrorIcmpPackets:       stats.ErrorIcmpPackets,
		RealIsDisabled:         stats.RealIsDisabled,
		RealIsRemoved:          stats.RealIsRemoved,
		NotRescheduledPackets:  stats.NotRescheduledPackets,
		BroadcastedIcmpPackets: stats.BroadcastedIcmpPackets,
		CreatedSessions:        stats.CreatedSessions,
		OutgoingPackets:        stats.OutgoingPackets,
		OutgoingBytes:          stats.OutgoingBytes,
	}
}

func ConvertRealStatsToProto(stats *balancerffi.RealStats) *balancerpb.RealStats {
	return &balancerpb.RealStats{
		PacketsRealDisabled:   stats.PacketsRealDisabled,
		PacketsRealNotPresent: stats.PacketsRealNotPresent,
		OpsPackets:            stats.OpsPackets,
		ErrorIcmpPackets:      stats.ErrorIcmpPackets,
		CreatedSessions:       stats.CreatedSessions,
		Packets:               stats.Packets,
		Bytes:                 stats.Bytes,
	}
}
