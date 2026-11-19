//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// ReleaseGeneration drops the reference a pin of the instance's
// configuration took, as cp_config_gen_release does, and frees the
// generation when a publication retired it meanwhile.
func ReleaseGeneration(cp *layout.CpConfig, pinned *zone.Generation) {
	gen := pinned.Gen
	if gen == nil {
		return
	}
	pinned.Gen = nil
	shmalloc.Lock(&cp.BlockAllocator)
	defer shmalloc.Unlock(&cp.BlockAllocator)
	retire(cp, gen)
}

// retire drops the publication's reference to a replaced generation and
// frees it when no reader pins it any more. The caller holds the
// instance's allocator lock.
func retire(cp *layout.CpConfig, old *layout.CpConfigGen) {
	if atomic.AddUint64(&old.Refcnt, ^uint64(0)) == 0 {
		freeGeneration(cp, old)
	}
}

// freeGeneration releases a generation nothing references: its workers'
// contexts, the references its registries hold and its own memory, in
// the order cp_config_gen_free releases them. The caller holds the
// instance's allocator lock.
func freeGeneration(cp *layout.CpConfig, gen *layout.CpConfigGen) {
	if first := gen.ConfigGenEctxs.Get(); first != nil {
		roots := unsafe.Slice(first, gen.ConfigGenEctxCount)
		for idx := range roots {
			if root := roots[idx].Get(); root != nil {
				freeRoot(cp, root)
			}
		}
		shmalloc.ContextFreeLocked(&cp.EctxMemoryContext, unsafe.Pointer(first), 8*uintptr(gen.ConfigGenEctxCount))
	}
	entityRegistryFini(&gen.ModuleRegistry.EntityRegistry)
	registryFini(&gen.FunctionRegistry.Registry)
	registryFini(&gen.PipelineRegistry.Registry)
	entityRegistryFini(&gen.DeviceRegistry.EntityRegistry)
	entityRegistryFini(&gen.ObjectRegistry.EntityRegistry)
	shmalloc.ContextFreeLocked(&cp.MemoryContext, unsafe.Pointer(gen), unsafe.Sizeof(layout.CpConfigGen{}))
}

// registryFini drops every item's reference and frees the registry's
// arrays. An item whose last reference goes stays allocated: a module
// update never replaces a function or a pipeline, whose registries alone
// destroy their items.
func registryFini(registry *layout.Registry) {
	if registry.Items.IsNil() {
		return
	}
	for idx := range registry.Capacity {
		if item := registryGet(registry, idx); item != nil {
			atomic.AddUint64(&item.Refcnt, ^uint64(0))
		}
	}
	hashIndexFini(&registry.KeyIndex)
	shmalloc.ContextFreeLocked(registry.MemoryContext.Get(), unsafe.Pointer(registry.Items.Get()), 8*uintptr(registry.Capacity))
	*registry = layout.Registry{}
}

// entityRegistryFini drops an entity registry's references and then the
// agents' loaded counts, last, since a zero count makes an agent
// reclaimable.
func entityRegistryFini(registry *layout.CpEntityRegistry) {
	if registry.Registry.Items.IsNil() {
		return
	}
	owners := make([]*layout.Agent, 0, registry.Registry.Capacity)
	for idx := range registry.Registry.Capacity {
		if item := registryGet(&registry.Registry, idx); item != nil {
			owners = append(owners, entityOwner(item))
		}
	}
	registryFini(&registry.Registry)
	for _, owner := range owners {
		if owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0))
		}
	}
}

