package operator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

type fakeFunctionClient struct {
	getResp *ynpb.GetFunctionResponse
	getErr  error
	updates int
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
	_ *ynpb.UpdateFunctionRequest,
	_ ...grpc.CallOption,
) (*ynpb.UpdateFunctionResponse, error) {
	m.updates++
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

func functionApplierSpec() FunctionChainSpec {
	return FunctionChainSpec{
		Name:    "fn:test",
		Chain:   "default",
		Weight:  1,
		Modules: functionApplierSpecModules,
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

	skipped, err := NewFunctionApplier(c, functionApplierSpec(), WithIgnorePdump(true)).
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
