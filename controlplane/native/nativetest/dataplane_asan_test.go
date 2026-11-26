//go:build linux && (amd64 || arm64) && asan

package nativetest_test

import (
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_Dataplane_Round_SanitizerReportFailsTest verifies that a sanitizer
// report of the helper halts it, whatever the inherited options say, and
// fails the request and the test with an abnormal end and the whole report.
//
// The reply timeout is shorter than symbolizing a report takes, so the
// report ends whole only if the client waits for a helper writing one;
// the case of inherited options skips symbolizing to stay quick.
func Test_Dataplane_Round_SanitizerReportFailsTest(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
	}{
		{name: "default options"},
		{
			name: "inherited options that let a report pass",
			options: map[string]string{
				"ASAN_OPTIONS":  "exitcode=0:detect_leaks=0:halt_on_error=0:symbolize=0",
				"UBSAN_OPTIONS": "exitcode=0:halt_on_error=0",
				"LSAN_OPTIONS":  "exitcode=0:detect_leaks=0",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.options {
				t.Setenv(name, value)
			}
			var roundErr error
			test := record(t, func(t testing.TB) {
				dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultSanitizer, ReplyTimeout: time.Second})
				_, roundErr = dataplane.Round(0, 0)
			})

			requireSanitizerReport(t, roundErr, "ERROR: AddressSanitizer")
			require.True(t, test.Failed())
		})
	}
}

// Test_Dataplane_Round_ReadOfFreedBlockFailsTest verifies that a round
// reading a control-plane block this process freed halts the helper with
// a sanitizer report: a publication whose delivery never reaches the
// workers retires the generation they still run.
func Test_Dataplane_Round_ReadOfFreedBlockFailsTest(t *testing.T) {
	var roundErr error
	test := record(t, func(t testing.TB) {
		dataplane, owner := newDataplane(t)
		publishPipeline(t, dataplane, nil, owner)
		undelivered := cpconfig.WithDelivery(func(*layout.DpConfig, *layout.CpConfigGen) {})
		require.NoError(t, cpconfig.UpdatePipelines(dataplane.Instance(), []cpconfig.PipelineConfig{
			{Name: "input"},
			{Name: "output"},
		}, undelivered))
		_, roundErr = dataplane.Round(0, 0)
	})

	requireSanitizerReport(t, roundErr, "ERROR: AddressSanitizer: use-after-poison")
	require.True(t, test.Failed())
}

// Test_NewDataplane_KeepsRedZonesOfZoneBlocks verifies that a block this
// process takes from the control-plane zone's allocator after mapping the
// storage is addressable while its red zones are not.
func Test_NewDataplane_KeepsRedZonesOfZoneBlocks(t *testing.T) {
	dataplane := nativetest.NewDataplane(t, nativetest.Config{})
	context := &dataplane.Instance().Cp.MemoryContext
	block := shmalloc.ContextAlloc(context, 100)
	require.NotNil(t, block)
	t.Cleanup(func() { shmalloc.ContextFree(context, block, 100) })

	require.False(t, nativetest.Unaddressable(block), "the block")
	require.True(t, nativetest.Unaddressable(unsafe.Add(block, -1)), "the leading red zone")
	require.True(t, nativetest.Unaddressable(unsafe.Add(block, 100)), "the trailing red zone")
}

// Test_Dataplane_Deliver_ReadPastGenerationFailsTest verifies that the
// dataplane reading past the end of a generation this process allocated
// halts the helper with a sanitizer report: the red zones of this
// process's blocks reach the helper.
func Test_Dataplane_Deliver_ReadPastGenerationFailsTest(t *testing.T) {
	var roundErr error
	test := record(t, func(t testing.TB) {
		dataplane := nativetest.NewDataplane(t, nativetest.Config{Fault: helperproto.FaultReadPastGeneration})
		require.NoError(t, cpconfig.UpdatePipelines(dataplane.Instance(), []cpconfig.PipelineConfig{
			{Name: "input"},
		}, cpconfig.WithDelivery(dataplane.Deliver)))
		_, roundErr = dataplane.Round(0, 0)
	})

	requireSanitizerReport(t, roundErr, "ERROR: AddressSanitizer: use-after-poison")
	require.True(t, test.Failed())
}

// requireSanitizerReport checks that a request failed with a helper that
// ended abnormally, not by the kill, after writing a whole report that
// opens with the header.
func requireSanitizerReport(t *testing.T, err error, header string) {
	t.Helper()
	failure := helperFailure(t, err)
	require.NotNil(t, failure.State, "the helper ended")
	require.False(t, failure.State.Success())
	require.NotEqual(t, syscall.SIGKILL, signalOf(failure.State), "the report ends whole")
	require.Contains(t, failure.Stderr, header)
	require.Contains(t, failure.Stderr, "SUMMARY: AddressSanitizer")
}
