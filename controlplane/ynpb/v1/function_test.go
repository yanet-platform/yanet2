package ynpb_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_Function_Validate verifies that chain weights obey a shared budget
// without changing the supplied definition.
func Test_Function_Validate(t *testing.T) {
	for _, test := range []struct {
		name     string
		function *ynpb.Function
		message  string
	}{
		{name: "empty chain list", function: functionWithWeights()},
		{name: "disabled chain", function: functionWithWeights(0)},
		{name: "individual limit", function: functionWithWeights(65535)},
		{name: "sum at limit", function: functionWithWeights(0, 65534, 1)},
		{
			name: "individual above limit", function: functionWithWeights(65536),
			message: "function.chains[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "sum above limit", function: functionWithWeights(65535, 1),
			message: "function.chains weight sum 65536 must be in range 0..65535",
		},
		{
			name: "overflowing sum", function: functionWithWeights(1, math.MaxUint64),
			message: "function.chains[1].weight 18446744073709551615 must be in range 0..65535",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := proto.Clone(test.function)
			err := test.function.Validate()
			if test.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, test.message)
			}
			require.True(t, proto.Equal(before, test.function))
		})
	}
}

// functionWithWeights creates a named function whose chains have no modules.
func functionWithWeights(weights ...uint64) *ynpb.Function {
	function := &ynpb.Function{Id: &commonpb.FunctionId{Name: "f"}}
	for idx, weight := range weights {
		function.Chains = append(function.Chains, &ynpb.FunctionChain{
			Chain: &ynpb.Chain{Name: fmt.Sprintf("c%d", idx)}, Weight: weight,
		})
	}
	return function
}

// Test_Function_WithoutModules_DropsExactTypeFromEveryChain verifies that
// only exact type matches leave each chain and the original stays untouched.
func Test_Function_WithoutModules_DropsExactTypeFromEveryChain(t *testing.T) {
	function := &ynpb.Function{
		Id: &commonpb.FunctionId{Name: "fn"},
		Chains: []*ynpb.FunctionChain{
			{
				Chain: &ynpb.Chain{
					Name: "default",
					Modules: []*commonpb.ModuleId{
						{Type: "pdump", Name: "pd0"},
						{Type: "forward", Name: "fwd0"},
						{Type: "pdumpx", Name: "pdx0"},
					},
				},
				Weight: 3,
			},
			{
				Chain:  &ynpb.Chain{Name: "tap", Modules: []*commonpb.ModuleId{{Type: "pdump", Name: "pd1"}}},
				Weight: 1,
			},
			{Weight: 1},
		},
	}
	original := proto.Clone(function).(*ynpb.Function)

	got := function.WithoutModules("pdump")

	require.True(t, function.Equal(original), "original mutated: %v", function)
	require.True(t, got.Equal(&ynpb.Function{
		Id: &commonpb.FunctionId{Name: "fn"},
		Chains: []*ynpb.FunctionChain{
			{
				Chain: &ynpb.Chain{
					Name: "default",
					Modules: []*commonpb.ModuleId{
						{Type: "forward", Name: "fwd0"},
						{Type: "pdumpx", Name: "pdx0"},
					},
				},
				Weight: 3,
			},
			{Chain: &ynpb.Chain{Name: "tap"}, Weight: 1},
			{Weight: 1},
		},
	}), "got %v", got)
}

// Test_Function_WithoutModules_Nil verifies that a nil function stays nil
// instead of panicking.
func Test_Function_WithoutModules_Nil(t *testing.T) {
	var function *ynpb.Function

	require.Nil(t, function.WithoutModules("pdump"))
}
