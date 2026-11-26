//go:build linux && (amd64 || arm64)

package nativetest_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// faultTimeout is the reply timeout of the tests of a helper that never
// answers, long enough for the helper to start under a sanitizer.
const faultTimeout = time.Second

// recorder is a test the client fails in place of the real one, so a test
// can check that a failure reaches it.
//
// It embeds the real test for what the client and require do not call.
// Fatal failures stop the goroutine, as they stop a test, and cleanups run
// when the test asks.
type recorder struct {
	testing.TB
	mu       sync.Mutex
	failures []string
	errors   []error
	logs     []string
	cleanups []func()
}

func (m *recorder) Helper() {}

func (m *recorder) Errorf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = append(m.failures, fmt.Sprintf(format, args...))
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			m.errors = append(m.errors, err)
		}
	}
}

func (m *recorder) Fatalf(format string, args ...any) {
	m.Errorf(format, args...)
	runtime.Goexit()
}

func (m *recorder) FailNow() {
	m.Errorf("failed")
	runtime.Goexit()
}

func (m *recorder) Failed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.failures) != 0
}

func (m *recorder) Logf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, fmt.Sprintf(format, args...))
}

func (m *recorder) Cleanup(cleanup func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups = append(m.cleanups, cleanup)
}

// Failures returns every failure recorded, one per line.
func (m *recorder) Failures() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.failures, "\n")
}

// FailedWith reports whether a failure was formatted from an error that
// is the target, as errors.Is matches it.
func (m *recorder) FailedWith(target error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, err := range m.errors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// HelperFailure returns the first failure of the helper process the
// test failed with.
func (m *recorder) HelperFailure(t *testing.T) *nativetest.HelperError {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, err := range m.errors {
		if failure, ok := errors.AsType[*nativetest.HelperError](err); ok {
			return failure
		}
	}
	require.Fail(t, "no failure of the helper", "failures: %s", strings.Join(m.failures, "\n"))
	return nil
}

// Logs returns every log line recorded.
func (m *recorder) Logs() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.logs, "\n")
}

// record runs a test body against a recorder, as the testing package runs
// a test: in a goroutine of its own, then its cleanups in reverse order.
func record(t *testing.T, body func(t testing.TB)) *recorder {
	m := &recorder{TB: t}
	runToEnd(func() { body(m) })
	for {
		m.mu.Lock()
		count := len(m.cleanups)
		if count == 0 {
			m.mu.Unlock()
			return m
		}
		cleanup := m.cleanups[count-1]
		m.cleanups = m.cleanups[:count-1]
		m.mu.Unlock()
		runToEnd(cleanup)
	}
}

// runToEnd runs a function in a goroutine and returns when it returned or
// stopped, as a fatal failure stops it.
func runToEnd(fn func()) {
	var group sync.WaitGroup
	group.Go(fn)
	group.Wait()
}

// helperFailure returns the failure of the helper process an error is.
func helperFailure(t *testing.T, err error) *nativetest.HelperError {
	t.Helper()
	failure, ok := errors.AsType[*nativetest.HelperError](err)
	require.True(t, ok, "not a failure of the helper: %v", err)
	return failure
}

// signalOf returns the signal that ended a process, or zero.
func signalOf(state *os.ProcessState) syscall.Signal {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0
	}
	return status.Signal()
}

// udpFrame returns an Ethernet frame of an IPv4 UDP datagram without
// payload, which the dataplane parses.
func udpFrame(t *testing.T) []byte {
	ip := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IPv4(192, 0, 2, 1),
		DstIP:    net.IPv4(198, 51, 100, 1),
	}
	udp := &layers.UDP{SrcPort: 12345, DstPort: 53}
	require.NoError(t, udp.SetNetworkLayerForChecksum(ip))
	return xpacket.LayersToPacket(t,
		&layers.Ethernet{
			SrcMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02},
			DstMAC:       net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01},
			EthernetType: layers.EthernetTypeIPv4,
		},
		ip,
		udp,
	).Data()
}

