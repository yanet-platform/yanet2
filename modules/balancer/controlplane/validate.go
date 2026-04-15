package balancer

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

func compareIPNet(a, b *filterpb.IPNet) int {
	if c := bytes.Compare(a.Addr, b.Addr); c != 0 {
		return c
	}
	return bytes.Compare(a.Mask, b.Mask)
}

func comparePortRange(a, b *filterpb.PortRange) int {
	if c := cmp.Compare(a.From, b.From); c != 0 {
		return c
	}
	return cmp.Compare(a.To, b.To)
}

func compareAllowedSourcesPb(a, b *balancerpb.AllowedSources) int {
	if c := cmp.Compare(len(a.Nets), len(b.Nets)); c != 0 {
		return c
	}
	for i := range a.Nets {
		if c := compareIPNet(a.Nets[i], b.Nets[i]); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(len(a.Ports), len(b.Ports)); c != 0 {
		return c
	}
	for i := range a.Ports {
		if c := comparePortRange(a.Ports[i], b.Ports[i]); c != 0 {
			return c
		}
	}
	return 0
}

func validateWlcConfig(wlc *balancerpb.WlcConfig) error {
	if wlc.Power == nil {
		return NewError("power is nil")
	}
	if wlc.MaxWeight == nil {
		return NewError("max_weight is nil")
	}
	return nil
}

func validateStateConfig(state *balancerpb.StateConfig) error {
	if state.SessionTableCapacity == nil {
		return NewError("session_table_capacity is nil")
	}
	if *state.SessionTableCapacity == 0 {
		return NewError("session_table_capacity must be greater than 0")
	}
	if state.RefreshPeriod == nil {
		return NewError("refresh_period is nil")
	}
	if state.SessionTableMaxLoadFactor == nil {
		return NewError("session_table_max_load_factor is nil")
	}
	if *state.SessionTableMaxLoadFactor <= 0 || *state.SessionTableMaxLoadFactor > 1 {
		return NewError("session_table_max_load_factor must be between 0 and 1")
	}
	if state.Wlc == nil {
		return NewError("wlc config is nil")
	}
	if err := validateWlcConfig(state.Wlc); err != nil {
		return NewError("wlc: %w", err)
	}
	return nil
}

func validateSessionsTimeouts(timeouts *balancerpb.SessionsTimeouts) error {
	if timeouts.TcpSynAck > MaxSessionTimeout {
		return NewError("tcp_syn_ack must be less than or equal to %d", MaxSessionTimeout)
	}
	if timeouts.TcpSyn > MaxSessionTimeout {
		return NewError("tcp_syn must be less than or equal to %d", MaxSessionTimeout)
	}
	if timeouts.TcpFin > MaxSessionTimeout {
		return NewError("tcp_fin must be less than or equal to %d", MaxSessionTimeout)
	}
	if timeouts.Tcp > MaxSessionTimeout {
		return NewError("tcp must be less than or equal to %d", MaxSessionTimeout)
	}
	if timeouts.Udp > MaxSessionTimeout {
		return NewError("udp must be less than or equal to %d", MaxSessionTimeout)
	}
	return nil
}

func validateMask4(mask []byte) error {
	bits := uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3])
	inverted := ^bits
	if inverted&(inverted+1) != 0 {
		return NewError("mask is not contiguous")
	}
	return nil
}

func isContiguous8(mask []byte) bool {
	bits := uint64(0)
	for i := range 8 {
		bits |= uint64(mask[i]) << ((7 - i) * 8)
	}
	inverted := ^bits
	return inverted&(inverted+1) == 0
}

// Check if the mask halves are contiguous.
func validateMask6(mask []byte) error {
	if !isContiguous8(mask[:8]) {
		return NewError("high mask bits are not contiguous")
	}
	if !isContiguous8(mask[8:]) {
		return NewError("low mask bits are not contiguous")
	}
	return nil
}

