//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"iter"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// maxWeightSum bounds the weights of one function's chains and of one
// device entry's pipelines (YANET_MAX_WEIGHT_SUM).
const maxWeightSum = 65535

const (
	// directionInput and directionOutput mark a device's two entries.
	directionInput  = 0
	directionOutput = 1
	// noPort marks a device without a physical port and a port without a
	// device.
	noPort = 0xffff
)

// contexts builds every worker's tree for the generation and returns the
// array of their roots, as config_gen_ectxs_create does.
//
// Each worker gets a tree, device down to module, whose counter storages
// continue those of the generation it replaces at the same places. A
// worker that fails frees the trees built before it and the array.
func (m *build) contexts(workerCount uint64) (*shm.Ptr[layout.ConfigGenEctx], error) {
	if workerCount == 0 {
		return nil, errors.New("cannot build execution contexts for zero workers")
	}
	context := &m.cp.EctxMemoryContext
	roots, ok := allocArray[shm.Ptr[layout.ConfigGenEctx]](context, workerCount, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate worker roots: %w", errZoneMemory)
	}
	for worker := range roots {
		m.worker = uint64(worker)
		root, err := m.root()
		if err != nil {
			for built := range registry.Pointees(roots) {
				teardown.FreeWorkerContext(m.cp, built)
			}
			shmalloc.FreeArray(context, roots)
			return nil, fmt.Errorf("worker %d: %w", worker, err)
		}
		roots[worker].Set(root)
	}
	return &roots[0], nil
}

// pathNames names the place of a context in the tree, as far down as the
// build has come.
type pathNames struct {
	Device, Pipeline, Function, Chain string
}

