package balancer

// BalancerManager implementation providing lifecycle management, configuration updates,
// real server management, and WLC (Weighted Least Connection) scheduling with automatic
// session table resizing and periodic refresh tasks.

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"go.uber.org/zap"
)

type BalancerManager struct {
	handle *ffi.BalancerManager

	realUpdateBuffer []ffi.RealUpdate

	// Background task management
	cancel context.CancelFunc

	mu sync.Mutex

	// Logger
	log *zap.SugaredLogger

	handlerMetrics handlersMetrics
}

func NewBalancerManager(
	handle *ffi.BalancerManager,
	log *zap.SugaredLogger,
) *BalancerManager {
	name := handle.Name()
	manager := &BalancerManager{
		handle:           handle,
		realUpdateBuffer: []ffi.RealUpdate{},
		log:              log.With("balancer", name),
		handlerMetrics:   newHandlersMetrics(),
	}
	manager.startBackgroundTasks()
	return manager
}

func (b *BalancerManager) newHandlerTracker(handle string, extraLabels ...metrics.Labels) *handlerMetricTracker {
	labels := metrics.Labels{
		"config": b.Name(),
	}
	for _, extra := range extraLabels {
		maps.Copy(labels, extra)
	}
	return newHandlerMetricTracker(handle, &b.handlerMetrics, defaultLatencyBoundsMS, labels)
}

func (b *BalancerManager) Name() string {
	return b.handle.Name()
}

func (b *BalancerManager) Update(
	config *balancerpb.BalancerConfig,
	now time.Time,
) (*ffi.UpdateInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("update")
	defer tracker.Fix()

	b.log.Debugw("updating balancer configuration")

	// Merge new config with current config for UPDATE mode
	mergedConfig, err := mergeBalancerConfig(config, b.handle.Config())
	if err != nil {
		b.log.Errorw("failed to merge config", "error", err)
		return nil, fmt.Errorf("failed to merge config: %w", err)
	}

	// Convert merged protobuf to FFI config
	ffiConfig, err := ProtoToFFIConfig(mergedConfig)
	if err != nil {
		b.log.Errorw("failed to convert config", "error", err)
		return nil, fmt.Errorf("failed to convert config: %w", err)
	}

	// Create WLC configuration with validation
	wlcConfig, err := createWlcConfig(mergedConfig)
	if err != nil {
		b.log.Errorw("failed to create WLC config", "error", err)
		return nil, fmt.Errorf("failed to create WLC config: %w", err)
	}

	// Create manager config
	managerConfig := &ffi.BalancerManagerConfig{
		Balancer:      ffiConfig,
		RefreshPeriod: mergedConfig.State.RefreshPeriod.AsDuration(),
		MaxLoadFactor: *mergedConfig.State.SessionTableMaxLoadFactor,
		Wlc:           wlcConfig,
	}

	// Update via FFI
	updateInfo, err := b.handle.Update(managerConfig, now)
	if err != nil {
		b.log.Errorw("failed to update manager", "error", err)
		return nil, fmt.Errorf("failed to update manager: %w", err)
	}

	// Log update information
	b.log.Infow("balancer configuration updated successfully",
		"vs_ipv4_matcher_reused", updateInfo.VsIpv4MatcherReused,
		"vs_ipv6_matcher_reused", updateInfo.VsIpv6MatcherReused,
		"acl_reused_vs_count", len(updateInfo.ACLReusedVs))

	if len(updateInfo.ACLReusedVs) > 0 {
		b.log.Debugw("ACL filters reused for virtual services",
			"count", len(updateInfo.ACLReusedVs),
			"vs_identifiers", updateInfo.ACLReusedVs)
	}

	// restart background tasks
	b.startBackgroundTasks()

	return updateInfo, nil
}

