package cpconfig

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// maxWeightSum bounds the weights of one function's chains and of one
// device entry's pipelines (YANET_MAX_WEIGHT_SUM).
const maxWeightSum = 65535

const (
	directionInput  = 0
	directionOutput = 1
	noPort          = 0xffff
)

// BuildWorkerContexts builds the execution contexts of every worker for
// a generation, as config_gen_ectxs_create does: a whole tree per worker,
// device to module, whose counter storages continue those old has at the
// same places.
func BuildWorkerContexts(gen, old *layout.CpConfigGen, workerCount uint64) (*shm.Ptr[layout.ConfigGenEctx], error) {
	if workerCount == 0 {
		return nil, fmt.Errorf("cannot build execution context for zero workers")
	}
	cp := gen.CpConfig.Get()
	pointerSize := unsafe.Sizeof(shm.Ptr[layout.ConfigGenEctx]{})
	block := zeroedAlloc(&cp.EctxMemoryContext, pointerSize*uintptr(workerCount))
	if block == nil {
		return nil, fmt.Errorf("allocate per-worker contexts: %w", ErrNoMemory)
	}
	roots := unsafe.Slice((*shm.Ptr[layout.ConfigGenEctx])(block), workerCount)
	for worker := range workerCount {
		builder := &contextBuilder{gen: gen, old: old, cp: cp, dp: cp.DpConfig.Get(), worker: worker}
		root, err := builder.root()
		if err != nil {
			return nil, fmt.Errorf("build execution context of worker %d: %w", worker, err)
		}
		roots[worker].Set(root)
	}
	return &roots[0], nil
}

// contextBuilder builds the execution contexts of one worker.
type contextBuilder struct {
	gen    *layout.CpConfigGen
	old    *layout.CpConfigGen
	cp     *layout.CpConfig
	dp     *layout.DpConfig
	worker uint64
	// tree is the worker's root context being built.
	tree *layout.ConfigGenEctx
}

// alloc returns zeroed memory for a context.
func (m *contextBuilder) alloc(size uintptr) unsafe.Pointer {
	return zeroedAlloc(&m.cp.EctxMemoryContext, size)
}

// oldStorages returns the counter storage registry of the same worker in
// the previous generation, or nil.
func (m *contextBuilder) oldStorages() *layout.CpConfigCounterStorageRegistry {
	if m.old == nil || m.worker >= m.old.ConfigGenEctxCount {
		return nil
	}
	roots := m.old.ConfigGenEctxs.Get()
	if roots == nil {
		return nil
	}
	root := unsafe.Slice(roots, m.old.ConfigGenEctxCount)[m.worker].Get()
	if root == nil {
		return nil
	}
	return root.CounterStorageRegistry.Get()
}

// spawn returns the worker's counter storage for a registry, continuing
// the one registered under tags in the previous generation, and
// registers it under tags in this one.
func (m *contextBuilder) spawn(tags []layout.CounterTag, registry *layout.CounterRegistry) (*layout.CounterStorage, error) {
	old := storageRegistryLookup(m.oldStorages(), tags)
	storage := spawnCounterStorage(&m.cp.CounterStorageMemoryContext, old, registry)
	if storage == nil {
		return nil, fmt.Errorf("spawn counter storage: %w", ErrNoMemory)
	}
	if err := storageRegistryInsert(m.tree.CounterStorageRegistry.Get(), tags, storage); err != nil {
		return nil, err
	}
	return storage, nil
}