// root builds the worker's root context, its tables, counter storage
// registry, objects and devices, as config_gen_ectx_create does.
//
// A failed root frees what it built.
func (m *build) root() (_ *layout.ConfigGenEctx, err error) {
	context := &m.cp.EctxMemoryContext
	deviceCount := m.gen.DeviceRegistry.EntityRegistry.Registry.Capacity
	tree := newNode[layout.ConfigGenEctx](context, shm.PtrSize*uintptr(deviceCount))
	if tree == nil {
		return nil, fmt.Errorf("allocate root context: %w", errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeWorkerContext(m.cp, tree)
		}
	}()
	m.tree = tree
	tree.CpConfigGen.Set(m.gen)
	tree.DeviceCount = deviceCount
	tree.ObjectCount = m.gen.ObjectRegistry.EntityRegistry.Registry.Capacity

	devices, ok := allocArray[shm.Ptr[layout.DeviceEctx]](context, deviceCount, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate device contexts: %w", errZoneMemory)
	}
	if deviceCount > 0 {
		tree.DevicePtrs.Set(&devices[0])
	}
	// Every slot reads as port-less until a port answers to its name.
	egress, ok := allocArray[uint16](context, deviceCount, stale)
	if !ok {
		return nil, fmt.Errorf("allocate egress table: %w", errZoneMemory)
	}
	for idx := range egress {
		egress[idx] = noPort
	}
	if deviceCount > 0 {
		tree.EgressBacking.Set(&egress[0])
	}
	ports := m.dp.DpTopology.DeviceCount
	tree.TxBucketCount = uint32(ports)
	buckets, ok := allocArray[layout.PacketList](context, uint64(tree.TxBucketCount), zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate transmit buckets: %w", errZoneMemory)
	}
	if len(buckets) > 0 {
		tree.TxBuckets.Set(&buckets[0])
	}

	storages := newNode[layout.CpConfigCounterStorageRegistry](&m.cp.MemoryContext, 0)
	if storages == nil {
		return nil, fmt.Errorf("allocate counter storage registry: %w", errZoneMemory)
	}
	if err := m.initStorages(storages); err != nil {
		shmalloc.FreeNode(&m.cp.MemoryContext, storages, 0)
		return nil, err
	}
	tree.CounterStorageRegistry.Set(storages)

	if err := m.objects(); err != nil {
		return nil, err
	}
	topology := registry.Slice(m.dp.DpTopology.Devices.Get(), ports)
	for idx, item := range registry.Items(&m.gen.DeviceRegistry.EntityRegistry.Registry) {
		device := registry.Container[layout.CpDevice](item, 0)
		name := shm.String(device.Name[:])
		for port := range topology {
			if shm.Equal(topology[port].DeviceName[:], name) {
				egress[idx] = uint16(port)
				break
			}
		}
		built, err := m.device(device, pathNames{Device: name}, idx)
		if err != nil {
			return nil, err
		}
		devices[idx].Set(built)
		built.InputPipelines.Get().DeviceId = uint16(idx)
		built.OutputPipelines.Get().DeviceId = uint16(idx)
	}

	// The first device a port backs wins it; a port no device names keeps
	// the port-less mark.
	tree.BackingRegistryCount = ports
	backing, ok := allocArray[uint16](context, ports, stale)
	if !ok {
		return nil, fmt.Errorf("allocate backing table: %w", errZoneMemory)
	}
	for idx := range backing {
		backing[idx] = noPort
	}
	for idx, port := range egress {
		if port != noPort && uint64(port) < ports && backing[port] == noPort {
			backing[port] = uint16(idx)
		}
	}
	if ports > 0 {
		tree.BackingRegistry.Set(&backing[0])
	}

	for device := range registry.Pointees(devices) {
		if err := m.linkEntry(devices, device, device.InputPipelines.Get()); err != nil {
			return nil, err
		}
		if err := m.linkEntry(devices, device, device.OutputPipelines.Get()); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// objects builds the contexts of the generation's objects, before the
// devices', whose modules link them.
func (m *build) objects() error {
	count := m.tree.ObjectCount
	if count == 0 {
		return nil
	}
	objects, ok := allocArray[shm.Ptr[layout.ObjectEctx]](&m.cp.EctxMemoryContext, count, zeroed)
	if !ok {
		return fmt.Errorf("allocate object contexts: %w", errZoneMemory)
	}
	m.tree.Objects.Set(&objects[0])
	for idx, item := range registry.Items(&m.gen.ObjectRegistry.EntityRegistry.Registry) {
		object, err := m.object(registry.Container[layout.CpObject](item, 0))
		if err != nil {
			return err
		}
		objects[idx].Set(object)
	}
	return nil
}

// object builds an object's context, as object_ectx_create does.
//
// A failed context frees what it built.
func (m *build) object(object *layout.CpObject) (_ *layout.ObjectEctx, err error) {
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.ObjectEctx](context, 0)
	if built == nil {
		return nil, fmt.Errorf("allocate context of object %s: %w", shm.String(object.Name[:]), errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeObjectContext(m.cp, built)
		}
	}()
	built.CpObject.Set(object)
	built.WorkerIdx = m.worker
	descriptor := &registry.Slice(m.dp.DpObjects.Get(), m.dp.ObjectCount)[object.DpObjectIdx]
	if descriptor.PreparedSize > 0 {
		prepared, ok := allocArray[byte](context, descriptor.PreparedSize, zeroed)
		if !ok {
			return nil, fmt.Errorf("allocate prepared buffer of object %s: %w", shm.String(object.Name[:]), errZoneMemory)
		}
		built.ObjectPrepared.Set(&prepared[0])
	}
	tags := objectTags(shm.String(object.Type[:]), shm.String(object.Name[:]))
	if err := m.storageFor(&built.CounterStorage, tags, &object.CounterRegistry); err != nil {
		return nil, err
	}
	return built, nil
}

// device builds a device's context and its two entries, as
// device_ectx_create does.
//
// A failed context frees what it built.
func (m *build) device(device *layout.CpDevice, path pathNames, index uint64) (_ *layout.DeviceEctx, err error) {
	built := newNode[layout.DeviceEctx](&m.cp.EctxMemoryContext, 0)
	if built == nil {
		return nil, fmt.Errorf("allocate context of device %q: %w", path.Device, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeDeviceContext(m.cp, built)
		}
	}()
	// The ids land before any nested context, which reads them.
	built.DeviceId = uint16(index)
	built.CpDevice.Set(device)
	built.ConfigGenEctx.Set(m.tree)
	if err := m.storageFor(&built.CounterStorage, deviceTags(path.Device), &device.CounterRegistry); err != nil {
		return nil, err
	}

	descriptor := &registry.Slice(m.dp.DpDevices.Get(), m.dp.DeviceCount)[device.DpDeviceIdx]
	input, err := m.entry(built, path, descriptor.InputHandler, device.InputPipelines.Get())
	if err != nil {
		return nil, err
	}
	built.InputPipelines.Set(input)
	input.DeviceEctx.Set(built)
	input.Direction = directionInput
	output, err := m.entry(built, path, descriptor.OutputHandler, device.OutputPipelines.Get())
	if err != nil {
		return nil, err
	}
	built.OutputPipelines.Set(output)
	output.DeviceEctx.Set(built)
	output.Direction = directionOutput
	return built, nil
}

// entry builds a device entry's context and its pipelines, as
// device_entry_ectx_create does.
//
// A failed context frees what it built.
func (m *build) entry(
	device *layout.DeviceEctx,
	path pathNames,
	handler shm.Local,
	entry *layout.CpDeviceEntry,
) (_ *layout.DeviceEntryEctx, err error) {
	named := entry.Pipelines(int(entry.PipelineCount))
	sum := uint64(0)
	for idx := range named {
		if named[idx].Weight > maxWeightSum-sum {
			return nil, fmt.Errorf("device %q: pipeline weights sum past %d", path.Device, maxWeightSum)
		}
		sum += named[idx].Weight
	}
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.DeviceEntryEctx](context, shm.PtrSize*uintptr(sum))
	if built == nil {
		return nil, fmt.Errorf("allocate entry context of device %q: %w", path.Device, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeDeviceEntryContext(m.cp, built)
		}
	}()
	built.Handler = handler
	built.PipelineMapSize = sum
	count := entry.PipelineCount
	pipelines, ok := allocArray[shm.Ptr[layout.PipelineEctx]](context, count, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate pipelines of device %q: %w", path.Device, errZoneMemory)
	}
	built.PipelineCount = count
	if count == 0 {
		return built, nil
	}
	built.PipelinePtrs.Set(&pipelines[0])
	absolute, ok := allocArray[shm.Local](context, count, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate pipeline addresses of device %q: %w", path.Device, errZoneMemory)
	}
	built.Pipelines.Set(&absolute[0])

	pipelineMap := built.PipelineMap(int(sum))
	position := 0
	for idx := range named {
		path.Pipeline = shm.String(named[idx].Name[:])
		pipeline := m.lookupPipeline(path.Pipeline)
		if pipeline == nil {
			return nil, fmt.Errorf("device %q: %w: %s", path.Device, ErrMissingPipeline, path.Pipeline)
		}
		context, err := m.pipeline(device, path, pipeline)
		if err != nil {
			return nil, err
		}
		pipelines[idx].Set(context)
		for range named[idx].Weight {
			pipelineMap[position].Set(context)
			position++
		}
	}
	return built, nil
}

