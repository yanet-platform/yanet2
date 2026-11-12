package operator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// StaticTarget is one module config pushed to every gateway on each
// reconcile pass, or one function published after every config, or both.
type StaticTarget struct {
	// Name labels the target in logs and errors, the module config's name
	// for the module operators.
	Name string
	// Method is the unary gRPC method that replaces the module config,
	// spelled as "package.Service/Method".
	//
	// Empty when the target only publishes a function.
	Method string
	// Descriptors resolves the method, its request and its reply, nil
	// resolving them among the descriptors linked into this binary.
	Descriptors *protoregistry.Files
	// Request is the message the method receives, sent as is.
	Request proto.Message
	// Function is published after the configs, nil when the target owns
	// none.
	Function *ynpb.Function
	// IgnorePdump leaves pdump modules on both sides out of the function
	// comparison.
	IgnorePdump bool
}

// StaticConfig is the part of an operator's configuration the static module
// operator consumes.
type StaticConfig struct {
	// Server is where the operator serves its readiness and metrics.
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
// gateway on each reconcile pass and reports readiness and metrics under name.
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

	tracker := readiness.NewTracker(
		staticScopes(cfg),
		readiness.WithDrainLatch(),
		readiness.WithLog(log),
	)
	gateways := make([]Target[[]StaticTarget], 0, len(cfg.Gateways))
	for _, gw := range cfg.Gateways {
		gatewayLog := log.With(zap.String("gateway", gw.Name))
		conn, err := DialGateway(gw, xgrpc.WithCertificateStoreLog(gatewayLog))
		if err != nil {
			for _, gateway := range gateways {
				_ = gateway.Actuator.Close()
			}
			return nil, err
		}
		gateways = append(gateways, Target[[]StaticTarget]{
			Name: gw.Name,
			Actuator: &staticGatewayActuator{
				name:      gw.Name,
				conn:      conn,
				functions: ynpb.NewFunctionServiceClient(conn),
				log:       gatewayLog,
			},
			Scope: staticScope(gw.Name),
		})
	}

	// The targets never change, so they are published once and the interval
	// alone paces the pushes.
	desired := NewLatest[[]StaticTarget]()
	desired.Publish(state)

	return NewOperator(
		gateways,
		desired,
		WithGRPCServer(cfg.Server),
		WithReadiness(name, tracker),
		WithMetrics(MetricsConfig{
			Name:   name,
			Prefix: "generic_operator",
			Labels: []*commonpb.Label{commonpb.NewLabel("operator", name)},
		}),
		WithGateways(cfg.Register, cfg.Gateways...),
		WithLog(log),
		WithReconcile(cfg.Reconcile),
	), nil
}

// staticTargets checks every target against its descriptors and returns
// the copy the operator keeps, messages detached from the caller.
func staticTargets(targets []StaticTarget) ([]StaticTarget, error) {
	if len(targets) == 0 {
		return nil, errors.New("at least one target must be configured")
	}

	functions := map[string]bool{}
	out := make([]StaticTarget, 0, len(targets))
	for idx, target := range targets {
		if target.Method == "" && target.Request == nil {
			if target.Function == nil {
				return nil, fmt.Errorf("target %d: neither a method nor a function", idx)
			}
		} else if _, err := resolveMethod(target.Method, target.Request, target.Descriptors); err != nil {
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
			Name:                        staticScope(gw.Name),
			ExpectedObservationInterval: cfg.Reconcile.Interval.Unwrap(),
		}
	}
	return specs
}

// staticScope names the readiness scope of a gateway: whether every target
// applies there.
func staticScope(gateway string) string {
	return "config:" + gateway
}

// resolvedMethod is a unary method ready to be invoked over a connection.
type resolvedMethod struct {
	// Full is the method as the transport spells it, with a leading slash.
	Full string
	// Reply builds a fresh response message for each call.
	Reply protoreflect.MessageType
}

// methodDescriptor finds the spelled method in the descriptor set, or among
// the descriptors linked into this binary when files is nil, refusing a
// streaming one.
func methodDescriptor(method string, files *protoregistry.Files) (protoreflect.MethodDescriptor, error) {
	service, name, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !ok || service == "" || name == "" {
		return nil, fmt.Errorf("method %q must be spelled as package.Service/Method", method)
	}
	resolver, where := protoregistry.GlobalFiles, "linked into this binary"
	if files != nil {
		resolver, where = files, "in the descriptor set"
	}
	descriptor, err := resolver.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, fmt.Errorf("service %q is not %s", service, where)
	}
	serviceDescriptor, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a service", service)
	}
	found := serviceDescriptor.Methods().ByName(protoreflect.Name(name))
	if found == nil {
		return nil, fmt.Errorf("service %q has no method %q", service, name)
	}
	if found.IsStreamingClient() || found.IsStreamingServer() {
		return nil, fmt.Errorf(
			"method %q is streaming, a module config needs a unary one", method,
		)
	}
	return found, nil
}