// freeCounterStorage drops a reference to a counter storage and frees it
// with the last one, and its value pages with their last sharer.
func freeCounterStorage(storage *layout.CounterStorage) {
	if storage == nil || atomic.AddUint64(&storage.Refcnt, ^uint64(0)) != 0 {
		return
	}
	context := storage.MemoryContext.Get()
	if handles := storage.CounterValueHandles.Get(); handles != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(handles), 8*uintptr(storage.Registry.Get().Count))
	}
	for pool := range storage.Pools {
		entry := &storage.Pools[pool]
		first := entry.Blocks.Get()
		if first == nil {
			continue
		}
		blocks := unsafe.Slice(first, entry.BlockCount)
		for idx := range blocks {
			block := blocks[idx].Get()
			if block == nil {
				continue
			}
			if atomic.AddUint64(&block.Refcnt, ^uint64(0)) == 0 {
				shmalloc.ContextFreeLocked(context, unsafe.Pointer(block.Pages.Get()), uintptr(counterPageSize))
				shmalloc.ContextFreeLocked(context, unsafe.Pointer(block), unsafe.Sizeof(layout.CounterStorageBlock{}))
			}
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(entry.BlockCount))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(storage), unsafe.Sizeof(layout.CounterStorage{}))
}

func storageRegistryFini(registry *layout.CpConfigCounterStorageRegistry) {
	context := registry.MemoryContext.Get()
	if context == nil {
		return
	}
	hashIndexFini(&registry.TagIndex)
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(registry.Items.Get()),
		unsafe.Sizeof(layout.CpCounterStorage{})*uintptr(registry.Capacity),
	)
	*registry = layout.CpConfigCounterStorageRegistry{}
}

func freeRoot(cp *layout.CpConfig, root *layout.ConfigGenEctx) {
	context := &cp.EctxMemoryContext
	if first := root.DevicePtrs.Get(); first != nil {
		devices := unsafe.Slice(first, root.DeviceCount)
		for idx := range devices {
			if device := devices[idx].Get(); device != nil {
				freeDevice(cp, device)
			}
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(root.DeviceCount))
	}
	if egress := root.EgressBacking.Get(); egress != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(egress), 2*uintptr(root.DeviceCount))
	}
	if buckets := root.TxBuckets.Get(); buckets != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(buckets), unsafe.Sizeof(layout.PacketList{})*uintptr(root.TxBucketCount))
	}
	if backing := root.BackingRegistry.Get(); backing != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(backing), 2*uintptr(root.BackingRegistryCount))
	}
	if first := root.Objects.Get(); first != nil {
		objects := unsafe.Slice(first, root.ObjectCount)
		for idx := range objects {
			if object := objects[idx].Get(); object != nil {
				freeObject(cp, object)
			}
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(root.ObjectCount))
	}
	if registry := root.CounterStorageRegistry.Get(); registry != nil {
		storageRegistryFini(registry)
		shmalloc.ContextFreeLocked(&cp.MemoryContext, unsafe.Pointer(registry), unsafe.Sizeof(layout.CpConfigCounterStorageRegistry{}))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(root), unsafe.Sizeof(layout.ConfigGenEctx{})+8*uintptr(root.DeviceCount))
}

func freeObject(cp *layout.CpConfig, object *layout.ObjectEctx) {
	context := &cp.EctxMemoryContext
	freeCounterStorage(object.CounterStorage.Get())
	if prepared := object.ObjectPrepared.Get(); prepared != nil {
		dp := cp.DpConfig.Get()
		descriptor := &unsafe.Slice(dp.DpObjects.Get(), dp.ObjectCount)[object.CpObject.Get().DpObjectIdx]
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(prepared), uintptr(descriptor.PreparedSize))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(object), unsafe.Sizeof(layout.ObjectEctx{}))
}

func freeDevice(cp *layout.CpConfig, device *layout.DeviceEctx) {
	if input := device.InputPipelines.Get(); input != nil {
		freeEntry(cp, input)
	}
	if output := device.OutputPipelines.Get(); output != nil {
		freeEntry(cp, output)
	}
	freeCounterStorage(device.CounterStorage.Get())
	shmalloc.ContextFreeLocked(&cp.EctxMemoryContext, unsafe.Pointer(device), unsafe.Sizeof(layout.DeviceEctx{}))
}