func (m *contextBuilder) root() (*layout.ConfigGenEctx, error) {
	deviceCount := m.gen.DeviceRegistry.EntityRegistry.Registry.Capacity
	pointerSize := uintptr(8)
	tree := (*layout.ConfigGenEctx)(m.alloc(unsafe.Sizeof(layout.ConfigGenEctx{}) + pointerSize*uintptr(deviceCount)))
	if tree == nil {
		return nil, fmt.Errorf("allocate generation context: %w", ErrNoMemory)
	}
	m.tree = tree
	tree.CpConfigGen.Set(m.gen)
	tree.DeviceCount = deviceCount
	tree.ObjectCount = m.gen.ObjectRegistry.EntityRegistry.Registry.Capacity

	devicePtrs := unsafe.Slice((*shm.Ptr[layout.DeviceEctx])(m.alloc(pointerSize*uintptr(deviceCount))), deviceCount)
	if deviceCount > 0 {
		tree.DevicePtrs.Set(&devicePtrs[0])
	}

	egress := unsafe.Slice((*uint16)(shmalloc.ContextAlloc(&m.cp.EctxMemoryContext, 2*uintptr(deviceCount))), deviceCount)
	for idx := range egress {
		egress[idx] = noPort
	}
	if deviceCount > 0 {
		tree.EgressBacking.Set(&egress[0])
	}

	ports := m.dp.DpTopology.DeviceCount
	tree.TxBucketCount = uint32(ports)
	buckets := unsafe.Slice((*layout.PacketList)(shmalloc.ContextAlloc(&m.cp.EctxMemoryContext,
		unsafe.Sizeof(layout.PacketList{})*uintptr(ports))), ports)
	for idx := range buckets {
		clear(unsafe.Slice((*byte)(unsafe.Pointer(&buckets[idx])), unsafe.Sizeof(layout.PacketList{})))
	}
	if ports > 0 {
		tree.TxBuckets.Set(&buckets[0])
	}

	registry := (*layout.CpConfigCounterStorageRegistry)(shmalloc.ContextAlloc(&m.cp.MemoryContext,
		unsafe.Sizeof(layout.CpConfigCounterStorageRegistry{})))
	if registry == nil {
		return nil, fmt.Errorf("allocate counter storage registry: %w", ErrNoMemory)
	}
	if err := storageRegistryInit(&m.cp.MemoryContext, registry); err != nil {
		return nil, err
	}
	tree.CounterStorageRegistry.Set(registry)

	if tree.ObjectCount > 0 {
		objects := unsafe.Slice((*shm.Ptr[layout.ObjectEctx])(m.alloc(pointerSize*uintptr(tree.ObjectCount))), tree.ObjectCount)
		tree.Objects.Set(&objects[0])
		for idx := range tree.ObjectCount {
			item := registryGet(&m.gen.ObjectRegistry.EntityRegistry.Registry, idx)
			if item == nil {
				continue
			}
			object, err := m.object(containerOf[layout.CpObject](item, 0))
			if err != nil {
				return nil, err
			}
			objects[idx].Set(object)
		}
	}

	topology := unsafe.Slice(m.dp.DpTopology.Devices.Get(), ports)
	for idx := range deviceCount {
		item := registryGet(&m.gen.DeviceRegistry.EntityRegistry.Registry, idx)
		if item == nil {
			continue
		}
		device := containerOf[layout.CpDevice](item, 0)
		for port := range topology {
			if shm.String(topology[port].DeviceName[:]) == shm.String(device.Name[:]) {
				egress[idx] = uint16(port)
				break
			}
		}
		context, err := m.device(device, idx)
		if err != nil {
			return nil, err
		}
		devicePtrs[idx].Set(context)
		context.InputPipelines.Get().DeviceId = uint16(idx)
		context.OutputPipelines.Get().DeviceId = uint16(idx)
	}

	tree.BackingRegistryCount = ports
	backing := unsafe.Slice((*uint16)(shmalloc.ContextAlloc(&m.cp.EctxMemoryContext, 2*uintptr(ports))), ports)
	for idx := range backing {
		backing[idx] = noPort
	}
	for idx := range deviceCount {
		port := egress[idx]
		if port == noPort || uint64(port) >= ports {
			continue
		}
		if backing[port] == noPort {
			backing[port] = uint16(idx)
		}
	}
	if ports > 0 {
		tree.BackingRegistry.Set(&backing[0])
	}

	m.link(devicePtrs)
	return tree, nil
}

