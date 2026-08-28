package operator

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// FunctionActuator publishes network functions to a gateway, leaving alone
// the ones the gateway already holds.
type FunctionActuator struct {
	client  ynpb.FunctionServiceClient
	compare functionCompare
	log     *zap.Logger
}

// NewFunctionActuator returns a FunctionActuator publishing through client.
func NewFunctionActuator(
	client ynpb.FunctionServiceClient,
	options ...FunctionActuatorOption,
) *FunctionActuator {
	opts := newFunctionActuatorOptions()
	for _, o := range options {
		o(opts)
	}

	return &FunctionActuator{
		client:  client,
		compare: opts.Compare,
		log:     opts.Log,
	}
}

// Apply publishes function to the gateway unless it already holds it.
func (m *FunctionActuator) Apply(ctx context.Context, function *ynpb.Function) error {
	name := function.GetId().GetName()
	if len(function.GetChains()) == 0 {
		return fmt.Errorf("function %q has no chains", name)
	}

	ok, err := m.alreadyCorrect(ctx, function)
	if err != nil {
		return err
	}
	if ok {
		m.log.Debug("function already correct, skipped", zap.String("function", name))
		return nil
	}

	req := &ynpb.UpdateFunctionRequest{Function: function}
	if _, err := m.client.Update(ctx, req); err != nil {
		return fmt.Errorf("failed to update function %q: %w", name, err)
	}
	m.log.Info("updated function", zap.String("function", name))

	return nil
}

// Close is a no-op, the client's connection belongs to the caller.
func (m *FunctionActuator) Close() error {
	return nil
}

// alreadyCorrect reports whether the gateway already holds function.
func (m *FunctionActuator) alreadyCorrect(ctx context.Context, function *ynpb.Function) (bool, error) {
	name := function.GetId().GetName()
	resp, err := m.client.Get(ctx, &ynpb.GetFunctionRequest{
		Id: &commonpb.FunctionId{Name: name},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to get function %q: %w", name, err)
	}

	return m.compare(resp.GetFunction(), function), nil
}
