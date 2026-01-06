package balancer

import (
	"fmt"
	"net/netip"

	balancerffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// Protobuf to FFI conversions

func NewRealUpdateFromProto(
	update *balancerpb.RealUpdate,
) (*balancerffi.RealUpdate, error) {
	if update.RealId == nil || update.RealId.Vs == nil ||
		update.RealId.Real == nil {
		return nil, fmt.Errorf("incomplete real identifier in update")
	}

	if update.Weight != nil {
		if *update.Weight > uint32(balancerffi.MaxRealWeight) {
			return nil, fmt.Errorf(
				"incorrect real weight: real weight cannot exceed %d",
				balancerffi.MaxRealWeight,
			)
		}
	}

	vip, ok := netip.AddrFromSlice(update.RealId.Vs.Addr.Bytes)
	if !ok {
		return nil, fmt.Errorf("incorrect virtual service IP")
	}
	realIp, ok := netip.AddrFromSlice(update.RealId.Real.Ip.Bytes)
	if !ok {
		return nil, fmt.Errorf("incorrect real ip")
	}

	proto := balancerffi.ProtoUdp
	if update.RealId.Vs.Proto == balancerpb.TransportProto_TCP {
		proto = balancerffi.ProtoTcp
	}

	var weight *uint16
	if update.Weight != nil {
		w := uint16(*update.Weight)
		weight = &w
	}

	var enabled *bool
	if update.Enable != nil {
		enabled = update.Enable
	}

	realPort := uint16(update.RealId.Vs.Port) // Default to VS port
	if update.RealId.Real.Port != 0 {
		realPort = uint16(update.RealId.Real.Port)
	}

	return &balancerffi.RealUpdate{
		Identifier: balancerffi.RealIdentifier{
			Vs: balancerffi.VsIdentifier{
				Ip:    vip,
				Port:  uint16(update.RealId.Vs.Port),
				Proto: proto,
			},
			Relative: balancerffi.RelativeRealIdentifier{
				Ip:   realIp,
				Port: realPort,
			},
		},
		Weight:  weight,
		Enabled: enabled,
	}, nil
}

func ProtoToFFIConfig(
	config *balancerpb.BalancerConfig,
) (balancerffi.BalancerConfig, error) {
	if config.PacketHandler == nil {
		return balancerffi.BalancerConfig{}, fmt.Errorf(
			"packet_handler is required in CREATE mode",
		)
	}
	if config.State == nil {
		return balancerffi.BalancerConfig{}, fmt.Errorf(
			"state config is required in CREATE mode",
		)
	}
	if config.State.SessionTableCapacity == nil {
		return balancerffi.BalancerConfig{}, fmt.Errorf(
			"session_table_capacity is required in CREATE mode",
		)
	}
	if config.State.SessionTableMaxLoadFactor == nil {
		return balancerffi.BalancerConfig{}, fmt.Errorf(
			"session_table_max_load_factor is required in CREATE mode",
		)
	}
	if config.State.RefreshPeriod == nil {
		return balancerffi.BalancerConfig{}, fmt.Errorf(
			"refresh_period is required in CREATE mode",
		)
	}

	handlerConfig, err := ProtoToHandlerConfig(config.PacketHandler)
	if err != nil {
		return balancerffi.BalancerConfig{}, err
	}

	return balancerffi.BalancerConfig{
		State:   balancerffi.StateConfig{SessionTableCapacity: uint(*config.State.SessionTableCapacity)},
		Handler: handlerConfig,
	}, nil
}

func ProtoToHandlerConfig(
	config *balancerpb.PacketHandlerConfig,
) (balancerffi.PacketHandlerConfig, error) {
	// Validate required fields (non-optional in UPDATE mode)
	if config.SessionsTimeouts == nil {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"sessions_timeouts is required",
		)
	}
	if config.SourceAddressV4 == nil {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v4 is required",
		)
	}
	if config.SourceAddressV6 == nil {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v6 is required",
		)
	}
	if config.DecapAddresses == nil {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"decap_addresses is required (can be empty list)",
		)
	}
	if config.Vs == nil {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"vs (virtual services) is required",
		)
	}

	// Convert session timeouts
	timeouts := balancerffi.SessionsTimeouts{
		TcpSynAck: config.SessionsTimeouts.TcpSynAck,
		TcpSyn:    config.SessionsTimeouts.TcpSyn,
		TcpFin:    config.SessionsTimeouts.TcpFin,
		Tcp:       config.SessionsTimeouts.Tcp,
		Udp:       config.SessionsTimeouts.Udp,
		Default:   config.SessionsTimeouts.Default,
	}

	// Convert source addresses
	var sourceV4, sourceV6 netip.Addr
	if len(config.SourceAddressV4.Bytes) == 4 {
		sourceV4 = netip.AddrFrom4([4]byte(config.SourceAddressV4.Bytes))
	} else {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v4 must be a valid IPv4 address",
		)
	}
	if len(config.SourceAddressV6.Bytes) == 16 {
		sourceV6 = netip.AddrFrom16([16]byte(config.SourceAddressV6.Bytes))
	} else {
		return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v6 must be a valid IPv6 address",
		)
	}

	// Convert decap addresses
	decapAddrs := make([]netip.Addr, 0, len(config.DecapAddresses))
	for _, addrMsg := range config.DecapAddresses {
		if addrMsg != nil {
			if addr, ok := netip.AddrFromSlice(addrMsg.Bytes); ok {
				decapAddrs = append(decapAddrs, addr)
			}
		}
	}

	// Convert virtual services
	virtualServices := make([]balancerffi.VsConfig, 0, len(config.Vs))
	for _, protoVs := range config.Vs {
		vsConfig, err := protoToVsConfig(protoVs)
		if err != nil {
			return balancerffi.PacketHandlerConfig{}, fmt.Errorf(
				"failed to convert VS: %w",
				err,
			)
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

func protoToVsConfig(
	protoVs *balancerpb.VirtualService,
) (balancerffi.VsConfig, error) {
	if protoVs.Id == nil || protoVs.Id.Addr == nil {
		return balancerffi.VsConfig{}, fmt.Errorf("invalid VS identifier")
	}

	// Convert VS address
	vsAddr, ok := netip.AddrFromSlice(protoVs.Id.Addr.Bytes)
	if !ok {
		return balancerffi.VsConfig{}, fmt.Errorf("invalid VS address")
	}

	// Convert proto
	var proto balancerffi.VsProto
	if protoVs.Id.Proto == balancerpb.TransportProto_TCP {
		proto = balancerffi.ProtoTcp
	} else {
		proto = balancerffi.ProtoUdp
	}

	meta := uint64(0)

	// Convert flags and setup meta
	flags := balancerffi.VsFlags{}
	if protoVs.Flags != nil {
		flags.GRE = protoVs.Flags.Gre
		flags.OPS = protoVs.Flags.Ops
		flags.PureL3 = protoVs.Flags.PureL3
		flags.FixMSS = protoVs.Flags.FixMss
		if protoVs.Flags.AdjustWeights {
			meta = 1
		}
	}

	// Convert scheduler
	var scheduler balancerffi.VsScheduler
	if protoVs.Scheduler == balancerpb.VsScheduler_ROUND_ROBIN {
		scheduler = balancerffi.VsSchedulerRoundRobin
	} else {
		scheduler = balancerffi.VsSchedulerSourceHash
	}

	// Convert reals
	reals := make([]balancerffi.RealConfig, 0, len(protoVs.Reals))
	for _, protoReal := range protoVs.Reals {
		realConfig, err := protoToRealConfig(
			protoReal,
			vsAddr,
			uint16(protoVs.Id.Port),
			proto,
		)
		if err != nil {
			return balancerffi.VsConfig{}, fmt.Errorf(
				"failed to convert real: %w",
				err,
			)
		}
		reals = append(reals, realConfig)
	}

	// Convert allowed sources
	allowedSrc := make([]netip.Prefix, 0, len(protoVs.AllowedSrcs))
	for _, subnet := range protoVs.AllowedSrcs {
		if subnet != nil && subnet.Addr != nil {
			addr, ok := netip.AddrFromSlice(subnet.Addr.Bytes)
			if !ok {
				continue
			}
			if prefix, err := addr.Prefix(int(subnet.Size)); err == nil {
				allowedSrc = append(allowedSrc, prefix)
			}
		}
	}

	// Convert peers
	var peersV4, peersV6 []netip.Addr
	for _, peerMsg := range protoVs.Peers {
		if peerMsg != nil {
			if peer, ok := netip.AddrFromSlice(peerMsg.Bytes); ok {
				if peer.Is4() {
					peersV4 = append(peersV4, peer)
				} else {
					peersV6 = append(peersV6, peer)
				}
			}
		}
	}

	return balancerffi.VsConfig{
		Identifier: balancerffi.VsIdentifier{
			Ip:    vsAddr,
			Port:  uint16(protoVs.Id.Port),
			Proto: proto,
		},
		Flags:      flags,
		Scheduler:  scheduler,
		Reals:      reals,
		AllowedSrc: allowedSrc,
		PeersV4:    peersV4,
		PeersV6:    peersV6,
		User:       meta,
	}, nil
}

func protoToRealConfig(
	protoReal *balancerpb.Real,
	vsAddr netip.Addr,
	vsPort uint16,
	vsProto balancerffi.VsProto,
) (balancerffi.RealConfig, error) {
	if protoReal.Id == nil || protoReal.Id.Ip == nil {
		return balancerffi.RealConfig{}, fmt.Errorf("invalid real identifier")
	}

	realAddr, ok := netip.AddrFromSlice(protoReal.Id.Ip.Bytes)
	if !ok {
		return balancerffi.RealConfig{}, fmt.Errorf("invalid real address")
	}

	// Validate weight
	if protoReal.Weight == 0 {
		return balancerffi.RealConfig{}, fmt.Errorf(
			"invalid real weight: weight must be at least 1",
		)
	}
	if protoReal.Weight > uint32(balancerffi.MaxRealWeight) {
		return balancerffi.RealConfig{}, fmt.Errorf(
			"invalid real weight: weight cannot exceed %d",
			balancerffi.MaxRealWeight,
		)
	}

	var srcAddr, srcMask netip.Addr
	if protoReal.SrcAddr != nil {
		srcAddr, ok = netip.AddrFromSlice(protoReal.SrcAddr.Bytes)
		if !ok {
			return balancerffi.RealConfig{}, fmt.Errorf(
				"invalid source address",
			)
		}
	}

	if protoReal.SrcMask != nil {
		srcMask, ok = netip.AddrFromSlice(protoReal.SrcMask.Bytes)
		if !ok {
			return balancerffi.RealConfig{}, fmt.Errorf("invalid source mask")
		}
	}

	return balancerffi.RealConfig{
		Identifier: balancerffi.RealIdentifier{
			Vs: balancerffi.VsIdentifier{
				Ip:    vsAddr,
				Port:  vsPort,
				Proto: vsProto,
			},
			Relative: balancerffi.RelativeRealIdentifier{
				Ip:   realAddr,
				Port: uint16(protoReal.Id.Port),
			},
		},
		Weight:  uint16(protoReal.Weight),
		SrcAddr: srcAddr,
		SrcMask: srcMask,
	}, nil
}

// FFI to Protobuf conversions

func ConvertFFIProtoToProto(
	proto balancerffi.VsProto,
) balancerpb.TransportProto {
	if proto == balancerffi.ProtoTcp {
		return balancerpb.TransportProto_TCP
	}
	return balancerpb.TransportProto_UDP
}

func ConvertBalancerInfoToProto(
	info *balancerffi.BalancerInfo,
) *balancerpb.BalancerInfo {
	vsInfo := make([]*balancerpb.VsInfo, 0, len(info.VsInfo))
	for i := range info.VsInfo {
		vsInfo = append(vsInfo, ConvertVsInfoToProto(&info.VsInfo[i]))
	}

	return &balancerpb.BalancerInfo{
		ActiveSessions: info.ActiveSessions,
		Vs:             vsInfo,
	}
}

func ConvertVsInfoToProto(info *balancerffi.VsInfo) *balancerpb.VsInfo {
	reals := make([]*balancerpb.RealInfo, 0)

	return &balancerpb.VsInfo{
		Id: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: info.VsIdentifier.Ip.AsSlice(),
			},
			Port:  uint32(info.VsIdentifier.Port),
			Proto: ConvertFFIProtoToProto(info.VsIdentifier.Proto),
		},
		ActiveSessions: info.ActiveSessions,
		Reals:          reals,
	}
}

