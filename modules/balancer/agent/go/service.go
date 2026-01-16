package balancer

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/c2h5oh/datasize"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"go.uber.org/zap"
)

////////////////////////////////////////////////////////////////////////////////

// gRPC service for controlling balancer
type BalancerService struct {
	balancerpb.UnimplementedBalancerServiceServer

	agent *BalancerAgent

	log *zap.SugaredLogger
}

////////////////////////////////////////////////////////////////////////////////

func NewBalancerService(
	shm *yanet.SharedMemory,
	memory datasize.ByteSize,
	log *zap.SugaredLogger,
) (*BalancerService, error) {
	log.Info("initializing balancer service")

	agent, err := NewBalancerAgent(shm, memory, log)
	if err != nil {
		log.Errorw("failed to create balancer agent", "error", err)
		return nil, err
	}

	service := &BalancerService{
		agent: agent,
		log:   log,
	}

	return service, nil
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

	manager, _ := m.agent.BalancerManager(name)
	if manager != nil {
		m.log.Infow("updating balancer config", "name", name)
		if err := manager.Update(req.Config, time.Now()); err != nil {
			m.log.Errorw(
				"failed to update balancer",
				"name",
				name,
				"error",
				err,
			)
			return nil, fmt.Errorf("failed to update balancer: %v", err)
		}
		m.log.Infow("balancer config updated", "name", name)
		return &balancerpb.UpdateConfigResponse{}, nil
	} else {
		m.log.Infow("creating new balancer", "name", name)
		if err := m.agent.NewBalancerManager(name, req.Config); err != nil {
			m.log.Errorw("failed to create balancer", "name", name, "error", err)
			return nil, fmt.Errorf("failed to create balancer: %v", err)
		}
		m.log.Infow("balancer created", "name", name)
		return &balancerpb.UpdateConfigResponse{}, nil
	}
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		m.log.Warnw("balancer not found", "name", name)
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	count, err := manager.UpdateReals(req.Updates, req.Buffer)
	if err != nil {
		m.log.Errorw("failed to update reals", "name", name, "error", err)
		msg := fmt.Sprintf("failed to make reals update: %v", err)
		return nil, status.Error(codes.Internal, msg)
	}

	if req.Buffer {
		m.log.Debugw("real updates buffered", "name", name, "count", count)
	} else {
		m.log.Infow("real updates applied", "name", name, "count", count)
	}

	return &balancerpb.UpdateRealsResponse{
		UpdatesApplied: uint32(count),
	}, nil
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		m.log.Warnw("balancer not found", "name", name)
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	count, err := manager.FlushRealUpdates()
	if err != nil {
		m.log.Errorw("failed to flush updates", "name", name, "error", err)
		msg := fmt.Sprintf("failed to flush updates: %v", err)
		return nil, status.Error(codes.Internal, msg)
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	config := manager.Config()
	bufferedUpdates := manager.BufferedUpdates()

	return &balancerpb.ShowConfigResponse{
		Config:              config,
		BufferedRealUpdates: bufferedUpdates,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ListConfigs lists balancer configs
func (m *BalancerService) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	managers := m.agent.Managers()
	m.log.Debugw("listing managers", "count", len(managers))
	return &balancerpb.ListConfigsResponse{
		Configs: managers,
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	info, err := manager.Info(time.Now())
	if err != nil {
		msg := fmt.Sprintf("failed to get info: %v", err)
		return nil, status.Error(codes.Internal, msg)
	}

	return &balancerpb.ShowInfoResponse{
		Name: name,
		Info: info,
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	stats, err := manager.Stats(req.Ref)
	if err != nil {
		msg := fmt.Sprintf("failed to get stats: %v", err)
		return nil, status.Error(codes.Internal, msg)
	}

	return &balancerpb.ShowStatsResponse{
		Ref:   req.Ref,
		Stats: stats,
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

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	sessions, err := manager.Sessions(time.Now())
	if err != nil {
		msg := fmt.Sprintf("failed to get sessions: %v", err)
		return nil, status.Error(codes.Internal, msg)
	}

	return &balancerpb.ShowSessionsResponse{
		Sessions: sessions,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

// ShowGraph returns the balancer topology graph
func (m *BalancerService) ShowGraph(
	ctx context.Context,
	req *balancerpb.ShowGraphRequest,
) (*balancerpb.ShowGraphResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"module config name is required",
		)
	}

	manager, err := m.agent.BalancerManager(name)
	if err != nil {
		msg := fmt.Sprintf("balancer not found: %v", err)
		return nil, status.Error(codes.NotFound, msg)
	}

	graph := manager.Graph()

	return &balancerpb.ShowGraphResponse{
		Graph: graph,
	}, nil
}
