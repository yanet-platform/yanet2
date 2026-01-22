package balancer

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Protobuf to FFI conversions

func NewRealUpdateFromProto(
	update *balancerpb.RealUpdate,
) (*ffi.RealUpdate, error) {
	if update.RealId == nil || update.RealId.Vs == nil ||
		update.RealId.Real == nil {
		return nil, fmt.Errorf("incomplete real identifier in update")
	}

	vip, ok := netip.AddrFromSlice(update.RealId.Vs.Addr.Bytes)
	if !ok {
		return nil, fmt.Errorf("incorrect virtual service IP")
	}
	realIp, ok := netip.AddrFromSlice(update.RealId.Real.Ip.Bytes)
	if !ok {
		return nil, fmt.Errorf("incorrect real ip")
	}

	proto := ffi.VsTransportProtoUdp
	if update.RealId.Vs.Proto == balancerpb.TransportProto_TCP {
		proto = ffi.VsTransportProtoTcp
	}

	// Use the real port as specified (don't default to VS port)
	realPort := uint16(update.RealId.Real.Port)

	result := &ffi.RealUpdate{
		Identifier: ffi.RealIdentifier{
			VsIdentifier: ffi.VsIdentifier{
				Addr:           vip,
				Port:           uint16(update.RealId.Vs.Port),
				TransportProto: proto,
			},
			Relative: ffi.RelativeRealIdentifier{
				Addr: realIp,
				Port: realPort,
			},
		},
		Weight:  ffi.DontUpdateRealWeight,
		Enabled: ffi.DontUpdateRealEnabled,
	}

	if update.Weight != nil {
		result.Weight = uint16(*update.Weight)
	}

	if update.Enable != nil {
		if *update.Enable {
			result.Enabled = 1
		} else {
			result.Enabled = 0
		}
	}

	return result, nil
}

func ProtoToFFIConfig(
	config *balancerpb.BalancerConfig,
) (ffi.BalancerConfig, error) {
	if config.PacketHandler == nil {
		return ffi.BalancerConfig{}, fmt.Errorf(
			"packet_handler is required in CREATE mode",
		)
	}
	if config.State == nil {
		return ffi.BalancerConfig{}, fmt.Errorf(
			"state config is required in CREATE mode",
		)
	}
	if config.State.SessionTableCapacity == nil {
		return ffi.BalancerConfig{}, fmt.Errorf(
			"session_table_capacity is required in CREATE mode",
		)
	}
	if config.State.SessionTableMaxLoadFactor == nil {
		return ffi.BalancerConfig{}, fmt.Errorf(
			"session_table_max_load_factor is required in CREATE mode",
		)
	}
	if config.State.RefreshPeriod == nil {
		return ffi.BalancerConfig{}, fmt.Errorf(
			"refresh_period is required in CREATE mode",
		)
	}

	handlerConfig, err := ProtoToHandlerConfig(config.PacketHandler)
	if err != nil {
		return ffi.BalancerConfig{}, err
	}

	return ffi.BalancerConfig{
		State: ffi.StateConfig{
			TableCapacity: uint(*config.State.SessionTableCapacity),
		},
		Handler: handlerConfig,
	}, nil
}

// ProtoToManagerConfig converts protobuf config to FFI manager config for CREATE mode
// Validates that all required fields are present
func ProtoToManagerConfig(
	config *balancerpb.BalancerConfig,
) (*ffi.BalancerManagerConfig, error) {
	// Validate required fields
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if config.PacketHandler == nil {
		return nil, fmt.Errorf("packet_handler is required")
	}
	if config.State == nil {
		return nil, fmt.Errorf("state config is required")
	}
	if config.State.SessionTableCapacity == nil {
		return nil, fmt.Errorf("session_table_capacity is required")
	}

	// Check if any of refresh_period, max_load_factor, or wlc is present
	hasRefreshPeriod := config.State.RefreshPeriod != nil
	isRefreshPeriodValued := hasRefreshPeriod &&
		config.State.RefreshPeriod.AsDuration() != 0
	hasMaxLoadFactor := config.State.SessionTableMaxLoadFactor != nil
	hasWlc := config.State.Wlc != nil

	// If any one is present, all three must be present
	if isRefreshPeriodValued || hasMaxLoadFactor || hasWlc {
		if !hasRefreshPeriod {
			return nil, fmt.Errorf(
				"refresh_period is required when max_load_factor or wlc is specified",
			)
		}
		if !hasMaxLoadFactor {
			return nil, fmt.Errorf(
				"session_table_max_load_factor is required when refresh_period or wlc is specified",
			)
		}
		if !hasWlc {
			return nil, fmt.Errorf(
				"wlc config is required when refresh_period or session_table_max_load_factor is specified",
			)
		}
	}

	// Convert handler config
	handlerConfig, err := ProtoToHandlerConfig(config.PacketHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to convert handler config: %w", err)
	}

	// Create FFI balancer config
	balancerConfig := ffi.BalancerConfig{
		State: ffi.StateConfig{
			TableCapacity: uint(*config.State.SessionTableCapacity),
		},
		Handler: handlerConfig,
	}

	// Create WLC configuration
	wlcConfig, err := createWlcConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create WLC config: %w", err)
	}

	// Create manager config
	managerConfig := &ffi.BalancerManagerConfig{
		Balancer: balancerConfig,
		Wlc:      wlcConfig,
	}

	// Set refresh period and max load factor if present
	if hasRefreshPeriod {
		managerConfig.RefreshPeriod = config.State.RefreshPeriod.AsDuration()
	} else {
		managerConfig.RefreshPeriod = 0
	}
	if hasMaxLoadFactor {
		managerConfig.MaxLoadFactor = *config.State.SessionTableMaxLoadFactor
	} else {
		managerConfig.MaxLoadFactor = 0.0
	}

	return managerConfig, nil
}