func (m *contextBuilder) object(object *layout.CpObject) (*layout.ObjectEctx, error) {
	context := (*layout.ObjectEctx)(m.alloc(unsafe.Sizeof(layout.ObjectEctx{})))
	if context == nil {
		return nil, fmt.Errorf("allocate object context: %w", ErrNoMemory)
	}
	context.CpObject.Set(object)
	context.WorkerIdx = m.worker
	descriptor := &unsafe.Slice(m.dp.DpObjects.Get(), m.dp.ObjectCount)[object.DpObjectIdx]
	if descriptor.PreparedSize > 0 {
		prepared := m.alloc(uintptr(descriptor.PreparedSize))
		if prepared == nil {
			return nil, fmt.Errorf("allocate object prepared buffer: %w", ErrNoMemory)
		}
		context.ObjectPrepared.Set((*byte)(prepared))
	}
	storage, err := m.spawn(objectTags(shm.String(object.Type[:]), shm.String(object.Name[:])), &object.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)
	return context, nil
}

func (m *contextBuilder) device(device *layout.CpDevice, index uint64) (*layout.DeviceEctx, error) {
	context := (*layout.DeviceEctx)(m.alloc(unsafe.Sizeof(layout.DeviceEctx{})))
	if context == nil {
		return nil, fmt.Errorf("allocate device context: %w", ErrNoMemory)
	}
	context.DeviceId = uint16(index)
	context.CpDevice.Set(device)
	storage, err := m.spawn(deviceTags(shm.String(device.Name[:])), &device.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)

	descriptor := &unsafe.Slice(m.dp.DpDevices.Get(), m.dp.DeviceCount)[device.DpDeviceIdx]
	input, err := m.entry(context, descriptor.InputHandler, device.InputPipelines.Get())
	if err != nil {
		return nil, err
	}
	context.InputPipelines.Set(input)
	input.DeviceEctx.Set(context)
	input.Direction = directionInput
	output, err := m.entry(context, descriptor.OutputHandler, device.OutputPipelines.Get())
	if err != nil {
		return nil, err
	}
	context.OutputPipelines.Set(output)
	output.DeviceEctx.Set(context)
	output.Direction = directionOutput
	return context, nil
}

func (m *contextBuilder) entry(device *layout.DeviceEctx, handler shm.Local, entry *layout.CpDeviceEntry) (*layout.DeviceEntryEctx, error) {
	pipelines := entry.Pipelines(int(entry.PipelineCount))
	weightSum := uint64(0)
	for idx := range pipelines {
		if pipelines[idx].Weight > maxWeightSum-weightSum {
			return nil, fmt.Errorf("pipeline weight sum exceeds %d", maxWeightSum)
		}
		weightSum += pipelines[idx].Weight
	}
	pointerSize := uintptr(8)
	context := (*layout.DeviceEntryEctx)(m.alloc(unsafe.Sizeof(layout.DeviceEntryEctx{}) + pointerSize*uintptr(weightSum)))
	if context == nil {
		return nil, fmt.Errorf("allocate device entry context: %w", ErrNoMemory)
	}
	context.Handler = handler
	ptrsBlock := shmalloc.ContextAlloc(&m.cp.EctxMemoryContext, pointerSize*uintptr(entry.PipelineCount))
	context.PipelineCount = entry.PipelineCount
	if entry.PipelineCount == 0 {
		return context, nil
	}
	ptrs := unsafe.Slice((*shm.Ptr[layout.PipelineEctx])(ptrsBlock), entry.PipelineCount)
	clear(unsafe.Slice((*byte)(ptrsBlock), pointerSize*uintptr(entry.PipelineCount)))
	context.PipelinePtrs.Set(&ptrs[0])
	absolute := unsafe.Slice((*shm.Ptr[layout.PipelineEctx])(m.alloc(pointerSize*uintptr(entry.PipelineCount))), entry.PipelineCount)
	context.Pipelines.Set(&absolute[0])
	context.PipelineMapSize = weightSum

	pipelineMap := context.PipelineMap(int(weightSum))
	position := 0
	for idx := range pipelines {
		name := shm.String(pipelines[idx].Name[:])
		pipeline := m.lookupPipeline(name)
		if pipeline == nil {
			return nil, fmt.Errorf("pipeline %q not found in device entry", name)
		}
		built, err := m.pipeline(device, pipeline)
		if err != nil {
			return nil, err
		}
		ptrs[idx].Set(built)
		for range pipelines[idx].Weight {
			pipelineMap[position].Set(built)
			position++
		}
	}
	return context, nil
}