// pipeline builds a pipeline's context and its functions, as
// pipeline_ectx_create does.
//
// A failed context frees what it built.
func (m *build) pipeline(
	device *layout.DeviceEctx,
	path pathNames,
	pipeline *layout.CpPipeline,
) (_ *layout.PipelineEctx, err error) {
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.PipelineEctx](context, shm.PtrSize*uintptr(pipeline.Length))
	if built == nil {
		return nil, fmt.Errorf("allocate context of pipeline %q: %w", path.Pipeline, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreePipelineContext(m.cp, built)
		}
	}()
	built.CpPipeline.Set(pipeline)
	built.Length = pipeline.Length
	functions, ok := allocArray[shm.Ptr[layout.FunctionEctx]](context, pipeline.Length, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate functions of pipeline %q: %w", path.Pipeline, errZoneMemory)
	}
	if len(functions) > 0 {
		built.FunctionPtrs.Set(&functions[0])
	}
	tags := pipelineTags(path.Device, path.Pipeline)
	if err := m.storageFor(&built.CounterStorage, tags, &pipeline.CounterRegistry); err != nil {
		return nil, err
	}

	for idx, named := range pipeline.Functions(int(pipeline.Length)) {
		path.Function = shm.String(named.Name[:])
		function := m.lookupFunction(path.Function)
		if function == nil {
			return nil, fmt.Errorf("pipeline %q: %w: %s", path.Pipeline, ErrMissingFunction, path.Function)
		}
		context, err := m.function(device, path, function)
		if err != nil {
			return nil, err
		}
		functions[idx].Set(context)
	}
	return built, nil
}