func freeEntry(cp *layout.CpConfig, entry *layout.DeviceEntryEctx) {
	context := &cp.EctxMemoryContext
	if first := entry.PipelinePtrs.Get(); first != nil {
		pipelines := unsafe.Slice(first, entry.PipelineCount)
		for idx := range pipelines {
			if pipeline := pipelines[idx].Get(); pipeline != nil {
				freePipeline(cp, pipeline)
			}
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(entry.PipelineCount))
	}
	if absolute := entry.Pipelines.Get(); absolute != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(absolute), 8*uintptr(entry.PipelineCount))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(entry), unsafe.Sizeof(layout.DeviceEntryEctx{})+8*uintptr(entry.PipelineMapSize))
}

func freePipeline(cp *layout.CpConfig, pipeline *layout.PipelineEctx) {
	context := &cp.EctxMemoryContext
	first := pipeline.FunctionPtrs.Get()
	if first != nil {
		functions := unsafe.Slice(first, pipeline.Length)
		for idx := range functions {
			if function := functions[idx].Get(); function != nil {
				freeFunction(cp, function)
			}
		}
	}
	freeCounterStorage(pipeline.CounterStorage.Get())
	if first != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(pipeline.Length))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(pipeline), unsafe.Sizeof(layout.PipelineEctx{})+8*uintptr(pipeline.Length))
}

func freeFunction(cp *layout.CpConfig, function *layout.FunctionEctx) {
	context := &cp.EctxMemoryContext
	if first := function.ChainPtrs.Get(); first != nil {
		chains := unsafe.Slice(first, function.ChainCount)
		for idx := range chains {
			if chain := chains[idx].Get(); chain != nil {
				freeChain(cp, chain)
			}
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(function.ChainCount))
	}
	if absolute := function.Chains.Get(); absolute != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(absolute), 8*uintptr(function.ChainCount))
	}
	freeCounterStorage(function.CounterStorage.Get())
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(function), unsafe.Sizeof(layout.FunctionEctx{})+8*uintptr(function.ChainMapSize))
}

func freeChain(cp *layout.CpConfig, chain *layout.ChainEctx) {
	context := &cp.EctxMemoryContext
	first := chain.ModulePtrs.Get()
	if first != nil {
		modules := unsafe.Slice(first, chain.Length)
		for idx := range modules {
			if module := modules[idx].Get(); module != nil {
				freeModule(cp, module)
			}
		}
	}
	freeCounterStorage(chain.CounterStorage.Get())
	if first != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(chain.Length))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(chain), unsafe.Sizeof(layout.ChainEctx{})+8*uintptr(chain.Length))
}

func freeModule(cp *layout.CpConfig, module *layout.ModuleEctx) {
	context := &cp.EctxMemoryContext
	freeCounterStorage(module.CounterStorage.Get())
	if first := module.RuntimeCounterStorages.Get(); first != nil {
		storages := unsafe.Slice(first, module.RuntimeCounterStorageCount)
		for idx := range storages {
			freeCounterStorage(storages[idx].Get())
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), 8*uintptr(module.RuntimeCounterStorageCount))
	}
	if absolute := module.AbsRuntimeCounterStorages.Get(); absolute != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(absolute), 8*uintptr(module.RuntimeCounterStorageCount))
	}
	if targets := module.DeviceTargets.Get(); targets != nil {
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(targets), unsafe.Sizeof(layout.ModuleDeviceTarget{})*uintptr(module.DeviceTargetCount))
	}
	if first := module.ObjectLinks.Get(); first != nil {
		links := unsafe.Slice(first, module.ObjectLinkCount)
		for idx := range links {
			freeCounterStorage(links[idx].CounterStorage.Get())
		}
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(first), unsafe.Sizeof(layout.ModuleObjectLinkEctx{})*uintptr(module.ObjectLinkCount))
	}
	if prepared := module.ModulePrepared.Get(); prepared != nil {
		dp := cp.DpConfig.Get()
		descriptor := &unsafe.Slice(dp.DpModules.Get(), dp.ModuleCount)[module.CpModule.Get().DpModuleIdx]
		shmalloc.ContextFreeLocked(context, unsafe.Pointer(prepared), uintptr(descriptor.PreparedSize))
	}
	shmalloc.ContextFreeLocked(context, unsafe.Pointer(module), unsafe.Sizeof(layout.ModuleEctx{}))
}
