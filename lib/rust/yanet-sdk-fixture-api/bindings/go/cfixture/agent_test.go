//go:build sdk_rust_fixtures

package cfixture_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// octetRanges returns count IPv4 ranges, the n-th covering n.1.0.0 through
// n.1.255.255, whose table needs more memory than an empty one.
func octetRanges(count int) []cfixture.Range4 {
	ranges := make([]cfixture.Range4, 0, count)
	for octet := range count {
		ranges = append(ranges, cfixture.NewRange4(
			[4]byte{byte(octet), 1, 0, 0},
			[4]byte{byte(octet), 1, 255, 255},
			1,
		))
	}
	return ranges
}

// failedFixtureBuilds are the builds of a fixture configuration that fail after
// the build has allocated a block, that the api refuses before it builds
// anything, or that C refuses.
var failedFixtureBuilds = []struct {
	name     string
	adjust   []func(*dataplaneut.Config)
	options  fixtureOptions
	wantKind error
}{
	{
		name:    "unknown module type",
		adjust:  []func(*dataplaneut.Config){withoutModules},
		options: fixtureOf(cfixture.ModePass),
	},
	{
		name:     "name that is not UTF-8",
		options:  fixtureOptions{name: "\xff", counter: "hits", device: "port0"},
		wantKind: ffi.ErrInvalidArgument,
	},
	{
		name:     "overlong counter name",
		options:  fixtureOptions{name: "config", counter: strings.Repeat("c", 128), device: "port0"},
		wantKind: ffi.ErrInvalidArgument,
	},
	{
		name: "overlong device name",
		options: fixtureOptions{
			name:    "config",
			counter: "hits",
			device:  strings.Repeat("d", ffi.MaxDeviceNameLen),
		},
		wantKind: ffi.ErrInvalidArgument,
	},
	{
		name: "fault error",
		options: fixtureOptions{
			name:    "config",
			counter: "hits",
			device:  "port0",
			fault:   cfixture.FaultError,
		},
		wantKind: ffi.ErrInvalidArgument,
	},
	{
		name: "fault panic",
		options: fixtureOptions{
			name:    "config",
			counter: "hits",
			device:  "port0",
			fault:   cfixture.FaultPanic,
		},
	},
}

// Test_FixtureConfig_Publish_RegistersTypeAndName verifies that a published
// configuration is known to the dataplane under the module type and the name
// it was built with.
func Test_FixtureConfig_Publish_RegistersTypeAndName(t *testing.T) {
	bed := newBed(t)
	bed.publishModules(bed.fixtureInstance(fixtureOf(cfixture.ModePass)))

	configs := bed.harness.SharedMemory().DPConfig(0).CPConfigs()

	require.True(t, slices.ContainsFunc(configs, func(config ffi.CPConfig) bool {
		return config.Type == "fixture" && config.Name == "config"
	}), "%v", configs)
}

// Test_FixtureConfig_Publish_RegistersCounterByName verifies that the counter
// a build registers is read back by its name, one value for each worker.
func Test_FixtureConfig_Publish_RegistersCounterByName(t *testing.T) {
	bed := newBed(t)
	inst := bed.fixtureInstance(fixtureOf(cfixture.ModePass))
	bed.serveBeforeForward(inst)

	values := bed.counter(inst, "hits")

	require.Equal(t, [][]uint64{{0}, {0}, {0}, {0}}, values)
}

// Test_FixtureConfig_Publish_LinksDeviceByName verifies that a configuration
// built with the name of a device redirects to that device and no other.
func Test_FixtureConfig_Publish_LinksDeviceByName(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.Devices = []string{"port0", "port1"}
	})
	options := fixtureOf(cfixture.ModeForward)
	options.device = "port1"
	forward := bed.fixtureInstance(options)
	tap := bed.tapInstance("tap")
	bed.publish(
		route{device: "port0", input: []*instance{forward}},
		route{device: "port1", output: []*instance{tap}},
	)

	bed.run(0, arpFrames(1)...)

	require.Equal(t, packetsAndBytes(1, 60), bed.counter(tap, "rx")[0])
}

// Test_FixtureConfig_Build_Failure_ReportsItsKind verifies that a build
// refused by the api or by C reports the kind of the failure, and none when C
// tags none.
func Test_FixtureConfig_Build_Failure_ReportsItsKind(t *testing.T) {
	for _, tc := range failedFixtureBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := bed.buildFixture(tc.options)

			if tc.wantKind == nil {
				requireUntagged(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantKind)
		})
	}
}

