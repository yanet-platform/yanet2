package operator

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// chainModulesCompare reports whether the modules the gateway holds satisfy
// the wanted module list for a single chain.
type chainModulesCompare func(gateway, want []*commonpb.ModuleId) bool

func compareChainModulesExact(gateway, want []*commonpb.ModuleId) bool {
	if len(gateway) != len(want) {
		return false
	}

	for idx, module := range gateway {
		if module.GetType() != want[idx].GetType() || module.GetName() != want[idx].GetName() {
			return false
		}
	}

	return true
}

func compareChainModulesIgnorePdump(gateway, want []*commonpb.ModuleId) bool {
	survivors := filterPdump(gateway)
	return compareChainModulesExact(survivors, want)
}

// FunctionApplier publishes a fixed function definition to a gateway.
type FunctionApplier struct {
	client         ynpb.FunctionServiceClient
	function       *ynpb.Function
	compareModules chainModulesCompare
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

	compare := compareChainModulesExact
	if opts.IgnorePdump {
		compare = compareChainModulesIgnorePdump
	}

	return &FunctionApplier{
		client:         client,
		function:       function,
		compareModules: compare,
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

// alreadyCorrect reports whether the gateway holds the wanted chains in the
// wanted order, each with the wanted weight and modules.
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

	gateway := resp.GetFunction().GetChains()
	want := m.function.GetChains()
	if len(gateway) != len(want) {
		return false, nil
	}
	for idx, chain := range gateway {
		if chain.GetChain().GetName() != want[idx].GetChain().GetName() ||
			chain.GetWeight() != want[idx].GetWeight() {
			return false, nil
		}
		if !m.compareModules(chain.GetChain().GetModules(), want[idx].GetChain().GetModules()) {
			return false, nil
		}
	}

	return true, nil
}

// filterPdump returns a new slice containing only the modules whose type is
// not exactly "pdump".
func filterPdump(modules []*commonpb.ModuleId) []*commonpb.ModuleId {
	out := make([]*commonpb.ModuleId, 0, len(modules))
	for _, mod := range modules {
		if mod.GetType() != "pdump" {
			out = append(out, mod)
		}
	}
	return out
}
