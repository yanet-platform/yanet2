package balancer2

import (
	"context"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

var (
	errConfigNameRequired        = status.Error(codes.InvalidArgument, "config name is required")
	errSessionsStateNameRequired = status.Error(
		codes.InvalidArgument,
		"sessions state name is required",
	)
)

// ServiceOption configures the Service constructor.
type ServiceOption func(*serviceOptions)

type serviceOptions struct {
	Log *zap.Logger
}

func newServiceOptions() *serviceOptions {
	return &serviceOptions{
		Log: zap.NewNop(),
	}
}

// WithServiceLog sets the logger for the Service.
func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

type Service struct {
	balancerpb.UnimplementedBalancerServer

	agent *ffi.Agent

	mu             sync.Mutex
	moduleConfigs  map[string]*ModuleConfig
	sessionsStates map[string]*SessionsState

	log *zap.Logger
}

func NewService(agent *ffi.Agent, options ...ServiceOption) *Service {
	opts := newServiceOptions()
	for _, o := range options {
		o(opts)
	}

	opts.Log.Info("balancer service initialized")

	return &Service{
		agent:          agent,
		moduleConfigs:  map[string]*ModuleConfig{},
		sessionsStates: map[string]*SessionsState{},
		log:            opts.Log,
	}
}

// Close releases all module configs and session states held by the service.
// The agent lifecycle is owned by the parent module. After Close the Service
// must not be used.
func (m *Service) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, mc := range m.moduleConfigs {
		mc.Free()
	}
	m.moduleConfigs = nil

	for _, st := range m.sessionsStates {
		st.Free()
	}
	m.sessionsStates = nil

	return nil
}

func (m *Service) UpdateConfig(
	ctx context.Context,
	req *balancerpb.UpdateConfigRequest,
) (*balancerpb.UpdateConfigResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	params := &ConfigParams{
		Vs:       req.GetVs(),
		Timeouts: req.GetTimeouts(),
		Addr:     req.GetAddr(),
		Wlc:      req.GetWlc(),
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cur, ok := m.moduleConfigs[name]; ok {
		return m.updateBalancer(name, cur, params, req.GetSessionsStateName())
	}
	return m.createBalancer(name, params, req.GetSessionsStateName())
}

func (m *Service) updateBalancer(
	name string,
	cur *ModuleConfig,
	params *ConfigParams,
	sessionsName string,
) (*balancerpb.UpdateConfigResponse, error) {
	var st *SessionsState
	if sessionsName != "" {
		found, ok := m.sessionsStates[sessionsName]
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

func (m *Service) createBalancer(
	name string,
	params *ConfigParams,
	sessionsName string,
) (*balancerpb.UpdateConfigResponse, error) {
	if sessionsName == "" {
		return nil, errSessionsStateNameRequired
	}

	st, ok := m.sessionsStates[sessionsName]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "sessions state %q not found", sessionsName)
	}

	mc, err := NewModuleConfig(name, m.agent, params, st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create config %q: %v", name, err)
	}
	m.moduleConfigs[name] = mc

	return &balancerpb.UpdateConfigResponse{}, nil
}

func (m *Service) GetConfig(
	ctx context.Context,
	req *balancerpb.GetConfigRequest,
) (*balancerpb.GetConfigResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mc, ok := m.moduleConfigs[name]
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

func (m *Service) ListConfigs(
	ctx context.Context,
	req *balancerpb.ListConfigsRequest,
) (*balancerpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.moduleConfigs))
	for name := range m.moduleConfigs {
		names = append(names, name)
	}
	sort.Strings(names)
	return &balancerpb.ListConfigsResponse{Names: names}, nil
}

func (m *Service) UpdateReals(
	ctx context.Context,
	req *balancerpb.UpdateRealsRequest,
) (*balancerpb.UpdateRealsResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mc, ok := m.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.UpdateReals(req.GetUpdates()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update reals: %v", err)
	}
	return &balancerpb.UpdateRealsResponse{}, nil
}

func (m *Service) UpdateVS(
	ctx context.Context,
	req *balancerpb.UpdateVSRequest,
) (*balancerpb.UpdateVSResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mc, ok := m.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.UpdateVS(req.GetVs()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update virtual services: %v", err)
	}
	return &balancerpb.UpdateVSResponse{}, nil
}

func (m *Service) DeleteVS(
	ctx context.Context,
	req *balancerpb.DeleteVSRequest,
) (*balancerpb.DeleteVSResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mc, ok := m.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err := mc.DeleteVS(req.GetVs()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete virtual services: %v", err)
	}
	return &balancerpb.DeleteVSResponse{}, nil
}

