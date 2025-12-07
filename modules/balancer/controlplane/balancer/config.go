package balancer

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	balancer_ffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

// Balancer module configuration
type ModuleConfig struct {
	// Mirrors C struct balancer_module_config
	cHandle balancer_ffi.ModuleConfigPtr

	// Balancer virtual services
	VirtualServices []module.VirtualService

	// Balancer source and decap addresses
	Addresses module.BalancerAddresses

	// Timeouts of the balancer sessions
	SessionTimeouts module.SessionsTimeouts

	// Weighted least connections config
	wlc module.WlcConfig

	// Name of the module config
	Name string

	// Buffer for real updates
	realUpdates module.RealUpdateBuffer

	// State of the module config
	state *ModuleConfigState
}

////////////////////////////////////////////////////////////////////////////////

func tryCreateNewModuleConfig(
	agent ffi.Agent,
	name string,
	state balancer_ffi.ModuleConfigStatePtr,
	virtualServices []module.VirtualService,
	addresses module.BalancerAddresses,
	sessionsTimeouts module.SessionsTimeouts,
) (*balancer_ffi.ModuleConfigPtr, error) {
	cHandle, err := balancer_ffi.NewModuleConfig(agent, name, state, virtualServices, addresses, sessionsTimeouts)
	if err != nil {
		return nil, fmt.Errorf("failed to create new C `cp_module`: %w", err)
	}
	if err := cHandle.UpdateShmModule(agent); err != nil {
		return nil, fmt.Errorf("failed to insert C `cp_module`: %w", err)
	}
	return &cHandle, nil
}

////////////////////////////////////////////////////////////////////////////////

func updateEffectiveWeights(wlc *module.WlcConfig, vs []module.VirtualService, state *ModuleConfigState) bool {
	activeSessions := state.RealsActiveSessions(vs)
	updated := false
	for vsIdx := range vs {
		if vs[vsIdx].UpdateEffectiveWeights(wlc, activeSessions) {
			updated = true
		}
	}
	return updated
}

////////////////////////////////////////////////////////////////////////////////

func NewModuleConfig(
	agent ffi.Agent,
	name string,
	state *ModuleConfigState,
	virtualServices []module.VirtualService,
	addresses module.BalancerAddresses,
	sessionsTimeouts module.SessionsTimeouts,
	wlc module.WlcConfig,
) (*ModuleConfig, error) {
	// Calculate effective weights for reals of virtual services
	updateEffectiveWeights(&wlc, virtualServices, state)

	// Try create and insert new controlplane module
	cHandle, err := tryCreateNewModuleConfig(agent, name, state.CHandle(), virtualServices, addresses, sessionsTimeouts)
	if err != nil {
		return nil, err
	}

	return &ModuleConfig{
		cHandle:         *cHandle,
		VirtualServices: virtualServices,
		Addresses:       addresses,
		SessionTimeouts: sessionsTimeouts,
		realUpdates:     module.RealUpdateBuffer{},
		Name:            name,
		state:           state,
		wlc:             wlc,
	}, nil
}