// function builds a function's context and its chains, as
// function_ectx_create does.
//
// A failed context frees what it built.
func (m *build) function(
	device *layout.DeviceEctx,
	path pathNames,
	function *layout.CpFunction,
) (_ *layout.FunctionEctx, err error) {
	weighted := function.Chains(int(function.ChainCount))
	sum := uint64(0)
	for idx := range weighted {
		if weighted[idx].Weight > maxWeightSum-sum {
			return nil, fmt.Errorf("function %q: chain weights sum past %d", path.Function, maxWeightSum)
		}
		sum += weighted[idx].Weight
	}
	// C fails a function without chains, its empty chain array reading as
	// a failed allocation.
	if function.ChainCount == 0 {
		return nil, fmt.Errorf("function %q has no chains", path.Function)
	}
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.FunctionEctx](context, shm.PtrSize*uintptr(sum))
	if built == nil {
		return nil, fmt.Errorf("allocate context of function %q: %w", path.Function, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeFunctionContext(m.cp, built)
		}
	}()
	built.CpFunction.Set(function)
	built.ChainMapSize = sum
	built.ChainCount = function.ChainCount
	chains, ok := allocArray[shm.Ptr[layout.ChainEctx]](context, function.ChainCount, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate chains of function %q: %w", path.Function, errZoneMemory)
	}
	built.ChainPtrs.Set(&chains[0])
	absolute, ok := allocArray[shm.Local](context, function.ChainCount, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate chain addresses of function %q: %w", path.Function, errZoneMemory)
	}
	built.Chains.Set(&absolute[0])
	tags := functionTags(path.Device, path.Pipeline, path.Function)
	if err := m.storageFor(&built.CounterStorage, tags, &function.CounterRegistry); err != nil {
		return nil, err
	}

	chainMap := built.ChainMap(int(sum))
	position := 0
	for idx := range weighted {
		chain := weighted[idx].CpChain.Get()
		context, err := m.chain(device, path, chain)
		if err != nil {
			return nil, err
		}
		chains[idx].Set(context)
		for range weighted[idx].Weight {
			chainMap[position].Set(context)
			position++
		}
	}
	return built, nil
}

// chain builds a chain's context and its modules, as chain_ectx_create
// does.
//
// A failed context frees what it built.
func (m *build) chain(device *layout.DeviceEctx, path pathNames, chain *layout.CpChain) (_ *layout.ChainEctx, err error) {
	path.Chain = shm.String(chain.Name[:])
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.ChainEctx](context, shm.PtrSize*uintptr(chain.Length))
	if built == nil {
		return nil, fmt.Errorf("allocate context of chain %q: %w", path.Chain, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeChainContext(m.cp, built)
		}
	}()
	built.CpChain.Set(chain)
	built.Length = chain.Length
	modules, ok := allocArray[shm.Ptr[layout.ModuleEctx]](context, chain.Length, zeroed)
	if !ok {
		return nil, fmt.Errorf("allocate modules of chain %q: %w", path.Chain, errZoneMemory)
	}
	if len(modules) > 0 {
		built.ModulePtrs.Set(&modules[0])
	}
	tags := chainTags(path.Device, path.Pipeline, path.Function, path.Chain)
	if err := m.storageFor(&built.CounterStorage, tags, &chain.CounterRegistry); err != nil {
		return nil, err
	}

	for idx, named := range chain.Modules(int(chain.Length)) {
		moduleType, moduleName := shm.String(named.Type[:]), shm.String(named.Name[:])
		module := registry.LookupModule(&m.gen.ModuleRegistry, moduleType, moduleName)
		if module == nil {
			return nil, fmt.Errorf(
				"%w: module %s:%s in chain %q of function %q in pipeline %q",
				ErrMissingModule, moduleType, moduleName, path.Chain, path.Function, path.Pipeline,
			)
		}
		context, err := m.module(device, path, module, moduleType, moduleName)
		if err != nil {
			return nil, err
		}
		modules[idx].Set(context)
	}
	return built, nil
}