func ProtoToHandlerConfig(
	config *balancerpb.PacketHandlerConfig,
) (ffi.PacketHandlerConfig, error) {
	// Validate required fields (non-optional in UPDATE mode)
	if config.SessionsTimeouts == nil {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"sessions_timeouts is required",
		)
	}
	if config.SourceAddressV4 == nil {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v4 is required",
		)
	}
	if config.SourceAddressV6 == nil {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v6 is required",
		)
	}
	if config.DecapAddresses == nil {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"decap_addresses is required (can be empty list)",
		)
	}
	if config.Vs == nil {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"vs (virtual services) is required",
		)
	}

	// Convert session timeouts
	timeouts := ffi.SessionsTimeouts{
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
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v4 must be a valid IPv4 address",
		)
	}
	if len(config.SourceAddressV6.Bytes) == 16 {
		sourceV6 = netip.AddrFrom16([16]byte(config.SourceAddressV6.Bytes))
	} else {
		return ffi.PacketHandlerConfig{}, fmt.Errorf(
			"source_address_v6 must be a valid IPv6 address",
		)
	}

	// Convert decap addresses
	decapV4 := make([]netip.Addr, 0)
	decapV6 := make([]netip.Addr, 0)
	for _, addrMsg := range config.DecapAddresses {
		if addrMsg != nil {
			if addr, ok := netip.AddrFromSlice(addrMsg.Bytes); ok {
				if addr.Is4() {
					decapV4 = append(decapV4, addr)
				} else {
					decapV6 = append(decapV6, addr)
				}
			}
		}
	}

	// Convert virtual services
	virtualServices := make([]ffi.VsConfig, 0, len(config.Vs))
	for _, protoVs := range config.Vs {
		vsConfig, err := protoToVsConfig(protoVs)
		if err != nil {
			return ffi.PacketHandlerConfig{}, fmt.Errorf(
				"failed to convert VS: %w",
				err,
			)
		}
		virtualServices = append(virtualServices, vsConfig)
	}

	return ffi.PacketHandlerConfig{
		SessionsTimeouts: timeouts,
		VirtualServices:  virtualServices,
		SourceV4:         sourceV4,
		SourceV6:         sourceV6,
		DecapV4:          decapV4,
		DecapV6:          decapV6,
	}, nil
}

