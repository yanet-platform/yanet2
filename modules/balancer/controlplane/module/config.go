package module

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	balancer_ffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
	"go.uber.org/zap"
)

// Balancer module configuration
type ModuleConfig struct {
	// Agent which memory module config lives in.
	agent ffi.Agent

	// Mirrors C struct balancer_module_config
	cHandle balancer_ffi.ModuleConfigPtr

	// Balancer virtual services
	VirtualServices []lib.VirtualService

	// Balancer source and decap addresses
	Addresses lib.BalancerAddresses

	// Timeouts of the balancer sessions
	SessionTimeouts lib.SessionsTimeouts

	// Weighted least connections config
	wlc lib.WlcConfig

	// Name of the module config
	Name string

	// Buffer for real updates
	realUpdates lib.RealUpdateBuffer

	// State of the module config
	state *ModuleConfigState

	// Lock for background tasks
	lock *sync.Mutex

	// Logger for config operations
	log *zap.SugaredLogger

	// Context for background tasks cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

////////////////////////////////////////////////////////////////////////////////

func tryCreateNewModuleConfig(
	agent ffi.Agent,
	name string,
	state balancer_ffi.ModuleConfigStatePtr,
	virtualServices []lib.VirtualService,
	addresses lib.BalancerAddresses,
	sessionsTimeouts lib.SessionsTimeouts,
) (*balancer_ffi.ModuleConfigPtr, error) {
	cHandle, err := balancer_ffi.NewModuleConfig(
		agent,
		name,
		state,
		virtualServices,
		addresses,
		sessionsTimeouts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create new C `cp_module`: %w", err)
	}
	if err := cHandle.UpdateShmModule(agent); err != nil {
		return nil, fmt.Errorf("failed to insert C `cp_module`: %w", err)
	}
	return &cHandle, nil
}

////////////////////////////////////////////////////////////////////////////////

func updateEffectiveWeights(
	wlc *lib.WlcConfig,
	vs []lib.VirtualService,
	state *ModuleConfigState,
) bool {
	activeSessions := state.RealActiveSessions
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
	virtualServices []lib.VirtualService,
	addresses lib.BalancerAddresses,
	sessionsTimeouts lib.SessionsTimeouts,
	wlc lib.WlcConfig,
	lock *sync.Mutex,
	log *zap.SugaredLogger,
) (*ModuleConfig, error) {
	moduleConfig := ModuleConfig{
		agent:       agent,
		realUpdates: lib.RealUpdateBuffer{},
		Name:        name,
		state:       state,
		lock:        lock,
		log:         log,
	}

	if err := moduleConfig.Update(virtualServices, addresses, sessionsTimeouts, wlc); err != nil {
		return nil, err
	}

	return &moduleConfig, nil
}

func (config *ModuleConfig) Free() {
	config.cHandle.Free()
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) Update(
	virtualServices []lib.VirtualService,
	addresses lib.BalancerAddresses,
	sessionsTimeouts lib.SessionsTimeouts,
	wlc lib.WlcConfig,
) error {
	// Calculate effective weights for reals of virtual services
	updateEffectiveWeights(&wlc, virtualServices, config.state)

	// Try create and insert new controlplane module
	cHandle, err := tryCreateNewModuleConfig(
		config.agent,
		config.Name,
		config.state.CHandle(),
		virtualServices,
		addresses,
		sessionsTimeouts,
	)
	if err != nil {
		return err
	}

	// Here new module already inserted into controlplane

	config.cancelBackgroundTasks()

	// Update C handle
	config.cHandle = *cHandle

	// Update info
	config.VirtualServices = virtualServices
	config.Addresses = addresses
	config.SessionTimeouts = sessionsTimeouts

	// Set wlc
	config.wlc = wlc

	// clear real updates buffer
	config.realUpdates.Clear()

	config.runBackgroundTasks()

	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) UpdateEffectiveWeights() (bool, error) {
	if updateEffectiveWeights(
		&config.wlc,
		config.VirtualServices,
		config.state,
	) {
		err := config.Update(
			config.VirtualServices,
			config.Addresses,
			config.SessionTimeouts,
			config.wlc,
		)
		if err != nil {
			return false, fmt.Errorf(
				"effective weights updated, but failed to update config",
			)
		} else {
			return true, nil
		}
	} else {
		return false, nil
	}
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) UpdateReals(
	updates []lib.RealUpdate,
	buffer bool,
) error {
	if buffer {
		config.realUpdates.Append(updates)
		return nil
	} else {
		// clone virtual services
		cloneVirtualServices := config.VirtualServices
		for updateIdx := range updates {
			update := &updates[updateIdx]
			var updateVs *lib.VirtualService = nil
			for vsIdx := range cloneVirtualServices {
				vs := &config.VirtualServices[vsIdx]
				if vs.Identifier == update.Real.Vs {
					updateVs = vs
					break
				}
			}
			if updateVs == nil {
				return fmt.Errorf("failed to find virtual service for update at index %d", updateIdx)
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
				return fmt.Errorf("failed to find real for update at index %d", updateIdx)
			}
		}
		return config.Update(cloneVirtualServices, config.Addresses, config.SessionTimeouts, config.wlc)
	}
}

func (config *ModuleConfig) FlushRealUpdates() (int, error) {
	updates := config.realUpdates.Clear()
	err := config.UpdateReals(updates, false)
	if err != nil {
		return 0, err
	}
	return len(updates), nil
}

////////////////////////////////////////////////////////////////////////////////

// IntoProto converts ModuleConfig to protobuf message
func (config *ModuleConfig) IntoProto() *balancerpb.ModuleConfig {
	// Convert virtual services
	virtualServices := make(
		[]*balancerpb.VirtualService,
		0,
		len(config.VirtualServices),
	)
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
		Wlc:              config.wlc.IntoProto(),
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
func (config *ModuleConfig) GetStats(
	device, pipeline, function, chain string,
) lib.BalancerStats {
	// Get state info which contains the stats
	stateInfo := config.state.GetInfo()

	// Build VS stats from state info
	vsStats := make([]lib.VsStatsInfo, 0, len(config.VirtualServices))
	for i := range config.VirtualServices {
		vs := &config.VirtualServices[i]
		if vs.RegistryIdx < uint(len(stateInfo.VsInfo)) {
			vsInfo := stateInfo.VsInfo[vs.RegistryIdx]
			vsStats = append(vsStats, lib.VsStatsInfo{
				VsRegistryIdx: vsInfo.VsRegistryIdx,
				VsIdentifier:  vsInfo.VsIdentifier,
				Stats:         vsInfo.Stats,
			})
		}
	}

	// Build real stats from state info
	realStats := make([]lib.RealStatsInfo, 0)
	for i := range config.VirtualServices {
		vs := &config.VirtualServices[i]
		for j := range vs.Reals {
			real := &vs.Reals[j]
			if real.RegistryIdx < uint64(len(stateInfo.RealInfo)) {
				realInfo := stateInfo.RealInfo[real.RegistryIdx]
				realStats = append(realStats, lib.RealStatsInfo{
					RealRegistryIdx: realInfo.RealRegistryIdx,
					RealIdentifier:  realInfo.RealIdentifier,
					Stats:           realInfo.Stats,
				})
			}
		}
	}

	return lib.BalancerStats{
		Module: stateInfo.Module,
		Vs:     vsStats,
		Reals:  realStats,
	}
}

////////////////////////////////////////////////////////////////////////////////

func (config *ModuleConfig) runBackgroundTasks() {
	// Create a new context for background tasks
	config.ctx, config.cancel = context.WithCancel(context.Background())

	// Start update effective weights task
	if config.wlc.UpdatePeriodMs > 0 {
		period := time.Duration(config.wlc.UpdatePeriodMs) * time.Millisecond
		ctx := config.ctx
		go func() {
			ticker := time.NewTicker(period)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					config.lock.Lock()
					updated, err := config.UpdateEffectiveWeights()
					config.lock.Unlock()

					if err != nil {
						config.log.Error(
							"failed to update effective weights",
							zap.Error(err),
						)
					}
					if updated {
						config.log.Info("effective weights updated")
					}
				}
			}
		}()
	} else {
		config.log.Warn("passed zero WLC update period, updating routine not started")
	}
}

func (config *ModuleConfig) cancelBackgroundTasks() {
	if config.cancel != nil {
		config.cancel()
	}
}
