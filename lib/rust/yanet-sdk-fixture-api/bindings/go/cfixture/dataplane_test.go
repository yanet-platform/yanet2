//go:build sdk_rust_fixtures

package cfixture_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// Test_Fixture_Mode_FatesOfEachPacket verifies that a verdict puts a packet on
// exactly one list, and that a module which forgets a packet leaves it on
// none.
func Test_Fixture_Mode_FatesOfEachPacket(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  cfixture.Mode
		count int
		want  []fate
	}{
		{
			name:  "forgotten packets are on no list",
			mode:  cfixture.ModeForget,
			count: 3,
			want:  []fate{fateLost, fateLost, fateOutput},
		},
		{
			name:  "packets taken together are disposed of out of order",
			mode:  cfixture.ModeShuffle,
			count: 3,
			want:  []fate{fateDrop, fateOutput, fateOutput},
		},
		{
			name:  "dispatch applies every verdict without a prepared value",
			mode:  cfixture.ModeParity,
			count: 4,
			want:  []fate{fateOutput, fateDrop, fateOutput, fateDrop},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			frames := arpFrames(tc.count)

			result := bed.runFixture(tc.mode, frames...)

			require.Equal(t, tc.want, fatesOf(result, frames))
		})
	}
}

// Test_Fixture_ForgottenPackets_CountedByTheModuleCounters verifies that the
// counters of a module that forgets packets count every input, only the packet
// it passed on the way out, and no drop.
func Test_Fixture_ForgottenPackets_CountedByTheModuleCounters(t *testing.T) {
	bed := newBed(t)
	inst := bed.fixtureInstance(fixtureOf(cfixture.ModeForget))
	bed.serveBeforeForward(inst)
	bed.run(0, arpFrames(3)...)

	for _, tc := range []struct {
		name    string
		counter string
		want    []uint64
	}{
		{name: "input counter counts every input", counter: "rx", want: packetsAndBytes(3, 180)},
		{name: "output counter counts only the passed packet", counter: "tx", want: packetsAndBytes(1, 60)},
		{name: "drop counter counts no forgotten packet", counter: "drop", want: packetsAndBytes(0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, bed.counter(inst, tc.counter)[0])
		})
	}
}

// Test_Fixture_WorkerMode_ReadsTheRunningWorker verifies that a module reads
// the index of the worker its round runs on: worker 3 drops what workers
// before it pass.
func Test_Fixture_WorkerMode_ReadsTheRunningWorker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		worker int
		want   fate
	}{
		{name: "worker 2 passes", worker: 2, want: fateOutput},
		{name: "worker 3 drops", worker: 3, want: fateDrop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			bed.harness.SetCurrentTime(time.Unix(0, 0))
			bed.serveBeforeForward(bed.fixtureInstance(fixtureOf(cfixture.ModeWorker)))
			frames := arpFrames(1)

			result := bed.run(tc.worker, frames...)

			require.Equal(t, []fate{tc.want}, fatesOf(result, frames))
		})
	}
}

// Test_Fixture_WorkerMode_ReadsTheRoundTime verifies that a module reads the
// time of its round: the module drops once the time has passed 1000.
func Test_Fixture_WorkerMode_ReadsTheRoundTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Duration
		want fate
	}{
		{name: "time 1000 passes", now: 1000, want: fateOutput},
		{name: "time 1001 drops", now: 1001, want: fateDrop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			bed.harness.SetCurrentTime(time.Unix(0, int64(tc.now)))
			bed.serveBeforeForward(bed.fixtureInstance(fixtureOf(cfixture.ModeWorker)))
			frames := arpFrames(1)

			result := bed.run(0, frames...)

			require.Equal(t, []fate{tc.want}, fatesOf(result, frames))
		})
	}
}

// Test_Tally_PreparedValue_DerivesFromItsConfiguration verifies that the value
// a module prepares at commit is derived from the configuration it was built
// with: twice the word must be 14 for the packet to pass.
func Test_Tally_PreparedValue_DerivesFromItsConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		word uint64
		want fate
	}{
		{name: "word of the configuration passes", word: 7, want: fateOutput},
		{name: "other word drops", word: 8, want: fateDrop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			bed.serveBeforeForward(bed.tallyInstance("config", tc.word))
			frames := arpFrames(1)

			result := bed.run(0, frames...)

			require.Equal(t, []fate{tc.want}, fatesOf(result, frames))
		})
	}
}