// publishPipeline publishes through the dataplane a plain device whose
// input runs the pipeline, of functions running the chains of modules.
func publishPipeline(t testing.TB, dataplane *nativetest.Dataplane, modules []*layout.CpModule, owner *agent.Agent, chains ...cpconfig.ChainConfig) {
	t.Helper()
	instance := dataplane.Instance()
	deliver := cpconfig.WithDelivery(dataplane.Deliver)
	if len(modules) != 0 {
		require.NoError(t, cpconfig.UpdateModules(instance, modules, deliver))
	}
	var functions []string
	for _, chain := range chains {
		require.NoError(t, cpconfig.UpdateFunctions(instance, []cpconfig.FunctionConfig{{
			Name:   chain.Name,
			Chains: []cpconfig.FunctionChainConfig{{Chain: chain, Weight: 1}},
		}}, deliver))
		functions = append(functions, chain.Name)
	}
	require.NoError(t, cpconfig.UpdatePipelines(instance, []cpconfig.PipelineConfig{
		{Name: "input", Functions: functions},
		{Name: "output"},
	}, deliver))
	device, err := cpconfig.NewPlainDevice(owner, cpconfig.DeviceConfig{
		Name:   "port0",
		Input:  []cpconfig.DevicePipelineConfig{{Name: "input", Weight: 1}},
		Output: []cpconfig.DevicePipelineConfig{{Name: "output", Weight: 1}},
	})
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateDevices(instance, []*layout.CpDevice{device}, deliver))
}

// newDataplane starts a dataplane of two workers with one plain device and
// the blackhole module, and attaches an agent to it.
func newDataplane(t testing.TB) (*nativetest.Dataplane, *agent.Agent) {
	t.Helper()
	dataplane := nativetest.NewDataplane(t, nativetest.Config{
		Workers:     2,
		Devices:     []string{"port0"},
		Modules:     []string{"blackhole"},
		DeviceTypes: []string{"plain"},
	})
	owner, err := agent.Attach(dataplane.Instance(), "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	return dataplane, owner
}

// Test_Dataplane_Round_DropsThroughPublishedBlackhole verifies that a
// topology Go published and the helper delivered runs in the helper's
// rounds: a blackhole on the device's input drops every frame.
func Test_Dataplane_Round_DropsThroughPublishedBlackhole(t *testing.T) {
	dataplane, owner := newDataplane(t)
	module, err := cpconfig.NewBlackhole(owner, "sink")
	require.NoError(t, err)
	publishPipeline(t, dataplane, []*layout.CpModule{module}, owner, cpconfig.ChainConfig{
		Name:    "drop",
		Modules: []cpconfig.ChainModuleConfig{{Type: "blackhole", Name: "sink"}},
	})

	frame := udpFrame(t)
	result, err := dataplane.Round(1, 0, frame, frame)

	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Equal(t, [][]byte{frame, frame}, result.Drop)
}

// Test_Dataplane_Round_EmptyAcknowledgesGenerationAndTime verifies that a
// round without frames makes its worker acknowledge the generation last
// delivered, while the time set reaches every worker at once.
func Test_Dataplane_Round_EmptyAcknowledgesGenerationAndTime(t *testing.T) {
	dataplane, owner := newDataplane(t)
	publishPipeline(t, dataplane, nil, owner)
	published := dataplane.Instance().Cp.CpConfigGen.Get().Gen
	now := time.Unix(1_700_000_000, 5)

	require.NoError(t, dataplane.SetTime(now))
	_, err := dataplane.Round(1, 0)

	require.NoError(t, err)
	workers, err := dataplane.Instance().Workers()
	require.NoError(t, err)
	require.Less(t, workers[0].Generation, published, "the idle worker has not acknowledged")
	require.Equal(t, published, workers[1].Generation)
	current, ok := dataplane.Instance().CurrentTime()
	require.True(t, ok)
	require.Equal(t, uint64(now.UnixNano()), current)
}

// Test_Dataplane_Round_ErrorReplyFailsCaller verifies that a request the
// helper refuses fails the caller, not the test, and that the helper
// answers the next request.
func Test_Dataplane_Round_ErrorReplyFailsCaller(t *testing.T) {
	var refused, next error
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultError})
		_, refused = dataplane.Round(0, 0)
		_, next = dataplane.Round(0, 0)
	})

	require.ErrorIs(t, refused, nativetest.ErrRefused)
	require.False(t, test.Failed(), test.Failures())
	require.NoError(t, next)
}

// Test_Dataplane_Round_CrashFailsTest verifies that a helper that panics
// aborts, never replying, and fails the request and the test with its end
// and its panic and stack on its error output.
func Test_Dataplane_Round_CrashFailsTest(t *testing.T) {
	var roundErr error
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultCrash})
		_, roundErr = dataplane.Round(0, 0)
	})

	failure := helperFailure(t, roundErr)
	require.NotErrorIs(t, roundErr, nativetest.ErrRefused)
	require.Equal(t, syscall.SIGABRT, signalOf(failure.State))
	require.Contains(t, failure.Stderr, "panic: injected crash")
	require.Contains(t, failure.Stderr, "goroutine ")
	require.Equal(t, syscall.SIGABRT, signalOf(test.HelperFailure(t).State))
}