func (b *BalancerManager) UpdateReals(
	updates []*balancerpb.RealUpdate,
	buffer bool,
) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("update_reals", metrics.Labels{"buffer": strconv.FormatBool(buffer)})
	defer tracker.Fix()

	b.log.Debugw("updating reals", "count", len(updates), "buffer", buffer)

	// Convert protobuf updates to FFI updates
	ffiUpdates := make([]ffi.RealUpdate, 0, len(updates))
	for i, update := range updates {
		ffiUpdate, err := NewRealUpdateFromProto(update)
		if err != nil {
			b.log.Errorw("failed to convert update", "index", i, "error", err)
			return 0, fmt.Errorf(
				"failed to convert update at index %d: %w",
				i,
				err,
			)
		}
		ffiUpdates = append(ffiUpdates, *ffiUpdate)
	}

	if buffer {
		// Buffer the updates
		b.realUpdateBuffer = append(b.realUpdateBuffer, ffiUpdates...)
		b.log.Debugw(
			"real updates buffered",
			"count",
			len(updates),
			"total_buffered",
			len(b.realUpdateBuffer),
		)
		return len(updates), nil
	}

	// Apply immediately
	if err := b.handle.UpdateReals(ffiUpdates); err != nil {
		b.log.Errorw("failed to update reals", "error", err)
		return 0, err
	}

	b.log.Infow("real updates applied", "count", len(updates))
	return len(updates), nil
}

func (b *BalancerManager) FlushRealUpdates() (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("flush_real_updates")
	defer tracker.Fix()

	count := len(b.realUpdateBuffer)
	if count == 0 {
		b.log.Debugw("no buffered updates to flush")
		return 0, nil
	}

	b.log.Debugw("flushing buffered real updates", "count", count)

	// Apply buffered updates
	if err := b.handle.UpdateReals(b.realUpdateBuffer); err != nil {
		b.log.Errorw("failed to flush real updates", "error", err)
		return 0, err
	}

	// Clear buffer
	b.realUpdateBuffer = b.realUpdateBuffer[:0]

	b.log.Infow("buffered real updates flushed", "count", count)
	return count, nil
}

func (b *BalancerManager) Config() *balancerpb.BalancerConfig {
	b.mu.Lock()
	defer b.mu.Unlock()

	return ConvertBalancerConfigToProto(b.handle.Config())
}

func (b *BalancerManager) BufferedUpdates() []*balancerpb.RealUpdate {
	b.mu.Lock()
	defer b.mu.Unlock()

	updates := make([]*balancerpb.RealUpdate, len(b.realUpdateBuffer))
	for i := range b.realUpdateBuffer {
		updates[i] = ConvertFFIRealUpdateToProto(&b.realUpdateBuffer[i])
	}
	return updates
}

func (b *BalancerManager) Graph() *balancerpb.Graph {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("graph")
	defer tracker.Fix()

	cfg := b.handle.Config()
	graph := b.handle.Graph()

	return ConvertGraphToProtoWithConfig(graph, cfg)
}

func (b *BalancerManager) Info(
	now time.Time,
) (*balancerpb.BalancerInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("info")
	defer tracker.Fix()

	ffiInfo, err := b.handle.Info(now)
	if err != nil {
		return nil, err
	}

	return ConvertBalancerInfoToProto(ffiInfo), nil
}

func (b *BalancerManager) Stats(
	ref *balancerpb.PacketHandlerRef,
) (*balancerpb.BalancerStats, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("stats")
	defer tracker.Fix()

	// Convert protobuf ref to FFI ref
	ffiRef := &ffi.PacketHandlerRef{
		Device:   ref.Device,
		Pipeline: ref.Pipeline,
		Function: ref.Function,
		Chain:    ref.Chain,
	}

	ffiStats, err := b.handle.Stats(ffiRef)
	if err != nil {
		return nil, err
	}

	return ConvertBalancerStatsToProto(ffiStats), nil
}

////////////////////////////////////////////////////////////////////////////////

