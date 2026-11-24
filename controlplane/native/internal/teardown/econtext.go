//go:build linux && (amd64 || arm64)

package teardown

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// The frees of a worker's execution contexts, in the order of
// lib/controlplane's econtext frees.
//
// Each frees what the context points to, leaves alone what it does not
// and sizes its blocks by the counts the context holds. A generation's
// teardown frees whole contexts with them, and a failed context build
// frees the part it built, as C's error paths do.

// FreeWorkerContext frees a worker's root context and everything below
// it, as config_gen_ectx_free does.
func FreeWorkerContext(cp *layout.CpConfig, root *layout.ConfigGenEctx) {
	context := &cp.EctxMemoryContext
	devices := registry.Slice(root.DevicePtrs.Get(), root.DeviceCount)
	for device := range registry.Pointees(devices) {
		FreeDeviceContext(cp, device)
	}
	registry.FreeArray(context, devices)
	registry.FreeArray(context, registry.Slice(root.EgressBacking.Get(), root.DeviceCount))
	registry.FreeArray(context, registry.Slice(root.TxBuckets.Get(), uint64(root.TxBucketCount)))
	registry.FreeArray(context, registry.Slice(root.BackingRegistry.Get(), root.BackingRegistryCount))
	objects := registry.Slice(root.Objects.Get(), root.ObjectCount)
	for object := range registry.Pointees(objects) {
		FreeObjectContext(cp, object)
	}
	registry.FreeArray(context, objects)
	if storages := root.CounterStorageRegistry.Get(); storages != nil {
		finiStorageRegistry(storages)
		registry.FreeNode(&cp.MemoryContext, storages, 0)
	}
	registry.FreeNode(context, root, shm.PtrSize*uintptr(root.DeviceCount))
}

// FreeObjectContext frees an object's context, as object_ectx_free does.
func FreeObjectContext(cp *layout.CpConfig, object *layout.ObjectEctx) {
	FreeCounterStorage(object.CounterStorage.Get())
	// The buffer's size is the dataplane object's, found through the
	// object's own index as C finds it.
	if prepared := object.ObjectPrepared.Get(); prepared != nil {
		dp := cp.DpConfig.Get()
		descriptor := &registry.Slice(dp.DpObjects.Get(), dp.ObjectCount)[object.CpObject.Get().DpObjectIdx]
		registry.FreeArray(&cp.EctxMemoryContext, registry.Slice(prepared, descriptor.PreparedSize))
	}
	registry.FreeNode(&cp.EctxMemoryContext, object, 0)
}

// FreeDeviceContext frees a device's context and its two entries, as
// device_ectx_free does.
func FreeDeviceContext(cp *layout.CpConfig, device *layout.DeviceEctx) {
	if input := device.InputPipelines.Get(); input != nil {
		FreeDeviceEntryContext(cp, input)
	}
	if output := device.OutputPipelines.Get(); output != nil {
		FreeDeviceEntryContext(cp, output)
	}
	FreeCounterStorage(device.CounterStorage.Get())
	registry.FreeNode(&cp.EctxMemoryContext, device, 0)
}

// FreeDeviceEntryContext frees a device entry's context and its
// pipelines, as device_entry_ectx_free does.
func FreeDeviceEntryContext(cp *layout.CpConfig, entry *layout.DeviceEntryEctx) {
	context := &cp.EctxMemoryContext
	pipelines := registry.Slice(entry.PipelinePtrs.Get(), entry.PipelineCount)
	for pipeline := range registry.Pointees(pipelines) {
		FreePipelineContext(cp, pipeline)
	}
	registry.FreeArray(context, pipelines)
	registry.FreeArray(context, registry.Slice(entry.Pipelines.Get(), entry.PipelineCount))
	registry.FreeNode(context, entry, shm.PtrSize*uintptr(entry.PipelineMapSize))
}

// FreePipelineContext frees a pipeline's context and its functions, as
// pipeline_ectx_free does.
func FreePipelineContext(cp *layout.CpConfig, pipeline *layout.PipelineEctx) {
	context := &cp.EctxMemoryContext
	functions := registry.Slice(pipeline.FunctionPtrs.Get(), pipeline.Length)
	for function := range registry.Pointees(functions) {
		FreeFunctionContext(cp, function)
	}
	FreeCounterStorage(pipeline.CounterStorage.Get())
	registry.FreeArray(context, functions)
	registry.FreeNode(context, pipeline, shm.PtrSize*uintptr(pipeline.Length))
}