// Test_Dataplane_Round_ExitLeavingProcessFailsTestAtOnce verifies that a
// helper that exits while a process it started holds its error output
// fails the request and the test at once, well within the reply timeout,
// with its end and its output: the kill reaches that process too.
func Test_Dataplane_Round_ExitLeavingProcessFailsTestAtOnce(t *testing.T) {
	var roundErr error
	var elapsed time.Duration
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultLinger})
		start := time.Now()
		_, roundErr = dataplane.Round(0, 0)
		elapsed = time.Since(start)
	})

	failure := helperFailure(t, roundErr)
	require.NotNil(t, failure.State, "the helper ended")
	require.Equal(t, 1, failure.State.ExitCode())
	require.Contains(t, failure.Stderr, "injected exit leaving a process behind")
	require.True(t, test.Failed())
	require.Less(t, elapsed, time.Second)
}

// Test_Dataplane_Round_HangFailsTestWithinTimeout verifies that a helper
// that does not answer is killed at the reply timeout and fails the
// request and the test.
func Test_Dataplane_Round_HangFailsTestWithinTimeout(t *testing.T) {
	var roundErr error
	var start time.Time
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultHang, ReplyTimeout: faultTimeout})
		start = time.Now()
		_, roundErr = dataplane.Round(0, 0)
	})

	require.ErrorIs(t, roundErr, os.ErrDeadlineExceeded)
	require.Equal(t, syscall.SIGKILL, signalOf(helperFailure(t, roundErr).State))
	require.True(t, test.FailedWith(os.ErrDeadlineExceeded))
	require.Less(t, time.Since(start), 2*faultTimeout)
}

// Test_Dataplane_Deliver_FailureFailsTest verifies that a delivery the
// helper refuses fails the next request, and the test at its end with
// how the helper ended.
func Test_Dataplane_Deliver_FailureFailsTest(t *testing.T) {
	var roundErr error
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultError})
		instance := dataplane.Instance()
		dataplane.Deliver(instance.Dp, instance.Cp.CpConfigGen.Get())
		_, roundErr = dataplane.Round(0, 0)
	})

	require.ErrorIs(t, roundErr, nativetest.ErrRefused)
	failure := test.HelperFailure(t)
	require.ErrorIs(t, failure, nativetest.ErrRefused)
	require.True(t, failure.State.Success(), "the helper quit cleanly")
}

// Test_NewDataplane_RedZoneMismatchFailsTest verifies that a helper whose
// blocks carry other red zones than this test's fails the test.
func Test_NewDataplane_RedZoneMismatchFailsTest(t *testing.T) {
	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultRedZone})
	})

	require.True(t, test.FailedWith(nativetest.ErrRedZoneMismatch))
}

// Test_Dataplane_Close_NonzeroExitFailsTest verifies that a helper that
// answers quit and exits nonzero fails the test.
func Test_Dataplane_Close_NonzeroExitFailsTest(t *testing.T) {
	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultExit})
	})

	require.Equal(t, 3, test.HelperFailure(t).State.ExitCode())
}

// Test_Dataplane_Close_LeakedMbufFailsTest verifies that an mbuf the
// dataplane still holds at quit fails the test.
func Test_Dataplane_Close_LeakedMbufFailsTest(t *testing.T) {
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultLeak})
		_, err := dataplane.Round(0, 0)
		require.NoError(t, err)
	})

	failure := test.HelperFailure(t)
	require.ErrorIs(t, failure, nativetest.ErrRefused)
	require.Equal(t, 1, failure.State.ExitCode())
}

// Test_NewDataplane_UnknownModuleFailsTestWithLog verifies that a helper
// that cannot build its harness fails the test with its end and the
// harness's log of the cause.
func Test_NewDataplane_UnknownModuleFailsTestWithLog(t *testing.T) {
	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{Modules: []string{"no-such-module"}})
	})

	failure := test.HelperFailure(t)
	require.ErrorIs(t, failure, nativetest.ErrRefused)
	require.Equal(t, 1, failure.State.ExitCode())
	require.Contains(t, failure.Stderr, "no-such-module")
}

// Test_NewDataplane_MissingHelperFailsTest verifies that a helper that is
// not built fails the test naming the build target.
func Test_NewDataplane_MissingHelperFailsTest(t *testing.T) {
	t.Setenv(nativetest.HelperEnv, filepath.Join(t.TempDir(), "dataplane-helper"))

	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{})
	})

	require.True(t, test.FailedWith(fs.ErrNotExist))
	require.Contains(t, test.Failures(), "meson compile -C build")
}