// Test_FixtureConfig_Build_Failure_ReturnsMemoryToBaseline verifies that a
// failed build gives back every block it took, including the one it allocated
// before it failed, and takes none when the api refuses it up front.
func Test_FixtureConfig_Build_Failure_ReturnsMemoryToBaseline(t *testing.T) {
	for _, tc := range failedFixtureBuilds {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t, tc.adjust...)

			_, err := bed.buildFixture(tc.options)

			require.Error(t, err)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_FixtureConfig_Build_Panic_IsContained verifies that ordinary panics and
// panicking payload destructors return errors and release all allocations.
func Test_FixtureConfig_Build_Panic_IsContained(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   cfixture.Fault
		message string
	}{
		{name: "ordinary panic", fault: cfixture.FaultPanic, message: "panic: fixture fault"},
		{name: "payload destructor panic", fault: cfixture.FaultPanicPayload, message: "panic: no message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			options := fixtureOf(cfixture.ModePass)
			options.fault = tc.fault

			_, err := bed.buildFixture(options)

			require.ErrorContains(t, err, tc.message)
			require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_FixtureConfig_Build_NullSpec_IsInvalidArgument verifies that a record
// pointer C passes as NULL is refused as an invalid argument, not read.
func Test_FixtureConfig_Build_NullSpec_IsInvalidArgument(t *testing.T) {
	bed := newBed(t)

	_, err := cfixture.NewFixtureConfig(bed.agent, "config", "hits", "port0", nil, nil)

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
}

// Test_FixtureConfig_Build_Failure_KeepsTheCallersChainAsCause verifies that a
// refused build adds its error over a chain the caller passed in, which stays
// as its cause.
func Test_FixtureConfig_Build_Failure_KeepsTheCallersChainAsCause(t *testing.T) {
	err := cfixture.RefusedBuildOverCause("earlier failure")

	require.ErrorIs(t, err, ffi.ErrInvalidArgument)
	require.EqualError(t, err, "argument `agent` is null or misaligned: earlier failure")
}

// exhaustedBuild builds a fixture with a table of many ranges in an agent too
// small for it.
func exhaustedBuild(t *testing.T) error {
	t.Helper()

	bed := newBed(t)
	small := attach(t, bed.harness, "small", 64*datasize.KB)
	spec := cfixture.NewFixtureSpec(cfixture.ModePass, 0, cfixture.FaultNone)

	_, err := cfixture.NewFixtureConfig(small, "config", "hits", "port0", &spec, octetRanges(256))
	return err
}

// Test_FixtureConfig_Build_MemoryExhausted_IsResourceExhausted verifies that a
// build that runs out of the agent's memory is refused as resource exhausted.
func Test_FixtureConfig_Build_MemoryExhausted_IsResourceExhausted(t *testing.T) {
	require.ErrorIs(t, exhaustedBuild(t), ffi.ErrResourceExhausted)
}

// Test_FixtureConfig_Build_MemoryExhausted_CarriesTheSdkMessage verifies that
// the refusal of a build that ran out of memory carries the SDK's own message.
func Test_FixtureConfig_Build_MemoryExhausted_CarriesTheSdkMessage(t *testing.T) {
	require.ErrorContains(t, exhaustedBuild(t), "the shared memory is exhausted")
}

// Test_FixtureConfig_Build_RangeTable_UsesMoreMemoryThanSmaller verifies that
// the table of a configuration with more ranges takes more of the agent's
// memory.
func Test_FixtureConfig_Build_RangeTable_UsesMoreMemoryThanSmaller(t *testing.T) {
	bed := newBed(t)
	small := fixtureOf(cfixture.ModePass)
	small.ranges = octetRanges(1)
	smallConfig := bed.fixture(small)
	smallUsed := bed.used()
	require.NoError(t, smallConfig.Free())
	large := fixtureOf(cfixture.ModePass)
	large.ranges = octetRanges(256)

	bed.fixture(large)

	require.Greater(t, bed.used(), smallUsed)
}

// Test_FixtureConfig_Free_RangeTable_ReturnsMemoryToBaseline verifies that
// freeing a configuration with a large table gives back the table too.
func Test_FixtureConfig_Free_RangeTable_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	options := fixtureOf(cfixture.ModePass)
	options.ranges = octetRanges(256)
	config := bed.fixture(options)

	require.NoError(t, config.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_FixtureConfig_Free_Unpublished_ReturnsMemoryToBaseline verifies that
// freeing a configuration no generation holds gives back all it took.
func Test_FixtureConfig_Free_Unpublished_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t)
	config := bed.fixture(fixtureOf(cfixture.ModePass))

	require.NoError(t, config.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_FixtureConfig_Free_Unpublished_LeavesLoadedCountUnchanged verifies that
// building and freeing a configuration returns the destruction hold the free
// takes, so the agent's loaded count ends where it began.
func Test_FixtureConfig_Free_Unpublished_LeavesLoadedCountUnchanged(t *testing.T) {
	bed := newBed(t)
	before := dataplaneut.LoadedEntityCount(bed.agent)
	config := bed.fixture(fixtureOf(cfixture.ModePass))

	require.NoError(t, config.Free())

	require.Equal(t, before, dataplaneut.LoadedEntityCount(bed.agent))
}

// Test_FixtureConfig_Free_WithOtherAgentAttached_ReturnsMemoryToBuilder
// verifies that a free gives the memory back to the agent that built the
// configuration, and takes none from another agent attached to the same
// memory.
func Test_FixtureConfig_Free_WithOtherAgentAttached_ReturnsMemoryToBuilder(t *testing.T) {
	bed := newBed(t)
	other := attach(t, bed.harness, "other", datasize.MB)
	otherFree := other.BlockAllocatorFreeSize()
	config := bed.fixture(fixtureOf(cfixture.ModePass))

	require.NoError(t, config.Free())

	require.Equal(
		t,
		[]uint64{bed.baseline, otherFree},
		[]uint64{bed.agent.BlockAllocatorFreeSize(), other.BlockAllocatorFreeSize()},
	)
}

// Test_FixtureConfig_Free_WithOtherAgentAttached_LeavesLoadedCountsUnchanged
// verifies that a free returns the destruction hold to the agent that built the
// configuration, and leaves the count of another agent attached to the same
// memory where it was.
func Test_FixtureConfig_Free_WithOtherAgentAttached_LeavesLoadedCountsUnchanged(t *testing.T) {
	bed := newBed(t)
	other := attach(t, bed.harness, "other", datasize.MB)
	before := []uint64{dataplaneut.LoadedEntityCount(bed.agent), dataplaneut.LoadedEntityCount(other)}
	config := bed.fixture(fixtureOf(cfixture.ModePass))

	require.NoError(t, config.Free())

	require.Equal(t, before, []uint64{dataplaneut.LoadedEntityCount(bed.agent), dataplaneut.LoadedEntityCount(other)})
}

// publishedFixture builds a fixture configuration and publishes it as the only
// module of a generation, which holds it until a later generation drops it.
func publishedFixture(t *testing.T) (*bed, *instance) {
	t.Helper()

	bed := newBed(t)
	inst := bed.fixtureInstance(fixtureOf(cfixture.ModePass))
	bed.publishModules(inst)
	return bed, inst
}

// Test_FixtureConfig_Free_Busy_ReportsStillReferenced verifies that freeing a
// configuration a live generation holds is refused as still referenced.
func Test_FixtureConfig_Free_Busy_ReportsStillReferenced(t *testing.T) {
	_, inst := publishedFixture(t)

	require.ErrorIs(t, inst.config.Free(), ffi.ErrStillReferenced)
}

// Test_FixtureConfig_Free_Busy_KeepsHandle verifies that a refused free keeps
// the handle, so that the free can be tried again.
func Test_FixtureConfig_Free_Busy_KeepsHandle(t *testing.T) {
	_, inst := publishedFixture(t)

	_ = inst.config.Free()

	require.NotNil(t, inst.config.AsFFIModule().AsRawPtr())
}

// Test_FixtureConfig_Free_Busy_KeepsMemory verifies that a refused free takes
// none of the configuration's memory back.
func Test_FixtureConfig_Free_Busy_KeepsMemory(t *testing.T) {
	bed, inst := publishedFixture(t)
	used := bed.used()

	_ = inst.config.Free()

	require.Equal(t, used, bed.used())
}

// Test_FixtureConfig_Free_Busy_TakesNoDestructionHold verifies that a refused
// free leaves the agent's loaded count as the live generation made it.
func Test_FixtureConfig_Free_Busy_TakesNoDestructionHold(t *testing.T) {
	bed, inst := publishedFixture(t)
	held := dataplaneut.LoadedEntityCount(bed.agent)

	_ = inst.config.Free()

	require.Equal(t, held, dataplaneut.LoadedEntityCount(bed.agent))
}

// Test_FixtureConfig_Free_Busy_KeepsConfigurationIntact verifies that the
// generation which holds a configuration still runs it after a refused free.
func Test_FixtureConfig_Free_Busy_KeepsConfigurationIntact(t *testing.T) {
	bed := newBed(t)
	inst := bed.fixtureInstance(fixtureOf(cfixture.ModePass))
	bed.serveBeforeForward(inst)
	_ = inst.config.Free()
	frames := arpFrames(1)

	result := bed.run(0, frames...)

	require.Equal(t, frames, result.Output)
}

// retire publishes a generation without the configuration, which retires the
// generation that held it.
func retire(t *testing.T, bed *bed, inst *instance) {
	t.Helper()

	require.NoError(t, bed.agent.DeleteModuleConfig(inst.moduleType, inst.name))
}

// Test_FixtureConfig_Free_AfterGenerationRetired_Succeeds verifies that a
// configuration whose free was refused is freed once the generation that held
// it is gone.
func Test_FixtureConfig_Free_AfterGenerationRetired_Succeeds(t *testing.T) {
	bed, inst := publishedFixture(t)
	require.ErrorIs(t, inst.config.Free(), ffi.ErrStillReferenced)
	retire(t, bed, inst)

	require.NoError(t, inst.config.Free())
}

// Test_FixtureConfig_Free_AfterGenerationRetired_ReturnsMemoryToBaseline
// verifies that the retried free gives back all the memory the configuration
// took.
func Test_FixtureConfig_Free_AfterGenerationRetired_ReturnsMemoryToBaseline(t *testing.T) {
	bed, inst := publishedFixture(t)
	_ = inst.config.Free()
	retire(t, bed, inst)

	require.NoError(t, inst.config.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}
