package operator

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// FunctionApplier publishes a fixed function definition to a gateway.
type FunctionApplier struct {
	client   ynpb.FunctionServiceClient
	function *ynpb.Function
	compare  functionCompare
}

// NewFunctionApplier returns a FunctionApplier that will publish function to
// client on each Apply call.
func NewFunctionApplier(
	client ynpb.FunctionServiceClient,
	function *ynpb.Function,
	options ...FunctionApplierOption,
) *FunctionApplier {
	opts := newFunctionApplierOptions()
	for _, o := range options {
		o(opts)
	}

	return &FunctionApplier{
		client:   client,
		function: function,
		compare:  opts.Compare,
	}
}

// Name returns the function identifier the applier publishes.
func (m *FunctionApplier) Name() string {
	return m.function.GetId().GetName()
}

// Apply publishes the captured definition to the gateway, or returns true
// if the gateway is already correctly configured.
func (m *FunctionApplier) Apply(ctx context.Context) (bool, error) {
	if len(m.function.GetChains()) == 0 {
		return false, fmt.Errorf("function %q has no chains", m.Name())
	}

	ok, err := m.alreadyCorrect(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}

	req := &ynpb.UpdateFunctionRequest{Function: m.function}
	if _, err := m.client.Update(ctx, req); err != nil {
		return false, fmt.Errorf("failed to update function %q: %w", m.Name(), err)
	}

	return false, nil
}

// alreadyCorrect reports whether the gateway already holds the wanted function.
func (m *FunctionApplier) alreadyCorrect(ctx context.Context) (bool, error) {
	resp, err := m.client.Get(ctx, &ynpb.GetFunctionRequest{
		Id: &commonpb.FunctionId{
			Name: m.Name(),
		},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to get function %q: %w", m.Name(), err)
	}

	return m.compare(resp.GetFunction(), m.function), nil
}