func validateNet(net *filterpb.IPNet, isV6 bool) error {
	requiredLen := 4
	if isV6 {
		requiredLen = 16
	}
	if len(net.Addr) != requiredLen {
		return NewError("net.addr must be %d bytes", requiredLen)
	}
	if len(net.Mask) != requiredLen {
		return NewError("net.mask must be %d bytes", requiredLen)
	}
	if isV6 {
		if err := validateMask6(net.Mask); err != nil {
			return NewError("IPv6 net mask: %w", err)
		}
	} else {
		if err := validateMask4(net.Mask); err != nil {
			return NewError("IPv4 net mask: %w", err)
		}
	}
	return nil
}

func validatePortRange(portRange *filterpb.PortRange) error {
	if portRange.From > portRange.To {
		return NewError("port_range.from must be less than or equal to port_range.to")
	}
	if portRange.To > 65535 {
		return NewError("port_range.to must be less than or equal to 65535")
	}
	return nil
}

func validateAllowedSrc(
	allowedSrc *balancerpb.AllowedSources,
	isIPv6 bool,
) error {
	for i, net := range allowedSrc.Nets {
		if err := validateNet(net, isIPv6); err != nil {
			return NewError("net %x/%x at index %d: %w", net.Addr, net.Mask, i, err)
		}
	}
	slices.SortFunc(allowedSrc.Nets, compareIPNet)
	allowedSrc.Nets = slices.CompactFunc(allowedSrc.Nets, func(a, b *filterpb.IPNet) bool {
		return compareIPNet(a, b) == 0
	})
	for i, port := range allowedSrc.Ports {
		if err := validatePortRange(port); err != nil {
			return NewError("port range [%d-%d] at index %d: %w", port.From, port.To, i, err)
		}
	}
	slices.SortFunc(allowedSrc.Ports, comparePortRange)
	allowedSrc.Ports = slices.CompactFunc(allowedSrc.Ports, func(a, b *filterpb.PortRange) bool {
		return comparePortRange(a, b) == 0
	})
	if allowedSrc.Tag != nil && len(*allowedSrc.Tag) > int(AllowedSourceMaxTagLength) {
		return NewError(
			"tag %s must be less than or equal to %d characters",
			*allowedSrc.Tag,
			AllowedSourceMaxTagLength,
		)
	}
	return nil
}

func validateReal(r *balancerpb.Real) error {
	if r.Id == nil {
		return NewError("id is nil")
	}
	id := r.Id
	if len(id.Ip) != 4 && len(id.Ip) != 16 {
		return NewError("id.ip must be 4 or 16 bytes long")
	}
	if id.Port != 0 {
		return NewError("only zero ports is currently supported")
	}
	if r.Src == nil {
		return NewError("src is nil")
	}
	if len(r.Src.Addr) != len(id.Ip) {
		return NewError("src.addr must be the same length as id.ip")
	}
	if len(r.Src.Mask) != len(id.Ip) {
		return NewError("src.mask must be the same length as id.ip")
	}
	return nil
}

// validateAllowedSources validates, sorts, and deduplicates the allowed sources slice.
// Side effect: sorts and compacts allowedSources. canReuseACL depends on this sort order
// to compare allowed sources element-by-element between old and new configs.
func validateAllowedSources(
	allowedSources []*balancerpb.AllowedSources,
	isIPv6 bool,
) ([]*balancerpb.AllowedSources, error) {
	for i, allowedSrc := range allowedSources {
		if allowedSrc == nil {
			return nil, NewError("allowed_src at index %d is nil", i)
		}
		if err := validateAllowedSrc(allowedSrc, isIPv6); err != nil {
			return nil, NewError("allowed_src at index %d: %w", i, err)
		}
	}
	slices.SortFunc(allowedSources, compareAllowedSourcesPb)
	allowedSources = slices.CompactFunc(allowedSources, func(a, b *balancerpb.AllowedSources) bool {
		return compareAllowedSourcesPb(a, b) == 0
	})
	return allowedSources, nil
}

func validateReals(reals []*balancerpb.Real) error {
	realsMap := make(map[realKey]int, len(reals))
	for i, r := range reals {
		if r == nil {
			return NewError("real at index %d is nil", i)
		}
		if err := validateReal(r); err != nil {
			return NewError("real %s at index %d: %w", realIDToString(r.Id), i, err)
		}
		key := makeRealKey(r.Id)
		if prevIdx, ok := realsMap[key]; ok {
			return NewError(
				"real %s at index %d: duplicate of real at index %d",
				realIDToString(r.Id),
				i,
				prevIdx,
			)
		}
		realsMap[key] = i
	}
	return nil
}

