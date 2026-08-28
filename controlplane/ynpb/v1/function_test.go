package ynpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

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
