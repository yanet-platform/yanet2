package operator

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// FunctionChainSpec is the desired single-chain function definition that
// FunctionApplier publishes.
type FunctionChainSpec struct {
	// Name is the function identifier (Function.Id.Name).
	Name string
	// Chain is the chain name (Chain.Name).
	Chain string
	// Weight is the chain weight (FunctionChain.Weight).
	Weight uint64
	// Modules is the ordered list of module IDs for the chain.
	Modules []*commonpb.ModuleId
}

// chainModulesCompare reports whether current modules satisfy the desired
// module list for a single chain.
type chainModulesCompare func(current, want []*commonpb.ModuleId) bool

func compareChainModulesExact(current, want []*commonpb.ModuleId) bool {
	if len(current) != len(want) {
		return false
	}

	for idx, mod := range current {
		spec := want[idx]
		if mod.GetType() != spec.GetType() || mod.GetName() != spec.GetName() {
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
	spec           FunctionChainSpec
	compareModules chainModulesCompare
}

// NewFunctionApplier returns a FunctionApplier that will publish spec to
// client on each Apply call.
func NewFunctionApplier(
	client ynpb.FunctionServiceClient,
	spec FunctionChainSpec,
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
		spec:           spec,
		compareModules: compare,
	}
}

// Apply publishes the captured spec to the gateway, or returns true
// if the gateway is already correctly configured.
func (m *FunctionApplier) Apply(ctx context.Context) (bool, error) {
	ok, err := m.alreadyCorrect(ctx)
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}

	req := &ynpb.UpdateFunctionRequest{
		Function: &ynpb.Function{
			Id: &commonpb.FunctionId{
				Name: m.spec.Name,
			},
			Chains: []*ynpb.FunctionChain{{
				Chain: &ynpb.Chain{
					Name:    m.spec.Chain,
					Modules: m.spec.Modules,
				},
				Weight: m.spec.Weight,
			}},
		},
	}
	if _, err := m.client.Update(ctx, req); err != nil {
		return false, fmt.Errorf("failed to update function %q: %w", m.spec.Name, err)
	}

	return false, nil
}

func (m *FunctionApplier) alreadyCorrect(ctx context.Context) (bool, error) {
	resp, err := m.client.Get(ctx, &ynpb.GetFunctionRequest{
		Id: &commonpb.FunctionId{
			Name: m.spec.Name,
		},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to get function %q: %w", m.spec.Name, err)
	}

	for _, fc := range resp.GetFunction().GetChains() {
		if fc.GetChain().GetName() != m.spec.Chain {
			continue
		}

		if m.compareModules(fc.GetChain().GetModules(), m.spec.Modules) {
			return true, nil
		}

		return false, nil
	}

	return false, nil
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