func (m *contextBuilder) pipeline(device *layout.DeviceEctx, pipeline *layout.CpPipeline) (*layout.PipelineEctx, error) {
	pointerSize := uintptr(8)
	context := (*layout.PipelineEctx)(m.alloc(unsafe.Sizeof(layout.PipelineEctx{}) + pointerSize*uintptr(pipeline.Length)))
	if context == nil {
		return nil, fmt.Errorf("allocate pipeline context: %w", ErrNoMemory)
	}
	context.CpPipeline.Set(pipeline)
	context.Length = pipeline.Length
	ptrs := unsafe.Slice((*shm.Ptr[layout.FunctionEctx])(m.alloc(pointerSize*uintptr(pipeline.Length))), pipeline.Length)
	if pipeline.Length > 0 {
		context.FunctionPtrs.Set(&ptrs[0])
	}
	deviceName := shm.String(device.CpDevice.Get().Name[:])
	pipelineName := shm.String(pipeline.Name[:])
	storage, err := m.spawn(pipelineTags(deviceName, pipelineName), &pipeline.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)
	functions := pipeline.Functions(int(pipeline.Length))
	for idx := range functions {
		name := shm.String(functions[idx].Name[:])
		function := m.lookupFunction(name)
		if function == nil {
			return nil, fmt.Errorf("function %q not found for pipeline %q", name, pipelineName)
		}
		built, err := m.function(device, context, function)
		if err != nil {
			return nil, err
		}
		ptrs[idx].Set(built)
	}
	return context, nil
}

func (m *contextBuilder) function(device *layout.DeviceEctx, pipeline *layout.PipelineEctx, function *layout.CpFunction) (*layout.FunctionEctx, error) {
	chains := function.Chains(int(function.ChainCount))
	weightSum := uint64(0)
	for idx := range chains {
		if chains[idx].Weight > maxWeightSum-weightSum {
			return nil, fmt.Errorf("chain weight sum exceeds %d", maxWeightSum)
		}
		weightSum += chains[idx].Weight
	}
	pointerSize := uintptr(8)
	context := (*layout.FunctionEctx)(m.alloc(unsafe.Sizeof(layout.FunctionEctx{}) + pointerSize*uintptr(weightSum)))
	if context == nil {
		return nil, fmt.Errorf("allocate function context: %w", ErrNoMemory)
	}
	context.CpFunction.Set(function)
	context.ChainMapSize = weightSum
	ptrsBlock := shmalloc.ContextAlloc(&m.cp.EctxMemoryContext, pointerSize*uintptr(function.ChainCount))
	if ptrsBlock == nil {
		return nil, fmt.Errorf("allocate chains of function %q: %w", shm.String(function.Name[:]), ErrNoMemory)
	}
	clear(unsafe.Slice((*byte)(ptrsBlock), pointerSize*uintptr(function.ChainCount)))
	ptrs := unsafe.Slice((*shm.Ptr[layout.ChainEctx])(ptrsBlock), function.ChainCount)
	context.ChainPtrs.Set(&ptrs[0])
	absolute := unsafe.Slice((*shm.Ptr[layout.ChainEctx])(m.alloc(pointerSize*uintptr(function.ChainCount))), function.ChainCount)
	context.Chains.Set(&absolute[0])
	context.ChainCount = function.ChainCount

	deviceName := shm.String(device.CpDevice.Get().Name[:])
	pipelineName := shm.String(pipeline.CpPipeline.Get().Name[:])
	storage, err := m.spawn(functionTags(deviceName, pipelineName, shm.String(function.Name[:])), &function.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)

	chainMap := context.ChainMap(int(weightSum))
	position := 0
	for idx := range chains {
		built, err := m.chain(device, pipeline, context, chains[idx].CpChain.Get())
		if err != nil {
			return nil, err
		}
		ptrs[idx].Set(built)
		for range chains[idx].Weight {
			chainMap[position].Set(built)
			position++
		}
	}
	return context, nil
}

