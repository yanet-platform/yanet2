package balancer2

import (
	"context"
	"fmt"
	"sync"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errConfigNameRequired        = status.Error(codes.InvalidArgument, "config name is required")
	errSessionsStateNameRequired = status.Error(codes.InvalidArgument, "sessions state name is required")
)

type Service struct {
	balancerpb.UnimplementedBalancerServer

	agent          *ffi.Agent
	mu             *sync.Mutex
	log            *zap.SugaredLogger
	moduleConfigs  map[string]*ModuleConfig
	sessionsStates map[string]*SessionsState
}

func NewService(
	shm *ffi.SharedMemory,
	instanceIdx uint32,
	size datasize.ByteSize,
	log *zap.SugaredLogger,
) (*Service, error) {
	log.Info("initializing balancer service")

	agent, err := shm.AgentAttach("balancer", instanceIdx, size)
	if err != nil {
		log.Errorw("failed to reattach balancer agent", "error", err)
		return nil, fmt.Errorf("failed to reattach balancer agent: %w", err)
	}

	s := &Service{
		agent:          agent,
		log:            log,
		mu:             &sync.Mutex{},
		moduleConfigs:  map[string]*ModuleConfig{},
		sessionsStates: map[string]*SessionsState{},
	}

	return s, nil
}

func (s *Service) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	params := &ConfigParams{
		Vs:       req.GetVs(),
		Timeouts: req.GetTimeouts(),
		Addr:     req.GetAddr(),
		Wlc:      req.GetWlc(),
	}

	sessionsName := req.GetSessionsStateName()

	if cur, ok := s.moduleConfigs[name]; ok {
		var st *SessionsState
		if sessionsName != "" {
			found, ok := s.sessionsStates[sessionsName]
			if !ok {
				return nil, status.Errorf(codes.NotFound, "sessions state %q not found", sessionsName)
			}
			st = found
		}
		if err := cur.Update(params, st); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to update config %q: %v", name, err)
		}
		return &balancerpb.UpdateConfigResponse{}, nil
	}

	if sessionsName == "" {
		return nil, status.Error(codes.InvalidArgument, "sessions state name is required on create")
	}

	st, ok := s.sessionsStates[sessionsName]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "sessions state %q not found", sessionsName)
	}

	mc, err := NewModuleConfig(name, s.agent, params, st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create config %q: %v", name, err)
	}
	s.moduleConfigs[name] = mc

	return &balancerpb.UpdateConfigResponse{}, nil
}

func (s *Service) GetConfig(
	ctx context.Context,
	req *balancerpb.GetConfigRequest,
) (*balancerpb.GetConfigResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	mc, ok := s.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	cfg := mc.Params()
	return &balancerpb.GetConfigResponse{
		ConfigName:        name,
		SessionsStateName: mc.SessionsStateName(),
		Vs:                cfg.Vs,
		Timeouts:          cfg.Timeouts,
		Addr:              cfg.Addr,
		Wlc:               cfg.Wlc,
	}, nil
}

func (s *Service) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.moduleConfigs))
	for name := range s.moduleConfigs {
		names = append(names, name)
	}
	return &balancerpb.ListConfigsResponse{Names: names}, nil
}

func (s *Service) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	mc, ok := s.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.UpdateReals(req.GetUpdates()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update reals: %v", err)
	}
	return &balancerpb.UpdateRealsResponse{}, nil
}

func (s *Service) UpdateVS(
	ctx context.Context,
	req *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	mc, ok := s.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.UpdateVS(req.GetVs()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update virtual services: %v", err)
	}
	return &balancerpb.UpdateVSResponse{}, nil
}

func (s *Service) DeleteVS(
	ctx context.Context,
	req *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	mc, ok := s.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.DeleteVS(req.GetVs()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete virtual services: %v", err)
	}
	return &balancerpb.DeleteVSResponse{}, nil
}

func (s *Service) UpdateSessionsState(
	ctx context.Context,
	req *balancerpb.UpdateSessionsStateRequest,
) (*balancerpb.UpdateSessionsStateResponse, error) {
	name := req.GetSessionsStateName()
	if name == "" {
		return nil, errSessionsStateNameRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessionsStates[name]; ok {
		return nil, status.Errorf(
			codes.AlreadyExists,
			"sessions state %q already exists; resize is not yet implemented", name,
		)
	}

	st, err := NewSessionsState(s.agent, name, req.GetCapacity())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create sessions state %q: %v", name, err)
	}
	s.sessionsStates[name] = st

	return &balancerpb.UpdateSessionsStateResponse{}, nil
}

func (s *Service) ListSessionsStates(
	ctx context.Context,
	req *balancerpb.ListSessionsStatesRequest,
) (*balancerpb.ListSessionsStatesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.sessionsStates))
	for name := range s.sessionsStates {
		names = append(names, name)
	}
	return &balancerpb.ListSessionsStatesResponse{Names: names}, nil
}

func (s *Service) GetState(
	ctx context.Context,
	req *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetState is not implemented")
}

func (s *Service) ListSessions(
	req *balancerpb.ListSessionsRequest,
	stream grpc.ServerStreamingServer[balancerpb.Session],
) error {
	return status.Error(codes.Unimplemented, "ListSessions is not implemented")
}

func (s *Service) GetMetrics(
	ctx context.Context,
	req *balancerpb.GetMetricsRequest,
) (*balancerpb.GetMetricsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetMetrics is not implemented")
}