func (m *Service) UpdateSessionsState(
	ctx context.Context,
	req *balancerpb.UpdateSessionsStateRequest,
) (*balancerpb.UpdateSessionsStateResponse, error) {
	name := req.GetSessionsStateName()
	if name == "" {
		return nil, errSessionsStateNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessionsStates[name]; ok {
		return nil, status.Errorf(codes.AlreadyExists, "sessions state %q already exists", name)
	}

	st, err := NewSessionsState(name, m.agent, req.GetCapacity())
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to create sessions state %q: %v",
			name,
			err,
		)
	}
	m.sessionsStates[name] = st

	return &balancerpb.UpdateSessionsStateResponse{}, nil
}

func (m *Service) ListSessionsStates(
	ctx context.Context,
	req *balancerpb.ListSessionsStatesRequest,
) (*balancerpb.ListSessionsStatesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.sessionsStates))
	for name := range m.sessionsStates {
		names = append(names, name)
	}
	sort.Strings(names)
	return &balancerpb.ListSessionsStatesResponse{Names: names}, nil
}

func (m *Service) GetState(
	ctx context.Context,
	req *balancerpb.GetStateRequest,
) (*balancerpb.GetStateResponse, error) {
	name := req.GetConfigName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mc, ok := m.moduleConfigs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	states := mc.GetState(req.GetPacketHandlerRef(), req.GetFilter(), time.Now())
	return &balancerpb.GetStateResponse{
		States: states,
	}, nil
}

func (m *Service) ListSessions(
	req *balancerpb.ListSessionsRequest,
	stream grpc.ServerStreamingServer[balancerpb.Session],
) error {
	name := req.GetSessionsStateName()
	if name == "" {
		return errSessionsStateNameRequired
	}

	m.mu.Lock()
	sessions, ok := m.sessionsStates[name]
	m.mu.Unlock()

	if !ok {
		return status.Errorf(codes.NotFound, "sessions state %q not found", name)
	}

	filter := newStateFilter(req.GetFilter())
	for id, state := range sessions.IterSessions(time.Now()) {
		session, err := makeSession(id, state)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to convert session: %v", err)
		}
		if !filter.matchVs(session.VsId) || !filter.matchReal(session.RealId) {
			continue
		}
		if err := stream.Context().Err(); err != nil {
			return err
		}
		if err := stream.Send(session); err != nil {
			return err
		}
	}

	return nil
}

func makeSession(
	id cbalancer2.SessionID,
	state cbalancer2.SessionState,
) (*balancerpb.Session, error) {
	proto, err := toPBTransport(id.Transport)
	if err != nil {
		return nil, err
	}

	return &balancerpb.Session{
		ClientAddr: id.ClientIP.AsSlice(),
		ClientPort: uint32(id.ClientPort),
		VsId: &balancerpb.VsIdentifier{
			Addr:  id.VIP.AsSlice(),
			Port:  uint32(id.VSPort),
			Proto: proto,
		},
		RealId: &balancerpb.RelativeRealIdentifier{
			Ip:   state.RealIP.AsSlice(),
			Port: 0,
		},
		CreateTimestamp:     timestamppb.New(state.CreateTimestamp),
		LastPacketTimestamp: timestamppb.New(state.LastPacketTimestamp),
		Timeout:             durationpb.New(state.Timeout),
	}, nil
}

func toPBTransport(proto cbalancer2.TransportProto) (balancerpb.TransportProto, error) {
	switch proto {
	case cbalancer2.TransportTCP:
		return balancerpb.TransportProto_TCP, nil
	case cbalancer2.TransportUDP:
		return balancerpb.TransportProto_UDP, nil
	default:
		return 0, status.Errorf(codes.Internal, "unsupported transport: %v", proto)
	}
}

func (m *Service) GetMetrics(
	ctx context.Context,
	req *balancerpb.GetMetricsRequest,
) (*balancerpb.GetMetricsResponse, error) {
	now := time.Now()

	m.mu.Lock()
	names := make([]string, 0, len(m.moduleConfigs))
	for name := range m.moduleConfigs {
		names = append(names, name)
	}
	sort.Strings(names)
	mcs := make([]*ModuleConfig, 0, len(names))
	for _, name := range names {
		if mc, ok := m.moduleConfigs[name]; ok {
			mcs = append(mcs, mc)
		}
	}
	m.mu.Unlock()

	var result []*commonpb.Metric
	for _, mc := range mcs {
		states := mc.GetState(nil, nil, now)
		for _, state := range states {
			result = append(result, collectStateMetrics(state)...)
		}
	}

	return &balancerpb.GetMetricsResponse{Metrics: result}, nil
}