func (m *contextBuilder) chain(device *layout.DeviceEctx, pipeline *layout.PipelineEctx, function *layout.FunctionEctx,
	chain *layout.CpChain,
) (*layout.ChainEctx, error) {
	pointerSize := uintptr(8)
	context := (*layout.ChainEctx)(m.alloc(unsafe.Sizeof(layout.ChainEctx{}) + pointerSize*uintptr(chain.Length)))
	if context == nil {
		return nil, fmt.Errorf("allocate chain context: %w", ErrNoMemory)
	}
	context.CpChain.Set(chain)
	context.Length = chain.Length
	ptrs := unsafe.Slice((*shm.Ptr[layout.ModuleEctx])(m.alloc(pointerSize*uintptr(chain.Length))), chain.Length)
	if chain.Length > 0 {
		context.ModulePtrs.Set(&ptrs[0])
	}
	path := pathNames{
		device:   shm.String(device.CpDevice.Get().Name[:]),
		pipeline: shm.String(pipeline.CpPipeline.Get().Name[:]),
		function: shm.String(function.CpFunction.Get().Name[:]),
		chain:    shm.String(chain.Name[:]),
	}
	storage, err := m.spawn(chainTags(path.device, path.pipeline, path.function, path.chain), &chain.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)
	modules := chain.Modules(int(chain.Length))
	for idx := range modules {
		moduleType, moduleName := shm.String(modules[idx].Type[:]), shm.String(modules[idx].Name[:])
		module := lookupModule(&m.gen.ModuleRegistry, moduleType, moduleName)
		if module == nil {
			return nil, fmt.Errorf("module %s:%s not found in chain %q", moduleType, moduleName, path.chain)
		}
		built, err := m.module(device, path, module)
		if err != nil {
			return nil, err
		}
		ptrs[idx].Set(built)
	}
	return context, nil
}

// pathNames names the place of a module context in the tree.
type pathNames struct {
	device, pipeline, function, chain string
}

