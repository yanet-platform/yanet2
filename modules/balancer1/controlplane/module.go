package balancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type SessionTimeouts struct {
	TcpSynAck uint32
	TcpSyn    uint32
	TcpFin    uint32
	Tcp       uint32
	Udp       uint32
	Default   uint32
}

type BalancerConfig struct {
	Services        []VirtualService
	SessionTimeouts SessionTimeouts
	Name            string
}

type BalancerInstance struct {
	agent        *ffi.Agent
	config       *BalancerConfig
	sessionTable SessionTable
	moduleConfig ModuleConfig
}

func NewBalancerInstance(agent *ffi.Agent, config *BalancerConfig, sessionTableSize uint64) (*BalancerInstance, error) {
	sessionTable, err := NewSessionTable(agent, sessionTableSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create session table: %w", err)
	}
	moduleConfig, err := NewModuleconfig(agent, &sessionTable, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create cp module: %w", err)
	}
	return &BalancerInstance{
		agent:        agent,
		config:       config,
		sessionTable: sessionTable,
		moduleConfig: moduleConfig,
	}, nil
}

func (balancer *BalancerInstance) UpdateConfig(config *BalancerConfig) error {
	moduleConfig, err := NewModuleconfig(balancer.agent, &balancer.sessionTable, config)
	if err != nil {
		return fmt.Errorf("failed to create cp module: %w", err)
	}
	balancer.moduleConfig = moduleConfig
	return nil
}

func (balancer *BalancerInstance) UpdateModules(agent *ffi.Agent, config *BalancerConfig) error {
	return balancer.moduleConfig.InsertIntoRegistry(balancer.agent)
}

func (balancer *BalancerInstance) Free() {
	FreeSessionTable(&balancer.sessionTable)
	FreeModuleConfig(&balancer.moduleConfig)
}

func (balancer *BalancerInstance) ModuleConfig() *ModuleConfig {
	return &balancer.moduleConfig
}
