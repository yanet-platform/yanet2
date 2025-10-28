package balancer

import (
	"fmt"

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

////////////////////////////////////////////////////////////////////////////////

type BalancerInstance struct {
	agent        *ffi.Agent
	name         string
	config       *BalancerConfig
	sessionTable SessionTable
	moduleConfig ModuleConfig
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
		agent:        agent,
		name:         name,
		config:       config,
		sessionTable: sessionTable,
		moduleConfig: moduleConfig,
	}, nil
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