func (m *contextBuilder) module(device *layout.DeviceEctx, path pathNames, module *layout.CpModule) (*layout.ModuleEctx, error) {
	context := (*layout.ModuleEctx)(m.alloc(unsafe.Sizeof(layout.ModuleEctx{})))
	if context == nil {
		return nil, fmt.Errorf("allocate module context: %w", ErrNoMemory)
	}
	context.RegistryDeviceId = device.DeviceId
	context.CpModule.Set(module)
	context.ConfigGenEctx.Set(m.tree)
	context.PacketRecircLimit = m.dp.PacketRecircLimit
	descriptor := &unsafe.Slice(m.dp.DpModules.Get(), m.dp.ModuleCount)[module.DpModuleIdx]
	context.Handler = descriptor.Handler
	if descriptor.PreparedSize > 0 {
		prepared := m.alloc(uintptr(descriptor.PreparedSize))
		if prepared == nil {
			return nil, fmt.Errorf("allocate module prepared buffer: %w", ErrNoMemory)
		}
		context.ModulePrepared.Set((*byte)(prepared))
	}

	moduleType, moduleName := shm.String(module.Type[:]), shm.String(module.Name[:])
	modulePath := modulePathTags(path.device, path.pipeline, path.function, path.chain, moduleType, moduleName)
	storage, err := m.spawn(moduleTags(modulePath), &module.CounterRegistry)
	if err != nil {
		return nil, err
	}
	context.CounterStorage.Set(storage)

	if registries := runtimeRegistries(module); len(registries) > 0 {
		pointerSize := uintptr(8)
		storages := unsafe.Slice((*shm.Ptr[layout.CounterStorage])(m.alloc(pointerSize*uintptr(len(registries)))), len(registries))
		context.RuntimeCounterStorageCount = uint64(len(registries))
		context.RuntimeCounterStorages.Set(&storages[0])
		absolute := shmalloc.ContextAlloc(&m.cp.EctxMemoryContext, pointerSize*uintptr(len(registries)))
		context.AbsRuntimeCounterStorages.Set((*shm.Ptr[layout.CounterStorage])(absolute))
		for idx := range registries {
			entry := registries[idx].Get()
			runtime, err := m.spawn(runtimeTags(modulePath, shm.String(entry.Tag[:])), &entry.Registry)
			if err != nil {
				return nil, err
			}
			storages[idx].Set(runtime)
		}
	}

	if module.ObjectCount > 0 {
		links := unsafe.Slice((*layout.ModuleObjectLinkEctx)(m.alloc(unsafe.Sizeof(layout.ModuleObjectLinkEctx{})*uintptr(module.ObjectCount))), module.ObjectCount)
		context.ObjectLinkCount = module.ObjectCount
		context.ObjectLinks.Set(&links[0])
		linked := unsafe.Slice(module.Objects.Get(), module.ObjectCount)
		objects := unsafe.Slice(m.tree.Objects.Get(), m.tree.ObjectCount)
		for idx := range linked {
			objectType, objectName := shm.String(linked[idx].Type[:]), shm.String(linked[idx].Name[:])
			index, ok := m.lookupObject(objectType, objectName)
			if !ok {
				return nil, fmt.Errorf("linked object %s:%s not found for module %s:%s", objectType, objectName, moduleType, moduleName)
			}
			object := containerOf[layout.CpObject](registryGet(&m.gen.ObjectRegistry.EntityRegistry.Registry, index), 0)
			objectContext := objects[index].Get()
			linkStorage, err := m.spawn(objectLinkTags(modulePath, objectType, objectName), &object.LinkCounterRegistry)
			if err != nil {
				return nil, err
			}
			links[idx].CounterStorage.Set(linkStorage)
			links[idx].ObjectEctx.Set(objectContext)
		}
	}
	return context, nil
}

// link fills every module context's device targets: the entries of the
// devices the module names.
func (m *contextBuilder) link(devices []shm.Ptr[layout.DeviceEctx]) {
	for deviceIdx := range devices {
		device := devices[deviceIdx].Get()
		if device == nil {
			continue
		}
		for _, entry := range []*layout.DeviceEntryEctx{device.InputPipelines.Get(), device.OutputPipelines.Get()} {
			pipelines := unsafe.Slice(entry.PipelinePtrs.Get(), entry.PipelineCount)
			for pipelineIdx := range pipelines {
				pipeline := pipelines[pipelineIdx].Get()
				if pipeline == nil {
					continue
				}
				functions := unsafe.Slice(pipeline.FunctionPtrs.Get(), pipeline.Length)
				for functionIdx := range functions {
					function := functions[functionIdx].Get()
					if function == nil {
						continue
					}
					chains := unsafe.Slice(function.ChainPtrs.Get(), function.ChainCount)
					for chainIdx := range chains {
						chain := chains[chainIdx].Get()
						if chain == nil {
							continue
						}
						modules := unsafe.Slice(chain.ModulePtrs.Get(), chain.Length)
						for moduleIdx := range modules {
							if module := modules[moduleIdx].Get(); module != nil {
								m.linkModule(devices, device, module)
							}
						}
					}
				}
			}
		}
	}
}