// NewMethodRequest builds an empty request for the spelled unary method,
// resolved in the descriptor set, or among the descriptors linked into this
// binary when files is nil.
//
// A method resolved in a set takes a dynamic message of the set's request
// type, even when this binary links that type.
func NewMethodRequest(method string, files *protoregistry.Files) (proto.Message, error) {
	descriptor, err := methodDescriptor(method, files)
	if err != nil {
		return nil, err
	}
	if files != nil {
		return dynamicpb.NewMessage(descriptor.Input()), nil
	}
	request, err := protoregistry.GlobalTypes.FindMessageByName(descriptor.Input().FullName())
	if err != nil {
		return nil, fmt.Errorf(
			"request %s of method %q is not linked into this binary",
			descriptor.Input().FullName(), method,
		)
	}
	return request.New().Interface(), nil
}

// resolveMethod turns the spelled method into a call, refusing one the
// descriptors do not hold, a streaming one, or a request of another type.
//
// A method resolved in a set replies with a dynamic message of the set's
// reply type.
func resolveMethod(
	method string,
	request proto.Message,
	files *protoregistry.Files,
) (resolvedMethod, error) {
	descriptor, err := methodDescriptor(method, files)
	if err != nil {
		return resolvedMethod{}, err
	}
	if request == nil || !request.ProtoReflect().IsValid() {
		return resolvedMethod{}, fmt.Errorf("method %q has no request", method)
	}
	want := descriptor.Input().FullName()
	if got := request.ProtoReflect().Descriptor().FullName(); got != want {
		return resolvedMethod{}, fmt.Errorf("method %q takes %s, not %s", method, want, got)
	}
	full := "/" + string(descriptor.Parent().FullName()) + "/" + string(descriptor.Name())
	if files != nil {
		return resolvedMethod{Full: full, Reply: dynamicpb.NewMessageType(descriptor.Output())}, nil
	}
	reply, err := protoregistry.GlobalTypes.FindMessageByName(descriptor.Output().FullName())
	if err != nil {
		return resolvedMethod{}, fmt.Errorf(
			"reply %s of method %q is not linked into this binary",
			descriptor.Output().FullName(), method,
		)
	}
	return resolvedMethod{Full: full, Reply: reply}, nil
}

// staticGatewayActuator pushes the targets to one gateway: every module
// config first, then every function.
type staticGatewayActuator struct {
	name      string
	conn      *grpc.ClientConn
	functions ynpb.FunctionServiceClient
	log       *zap.Logger
}

// Apply tries every target regardless of partial failures and joins the
// errors, leaving the retry to the reconcile loop.
func (m *staticGatewayActuator) Apply(ctx context.Context, targets []StaticTarget) error {
	var err error
	for _, target := range targets {
		if target.Method == "" && target.Request == nil {
			continue
		}
		method, e := resolveMethod(target.Method, target.Request, target.Descriptors)
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
		options := []FunctionActuatorOption{WithFunctionLog(m.log)}
		if target.IgnorePdump {
			options = append(options, WithIgnorePDump())
		}
		if e := NewFunctionActuator(m.functions, options...).Apply(ctx, target.Function); e != nil {
			err = errors.Join(err, fmt.Errorf(
				"failed to update function %q on gateway %q: %w", target.Function.GetId().GetName(), m.name, e,
			))
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

// NewReadinessServiceRegistrar serves the shared readiness contract under
// the operator's own name.
func NewReadinessServiceRegistrar(name string, tracker *readiness.Tracker) ServiceRegistrar {
	return func(server *grpc.Server) string {
		desc := ynpb.ReadinessService_ServiceDesc
		desc.ServiceName = ReadinessServiceName(name)
		server.RegisterService(&desc, &readinessService{tracker: tracker})
		return desc.ServiceName
	}
}

// ReadinessServiceName identifies an operator's scoped readiness API.
func ReadinessServiceName(name string) string {
	return "operators." + name + ".operatorpb.v1.ReadinessService"
}
