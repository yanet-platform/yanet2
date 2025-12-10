package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancer"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// Module instance identifier
type moduleKey struct {
	name              string
	dataplaneInstance uint32
}

////////////////////////////////////////////////////////////////////////////////

// gRPC service for controlling balancer module instances
type BalancerService struct {
	balancerpb.UnimplementedBalancerServer

	mu sync.Mutex

	instances map[moduleKey]*balancer.Balancer
	agents    []ffi.Agent
	log       *zap.SugaredLogger
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(
	agents []ffi.Agent,
	log *zap.SugaredLogger,
) *BalancerService {
	log.Info("initializing balancer service")
	return &BalancerService{
		mu:        sync.Mutex{},
		agents:    agents,
		instances: make(map[moduleKey]*balancer.Balancer),
		log:       log,
	}
}

////////////////////////////////////////////////////////////////////////////////

// UpdateConfig updates or enables balancer config
func (service *BalancerService) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	if int(inst) >= len(service.agents) {
		service.log.Errorw(
			"invalid dataplane instance",
			"name",
			name,
			"instance",
			inst,
			"max",
			len(service.agents)-1,
		)
		return nil, fmt.Errorf(
			"invalid dataplane instance: %d (max: %d)",
			inst,
			len(service.agents)-1,
		)
	}

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Check if balancer exists (hold lock only for map access)
	service.mu.Lock()
	existingBalancer, exists := service.instances[key]
	service.mu.Unlock()

	if exists {
		// Update existing balancer (no service lock held)
		service.log.Infow(
			"updating existing balancer",
			"name",
			name,
			"instance",
			inst,
		)
		if err := existingBalancer.Update(req.ModuleConfig, req.ModuleStateConfig); err != nil {
			service.log.Errorw(
				"failed to update balancer",
				"name",
				name,
				"instance",
				inst,
				"error",
				err,
			)
			return nil, fmt.Errorf("failed to update balancer: %w", err)
		}
		service.log.Infow(
			"balancer updated successfully",
			"name",
			name,
			"instance",
			inst,
		)
		return &balancerpb.UpdateConfigResponse{}, nil
	}

	// Create new balancer (no service lock held during creation)
	service.log.Infow("creating new balancer", "name", name, "instance", inst)
	balancerLog := service.log.With("balancer", name, "instance", inst)
	newBalancer, err := balancer.NewBalancerFromProto(
		service.agents[inst],
		name,
		req.ModuleConfig,
		req.ModuleStateConfig,
		balancerLog,
	)
	if err != nil {
		service.log.Errorw(
			"failed to create balancer",
			"name",
			name,
			"instance",
			inst,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to create balancer: %w", err)
	}

	// Add to map (hold lock only for map modification)
	service.mu.Lock()
	service.instances[key] = newBalancer
	service.mu.Unlock()

	service.log.Infow(
		"balancer created successfully",
		"name",
		name,
		"instance",
		inst,
	)
	return &balancerpb.UpdateConfigResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// UpdateReals updates reals with optional buffering
func (service *BalancerService) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw(
		"updating reals",
		"name",
		name,
		"instance",
		inst,
		"count",
		len(req.Updates),
		"buffer",
		req.Buffer,
	)

	// Parse real updates
	updates := make([]module.RealUpdate, 0, len(req.Updates))
	for i, protoUpdate := range req.Updates {
		update, err := module.NewRealUpdateFromProto(protoUpdate)
		if err != nil {
			service.log.Errorw(
				"failed to parse real update",
				"name",
				name,
				"instance",
				inst,
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
		service.log.Errorw(
			"failed to update reals",
			"name",
			name,
			"instance",
			inst,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to update reals: %w", err)
	}

	service.log.Infow(
		"reals updated successfully",
		"name",
		name,
		"instance",
		inst,
		"count",
		len(updates),
		"buffered",
		req.Buffer,
	)
	return &balancerpb.UpdateRealsResponse{}, nil
}

////////////////////////////////////////////////////////////////////////////////

// FlushRealUpdates flushes buffered reals updates
func (service *BalancerService) FlushRealUpdates(
	ctx context.Context,
	req *balancerpb.FlushRealUpdatesRequest,
) (*balancerpb.FlushRealUpdatesResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw("flushing real updates", "name", name, "instance", inst)

	// Flush updates (no service lock held)
	count, err := balancerInstance.FlushRealUpdates()
	if err != nil {
		service.log.Errorw(
			"failed to flush real updates",
			"name",
			name,
			"instance",
			inst,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to flush real updates: %w", err)
	}

	service.log.Infow(
		"real updates flushed",
		"name",
		name,
		"instance",
		inst,
		"count",
		count,
	)
	return &balancerpb.FlushRealUpdatesResponse{
		UpdatesFlushed: uint32(count),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowConfig shows balancer config
func (service *BalancerService) ShowConfig(
	ctx context.Context,
	req *balancerpb.ShowConfigRequest,
) (*balancerpb.ShowConfigResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw("showing config", "name", name, "instance", inst)

	// Get config (no service lock held)
	moduleConfigProto, moduleStateConfigProto := balancerInstance.GetConfig()

	return &balancerpb.ShowConfigResponse{
		Target:            req.Target,
		ModuleConfig:      moduleConfigProto,
		ModuleStateConfig: moduleStateConfigProto,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ListConfigs lists balancer configs
func (service *BalancerService) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	service.log.Debugw("listing configs", "count", len(service.instances))

	configs := make([]*balancerpb.ShowConfigResponse, 0, len(service.instances))

	for key, balancerInstance := range service.instances {
		moduleConfigProto, moduleStateConfigProto := balancerInstance.GetConfig()

		config := &balancerpb.ShowConfigResponse{
			Target: &commonpb.TargetModule{
				ConfigName:        key.name,
				DataplaneInstance: key.dataplaneInstance,
			},
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
func (service *BalancerService) StateInfo(
	ctx context.Context,
	req *balancerpb.StateInfoRequest,
) (*balancerpb.StateInfoResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw("getting state info", "name", name, "instance", inst)

	// Get state info (no service lock held)
	info := balancerInstance.GetStateInfo()

	return &balancerpb.StateInfoResponse{
		Target: req.Target,
		Info:   info.IntoProto(),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ConfigStats returns stats of the balancer config
func (service *BalancerService) ConfigStats(
	ctx context.Context,
	req *balancerpb.ConfigStatsRequest,
) (*balancerpb.ConfigStatsResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw("getting config stats", "name", name, "instance", inst)

	// Get config stats (no service lock held)
	stats := balancerInstance.GetConfigStats(
		req.DataplaneInstance,
		req.Device,
		req.Pipeline,
		req.Function,
		req.Chain,
	)

	return &balancerpb.ConfigStatsResponse{
		Target:   req.Target,
		Device:   req.Device,
		Pipeline: req.Pipeline,
		Function: req.Function,
		Chain:    req.Chain,
		Stats:    stats.IntoProto(),
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// SessionsInfo returns info about active balancer sessions
func (service *BalancerService) SessionsInfo(
	ctx context.Context,
	req *balancerpb.SessionsInfoRequest,
) (*balancerpb.SessionsInfoResponse, error) {
	if req.Target == nil {
		return nil, fmt.Errorf("target is required")
	}

	name := req.Target.ConfigName
	inst := req.Target.DataplaneInstance

	key := moduleKey{name: name, dataplaneInstance: inst}

	// Get balancer instance (hold lock only for map access)
	service.mu.Lock()
	balancerInstance, exists := service.instances[key]
	service.mu.Unlock()

	if !exists {
		service.log.Warnw("balancer not found", "name", name, "instance", inst)
		return nil, fmt.Errorf(
			"balancer [name=%s, inst=%d] not found",
			name,
			inst,
		)
	}

	service.log.Debugw("getting sessions info", "name", name, "instance", inst)

	// Get sessions info (no service lock held)
	sessionsInfo, err := balancerInstance.GetSessionsInfo(time.Now())
	if err != nil {
		service.log.Errorw(
			"failed to get sessions info",
			"name",
			name,
			"instance",
			inst,
			"error",
			err,
		)
		return nil, fmt.Errorf("failed to get sessions info: %w", err)
	}

	// Convert to protobuf
	sessionsPb := make([]*balancerpb.SessionInfo, 0, len(sessionsInfo.Sessions))
	for idx := range sessionsInfo.Sessions {
		sessionsPb = append(sessionsPb, sessionsInfo.Sessions[idx].IntoProto())
	}

	service.log.Infow(
		"sessions info retrieved",
		"name",
		name,
		"instance",
		inst,
		"count",
		sessionsInfo.SessionsCount,
	)

	return &balancerpb.SessionsInfoResponse{
		Target:       req.Target,
		SessionsInfo: sessionsPb,
	}, nil
}
