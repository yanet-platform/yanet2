package main

import (
	"fmt"
	"log"
	"net/netip"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

type RunConfig struct {
	// MemoryPath is the path to the shared memory file
	MemoryPath string `yaml:"memory_path"`

	// Memory specifies memory requirements for the module
	Memory datasize.ByteSize `yaml:"memory"`

	Sessions uint64 `yaml:"sessions"`
}

func Run(cfg *RunConfig) error {
	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return fmt.Errorf("failed to attach shared memory: %w", err)
	}
	agents, err := shm.AgentsAttach("balancer", []uint32{0}, uint(cfg.Memory))
	if err != nil {
		return fmt.Errorf("failed to attach agent: %w", err)
	}
	agent := agents[0]
	state, err := NewPersistentState(agent, cfg.Sessions)
	if err != nil {
		return fmt.Errorf("failed to make new persistent state: %w", err)
	}
	moduleConfig, err := NewModuleConfig(agent, state, "balancer")
	if err != nil {
		return err
	}

	moduleConfig.SetTimeouts(Timeouts{
		TcpSynAckTtl: 5,
		TcpSynTtl:    5,
		TcpFinTtl:    5,
		TcpTtl:       5,
		UdpTtl:       5,
		DefaultTtl:   5,
	})

	service := Service{
		Addr:  common.Unwrap(netip.ParseAddr("192.132.1.1")),
		Port:  80,
		Proto: ServiceProtoTcp,
		Prefixes: []netip.Prefix{
			common.Unwrap(netip.ParsePrefix("0.0.0.0/0")),
		},
		Reals: []Real{{
			Weight:  1,
			DstAddr: common.Unwrap(netip.ParseAddr("198.166.3.5")),
			SrcAddr: common.Unwrap(netip.ParseAddr("255.127.0.0")),
			SrcMask: common.Unwrap(netip.ParseAddr("255.255.0.0")),
		}, {
			Weight:  2,
			DstAddr: common.Unwrap(netip.ParseAddr("198.169.0.1")),
			SrcAddr: common.Unwrap(netip.ParseAddr("255.127.0.0")),
			SrcMask: common.Unwrap(netip.ParseAddr("255.255.0.0")),
		},
		},
		GRE:                false,
		FixMss:             true,
		OnePacketScheduler: false,
		PureL3:             false,
	}

	err = moduleConfig.AddService(service)
	if err != nil {
		return err
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{moduleConfig.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %s", err)
	}

	log.Println("updated modules!")

	for {
		log.Println("updating time...")
		if err = moduleConfig.UpdateCurrentTime(); err != nil {
			return fmt.Errorf("failed to update current time: %s", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