func (b *BalancerManager) Metrics(
	now time.Time,
	ref *balancerpb.PacketHandlerRef,
) ([]*commonpb.Metric, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("metrics")
	defer tracker.Fix()

	// Convert protobuf ref to FFI ref
	ffiRef := &ffi.PacketHandlerRef{
		Device:   ref.Device,
		Pipeline: ref.Pipeline,
		Function: ref.Function,
		Chain:    ref.Chain,
	}

	ffiStats, err := b.handle.Stats(ffiRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %s", err)
	}

	info, err := b.handle.Info(now)
	if err != nil {
		return nil, fmt.Errorf("failed to get info: %s", err)
	}

	config := b.handle.Config()

	refLabels := []*commonpb.Label{
		{Name: "device", Value: *ref.Device},
		{Name: "pipeline", Value: *ref.Pipeline},
		{Name: "function", Value: *ref.Function},
		{Name: "chain", Value: *ref.Chain},
		{Name: "config", Value: b.Name()},
	}

	makeCounter := func(name string, value uint64, extraLabels ...*commonpb.Label) *commonpb.Metric {
		metric := commonpb.Metric{
			Name:   name,
			Labels: append(refLabels, extraLabels...),
			Value:  &commonpb.Metric_Counter{Counter: value},
		}
		return &metric
	}

	makeGauge := func(name string, value float64, extraLabels ...*commonpb.Label) *commonpb.Metric {
		metric := commonpb.Metric{
			Name:   name,
			Labels: append(refLabels, extraLabels...),
			Value:  &commonpb.Metric_Gauge{Gauge: value},
		}
		return &metric
	}

	commonMetricsCount := len(
		commonCounters,
	) + 2 // +2 for active sessions and session table capacity (from info and config)

	perVsMetrics := len(
		vsCounters,
	) + 1 // +1 for active sessions (from info)
	perRealMetrics := len(
		realCounters,
	) + 1 // +1 for active sessions (from info)

	metricsCount := commonMetricsCount + perVsMetrics*len(ffiStats.Vs)

	for vsIdx := range ffiStats.Vs {
		vs := &ffiStats.Vs[vsIdx]
		metricsCount += perRealMetrics * len(vs.Reals)
	}

	metrics := make([]*commonpb.Metric, 0, metricsCount)

	// make common metrics
	{
		// active sessions and session table capacity
		metrics = append(
			metrics,
			makeGauge("active_sessions", float64(info.ActiveSessions)),
			makeGauge(
				"session_table_capacity",
				float64(config.Balancer.State.TableCapacity),
			),
		)

		// counters
		for _, counter := range commonCounters {
			metrics = append(
				metrics,
				makeCounter(counter.name, counter.getter(ffiStats)),
			)
		}
	}

	// make vs metrics
	for vsIdx := range ffiStats.Vs {
		vs := &ffiStats.Vs[vsIdx]
		vsInfo := &info.Vs[vsIdx]
		labelsVS := []*commonpb.Label{
			{Name: "vip", Value: vs.Identifier.Addr.String()},
			{Name: "port", Value: strconv.Itoa(int(vs.Identifier.Port))},
			{Name: "protocol", Value: vs.Identifier.TransportProto.String()},
		}

		// active sessions
		metrics = append(
			metrics,
			makeGauge(
				"vs_active_sessions",
				float64(vsInfo.ActiveSessions),
				labelsVS...,
			),
		)

		// counters
		for _, counter := range vsCounters {
			metrics = append(
				metrics,
				makeCounter(
					counter.name,
					counter.getter(&vs.Stats),
					labelsVS...),
			)
		}

		// make real metrics
		for realIdx := range vs.Reals {
			real := &vs.Reals[realIdx]
			realInfo := &vsInfo.Reals[realIdx]
			labelsReal := append(labelsVS, &commonpb.Label{Name: "real_ip", Value: real.Dst.String()})

			// active sessions
			metrics = append(
				metrics,
				makeGauge(
					"real_active_sessions",
					float64(realInfo.ActiveSessions),
					labelsReal...,
				),
			)

			// counters
			for _, counter := range realCounters {
				metrics = append(
					metrics,
					makeCounter(
						counter.name,
						counter.getter(&real.Stats),
						labelsReal...,
					),
				)
			}
		}

		// make acl metrics
		for aclIdx := range vs.AllowedSources {
			acl := &vs.AllowedSources[aclIdx]
			labelsACL := append(labelsVS, &commonpb.Label{Name: "acl_tag", Value: acl.Tag})

			metrics = append(
				metrics,
				makeCounter(
					"vs_acl_hits",
					acl.Passes,
					labelsACL...,
				),
			)
		}
	}

	calls := b.handlerMetrics.collect()

	return append(metrics, calls...), nil
}