func protoToVsConfig(
	protoVs *balancerpb.VirtualService,
) (ffi.VsConfig, error) {
	if protoVs.Id == nil || protoVs.Id.Addr == nil {
		return ffi.VsConfig{}, fmt.Errorf("invalid VS identifier")
	}

	// Convert VS address
	vsAddr, ok := netip.AddrFromSlice(protoVs.Id.Addr.Bytes)
	if !ok {
		return ffi.VsConfig{}, fmt.Errorf("invalid VS address")
	}

	// Convert proto
	var proto ffi.VsTransportProto
	if protoVs.Id.Proto == balancerpb.TransportProto_TCP {
		proto = ffi.VsTransportProtoTcp
	} else {
		proto = ffi.VsTransportProtoUdp
	}

	// Convert flags
	flags := ffi.VsFlags{}
	if protoVs.Flags != nil {
		flags.GRE = protoVs.Flags.Gre
		flags.OPS = protoVs.Flags.Ops
		flags.PureL3 = protoVs.Flags.PureL3
		flags.FixMSS = protoVs.Flags.FixMss
	}

	// Convert scheduler
	var scheduler ffi.VsScheduler
	if protoVs.Scheduler == balancerpb.VsScheduler_ROUND_ROBIN {
		scheduler = ffi.VsSchedulerRoundRobin
	} else {
		scheduler = ffi.VsSchedulerSourceHash
	}

	// Convert reals
	reals := make([]ffi.RealConfig, 0, len(protoVs.Reals))
	for _, protoReal := range protoVs.Reals {
		realConfig, err := protoToRealConfig(protoReal)
		if err != nil {
			return ffi.VsConfig{}, fmt.Errorf(
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

	return ffi.VsConfig{
		Identifier: ffi.VsIdentifier{
			Addr:           vsAddr,
			Port:           uint16(protoVs.Id.Port),
			TransportProto: proto,
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
) (ffi.RealConfig, error) {
	if protoReal.Id == nil || protoReal.Id.Ip == nil {
		return ffi.RealConfig{}, fmt.Errorf("invalid real identifier")
	}

	realAddr, ok := netip.AddrFromSlice(protoReal.Id.Ip.Bytes)
	if !ok {
		return ffi.RealConfig{}, fmt.Errorf("invalid real address")
	}

	// Validate weight
	if protoReal.Weight == 0 {
		return ffi.RealConfig{}, fmt.Errorf(
			"invalid real weight: weight must be at least 1",
		)
	}

	var srcNet xnetip.NetWithMask
	if protoReal.SrcAddr != nil && protoReal.SrcMask != nil {
		srcAddr, ok := netip.AddrFromSlice(protoReal.SrcAddr.Bytes)
		if !ok {
			return ffi.RealConfig{}, fmt.Errorf("invalid source address")
		}

		// Accept arbitrary masks (no validation for contiguous bits)
		maskBytes := protoReal.SrcMask.Bytes

		// Validate mask length matches address type
		expectedLen := 4
		if srcAddr.Is6() {
			expectedLen = 16
		}
		if len(maskBytes) != expectedLen {
			return ffi.RealConfig{}, fmt.Errorf(
				"invalid source mask length: got %d, expected %d",
				len(maskBytes), expectedLen,
			)
		}

		var err error
		srcNet, err = xnetip.NewNetWithMask(srcAddr, maskBytes)
		if err != nil {
			return ffi.RealConfig{}, fmt.Errorf(
				"invalid source network: %w",
				err,
			)
		}
	}

	return ffi.RealConfig{
		Identifier: ffi.RelativeRealIdentifier{
			Addr: realAddr,
			Port: uint16(protoReal.Id.Port),
		},
		Src:    srcNet,
		Weight: uint16(protoReal.Weight),
	}, nil
}

// createWlcConfig creates WLC configuration from protobuf config
// Returns error if WLC is enabled but required fields are missing
func createWlcConfig(
	config *balancerpb.BalancerConfig,
) (ffi.BalancerManagerWlcConfig, error) {
	wlc := ffi.BalancerManagerWlcConfig{
		Power:         0,
		MaxRealWeight: 0,
		Vs:            []uint32{},
	}

	// Check if any VS has WLC enabled
	hasWlcEnabled := false
	if config.PacketHandler != nil {
		for _, vs := range config.PacketHandler.Vs {
			if vs.Flags != nil && vs.Flags.Wlc {
				hasWlcEnabled = true
				break
			}
		}
	}

	// If WLC is enabled, validate configuration
	if hasWlcEnabled {
		if config.State == nil || config.State.Wlc == nil {
			return wlc, fmt.Errorf(
				"wlc config is required when WLC flag is enabled on virtual services",
			)
		}

		if config.State.Wlc.Power == nil {
			return wlc, fmt.Errorf("wlc.power is required when WLC is enabled")
		}
		if config.State.Wlc.MaxWeight == nil {
			return wlc, fmt.Errorf(
				"wlc.max_weight is required when WLC is enabled",
			)
		}

		wlc.Power = uint(*config.State.Wlc.Power)
		wlc.MaxRealWeight = uint(*config.State.Wlc.MaxWeight)

		// Collect VS indices that have WLC enabled
		for i, vs := range config.PacketHandler.Vs {
			if vs.Flags != nil && vs.Flags.Wlc {
				wlc.Vs = append(wlc.Vs, uint32(i))
			}
		}
	}

	// Always apply WLC config if provided (even if no VS has WLC enabled)
	if config.State != nil && config.State.Wlc != nil {
		if config.State.Wlc.Power != nil {
			wlc.Power = uint(*config.State.Wlc.Power)
		} else {
			wlc.Power = 0
		}
		if config.State.Wlc.MaxWeight != nil {
			wlc.MaxRealWeight = uint(*config.State.Wlc.MaxWeight)
		} else {
			wlc.MaxRealWeight = 0
		}
	}

	return wlc, nil
}

// mergeBalancerConfig merges new config with current config for UPDATE mode
// Returns merged config with all required fields filled recursively
func mergeBalancerConfig(
	newConfig *balancerpb.BalancerConfig,
	currentConfig *ffi.BalancerManagerConfig,
) (*balancerpb.BalancerConfig, error) {
	merged := &balancerpb.BalancerConfig{}

	// Recursively merge State first to get WLC config
	merged.State = mergeStateConfig(
		newConfig.State,
		currentConfig,
	)

	merged.PacketHandler = mergePacketHandlerConfig(
		newConfig.PacketHandler,
		&currentConfig.Balancer.Handler,
		&currentConfig.Wlc,
	)

	return merged, nil
}

// mergePacketHandlerConfig recursively merges packet handler fields
// If newHandler is nil, returns current handler converted to proto with WLC info
// Otherwise, merges each field individually, using current values for nil fields
func mergePacketHandlerConfig(
	newHandler *balancerpb.PacketHandlerConfig,
	currentHandler *ffi.PacketHandlerConfig,
	wlcConfig *ffi.BalancerManagerWlcConfig,
) *balancerpb.PacketHandlerConfig {
	if newHandler == nil {
		return convertPacketHandlerToProtoWithWlc(currentHandler, wlcConfig)
	}

	merged := &balancerpb.PacketHandlerConfig{}

	// Merge sessions_timeouts
	if newHandler.SessionsTimeouts != nil {
		merged.SessionsTimeouts = newHandler.SessionsTimeouts
	} else {
		merged.SessionsTimeouts = &balancerpb.SessionsTimeouts{
			TcpSynAck: currentHandler.SessionsTimeouts.TcpSynAck,
			TcpSyn:    currentHandler.SessionsTimeouts.TcpSyn,
			TcpFin:    currentHandler.SessionsTimeouts.TcpFin,
			Tcp:       currentHandler.SessionsTimeouts.Tcp,
			Udp:       currentHandler.SessionsTimeouts.Udp,
			Default:   currentHandler.SessionsTimeouts.Default,
		}
	}

	// Merge source_address_v4
	if newHandler.SourceAddressV4 != nil {
		merged.SourceAddressV4 = newHandler.SourceAddressV4
	} else {
		merged.SourceAddressV4 = &balancerpb.Addr{
			Bytes: currentHandler.SourceV4.AsSlice(),
		}
	}

	// Merge source_address_v6
	if newHandler.SourceAddressV6 != nil {
		merged.SourceAddressV6 = newHandler.SourceAddressV6
	} else {
		merged.SourceAddressV6 = &balancerpb.Addr{
			Bytes: currentHandler.SourceV6.AsSlice(),
		}
	}

	// Merge decap_addresses (array replacement)
	if newHandler.DecapAddresses != nil {
		merged.DecapAddresses = newHandler.DecapAddresses
	} else {
		// Convert current decap addresses
		decapAddrs := make(
			[]*balancerpb.Addr,
			0,
			len(currentHandler.DecapV4)+len(currentHandler.DecapV6),
		)
		for _, addr := range currentHandler.DecapV4 {
			decapAddrs = append(decapAddrs, &balancerpb.Addr{Bytes: addr.AsSlice()})
		}
		for _, addr := range currentHandler.DecapV6 {
			decapAddrs = append(decapAddrs, &balancerpb.Addr{Bytes: addr.AsSlice()})
		}
		merged.DecapAddresses = decapAddrs
	}

	// Merge vs (virtual services - array replacement)
	if newHandler.Vs != nil {
		merged.Vs = newHandler.Vs
	} else {
		// Convert current virtual services with WLC info
		wlcEnabledVs := make(map[uint32]bool)
		for _, vsIdx := range wlcConfig.Vs {
			wlcEnabledVs[vsIdx] = true
		}

		vs := make([]*balancerpb.VirtualService, 0, len(currentHandler.VirtualServices))
		for i := range currentHandler.VirtualServices {
			wlcEnabled := wlcEnabledVs[uint32(i)]
			vs = append(vs, convertVsConfigToProtoWithWlc(&currentHandler.VirtualServices[i], wlcEnabled))
		}
		merged.Vs = vs
	}

	return merged
}

// mergeStateConfig recursively merges state configuration
// If newState is nil, returns current state converted to proto
// Otherwise, merges each field individually, using current values for nil fields
func mergeStateConfig(
	newState *balancerpb.StateConfig,
	currentConfig *ffi.BalancerManagerConfig,
) *balancerpb.StateConfig {
	if newState == nil {
		// Return entire current state
		capacity := uint64(currentConfig.Balancer.State.TableCapacity)
		return &balancerpb.StateConfig{
			SessionTableCapacity:      &capacity,
			SessionTableMaxLoadFactor: &currentConfig.MaxLoadFactor,
			RefreshPeriod: durationpb.New(
				currentConfig.RefreshPeriod,
			),
			Wlc: convertWlcConfigToProto(
				&currentConfig.Wlc,
			),
		}
	}

	merged := &balancerpb.StateConfig{}

	// Merge session_table_capacity
	if newState.SessionTableCapacity != nil {
		merged.SessionTableCapacity = newState.SessionTableCapacity
	} else {
		capacity := uint64(currentConfig.Balancer.State.TableCapacity)
		merged.SessionTableCapacity = &capacity
	}

	// Merge session_table_max_load_factor
	if newState.SessionTableMaxLoadFactor != nil {
		merged.SessionTableMaxLoadFactor = newState.SessionTableMaxLoadFactor
	} else {
		merged.SessionTableMaxLoadFactor = &currentConfig.MaxLoadFactor
	}

	// Merge refresh_period
	if newState.RefreshPeriod != nil {
		merged.RefreshPeriod = newState.RefreshPeriod
	} else {
		merged.RefreshPeriod = durationpb.New(currentConfig.RefreshPeriod)
	}

	// Recursively merge WLC
	merged.Wlc = mergeWlcConfig(newState.Wlc, &currentConfig.Wlc)

	return merged
}

// mergeWlcConfig recursively merges WLC configuration
// If newWlc is nil, returns current WLC converted to proto
// Otherwise, merges each field individually, using current values for nil fields
func mergeWlcConfig(
	newWlc *balancerpb.WlcConfig,
	currentWlc *ffi.BalancerManagerWlcConfig,
) *balancerpb.WlcConfig {
	if newWlc == nil {
		return convertWlcConfigToProto(currentWlc)
	}

	merged := &balancerpb.WlcConfig{}

	// Merge power
	if newWlc.Power != nil {
		merged.Power = newWlc.Power
	} else {
		if currentWlc.Power != 0 {
			power := uint64(currentWlc.Power)
			merged.Power = &power
		}
	}

	// Merge max_weight
	if newWlc.MaxWeight != nil {
		merged.MaxWeight = newWlc.MaxWeight
	} else {
		if currentWlc.MaxRealWeight != 0 {
			maxWeight := uint32(currentWlc.MaxRealWeight)
			merged.MaxWeight = &maxWeight
		}
	}

	return merged
}

// FFI to Protobuf conversions

func ConvertFFIProtoToProto(
	proto ffi.VsTransportProto,
) balancerpb.TransportProto {
	if proto == ffi.VsTransportProtoTcp {
		return balancerpb.TransportProto_TCP
	}
	return balancerpb.TransportProto_UDP
}

func ConvertBalancerInfoToProto(
	info *ffi.BalancerInfo,
) *balancerpb.BalancerInfo {
	vsInfo := make([]*balancerpb.VsInfo, 0, len(info.Vs))
	for i := range info.Vs {
		vsInfo = append(vsInfo, ConvertVsInfoToProto(&info.Vs[i]))
	}

	return &balancerpb.BalancerInfo{
		ActiveSessions:      info.ActiveSessions,
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
		Vs:                  vsInfo,
	}
}

func ConvertVsInfoToProto(info *ffi.VsInfo) *balancerpb.VsInfo {
	reals := make([]*balancerpb.RealInfo, 0, len(info.Reals))
	for i := range info.Reals {
		reals = append(reals, ConvertRealInfoToProto(&info.Reals[i]))
	}

	return &balancerpb.VsInfo{
		Id: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: info.Identifier.Addr.AsSlice(),
			},
			Port:  uint32(info.Identifier.Port),
			Proto: ConvertFFIProtoToProto(info.Identifier.TransportProto),
		},
		ActiveSessions:      info.ActiveSessions,
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
		Reals:               reals,
	}
}

func ConvertRealInfoToProto(info *ffi.RealInfo) *balancerpb.RealInfo {
	return &balancerpb.RealInfo{
		Id: &balancerpb.RealIdentifier{
			Real: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: info.Dst.AsSlice(),
				},
			},
		},
		ActiveSessions:      info.ActiveSessions,
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
	}
}

func ConvertSessionInfoToProto(
	identifier *ffi.SessionIdentifier,
	info *ffi.SessionInfo,
) *balancerpb.SessionInfo {
	return &balancerpb.SessionInfo{
		LastPacketTimestamp: timestamppb.New(info.LastPacketTimestamp),
		CreateTimestamp:     timestamppb.New(info.CreateTimestamp),
		Timeout:             durationpb.New(info.Timeout),
		ClientAddr: &balancerpb.Addr{
			Bytes: identifier.ClientIp.AsSlice(),
		},
		ClientPort: uint32(identifier.ClientPort),
		VsId: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: identifier.Real.VsIdentifier.Addr.AsSlice(),
			},
			Port: uint32(identifier.Real.VsIdentifier.Port),
			Proto: ConvertFFIProtoToProto(
				identifier.Real.VsIdentifier.TransportProto,
			),
		},
		RealId: &balancerpb.RealIdentifier{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: identifier.Real.VsIdentifier.Addr.AsSlice(),
				},
				Port: uint32(identifier.Real.VsIdentifier.Port),
				Proto: ConvertFFIProtoToProto(
					identifier.Real.VsIdentifier.TransportProto,
				),
			},
			Real: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: identifier.Real.Relative.Addr.AsSlice(),
				},
				Port: uint32(identifier.Real.Relative.Port),
			},
		},
	}
}