// Test_Dataplane_Abandon_KeepsStorageMapped verifies that an abandoned
// dataplane's helper is killed at the end of the test without failing it,
// and that its storage stays readable.
func Test_Dataplane_Abandon_KeepsStorageMapped(t *testing.T) {
	var dataplane *nativetest.Dataplane
	test := record(t, func(t testing.TB) {
		dataplane = nativetest.NewDataplane(t, nativetest.Config{Workers: 3})
		dataplane.Abandon()
	})

	require.False(t, test.Failed(), test.Failures())
	require.Equal(t, uint64(3), dataplane.Instance().Dp.WorkerCount)
}

// Test_Dataplane_Abandon_CrashFailsTest verifies that an abandoned
// dataplane's helper that crashed still fails the test.
func Test_Dataplane_Abandon_CrashFailsTest(t *testing.T) {
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultCrash})
		dataplane.Abandon()
		_, err := dataplane.Round(0, 0)
		require.Error(t, err)
	})

	require.Equal(t, syscall.SIGABRT, signalOf(test.HelperFailure(t).State))
}

// Test_Dataplane_Abandon_KeptFailureFailsTest verifies that a delivery
// that failed before the dataplane was abandoned still fails the test,
// although the helper ends by the kill.
func Test_Dataplane_Abandon_KeptFailureFailsTest(t *testing.T) {
	cases := []struct {
		name  string
		fault helperproto.Fault
		// foreign delivers to a dataplane zone not the dataplane's.
		foreign bool
		// failure is the error the test fails with, when it has one.
		failure error
	}{
		{name: "refused delivery", fault: helperproto.FaultError, failure: nativetest.ErrRefused},
		{name: "timed out delivery", fault: helperproto.FaultHang, failure: os.ErrDeadlineExceeded},
		{name: "delivery to another dataplane zone", foreign: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			test := record(t, func(t testing.TB) {
				dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: tc.fault, ReplyTimeout: faultTimeout})
				instance := dataplane.Instance()
				dp := instance.Dp
				if tc.foreign {
					dp = &layout.DpConfig{}
				}
				dataplane.Deliver(dp, instance.Cp.CpConfigGen.Get())
				dataplane.Abandon()
			})

			require.True(t, test.Failed())
			if tc.failure != nil {
				require.True(t, test.FailedWith(tc.failure), test.Failures())
			}
		})
	}
}

// Test_Dataplane_Abandon_RequestInFlightEndsByKill verifies that the kill
// of an abandoned dataplane ends a request in flight with an error that
// does not fail the test.
func Test_Dataplane_Abandon_RequestInFlightEndsByKill(t *testing.T) {
	var inFlight errgroup.Group
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultHang})
		dataplane.Abandon()
		inFlight.Go(func() error {
			_, err := dataplane.Round(0, 0)
			if err == nil {
				return errors.New("the request in flight ended without an error")
			}
			return nil
		})
	})

	require.NoError(t, inFlight.Wait())
	require.False(t, test.Failed(), test.Failures())
}

// anonymousStorage counts the descriptors and mappings of anonymous
// dataplane storage this process holds.
func anonymousStorage(t testing.TB) int {
	t.Helper()
	count := 0
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	for _, entry := range entries {
		// A descriptor closed since the listing has no link left.
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && strings.Contains(target, "memfd:"+nativetest.MemfdName) {
			count++
		}
	}
	maps, err := os.ReadFile("/proc/self/maps")
	require.NoError(t, err)
	return count + strings.Count(string(maps), "memfd:"+nativetest.MemfdName)
}

// Test_NewDataplane_LeavesNoAnonymousStorage verifies that the storage is
// mapped while the dataplane runs and that neither a descriptor nor a
// mapping of it is left when the test ends.
func Test_NewDataplane_LeavesNoAnonymousStorage(t *testing.T) {
	before := anonymousStorage(t)
	var during int
	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{})
		during = anonymousStorage(t)
	})

	require.False(t, test.Failed(), test.Failures())
	require.Equal(t, before+1, during, "the storage is mapped once and its descriptor closed")
	require.Equal(t, before, anonymousStorage(t))
}

// Test_NewDataplane_KeepsNamedStorage verifies that storage the
// environment asks to keep is a new private file in its directory, named
// in the test log and left there with the dataplane's layout.
func Test_NewDataplane_KeepsNamedStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(nativetest.KeepEnv, dir)

	test := record(t, func(t testing.TB) {
		nativetest.NewDataplane(t, nativetest.Config{CPMemory: uint64(8 * datasize.MB), DPMemory: uint64(datasize.MB)})
	})

	require.False(t, test.Failed(), test.Failures())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(dir, entries[0].Name())
	require.Contains(t, test.Logs(), path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Equal(t, int64(9*datasize.MB), info.Size())
	storage, err := zone.Attach(path)
	require.NoError(t, err)
	require.Len(t, storage.Instances, 1)
	require.NoError(t, storage.Close())
}
