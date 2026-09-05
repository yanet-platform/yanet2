// Package multidev holds the functional tests that need more than one egress
// device.
//
// It boots its own VM pool rather than joining tests/functional/main: the extra
// device adds a third busy-poll worker to the guest's two cores, and baking it
// into the shared baseline would change the timing of every existing test.
package multidev

import (
	"fmt"
	"os"
	"testing"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// secondDevice is the guest's second NIC. QEMU wires it as a unix-stream
// netdev reachable at socket interface index 1, and the guest binds it to
// vfio-pci at boot, but the default dataplane configuration never declares it.
const secondDevice = "02:00.0"

// Socket interface indices, matching the QEMU netdev order: index 0 is the
// 01:00.0 port every functional test sends on, index 1 is secondDevice.
const (
	iface0 = 0
	iface1 = 1
)

// harness owns the baseline VM pool shared by every test in this package.
var harness *framework.Harness

// withBootedVM acquires a VM from the pool and restores it to a working YANET
// state, trying the baseline snapshot first and falling back to a fresh
// StartYANET only when that restore fails.
func withBootedVM(t *testing.T, fn func(fw *framework.TestFramework)) {
	t.Helper()
	if harness == nil {
		t.Fatal("VM pool is not initialized")
	}
	harness.WithBootedVM(t, fn)
}

// TestMain is the entry point for running tests in this package.
func TestMain(m *testing.M) {
	os.Exit(testMainWrapper(m))
}

// testMainWrapper builds the baseline VM pool via the framework harness, runs
// the package's tests, and tears the pool down on exit.
func testMainWrapper(m *testing.M) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "testMainWrapper recovered panic: %v\n", r)
			code = 1
		}
	}()

	// BaselineTag keeps this pool's cached baseline overlay separate from the
	// shared one, which SetupHarness requires for any custom YAML.
	h, cleanup, err := framework.SetupHarness(framework.HarnessConfig{
		PoolName:    "multidev",
		BaselineTag: "multidev",
		Dataplane: framework.DataplaneConfig(framework.DataplaneOptions{
			ExtraDevices: []string{secondDevice},
		}),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up functional-test harness: %v\n", err)
		return 1
	}
	defer cleanup()

	harness = h

	defer func() {
		if err := h.Shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shut down VM pool: %v\n", err)
			code = 12
		}
		harness = nil
	}()

	return m.Run()
}
