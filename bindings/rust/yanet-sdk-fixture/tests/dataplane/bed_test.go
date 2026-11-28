//go:build sdk_rust_fixtures

package dataplane_test

import (
	"bytes"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/rust/yanet-sdk-fixture/tests/dataplane"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

const (
	// workerCount is the number of workers of a bed.
	workerCount = 4
	// agentMemory is what the agent of a bed may take.
	agentMemory = 4 * datasize.MB
)

// bed is a harness with the fixture plugins loaded, the native view of its
// instance, one agent the native control plane attached to it, and the free
// size of that agent before it built anything.
type bed struct {
	t        *testing.T
	harness  *dataplaneut.Harness
	instance *zone.Instance
	agent    *agent.Agent
	baseline uint64
}

// newBed starts a harness of four workers and the device port0, which knows
// the modules and the state object type of the fixture, and attaches an agent
// to it.
//
// Each adjust may change the harness configuration before it starts. The
// harness is freed when the test ends, after everything built over the agent.
func newBed(t *testing.T, adjust ...func(*dataplaneut.Config)) *bed {
	t.Helper()

	config := dataplaneut.Config{
		CPMemory:      uint64(16 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   workerCount,
		Devices:       []string{"port0"},
		Modules:       []string{"fixture", "tally", "fib", "assign", "hosts", "stash"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{sdkfixture.StateType},
		PluginDir:     dataplane.PluginDir,
		// The fixture forgets packets on purpose and keeps none beyond a round.
		ReclaimLostPackets: true,
	}
	for _, apply := range adjust {
		apply(&config)
	}
	harness, err := dataplaneut.NewHarness(config)
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	instance := harness.NativeInstance(t)
	owner := attach(t, instance, "fixture", agentMemory)
	return &bed{t: t, harness: harness, instance: instance, agent: owner, baseline: freeSize(owner)}
}

// attach attaches an agent of that name and memory to the instance; the
// agent lives as long as the harness.
func attach(t *testing.T, instance *zone.Instance, name string, memory datasize.ByteSize) *agent.Agent {
	t.Helper()

	owner, err := agent.Attach(instance, name, uint64(memory))
	require.NoError(t, err)
	return owner
}

// freeSize returns the free bytes of the agent's memory.
func freeSize(owner *agent.Agent) uint64 {
	return uint64(shmalloc.FreeSize(&owner.Agent.BlockAllocator))
}

// loadedCount returns how many references to the agent's entities live
// generations hold, plus the destruction holds of frees in flight.
func loadedCount(owner *agent.Agent) uint64 {
	return atomic.LoadUint64(&owner.Agent.LoadedEntityCount)
}

// used returns the bytes of the bed's agent memory taken since the bed started.
func (m *bed) used() uint64 {
	return m.baseline - freeSize(m.agent)
}

// deliver returns the option that hands the harness's workers what an update
// publishes.
func (m *bed) deliver() cpconfig.UpdateOption {
	return cpconfig.WithDelivery(m.harness.NativeDelivery(m.t))
}

// handle is a configuration a native builder made and the free of its kind.
//
// A free that succeeded makes later ones do nothing, so that a test and its
// cleanup may both free the configuration.
type handle[T any] struct {
	entity *T
	free   func(*T) error
	freed  bool
}

// Free frees the configuration, or reports that a live generation holds it.
func (m *handle[T]) Free() error {
	if m.freed {
		return nil
	}
	if err := m.free(m.entity); err != nil {
		return err
	}
	m.freed = true
	return nil
}

// moduleConfig is a module configuration a native builder made.
type moduleConfig = handle[layout.CpModule]

// moduleHandle returns the handle of a configuration the build returned, or
// its refusal.
func moduleHandle(module *layout.CpModule, err error, free func(*layout.CpModule) error) (*moduleConfig, error) {
	if err != nil {
		return nil, err
	}
	return &moduleConfig{entity: module, free: free}, nil
}

// kindOf tags a refusal of the native control plane, the fixture builders'
// own included, with the kind a service reports for it.
func kindOf(err error) error {
	return crud.NativeError(err, sdkfixture.ErrInvalidArgument, classify.ErrInvalidRule)
}

// fixtureOptions describes a fixture configuration.
type fixtureOptions struct {
	name    string
	counter string
	device  string
	mode    uint64
	arg     uint64
	ranges  []sdkfixture.Range4
}

// fixtureOf returns the options of a fixture configuration named config that
// behaves as mode says, registers the counter hits and the device port0, and
// has no ranges.
func fixtureOf(mode uint64) fixtureOptions {
	return fixtureOptions{name: "config", counter: "hits", device: "port0", mode: mode}
}

// buildFixture builds the configuration the options describe over the agent,
// without registering it for cleanup.
func buildFixture(owner *agent.Agent, options fixtureOptions) (*moduleConfig, error) {
	module, err := sdkfixture.NewFixtureConfig(owner, options.name, sdkfixture.Fixture{
		Mode:    options.mode,
		Arg:     options.arg,
		Counter: options.counter,
		Device:  options.device,
		Routes:  options.ranges,
	})
	return moduleHandle(module, err, sdkfixture.FreeFixtureConfig)
}

// buildFixture builds the configuration the options describe over the bed's
// agent, without registering it for cleanup.
func (m *bed) buildFixture(options fixtureOptions) (*moduleConfig, error) {
	return buildFixture(m.agent, options)
}

// freeAtEnd frees the configuration when the test ends, before the harness
// goes.
func (m *bed) freeAtEnd(config *moduleConfig) {
	m.t.Cleanup(func() { _ = config.Free() })
}

// fixture builds the fixture configuration the options describe, which must
// succeed.
func (m *bed) fixture(options fixtureOptions) *moduleConfig {
	m.t.Helper()

	config, err := m.buildFixture(options)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return config
}

// instance is a module configuration of the bed and, once published in a
// route, where its counters are.
type instance struct {
	moduleType string
	name       string
	config     *moduleConfig
	path       zone.ModulePosition
}

// fixtureInstance builds the fixture configuration the options describe as an
// instance of the module type fixture.
func (m *bed) fixtureInstance(options fixtureOptions) *instance {
	m.t.Helper()

	return &instance{moduleType: "fixture", name: options.name, config: m.fixture(options)}
}

// forwardInstance builds an instance that redirects every packet to the output
// of port0, which puts it on the transmit list.
func (m *bed) forwardInstance() *instance {
	m.t.Helper()

	options := fixtureOf(sdkfixture.FixtureModeForward)
	options.name = "forward"
	return m.fixtureInstance(options)
}

// tallyInstance builds a tally configuration named name over word as an
// instance of the module type tally.
func (m *bed) tallyInstance(name string, word uint64) *instance {
	m.t.Helper()

	return m.tallyInstanceOf(name, word, true)
}

// tallyInstanceOf is tallyInstance with or without its lanes.
func (m *bed) tallyInstanceOf(name string, word uint64, withLanes bool) *instance {
	m.t.Helper()

	module, err := sdkfixture.NewTallyConfig(m.agent, name, word, withLanes)
	config, err := moduleHandle(module, err, sdkfixture.FreeTallyConfig)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return &instance{moduleType: "tally", name: name, config: config}
}

// tapInstance builds an instance named name that passes every packet, to count
// what reaches it.
func (m *bed) tapInstance(name string) *instance {
	m.t.Helper()

	options := fixtureOf(sdkfixture.FixtureModePass)
	options.name = name
	return m.fixtureInstance(options)
}

// route is the module instances a device runs a packet through on its way in
// and on its way out.
type route struct {
	device string
	input  []*instance
	output []*instance
}

// publishModules publishes the instances' configurations as one generation,
// with no function to run them.
func (m *bed) publishModules(instances ...*instance) {
	m.t.Helper()

	require.NoError(m.t, m.updateModules(instances...))
}

// updateModules publishes the instances' configurations as one generation
// and returns the refusal of the update.
func (m *bed) updateModules(instances ...*instance) error {
	modules := make([]*layout.CpModule, 0, len(instances))
	for _, inst := range instances {
		modules = append(modules, inst.config.entity)
	}
	return cpconfig.UpdateModules(m.instance, modules, m.deliver())
}

// publish publishes the routes' modules, one function and pipeline for each
// direction of each device, and the devices, and records where each instance
// runs.
//
// The pipelines of a device are named after it and the direction, port0-in and
// port0-out; so are its function and chain.
func (m *bed) publish(routes ...route) {
	m.t.Helper()

	var instances []*instance
	for _, r := range routes {
		instances = append(instances, r.input...)
		instances = append(instances, r.output...)
	}
	m.publishModules(instances...)

	var functions []cpconfig.FunctionConfig
	var pipelines []cpconfig.PipelineConfig
	devices := make([]*layout.CpDevice, 0, len(routes))
	for _, r := range routes {
		directions := []struct {
			suffix    string
			instances []*instance
		}{{"in", r.input}, {"out", r.output}}
		for _, direction := range directions {
			name := r.device + "-" + direction.suffix
			var names []string
			if len(direction.instances) > 0 {
				names = []string{name}
				modules := make([]cpconfig.ChainModuleConfig, 0, len(direction.instances))
				for _, inst := range direction.instances {
					modules = append(modules, cpconfig.ChainModuleConfig{Type: inst.moduleType, Name: inst.name})
					inst.path = zone.ModulePosition{
						Device:     r.device,
						Pipeline:   name,
						Function:   name,
						Chain:      name,
						ModuleType: inst.moduleType,
						ModuleName: inst.name,
					}
				}
				functions = append(functions, cpconfig.FunctionConfig{
					Name: name,
					Chains: []cpconfig.FunctionChainConfig{{
						Weight: 1,
						Chain:  cpconfig.ChainConfig{Name: name, Modules: modules},
					}},
				})
			}
			pipelines = append(pipelines, cpconfig.PipelineConfig{Name: name, Functions: names})
		}
		device, err := cpconfig.NewPlainDevice(m.agent, cpconfig.DeviceConfig{
			Name:   r.device,
			Input:  []cpconfig.DevicePipelineConfig{{Name: r.device + "-in", Weight: 1}},
			Output: []cpconfig.DevicePipelineConfig{{Name: r.device + "-out", Weight: 1}},
		})
		require.NoError(m.t, err)
		m.t.Cleanup(func() { _ = cpconfig.FreePlainDevice(device) })
		devices = append(devices, device)
	}
	require.NoError(m.t, cpconfig.UpdateFunctions(m.instance, functions, m.deliver()))
	require.NoError(m.t, cpconfig.UpdatePipelines(m.instance, pipelines, m.deliver()))
	require.NoError(m.t, cpconfig.UpdateDevices(m.instance, devices, m.deliver()))
}

// serve publishes the instances as the input pipeline of port0, in order, and
// as its output pipeline.
func (m *bed) serve(input, output []*instance) {
	m.t.Helper()

	m.publish(route{device: "port0", input: input, output: output})
}

// serveBeforeForward publishes the instance and a forwarding instance after
// it as the input pipeline of port0, so that a packet the instance passes ends
// on the transmit list.
func (m *bed) serveBeforeForward(inst *instance) {
	m.t.Helper()

	m.serve([]*instance{inst, m.forwardInstance()}, nil)
}

// run runs the frames through one round on the worker, as received on
// port0.
func (m *bed) run(worker int, frames ...[]byte) *dataplaneut.RawResult {
	m.t.Helper()

	packets := make([][][]byte, 0, len(frames))
	for _, frame := range frames {
		packets = append(packets, [][]byte{frame})
	}
	result, err := m.harness.HandleSegmentedPacketsOnDevice(worker, 0, packets...)
	require.NoError(m.t, err)
	return result
}

// runFixture publishes a fixture of that mode before a forwarding instance
// and runs the frames through them on worker 0.
func (m *bed) runFixture(mode uint64, frames ...[]byte) *dataplaneut.RawResult {
	m.t.Helper()

	m.serveBeforeForward(m.fixtureInstance(fixtureOf(mode)))
	return m.run(0, frames...)
}

// counters returns the counters of the one storage the generation the bed
// publishes registers under the tags.
func (m *bed) counters(tags ...zone.CounterTag) []zone.Counter {
	m.t.Helper()

	generation, err := m.instance.Pin()
	require.NoError(m.t, err)
	defer generation.Release()
	groups, err := generation.Counters(tags)
	require.NoError(m.t, err)
	require.Len(m.t, groups, 1, "storages of %v", tags)
	return groups[0].Counters
}

// valuesOf returns, per worker, the values of the counter of that name among
// the counters.
func valuesOf(t *testing.T, counters []zone.Counter, name string) [][]uint64 {
	t.Helper()

	idx := slices.IndexFunc(counters, func(counter zone.Counter) bool { return counter.Name == name })
	require.GreaterOrEqual(t, idx, 0, "counter %s", name)
	return counters[idx].Values
}

// pathTags returns the tags of the storages of the instance's position and
// of the kind, with the extra tags.
func pathTags(inst *instance, kind string, extra ...zone.CounterTag) []zone.CounterTag {
	path := inst.path
	return append([]zone.CounterTag{
		{Key: "device", Value: path.Device},
		{Key: "pipeline", Value: path.Pipeline},
		{Key: "function", Value: path.Function},
		{Key: "chain", Value: path.Chain},
		{Key: "module_type", Value: path.ModuleType},
		{Key: "module_name", Value: path.ModuleName},
		{Key: "kind", Value: kind},
	}, extra...)
}

// counter returns, per worker, the values of the counter of that name the
// dataplane keeps for the instance: rx, tx and drop of packets and bytes that
// every module has, or a counter its build registered.
func (m *bed) counter(inst *instance, name string) [][]uint64 {
	m.t.Helper()

	return valuesOf(m.t, m.counters(pathTags(inst, "module")...), name)
}

// moduleCounterNames returns the names of the instance's own counters among
// the given ones, in the order the dataplane lists them.
func (m *bed) moduleCounterNames(inst *instance, names ...string) []string {
	m.t.Helper()

	var found []string
	for _, counter := range m.counters(pathTags(inst, "module")...) {
		if slices.Contains(names, counter.Name) {
			found = append(found, counter.Name)
		}
	}
	return found
}

// taggedCounter returns, per worker, the values of the counter of that name
// in the instance's tagged registry of the tag, which the dataplane keeps
// among the instance's runtime counters under the tag.
func (m *bed) taggedCounter(inst *instance, tag, name string) [][]uint64 {
	m.t.Helper()

	counters := m.counters(pathTags(inst, "runtime", zone.CounterTag{Key: "config", Value: tag})...)
	return valuesOf(m.t, counters, name)
}

// objectCounter returns, per worker, the values of the counter of that name
// in the own registry of the object of that type and name, which the dataplane
// keeps apart from the counters of the modules linking the object.
func (m *bed) objectCounter(objectType, objectName, name string) [][]uint64 {
	m.t.Helper()

	counters := m.counters(
		zone.CounterTag{Key: "object_type", Value: objectType},
		zone.CounterTag{Key: "object_name", Value: objectName},
		zone.CounterTag{Key: "kind", Value: "object"},
	)
	return valuesOf(m.t, counters, name)
}

// counterGrowth returns, per worker, how much each value of a counter grew
// from before to after, both rows of every worker of a bed.
func counterGrowth(t *testing.T, before, after [][]uint64) [][]uint64 {
	t.Helper()

	require.Len(t, before, workerCount)
	require.Len(t, after, workerCount)
	growth := make([][]uint64, 0, workerCount)
	for worker := range workerCount {
		require.Len(t, after[worker], len(before[worker]), "worker %d", worker)
		row := make([]uint64, 0, len(before[worker]))
		for idx, value := range before[worker] {
			row = append(row, after[worker][idx]-value)
		}
		growth = append(growth, row)
	}
	return growth
}

// fate is where a frame of a round ended.
type fate string

const (
	fateOutput fate = "output"
	fateDrop   fate = "drop"
	// fateLost: on no list, because a module took the frame and kept it.
	fateLost fate = "lost"
)

// fatesOf returns the fate of each frame, found by its bytes in the lists of
// the result, so the frames must differ and no module may change them.
func fatesOf(result *dataplaneut.RawResult, frames [][]byte) []fate {
	contains := func(list [][]byte, frame []byte) bool {
		return slices.ContainsFunc(list, func(other []byte) bool { return bytes.Equal(other, frame) })
	}
	fates := make([]fate, 0, len(frames))
	for _, frame := range frames {
		switch {
		case contains(result.Output, frame):
			fates = append(fates, fateOutput)
		case contains(result.Drop, frame):
			fates = append(fates, fateDrop)
		default:
			fates = append(fates, fateLost)
		}
	}
	return fates
}

// arpFrames returns count frames of 60 bytes that the parser does not take for
// IP, frame n filled with n except for its ethertype.
func arpFrames(count int) [][]byte {
	frames := make([][]byte, 0, count)
	for idx := range count {
		frame := bytes.Repeat([]byte{byte(idx)}, 60)
		frame[12], frame[13] = 0x08, 0x06
		frames = append(frames, frame)
	}
	return frames
}

// ipv4UDP returns an IPv4/UDP frame the parser accepts, with its network
// header at 14 and its transport header at 34.
func ipv4UDP() []byte {
	frame := make([]byte, 14+20+8)
	frame[12], frame[13] = 0x08, 0x00
	frame[14] = 0x45
	frame[14+3] = 28
	frame[14+8] = 64
	frame[14+9] = 17
	copy(frame[14+12:], []byte{192, 0, 2, 1})
	copy(frame[14+16:], []byte{192, 0, 2, 2})
	frame[14+25] = 8
	return frame
}

// packetsAndBytes is the shape of a counter of packets and bytes on one
// worker.
func packetsAndBytes(packets, size int) []uint64 {
	return []uint64{uint64(packets), uint64(size)}
}

// withWorkers returns an adjust that starts a bed's harness with that many
// workers.
func withWorkers(workers uint64) func(*dataplaneut.Config) {
	return func(config *dataplaneut.Config) { config.WorkerCount = workers }
}

// withoutModules starts a bed's harness with no module loaded.
func withoutModules(config *dataplaneut.Config) {
	config.Modules = nil
}

// requireUntagged asserts that the error carries none of the kinds a service
// reports.
func requireUntagged(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	tagged := kindOf(err)
	for _, kind := range []error{
		cerrors.NotFound,
		cerrors.FailedPrecondition,
		cerrors.Busy,
		cerrors.InvalidArgument,
		cerrors.ResourceExhausted,
	} {
		require.NotErrorIs(t, tagged, kind)
	}
}

// allocations returns how many blocks the allocator has handed out.
func allocations(allocator *layout.BlockAllocator) uint64 {
	var count uint64
	for idx := range allocator.Pools {
		count += allocator.Pools[idx].Allocate
	}
	return count
}

// keptBuild runs the build with the agent keeping no free block but one of
// each of the request sizes, and returns how many blocks the build took and
// its error.
//
// A build that succeeds is freed. Either way the agent must get back all the
// free memory it had.
func keptBuild(t *testing.T, owner *agent.Agent, build func(*agent.Agent) (func() error, error), kept []uintptr) (uint64, error) {
	t.Helper()

	allocator := &owner.Agent.BlockAllocator
	free := freeSize(owner)
	restore := nativetest.LeaveOnly(t, allocator, kept...)
	before := allocations(allocator)
	release, err := build(owner)
	taken := allocations(allocator) - before
	if err == nil {
		require.NoError(t, release())
	}
	restore()

	require.Equal(t, free, freeSize(owner), "a build given %v", kept)
	return taken, err
}

// sweepAllocations refuses the build at each of its allocations in turn,
// from the first to the last, and requires each refusal to wrap the
// allocator's out-of-memory error and to give the agent back every byte.
//
// Each round keeps one more block, the smallest that lets the build take
// one more allocation. An allocation a block the build freed before serves
// is not refused on its own: the agent keeps that block for the first one.
// The sweep ends with the build that fits.
func sweepAllocations(t *testing.T, owner *agent.Agent, build func(*agent.Agent) (func() error, error)) {
	t.Helper()

	var kept []uintptr
	for {
		taken, err := keptBuild(t, owner, build, kept)
		if err == nil {
			require.NotEmpty(t, kept, "the sweep must refuse an allocation")
			return
		}
		require.ErrorIs(t, err, shmalloc.ErrNoMemory, "given %d blocks", len(kept))
		next := uintptr(0)
		for pool := range shmalloc.PoolCount {
			size := nativetest.BlockRequest(uintptr(1) << (shmalloc.MinBits + pool))
			if size == 0 {
				continue
			}
			if more, _ := keptBuild(t, owner, build, append(kept, size)); more > taken {
				next = size
				break
			}
		}
		require.NotZero(t, next, "no block serves allocation %d: %v", taken, err)
		kept = append(kept, next)
	}
}
