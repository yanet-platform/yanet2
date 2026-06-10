package operator

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// funcApplierEntry bundles a FunctionApplier with its function name for
// logging.
type funcApplierEntry struct {
	name    string
	applier *operator.FunctionApplier
}

// GatewayActuator publishes the desired decap state to a single gateway.
type GatewayActuator struct {
	name     string
	conn     *grpc.ClientConn
	decap    decappb.DecapServiceClient
	appliers []funcApplierEntry
	log      *zap.Logger
}

// NewGatewayActuator dials the gateway endpoint and returns a
// ready-to-use actuator for all configured functions. The connection is
// kept open for the lifetime of the actuator; call Close to release it.
func NewGatewayActuator(
	cfg operator.GatewayConfig,
	functions []FunctionConfig,
	options ...GatewayActuatorOption,
) (*GatewayActuator, error) {
	opts := newGatewayActuatorOptions()
	for _, o := range options {
		o(opts)
	}

	endpoint := cfg.Endpoint.Unwrap()
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	fnClient := ynpb.NewFunctionServiceClient(conn)
	appliers := make([]funcApplierEntry, 0, len(functions))
	for _, fn := range functions {
		spec := operator.FunctionChainSpec{
			Name:   fn.Name.Unwrap(),
			Chain:  fn.Chain.Unwrap(),
			Weight: fn.Weight,
			Modules: []*commonpb.ModuleId{{
				Type: "decap",
				Name: fn.Module.Unwrap(),
			}},
		}
		appliers = append(appliers, funcApplierEntry{
			name: fn.Name.Unwrap(),
			applier: operator.NewFunctionApplier(
				fnClient,
				spec,
				operator.WithIgnorePdump(fn.IgnorePdump),
			),
		})
	}

	return &GatewayActuator{
		name:     cfg.Name,
		conn:     conn,
		decap:    decappb.NewDecapServiceClient(conn),
		appliers: appliers,
		log:      opts.Log.With(zap.String("gateway", cfg.Name)),
	}, nil
}

// Apply pushes every module config and then ensures all function
// definitions are correct on the gateway.
//
// Errors from individual modules or functions are joined; the reconcile
// loop applies backoff, so each Apply pass tries everything regardless
// of partial failures.
func (m *GatewayActuator) Apply(ctx context.Context, state State) error {
	var err error

	for _, mc := range state.Modules {
		_, e := m.decap.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
			Name:     mc.Name,
			Prefixes: mc.Prefixes,
		})
		if e != nil {
			err = errors.Join(err, fmt.Errorf(
				"failed to apply module config %q to gateway %q: %w",
				mc.Name, m.name, e,
			))
		}
	}

	for _, entry := range m.appliers {
		skipped, e := entry.applier.Apply(ctx)
		if e != nil {
			err = errors.Join(err, fmt.Errorf(
				"failed to update function %q on gateway %q: %w",
				entry.name, m.name, e,
			))
			continue
		}
		if skipped {
			m.log.Debug("function already correct, skipped",
				zap.String("function", entry.name),
			)
		} else {
			m.log.Info("updated function",
				zap.String("function", entry.name),
			)
		}
	}

	return err
}

// Close releases the underlying gRPC connection.
func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}