func (b *BalancerManager) Sessions(
	now time.Time,
) ([]*balancerpb.SessionInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("sessions")
	defer tracker.Fix()

	ffiSessions := b.handle.Sessions(now)

	sessions := make([]*balancerpb.SessionInfo, 0, len(ffiSessions.Sessions))
	for i := range ffiSessions.Sessions {
		sessions = append(sessions, ConvertSessionInfoToProto(
			&ffiSessions.Sessions[i].Identifier,
			&ffiSessions.Sessions[i].Info,
		))
	}

	return sessions, nil
}

func (b *BalancerManager) startBackgroundTasks() {
	b.stopBackgroundTasks()

	if b.handle.Config().RefreshPeriod == 0 {
		return
	}

	var ctx context.Context
	ctx, b.cancel = context.WithCancel(context.Background())

	// Start background refresh task
	go b.backgroundRefreshTask(ctx)
}

// backgroundRefreshTask runs periodically to:
// 1. Get balancer info
// 2. Resize session table if load factor exceeds threshold
// 3. Adjust WLC weights based on active connections
// 4. Apply real updates if needed
func (b *BalancerManager) backgroundRefreshTask(ctx context.Context) {
	for {
		// Get current config to check refresh period
		b.mu.Lock()
		config := b.handle.Config()
		refreshPeriod := config.RefreshPeriod
		b.mu.Unlock()

		// If refresh period is zero, stop the task
		if refreshPeriod == 0 {
			b.log.Debugw(
				"background refresh task stopped (refresh_period is zero)",
			)
			return
		}

		// Wait for refresh period or context cancellation
		select {
		case <-ctx.Done():
			b.log.Debugw("background refresh task stopped (context cancelled)")
			return
		case <-time.After(refreshPeriod):
			// Continue with refresh
		}

		now := time.Now()

		// Perform refresh with error handling
		if err := b.Refresh(now); err != nil {
			b.log.Errorw("background refresh failed", "error", err)
		}
	}
}

// Refresh executes the refresh logic
func (b *BalancerManager) Refresh(now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("refresh")
	defer tracker.Fix()

	b.log.Info("refreshing state")

	// Get current config
	config := b.handle.Config()

	// Get balancer info
	info, err := b.handle.Info(now)
	if err != nil {
		return fmt.Errorf("failed to get info: %w", err)
	}

	// Check if session table needs resizing
	capacity := config.Balancer.State.TableCapacity
	activeSessions := info.ActiveSessions
	maxLoadFactor := config.MaxLoadFactor

	currentLoadFactor := float32(activeSessions) / float32(capacity)

	b.log.Debugw("fetched balancer info",
		"current_capacity", capacity,
		"active_sessions", activeSessions,
		"current_load_factor", currentLoadFactor,
		"max_load_factor", maxLoadFactor)

	if currentLoadFactor > maxLoadFactor {
		newCapacity := capacity * 2
		b.log.Infow("resizing session table",
			"current_capacity", capacity,
			"new_capacity", newCapacity,
			"active_sessions", activeSessions,
			"current_load_factor", currentLoadFactor,
			"max_load_factor", maxLoadFactor)

		if err := b.handle.ResizeSessionTable(newCapacity, now); err != nil {
			b.log.Errorw("failed to resize session table", "error", err)
		} else {
			b.log.Infow("session table resized successfully", "new_capacity", newCapacity)
		}
	}

	// WLC real updates - use UpdateRealsWlc to preserve config weights
	updates := WlcUpdates(b.handle.Config(), b.handle.Graph(), info)

	b.log.Debugw("calculated WLC updates", "count", len(updates))

	if len(updates) > 0 {
		b.log.Infow("applying WLC updates", "count", len(updates))
		if err := b.handle.UpdateRealsWlc(updates); err != nil {
			b.log.Errorw("failed to apply WLC updates", "error", err)
		} else {
			b.log.Infow("WLC updates applied successfully", "count", len(updates))
		}
	}

	return nil
}