func ConvertRealInfoToProto(info *balancerffi.RealInfo) *balancerpb.RealInfo {
	return &balancerpb.RealInfo{
		Id: &balancerpb.RealIdentifier{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: info.RealIdentifier.Vs.Ip.AsSlice(),
				},
				Port:  uint32(info.RealIdentifier.Vs.Port),
				Proto: ConvertFFIProtoToProto(info.RealIdentifier.Vs.Proto),
			},
			Real: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: info.RealIdentifier.Relative.Ip.AsSlice(),
				},
				Port: uint32(info.RealIdentifier.Relative.Port),
			},
		},
		ActiveSessions: info.ActiveSessions,
	}
}

func ConvertSessionInfoToProto(
	info *balancerffi.SessionInfo,
) *balancerpb.SessionInfo {
	return &balancerpb.SessionInfo{
		ClientAddr: &balancerpb.Addr{
			Bytes: info.ClientAddr.AsSlice(),
		},
		ClientPort: uint32(info.ClientPort),
		VsId: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: info.Real.Vs.Ip.AsSlice(),
			},
			Port:  uint32(info.Real.Vs.Port),
			Proto: ConvertFFIProtoToProto(info.Real.Vs.Proto),
		},
		RealId: &balancerpb.RealIdentifier{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: info.Real.Vs.Ip.AsSlice(),
				},
				Port:  uint32(info.Real.Vs.Port),
				Proto: ConvertFFIProtoToProto(info.Real.Vs.Proto),
			},
			Real: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: info.Real.Relative.Ip.AsSlice(),
				},
				Port: uint32(info.Real.Relative.Port),
			},
		},
	}
}

