package mbalancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// Timeouts of sessions with different types
type SessionsTimeouts struct {
	TcpSynAck uint32
	TcpSyn    uint32
	TcpFin    uint32
	Tcp       uint32
	Udp       uint32
	Default   uint32
}

func NewSessionsTimeoutsFromProto(proto *balancerpb.SessionsTimeouts) *SessionsTimeouts {
	return &SessionsTimeouts{
		TcpSynAck: proto.TcpSynAck,
		TcpSyn:    proto.TcpSyn,
		TcpFin:    proto.TcpFin,
		Tcp:       proto.Tcp,
		Udp:       proto.Udp,
		Default:   proto.Default,
	}
}

func (timeouts *SessionsTimeouts) IntoProto() *balancerpb.SessionsTimeouts {
	return &balancerpb.SessionsTimeouts{
		TcpSynAck: timeouts.TcpSyn,
		TcpSyn:    timeouts.TcpSyn,
		TcpFin:    timeouts.TcpFin,
		Tcp:       timeouts.Tcp,
		Udp:       timeouts.Udp,
		Default:   timeouts.Default,
	}
}

////////////////////////////////////////////////////////////////////////////////

// Config of the current balancer instance.
// One ModuleInstanceConfig corresponds to one `C.balancer_module_config`.
type ModuleInstanceConfig struct {
	Services []VirtualServiceConfig
}

func NewModuleInstanceConfig(
	proto *balancerpb.BalancerInstanceConfig,
) (*ModuleInstanceConfig, error) {
	services := make([]VirtualServiceConfig, 0)
	for idx, vs := range proto.VirtualServices {
		service, err := NewVirtualServiceConfigFromProto(vs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse virtual service no. %d: %w", idx, err)
		}
		services = append(services, *service)
	}
	return &ModuleInstanceConfig{
		Services: services,
	}, nil
}

func (config *ModuleInstanceConfig) Clone() *ModuleInstanceConfig {
	services := config.Services
	return &ModuleInstanceConfig{
		Services: services,
	}
}

func (config *ModuleInstanceConfig) IntoProto() *balancerpb.BalancerInstanceConfig {
	vs := make([]*balancerpb.VirtualService, 0)
	for _, service := range config.Services {
		vs = append(vs, service.IntoProto())
	}
	return &balancerpb.BalancerInstanceConfig{
		VirtualServices: vs,
	}
}

////////////////////////////////////////////////////////////////////////////////

// Represents instance of the balancer module.
// ModuleInstance = {session_table + current_config}
// Logically, one instance corresponds to one balancer module instance.
// It maintains current config (virtual services list + session timeouts) and session_table
// On request to update enabled reals, or change list of virtual services,
// it modifies current config and creates new cp_module with new config and with
// the same session_table.
type ModuleInstance struct {
	agent *ffi.Agent

	// name of the `cp_module`, balancer0 for example
	name string

	config *ModuleInstanceConfig

	vs []VirtualService

	// instance owns session table, service registry and wlc info
	state BalancerState

	// `cp_module`
	moduleConfig ModuleConfig

	// buffer of real updates
	realUpdateBuffer RealUpdateBuffer
}

func (instance *ModuleInstance) VirtualServices() []VirtualService {
	return instance.vs
}

// Create new balancer module instance.
// Insert created module config into dataplane registry.
func NewModuleInstance(
	agent *ffi.Agent,
	name string,
	config *ModuleInstanceConfig,
	sessionTableSize uint64,
	timeouts *SessionsTimeouts,
) (*ModuleInstance, error) {
	vs := make([]VirtualService, 0)
	state, err := NewState(agent, sessionTableSize, timeouts)
	if err != nil {
		return nil, fmt.Errorf("failed to create state: %w", err)
	}
	moduleConfig, err := state.NewModuleConfig(agent, config, name, &vs)
	if err != nil {
		return nil, fmt.Errorf("failed to create cp module: %w", err)
	}
	if err = moduleConfig.InsertIntoRegistry(agent); err != nil {
		return nil, fmt.Errorf("failed to insert module config into dataplane registry: %w", err)
	}
	return &ModuleInstance{
		agent:            agent,
		name:             name,
		config:           config,
		moduleConfig:     moduleConfig,
		state:            state,
		realUpdateBuffer: NewRealUpdateBuffer(),
		vs:               vs,
	}, nil
}

func (instance *ModuleInstance) Free() {
	instance.state.Free()
	instance.moduleConfig.Free()
}

////////////////////////////////////////////////////////////////////////////////

func (instance *ModuleInstance) StateInfo() (*StateInfo, error) {
	return instance.state.Info()
}

func (instance *ModuleInstance) ConfigInfo(
	device string,
	pipeline string,
	function string,
	chain string,
) (*ConfigInfo, error) {
	// todo: check if device, pipeline, function or chain is empty and traverse all variants then
	return instance.Info(
		instance.agent.DPConfig(),
		device,
		pipeline,
		function,
		chain,
		instance.name,
	)
}

////////////////////////////////////////////////////////////////////////////////

