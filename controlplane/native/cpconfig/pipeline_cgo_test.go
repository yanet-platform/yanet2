//go:build linux && (amd64 || arm64) && cgo

package cpconfig_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// updatePipelines publishes the pipelines natively on the fixture.
func updatePipelines(t *testing.T, memory *publishedFixture, pipelines ...cpconfig.PipelineConfig) error {
	t.Helper()
	return publishNatively(t, memory, func(options ...cpconfig.UpdateOption) error {
		return cpconfig.UpdatePipelines(memory.Instance(), pipelines, options...)
	})
}

// publishedPipeline returns the pipeline of a name the published
// generation holds.
func publishedPipeline(t *testing.T, memory *publishedFixture, name string) *layout.CpPipeline {
	t.Helper()
	pipelines := &memory.Instance().Cp.CpConfigGen.Get().PipelineRegistry.Registry
	for _, item := range registry.Items(pipelines) {
		if pipeline := registry.Pipeline(item); shm.String(pipeline.Name[:]) == name {
			return pipeline
		}
	}
	t.Fatalf("the published generation holds no pipeline %q", name)
	return nil
}

// registeredCounters returns the size of each counter a registry holds,
// by name.
func registeredCounters(counters *layout.CounterRegistry) map[string]uint64 {
	registered := map[string]uint64{}
	names := registry.Slice(counters.Names.Get(), counters.Count)
	for idx := range names {
		registered[shm.String(names[idx].Name[:])] = names[idx].Size
	}
	return registered
}

// Test_UpdatePipelines_DataplaneRunsGoPipelines verifies that the
// dataplane runs pipelines Go published in place of those C published,
// their functions in the given order and an empty one with none.
//
// The first function drops every packet, so the second sees none, and the
// pipeline's counters continue from the replaced pipeline's.
func Test_UpdatePipelines_DataplaneRunsGoPipelines(t *testing.T) {
	memory := newPublishedFixture(t)
	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "second",
		Chains: []cpconfig.FunctionChainConfig{chainOf("second_chain", 1, "c")},
	}))
	_, err := memory.Harness.HandlePackets(icmpPackets(t, 3)...)
	require.NoError(t, err)
	pipelineTags := []zone.CounterTag{{Key: "pipeline", Value: "drop"}, {Key: "kind", Value: "pipeline"}}
	before := counterTotal(t, memory, "input", pipelineTags...)
	require.Equal(t, uint64(3), before)

	err = updatePipelines(t, memory,
		cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop", "second"}},
		cpconfig.PipelineConfig{Name: "dummy"},
	)
	require.NoError(t, err)

	drop := publishedPipeline(t, memory, "drop")
	require.Equal(t, uint64(2), drop.Length)
	functions := drop.Functions(2)
	require.Equal(t, []string{"drop", "second"}, []string{shm.String(functions[0].Name[:]), shm.String(functions[1].Name[:])})
	require.Equal(t, map[string]uint64{
		"input": 2, "output": 2, "drop": 2, "input histogram": 8, "pending_input": 2, "pending_output": 2,
	}, registeredCounters(&drop.CounterRegistry))
	require.Zero(t, publishedPipeline(t, memory, "dummy").Length)

	published := memory.Instance().Cp.CpConfigGen.Get()
	root := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)[0].Get()
	var device *layout.DeviceEctx
	for built := range registry.Pointees(registry.Slice(root.DevicePtrs.Get(), root.DeviceCount)) {
		device = built
	}
	require.NotNil(t, device, "port0 has a context")
	input := device.InputPipelines.Get()
	context := unsafe.Slice(input.PipelinePtrs.Get(), input.PipelineCount)[0].Get()
	var order []string
	for function := range registry.Pointees(registry.Slice(context.FunctionPtrs.Get(), context.Length)) {
		order = append(order, shm.String(function.CpFunction.Get().Name[:]))
	}
	require.Equal(t, []string{"drop", "second"}, order, "port0's input runs the functions in order")

	result, err := memory.Harness.HandlePackets(icmpPackets(t, 5)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 5)
	require.Equal(t, before+5, counterTotal(t, memory, "input", pipelineTags...), "pipeline counters continue")
	secondTags := []zone.CounterTag{{Key: "function", Value: "second"}, {Key: "kind", Value: "function"}}
	require.Zero(t, counterTotal(t, memory, "input", secondTags...), "the second function sees no packet")
}