func (config *ModuleConfig) Free() {
	config.cHandle.Free()
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) Update(
	agent ffi.Agent,
	virtualServices []module.VirtualService,
	addresses module.BalancerAddresses,
	sessionsTimeouts module.SessionsTimeouts,
	wlc module.WlcConfig,
) error {
	// Calculate effective weights for reals of virtual services
	updateEffectiveWeights(&wlc, virtualServices, config.state)

	// Try create and insert new controlplane module
	cHandle, err := tryCreateNewModuleConfig(
		agent,
		config.Name,
		config.state.CHandle(),
		virtualServices,
		addresses,
		sessionsTimeouts,
	)
	if err != nil {
		return err
	}

	// Update C handle
	config.cHandle = *cHandle

	// Update info
	config.VirtualServices = virtualServices
	config.Addresses = addresses
	config.SessionTimeouts = sessionsTimeouts

	// clear real updates buffer
	config.realUpdates.Clear()

	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) UpdateEffectiveWeights(agent ffi.Agent) error {
	if updateEffectiveWeights(&config.wlc, config.VirtualServices, config.state) {
		return config.Update(agent, config.VirtualServices, config.Addresses, config.SessionTimeouts, config.wlc)
	} else {
		return nil
	}
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) UpdateReals(agent ffi.Agent, updates []module.RealUpdate, buffer bool) error {
	if buffer {
		config.realUpdates.Append(updates)
		return nil
	} else {
		// clone virtual services
		cloneVirtualServices := config.VirtualServices
		for updateIdx := range updates {
			update := &updates[updateIdx]
			var updateVs *module.VirtualService = nil
			for vsIdx := range cloneVirtualServices {
				vs := &config.VirtualServices[vsIdx]
				if vs.Identifier == update.Real.Vs {
					updateVs = vs
					break
				}
			}
			if updateVs == nil {
				return fmt.Errorf("update[%d]: failed to find virtual service", updateIdx)
			}
			found := false
			for realIdx := range updateVs.Reals {
				real := &updateVs.Reals[realIdx]
				if real.Identifier == update.Real {
					real.Weight = update.Weight
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("update[%d]: failed to find real", updateIdx)
			}
		}
		return config.Update(agent, cloneVirtualServices, config.Addresses, config.SessionTimeouts, config.wlc)
	}
}

func (config *ModuleConfig) FlushRealUpdates(agent ffi.Agent) (int, error) {
	updates := config.realUpdates.Clear()
	err := config.UpdateReals(agent, updates, false)
	if err != nil {
		return 0, err
	}
	return len(updates), nil
}

////////////////////////////////////////////////////////////////////////////////

// IntoProto converts ModuleConfig to protobuf message
func (config *ModuleConfig) IntoProto() *balancerpb.ModuleConfig {
	// Convert virtual services
	virtualServices := make([]*balancerpb.VirtualService, 0, len(config.VirtualServices))
	for i := range config.VirtualServices {
		vs := &config.VirtualServices[i]

		// Convert reals
		reals := make([]*balancerpb.Real, 0, len(vs.Reals))
		for j := range vs.Reals {
			real := &vs.Reals[j]
			reals = append(reals, &balancerpb.Real{
				Weight:  uint32(real.Weight),
				DstAddr: real.Identifier.Ip.AsSlice(),
				SrcAddr: real.SrcAddr.AsSlice(),
				SrcMask: real.SrcMask.AsSlice(),
				Enabled: real.Enabled,
			})
		}

		// Convert allowed sources
		allowedSrcs := make([]*balancerpb.Subnet, 0, len(vs.AllowedSources))
		for j := range vs.AllowedSources {
			prefix := vs.AllowedSources[j]
			allowedSrcs = append(allowedSrcs, &balancerpb.Subnet{
				Addr: prefix.Addr().AsSlice(),
				Size: uint32(prefix.Bits()),
			})
		}

		// Convert peers
		peers := make([][]byte, 0, len(vs.Peers))
		for j := range vs.Peers {
			peers = append(peers, vs.Peers[j].AsSlice())
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Addr:        vs.Identifier.Ip.AsSlice(),
			Port:        uint32(vs.Identifier.Port),
			Proto:       vs.Identifier.Proto.IntoProto(),
			Scheduler:   vs.Scheduler.IntoProto(),
			AllowedSrcs: allowedSrcs,
			Reals:       reals,
			Flags:       vs.Flags.IntoProto(),
			Peers:       peers,
		})
	}

	return &balancerpb.ModuleConfig{
		VirtualServices:  virtualServices,
		SourceAddressV4:  config.Addresses.SourceIpV4[:],
		SourceAddressV6:  config.Addresses.SourceIpV6[:],
		DecapAddresses:   convertAddrsToBytes(config.Addresses.DecapAddresses),
		SessionsTimeouts: config.SessionTimeouts.IntoProto(),
	}
}

func convertAddrsToBytes(addrs []netip.Addr) [][]byte {
	result := make([][]byte, 0, len(addrs))
	for i := range addrs {
		result = append(result, addrs[i].AsSlice())
	}
	return result
}

// GetStats returns configuration statistics
func (config *ModuleConfig) GetStats(dataplaneInstance uint32, device, pipeline, function, chain string) module.BalancerStats {
	// Get state info which contains the stats
	stateInfo := config.state.GetInfo()

	// Build VS stats from state info
	vsStats := make([]module.VsStatsInfo, 0, len(config.VirtualServices))
	for i := range config.VirtualServices {
		vs := &config.VirtualServices[i]
		if vs.RegistryIdx < uint(len(stateInfo.VsInfo)) {
			vsInfo := stateInfo.VsInfo[vs.RegistryIdx]
			vsStats = append(vsStats, module.VsStatsInfo{
				VsRegistryIdx: vsInfo.VsRegistryIdx,
				VsIdentifier:  vsInfo.VsIdentifier,
				Stats:         vsInfo.Stats,
			})
		}
	}

	// Build real stats from state info
	realStats := make([]module.RealStatsInfo, 0)
	for i := range config.VirtualServices {
		vs := &config.VirtualServices[i]
		for j := range vs.Reals {
			real := &vs.Reals[j]
			if real.RegistryIdx < uint64(len(stateInfo.RealInfo)) {
				realInfo := stateInfo.RealInfo[real.RegistryIdx]
				realStats = append(realStats, module.RealStatsInfo{
					RealRegistryIdx: realInfo.RealRegistryIdx,
					RealIdentifier:  realInfo.RealIdentifier,
					Stats:           realInfo.Stats,
				})
			}
		}
	}

	return module.BalancerStats{
		Module: stateInfo.Module,
		Vs:     vsStats,
		Reals:  realStats,
	}
}
