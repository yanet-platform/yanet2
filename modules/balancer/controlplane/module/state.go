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

////////////////////////////////////////////////////////////////////////////////

// State of the module config
type ModuleConfigState struct {
	// Agent which memory state lives in.
	agent ffi.Agent

	// Mirrors C struct balancer_module_config_state
	cHandle balancer_ffi.ModuleConfigStatePtr

	// Period to scan the state of the session table
	// to update active connections and WLC.
	ScanSessionTablePeriodMs uint

	// If the relation of active sessions
	// and table capacity is greater than
	// this limit, we extend session table.
	MaxLoadFactor float32

	// Total number of active sessions.
	ActiveSessions uint

	// Virtual services active sessions information
	VsActiveSessions map[lib.VsIdentifier]uint

	// Real active sessions information
	RealActiveSessions map[lib.RealIdentifier]uint

	// Active sessions update time
	ActiveSessionsUpdateTimestamp time.Time

	// The background operations with state must use this lock.
	lock *sync.Mutex

	// Logger for state operations
	log *zap.SugaredLogger

	// Context for background tasks cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

func NewModuleConfigState(
	agent ffi.Agent,
	lock *sync.Mutex,
	initialTableSize, scanSessionTablePeriodMs uint,
	maxLoadFactor float32,
	log *zap.SugaredLogger,
) (*ModuleConfigState, error) {
	if initialTableSize == 0 {
		// Log warning, set default value
		log.Warn(
			"initial table size is 0, setting size to default value (1024)",
		)
		initialTableSize = 1024
	}

	// not null check
	if maxLoadFactor < 0.001 {
		return nil, fmt.Errorf("max load factor must be greater than 0.001")
	}

	state, err := balancer_ffi.NewModuleConfigState(agent, initialTableSize)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create new module config state: %w",
			err,
		)
	}

	s := &ModuleConfigState{
		agent:                    agent,
		cHandle:                  state,
		ScanSessionTablePeriodMs: scanSessionTablePeriodMs,
		MaxLoadFactor:            maxLoadFactor,
		lock:                     lock,
		log:                      log,
		VsActiveSessions:         map[lib.VsIdentifier]uint{},
		RealActiveSessions:       map[lib.RealIdentifier]uint{},
	}

	s.runBackgroundTasks()

	return s, nil
}

func (s *ModuleConfigState) Free() {
	s.cancelBackgroundTasks()
	s.cHandle.Free()
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) SessionTableCapacity() uint {
	return s.cHandle.SessionTableCapacity()
}

func (s *ModuleConfigState) Update(
	requestedCapacity, scanSessionTablePeriodMs uint,
	maxLoadFactor float32,
	now time.Time,
) error {
	// not null check
	if maxLoadFactor < 0.001 {
		return fmt.Errorf("max load factor must be greater than 0.001")
	}

	s.log.Infow(
		"resizing session table",
		zap.Uint("current_capacity", s.SessionTableCapacity()),
		zap.Uint("requested_capacity", requestedCapacity),
	)

	if requestedCapacity != 0 {
		err := s.cHandle.ResizeSessionTable(requestedCapacity, now)
		if err != nil {
			s.log.Errorf(
				"failed to resize session table",
				zap.Uint("requested_capacity", requestedCapacity),
				zap.Error(err),
			)
			return fmt.Errorf("failed to resize session table: %w", err)
		}

		s.log.Infow(
			"successfully resized session table",
			zap.Uint("requested_capacity", requestedCapacity),
			zap.Uint("new_capacity", s.SessionTableCapacity()),
		)
	} else {
		s.log.Info("did not resize session table as zero size is requested")
	}

	s.ScanSessionTablePeriodMs = scanSessionTablePeriodMs

	s.MaxLoadFactor = maxLoadFactor

	s.cancelBackgroundTasks()
	s.runBackgroundTasks()

	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) CHandle() balancer_ffi.ModuleConfigStatePtr {
	return s.cHandle
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) GetAndUpdateSessionsInfo(
	now time.Time,
) (*lib.SessionsInfo, error) {
	sessions := s.cHandle.SessionsInfo(uint32(now.Unix()), false)
	if sessions == nil {
		s.log.Warn("failed to get sessions info from C handle")
		return nil, fmt.Errorf("failed to scan session table")
	}

	s.log.Debugw("retrieved sessions from C handle",
		zap.Uint("sessions_count", sessions.SessionsCount))

	// remove old active sessions info for real
	for k := range s.RealActiveSessions {
		delete(s.RealActiveSessions, k)
	}

	// remove old active sessions info for virtual services
	for k := range s.VsActiveSessions {
		delete(s.VsActiveSessions, k)
	}

	// Update active sessions
	for _, session := range sessions.Sessions {
		s.RealActiveSessions[session.Real]++
		s.VsActiveSessions[session.Real.Vs]++
	}

	s.ActiveSessions = sessions.SessionsCount
	s.ActiveSessionsUpdateTimestamp = now

	return sessions, nil
}

