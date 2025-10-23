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
	Config       *BalancerConfig
	SessionTable SessionTable
	ModuleConfig ModuleConfig
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
	if err := moduleConfig.InsertIntoRegistry(agent); err != nil {
		return nil, fmt.Errorf("failed to insert balancer module into modules registry: %w", err)
	}
	return &BalancerInstance{
		Config:       config,
		SessionTable: sessionTable,
		ModuleConfig: moduleConfig,
	}, nil
}

func (balancer *BalancerInstance) UpdateConfig(agent *ffi.Agent, config *BalancerConfig) error {
	moduleConfig, err := NewModuleconfig(agent, &balancer.SessionTable, config)
	if err != nil {
		return fmt.Errorf("failed to create cp module: %w", err)
	}
	if err := moduleConfig.InsertIntoRegistry(agent); err != nil {
		return fmt.Errorf("failed to insert balancer module into modules registry: %w", err)
	}
	balancer.ModuleConfig = moduleConfig
	return nil
}

func (balancer *BalancerInstance) Free() {
	FreeSessionTable(&balancer.SessionTable)
	FreeModuleConfig(&balancer.ModuleConfig)
}