func ConvertBalancerStatsToProto(
	stats *ffi.BalancerStats,
) *balancerpb.BalancerStats {
	vsStats := make([]*balancerpb.NamedVsStats, 0, len(stats.Vs))
	for i := range stats.Vs {
		// Convert real stats for this VS
		realStats := make(
			[]*balancerpb.NamedRealStats,
			0,
			len(stats.Vs[i].Reals),
		)
		for j := range stats.Vs[i].Reals {
			realStats = append(realStats, &balancerpb.NamedRealStats{
				Real: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: stats.Vs[i].Identifier.Addr.AsSlice(),
						},
						Port: uint32(stats.Vs[i].Identifier.Port),
						Proto: ConvertFFIProtoToProto(
							stats.Vs[i].Identifier.TransportProto,
						),
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: stats.Vs[i].Reals[j].Dst.AsSlice(),
						},
					},
				},
				Stats: ConvertRealStatsToProto(&stats.Vs[i].Reals[j].Stats),
			})
		}

		vsStats = append(vsStats, &balancerpb.NamedVsStats{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: stats.Vs[i].Identifier.Addr.AsSlice(),
				},
				Port: uint32(stats.Vs[i].Identifier.Port),
				Proto: ConvertFFIProtoToProto(
					stats.Vs[i].Identifier.TransportProto,
				),
			},
			Stats: ConvertVsStatsToProto(&stats.Vs[i].Stats),
			Reals: realStats,
		})
	}

	return &balancerpb.BalancerStats{
		L4:     ConvertL4StatsToProto(&stats.L4),
		Icmpv4: ConvertIcmpStatsToProto(&stats.IcmpIpv4),
		Icmpv6: ConvertIcmpStatsToProto(&stats.IcmpIpv6),
		Common: ConvertCommonStatsToProto(&stats.Common),
		Vs:     vsStats,
	}
}

