package operator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// StaticTarget is one module config pushed to every gateway on each
// reconcile pass, with the function that references it.
type StaticTarget struct {
	// Name labels the target in logs and errors, the module config's name
	// for the module operators.
	Name string
	// Method is the unary gRPC method that replaces the module config,
	// spelled as "package.Service/Method".
	Method string
	// Request is the message the method receives, sent as is.
	Request proto.Message
	// Function is published after the config, nil when the target owns none.
	Function *ynpb.Function
	// IgnorePdump leaves pdump modules on both sides out of the function
	// comparison.
	IgnorePdump bool
}

// StaticConfig is the part of an operator's configuration the static module
// operator consumes.
type StaticConfig struct {
	// Server is where the operator serves its own readiness.
	Server GRPCServerConfig
	// Gateways lists the gateways every target is pushed to.
	Gateways []GatewayConfig
	// Register paces the registration heartbeat to the gateways.
	Register RegisterConfig
	// Reconcile paces the push and its retries.
	Reconcile ReconcileConfig
}

type staticOptions struct {
	// Log is the logger the operator and its parts share.
	Log *zap.Logger
}

func newStaticOptions() *staticOptions {
	return &staticOptions{
		Log: zap.NewNop(),
	}
}

// StaticOption configures NewStaticModuleOperator.
type StaticOption func(*staticOptions)

// WithStaticLog sets the logger for the static module operator.
func WithStaticLog(log *zap.Logger) StaticOption {
	return func(o *staticOptions) {
		o.Log = log
	}
}

// NewStaticModuleOperator builds an operator that pushes targets to every
// gateway on each reconcile pass and reports readiness under name.
func NewStaticModuleOperator(
	name string,
	cfg StaticConfig,
	targets []StaticTarget,
	options ...StaticOption,
) (Runnable, error) {
	opts := newStaticOptions()
	for _, o := range options {
		o(opts)
	}
	log := opts.Log.With(zap.String("operator", name))

	if name == "" || strings.ContainsAny(name, "/ ") {
		return nil, fmt.Errorf("operator name %q must be a single word without slashes", name)
	}
	if len(cfg.Gateways) == 0 {
		return nil, errors.New("at least one gateway must be configured")
	}
	state, err := staticTargets(targets)
	if err != nil {
		return nil, err
	}

	tracker := readiness.NewTracker(staticScopes(cfg), readiness.WithLog(log))
	actuators := make([]Actuator[[]StaticTarget], 0, len(cfg.Gateways))
	for _, gw := range cfg.Gateways {
		conn, err := dialGateway(gw)
		if err != nil {
			for _, a := range actuators {
				_ = a.Close()
			}
			return nil, err
		}
		actuator := &staticGatewayActuator{
			name:      gw.Name,
			conn:      conn,
			functions: ynpb.NewFunctionServiceClient(conn),
			log:       log.With(zap.String("gateway", gw.Name)),
		}
		observed := NewObservedActuator(actuator, "config:"+gw.Name, tracker.Observe)
		actuators = append(actuators, observed)
	}

	return NewOperator(
		NewFanOutActuator(actuators, WithFanOutLog(log)),
		newStaticSource(state),
		WithGRPCServer(cfg.Server, staticReadinessRegistrar(name, tracker)),
		WithGateways(cfg.Register, cfg.Gateways...),
		WithWorkers(func(ctx context.Context) error {
			<-ctx.Done()
			tracker.Drain()
			return nil
		}),
		WithLog(log),
		WithReconcile(cfg.Reconcile),
	), nil
}

// staticTargets checks every target against the linked descriptors and
// returns the copy the operator keeps, messages detached from the caller.
func staticTargets(targets []StaticTarget) ([]StaticTarget, error) {
	if len(targets) == 0 {
		return nil, errors.New("at least one target must be configured")
	}

	functions := map[string]bool{}
	out := make([]StaticTarget, 0, len(targets))
	for idx, target := range targets {
		if _, err := resolveMethod(target.Method, target.Request); err != nil {
			return nil, fmt.Errorf("target %d: %w", idx, err)
		}
		if target.Name == "" {
			target.Name = fmt.Sprintf("target %d", idx)
		}
		target.Request = proto.Clone(target.Request)
		if target.Function != nil {
			name := target.Function.GetId().GetName()
			if name == "" {
				return nil, fmt.Errorf("target %d: the function has no name", idx)
			}
			if len(target.Function.GetChains()) == 0 {
				return nil, fmt.Errorf("target %d: function %q has no chains", idx, name)
			}
			if functions[name] {
				return nil, fmt.Errorf("target %d: function %q is declared twice", idx, name)
			}
			functions[name] = true
			target.Function = proto.Clone(target.Function).(*ynpb.Function)
		}
		out = append(out, target)
	}
	return out, nil
}

// staticScopes gives every gateway a readiness scope refreshed at the
// reconcile cadence.
func staticScopes(cfg StaticConfig) []readiness.ScopeSpec {
	specs := make([]readiness.ScopeSpec, len(cfg.Gateways))
	for idx, gw := range cfg.Gateways {
		specs[idx] = readiness.ScopeSpec{
			Name:                        "config:" + gw.Name,
			ExpectedObservationInterval: cfg.Reconcile.Interval.Unwrap(),
		}
	}
	return specs
}

// resolvedMethod is a unary method ready to be invoked over a connection.
type resolvedMethod struct {
	// Full is the method as the transport spells it, with a leading slash.
	Full string
	// Reply builds a fresh response message for each call.
	Reply protoreflect.MessageType
}