func ConvertBalancerStatsToProto(
	info *balancerffi.BalancerInfo,
) *balancerpb.BalancerStats {
	vsStats := make([]*balancerpb.NamedVsStats, 0, len(info.VsInfo))
	for i := range info.VsInfo {
		vsStats = append(vsStats, &balancerpb.NamedVsStats{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: info.VsInfo[i].VsIdentifier.Ip.AsSlice(),
				},
				Port: uint32(info.VsInfo[i].VsIdentifier.Port),
				Proto: ConvertFFIProtoToProto(
					info.VsInfo[i].VsIdentifier.Proto,
				),
			},
			Stats: ConvertVsStatsToProto(&info.VsInfo[i].Stats),
		})
	}

	return &balancerpb.BalancerStats{
		L4:     ConvertL4StatsToProto(&info.Module.L4),
		Icmpv4: ConvertIcmpStatsToProto(&info.Module.ICMPv4),
		Icmpv6: ConvertIcmpStatsToProto(&info.Module.ICMPv6),
		Common: ConvertCommonStatsToProto(&info.Module.Common),
		Vs:     vsStats,
	}
}

func ConvertL4StatsToProto(stats *balancerffi.L4Stats) *balancerpb.L4Stats {
	return &balancerpb.L4Stats{
		IncomingPackets:  stats.IncomingPackets,
		SelectVsFailed:   stats.SelectVSFailed,
		InvalidPackets:   stats.InvalidPackets,
		SelectRealFailed: stats.SelectRealFailed,
		OutgoingPackets:  stats.OutgoingPackets,
	}
}

func ConvertIcmpStatsToProto(
	stats *balancerffi.ICMPStats,
) *balancerpb.IcmpStats {
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

func ConvertCommonStatsToProto(
	stats *balancerffi.CommonStats,
) *balancerpb.CommonStats {
	return &balancerpb.CommonStats{
		IncomingPackets:        stats.IncomingPackets,
		IncomingBytes:          stats.IncomingBytes,
		UnexpectedNetworkProto: stats.UnexpectedNetworkProto,
		DecapSuccessful:        stats.DecapSuccessful,
		DecapFailed:            stats.DecapFailed,
		OutgoingPackets:        stats.OutgoingPackets,
		OutgoingBytes:          stats.OutgoingBytes,
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

func ConvertRealStatsToProto(
	stats *balancerffi.RealStats,
) *balancerpb.RealStats {
	return &balancerpb.RealStats{
		PacketsRealDisabled: stats.PacketsRealDisabled,
		OpsPackets:          stats.OpsPackets,
		ErrorIcmpPackets:    stats.ErrorIcmpPackets,
		CreatedSessions:     stats.CreatedSessions,
		Packets:             stats.Packets,
		Bytes:               stats.Bytes,
	}
}
