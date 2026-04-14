package balancer

import (
	"context"
	"sync"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service struct {
	balancerpb.UnimplementedBalancerServer

	agent           *Agent
	mu              sync.Mutex
	log             *zap.SugaredLogger
	handlersMetrics methodMetrics
}

func NewService(
	shm *yanet.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
	log *zap.SugaredLogger,
) (*Service, error) {
	log.Info("initializing balancer service")

	agent, err := ReattachAgent(shm, instanceIdx, size, log)
	if err != nil {
		log.Errorw("failed to reattach balancer agent", "error", err)
		return nil, err
	}

	s := &Service{
		agent:           agent,
		log:             log,
		handlersMetrics: newMethodMetrics(),
	}

	for _, balancer := range agent.AllBalancers() {
		balancer.startRefreshing(&s.mu)
	}

	return s, nil
}

// getBalancerWithAutoSelection retrieves a balancer by name.
// If name is nil or empty, attempts to auto-select when exactly one balancer exists.
// The caller must hold s.mu.
func (s *Service) getBalancerWithAutoSelection(
	name *string,
) (*Balancer, string, error) {
	if name != nil {
		b, ok := s.agent.GetBalancer(*name)
		if !ok {
			return nil, "", status.Errorf(codes.NotFound, "balancer %q not found", *name)
		}
		return b, *name, nil
	}

	names := s.agent.BalancerNames()

	if len(names) == 0 {
		return nil, "", status.Error(codes.NotFound, "no balancers found")
	}

	if len(names) > 1 {
		return nil, "", status.Errorf(
			codes.InvalidArgument,
			"multiple balancers found (%d), please specify name explicitly",
			len(names),
		)
	}

	selected := names[0]
	s.log.Debugw("auto-selected balancer", "name", selected)

	b, _ := s.agent.GetBalancer(selected)
	return b, selected, nil
}

func (s *Service) SetConfig(
	_ context.Context,
	req *balancerpb.SetConfigRequest,
) (*balancerpb.SetConfigResponse, error) {
	tracker := newMetricsTracker(
		"set_config", s.handlersMetrics, defaultLatencyBoundsMS,
		metrics.Labels{"config": req.GetName()},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	b, exists := s.agent.GetBalancer(name)
	if exists {
		s.log.Infow("updating balancer config", "name", name)

		now := time.Now()
		reuseReport, err := b.Update(req.Config, &now)
		if err != nil {
			s.log.Errorw("failed to update balancer", "name", name, "error", err)
			return nil, err
		}

		s.log.Infow("balancer config updated", "name", name)

		return &balancerpb.SetConfigResponse{
			Name:                 name,
			Reuse:                reuseReport,
			SessionTableCapacity: b.SessionTableCapacity(),
		}, nil
	}

	s.log.Infow("creating new balancer", "name", name)

	b, err := NewBalancer(s.agent, name, req.Config, s.log)
	if err != nil {
		s.log.Errorw("failed to create balancer", "name", name, "error", err)
		return nil, err
	}
	s.agent.PutBalancer(name, b)

	b.startRefreshing(&s.mu)

	s.log.Infow("balancer created", "name", name)

	return &balancerpb.SetConfigResponse{
		Name:                 name,
		SessionTableCapacity: b.SessionTableCapacity(),
	}, nil
}

func (s *Service) ListBalancers(
	_ context.Context,
	_ *balancerpb.ListBalancersRequest,
) (*balancerpb.ListBalancersResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return &balancerpb.ListBalancersResponse{
		Names: s.agent.BalancerNames(),
	}, nil
}

func (s *Service) GetConfig(
	_ context.Context,
	req *balancerpb.GetConfigRequest,
) (*balancerpb.GetConfigResponse, error) {
	tracker := newMetricsTracker(
		"get_config", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, name, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return nil, err
	}

	return &balancerpb.GetConfigResponse{
		Name:                name,
		Config:              b.Config(),
		BufferedRealUpdates: b.BufferedRealUpdates(),
	}, nil
}

func (s *Service) GetState(
	_ context.Context,
	req *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	tracker := newMetricsTracker(
		"get_state", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	var balancers map[string]*Balancer
	if req.Name != nil {
		b, ok := s.agent.GetBalancer(*req.Name)
		if !ok {
			return nil, status.Errorf(codes.NotFound, "balancer %q not found", *req.Name)
		}
		balancers = map[string]*Balancer{*req.Name: b}
	} else {
		balancers = s.agent.AllBalancers()
	}

	var allStates []*balancerpb.BalancerState
	for _, b := range balancers {
		states, err := b.GetState(req.PacketHandlerRef, req.Filter, req.IncludeCounters, time.Now())
		if err != nil {
			return nil, err
		}
		allStates = append(allStates, states...)
	}

	return &balancerpb.GetStateResponse{
		State: allStates,
	}, nil
}

func (s *Service) ListSessions(
	req *balancerpb.ListSessionsRequest,
	stream grpc.ServerStreamingServer[balancerpb.Session],
) error {
	tracker := newMetricsTracker(
		"list_sessions", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, _, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return err
	}

	return b.ListSessions(req.Filter, time.Now(), func(session *balancerpb.Session) error {
		return stream.Send(session)
	})
}

func (s *Service) UpdateReals(
	_ context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	tracker := newMetricsTracker(
		"update_reals", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, name, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return nil, err
	}

	count, err := b.UpdateReals(req.Updates, req.Buffer)
	if err != nil {
		s.log.Errorw("failed to update reals", "name", name, "error", err)
		return nil, err
	}

	resp := &balancerpb.UpdateRealsResponse{Name: name}
	if req.Buffer {
		resp.UpdatesBuffered = uint32(count)
		s.log.Debugw("real updates buffered", "name", name, "count", count)
	} else {
		resp.UpdatesApplied = uint32(count)
		s.log.Infow("real updates applied", "name", name, "count", count)
	}

	return resp, nil
}

func (s *Service) FlushReals(
	_ context.Context,
	req *balancerpb.FlushRealsRequest,
) (*balancerpb.FlushRealsResponse, error) {
	tracker := newMetricsTracker(
		"flush_reals", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, name, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return nil, err
	}

	count, err := b.FlushRealUpdates()
	if err != nil {
		s.log.Errorw("failed to flush real updates", "name", name, "error", err)
		return nil, err
	}

	s.log.Infow("real updates flushed", "name", name, "count", count)

	return &balancerpb.FlushRealsResponse{
		Name:           name,
		UpdatesFlushed: uint64(count),
	}, nil
}

func (s *Service) UpdateVS(
	_ context.Context,
	req *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	tracker := newMetricsTracker(
		"update_vs", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, name, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return nil, err
	}

	s.log.Infow("updating virtual services", "name", name, "vs_count", len(req.Services))

	reuseReport, err := b.UpdateVirtualServices(req.Services)
	if err != nil {
		s.log.Errorw("failed to update virtual services", "name", name, "error", err)
		return nil, err
	}

	s.log.Infow("virtual services updated", "name", name, "vs_count", len(req.Services))

	return &balancerpb.UpdateVSResponse{
		Name:  name,
		Reuse: reuseReport,
	}, nil
}

func (s *Service) DeleteVS(
	_ context.Context,
	req *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	tracker := newMetricsTracker(
		"delete_vs", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, name, err := s.getBalancerWithAutoSelection(req.Name)
	if err != nil {
		return nil, err
	}

	s.log.Infow("deleting virtual services", "name", name, "vs_count", len(req.Services))

	reuseReport, err := b.DeleteVirtualServices(req.Services)
	if err != nil {
		s.log.Errorw("failed to delete virtual services", "name", name, "error", err)
		return nil, err
	}

	s.log.Infow("virtual services deleted", "name", name, "vs_count", len(req.Services))

	return &balancerpb.DeleteVSResponse{
		Name:  name,
		Reuse: reuseReport,
	}, nil
}

func (s *Service) GetMetrics(
	_ context.Context,
	_ *balancerpb.GetMetricsRequest,
) (*balancerpb.GetMetricsResponse, error) {
	tracker := newMetricsTracker(
		"get_metrics", s.handlersMetrics, defaultLatencyBoundsMS, metrics.Labels{},
	)
	defer tracker.Fix()

	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*commonpb.Metric, 0)

	for _, b := range s.agent.AllBalancers() {
		bMetrics, err := b.Metrics(time.Now())
		if err != nil {
			return nil, err
		}
		result = append(result, bMetrics...)
	}

	result = append(result, s.handlersMetrics.collect()...)

	return &balancerpb.GetMetricsResponse{Metrics: result}, nil
}