// resolveMethod turns the spelled method into a call, refusing one the binary
// does not know, a streaming one, or a request of another type.
func resolveMethod(method string, request proto.Message) (resolvedMethod, error) {
	service, name, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !ok || service == "" || name == "" {
		return resolvedMethod{}, fmt.Errorf("method %q must be spelled as package.Service/Method", method)
	}
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return resolvedMethod{}, fmt.Errorf("service %q is not linked into this binary", service)
	}
	serviceDescriptor, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return resolvedMethod{}, fmt.Errorf("%q is not a service", service)
	}
	methodDescriptor := serviceDescriptor.Methods().ByName(protoreflect.Name(name))
	if methodDescriptor == nil {
		return resolvedMethod{}, fmt.Errorf("service %q has no method %q", service, name)
	}
	if methodDescriptor.IsStreamingClient() || methodDescriptor.IsStreamingServer() {
		return resolvedMethod{}, fmt.Errorf(
			"method %q is streaming, a module config needs a unary one", method,
		)
	}
	if request == nil || !request.ProtoReflect().IsValid() {
		return resolvedMethod{}, fmt.Errorf("method %q has no request", method)
	}
	want := methodDescriptor.Input().FullName()
	if got := request.ProtoReflect().Descriptor().FullName(); got != want {
		return resolvedMethod{}, fmt.Errorf("method %q takes %s, not %s", method, want, got)
	}
	reply, err := protoregistry.GlobalTypes.FindMessageByName(methodDescriptor.Output().FullName())
	if err != nil {
		return resolvedMethod{}, fmt.Errorf(
			"reply %s of method %q is not linked into this binary",
			methodDescriptor.Output().FullName(), method,
		)
	}
	return resolvedMethod{Full: "/" + service + "/" + name, Reply: reply}, nil
}

// staticSource holds the targets for the lifetime of the operator and never
// wakes the reconcile loop, so the interval is the sole pacing.
type staticSource struct {
	targets []StaticTarget
	wake    chan struct{}
}

func newStaticSource(targets []StaticTarget) *staticSource {
	return &staticSource{
		targets: targets,
		wake:    make(chan struct{}),
	}
}

func (m *staticSource) Snapshot() ([]StaticTarget, bool) {
	return m.targets, true
}

func (m *staticSource) Wake() <-chan struct{} {
	return m.wake
}

func (m *staticSource) Advance(_ []StaticTarget) {}

// staticGatewayActuator pushes the targets to one gateway: every module
// config first, then every function.
type staticGatewayActuator struct {
	name      string
	conn      *grpc.ClientConn
	functions ynpb.FunctionServiceClient
	log       *zap.Logger
}

func dialGateway(cfg GatewayConfig) (*grpc.ClientConn, error) {
	endpoint := cfg.Endpoint.Unwrap()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}
	return conn, nil
}

// Apply tries every target regardless of partial failures and joins the
// errors, leaving the retry to the reconcile loop.
func (m *staticGatewayActuator) Apply(ctx context.Context, targets []StaticTarget) error {
	var err error
	for _, target := range targets {
		method, e := resolveMethod(target.Method, target.Request)
		if e != nil {
			err = errors.Join(err, e)
			continue
		}
		reply := method.Reply.New().Interface()
		if e := m.conn.Invoke(ctx, method.Full, target.Request, reply); e != nil {
			err = errors.Join(err, fmt.Errorf(
				"failed to apply %s to gateway %q: %w", target.Name, m.name, e,
			))
		}
	}

	for _, target := range targets {
		if target.Function == nil {
			continue
		}
		var options []FunctionApplierOption
		if target.IgnorePdump {
			options = append(options, WithIgnorePDump())
		}
		applier := NewFunctionApplier(m.functions, target.Function, options...)
		skipped, e := applier.Apply(ctx)
		if e != nil {
			err = errors.Join(err, fmt.Errorf(
				"failed to update function %q on gateway %q: %w", applier.Name(), m.name, e,
			))
			continue
		}
		if skipped {
			m.log.Debug("function already correct, skipped", zap.String("function", applier.Name()))
		} else {
			m.log.Info("updated function", zap.String("function", applier.Name()))
		}
	}

	return err
}

func (m *staticGatewayActuator) Close() error {
	return m.conn.Close()
}

// readinessService is the gateway's readiness contract, served by an
// operator under its own name.
type readinessService struct {
	ynpb.UnimplementedReadinessServiceServer

	tracker *readiness.Tracker
}

func (m *readinessService) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	return m.tracker.Ready(req), nil
}

func (m *readinessService) Watch(
	req *readinesspb.ReadyRequest,
	stream ynpb.ReadinessService_WatchServer,
) error {
	return m.tracker.Watch(stream.Context(), req, stream.Send)
}

// staticReadinessRegistrar registers the readiness service under the
// operator's own name, so several operators can share one gateway.
func staticReadinessRegistrar(name string, tracker *readiness.Tracker) ServiceRegistrar {
	return func(server *grpc.Server) string {
		desc := ynpb.ReadinessService_ServiceDesc
		desc.ServiceName = ReadinessServiceName(name)
		server.RegisterService(&desc, &readinessService{tracker: tracker})
		return desc.ServiceName
	}
}

// ReadinessServiceName is the gRPC service name under which the operator
// called name reports readiness through a gateway.
func ReadinessServiceName(name string) string {
	return "operators." + name + ".operatorpb.v1.ReadinessService"
}