func (s *ModuleConfigState) SyncActiveSessionsAndResizeTableOnDemand(
	now time.Time,
) error {
	// Update active connections info
	_, err := s.GetAndUpdateSessionsInfo(now)
	if err != nil {
		s.log.Errorw(
			"failed to get sessions info during table scan",
			zap.Error(err),
		)
		return fmt.Errorf("failed to scan sessions table: %w", err)
	}

	sessionTableCapacity := s.SessionTableCapacity()
	loadFactor := float32(s.ActiveSessions) / float32(sessionTableCapacity)

	s.log.Debugw("session table scan completed",
		zap.Uint("active_sessions", s.ActiveSessions),
		zap.Uint("table_capacity", sessionTableCapacity),
		zap.Float32("load_factor", loadFactor),
		zap.Float32("max_load_factor", s.MaxLoadFactor))

	if loadFactor > s.MaxLoadFactor {
		requestedCapacity := sessionTableCapacity * 2
		s.log.Infow("session table load factor exceeded, resizing",
			zap.Float32("load_factor", loadFactor),
			zap.Float32("max_load_factor", s.MaxLoadFactor),
			zap.Uint("current_capacity", sessionTableCapacity),
			zap.Uint("requested_capacity", requestedCapacity))

		err := s.cHandle.ResizeSessionTable(requestedCapacity, now)
		if err != nil {
			s.log.Warnw("failed to resize session table",
				zap.Uint("requested_capacity", requestedCapacity),
				zap.Error(err))
			return fmt.Errorf(
				"failed to resize session table to capacity %d: %w",
				requestedCapacity,
				err,
			)
		}
		newCapacity := s.SessionTableCapacity()
		s.log.Infow(
			"session table resized successfully",
			zap.Uint("old_capacity", sessionTableCapacity),
			zap.Uint("requested_capacity", requestedCapacity),
			zap.Uint("new_capacity", newCapacity),
		)
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) runBackgroundTasks() {
	// Create a new context for background tasks
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Start scanSessionTable task
	if s.ScanSessionTablePeriodMs > 0 {
		period := time.Duration(s.ScanSessionTablePeriodMs) * time.Millisecond
		ctx := s.ctx

		// run periodic task
		go func() {
			ticker := time.NewTicker(period)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.lock.Lock()
					err := s.SyncActiveSessionsAndResizeTableOnDemand(
						time.Now(),
					)
					s.lock.Unlock()
					if err != nil {
						s.log.Errorw(
							"background task failed",
							zap.Error(err),
						)
					}
				}
			}
		}()
	} else {
		s.log.Warn("passed zero period for session table scan routine, scanning routine not started")
	}
}

func (s *ModuleConfigState) cancelBackgroundTasks() {
	if s.cancel != nil {
		s.cancel()
	}
}

////////////////////////////////////////////////////////////////////////////////

func (s *ModuleConfigState) RegisterVsWithReals(
	virtualService *balancerpb.VirtualService,
) (*lib.VirtualService, error) {
	// Parse VS IP address
	vsAddr, ok := netip.AddrFromSlice(virtualService.Addr)
	if !ok {
		return nil, fmt.Errorf("invalid virtual service address")
	}

	// Create VS identifier
	vsIdentifier := lib.VsIdentifier{
		Ip:    vsAddr,
		Port:  uint16(virtualService.Port),
		Proto: lib.NewProtoFromProto(virtualService.Proto),
	}

	// Register VS in state registry
	vsRegistryIdx, err := s.cHandle.RegisterVs(&vsIdentifier)
	if err != nil {
		return nil, fmt.Errorf("failed to register virtual service: %w", err)
	}

	// Parse VS flags
	vsFlags := lib.NewFlagsFromProto(virtualService.Flags)

	// Parse allowed sources
	allowedSources := make([]netip.Prefix, 0, len(virtualService.AllowedSrcs))
	for i, subnet := range virtualService.AllowedSrcs {
		addr, ok := netip.AddrFromSlice(subnet.Addr)
		if !ok {
			return nil, fmt.Errorf(
				"invalid allowed source address at index %d",
				i,
			)
		}
		prefix, err := addr.Prefix(int(subnet.Size))
		if err != nil {
			return nil, fmt.Errorf(
				"invalid allowed source prefix at index %d: %w",
				i,
				err,
			)
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
	reals := make([]lib.Real, 0, len(virtualService.Reals))
	for i, protoReal := range virtualService.Reals {
		// Parse real IP address
		realAddr, ok := netip.AddrFromSlice(protoReal.DstAddr)
		if !ok {
			return nil, fmt.Errorf("invalid real address at index %d", i)
		}

		// Create real identifier
		realIdentifier := lib.RealIdentifier{
			Vs: vsIdentifier,
			Ip: realAddr,
		}

		// Register real in state registry
		realRegistryIdx, err := s.cHandle.RegisterReal(&realIdentifier)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to register real at index %d: %w",
				i,
				err,
			)
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
		real := lib.Real{
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
	scheduler := lib.NewSchedulerFromProto(virtualService.Scheduler)

	// Create and return the virtual service
	vs := &lib.VirtualService{
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
// Note: Caller must hold the lock
func (s *ModuleConfigState) GetInfo() *lib.BalancerInfo {
	info := s.cHandle.BalancerInfo()

	// Setup active sessions for virtual services
	summaryVsSessions := uint(0)
	for idx := range info.VsInfo {
		vs := &info.VsInfo[idx]
		vs.ActiveSessions = lib.AsyncInfo{
			Value:     s.VsActiveSessions[vs.VsIdentifier],
			UpdatedAt: s.ActiveSessionsUpdateTimestamp,
		}
		summaryVsSessions += vs.ActiveSessions.Value
	}

	// Set active session for reals
	summaryRealSessions := uint(0)
	for idx := range info.RealInfo {
		real := &info.RealInfo[idx]
		real.ActiveSessions = lib.AsyncInfo{
			Value:     s.RealActiveSessions[real.RealIdentifier],
			UpdatedAt: s.ActiveSessionsUpdateTimestamp,
		}
		summaryRealSessions += real.ActiveSessions.Value
	}

	// Log error, which should not occur
	if summaryVsSessions != summaryRealSessions ||
		summaryVsSessions != s.ActiveSessions {
		panic("active sessions invariant violation")
	}

	// Set active sessions
	info.ActiveSessions = lib.AsyncInfo{
		Value:     s.ActiveSessions,
		UpdatedAt: s.ActiveSessionsUpdateTimestamp,
	}

	return info
}