// module builds a module's context, its runtime storages and its object
// links, as module_ectx_create does.
//
// A failed context frees what it built.
func (m *build) module(
	device *layout.DeviceEctx,
	path pathNames,
	module *layout.CpModule,
	moduleType, moduleName string,
) (_ *layout.ModuleEctx, err error) {
	context := &m.cp.EctxMemoryContext
	built := newNode[layout.ModuleEctx](context, 0)
	if built == nil {
		return nil, fmt.Errorf("allocate context of module %s:%s: %w", moduleType, moduleName, errZoneMemory)
	}
	defer func() {
		if err != nil {
			teardown.FreeModuleContext(m.cp, built)
		}
	}()
	built.RegistryDeviceId = device.DeviceId
	built.CpModule.Set(module)
	built.ConfigGenEctx.Set(m.tree)
	built.PacketRecircLimit = m.dp.PacketRecircLimit
	descriptor := &registry.Slice(m.dp.DpModules.Get(), m.dp.ModuleCount)[module.DpModuleIdx]
	built.Handler = descriptor.Handler
	if descriptor.PreparedSize > 0 {
		prepared, ok := allocArray[byte](context, descriptor.PreparedSize, zeroed)
		if !ok {
			return nil, fmt.Errorf("allocate prepared buffer of module %s:%s: %w", moduleType, moduleName, errZoneMemory)
		}
		built.ModulePrepared.Set(&prepared[0])
	}

	modulePath := modulePathTags(path.Device, path.Pipeline, path.Function, path.Chain, moduleType, moduleName)
	if err := m.storageFor(&built.CounterStorage, moduleTags(modulePath), &module.CounterRegistry); err != nil {
		return nil, err
	}
	if err := m.runtimeStorages(built, modulePath, module); err != nil {
		return nil, fmt.Errorf("module %s:%s: %w", moduleType, moduleName, err)
	}
	if err := m.objectLinks(built, modulePath, module); err != nil {
		return nil, fmt.Errorf("module %s:%s: %w", moduleType, moduleName, err)
	}
	return built, nil
}

// runtimeStorages builds the storages of a module's tagged runtime
// counter registries.
func (m *build) runtimeStorages(built *layout.ModuleEctx, modulePath tagSet, module *layout.CpModule) error {
	registries := runtimeRegistries(module)
	if len(registries) == 0 {
		return nil
	}
	context := &m.cp.EctxMemoryContext
	count := uint64(len(registries))
	storages, ok := allocArray[shm.Ptr[layout.CounterStorage]](context, count, zeroed)
	if !ok {
		return fmt.Errorf("allocate runtime counter storages: %w", errZoneMemory)
	}
	built.RuntimeCounterStorageCount = count
	built.RuntimeCounterStorages.Set(&storages[0])
	// The dataplane fills the addresses in.
	absolute, ok := allocArray[shm.Local](context, count, stale)
	if !ok {
		return fmt.Errorf("allocate runtime counter storage addresses: %w", errZoneMemory)
	}
	built.AbsRuntimeCounterStorages.Set(&absolute[0])
	for idx := range registries {
		entry := registries[idx].Get()
		tags := runtimeTags(modulePath, shm.String(entry.Tag[:]))
		if err := m.storageFor(&storages[idx], tags, &entry.Registry); err != nil {
			// A runtime storage that fails to register goes before the
			// module's context, as module_ectx_create drops it.
			teardown.FreeCounterStorage(storages[idx].Get())
			storages[idx].Set(nil)
			return err
		}
	}
	return nil
}