func ConvertL4StatsToProto(stats *ffi.L4Stats) *balancerpb.L4Stats {
	return &balancerpb.L4Stats{
		IncomingPackets:  stats.IncomingPackets,
		SelectVsFailed:   stats.SelectVsFailed,
		InvalidPackets:   stats.InvalidPackets,
		SelectRealFailed: stats.SelectRealFailed,
		OutgoingPackets:  stats.OutgoingPackets,
	}
}

func ConvertIcmpStatsToProto(
	stats *ffi.IcmpStats,
) *balancerpb.IcmpStats {
	return &balancerpb.IcmpStats{
		IncomingPackets:           stats.IncomingPackets,
		SrcNotAllowed:             stats.SrcNotAllowed,
		EchoResponses:             stats.EchoResponses,
		PayloadTooShortIp:         stats.PayloadTooShortIp,
		UnmatchingSrcFromOriginal: stats.UnmatchingSrcFromOriginal,
		PayloadTooShortPort:       stats.PayloadTooShortPort,
		UnexpectedTransport:       stats.UnexpectedTransport,
		UnrecognizedVs:            stats.UnrecognizedVs,
		ForwardedPackets:          stats.ForwardedPackets,
		BroadcastedPackets:        stats.BroadcastedPackets,
		PacketClonesSent:          stats.PacketClonesSent,
		PacketClonesReceived:      stats.PacketClonesReceived,
		PacketCloneFailures:       stats.PacketCloneFailures,
	}
}