func (b *BalancerManager) stopBackgroundTasks() {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

func (b *BalancerManager) Free() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopBackgroundTasks()
}

// UpdateVS updates specific virtual services in the balancer configuration.
//
// This method takes the current FFI config, updates/adds the specified virtual
// services (provided in protobuf format), and calls the FFI update. The ACL
// reuse list in the returned UpdateInfo only contains virtual services from
// the update request.
//
// Behavior:
// - Virtual services in the request that already exist are replaced
// - Virtual services in the request that don't exist are added
// - Virtual services not in the request remain unchanged
func (b *BalancerManager) UpdateVS(
	vsList []*balancerpb.VirtualService,
	now time.Time,
) (*ffi.UpdateInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("update_vs")
	defer tracker.Fix()

	b.log.Debugw("updating virtual services", "vs_count", len(vsList))

	// Convert protobuf VS list to FFI format
	ffiVsList := make([]ffi.VsConfig, 0, len(vsList))
	for i, protoVs := range vsList {
		ffiVs, err := protoToVsConfig(protoVs)
		if err != nil {
			b.log.Errorw("failed to convert VS", "index", i, "error", err)
			return nil, fmt.Errorf(
				"failed to convert VS at index %d: %w",
				i,
				err,
			)
		}
		ffiVsList = append(ffiVsList, ffiVs)
	}

	// Get current config
	currentConfig := b.handle.Config()

	// Build a map of VS identifiers from the request for quick lookup
	requestedVsIds := make(map[ffi.VsIdentifier]bool)
	for _, vs := range ffiVsList {
		requestedVsIds[vs.Identifier] = true
	}

	// Create new VS list: keep existing VS that are not in the request,
	// then add/replace with VS from the request
	newVsList := make([]ffi.VsConfig, 0)

	// Keep existing VS that are not being updated
	for _, existingVs := range currentConfig.Balancer.Handler.VirtualServices {
		if !requestedVsIds[existingVs.Identifier] {
			newVsList = append(newVsList, existingVs)
		}
	}

	// Add VS from the request (these are new or updated)
	newVsList = append(newVsList, ffiVsList...)

	// Update WLC config - recalculate which VS indices have WLC enabled
	newWlcVs := make([]uint32, 0)
	wlcEnabledOld := make(map[ffi.VsIdentifier]bool)
	for _, vsIdx := range currentConfig.Wlc.Vs {
		if int(vsIdx) < len(currentConfig.Balancer.Handler.VirtualServices) {
			wlcEnabledOld[currentConfig.Balancer.Handler.VirtualServices[vsIdx].Identifier] = true
		}
	}
	// Check which VS in the request have WLC enabled
	wlcEnabledNew := make(map[ffi.VsIdentifier]bool)
	for _, protoVs := range vsList {
		if protoVs.Flags != nil && protoVs.Flags.Wlc {
			ffiVs, _ := protoToVsConfig(protoVs)
			wlcEnabledNew[ffiVs.Identifier] = true
		}
	}
	// For new VS list, find indices of VS that have WLC enabled
	for i, vs := range newVsList {
		// If VS is in the request, use the new WLC flag; otherwise use the old one
		if requestedVsIds[vs.Identifier] {
			if wlcEnabledNew[vs.Identifier] {
				newWlcVs = append(newWlcVs, uint32(i))
			}
		} else if wlcEnabledOld[vs.Identifier] {
			newWlcVs = append(newWlcVs, uint32(i))
		}
	}

	// Create updated manager config
	updatedConfig := &ffi.BalancerManagerConfig{
		Balancer: ffi.BalancerConfig{
			Handler: ffi.PacketHandlerConfig{
				SessionsTimeouts: currentConfig.Balancer.Handler.SessionsTimeouts,
				VirtualServices:  newVsList,
				SourceV4:         currentConfig.Balancer.Handler.SourceV4,
				SourceV6:         currentConfig.Balancer.Handler.SourceV6,
				DecapV4:          currentConfig.Balancer.Handler.DecapV4,
				DecapV6:          currentConfig.Balancer.Handler.DecapV6,
			},
			State: currentConfig.Balancer.State,
		},
		Wlc: ffi.BalancerManagerWlcConfig{
			Power:         currentConfig.Wlc.Power,
			MaxRealWeight: currentConfig.Wlc.MaxRealWeight,
			Vs:            newWlcVs,
		},
		RefreshPeriod: currentConfig.RefreshPeriod,
		MaxLoadFactor: currentConfig.MaxLoadFactor,
	}

	// Update via FFI
	updateInfo, err := b.handle.Update(updatedConfig, now)
	if err != nil {
		b.log.Errorw("failed to update manager", "error", err)
		return nil, fmt.Errorf("failed to update manager: %w", err)
	}

	// Filter ACL reuse list to only include VS from the update request
	filteredUpdateInfo := filterACLReusesForRequestedVs(
		updateInfo,
		requestedVsIds,
	)

	b.log.Infow("virtual services updated successfully",
		"vs_count", len(vsList),
		"vs_ipv4_matcher_reused", filteredUpdateInfo.VsIpv4MatcherReused,
		"vs_ipv6_matcher_reused", filteredUpdateInfo.VsIpv6MatcherReused,
		"acl_reused_vs_count", len(filteredUpdateInfo.ACLReusedVs))

	return filteredUpdateInfo, nil
}