// Test_Tally_Counters_CountOnTheRunningWorker verifies that a counter added to
// by value and a counter added to through a handle are those registered under
// their names, and that only the worker the round ran on counts.
func Test_Tally_Counters_CountOnTheRunningWorker(t *testing.T) {
	bed := newBed(t)
	inst := bed.tallyInstance("config", 7)
	bed.serveBeforeForward(inst)
	bed.run(1, arpFrames(5)...)

	for _, tc := range []struct {
		name    string
		counter string
		want    [][]uint64
	}{
		{name: "first counter by value", counter: "v0", want: [][]uint64{{0}, {2}, {0}, {0}}},
		{name: "second counter by value", counter: "v1", want: [][]uint64{{0}, {2}, {0}, {0}}},
		{name: "third counter by value", counter: "v2", want: [][]uint64{{0}, {1}, {0}, {0}}},
		{name: "counter by handle", counter: "held", want: [][]uint64{{0}, {10}, {0}, {0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, bed.counter(inst, tc.counter))
		})
	}
}

// Test_Fixture_DeviceResolution_ByTheGeneration verifies that the device of a
// configuration resolves as a target of the generation when the generation has
// it, as an output or as an input target, and does not when the generation
// lacks it.
func Test_Fixture_DeviceResolution_ByTheGeneration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   cfixture.Mode
		device string
		want   fate
	}{
		{name: "device of the generation as output target", mode: cfixture.ModeResolve, device: "port0", want: fateOutput},
		{name: "device of the generation as input target", mode: cfixture.ModeResolveInput, device: "port0", want: fateOutput},
		{name: "absent device as output target", mode: cfixture.ModeResolve, device: "absent", want: fateDrop},
		{name: "absent device as input target", mode: cfixture.ModeResolveInput, device: "absent", want: fateDrop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bed := newBed(t)
			options := fixtureOf(tc.mode)
			options.device = tc.device
			bed.serveBeforeForward(bed.fixtureInstance(options))
			frames := arpFrames(1)

			result := bed.run(0, frames...)

			require.Equal(t, []fate{tc.want}, fatesOf(result, frames))
		})
	}
}

// Test_Fixture_RedirectToOutput_EndsOnTheTransmitList verifies that a packet
// redirected to the output of a device and passed by the pipeline there ends on
// the transmit list.
func Test_Fixture_RedirectToOutput_EndsOnTheTransmitList(t *testing.T) {
	bed := newBed(t)
	bed.serve([]*instance{bed.forwardInstance()}, []*instance{bed.tapInstance("tap")})
	frames := arpFrames(3)

	result := bed.run(0, frames...)

	require.Equal(t, frames, result.Output)
}

// Test_Fixture_RedirectToOutput_ReachesTheOutputPipeline verifies that a packet
// redirected to the output of a device runs the pipeline of that output.
func Test_Fixture_RedirectToOutput_ReachesTheOutputPipeline(t *testing.T) {
	bed := newBed(t)
	tap := bed.tapInstance("tap")
	bed.serve([]*instance{bed.forwardInstance()}, []*instance{tap})
	bed.run(0, arpFrames(3)...)

	require.Equal(t, packetsAndBytes(3, 180), bed.counter(tap, "rx")[0])
}

// Test_Fixture_RedirectToInput_EndsOnTheTransmitList verifies that a packet
// redirected to the input of a device and passed by the pipeline there ends on
// the transmit list, marked by the module that redirected it.
func Test_Fixture_RedirectToInput_EndsOnTheTransmitList(t *testing.T) {
	bed := newBed(t)
	bed.serve(
		[]*instance{
			bed.fixtureInstance(fixtureOf(cfixture.ModeReenter)),
			bed.tapInstance("second"),
			bed.forwardInstance(),
		},
		nil,
	)
	marked := arpFrames(2)
	marked[0][0] |= 0x80
	marked[1][0] |= 0x80

	result := bed.run(0, arpFrames(2)...)

	require.Equal(t, marked, result.Output)
}

// Test_Fixture_RedirectToInput_RunsTheInputPipelineAgain verifies that a packet
// redirected to the input of a device runs the input pipeline from its first
// module again: each of the two packets passes the redirecting module twice.
func Test_Fixture_RedirectToInput_RunsTheInputPipelineAgain(t *testing.T) {
	bed := newBed(t)
	first := bed.fixtureInstance(fixtureOf(cfixture.ModeReenter))
	bed.serve([]*instance{first, bed.tapInstance("second"), bed.forwardInstance()}, nil)
	bed.run(0, arpFrames(2)...)

	require.Equal(t, packetsAndBytes(4, 240), bed.counter(first, "rx")[0])
}

// Test_Fixture_RedirectToInput_PassesTheFollowingModuleOnce verifies that the
// module after the redirecting one sees each packet once: the redirect leaves
// the first pass before it and the marked second pass reaches it.
func Test_Fixture_RedirectToInput_PassesTheFollowingModuleOnce(t *testing.T) {
	bed := newBed(t)
	second := bed.tapInstance("second")
	bed.serve(
		[]*instance{bed.fixtureInstance(fixtureOf(cfixture.ModeReenter)), second, bed.forwardInstance()},
		nil,
	)
	bed.run(0, arpFrames(2)...)

	require.Equal(t, packetsAndBytes(2, 120), bed.counter(second, "rx")[0])
}