func ConvertCommonStatsToProto(
	stats *ffi.CommonStats,
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

func ConvertVsStatsToProto(stats *ffi.VsStats) *balancerpb.VsStats {
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
	stats *ffi.RealStats,
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

// ConvertGraphToProtoWithConfig converts FFI graph to protobuf with proper weight mapping.
// Weight in the result comes from config (original configured weight).
// EffectiveWeight in the result comes from graph (current effective weight after WLC adjustments).
func ConvertGraphToProtoWithConfig(
	graph *ffi.BalancerGraph,
	config *ffi.BalancerManagerConfig,
) *balancerpb.Graph {
	if graph == nil {
		return &balancerpb.Graph{}
	}

	// Build a lookup map for config weights: VS identifier -> Real identifier -> weight
	configWeights := buildConfigWeightsMap(config)

	vsServices := make([]*balancerpb.GraphVs, 0, len(graph.VirtualServices))
	for i := range graph.VirtualServices {
		vsServices = append(
			vsServices,
			convertGraphVsToProtoWithConfig(
				&graph.VirtualServices[i],
				configWeights,
			),
		)
	}

	return &balancerpb.Graph{
		VirtualServices: vsServices,
	}
}

// vsRealKey creates a unique key for a real within a VS context
type vsRealKey struct {
	vsAddr   string
	vsPort   uint16
	vsProto  ffi.VsTransportProto
	realAddr string
	realPort uint16
}

// buildConfigWeightsMap builds a map from VS+Real identifiers to config weights
func buildConfigWeightsMap(
	config *ffi.BalancerManagerConfig,
) map[vsRealKey]uint16 {
	weights := make(map[vsRealKey]uint16)
	if config == nil {
		return weights
	}

	for _, vs := range config.Balancer.Handler.VirtualServices {
		for _, real := range vs.Reals {
			key := vsRealKey{
				vsAddr:   vs.Identifier.Addr.String(),
				vsPort:   vs.Identifier.Port,
				vsProto:  vs.Identifier.TransportProto,
				realAddr: real.Identifier.Addr.String(),
				realPort: real.Identifier.Port,
			}
			weights[key] = real.Weight
		}
	}

	return weights
}

func convertGraphVsToProtoWithConfig(
	vs *ffi.GraphVs,
	configWeights map[vsRealKey]uint16,
) *balancerpb.GraphVs {
	reals := make([]*balancerpb.GraphReal, 0, len(vs.Reals))
	for i := range vs.Reals {
		// Look up config weight for this real
		key := vsRealKey{
			vsAddr:   vs.Identifier.Addr.String(),
			vsPort:   vs.Identifier.Port,
			vsProto:  vs.Identifier.TransportProto,
			realAddr: vs.Reals[i].Identifier.Addr.String(),
			realPort: vs.Reals[i].Identifier.Port,
		}

		configWeight := uint16(0)
		if w, ok := configWeights[key]; ok {
			configWeight = w
		}

		reals = append(reals, &balancerpb.GraphReal{
			Identifier: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: vs.Reals[i].Identifier.Addr.AsSlice(),
				},
				Port: uint32(vs.Reals[i].Identifier.Port),
			},
			// Weight = config weight (original configured weight)
			Weight: uint32(configWeight),
			// EffectiveWeight = graph weight (current effective weight after WLC)
			EffectiveWeight: uint32(vs.Reals[i].Weight),
			Enabled:         vs.Reals[i].Enabled,
		})
	}

	return &balancerpb.GraphVs{
		Identifier: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: vs.Identifier.Addr.AsSlice(),
			},
			Port:  uint32(vs.Identifier.Port),
			Proto: ConvertFFIProtoToProto(vs.Identifier.TransportProto),
		},
		Reals: reals,
	}
}

