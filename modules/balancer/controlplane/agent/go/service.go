package balancer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent/go/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// gRPC service for controlling balancer module instances
type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	mu sync.Mutex

	balancers map[string]*Balancer
	agent     ffi.BalancerAgent
	log       *zap.SugaredLogger

	// Background task management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(
	shm *yanet.SharedMemory,
	memory uint,
	log *zap.SugaredLogger,
) *BalancerService {
	log.Info("initializing balancer service")

	// Create balancer agent
	balancerAgent, err := ffi.NewBalancerAgent(shm, memory)
	if err != nil {
		log.Errorw("failed to create balancer agent", "error", err)
		// Fall back to empty service
		return &BalancerService{
			mu:        sync.Mutex{},
			agent:     balancerAgent,
			balancers: make(map[string]*Balancer),
			log:       log,
		}
	}

	instances := make(map[string]*Balancer)

	// Get existing balancers from the agent using balancer_agent_balancers
	existingBalancers, err := balancerAgent.ListBalancers()
	if err != nil {
		log.Errorw("failed to list existing balancers", "error", err)
	} else if len(existingBalancers.Balancers) > 0 {
		log.Infow("found existing balancers", "count", len(existingBalancers.Balancers))

		// Store each existing balancer in instances map and start background tasks
		for _, agentBalancer := range existingBalancers.Balancers {
			name := agentBalancer.Config.BalancerName
			log.Infow("registering existing balancer", "name", name)

			// Create Balancer wrapper
			balancerLog := log.With("balancer", name)
			b := &Balancer{
				agent:            agent,
				balancer:         *agentBalancer.Handle,
				config:           &agentBalancer.Config.BalancerConfig,
				realUpdateBuffer: make([]ffi.RealUpdate, 0),
				reals:            make(map[ffi.RealIdentifier]RealState),
				log:              balancerLog,
			}

			// Populate reals map from config
			graph := agentBalancer.Handle.Graph()
			for _, vs := range graph.VirtualServices {
				for _, graphReal := range vs.Reals {
					realId := ffi.RealIdentifier{
						Vs: vs.Identifier,
						Relative: ffi.RelativeRealIdentifier{
							Ip:   graphReal.Identifier,
							Port: vs.Identifier.Port,
						},
					}
					b.reals[realId] = RealState{
						enabled:         graphReal.Enabled,
						activeSessions:  0,
						effectiveWeight: uint64(graphReal.Weight),
					}
				}
			}

			instances[name] = b
		}
	}

	// Create service with context for background tasks
	ctx, cancel := context.WithCancel(context.Background())
	service := &BalancerService{
		mu:            sync.Mutex{},
		agent:         agent,
		balancerAgent: balancerAgent,
		balancers:     instances,
		log:           log,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start background tasks for existing balancers with weight adjustment
	for name, balancer := range instances {
		// Get the agent config to check for weight adjustment settings
		agentBalancers, err := balancerAgent.ListBalancers()
		if err == nil {
			for _, agentBal := range agentBalancers.Balancers {
				if agentBal.Config.BalancerName == name && len(agentBal.Config.AdjustWeightsVs) > 0 {
					log.Infow("starting weight adjustment for balancer",
						"name", name,
						"vs_count", len(agentBal.Config.AdjustWeightsVs),
						"refresh_period_ms", agentBal.Config.RefreshPeriod)
					service.startWeightAdjustmentTask(balancer, agentBal.Config)
				}
			}
		}
	}

	return service
}

////////////////////////////////////////////////////////////////////////////////

// UpdateConfig updates or enables balancer config
func (m *BalancerService) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Check if balancer exists (hold lock only for map access)
	m.mu.Lock()
	existingBalancer, exists := m.balancers[name]
	m.mu.Unlock()

	if exists {
		// Update existing balancer (no service lock held)
		m.log.Infow("updating existing balancer", "name", name)
		if err := existingBalancer.Update(req.Config); err != nil {
			m.log.Errorw(
				"failed to update balancer",
				"name",
				name,
				"error",
				err,
			)
			return nil, fmt.Errorf("failed to update balancer: %w", err)
		}
		m.log.Infow("balancer updated successfully", "name", name)
		return &balancerpb.UpdateConfigResponse{}, nil
	}

	// Create new balancer (no service lock held during creation)
	m.log.Infow("creating new balancer", "name", name)
	balancerLog := m.log.With("balancer", name)
	newBalancer, err := NewBalancerFromProto(
		*m.agent,
		name,
		req.Config,
		balancerLog,
	)
	if err != nil {
		m.log.Errorw("failed to create balancer", "name", name, "error", err)
		return nil, fmt.Errorf("failed to create balancer: %w", err)
	}

	// Add to map (hold lock only for map modification)
	m.mu.Lock()
	m.balancers[name] = newBalancer
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
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer [name=%s] not found", name)
	}

	m.log.Debugw(
		"updating reals",
		"name",
		name,
		"count",
		len(req.Updates),
		"buffer",
		req.Buffer,
	)

	// Parse real updates
	updates := make([]ffi.RealUpdate, 0, len(req.Updates))
	for i, protoUpdate := range req.Updates {
		update, err := NewRealUpdateFromProto(protoUpdate)
		if err != nil {
			m.log.Errorw(
				"failed to parse real update",
				"name",
				name,
				"index",
				i,
				"error",
				err,
			)
			return nil, fmt.Errorf(
				"failed to parse update at index %d: %w",
				i,
				err,
			)
		}
		updates = append(updates, *update)
	}

	// Apply updates (no service lock held)
	if err := balancerInstance.UpdateReals(updates, req.Buffer); err != nil {
		m.log.Errorw("failed to update reals", "name", name, "error", err)
		return nil, fmt.Errorf("failed to update reals: %w", err)
	}

	m.log.Infow(
		"reals updated successfully",
		"name",
		name,
		"count",
		len(updates),
		"buffered",
		req.Buffer,
	)
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
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
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
		return nil, fmt.Errorf(
			"failed to flush real updates for balancer %s: %w",
			name,
			err,
		)
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
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("showing config", "name", name)

	// Get config (no service lock held)
	_ = balancerInstance.GetConfig()

	// Note: config is *ffi.BalancerConfig, not *balancerpb.BalancerConfig
	// Conversion would be needed here, but for now return error
	return nil, fmt.Errorf("ShowConfig not fully implemented - config conversion needed")
}