// Test_UpdatePipelines_RefusesMissingFunction verifies that a pipeline
// running an unpublished function is refused by the contexts' build,
// publishing nothing and taking nothing.
func Test_UpdatePipelines_RefusesMissingFunction(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState[*layout.CpModule](memory)

	err := updatePipelines(t, memory, cpconfig.PipelineConfig{Name: "drop", Functions: []string{"absent"}})

	require.ErrorIs(t, err, cpconfig.ErrMissingFunction)
	requireLeftAsFound[*layout.CpModule](t, before, memory)
}

// Test_UpdatePipelines_RefusesRepeatedPipeline verifies that an update
// naming one pipeline twice is refused, publishing nothing and taking
// nothing.
func Test_UpdatePipelines_RefusesRepeatedPipeline(t *testing.T) {
	memory := newPublishedFixture(t)
	before := sharedState[*layout.CpModule](memory)
	pipeline := cpconfig.PipelineConfig{Name: "twice"}

	err := updatePipelines(t, memory, pipeline, pipeline)

	require.ErrorIs(t, err, cpconfig.ErrRepeated)
	requireLeftAsFound[*layout.CpModule](t, before, memory)
}

// pipelineRequests returns what building a pipeline of that many
// functions asks the zone for, in order.
func pipelineRequests(length int) []uintptr {
	node := unsafe.Sizeof(layout.CpPipeline{}) + uintptr(length)*unsafe.Sizeof(layout.CpPipelineFunction{})
	return append([]uintptr{node}, counterRequests(6)...)
}

// Test_UpdatePipelines_ExhaustedZoneFreesUpdate verifies that an update of
// pipelines the zone runs out of memory for at any step frees every block
// it took and drops every reference it took.
//
// The update replaces the published pipeline and adds a new one, so a
// failure inside the second finds the first in the copy.
func Test_UpdatePipelines_ExhaustedZoneFreesUpdate(t *testing.T) {
	requests := append(pipelineRequests(1), pipelineRequests(2)...)
	budgets := prefixBudgets(requests, contextTails...)
	requireExhaustionLeavesAsFound(t, func(t *testing.T, memory *publishedFixture) error {
		return updatePipelines(t, memory,
			cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop"}},
			cpconfig.PipelineConfig{Name: "fresh", Functions: []string{"drop", "drop"}},
		)
	}, budgets, []string{
		`pipeline "drop": allocate pipeline`,
		`pipeline "drop": register`,
		`pipeline "fresh": allocate pipeline`,
		`pipeline "fresh": register`,
		"worker 0",
		"worker 1",
	})
}

// Test_UpdatePipelines_DataplaneRunsGoTopology verifies that packets on a
// device run a module, function and pipeline Go published, the module
// counting into a runtime registry Go added.
//
// The device's input pipeline runs the Go function, whose chain runs the
// Go module; the module drops every packet, and the module's counters and
// the device's count them by their tags.
func Test_UpdatePipelines_DataplaneRunsGoTopology(t *testing.T) {
	memory := newPublishedFixture(t)
	module, err := cpconfig.NewBlackhole(memory.owner, "native")
	require.NoError(t, err)
	rules, _, err := cpconfig.ModuleCounterRegistry(module, "rules")
	require.NoError(t, err)
	_, err = cpconfig.RegisterCounter(rules, "hits", 4)
	require.NoError(t, err)
	require.NoError(t, updateModules(t, memory, module))
	require.NoError(t, updateFunctions(t, memory, cpconfig.FunctionConfig{
		Name:   "native",
		Chains: []cpconfig.FunctionChainConfig{chainOf("native_chain", 1, "native")},
	}))
	require.NoError(t, updatePipelines(t, memory, cpconfig.PipelineConfig{Name: "drop", Functions: []string{"native"}}))
	deviceTags := []zone.CounterTag{{Key: "device", Value: "port0"}, {Key: "kind", Value: "device"}}
	received := counterTotal(t, memory, "input_rx", deviceTags...)

	result, err := memory.Harness.HandlePackets(icmpPackets(t, 4)...)

	require.NoError(t, err)
	require.Len(t, result.Drop, 4, "the Go module drops every packet")
	moduleTags := []zone.CounterTag{{Key: "module_name", Value: "native"}, {Key: "kind", Value: "module"}}
	require.Equal(t, uint64(4), counterTotal(t, memory, "drop", moduleTags...))
	require.Equal(t, received+4, counterTotal(t, memory, "input_rx", deviceTags...))
	runtimeTags := []zone.CounterTag{{Key: "module_name", Value: "native"}, {Key: "kind", Value: "runtime"}, {Key: "config", Value: "rules"}}
	require.Zero(t, counterTotal(t, memory, "hits", runtimeTags...), "the runtime registry has a storage")
}
