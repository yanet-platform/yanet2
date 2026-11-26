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
	registry.Fini(&gen.FunctionRegistry.Registry, FreeFunctionItem)
	registry.Fini(&gen.PipelineRegistry.Registry, FreePipelineItem)
	registry.FiniEntities(&gen.DeviceRegistry.EntityRegistry)
	registry.FiniEntities(&gen.ObjectRegistry.EntityRegistry)
	registry.FreeNode(&cp.MemoryContext, gen, 0)
}

// FreeFunctionItem frees the function of a registry item whose last
// reference went, as the function registry's free callback does.
func FreeFunctionItem(item *layout.RegistryItem) {
	FreeFunction(registry.Function(item))
}

// FreeFunction frees a function and its chains, as cp_function_fini and
// cp_function_free do.
//
// A failed build frees a function part-built with it: a chain not built
// yet reads as absent.
func FreeFunction(function *layout.CpFunction) {
	chains := function.Chains(int(function.ChainCount))
	for idx := range chains {
		if chain := chains[idx].CpChain.Get(); chain != nil {
			FreeChain(chain)
			chains[idx].CpChain.Set(nil)
		}
	}
	FiniCounterRegistry(&function.CounterRegistry)
	registry.FreeNode(
		function.MemoryContext.Get(), function,
		unsafe.Sizeof(layout.CpFunctionChain{})*uintptr(function.ChainCount),
	)
}

// FreeChain frees a chain, as cp_chain_fini and cp_chain_free do.
func FreeChain(chain *layout.CpChain) {
	FiniCounterRegistry(&chain.CounterRegistry)
	registry.FreeNode(chain.MemoryContext.Get(), chain, unsafe.Sizeof(layout.CpChainModule{})*uintptr(chain.Length))
}

// FreePipelineItem frees the pipeline of a registry item whose last
// reference went, as the pipeline registry's free callback does.
func FreePipelineItem(item *layout.RegistryItem) {
	FreePipeline(registry.Pipeline(item))
}

// FreePipeline frees a pipeline, as cp_pipeline_fini and
// cp_pipeline_free do.
func FreePipeline(pipeline *layout.CpPipeline) {
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