func (m *contextBuilder) linkModule(devices []shm.Ptr[layout.DeviceEctx], owner *layout.DeviceEctx, context *layout.ModuleEctx) {
	module := context.CpModule.Get()
	targets := unsafe.Slice((*layout.ModuleDeviceTarget)(shmalloc.ContextAlloc(&m.cp.EctxMemoryContext,
		unsafe.Sizeof(layout.ModuleDeviceTarget{})*uintptr(module.DeviceCount))), module.DeviceCount)
	for idx := range targets {
		targets[idx].DeviceId = noPort
		targets[idx].InputEntry.Set(nil)
		targets[idx].OutputEntry.Set(nil)
	}
	if len(targets) > 0 {
		context.DeviceTargets.Set(&targets[0])
	}
	context.DeviceTargetCount = module.DeviceCount
	ownerName := shm.String(owner.CpDevice.Get().Name[:])
	named := unsafe.Slice(module.Devices.Get(), module.DeviceCount)
	for idx := range named {
		name := shm.String(named[idx].Name[:])
		if name == ownerName {
			context.ModuleDeviceId = uint32(idx)
		}
		for deviceIdx := range devices {
			target := devices[deviceIdx].Get()
			if target == nil || shm.String(target.CpDevice.Get().Name[:]) != name {
				continue
			}
			targets[idx].DeviceId = uint16(deviceIdx)
			targets[idx].InputEntry.Set(target.InputPipelines.Get())
			targets[idx].OutputEntry.Set(target.OutputPipelines.Get())
		}
	}
}

func (m *contextBuilder) lookupFunction(name string) *layout.CpFunction {
	registry := &m.gen.FunctionRegistry.Registry
	offset := unsafe.Offsetof(layout.CpFunction{}.ConfigItem)
	index, ok := registryLookup(registry, hashString(name), func(item *layout.RegistryItem) bool {
		return shm.String(containerOf[layout.CpFunction](item, offset).Name[:]) == name
	})
	if !ok {
		return nil
	}
	return containerOf[layout.CpFunction](registryGet(registry, index), offset)
}

func (m *contextBuilder) lookupPipeline(name string) *layout.CpPipeline {
	registry := &m.gen.PipelineRegistry.Registry
	offset := unsafe.Offsetof(layout.CpPipeline{}.ConfigItem)
	index, ok := registryLookup(registry, hashString(name), func(item *layout.RegistryItem) bool {
		return shm.String(containerOf[layout.CpPipeline](item, offset).Name[:]) == name
	})
	if !ok {
		return nil
	}
	return containerOf[layout.CpPipeline](registryGet(registry, index), offset)
}

func (m *contextBuilder) lookupObject(objectType, name string) (uint64, bool) {
	return registryLookup(&m.gen.ObjectRegistry.EntityRegistry.Registry, hashStringPair(name, objectType),
		func(item *layout.RegistryItem) bool {
			object := containerOf[layout.CpObject](item, 0)
			return shm.String(object.Type[:]) == objectType && shm.String(object.Name[:]) == name
		},
	)
}

// hashString hashes a one-field registry key.
func hashString(value string) uint64 {
	return fnv1a(strIndexKey(value, len(value)), registryHashSeed)
}

// hashStringPair hashes a two-field registry key.
func hashStringPair(first, second string) uint64 {
	hash := fnv1a(strIndexKey(first, len(first)), registryHashSeed)
	return fnv1a(strIndexKey(second, len(second)), uint32(hash))
}

// zeroedAlloc allocates size bytes from a context and zeroes them, or
// returns nil.
func zeroedAlloc(context *layout.MemoryContext, size uintptr) unsafe.Pointer {
	block := shmalloc.ContextAlloc(context, size)
	if block != nil {
		clear(unsafe.Slice((*byte)(block), size))
	}
	return block
}