// objectLinks builds the contexts of the links of a module to the
// objects it names, each with a storage of the object's link counters.
func (m *build) objectLinks(built *layout.ModuleEctx, modulePath tagSet, module *layout.CpModule) error {
	count := module.ObjectCount
	if count == 0 {
		return nil
	}
	links, ok := allocArray[layout.ModuleObjectLinkEctx](&m.cp.EctxMemoryContext, count, zeroed)
	if !ok {
		return fmt.Errorf("allocate object links: %w", errZoneMemory)
	}
	built.ObjectLinkCount = count
	built.ObjectLinks.Set(&links[0])
	objects := registry.Slice(m.tree.Objects.Get(), m.tree.ObjectCount)
	for idx, linked := range registry.Slice(module.Objects.Get(), count) {
		objectType, objectName := shm.String(linked.Type[:]), shm.String(linked.Name[:])
		index, ok := registry.LookupObjectIndex(&m.gen.ObjectRegistry, objectType, objectName)
		if !ok {
			return fmt.Errorf("%w: %s:%s", ErrMissingObject, objectType, objectName)
		}
		object := registry.Container[layout.CpObject](registry.Item(&m.gen.ObjectRegistry.EntityRegistry.Registry, index), 0)
		objectContext := objects[index].Get()
		if objectContext == nil {
			return fmt.Errorf("object %s:%s has no context", objectType, objectName)
		}
		tags := objectLinkTags(modulePath, shm.String(object.Type[:]), shm.String(object.Name[:]))
		links[idx].ObjectEctx.Set(objectContext)
		if err := m.storageFor(&links[idx].CounterStorage, tags, &object.LinkCounterRegistry); err != nil {
			return err
		}
	}
	return nil
}

// linkEntry resolves the device targets of every module context below a
// device entry, as link_config_gen_ectx does.
//
// It runs once every device context exists.
func (m *build) linkEntry(devices []shm.Ptr[layout.DeviceEctx], device *layout.DeviceEctx, entry *layout.DeviceEntryEctx) error {
	for pipeline := range pointees(entry.PipelinePtrs.Get(), entry.PipelineCount) {
		for function := range pointees(pipeline.FunctionPtrs.Get(), pipeline.Length) {
			if err := m.linkFunction(devices, device, function); err != nil {
				return err
			}
		}
	}
	return nil
}

// linkFunction resolves the device targets of every module context below
// a function context.
//
// It walks apart from the entry's pipelines and functions, so no walk
// nests more than two iterators and their loop bodies stay off the heap.
func (m *build) linkFunction(devices []shm.Ptr[layout.DeviceEctx], device *layout.DeviceEctx, function *layout.FunctionEctx) error {
	for chain := range pointees(function.ChainPtrs.Get(), function.ChainCount) {
		for module := range pointees(chain.ModulePtrs.Get(), chain.Length) {
			if err := m.linkModule(devices, device, module); err != nil {
				return err
			}
		}
	}
	return nil
}

// pointees yields what a pointer array of that length points to,
// skipping nil pointers.
func pointees[T any](first *shm.Ptr[T], count uint64) iter.Seq[*T] {
	return registry.Pointees(registry.Slice(first, count))
}

// linkModule resolves a module context's targets: the entries of each
// device the module names, and its own device's place among them.
func (m *build) linkModule(devices []shm.Ptr[layout.DeviceEctx], owner *layout.DeviceEctx, built *layout.ModuleEctx) error {
	module := built.CpModule.Get()
	count := module.DeviceCount
	targets, ok := allocArray[layout.ModuleDeviceTarget](&m.cp.EctxMemoryContext, count, stale)
	if !ok {
		return fmt.Errorf(
			"allocate device targets of module %s:%s: %w",
			shm.String(module.Type[:]), shm.String(module.Name[:]), errZoneMemory,
		)
	}
	// C writes only these fields; the rest is the dataplane's.
	for idx := range targets {
		targets[idx].DeviceId = noPort
		targets[idx].InputEntry.Set(nil)
		targets[idx].OutputEntry.Set(nil)
	}
	if count > 0 {
		built.DeviceTargets.Set(&targets[0])
	}
	built.DeviceTargetCount = count
	ownerName := shm.Bytes(owner.CpDevice.Get().Name[:])
	for idx, named := range registry.Slice(module.Devices.Get(), count) {
		name := shm.Bytes(named.Name[:])
		if bytes.Equal(name, ownerName) {
			built.ModuleDeviceId = uint32(idx)
		}
		for deviceIdx := range devices {
			target := devices[deviceIdx].Get()
			if target == nil || !bytes.Equal(shm.Bytes(target.CpDevice.Get().Name[:]), name) {
				continue
			}
			targets[idx].DeviceId = uint16(deviceIdx)
			targets[idx].InputEntry.Set(target.InputPipelines.Get())
			targets[idx].OutputEntry.Set(target.OutputPipelines.Get())
		}
	}
	return nil
}
