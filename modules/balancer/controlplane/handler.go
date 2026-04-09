package balancer

import (
	"bytes"
	"fmt"
	"time"

	"github.com/yanet-platform/yanet2/common/go/relptr"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// populateSourceAddrs copies source IPv4 and IPv6 addresses to the handler.
func (ph *PacketHandler) populateSourceAddrs(config *balancerpb.PacketHandlerConfig) {
	writeNet4Addr(&ph.Source_v4, config.SourceAddressV4)
	writeNet6Addr(&ph.Source_v6, config.SourceAddressV6)
}

// populateSessionTimeouts writes timeout values to the handler.
func (ph *PacketHandler) populateSessionTimeouts(t *balancerpb.SessionsTimeouts) {
	ph.Session_timeouts = SessionTimeouts{
		Syn_ack: uint8(t.TcpSynAck),
		Syn:     uint8(t.TcpSyn),
		Fin:     uint8(t.TcpFin),
		Tcp:     uint8(t.Tcp),
		Udp:     uint8(t.Udp),
	}
}

// populateDecapAddresses separates decap addresses into IPv4/IPv6, allocates
// shared-memory arrays, and writes them to the handler.
//
// Each address family is handled in two passes: first to count, then to fill.
// AllocSlice requires the total element count upfront and cannot grow
// incrementally, so we must count before allocating.
func (ph *PacketHandler) populateDecapAddresses(agent *BalancerAgent, addrs [][]byte) error {
	v4Count := 0
	v6Count := 0
	for _, addr := range addrs {
		if len(addr) == 4 {
			v4Count++
		} else {
			v6Count++
		}
	}

	if v4Count > 0 {
		slice := yanet.AllocSlice[Net4Addr](agent.AsYanetAgent(), v4Count)
		if slice == nil {
			return errNoAgentMemory
		}
		j := 0
		for _, addr := range addrs {
			if len(addr) == 4 {
				writeNet4Addr(&slice[j], addr)
				j++
			}
		}
		relptr.SetSlice(&ph.Decap_v4, slice)
		ph.Decap_v4_count = uint32(v4Count)
	}

	if v6Count > 0 {
		slice := yanet.AllocSlice[Net6Addr](agent.AsYanetAgent(), v6Count)
		if slice == nil {
			return errNoAgentMemory
		}
		j := 0
		for _, addr := range addrs {
			if len(addr) == 16 {
				writeNet6Addr(&slice[j], addr)
				j++
			}
		}
		relptr.SetSlice(&ph.Decap_v6, slice)
		ph.Decap_v6_count = uint32(v6Count)
	}

	return nil
}

// setupFilters compiles or reuses VS matchers and decap filters based on the reuse report.
// Precondition: when any *Reused flag is true, prev must be non-nil. This is guaranteed
// because reuse flags are only set when prev exists (decapFiltersReusable returns false
// for nil, and populateVS only sets matcher flags when previous VSes overlap with new ones).
func (ph *PacketHandler) setupFilters(
	prev *PacketHandler,
	reuseReport *balancerpb.ReuseReport,
) error {
	if reuseReport.Ipv4VsMatcherReused {
		relptr.Equate(&ph.Ipv4_vs_matcher, &prev.Ipv4_vs_matcher)
	} else {
		if err := ph.setIpv4VsMatcher(); err != nil {
			return fmt.Errorf("set ipv4 vs matcher: %w", err)
		}
	}

	if reuseReport.Ipv6VsMatcherReused {
		relptr.Equate(&ph.Ipv6_vs_matcher, &prev.Ipv6_vs_matcher)
	} else {
		if err := ph.setIpv6VsMatcher(); err != nil {
			return fmt.Errorf("set ipv6 vs matcher: %w", err)
		}
	}

	if reuseReport.Ipv4DecapFilterReused {
		relptr.Equate(&ph.Decap_ipv4_filter, &prev.Decap_ipv4_filter)
	} else {
		if err := ph.setIpv4DecapFilter(); err != nil {
			return fmt.Errorf("set ipv4 decap filter: %w", err)
		}
	}

	if reuseReport.Ipv6DecapFilterReused {
		relptr.Equate(&ph.Decap_ipv6_filter, &prev.Decap_ipv6_filter)
	} else {
		if err := ph.setIpv6DecapFilter(); err != nil {
			return fmt.Errorf("set ipv6 decap filter: %w", err)
		}
	}

	return nil
}

// NewPacketHandler allocates and fully populates a new PacketHandler in shared memory.
// If prev is non-nil, filters and selectors may be reused from it when the underlying
// data hasn't changed (reported via ReuseReport).
// On error, all partially-allocated resources are cleaned up automatically.
func NewPacketHandler(
	config *balancerpb.BalancerConfig,
	name string,
	sessionTable *SessionTable,
	agent *BalancerAgent,
	prev *PacketHandler,
) (*PacketHandler, *balancerpb.ReuseReport, error) {
	phConfig, stateConfig := config.PacketHandler, config.State

	handler := yanet.Alloc[PacketHandler](agent.AsYanetAgent())
	if handler == nil {
		return nil, nil, errNoAgentMemory
	}

	// Free all resources on error. handler.Free is safe on partially-initialized handlers
	// because sub-slices start as nil and FreeSlice/C helpers are no-ops on nil values.
	success := false
	defer func() {
		if !success {
			handler.free(agent)
			yanet.Free(agent.AsYanetAgent(), handler)
		}
	}()

	if err := handler.initialSetup(agent, name, sessionTable); err != nil {
		return nil, nil, fmt.Errorf("initial setup: %w", err)
	}

	handler.populateSourceAddrs(phConfig)
	handler.populateSessionTimeouts(phConfig.SessionsTimeouts)

	if err := handler.populateDecapAddresses(agent, phConfig.DecapAddresses); err != nil {
		return nil, nil, fmt.Errorf("populate decap addrs: %w", err)
	}

	reuseReport := &balancerpb.ReuseReport{}

	if err := handler.populateVS(agent, phConfig.Vs, prev, reuseReport); err != nil {
		return nil, nil, fmt.Errorf("populate virtual services: %w", err)
	}

	reuseReport.Ipv4DecapFilterReused, reuseReport.Ipv6DecapFilterReused = prev.decapFiltersReusable(
		phConfig.DecapAddresses,
	)

	if err := handler.setupFilters(prev, reuseReport); err != nil {
		return nil, nil, err
	}

	if err := handler.registerCounters(); err != nil {
		return nil, nil, fmt.Errorf("register counters: %w", err)
	}

	handler.setState(stateConfig, sessionTable)

	success = true

	return handler, reuseReport, nil
}

// decapFiltersReusable checks whether the previous handler's decap addresses match
// the new config, allowing compiled decap filters to be reused.
// Precondition: addrs must be sorted by address family (all IPv4 first, then all IPv6).
// This ordering is guaranteed by validatePacketHandlerConfig which sorts decap addresses.
// Returns false, false when ph is nil (initial creation, no previous handler to reuse from).
func (ph *PacketHandler) decapFiltersReusable(addrs [][]byte) (ipv4Reused, ipv6Reused bool) {
	if ph == nil {
		return
	}

	// split is the index where IPv6 addresses begin in the sorted addrs slice.
	split := len(addrs)
	for i := range addrs {
		if len(addrs[i]) == 16 {
			split = i
			break
		}
	}

	ipv4Addrs := relptr.Slice(&ph.Decap_v4, ph.Decap_v4_count)
	if split == len(ipv4Addrs) {
		ipv4Reused = true
		for i := range ipv4Addrs {
			if !bytes.Equal(ipv4Addrs[i].Bytes[:], addrs[i]) {
				ipv4Reused = false
				break
			}
		}
	}

	ipv6Addrs := relptr.Slice(&ph.Decap_v6, ph.Decap_v6_count)
	if len(addrs)-split == len(ipv6Addrs) {
		ipv6Reused = true
		for i := range ipv6Addrs {
			if !bytes.Equal(ipv6Addrs[i].Bytes[:], addrs[split+i]) {
				ipv6Reused = false
				break
			}
		}
	}

	return ipv4Reused, ipv6Reused
}

// placeExistingVS places virtual services that exist in both the previous and new configs
// into their original slot positions in targetVs. Each placed VS is deleted from vsMap,
// so after this call vsMap contains only genuinely new VSes for placeNewVS to handle.
func placeExistingVS(
	ph *PacketHandler,
	agent *BalancerAgent,
	vsList []*balancerpb.VirtualService,
	targetVs []VS,
	prevVs []VS,
	vsMap map[vsKey]int,
	reuseReport *balancerpb.ReuseReport,
) (ipv4MatcherReused bool, ipv6MatcherReused bool, err error) {
	ipv4MatcherReused = true
	ipv6MatcherReused = true

	for idx := range prevVs {
		prev := &prevVs[idx]
		if prev.isRemoved() {
			continue
		}
		k := prev.key()
		configIdx, ok := vsMap[k]
		if !ok {
			// This VS not present in new config
			if k.addrLen == 4 {
				ipv4MatcherReused = false
			} else {
				ipv6MatcherReused = false
			}
			continue
		}

		delete(vsMap, k)

		target := &targetVs[idx]
		target.Flags &^= uint16(VSFlagRemoved)
		report, err := target.populate(agent, vsList[configIdx], prev.Stable_idx, prev, ph)
		if err != nil {
			return false, false, fmt.Errorf("vs %d: %w", configIdx, err)
		}

		reuseReport.VsReuseReports = append(reuseReport.VsReuseReports, report)
	}

	return ipv4MatcherReused, ipv6MatcherReused, nil
}

// placeNewVS places virtual services that are new in the config (remaining in vsMap
// after placeExistingVS) into removed (empty) slots in targetVs.
// Invariant: there are always enough removed slots because targetVs has len(vsList) slots,
// placeExistingVS and placeNewVS together account for exactly len(vsList) entries,
// and each entry fills exactly one slot.
func placeNewVS(
	ph *PacketHandler,
	agent *BalancerAgent,
	vsList []*balancerpb.VirtualService,
	targetVs []VS,
	prevVs []VS,
	vsMap map[vsKey]int,
	reuseReport *balancerpb.ReuseReport,
) (ipv4MatcherReused bool, ipv6MatcherReused bool, err error) {
	ipv4MatcherReused = true
	ipv6MatcherReused = true

	nextRemoved := 0
	for idx, vs := range vsList {
		k := makeVsKey(vs.Id)
		if _, ok := vsMap[k]; !ok {
			continue
		}

		if k.addrLen == 4 {
			ipv4MatcherReused = false
		} else {
			ipv6MatcherReused = false
		}

		for !targetVs[nextRemoved].isRemoved() {
			nextRemoved++
		}

		epoch := uint32(0)
		if nextRemoved < len(prevVs) {
			epoch = prevVs[nextRemoved].epoch() + 1
		}
		stableIdx := makeStableIdx(epoch, uint32(nextRemoved))

		target := &targetVs[nextRemoved]
		target.Flags &^= uint16(VSFlagRemoved)
		report, err := target.populate(agent, vsList[idx], stableIdx, nil, ph)
		if err != nil {
			return false, false, fmt.Errorf("vs %d: %w", idx, err)
		}

		nextRemoved++

		reuseReport.VsReuseReports = append(reuseReport.VsReuseReports, report)
	}

	return ipv4MatcherReused, ipv6MatcherReused, nil
}

func (ph *PacketHandler) populateVS(
	agent *BalancerAgent,
	vsList []*balancerpb.VirtualService,
	prevPh *PacketHandler,
	reuseReport *balancerpb.ReuseReport,
) error {
	vsMap := make(map[vsKey]int, len(vsList))
	for i, vs := range vsList {
		k := makeVsKey(vs.Id)
		vsMap[k] = i
	}

	var prevVs []VS
	if prevPh != nil {
		prevVs = relptr.Slice(&prevPh.Vs, prevPh.Vs_count)
	}

	slotCount := max(len(vsList), len(prevVs))
	services := yanet.AllocSlice[VS](agent.AsYanetAgent(), slotCount)
	if services == nil {
		return errNoAgentMemory
	}
	for idx := range services {
		services[idx] = VS{
			Flags: VSFlagRemoved,
		}
	}

	// freeServices cleans up the local services slice on error. This slice hasn't been
	// written to ph yet (relptr.SetSlice happens at the end), so there's no conflict
	// with the deferred handler.Free cleanup in NewPacketHandler.
	freeServices := func() {
		for idx := range services {
			services[idx].free(agent)
		}
		yanet.FreeSlice(agent.AsYanetAgent(), services)
	}

	reuseReport.VsReuseReports = make([]*balancerpb.VsReuseReport, 0, len(vsList))

	// First, write virtual services which are present in the previous config
	oldIPv4Matches, oldIPv6Matches, err := placeExistingVS(
		ph,
		agent,
		vsList,
		services,
		prevVs,
		vsMap,
		reuseReport,
	)
	if err != nil {
		freeServices()
		return err
	}

	// Then, write virtual services which are new in the new config
	noNewIPv4, noNewIPv6, err := placeNewVS(
		ph,
		agent,
		vsList,
		services,
		prevVs,
		vsMap,
		reuseReport,
	)
	if err != nil {
		freeServices()
		return err
	}

	reuseReport.Ipv4VsMatcherReused = prevPh != nil && oldIPv4Matches && noNewIPv4
	reuseReport.Ipv6VsMatcherReused = prevPh != nil && oldIPv6Matches && noNewIPv6

	ph.Vs_count = uint32(len(services))
	relptr.SetSlice(&ph.Vs, services)

	return nil
}

func (ph *PacketHandler) setState(stateConfig *balancerpb.StateConfig, sessionTable *SessionTable) {
	ph.Wlc_power = uint32(*stateConfig.Wlc.Power)
	ph.Wlc_max_weight = uint32(*stateConfig.Wlc.MaxWeight)
	ph.Refresh_period_ms = uint32(stateConfig.RefreshPeriod.AsDuration().Milliseconds())
	ph.Session_table_max_load_factor = float32(*stateConfig.SessionTableMaxLoadFactor)
	relptr.Set(&ph.Session_table, sessionTable)
}

func (ph *PacketHandler) resizeSessionTable(
	sessionTable *SessionTable,
	newSize int,
	now time.Time,
) error {
	if newSize <= sessionTable.capacity() {
		return nil
	}
	return sessionTable.resize(newSize, now)
}

// free releases all fields owned by the packet handler: per-VS resources,
// the VS array, decap address arrays, and compiled handler-level filters.
//
// Safe to call on a partially-initialized handler because every sub-slice
// starts as nil (zero-initialized) and FreeSlice / the C helpers are no-ops
// on zero/nil values.
func (ph *PacketHandler) free(agent *BalancerAgent) {
	yanetAgent := agent.AsYanetAgent()

	// Free per-VS resources and the VS array itself.
	vsSlice := relptr.Slice(&ph.Vs, ph.Vs_count)
	for i := range vsSlice {
		vsSlice[i].free(agent)
	}
	yanet.FreeSlice(yanetAgent, vsSlice)

	// Free decap address arrays.
	decapV4 := relptr.Slice(&ph.Decap_v4, ph.Decap_v4_count)
	yanet.FreeSlice(yanetAgent, decapV4)
	decapV6 := relptr.Slice(&ph.Decap_v6, ph.Decap_v6_count)
	yanet.FreeSlice(yanetAgent, decapV6)

	// Free compiled handler-level filters.
	ph.freeDecapFilters()
	ph.freeVsMatchers()
}