// DeleteVS deletes specific virtual services from the balancer configuration.
//
// This method takes the current FFI config, removes the specified virtual
// services (identified by protobuf VS list), and calls the FFI update. The ACL
// reuse list in the returned UpdateInfo is always empty since deleted VSs don't
// have ACL filters to reuse.
//
// Behavior:
// - Virtual services matching the identifiers in the request are removed
// - Virtual services not in the request remain unchanged
// - Deleting a non-existent VS is not an error (idempotent)
func (b *BalancerManager) DeleteVS(
	vsList []*balancerpb.VirtualService,
	now time.Time,
) (*ffi.UpdateInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	tracker := b.newHandlerTracker("delete_vs")
	defer tracker.Fix()

	b.log.Debugw("deleting virtual services", "vs_count", len(vsList))

	// Get current config
	currentConfig := b.handle.Config()

	// Build a map of VS identifiers to delete for quick lookup
	vsToDelete := make(map[ffi.VsIdentifier]bool)
	for _, protoVs := range vsList {
		if protoVs.Id != nil {
			vsID := protoVsIdentifierToFFI(protoVs.Id)
			vsToDelete[vsID] = true
		}
	}

	// Create new VS list without the deleted VS
	newVsList := make([]ffi.VsConfig, 0)

	for _, existingVs := range currentConfig.Balancer.Handler.VirtualServices {
		if !vsToDelete[existingVs.Identifier] {
			newVsList = append(newVsList, existingVs)
		}
	}

	// Update WLC config - recalculate which VS indices have WLC enabled
	newWlcVs := make([]uint32, 0)
	wlcEnabledOld := make(map[ffi.VsIdentifier]bool)
	for _, vsIdx := range currentConfig.Wlc.Vs {
		if int(vsIdx) < len(currentConfig.Balancer.Handler.VirtualServices) {
			wlcEnabledOld[currentConfig.Balancer.Handler.VirtualServices[vsIdx].Identifier] = true
		}
	}
	// For new VS list, find indices of VS that had WLC enabled
	for i, vs := range newVsList {
		if wlcEnabledOld[vs.Identifier] {
			newWlcVs = append(newWlcVs, uint32(i))
		}
	}

	// Create updated manager config
	updatedConfig := &ffi.BalancerManagerConfig{
		Balancer: ffi.BalancerConfig{
			Handler: ffi.PacketHandlerConfig{
				SessionsTimeouts: currentConfig.Balancer.Handler.SessionsTimeouts,
				VirtualServices:  newVsList,
				SourceV4:         currentConfig.Balancer.Handler.SourceV4,
				SourceV6:         currentConfig.Balancer.Handler.SourceV6,
				DecapV4:          currentConfig.Balancer.Handler.DecapV4,
				DecapV6:          currentConfig.Balancer.Handler.DecapV6,
			},
			State: currentConfig.Balancer.State,
		},
		Wlc: ffi.BalancerManagerWlcConfig{
			Power:         currentConfig.Wlc.Power,
			MaxRealWeight: currentConfig.Wlc.MaxRealWeight,
			Vs:            newWlcVs,
		},
		RefreshPeriod: currentConfig.RefreshPeriod,
		MaxLoadFactor: currentConfig.MaxLoadFactor,
	}

	// Update via FFI
	updateInfo, err := b.handle.Update(updatedConfig, now)
	if err != nil {
		b.log.Errorw("failed to update manager", "error", err)
		return nil, fmt.Errorf("failed to update manager: %w", err)
	}

	// For delete, ACL reuse list should be empty
	updateInfo.ACLReusedVs = []ffi.VsIdentifier{}

	b.log.Infow("virtual services deleted successfully",
		"vs_count", len(vsList),
		"vs_ipv4_matcher_reused", updateInfo.VsIpv4MatcherReused,
		"vs_ipv6_matcher_reused", updateInfo.VsIpv6MatcherReused)

	return updateInfo, nil
}

