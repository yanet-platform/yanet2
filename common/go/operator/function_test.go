package operator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

type fakeFunctionClient struct {
	getResp    *ynpb.GetFunctionResponse
	getErr     error
	updates    int
	lastUpdate *ynpb.UpdateFunctionRequest
}

func (m *fakeFunctionClient) List(
	_ context.Context,
	_ *ynpb.ListFunctionsRequest,
	_ ...grpc.CallOption,
) (*ynpb.ListFunctionsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *fakeFunctionClient) Get(
	_ context.Context,
	_ *ynpb.GetFunctionRequest,
	_ ...grpc.CallOption,
) (*ynpb.GetFunctionResponse, error) {
	return m.getResp, m.getErr
}

func (m *fakeFunctionClient) Update(
	_ context.Context,
	req *ynpb.UpdateFunctionRequest,
	_ ...grpc.CallOption,
) (*ynpb.UpdateFunctionResponse, error) {
	m.updates++
	m.lastUpdate = req
	return &ynpb.UpdateFunctionResponse{}, nil
}

func (m *fakeFunctionClient) Delete(
	_ context.Context,
	_ *ynpb.DeleteFunctionRequest,
	_ ...grpc.CallOption,
) (*ynpb.DeleteFunctionResponse, error) {
	return nil, errors.New("not implemented")
}

func makeGetResp(modules ...*commonpb.ModuleId) *ynpb.GetFunctionResponse {
	return &ynpb.GetFunctionResponse{
		Function: &ynpb.Function{
			Id: &commonpb.FunctionId{Name: "fn:test"},
			Chains: []*ynpb.FunctionChain{{
				Chain:  &ynpb.Chain{Name: "default", Modules: modules},
				Weight: 1,
			}},
		},
	}
}

var functionApplierSpecModules = []*commonpb.ModuleId{
	{
		Type: "forward",
		Name: "fwd0",
	},
}

// functionApplierSpec builds the single-chain function the legacy cases
// publish, one forward module under the default chain.
func functionApplierSpec() *ynpb.Function {
	return &ynpb.Function{
		Id: &commonpb.FunctionId{Name: "fn:test"},
		Chains: []*ynpb.FunctionChain{{
			Chain:  &ynpb.Chain{Name: "default", Modules: functionApplierSpecModules},
			Weight: 1,
		}},
	}
}

