//go:build sdk_rust_fixtures

package cfixture_test

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

const (
	workerCount = 4
	agentMemory = 4 * datasize.MB
)

// bed is a harness with the fixture plugins loaded and one agent attached,
// and the free size of that agent before it built anything.
type bed struct {
	t        testing.TB
	harness  *dataplaneut.Harness
	agent    *ffi.Agent
	baseline uint64
}

// newBed attaches an agent to four workers with the fixture modules and port0.
//
// Optional adjustments apply before startup. Cleanup frees configurations
// before the harness and its attached agent.
func newBed(t testing.TB, adjust ...func(*dataplaneut.Config)) *bed {
	t.Helper()

	config := dataplaneut.Config{
		CPMemory:      uint64(16 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   workerCount,
		Devices:       []string{"port0"},
		Modules:       []string{"fixture", "tally", "fib"},
		DevicesToLoad: []string{"plain"},
		PluginDir:     cfixture.PluginDir,
		// The fixture forgets packets on purpose and keeps none beyond a round.
		ReclaimLostPackets: true,
	}
	for _, apply := range adjust {
		apply(&config)
	}
	harness, err := dataplaneut.NewHarness(config)
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent := attach(t, harness, "fixture", agentMemory)
	return &bed{t: t, harness: harness, agent: agent, baseline: agent.BlockAllocatorFreeSize()}
}

// attach attaches an agent of that name and memory to the harness and destroys
// it when the test ends, after everything registered since.
func attach(t testing.TB, harness *dataplaneut.Harness, name string, memory datasize.ByteSize) *ffi.Agent {
	t.Helper()

	agent, err := harness.SharedMemory().AgentAttach(name, 0, memory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	return agent
}

// withAgent attaches an independent allocation budget to the shared harness.
func (m *bed) withAgent(name string, memory datasize.ByteSize) *bed {
	m.t.Helper()

	agent := attach(m.t, m.harness, name, memory)
	return &bed{t: m.t, harness: m.harness, agent: agent, baseline: agent.BlockAllocatorFreeSize()}
}

// on routes failures and new cleanup registrations to a subtest or benchmark.
func (m *bed) on(t testing.TB) *bed {
	return &bed{t: t, harness: m.harness, agent: m.agent, baseline: m.baseline}
}

// used returns the bytes of the bed's agent memory taken since the bed started.
func (m *bed) used() uint64 {
	return m.baseline - m.agent.BlockAllocatorFreeSize()
}

type fixtureOptions struct {
	name    string
	counter string
	device  string
	mode    cfixture.Mode
	arg     uint64
	fault   cfixture.Fault
	ranges  []cfixture.Range4
}

func fixtureOf(mode cfixture.Mode) fixtureOptions {
	return fixtureOptions{name: "config", counter: "hits", device: "port0", mode: mode}
}

// buildFixture builds the configuration the options describe over the agent,
// without registering it for cleanup.
func (m *bed) buildFixture(options fixtureOptions) (*cfixture.ModuleConfig, error) {
	spec := cfixture.NewFixtureSpec(options.mode, options.arg, options.fault)
	return cfixture.NewFixtureConfig(
		m.agent,
		options.name,
		options.counter,
		options.device,
		&spec,
		options.ranges,
	)
}

// freeAtEnd frees the configuration when the test ends, before the agent goes.
func (m *bed) freeAtEnd(config *cfixture.ModuleConfig) {
	m.t.Cleanup(func() { _ = config.Free() })
}

func (m *bed) fixture(options fixtureOptions) *cfixture.ModuleConfig {
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
	config     interface {
		AsFFIModule() ffi.ModuleConfig
		Free() error
	}
	path dataplaneut.CounterPath
}

func (m *bed) fixtureInstance(options fixtureOptions) *instance {
	m.t.Helper()

	return &instance{moduleType: "fixture", name: options.name, config: m.fixture(options)}
}

// forwardInstance builds an instance that redirects every packet to the output
// of port0, which puts it on the transmit list.
func (m *bed) forwardInstance() *instance {
	m.t.Helper()

	options := fixtureOf(cfixture.ModeForward)
	options.name = "forward"
	return m.fixtureInstance(options)
}

func (m *bed) tallyInstance(name string, word uint64) *instance {
	m.t.Helper()

	config, err := cfixture.NewTallyConfig(m.agent, name, word)
	require.NoError(m.t, err)
	m.freeAtEnd(config)
	return &instance{moduleType: "tally", name: name, config: config}
}

// tapInstance builds an instance named name that passes every packet, to count
// what reaches it.
func (m *bed) tapInstance(name string) *instance {
	m.t.Helper()

	options := fixtureOf(cfixture.ModePass)
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

	modules := make([]ffi.ModuleConfig, 0, len(instances))
	for _, inst := range instances {
		modules = append(modules, inst.config.AsFFIModule())
	}
	require.NoError(m.t, m.agent.UpdateModules(modules))
}

// publish records each instance's counter path while installing device
// pipelines.
//
// Each device direction receives a matching function, chain and pipeline name,
// such as port0-in or port0-out.
func (m *bed) publish(routes ...route) {
	m.t.Helper()

	var instances []*instance
	for _, r := range routes {
		instances = append(instances, r.input...)
		instances = append(instances, r.output...)
	}
	m.publishModules(instances...)

	devices := make([]ffi.DeviceConfig, 0, len(routes))
	for _, r := range routes {
		directions := []struct {
			suffix    string
			instances []*instance
		}{{"in", r.input}, {"out", r.output}}
		for _, direction := range directions {
			name := r.device + "-" + direction.suffix
			var functions []string
			if len(direction.instances) > 0 {
				functions = []string{name}
				modules := make([]ffi.ChainModuleConfig, 0, len(direction.instances))
				for _, inst := range direction.instances {
					modules = append(modules, ffi.ChainModuleConfig{Type: inst.moduleType, Name: inst.name})
					inst.path = dataplaneut.CounterPath{
						Device:     r.device,
						Pipeline:   name,
						Function:   name,
						Chain:      name,
						ModuleType: inst.moduleType,
						ModuleName: inst.name,
					}
				}
				require.NoError(m.t, m.agent.UpdateFunction(ffi.FunctionConfig{
					Name: name,
					Chains: []ffi.FunctionChainConfig{{
						Weight: 1,
						Chain:  ffi.ChainConfig{Name: name, Modules: modules},
					}},
				}))
			}
			require.NoError(m.t, m.agent.UpdatePipeline(ffi.PipelineConfig{Name: name, Functions: functions}))
		}
		devices = append(devices, ffi.DeviceConfig{
			Name:   r.device,
			Input:  []ffi.DevicePipelineConfig{{Name: r.device + "-in", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: r.device + "-out", Weight: 1}},
		})
	}
	handles, err := cplain.UpdateDevices(m.agent, devices)
	require.NoError(m.t, err)
	m.t.Cleanup(func() {
		for _, handle := range handles {
			_ = handle.Free()
		}
	})
}

func (m *bed) serve(input, output []*instance) {
	m.t.Helper()

	m.publish(route{device: "port0", input: input, output: output})
}

// serveBeforeForward gives passed packets a subsequent route to the transmit
// list.
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

func (m *bed) runFixture(mode cfixture.Mode, frames ...[]byte) *dataplaneut.RawResult {
	m.t.Helper()

	m.serveBeforeForward(m.fixtureInstance(fixtureOf(mode)))
	return m.run(0, frames...)
}

// counter returns per-worker values for a named module counter.
//
// Both registered counters and the standard received, transmitted and dropped
// packet- and-byte counters use the published instance's path.
func (m *bed) counter(inst *instance, name string) [][]uint64 {
	m.t.Helper()

	path := inst.path
	counters := m.harness.SharedMemory().DPConfig(0).ModuleCounters(
		path.Device, path.Pipeline, path.Function, path.Chain, path.ModuleType, path.ModuleName,
		[]string{name},
	)
	require.Len(m.t, counters, 1, "counter %s of %s", name, inst.name)
	return counters[0].Values
}

type fate string

const (
	fateOutput fate = "output"
	fateDrop   fate = "drop"
	// fateLost identifies a frame absent from both round-result lists.
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

// arpFrames returns distinct 60-byte non-IP frames.
//
// Each frame repeats its ordinal byte around the ARP ethertype, allowing result
// matching by content.
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

func packetsAndBytes(packets, size int) []uint64 {
	return []uint64{uint64(packets), uint64(size)}
}

func withoutModules(config *dataplaneut.Config) {
	config.Modules = nil
}

// requireUntagged asserts that the error carries none of the kinds a C error
// chain reports.
func requireUntagged(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	for _, kind := range []error{
		ffi.ErrNotFound,
		ffi.ErrFailedPrecondition,
		ffi.ErrBusy,
		ffi.ErrInvalidArgument,
		ffi.ErrResourceExhausted,
	} {
		require.NotErrorIs(t, err, kind, fmt.Sprint(err))
	}
}

// buildable is a configuration a build benchmark frees.
type buildable interface {
	Free() error
}

// benchmarkBuild times construction, with release and memory use reported apart.
//
// Inputs are already prepared. The allocation peak is sampled every 10 ms;
// the process resident peak includes shared memory and resets per iteration.
// Optional inspection sees the final configuration before release. Every
// uninspected release must restore the allocator's original free capacity.
func (m *bed) benchmarkBuild(b *testing.B, build func() (buildable, error), inspect func(buildable)) {
	b.Helper()

	var used, peak, resident uint64
	var freeing time.Duration
	b.StopTimer()
	b.ResetTimer()
	for idx := range b.N {
		require.NoError(b, os.WriteFile("/proc/self/clear_refs", []byte("5"), 0))
		before := m.agent.BlockAllocatorFreeSize()
		lowest := m.sampleFreeSize()
		b.StartTimer()
		config, err := build()
		b.StopTimer()
		low := lowest()
		require.NoError(b, err)
		used += before - m.agent.BlockAllocatorFreeSize()
		peak += before - low
		resident += residentPeak(b)
		inspected := inspect != nil && idx == b.N-1
		if inspected {
			inspect(config)
		}
		start := time.Now()
		require.NoError(b, config.Free())
		freeing += time.Since(start)
		if !inspected {
			require.Equal(b, before, m.agent.BlockAllocatorFreeSize())
		}
	}
	ops := float64(b.N)
	b.ReportMetric(float64(freeing.Nanoseconds())/ops, "free-ns/op")
	b.ReportMetric(float64(used)/ops, "arena-B/op")
	b.ReportMetric(float64(peak)/ops, "arena-peak-B/op")
	b.ReportMetric(float64(resident)/ops, "rss-peak-B/op")
}

// sampleFreeSize returns a stop function for 10 ms allocator sampling.
//
// Stopping joins the sampler and includes one final free-capacity sample before
// returning the smallest observation.
func (m *bed) sampleFreeSize() func() uint64 {
	var lowest atomic.Uint64
	lowest.Store(m.agent.BlockAllocatorFreeSize())
	record := func() {
		if free := m.agent.BlockAllocatorFreeSize(); free < lowest.Load() {
			lowest.Store(free)
		}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				record()
			}
		}
	}()
	return func() uint64 {
		close(stop)
		<-done
		record()
		return lowest.Load()
	}
}

// residentPeak returns the peak resident set of the process since its last
// reset, in bytes, shared memory pages included.
func residentPeak(t testing.TB) uint64 {
	t.Helper()

	status, err := os.ReadFile("/proc/self/status")
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			kilobytes, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			require.NoError(t, err)
			return kilobytes << 10
		}
	}
	require.FailNow(t, "no peak resident set in the process status")
	return 0
}