////////////////////////////////////////////////////////////////////////////////

// ListConfigs lists balancer configs
func (m *BalancerService) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.Debugw("listing configs", "count", len(m.balancers))

	names := make([]string, 0, len(m.balancers))
	for name := range m.balancers {
		names = append(names, name)
	}

	return &balancerpb.ListConfigsResponse{
		Configs: names,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowInfo returns info of the balancer state
func (m *BalancerService) ShowInfo(
	ctx context.Context,
	req *balancerpb.ShowInfoRequest,
) (*balancerpb.ShowInfoResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting state info", "name", name)

	// Get state info (no service lock held)
	info := balancerInstance.GetStateInfo(time.Now())

	return &balancerpb.ShowInfoResponse{
		Name: name,
		Info: ConvertBalancerInfoToProto(info),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowStats returns stats of the balancer config
func (m *BalancerService) ShowStats(
	ctx context.Context,
	req *balancerpb.ShowStatsRequest,
) (*balancerpb.ShowStatsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting config stats", "name", name)

	// Get config stats (no service lock held)
	device := ""
	if req.Device != nil {
		device = *req.Device
	}
	pipeline := ""
	if req.Pipeline != nil {
		pipeline = *req.Pipeline
	}
	function := ""
	if req.Function != nil {
		function = *req.Function
	}
	chain := ""
	if req.Chain != nil {
		chain = *req.Chain
	}

	info := balancerInstance.GetConfigStats(device, pipeline, function, chain)

	return &balancerpb.ShowStatsResponse{
		Name:     name,
		Device:   device,
		Pipeline: pipeline,
		Function: function,
		Chain:    chain,
		Stats:    ConvertBalancerStatsToProto(info),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowSessions returns info about active balancer sessions
func (m *BalancerService) ShowSessions(
	ctx context.Context,
	req *balancerpb.ShowSessionsRequest,
) (*balancerpb.ShowSessionsResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	// Get balancer instance (hold lock only for map access)
	m.mu.Lock()
	balancerInstance, exists := m.balancers[name]
	m.mu.Unlock()

	if !exists {
		m.log.Warnw("balancer not found", "name", name)
		return nil, fmt.Errorf("balancer %s not found", name)
	}

	m.log.Debugw("getting sessions info", "name", name)

	// Get sessions info (no service lock held)
	sessions, err := balancerInstance.GetSessionsInfo(time.Now())
	if err != nil {
		m.log.Errorw("failed to get sessions info", "name", name, "error", err)
		return nil, fmt.Errorf("failed to get sessions info: %w", err)
	}

	// Convert to protobuf
	sessionsPb := make([]*balancerpb.SessionInfo, 0, len(sessions))
	for idx := range sessions {
		sessionsPb = append(
			sessionsPb,
			ConvertSessionInfoToProto(&sessions[idx]),
		)
	}

	m.log.Infow("sessions info retrieved", "name", name, "count", len(sessions))

	return &balancerpb.ShowSessionsResponse{
		Sessions: sessionsPb,
	}, nil
}