// ConvertBalancerConfigToProto converts FFI manager config to protobuf
func ConvertBalancerConfigToProto(
	config *ffi.BalancerManagerConfig,
) *balancerpb.BalancerConfig {
	if config == nil {
		return &balancerpb.BalancerConfig{}
	}

	// Convert packet handler with WLC config
	handler := convertPacketHandlerToProtoWithWlc(
		&config.Balancer.Handler,
		&config.Wlc,
	)

	// Convert state config
	capacity := uint64(config.Balancer.State.TableCapacity)
	loadFactor := config.MaxLoadFactor
	refreshPeriod := durationpb.New(config.RefreshPeriod)

	return &balancerpb.BalancerConfig{
		PacketHandler: handler,
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &capacity,
			SessionTableMaxLoadFactor: &loadFactor,
			RefreshPeriod:             refreshPeriod,
			Wlc:                       convertWlcConfigToProto(&config.Wlc),
		},
	}
}

func convertPacketHandlerToProtoWithWlc(
	handler *ffi.PacketHandlerConfig,
	wlcConfig *ffi.BalancerManagerWlcConfig,
) *balancerpb.PacketHandlerConfig {
	// Build a set of VS indices that have WLC enabled
	wlcEnabledVs := make(map[uint32]bool)
	if wlcConfig != nil {
		for _, vsIdx := range wlcConfig.Vs {
			wlcEnabledVs[vsIdx] = true
		}
	}

	// Convert virtual services
	vs := make([]*balancerpb.VirtualService, 0, len(handler.VirtualServices))
	for i := range handler.VirtualServices {
		wlcEnabled := wlcEnabledVs[uint32(i)]
		vs = append(
			vs,
			convertVsConfigToProtoWithWlc(
				&handler.VirtualServices[i],
				wlcEnabled,
			),
		)
	}

	// Convert decap addresses
	decapAddrs := make(
		[]*balancerpb.Addr,
		0,
		len(handler.DecapV4)+len(handler.DecapV6),
	)
	for _, addr := range handler.DecapV4 {
		decapAddrs = append(decapAddrs, &balancerpb.Addr{Bytes: addr.AsSlice()})
	}
	for _, addr := range handler.DecapV6 {
		decapAddrs = append(decapAddrs, &balancerpb.Addr{Bytes: addr.AsSlice()})
	}

	return &balancerpb.PacketHandlerConfig{
		Vs:              vs,
		SourceAddressV4: &balancerpb.Addr{Bytes: handler.SourceV4.AsSlice()},
		SourceAddressV6: &balancerpb.Addr{Bytes: handler.SourceV6.AsSlice()},
		DecapAddresses:  decapAddrs,
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: handler.SessionsTimeouts.TcpSynAck,
			TcpSyn:    handler.SessionsTimeouts.TcpSyn,
			TcpFin:    handler.SessionsTimeouts.TcpFin,
			Tcp:       handler.SessionsTimeouts.Tcp,
			Udp:       handler.SessionsTimeouts.Udp,
			Default:   handler.SessionsTimeouts.Default,
		},
	}
}

