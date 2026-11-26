//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// functionWith returns a function of one chain running one module, with
// the names given.
func functionWith(function, chain, moduleType, moduleName string) cpconfig.FunctionConfig {
	return cpconfig.FunctionConfig{
		Name: function,
		Chains: []cpconfig.FunctionChainConfig{{
			Chain: cpconfig.ChainConfig{
				Name:    chain,
				Modules: []cpconfig.ChainModuleConfig{{Type: moduleType, Name: moduleName}},
			},
			Weight: 1,
		}},
	}
}

// Test_UpdateFunctions_RefusesNameItsFieldCannotHold verifies that a
// function, chain or chained module name C would cut short is refused
// before the instance is touched.
//
// The instance has no zones, so taking its lock would fault.
func Test_UpdateFunctions_RefusesNameItsFieldCannotHold(t *testing.T) {
	type invalidFunction struct {
		name     string
		function cpconfig.FunctionConfig
	}
	var cases []invalidFunction
	add := func(name string, function cpconfig.FunctionConfig) {
		cases = append(cases, invalidFunction{name, function})
	}
	for _, invalid := range invalidNames(len(layout.CpFunction{}.Name)) {
		add("function: "+invalid.name, functionWith(invalid.value, "chain", "blackhole", "a"))
	}
	for _, invalid := range invalidNames(len(layout.CpChain{}.Name)) {
		add("chain: "+invalid.name, functionWith("drop", invalid.value, "blackhole", "a"))
	}
	for _, invalid := range invalidIdentities(len(layout.CpChainModule{}.Type), len(layout.CpChainModule{}.Name), "blackhole", "a") {
		add("chained module "+invalid.name, functionWith("drop", "chain", invalid.entityType, invalid.entityName))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cpconfig.UpdateFunctions(&zone.Instance{}, []cpconfig.FunctionConfig{tc.function})
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}

// Test_UpdateFunctions_RefusesWeightPastBound verifies that a chain
// weighing past the bound of a function's weights is refused before the
// instance is touched.
//
// The instance has no zones, so taking its lock would fault.
func Test_UpdateFunctions_RefusesWeightPastBound(t *testing.T) {
	function := functionWith("drop", "chain", "blackhole", "a")
	function.Chains[0].Weight = 65536

	err := cpconfig.UpdateFunctions(&zone.Instance{}, []cpconfig.FunctionConfig{function})

	require.ErrorContains(t, err, "weighs 65536")
}