// FreeFunctionContext frees a function's context and its chains, as
// function_ectx_free does.
func FreeFunctionContext(cp *layout.CpConfig, function *layout.FunctionEctx) {
	context := &cp.EctxMemoryContext
	chains := registry.Slice(function.ChainPtrs.Get(), function.ChainCount)
	for chain := range registry.Pointees(chains) {
		FreeChainContext(cp, chain)
	}
	registry.FreeArray(context, chains)
	registry.FreeArray(context, registry.Slice(function.Chains.Get(), function.ChainCount))
	FreeCounterStorage(function.CounterStorage.Get())
	registry.FreeNode(context, function, shm.PtrSize*uintptr(function.ChainMapSize))
}

// FreeChainContext frees a chain's context and its modules, as
// chain_ectx_free does.
func FreeChainContext(cp *layout.CpConfig, chain *layout.ChainEctx) {
	context := &cp.EctxMemoryContext
	modules := registry.Slice(chain.ModulePtrs.Get(), chain.Length)
	for module := range registry.Pointees(modules) {
		FreeModuleContext(cp, module)
	}
	FreeCounterStorage(chain.CounterStorage.Get())
	registry.FreeArray(context, modules)
	registry.FreeNode(context, chain, shm.PtrSize*uintptr(chain.Length))
}

// FreeModuleContext frees a module's context, as module_ectx_free does.
func FreeModuleContext(cp *layout.CpConfig, module *layout.ModuleEctx) {
	context := &cp.EctxMemoryContext
	FreeCounterStorage(module.CounterStorage.Get())
	storages := registry.Slice(module.RuntimeCounterStorages.Get(), module.RuntimeCounterStorageCount)
	for idx := range storages {
		FreeCounterStorage(storages[idx].Get())
	}
	registry.FreeArray(context, storages)
	registry.FreeArray(context, registry.Slice(module.AbsRuntimeCounterStorages.Get(), module.RuntimeCounterStorageCount))
	registry.FreeArray(context, registry.Slice(module.DeviceTargets.Get(), module.DeviceTargetCount))
	links := registry.Slice(module.ObjectLinks.Get(), module.ObjectLinkCount)
	for idx := range links {
		FreeCounterStorage(links[idx].CounterStorage.Get())
	}
	registry.FreeArray(context, links)
	// The buffer's size is the dataplane module's, found through the
	// module's own index as C finds it.
	if prepared := module.ModulePrepared.Get(); prepared != nil {
		dp := cp.DpConfig.Get()
		descriptor := &registry.Slice(dp.DpModules.Get(), dp.ModuleCount)[module.CpModule.Get().DpModuleIdx]
		registry.FreeArray(context, registry.Slice(prepared, descriptor.PreparedSize))
	}
	registry.FreeNode(context, module, 0)
}

// FreeCounterStorage drops a reference to a counter storage, as
// counter_storage_free does; a nil storage is ignored.
//
// The last reference frees the storage, and a value page goes with the
// last storage sharing it.
func FreeCounterStorage(storage *layout.CounterStorage) {
	if storage == nil || atomic.AddUint64(&storage.Refcnt, ^uint64(0)) != 0 {
		return
	}
	context := storage.MemoryContext.Get()
	// The handle table is sized by the count of the registry the storage
	// was built for, read from the agent's memory as C reads it.
	if handles := storage.CounterValueHandles.Get(); handles != nil {
		registry.FreeArray(context, registry.Slice(handles, storage.Registry.Get().Count))
	}
	for pool := range storage.Pools {
		entry := &storage.Pools[pool]
		blocks := registry.Slice(entry.Blocks.Get(), entry.BlockCount)
		for block := range registry.Pointees(blocks) {
			if atomic.AddUint64(&block.Refcnt, ^uint64(0)) == 0 {
				registry.FreeNode(context, block.Pages.Get(), 0)
				registry.FreeNode(context, block, 0)
			}
		}
		registry.FreeArray(context, blocks)
	}
	registry.FreeNode(context, storage, 0)
}

// finiStorageRegistry frees a worker's counter storage registry's index
// and items and zeroes it, as cp_config_counter_storage_registry_fini does.
//
// The storages it names are freed by the contexts that hold them.
func finiStorageRegistry(storages *layout.CpConfigCounterStorageRegistry) {
	context := storages.MemoryContext.Get()
	if context == nil {
		return
	}
	registry.IndexFini(&storages.TagIndex)
	registry.FreeArray(context, registry.Slice(storages.Items.Get(), storages.Capacity))
	*storages = layout.CpConfigCounterStorageRegistry{}
}