// Atomically update config.
// On error, the previous config state is assigned back.
// Clears update reals buffer.
// Insert created config into dataplane registry.
func (instance *ModuleInstance) UpdateConfig(config *ModuleInstanceConfig) error {
	instance.vs = make([]VirtualService, 0)
	moduleConfig, err := instance.state.NewModuleConfig(
		instance.agent,
		config,
		instance.name,
		&instance.vs,
	)
	if err != nil {
		return fmt.Errorf("failed to create cp module: %w", err)
	}
	if err = moduleConfig.InsertIntoRegistry(instance.agent); err != nil {
		return fmt.Errorf("failed to insert updated config into dataplane registry: %w", err)
	}
	instance.config = config
	instance.moduleConfig = moduleConfig
	instance.realUpdateBuffer.Clear()
	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (instance *ModuleInstance) ModuleConfig() *ModuleConfig {
	return &instance.moduleConfig
}

func (instance *ModuleInstance) GetConfig() *ModuleInstanceConfig {
	return instance.config
}

////////////////////////////////////////////////////////////////////////////////

// Update reals
// If `buffer` flag is specified, append updates to buffer.
// Else, make provided updates and CLEAR update buffer (without applying).
func (instance *ModuleInstance) UpdateReals(
	updates []*RealUpdate,
	buffer bool,
) error {
	for idx, update := range updates {
		if err := instance.config.ValidateRealUpdate(update); err != nil {
			return fmt.Errorf("update request no. %d is invalid: %w", idx, err)
		}
	}
	if buffer {
		instance.realUpdateBuffer.Append(updates)
	} else {
		newConfig := instance.config.Clone()
		for idx, update := range updates {
			if err := newConfig.UpdateReal(update); err != nil {
				return fmt.Errorf("failed to make update for real no. %d: %s", idx+1, err)
			}
		}
		if err := instance.UpdateConfig(newConfig); err != nil {
			return fmt.Errorf("failed to update config: %w", err)
		}
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

// Apply real updates from the update buffer.
func (instance *ModuleInstance) FlushRealUpdatesBuffer() (uint32, error) {
	updates := instance.realUpdateBuffer.updates
	newConfig := instance.config.Clone()
	for idx, update := range updates {
		if err := newConfig.UpdateReal(update); err != nil {
			return 0, fmt.Errorf("failed to make update no. %d: %w", idx+1, err)
		}
	}
	flushed := instance.realUpdateBuffer.Clear()
	if err := instance.UpdateConfig(newConfig); err != nil {
		return 0, fmt.Errorf("failed to update config: %w", err)
	}
	return flushed, nil
}

////////////////////////////////////////////////////////////////////////////////

// Extend session table if it is filled enough and free unused data.
func (instance *ModuleInstance) CheckSessionTable() error {
	if err := instance.state.ExtendSessionTable(false); err != nil {
		return fmt.Errorf("failed to extend session table: %w", err)
	}
	if err := instance.state.FreeUnusedInSessionTable(); err != nil {
		return fmt.Errorf("failed to free unused data in session table: %w", err)
	}
	return nil
}

// Force session table extension (for example, if there are many warnings about table overflow)
func (instance *ModuleInstance) ForceExtendSessionTable() error {
	if err := instance.state.ExtendSessionTable(true); err != nil {
		return fmt.Errorf("failed to extend session table: %w", err)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (instance *ModuleInstance) UpdateWlc() error {
	var updated bool = false
	for idx := range instance.vs {
		vs := &instance.vs[idx]
		if vs.Wlc != nil {
			for realIdx := range vs.Reals {
				real := &vs.Reals[realIdx]
				if real.Config.Enabled {
					currentConnections, err := instance.state.RealActiveSessionCount(
						uint64(real.RegistryIdx),
					)
					if err != nil {
						return fmt.Errorf(
							"failed to get active session count for real %d: %w",
							real.RegistryIdx,
							err,
						)
					}
					vs.Wlc.UpdateActiveConnections(uint64(real.RegistryIdx), currentConnections)
				}
			}
			if vs.Wlc.RecalculateWlcWeights() {
				updated = true
			}
		}
	}
	if updated {
		config := instance.config
		instance.UpdateConfig(config)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

// todo: traverse over all devices, pipelines, functions, chains and names
func (balancer *ModuleInstance) Info(
	dpConfig *ffi.DPConfig,
	device string,
	pipeline string,
	function string,
	chain string,
	name string,
) (*ConfigInfo, error) {
	counters := dpConfig.ModuleCounters(device, pipeline, function, chain, "balancer", name)
	configInfo := ConfigInfo{
		Vs: make([]ConfigVsInfo, 0, len(balancer.vs)),
	}
	for vsIdx := range balancer.vs {
		vs := &balancer.vs[vsIdx]
		vsCounters := findVsCounters(vs, counters)
		if vsCounters == nil {
			return nil, fmt.Errorf("failed to find counters for vs %d", vs.RegistryIdx)
		}
		vsInfo := ConfigVsInfo{
			Address:    vs.Info.Address,
			Port:       vs.Info.Port,
			Proto:      vs.Info.Proto,
			AllowedSrc: vs.Info.AllowedSrc,
			Reals:      make([]ConfigRealInfo, 0, len(vs.Reals)),
			Flags:      vs.Info.Flags,
			Stats:      vsStatsFromCounters(vsCounters.Values),
		}
		for realIdx := range len(vs.Reals) {
			real := &vs.Reals[realIdx]
			realCounters := findRealCounters(real, counters)
			if realCounters == nil {
				return nil, fmt.Errorf("failed to find counters for real %d", real.RegistryIdx)
			}
			realInfo := ConfigRealInfo{
				Weight:  real.Config.Weight,
				DstAddr: real.Config.DstAddr,
				SrcAddr: real.Config.SrcAddr,
				SrcMask: real.Config.SrcMask,
				Enabled: real.Config.Enabled,
				Stats:   realStatsFromCounters(realCounters.Values),
			}
			vsInfo.Reals = append(vsInfo.Reals, realInfo)
		}
		configInfo.Vs = append(configInfo.Vs, vsInfo)
	}
	return &configInfo, nil
}
