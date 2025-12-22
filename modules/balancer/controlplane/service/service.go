package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// gRPC service for controlling balancer module instances
type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	mu sync.Mutex

	instances map[string]*module.Balancer
	agent     *ffi.Agent
	log       *zap.SugaredLogger
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(
	agent *ffi.Agent,
	log *zap.SugaredLogger,
) *BalancerService {
	log.Info("initializing balancer service")
	return &BalancerService{
		mu:        sync.Mutex{},
		agent:     agent,
		instances: make(map[string]*module.Balancer),
		log:       log,
	}
}

////////////////////////////////////////////////////////////////////////////////

// UpdateConfig updates or enables balancer config
func (m *BalancerService) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Check if balancer exists (hold lock only for map access)
	m.mu.Lock()
	existingBalancer, exists := m.instances[name]
	m.mu.Unlock()

	if exists {
		// Update existing balancer (no service lock held)
		m.log.Infow("updating existing balancer", "name", name)
		if err := existingBalancer.Update(req.ModuleConfig, req.ModuleStateConfig); err != nil {
			m.log.Errorw("failed to update balancer", "name", name, "error", err)
			return nil, fmt.Errorf("failed to update balancer: %w", err)
		}
		m.log.Infow("balancer updated successfully", "name", name)
		return &balancerpb.UpdateConfigResponse{}, nil
	}

	// Create new balancer (no service lock held during creation)
	m.log.Infow("creating new balancer", "name", name)
	balancerLog := m.log.With("balancer", name)
	newBalancer, err := module.NewBalancerFromProto(
		*m.agent,
		name,
		req.ModuleConfig,
		req.ModuleStateConfig,
		balancerLog,
	)
	if err != nil {
		m.log.Errorw("failed to create balancer", "name", name, "error", err)
		return nil, fmt.Errorf("failed to create balancer: %w", err)
	}

	// Add to map (hold lock only for map modification)
	m.mu.Lock()
	m.instances[name] = newBalancer
	m.mu.Unlock()

	m.log.Infow("balancer created successfully", "name", name)
	return &balancerpb.UpdateConfigResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// UpdateReals updates reals with optional buffering
func (m *BalancerService) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer [name=%s] not found", name)
	}

	m.log.Debugw("updating reals", "name", name, "count", len(req.Updates), "buffer", req.Buffer)

	// Parse real updates
	updates := make([]lib.RealUpdate, 0, len(req.Updates))
	for i, protoUpdate := range req.Updates {
		update, err := lib.NewRealUpdateFromProto(protoUpdate)
		if err != nil {
			m.log.Errorw("failed to parse real update", "name", name, "index", i, "error", err)
			return nil, fmt.Errorf("failed to parse update at index %d: %w", i, err)
		}
		updates = append(updates, *update)
	}

	// Apply updates (no service lock held)
	if err := balancerInstance.UpdateReals(updates, req.Buffer); err != nil {
		m.log.Errorw("failed to update reals", "name", name, "error", err)
		return nil, fmt.Errorf("failed to update reals: %w", err)
	}

	m.log.Infow("reals updated successfully", "name", name, "count", len(updates), "buffered", req.Buffer)
	return &balancerpb.UpdateRealsResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// FlushRealUpdates flushes buffered reals updates
func (m *BalancerService) FlushRealUpdates(
	ctx context.Context,
	req *balancerpb.FlushRealUpdatesRequest,
) (*balancerpb.FlushRealUpdatesResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("flushing real updates", "name", name)

	// Flush updates (no service lock held)
	count, err := balancerInstance.FlushRealUpdates()
	if err != nil {
		m.log.Warnw(
			"failed to flush real updates",
			"name",
			name,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to flush real updates for balancer %s: %w", name, err)
	}

	m.log.Infow("real updates flushed", "name", name, "count", count)
	return &balancerpb.FlushRealUpdatesResponse{
		UpdatesFlushed: uint32(count),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowConfig shows balancer config
func (m *BalancerService) ShowConfig(
	ctx context.Context,
	req *balancerpb.ShowConfigRequest,
) (*balancerpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("showing config", "name", name)

	// Get config (no service lock held)
	moduleConfigProto, moduleStateConfigProto := balancerInstance.GetConfig()

	return &balancerpb.ShowConfigResponse{
		Name:              name,
		ModuleConfig:      moduleConfigProto,
		ModuleStateConfig: moduleStateConfigProto,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ListConfigs lists balancer configs
func (m *BalancerService) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.Debugw("listing configs", "count", len(m.instances))

	configs := make([]*balancerpb.ShowConfigResponse, 0, len(m.instances))

	for name, balancerInstance := range m.instances {
		moduleConfigProto, moduleStateConfigProto := balancerInstance.GetConfig()

		config := &balancerpb.ShowConfigResponse{
			Name:              name,
			ModuleConfig:      moduleConfigProto,
			ModuleStateConfig: moduleStateConfigProto,
		}
		configs = append(configs, config)
	}

	return &balancerpb.ListConfigsResponse{
		Configs: configs,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// StateInfo returns info of the balancer state
func (m *BalancerService) StateInfo(
	ctx context.Context,
	req *balancerpb.StateInfoRequest,
) (*balancerpb.StateInfoResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting state info", "name", name)

	// Get state info (no service lock held)
	info := balancerInstance.GetStateInfo(time.Now())

	return &balancerpb.StateInfoResponse{
		Name: name,
		Info: info.IntoProto(),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ConfigStats returns stats of the balancer config
func (m *BalancerService) ConfigStats(
	ctx context.Context,
	req *balancerpb.ConfigStatsRequest,
) (*balancerpb.ConfigStatsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting config stats", "name", name)

	// Get config stats (no service lock held)
	stats := balancerInstance.GetConfigStats(
		req.Device,
		req.Pipeline,
		req.Function,
		req.Chain,
	)

	return &balancerpb.ConfigStatsResponse{
		Name:     name,
		Device:   req.Device,
		Pipeline: req.Pipeline,
		Function: req.Function,
		Chain:    req.Chain,
		Stats:    stats.IntoProto(),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// SessionsInfo returns info about active balancer sessions
func (m *BalancerService) SessionsInfo(
	ctx context.Context,
	req *balancerpb.SessionsInfoRequest,
) (*balancerpb.SessionsInfoResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.instances[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting sessions info", "name", name)

	// Get sessions info (no service lock held)
	sessionsInfo, err := balancerInstance.GetSessionsInfo(time.Now())
	if err != nil {
		m.log.Errorw("failed to get sessions info", "name", name, "error", err)
		return nil, fmt.Errorf("failed to get sessions info: %w", err)
	}

	// Convert to protobuf
	sessionsPb := make([]*balancerpb.SessionInfo, 0, len(sessionsInfo.Sessions))
	for idx := range sessionsInfo.Sessions {
		sessionsPb = append(sessionsPb, sessionsInfo.Sessions[idx].IntoProto())
	}

	m.log.Infow("sessions info retrieved", "name", name, "count", sessionsInfo.SessionsCount)

	return &balancerpb.SessionsInfoResponse{
		Name:         name,
		SessionsInfo: sessionsPb,
	}, nil
}