func Test_FunctionApplier_Basic(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec()).
		Apply(t.Context())
	require.NoError(t, err)
	require.True(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_GetErrorAbortsWithoutUpdateDefaultStrategy(t *testing.T) {
	c := &fakeFunctionClient{
		getErr: errors.New("not found"),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec()).
		Apply(t.Context())
	require.Error(t, err)
	require.False(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_PdumpPresentExactStrategyNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "pdump",
				Name: "pd0",
			},
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec()).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_GetErrorAbortsWithoutUpdate(t *testing.T) {
	c := &fakeFunctionClient{
		getErr: errors.New("not found"),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.Error(t, err)
	require.False(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_ChainMissingNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: &ynpb.GetFunctionResponse{
			Function: &ynpb.Function{
				Id: &commonpb.FunctionId{Name: "fn:test"},
				Chains: []*ynpb.FunctionChain{{
					Chain: &ynpb.Chain{
						Name:    "other",
						Modules: functionApplierSpecModules,
					},
					Weight: 1,
				}},
			},
		},
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec()).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_ChainMatchesExactlyNoPdumpSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.True(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_PdumpBeforeAndAfterMatchingSurvivorsSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "pdump",
				Name: "pd0",
			},
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
			&commonpb.ModuleId{
				Type: "pdump",
				Name: "pd1",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.True(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_PdumpBetweenModulesAndAtStartSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "pdump",
				Name: "pd0",
			},
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.True(t, skipped)
	require.Equal(t, 0, c.updates)
}

func Test_FunctionApplier_WrongModuleTypeNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(&commonpb.ModuleId{
			Type: "route",
			Name: "fwd0",
		}),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_CorrectTypeWrongOrderNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: &ynpb.GetFunctionResponse{
			Function: &ynpb.Function{
				Id: &commonpb.FunctionId{Name: "fn:test"},
				Chains: []*ynpb.FunctionChain{{
					Chain: &ynpb.Chain{
						Name: "default",
						Modules: []*commonpb.ModuleId{
							{Type: "forward", Name: "fwd1"},
							{Type: "forward", Name: "fwd0"},
						},
					},
					Weight: 1,
				}},
			},
		},
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_ExtraNonPdumpModuleNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
			&commonpb.ModuleId{
				Type: "nat64",
				Name: "nat0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_ForwardWithWrongNameNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd99",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

func Test_FunctionApplier_PdumpxPrefixNotFilteredNotSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: makeGetResp(
			&commonpb.ModuleId{
				Type: "pdumpx",
				Name: "pd0",
			},
			&commonpb.ModuleId{
				Type: "forward",
				Name: "fwd0",
			},
		),
	}

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
		Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

// functionDefinition builds a two-chain definition, one chain holding two
// modules, so weight, chain set and module order are all in play.
func functionDefinition() *ynpb.Function {
	return &ynpb.Function{
		Id: &commonpb.FunctionId{Name: "fn:acl"},
		Chains: []*ynpb.FunctionChain{
			{
				Chain: &ynpb.Chain{
					Name: "default",
					Modules: []*commonpb.ModuleId{
						{Type: "acl", Name: "acl-in"},
						{Type: "fwstate", Name: "fwstate0"},
					},
				},
				Weight: 3,
			},
			{
				Chain: &ynpb.Chain{
					Name:    "bypass",
					Modules: []*commonpb.ModuleId{{Type: "forward", Name: "bypass"}},
				},
				Weight: 1,
			},
		},
	}
}

// Test_FunctionApplier_MatchingDefinitionSkipped verifies that a
// gateway holding the same chains, weights and modules is left alone.
func Test_FunctionApplier_MatchingDefinitionSkipped(t *testing.T) {
	c := &fakeFunctionClient{
		getResp: &ynpb.GetFunctionResponse{Function: functionDefinition()},
	}

	skipped, err := NewFunctionApplier(c, functionDefinition()).Apply(t.Context())
	require.NoError(t, err)
	require.True(t, skipped)
	require.Equal(t, 0, c.updates)
}

// Test_FunctionApplier_ChainOrderMatters verifies that chains in another
// order count as drift, as the order decides which packets each chain gets.
func Test_FunctionApplier_ChainOrderMatters(t *testing.T) {
	reordered := functionDefinition()
	reordered.Chains[0], reordered.Chains[1] = reordered.Chains[1], reordered.Chains[0]
	c := &fakeFunctionClient{
		getResp: &ynpb.GetFunctionResponse{Function: reordered},
	}

	skipped, err := NewFunctionApplier(c, functionDefinition()).Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
}

// Test_FunctionApplier_RejectsChainlessDefinition verifies that a definition
// without chains is refused before it can reach the gateway.
func Test_FunctionApplier_RejectsChainlessDefinition(t *testing.T) {
	c := &fakeFunctionClient{}
	definition := functionDefinition()
	definition.Chains = nil

	_, err := NewFunctionApplier(c, definition).Apply(t.Context())

	require.ErrorContains(t, err, "has no chains")
	require.Equal(t, 0, c.updates)
}

// Test_FunctionApplier_Drift verifies that each way the gateway
// can drift from the definition triggers an update.
func Test_FunctionApplier_Drift(t *testing.T) {
	cases := []struct {
		name  string
		drift func(current *ynpb.Function)
	}{
		{
			name:  "weight changed",
			drift: func(current *ynpb.Function) { current.Chains[0].Weight = 1 },
		},
		{
			name: "extra chain",
			drift: func(current *ynpb.Function) {
				current.Chains = append(current.Chains, &ynpb.FunctionChain{
					Chain:  &ynpb.Chain{Name: "extra"},
					Weight: 1,
				})
			},
		},
		{
			name:  "missing chain",
			drift: func(current *ynpb.Function) { current.Chains = current.Chains[:1] },
		},
		{
			name: "chain renamed",
			drift: func(current *ynpb.Function) {
				current.Chains[1].Chain.Name = "fallback"
			},
		},
		{
			name: "module order swapped",
			drift: func(current *ynpb.Function) {
				modules := current.Chains[0].Chain.Modules
				modules[0], modules[1] = modules[1], modules[0]
			},
		},
		{
			name: "module dropped",
			drift: func(current *ynpb.Function) {
				current.Chains[0].Chain.Modules = current.Chains[0].Chain.Modules[:1]
			},
		},
		{
			name: "chain duplicated in place of another",
			drift: func(current *ynpb.Function) {
				current.Chains[1] = current.Chains[0]
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := functionDefinition()
			tc.drift(current)
			c := &fakeFunctionClient{
				getResp: &ynpb.GetFunctionResponse{Function: current},
			}

			skipped, err := NewFunctionApplier(c, functionDefinition()).Apply(t.Context())
			require.NoError(t, err)
			require.False(t, skipped)
			require.Equal(t, 1, c.updates)
		})
	}
}

// Test_FunctionApplier_AbsentFunctionPublished verifies that a
// function the gateway does not know is published as is.
func Test_FunctionApplier_AbsentFunctionPublished(t *testing.T) {
	c := &fakeFunctionClient{
		getErr: status.Error(codes.NotFound, "no such function"),
	}
	definition := functionDefinition()

	skipped, err := NewFunctionApplier(c, definition).Apply(t.Context())
	require.NoError(t, err)
	require.False(t, skipped)
	require.Equal(t, 1, c.updates)
	require.True(t, proto.Equal(definition, c.lastUpdate.GetFunction()), "got %v", c.lastUpdate)
}
