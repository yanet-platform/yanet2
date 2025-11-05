package balancer

import (
	"fmt"
	"math"
	"net/netip"

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
// One ModuleInstanceConfig corresponds to one balancer_module_config.
type ModuleInstanceConfig struct {
	Services        []VirtualService
	SessionTimeouts SessionsTimeouts
}

func NewModuleInstanceConfig(
	proto *balancerpb.BalancerInstanceConfig,
) (*ModuleInstanceConfig, error) {
	services := make([]VirtualService, 0)
	for idx, vs := range proto.VirtualServices {
		service, err := NewVirtualServiceFromProto(vs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse virtual service no. %d: %w", idx, err)
		}
		services = append(services, *service)
	}
	timeouts := NewSessionsTimeoutsFromProto(proto.SessionsTimeouts)
	return &ModuleInstanceConfig{
		Services:        services,
		SessionTimeouts: *timeouts,
	}, nil
}

func (config *ModuleInstanceConfig) Clone() *ModuleInstanceConfig {
	services := config.Services
	timeouts := config.SessionTimeouts
	return &ModuleInstanceConfig{
		Services:        services,
		SessionTimeouts: timeouts,
	}
}

func (config *ModuleInstanceConfig) IntoProto() *balancerpb.BalancerInstanceConfig {
	vs := make([]*balancerpb.VirtualService, 0)
	for _, service := range config.Services {
		vs = append(vs, service.IntoProto())
	}
	return &balancerpb.BalancerInstanceConfig{
		SessionsTimeouts: config.SessionTimeouts.IntoProto(),
		VirtualServices:  vs,
	}
}

func (config *ModuleInstanceConfig) FindReal(vip *netip.Addr, realIp *netip.Addr, port uint16) *Real {
	for service_idx := range config.Services {
		service := &config.Services[service_idx]
		if service.Address == *vip && (port == service.Port || (service.Flags.PureL3 && port == 0)) {
			for idx := range service.Reals {
				real := &service.Reals[idx]
				if real.DstAddr == *realIp {
					return real
				}
			}
		}
	}
	return nil
}

func (config *ModuleInstanceConfig) ValidateRealUpdate(
	update *balancerpb.RealUpdate,
) (*RealUpdate, error) {
	if update.Weight > math.MaxUint16 {
		return nil, fmt.Errorf("real weight can not exceed %d", math.MaxUint16)
	}
	vip, err := netip.ParseAddr(string(update.VirtualIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse virtual ip: %w", err)
	}
	realIp, err := netip.ParseAddr(string(update.RealIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse real ip: %w", err)
	}
	if real := config.FindReal(&vip, &realIp, uint16(update.Port)); real == nil {
		return nil, fmt.Errorf("real with address %s not found on virtual service %s:%d", realIp, vip, update.Port)
	} else {
		update := RealUpdate{
			VirtualIp: vip,
			Proto:     update.Proto,
			Port:      uint16(update.Port),
			RealIp:    realIp,
			Enable:    update.Enable,
			Weight:    update.Weight,
		}
		return &update, nil
	}
}

func (config *ModuleInstanceConfig) UpdateReal(update *RealUpdate) error {
	real := config.FindReal(&update.VirtualIp, &update.RealIp, update.Port)
	if real == nil {
		return fmt.Errorf("failed to find real")
	}
	real.Enabled = update.Enable
	if update.Weight != 0 {
		real.Weight = uint16(update.Weight)
	}
	return nil
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

	// name of the `cp_module`
	name string

	config *ModuleInstanceConfig

	// instance owns session table
	sessionTable SessionTable

	// `cp_module`
	moduleConfig ModuleConfig

	// buffer of real updates
	realUpdateBuffer RealUpdateBuffer
}

// Create new balancer module instance.
// Insert created module config into dataplane registry.
func NewModuleInstance(
	agent *ffi.Agent,
	name string,
	config *ModuleInstanceConfig,
	sessionTableSize uint64,
) (*ModuleInstance, error) {
	sessionTable, err := NewSessionTable(agent, sessionTableSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create session table: %w", err)
	}
	moduleConfig, err := NewModuleConfig(agent, &sessionTable, config, name)
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
		sessionTable:     sessionTable,
		moduleConfig:     moduleConfig,
		realUpdateBuffer: NewRealUpdateBuffer(),
	}, nil
}

func (instance *ModuleInstance) Free() {
	FreeSessionTable(&instance.sessionTable)
	FreeModuleConfig(&instance.moduleConfig)
}

////////////////////////////////////////////////////////////////////////////////

// Atomically update config.
// On error, the previous config state is assigned back.
// Clears update reals buffer.
// Insert created config into dataplane registry.
func (instance *ModuleInstance) UpdateConfig(config *ModuleInstanceConfig) error {
	moduleConfig, err := NewModuleConfig(
		instance.agent,
		&instance.sessionTable,
		config,
		instance.name,
	)
	if err != nil {
		return fmt.Errorf("failed to create cp module: %w", err)
	}
	if err = moduleConfig.InsertIntoRegistry(instance.agent); err != nil {
		return fmt.Errorf("failed to insert updated config into dataplane registry: %w", err)
	}
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
	updates []*balancerpb.RealUpdate,
	buffer bool,
) error {
	validated := make([]*RealUpdate, 0)
	for idx, update := range updates {
		validated_update, err := instance.config.ValidateRealUpdate(update)
		if err != nil {
			return fmt.Errorf("update request no. %d is invalid: %w", idx+1, err)
		}
		if validated_update == nil {
			return fmt.Errorf("update request no. %d is invalid", idx+1)
		}
		validated = append(validated, validated_update)
	}
	if buffer {
		instance.realUpdateBuffer.Append(validated)
	} else {
		newConfig := instance.config.Clone()
		for idx, update := range validated {
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
	err := ExtendSessionTable(&instance.sessionTable, false)
	if err != nil {
		return fmt.Errorf("failed to extend session table: %w", err)
	}
	err = FreeUnusedInSessionTable(&instance.sessionTable)
	if err != nil {
		return fmt.Errorf("failed to free unused data in session table: %w", err)
	}
	return nil
}

// Force session table extension (for example, if there are many warnings about table overflow)
func (instance *ModuleInstance) ForceExtendSessionTable() error {
	err := ExtendSessionTable(&instance.sessionTable, false)
	if err != nil {
		return fmt.Errorf("failed to extend session table: %w", err)
	}
	return nil
}
