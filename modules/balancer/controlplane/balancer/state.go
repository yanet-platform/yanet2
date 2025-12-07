package balancer

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	balancer_ffi "github.com/yanet-platform/yanet2/modules/balancer/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

////////////////////////////////////////////////////////////////////////////////

// State of the module config
type ModuleConfigState struct {
	// Agent which memory state lives in.
	agent ffi.Agent

	// Mirrors C struct balancer_module_config_state
	cHandle balancer_ffi.ModuleConfigStatePtr

	// Initial size of the session table
	SessionTableSize uint

	// Period to try extend the session table
	ExtendPeriodMs uint

	// Period to try free unused memory in the session table
	FreeUnusedPeriodMs uint

	// Period to scan the state of the session table
	// to update active connections and WLC.
	ScanSessionTablePeriodMs uint

	// Module Config which works with this state.
	config *ModuleConfig

	// The background operations with state must use this lock.
	lock *sync.Mutex

	// Context for background tasks cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

func NewModuleConfigState(agent ffi.Agent, lock *sync.Mutex, initialTableSize, extendPeriodMs, freeUnusedPeriodMs, scanSessionTablePeriodMs uint) (*ModuleConfigState, error) {
	state, err := balancer_ffi.NewModuleConfigState(agent, initialTableSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create new module config state: %w", err)
	}
	s := &ModuleConfigState{
		agent:                    agent,
		cHandle:                  state,
		SessionTableSize:         initialTableSize,
		ExtendPeriodMs:           extendPeriodMs,
		FreeUnusedPeriodMs:       freeUnusedPeriodMs,
		ScanSessionTablePeriodMs: scanSessionTablePeriodMs,
		lock:                     lock,
	}
	return s, nil
}

func (s *ModuleConfigState) Free() {
	s.cancelBackgroundTasks()
	s.cHandle.Free()
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) ScheduleBackgroundTasks(config *ModuleConfig) {
	s.config = config
	s.runBackgroundTasks()
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) Update(tableSize, extendPeriodMs, freeUnusedPeriodMs, scanSessionTablePeriodMs uint) {
	// allow make force extension of session table.
	// todo: configure session table extension with strict size.
	s.cHandle.ExtendSessionTable(true)
	s.SessionTableSize = tableSize
	s.ExtendPeriodMs = extendPeriodMs
	s.FreeUnusedPeriodMs = freeUnusedPeriodMs
	s.ScanSessionTablePeriodMs = scanSessionTablePeriodMs
	s.cancelBackgroundTasks()
	s.runBackgroundTasks()
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) CHandle() balancer_ffi.ModuleConfigStatePtr {
	return s.cHandle
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) updateEffectiveWeights() error {
	// todo: scan session table to find active sessions, update effective weights
	s.config.UpdateEffectiveWeights(s.agent)
	return nil
}

func (s *ModuleConfigState) freeUnused() error {
	return s.cHandle.FreeUnusedInSessionTable()
}

func (s *ModuleConfigState) extendSessionTable() error {
	return s.cHandle.ExtendSessionTable(false)
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) runBackgroundTasks() {
	// Create a new context for background tasks
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Start scanSessionTable task
	if s.ScanSessionTablePeriodMs > 0 {
		go s.runPeriodicTask(s.ctx, time.Duration(s.ScanSessionTablePeriodMs)*time.Millisecond, s.updateEffectiveWeights)
	}

	// Start freeUnused task
	if s.FreeUnusedPeriodMs > 0 {
		go s.runPeriodicTask(s.ctx, time.Duration(s.FreeUnusedPeriodMs)*time.Millisecond, s.freeUnused)
	}

	// Start extendSessionTable task
	if s.ExtendPeriodMs > 0 {
		go s.runPeriodicTask(s.ctx, time.Duration(s.ExtendPeriodMs)*time.Millisecond, s.extendSessionTable)
	}
}

func (s *ModuleConfigState) cancelBackgroundTasks() {
	if s.cancel != nil {
		s.cancel()
	}
}

// runPeriodicTask runs a task periodically with the given period
func (s *ModuleConfigState) runPeriodicTask(ctx context.Context, period time.Duration, task func() error) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.lock.Lock()
			_ = task()
			s.lock.Unlock()
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) RealsActiveSessions(virtualServices []module.VirtualService) map[module.RealIdentifier]uint64 {
	result := make(map[module.RealIdentifier]uint64)
	for vsIdx := range virtualServices {
		vs := &virtualServices[vsIdx]
		for realIdx := range vs.Reals {
			real := &vs.Reals[realIdx]
			result[real.Identifier] = s.cHandle.RealInfo(uint(real.RegistryIdx)).ActiveSessions
		}
	}
	return result
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) RegisterVsWithReals(virtualService *balancerpb.VirtualService) (*module.VirtualService, error) {
	// Parse VS IP address
	vsAddr, ok := netip.AddrFromSlice(virtualService.Addr)
	if !ok {
		return nil, fmt.Errorf("invalid virtual service address")
	}

	// Create VS identifier
	vsIdentifier := module.VsIdentifier{
		Ip:    vsAddr,
		Port:  uint16(virtualService.Port),
		Proto: module.NewProtoFromProto(virtualService.Proto),
	}

	// Register VS in state registry
	vsRegistryIdx, err := s.cHandle.RegisterVs(&vsIdentifier)
	if err != nil {
		return nil, fmt.Errorf("failed to register virtual service: %w", err)
	}

	// Parse VS flags
	vsFlags := module.NewFlagsFromProto(virtualService.Flags)

	// Parse allowed sources
	allowedSources := make([]netip.Prefix, 0, len(virtualService.AllowedSrcs))
	for i, subnet := range virtualService.AllowedSrcs {
		addr, ok := netip.AddrFromSlice(subnet.Addr)
		if !ok {
			return nil, fmt.Errorf("invalid allowed source address at index %d", i)
		}
		prefix, err := addr.Prefix(int(subnet.Size))
		if err != nil {
			return nil, fmt.Errorf("invalid allowed source prefix at index %d: %w", i, err)
		}
		allowedSources = append(allowedSources, prefix)
	}

	// Parse peers
	peers := make([]netip.Addr, 0, len(virtualService.Peers))
	for i, peerBytes := range virtualService.Peers {
		peer, ok := netip.AddrFromSlice(peerBytes)
		if !ok {
			return nil, fmt.Errorf("invalid peer address at index %d", i)
		}
		peers = append(peers, peer)
	}

	// Parse and register reals
	reals := make([]module.Real, 0, len(virtualService.Reals))
	for i, protoReal := range virtualService.Reals {
		// Parse real IP address
		realAddr, ok := netip.AddrFromSlice(protoReal.DstAddr)
		if !ok {
			return nil, fmt.Errorf("invalid real address at index %d", i)
		}

		// Create real identifier
		realIdentifier := module.RealIdentifier{
			Vs: vsIdentifier,
			Ip: realAddr,
		}

		// Register real in state registry
		realRegistryIdx, err := s.cHandle.RegisterReal(&realIdentifier)
		if err != nil {
			return nil, fmt.Errorf("failed to register real at index %d: %w", i, err)
		}

		// Parse source address and mask
		srcAddr, ok := netip.AddrFromSlice(protoReal.SrcAddr)
		if !ok {
			return nil, fmt.Errorf("invalid real source address at index %d", i)
		}
		srcMask, ok := netip.AddrFromSlice(protoReal.SrcMask)
		if !ok {
			return nil, fmt.Errorf("invalid real source mask at index %d", i)
		}

		// Create real
		real := module.Real{
			RegistryIdx:     uint64(realRegistryIdx),
			Identifier:      realIdentifier,
			Weight:          uint16(protoReal.Weight),
			EffectiveWeight: 0, // Will be calculated later if WLC is used
			SrcAddr:         srcAddr,
			SrcMask:         srcMask,
			Enabled:         protoReal.Enabled,
		}
		reals = append(reals, real)
	}

	// Parse scheduler
	scheduler := module.NewSchedulerFromProto(virtualService.Scheduler)

	// Create and return the virtual service
	vs := &module.VirtualService{
		RegistryIdx:    vsRegistryIdx,
		Identifier:     vsIdentifier,
		Flags:          vsFlags,
		Reals:          reals,
		Peers:          peers,
		AllowedSources: allowedSources,
		Scheduler:      scheduler,
	}

	return vs, nil
}

////////////////////////////////////////////////////////////////////////////////

// GetInfo returns balancer state information
func (s *ModuleConfigState) GetInfo() module.BalancerInfo {
	s.lock.Lock()
	defer s.lock.Unlock()

	// Get VS info from state
	vsInfoList := s.cHandle.VirtualServicesInfo()

	// Get real info from state
	realInfoList := s.cHandle.RealsInfo()

	// TODO: Get module stats from dataplane
	moduleStats := module.ModuleStats{}

	return module.BalancerInfo{
		Module:   moduleStats,
		VsInfo:   vsInfoList,
		RealInfo: realInfoList,
	}
}