func validateVS(vs *balancerpb.VirtualService) error {
	if vs.Id == nil {
		return NewError("id is nil")
	}
	if len(vs.Id.Addr) != 4 && len(vs.Id.Addr) != 16 {
		return NewError("id.addr must be 4 or 16 bytes")
	}
	if vs.Scheduler != balancerpb.VsScheduler_SH &&
		vs.Scheduler != balancerpb.VsScheduler_WRR &&
		vs.Scheduler != balancerpb.VsScheduler_WLC {
		return NewError("scheduler must be SH/WRR/WLC")
	}
	if vs.Id.Proto != balancerpb.TransportProto_TCP &&
		vs.Id.Proto != balancerpb.TransportProto_UDP {
		return NewError("id.proto must be TCP or UDP")
	}
	if vs.Flags == nil {
		return NewError("flags is nil")
	}
	if vs.Flags.PureL3 && vs.Id.Port != 0 {
		return NewError("pure_l3 flag is set but port is not 0")
	}
	for i, peer := range vs.Peers {
		if len(peer) != 4 && len(peer) != 16 {
			return NewError("peer %x at index %d: addr must be 4 or 16 bytes long", peer, i)
		}
	}
	var err error
	vs.AllowedSrcs, err = validateAllowedSources(vs.AllowedSrcs, len(vs.Id.Addr) == 16)
	if err != nil {
		return err
	}
	if err := validateReals(vs.Reals); err != nil {
		return err
	}
	return nil
}

func validatePacketHandlerConfig(config *balancerpb.PacketHandlerConfig) error {
	if len(config.SourceAddressV4) != 4 {
		return NewError("source_address_v4 %x must be 4 bytes", config.SourceAddressV4)
	}
	if len(config.SourceAddressV6) != 16 {
		return NewError("source_address_v6 %x must be 16 bytes", config.SourceAddressV6)
	}
	if config.SessionsTimeouts == nil {
		return NewError("sessions_timeouts is nil")
	}
	if err := validateSessionsTimeouts(config.SessionsTimeouts); err != nil {
		return NewError("sessions_timeouts: %w", err)
	}
	for idx, addr := range config.DecapAddresses {
		if len(addr) != 4 && len(addr) != 16 {
			return NewError("decap_addresses %x at index %d: must be 4 or 16 bytes", addr, idx)
		}
	}

	// Sort decap addresses by family (IPv4 first, then IPv6), then by value.
	// decapFiltersReusable depends on this ordering to find the IPv4/IPv6 split point.
	slices.SortFunc(config.DecapAddresses, func(a, b []byte) int {
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return bytes.Compare(a, b)
	})
	config.DecapAddresses = slices.CompactFunc(config.DecapAddresses, bytes.Equal)

	vsMap := make(map[vsKey]int, len(config.Vs))
	for i, vs := range config.Vs {
		if vs == nil {
			return NewError("vs at index %d is nil", i)
		}
		if err := validateVS(vs); err != nil {
			return NewError("vs %s at index %d: %w", vsIDToString(vs.Id), i, err)
		}
		key := makeVsKey(vs.Id)
		if prevIdx, ok := vsMap[key]; ok {
			return NewError(
				"vs %s at index %d: duplicated at index %d",
				vsIDToString(vs.Id),
				i,
				prevIdx,
			)
		}
		vsMap[key] = i
	}

	return nil
}

// validateBalancerConfig checks that all required fields are present
// for creating a new balancer.
func validateBalancerConfig(config *balancerpb.BalancerConfig) error {
	if config == nil {
		return NewError("config is nil")
	}
	if config.PacketHandler == nil {
		return NewError("packet_handler is nil")
	}
	if err := validatePacketHandlerConfig(config.PacketHandler); err != nil {
		return NewError("packet_handler: %w", err)
	}
	if config.State == nil {
		return NewError("state is nil")
	}
	if err := validateStateConfig(config.State); err != nil {
		return NewError("state: %w", err)
	}
	return nil
}
