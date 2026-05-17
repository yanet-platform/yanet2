package operator

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// GatewayActuator publishes the desired forward state to a single gateway.
type GatewayActuator struct {
	endpoint    string
	conn        *grpc.ClientConn
	forward     forwardpb.ForwardServiceClient
	funcApplier *operator.FunctionApplier
	configName  string
	log         *zap.Logger
}

// NewGatewayActuator dials the gateway endpoint and returns a ready-to-use
// actuator. The connection is kept open for the lifetime of the actuator;
// call Close to release it. functionName is the gateway-side function
// identifier; configName is the forward module config name.
func NewGatewayActuator(
	endpoint, configName, functionName string,
	ignorePdump bool,
	log *zap.Logger,
) (*GatewayActuator, error) {
	dialTarget := strings.TrimPrefix(endpoint, "grpc://")
	conn, err := grpc.NewClient(
		dialTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q: %w", endpoint, err)
	}
	return &GatewayActuator{
		endpoint: endpoint,
		conn:     conn,
		forward:  forwardpb.NewForwardServiceClient(conn),
		funcApplier: operator.NewFunctionApplier(
			ynpb.NewFunctionServiceClient(conn),
			newFuncSpec(functionName, configName),
			operator.WithIgnorePdump(ignorePdump),
		),
		configName: configName,
		log: log.With(
			zap.String("gateway", endpoint),
			zap.String("function", functionName),
		),
	}, nil
}

// Apply pushes the module config and then ensures the function definition
// is correct on the gateway.
func (m *GatewayActuator) Apply(ctx context.Context, state State) error {
	_, err := m.forward.UpdateConfig(ctx, &forwardpb.UpdateConfigRequest{
		Name:  m.configName,
		Rules: state.Rules,
	})
	if err != nil {
		return fmt.Errorf("failed to apply module-config %q: %w", m.configName, err)
	}

	m.log.Info("applied module config",
		zap.String("name", m.configName),
	)

	skipped, err := m.funcApplier.Apply(ctx)
	if err != nil {
		return fmt.Errorf("failed to update function: %w", err)
	}

	if skipped {
		m.log.Info("function already correct, skipped")
	} else {
		m.log.Info("updated function")
	}

	return nil
}

// Close releases the underlying gRPC connection.
func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}

// newFuncSpec builds the FunctionChainSpec for the forward operator from
// the supplied function name, config name, and module type.
func newFuncSpec(functionName, configName string) operator.FunctionChainSpec {
	return operator.FunctionChainSpec{
		Name:    functionName,
		Chain:   "default",
		Weight:  1,
		Modules: []*commonpb.ModuleId{{Type: "forward", Name: configName}},
	}
}
