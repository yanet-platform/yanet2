// Represents instance of the balancer module

package balancer

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

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

type BalancerConfig struct {
	Services        []VirtualService
	SessionTimeouts SessionsTimeouts
}

func NewBalancerConfigFromProto(
	proto *balancerpb.BalancerInstanceConfig,
) (*BalancerConfig, error) {
	services := make([]VirtualService, 0)
	for idx, vs := range proto.VirtualServices {
		service, err := NewVirtualServiceFromProto(vs)
		if err != nil {
			return nil, fmt.Errorf("failed to parse virtual service no. %d: %w", idx, err)
		}
		services = append(services, *service)
	}
	timeouts := NewSessionsTimeoutsFromProto(proto.SessionsTimeouts)
	return &BalancerConfig{
		Services:        services,
		SessionTimeouts: *timeouts,
	}, nil
}

func (config *BalancerConfig) Clone() *BalancerConfig {
	timeouts := config.SessionTimeouts
	services := config.Services
	return &BalancerConfig{
		Services:        services,
		SessionTimeouts: timeouts,
	}
}

func (config *BalancerConfig) IntoProto() *balancerpb.BalancerInstanceConfig {
	vs := make([]*balancerpb.VirtualService, 0)
	for _, service := range config.Services {
		vs = append(vs, service.IntoProto())
	}
	return &balancerpb.BalancerInstanceConfig{
		SessionsTimeouts: config.SessionTimeouts.IntoProto(),
		VirtualServices:  vs,
	}
}

func (config *BalancerConfig) FindReal(vip *netip.Addr, realIp *netip.Addr, port uint16) *Real {
	for _, service := range config.Services {
		if service.Address == *vip && port == service.Port || (service.Flags.PureL3 && port == 0) {
			for _, real := range service.Reals {
				if real.DstAddr == *realIp {
					return &real
				}
			}
		}
	}
	return nil
}

func (config *BalancerConfig) ValidateRealUpdate(update *balancerpb.RealUpdate) (*RealUpdate, error) {
	if update.Weight > math.MaxUint16 {
		return nil, fmt.Errorf("real weight can not exceed %d", math.MaxUint16)
	}
	vip, err := netip.ParseAddr(string(update.VirtualIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse virtual ip: %s", err)
	}
	realIp, err := netip.ParseAddr(string(update.RealIp))
	if err != nil {
		return nil, fmt.Errorf("failed to parse real ip: %s", err)
	}
	if real := config.FindReal(&vip, &realIp, uint16(update.Port)); real != nil {
		return nil, nil
	} else {
		update := RealUpdate{
			VirtualIp: vip,
			Proto:     update.Proto,
			Port:      uint16(update.Port),
			RealIp:    realIp,
			Enable:    update.Enable,
			Weight:    update.Weight,
		}
		return &update, fmt.Errorf("real with address %s not found on virtual service [%s, %d]", realIp, vip, update.Port)
	}
}

func (config *BalancerConfig) UpdateReal(update *RealUpdate) error {
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

type BalancerInstance struct {
	agent            *ffi.Agent
	name             string
	config           *BalancerConfig
	sessionTable     SessionTable
	moduleConfig     ModuleConfig
	realUpdateBuffer RealUpdateBuffer
}

func NewBalancerInstance(
	agent *ffi.Agent,
	name string,
	config *BalancerConfig,
	sessionTableSize uint64,
) (*BalancerInstance, error) {
	sessionTable, err := NewSessionTable(agent, sessionTableSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create session table: %w", err)
	}
	moduleConfig, err := NewModuleConfig(agent, &sessionTable, config, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create cp module: %w", err)
	}
	return &BalancerInstance{
		agent:            agent,
		name:             name,
		config:           config,
		sessionTable:     sessionTable,
		moduleConfig:     moduleConfig,
		realUpdateBuffer: NewRealUpdateBuffer(),
	}, nil
}

func (balancer *BalancerInstance) Clone() *BalancerInstance {
	return &BalancerInstance{
		agent:            balancer.agent,
		name:             balancer.name,
		config:           balancer.config.Clone(),
		moduleConfig:     balancer.moduleConfig,
		realUpdateBuffer: balancer.realUpdateBuffer,
	}
}

func (balancer *BalancerInstance) Free() {
	FreeSessionTable(&balancer.sessionTable)
	FreeModuleConfig(&balancer.moduleConfig)
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) UpdateConfig(config *BalancerConfig) error {
	moduleConfig, err := NewModuleConfig(
		balancer.agent,
		&balancer.sessionTable,
		config,
		balancer.name,
	)
	if err != nil {
		return fmt.Errorf("failed to create cp module: %w", err)
	}
	balancer.moduleConfig = moduleConfig
	balancer.realUpdateBuffer.Clear()
	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) UpdateModules() error {
	return balancer.moduleConfig.InsertIntoRegistry(balancer.agent)
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) ModuleConfig() *ModuleConfig {
	return &balancer.moduleConfig
}

func (balancer *BalancerInstance) GetConfig() *BalancerConfig {
	return balancer.config
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) HandleRealUpdates(updates []*balancerpb.RealUpdate, buffer bool) error {
	validated := make([]*RealUpdate, 0)
	for idx, update := range updates {
		validated_update, err := balancer.config.ValidateRealUpdate(update)
		if err != nil {
			return fmt.Errorf("update request no. %d is invalid: %s", idx+1, err)
		}
		validated = append(validated, validated_update)
	}
	if buffer {
		balancer.realUpdateBuffer.Append(validated)
	} else {
		currentConfig := balancer.config
		newConfig := currentConfig.Clone()
		for idx, update := range validated {
			if err := newConfig.UpdateReal(update); err != nil {
				return fmt.Errorf("failed to make update no. %d: %s", idx+1, err)
			}
		}
		*balancer.config = *newConfig
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) FlushRealUpdatesBuffer() (uint32, error) {
	updates := balancer.realUpdateBuffer.updates
	currentConfig := balancer.config
	newConfig := currentConfig.Clone()
	for idx, update := range updates {
		if err := newConfig.UpdateReal(update); err != nil {
			return 0, fmt.Errorf("failed to make update no. %d: %s", idx+1, err)
		}
	}
	*balancer.config = *newConfig
	flushed := balancer.realUpdateBuffer.Clear()
	return flushed, nil
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *BalancerInstance) CheckSessionTable() error {
	err := ExtendSessionTable(&balancer.sessionTable, false)
	if err != nil {
		return fmt.Errorf("failed to extend session table: %w", err)
	}
	err = FreeUnusedInSessionTable(&balancer.sessionTable)
	if err != nil {
		return fmt.Errorf("failed to free unused data in session table: %w", err)
	}
	return nil
}
