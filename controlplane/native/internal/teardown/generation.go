//go:build linux && (amd64 || arm64)

// Package teardown frees the configuration generations and the
// superseded agents of YANET's shared memory from Go, in C's order.
//
// Free order is part of the layout: the block allocator never merges
// neighbours, so the same blocks freed in another order leave other free
// lists. Every free takes its allocator's lock itself, as C's do. Both the
// configuration builder, retiring the generation it replaces or freeing
// one it failed to build, and the storage reader, whose release may be a
// generation's last, import it.
package teardown

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// FreeGeneration frees a generation nothing references, as
// cp_config_gen_free does.
//
// It frees the workers' contexts, drops the references its registries
// hold and frees its own block. A function or a pipeline goes with its
// last reference, as C's registries free them; a module, device or object
// whose last reference goes stays allocated for its owner to free.
func FreeGeneration(cp *layout.CpConfig, gen *layout.CpConfigGen) {
	roots := registry.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)
	for root := range registry.Pointees(roots) {
		FreeWorkerContext(cp, root)
	}
	registry.FreeArray(&cp.EctxMemoryContext, roots)
	registry.FiniEntities(&gen.ModuleRegistry.EntityRegistry)
	registry.Fini(&gen.FunctionRegistry.Registry, freeFunction)
	registry.Fini(&gen.PipelineRegistry.Registry, freePipeline)
	registry.FiniEntities(&gen.DeviceRegistry.EntityRegistry)
	registry.FiniEntities(&gen.ObjectRegistry.EntityRegistry)
	registry.FreeNode(&cp.MemoryContext, gen, 0)
}

// freeFunction frees a function no generation holds, with its chains, as
// cp_function_fini and cp_function_free do.
func freeFunction(item *layout.RegistryItem) {
	function := registry.Function(item)
	chains := function.Chains(int(function.ChainCount))
	for idx := range chains {
		chain := chains[idx].CpChain.Get()
		if chain == nil {
			continue
		}
		FiniCounterRegistry(&chain.CounterRegistry)
		registry.FreeNode(chain.MemoryContext.Get(), chain, unsafe.Sizeof(layout.CpChainModule{})*uintptr(chain.Length))
		chains[idx].CpChain.Set(nil)
	}
	FiniCounterRegistry(&function.CounterRegistry)
	registry.FreeNode(
		function.MemoryContext.Get(), function,
		unsafe.Sizeof(layout.CpFunctionChain{})*uintptr(function.ChainCount),
	)
}

// freePipeline frees a pipeline no generation holds, as cp_pipeline_fini
// and cp_pipeline_free do.
func freePipeline(item *layout.RegistryItem) {
	pipeline := registry.Pipeline(item)
	FiniCounterRegistry(&pipeline.CounterRegistry)
	registry.FreeNode(
		pipeline.MemoryContext.Get(), pipeline,
		unsafe.Sizeof(layout.CpPipelineFunction{})*uintptr(pipeline.Length),
	)
}

// FiniCounterRegistry frees what a counter registry holds and zeroes it,
// as counter_registry_fini does.
func FiniCounterRegistry(counters *layout.CounterRegistry) {
	context := counters.MemoryContext.Get()
	registry.FreeArray(context, registry.Slice(counters.Names.Get(), counters.Capacity))
	for pool := range counters.FreeSlots {
		registry.FreeArray(context, registry.Slice(counters.FreeSlots[pool].Get(), counters.FreeCapacities[pool]))
	}
	registry.StrFini(&counters.StrIndex)
	*counters = layout.CounterRegistry{}
}