func convertVsConfigToProtoWithWlc(
	vs *ffi.VsConfig,
	wlcEnabled bool,
) *balancerpb.VirtualService {
	// Convert reals
	reals := make([]*balancerpb.Real, 0, len(vs.Reals))
	for i := range vs.Reals {
		reals = append(reals, convertRealConfigToProto(&vs.Reals[i]))
	}

	// Convert allowed sources
	allowedSrcs := make([]*balancerpb.Net, 0, len(vs.AllowedSrc))
	for _, prefix := range vs.AllowedSrc {
		allowedSrcs = append(allowedSrcs, &balancerpb.Net{
			Addr: &balancerpb.Addr{Bytes: prefix.Addr().AsSlice()},
			Size: uint32(prefix.Bits()),
		})
	}

	// Convert peers
	peers := make([]*balancerpb.Addr, 0, len(vs.PeersV4)+len(vs.PeersV6))
	for _, peer := range vs.PeersV4 {
		peers = append(peers, &balancerpb.Addr{Bytes: peer.AsSlice()})
	}
	for _, peer := range vs.PeersV6 {
		peers = append(peers, &balancerpb.Addr{Bytes: peer.AsSlice()})
	}

	scheduler := balancerpb.VsScheduler_SOURCE_HASH
	if vs.Scheduler == ffi.VsSchedulerRoundRobin {
		scheduler = balancerpb.VsScheduler_ROUND_ROBIN
	}

	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: vs.Identifier.Addr.AsSlice()},
			Port:  uint32(vs.Identifier.Port),
			Proto: ConvertFFIProtoToProto(vs.Identifier.TransportProto),
		},
		Scheduler:   scheduler,
		AllowedSrcs: allowedSrcs,
		Reals:       reals,
		Flags: &balancerpb.VsFlags{
			Gre:    vs.Flags.GRE,
			FixMss: vs.Flags.FixMSS,
			Ops:    vs.Flags.OPS,
			PureL3: vs.Flags.PureL3,
			Wlc:    wlcEnabled,
		},
		Peers: peers,
	}
}

func convertRealConfigToProto(real *ffi.RealConfig) *balancerpb.Real {
	srcAddr := real.Src.Addr.AsSlice()
	srcMask := real.Src.MaskBytes()

	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   &balancerpb.Addr{Bytes: real.Identifier.Addr.AsSlice()},
			Port: uint32(real.Identifier.Port),
		},
		Weight:  uint32(real.Weight),
		SrcAddr: &balancerpb.Addr{Bytes: srcAddr},
		SrcMask: &balancerpb.Addr{Bytes: srcMask},
	}
}

func convertWlcConfigToProto(
	wlc *ffi.BalancerManagerWlcConfig,
) *balancerpb.WlcConfig {
	if wlc == nil || (wlc.Power == 0 && wlc.MaxRealWeight == 0) {
		return nil
	}
	power := uint64(wlc.Power)
	maxWeight := uint32(wlc.MaxRealWeight)
	return &balancerpb.WlcConfig{
		Power:     &power,
		MaxWeight: &maxWeight,
	}
}

// ConvertProtoToFFIPacketHandlerRef converts protobuf ref to FFI
func ConvertProtoToFFIPacketHandlerRef(
	ref *balancerpb.PacketHandlerRef,
) *ffi.PacketHandlerRef {
	if ref == nil {
		return &ffi.PacketHandlerRef{}
	}

	result := &ffi.PacketHandlerRef{}
	if ref.Device != nil {
		device := *ref.Device
		result.Device = &device
	}
	if ref.Pipeline != nil {
		pipeline := *ref.Pipeline
		result.Pipeline = &pipeline
	}
	if ref.Function != nil {
		function := *ref.Function
		result.Function = &function
	}
	if ref.Chain != nil {
		chain := *ref.Chain
		result.Chain = &chain
	}
	return result
}

// ConvertFFIRealUpdateToProto converts FFI real update to protobuf
func ConvertFFIRealUpdateToProto(
	update *ffi.RealUpdate,
) *balancerpb.RealUpdate {
	result := &balancerpb.RealUpdate{
		RealId: &balancerpb.RealIdentifier{
			Vs: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: update.Identifier.VsIdentifier.Addr.AsSlice(),
				},
				Port: uint32(update.Identifier.VsIdentifier.Port),
				Proto: ConvertFFIProtoToProto(
					update.Identifier.VsIdentifier.TransportProto,
				),
			},
			Real: &balancerpb.RelativeRealIdentifier{
				Ip: &balancerpb.Addr{
					Bytes: update.Identifier.Relative.Addr.AsSlice(),
				},
				Port: uint32(update.Identifier.Relative.Port),
			},
		},
	}

	if update.Weight != ffi.DontUpdateRealWeight {
		weight := uint32(update.Weight)
		result.Weight = &weight
	}

	if update.Enabled != ffi.DontUpdateRealEnabled {
		enabled := update.Enabled != 0
		result.Enable = &enabled
	}

	return result
}