// protoVsIdentifierToFFI converts a protobuf VS identifier to FFI format
func protoVsIdentifierToFFI(id *balancerpb.VsIdentifier) ffi.VsIdentifier {
	var addr netip.Addr
	if id.Addr != nil {
		addr, _ = netip.AddrFromSlice(id.Addr.Bytes)
	}

	proto := ffi.VsTransportProtoUDP
	if id.Proto == balancerpb.TransportProto_TCP {
		proto = ffi.VsTransportProtoTCP
	}

	return ffi.VsIdentifier{
		Addr:           addr,
		Port:           uint16(id.Port),
		TransportProto: proto,
	}
}

// filterACLReusesForRequestedVs filters the ACL reuse list to only include
// VS identifiers that were in the original update request.
// This ensures the response only reports reuse status for VSs that were
// actually part of the update operation.
func filterACLReusesForRequestedVs(
	info *ffi.UpdateInfo,
	requestedVsIds map[ffi.VsIdentifier]bool,
) *ffi.UpdateInfo {
	if info == nil {
		return nil
	}

	filtered := &ffi.UpdateInfo{
		VsIpv4MatcherReused: info.VsIpv4MatcherReused,
		VsIpv6MatcherReused: info.VsIpv6MatcherReused,
		ACLReusedVs:         make([]ffi.VsIdentifier, 0),
	}

	for _, vsID := range info.ACLReusedVs {
		if requestedVsIds[vsID] {
			filtered.ACLReusedVs = append(filtered.ACLReusedVs, vsID)
		}
	}

	return filtered
}
