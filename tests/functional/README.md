# YANET Functional Testing Framework

`tests/functional/framework` provides the QEMU, serial-console, packet-socket,
and snapshot primitives used by the functional test packages. The preferred
test lifecycle is `SetupHarness`: it prepares a cached baseline template,
starts a reusable VM pool, and restores a clean booted VM for each test.

This document covers framework tests. For an interactive reusable VM, custom
manifests, or built-in packet scenarios, see [`docs/lab.md`](../../docs/lab.md).

## Requirements

- Go version required by the repository.
- QEMU with `qemu-system-x86_64` and `qemu-img`.
- `hdiutil` on macOS or `mkisofs` on Linux for the cloud-init image.
- A prepared functional-test image and Linux x86_64 guest artifacts.
- YANET dataplane, controlplane, operator, and CLI binaries built before the
  VM starts.

The guest is Linux x86_64. A macOS host uses QEMU TCG because KVM is not
available; TCG is slower and may need a larger `YANET_VM_READY_TIMEOUT`.
Native macOS Mach-O binaries cannot run inside the guest, so build guest
artifacts in a Linux x86_64 environment.

Prepare the image and artifacts from the repository root:

```bash
make all
make -C tests/functional prepare-vm
make -C tests/functional test
```

`make -C tests/functional check-deps` checks the host tools. The test target
runs `go test -count=1 -v ./main/...` after preparing the image.

## Minimal Test Package

Use one `Harness` for the package. `SetupHarness` owns preparation and starts
the pool; the caller must call `Harness.Shutdown` and the returned cleanup
function when the package finishes.

```go
package functional

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

var harness *framework.Harness

func TestMain(m *testing.M) {
	h, cleanup, err := framework.SetupHarness(framework.HarnessConfig{
		PoolName:    "example",
		BaselineTag: "example",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up harness: %v\n", err)
		os.Exit(1)
	}
	harness = h
	code := m.Run()
	if err := h.Shutdown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to shut down harness: %v\n", err)
		code = 1
	}
	cleanup()
	os.Exit(code)
}

func TestExample(t *testing.T) {
	harness.WithBootedVM(t, func(fw *framework.TestFramework) {
		fw.Run("guest-command", func(fw *framework.TestFramework, t *testing.T) {
			output, err := fw.ExecuteCommand("uname -s")
			require.NoError(t, err)
			require.Contains(t, output, "Linux")
		})
	})
}
```

`Harness.WithBootedVM` acquires a pool slot, restores the booted snapshot,
runs the callback, and releases the slot through `t.Cleanup`. It does not start
one new VM for each test. Set `YANET_VM_POOL_SIZE` to increase the number of
long-lived slots; the default is `1`.

The reference package is
[`tests/functional/main/framework_test.go`](main/framework_test.go). It shows
how to add package-specific `Dataplane` options and readiness hooks.

## Harness Configuration

`framework.SetupHarness(framework.HarnessConfig{...})` accepts:

- `PoolName`: name used for VM instances and logs.
- `PoolSize`: number of VM slots; zero uses `YANET_VM_POOL_SIZE`.
- `BaselineTag`: cache namespace for the baseline snapshot. It is required for
  custom YAML, hooks, or extra fingerprint files.
- `QEMUImage`: image path; empty uses `YANET_QEMU_IMAGE` or the standard test
  image under `tests/functional`.
- `ProjectRoot`: canonical repository root; empty uses the current project.
- `Dataplane`, `Controlplane`, `Forward`, `Route`: custom YAML strings. Empty
  values use the framework defaults.
- `Prepare`, `AfterStart`, `ProfileReady`: hooks for custom baseline setup and
  readiness checks.
- `FingerprintFiles`: extra files included in baseline cache invalidation.
- `SkipCommonConfig`: omit the standard kni0, forwarding, route, and pipeline
  setup when the test supplies its own setup.
- `EnableSSHForward`: reserve a host port forwarding to guest SSH for manual
  debugging.
- `ForceStop`: stop VMs even when `YANET_KEEP_VM_ALIVE` is set.

The baseline cache is fingerprinted from the image, generated YAML, binaries,
plugins, and configured extra files. Use a distinct `BaselineTag` when two
packages intentionally need different captured guest state.

## Test Isolation

`ForTest(t)` creates a test-scoped framework with the full test name and logger.
`Harness.WithBootedVM` already returns such a scope for the acquired VM.

`fw.Run(name, fn)` creates a Go subtest and resets packet-socket connections
before calling `fn`. Use it for subtests that intentionally build on the state
left by earlier subtests while keeping each socket stream clean.

`fw.RunWith(snapshot, name, fn)` restores a named VM snapshot, reconnects the
serial console and sockets, then runs the subtest. Use it when a subtest needs
the exact state captured by that snapshot. `fw.RestoreBooted()` is the direct
restore primitive for the booted template; `Harness.Restore` also provides a
baseline fast path and a pre-YANET fallback for non-test callers.

The framework unmounts 9P shares before snapshot operations and remounts them
afterward. Baseline templates copy YANET files to guest tmpfs so running
processes do not hold 9P files open during `loadvm`.

## Guest Paths and Commands

`fw.Paths` describes paths inside the guest:

- `DefaultGuestPaths` uses 9P mounts such as `/mnt/target/release`,
  `/mnt/build`, `/mnt/config`, and `/mnt/logs`.
- `LocalGuestPaths` uses guest tmpfs paths such as `/tmp/yanet/cli`,
  `/tmp/yanet/build`, `/tmp/yanet/config`, and `/tmp/yanet/logs`.

Use `fw.Paths.CLI("yanet-cli-route")` instead of hard-coding a guest CLI path.
The framework exposes:

- `ExecuteCommand(command)` for the default serial command timeout.
- `ExecuteCommandWithTimeout(command, timeout)` for a bounded custom timeout.
- `ExecuteCommands(commands...)` for fail-fast sequential commands.
- `ExecuteCommandsSeparately(commands...)` when every command result is needed.
- `ResetConnections()` to close stale packet sockets after a snapshot restore.

Commands are executed in the guest through the serial console. They are shell
commands inside the guest; host-side arguments should be quoted or passed
through the manifest `argv` rules instead of interpolating untrusted input.

## Packet APIs

The default topology exposes packet interfaces `0` and `1`. Socket clients are
created lazily by `GetSocketClient(index)` and are reset between `Run` calls.

- `SendPacketAndCapture` sends one raw packet and captures one response packet.
- `SendPacketAndCaptureAll` captures all matching response packets until the
  timeout.
- `SendPacketAndCaptureAllUnfiltered` captures every packet observed on the
  selected egress, including packets that are not matched as responses.
- `SendPacketAndParse` returns parsed input and output `PacketInfo` values.
- `SendPacketAndParseAll` and `SendPacketsAndParseAll` parse multiple outputs.

Packet helpers in `packet_builder.go` and the DSL in `packet_dsl.go` build
Ethernet/IP/TCP/UDP fixtures. Use `cmp_options.go` when comparing packet
structures and `packet_parser.go` when raw bytes need structured inspection.

Example packet assertion:

The example assumes the test package imports `time` and defines the packet
fixture and expected bytes.

```go
fw.Run("forwards-udp", func(fw *framework.TestFramework, t *testing.T) {
	input := buildPacket()
	output, err := fw.SendPacketAndCapture(0, 0, input, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, expectedPacket, output)
})
```

Use `SendPacketAndCaptureAllUnfiltered` when the assertion is about a drop or
when unrelated packets on the egress must be observed rather than filtered.

## Snapshots and Custom Setup

`SetupHarness` captures a reusable `baseline` template after its prepare and
configuration hooks complete. For a custom package:

1. Set a unique `BaselineTag`.
2. Provide custom YAML or hooks in `HarnessConfig`.
3. Use `WithBootedVM` for tests that can start from the captured template.
4. Use `Restore` when the package needs the baseline fast path and fallback.
5. Use `PrepareLocalStorage`, `SaveSnapshot`, and `RestoreAndReconnect` only
   when the test owns an additional snapshot lifecycle.

`SaveSnapshot` captures full VM state. 9P shares must be unmounted before a
manual save; the framework handles this for its standard snapshot helpers.

## Debugging

Environment variables:

- `YANET_QEMU_IMAGE` overrides the base image path.
- `YANET_VM_POOL_SIZE` sets the number of pool slots.
- `YANET_VM_READY_TIMEOUT` overrides readiness timeout with a Go duration such
  as `5m`.
- `YANET_TEST_DEBUG=1` enables verbose framework logging and packet dumps.
- `YANET_KEEP_VM_ALIVE=1` keeps QEMU running after tests for investigation and
  also enables debug logging; `ForceStop` overrides it for a harness.

Use the Makefile targets from `tests/functional`:

```bash
make check-deps
make prepare-vm
make test-run TEST=TestFramework
make debug-vm
make clean
```

The standard test run uses serial access and does not require SSH. SSH host
forwarding is optional and is enabled only when `EnableSSHForward` or the
keep-alive debugging mode requests it.

When debugging a failed run, preserve artifacts with `YANET_TEST_DEBUG=1` and
inspect the QEMU working directory, serial log, QEMU log, and packet dumps.
Use `docs/lab.md` for the reusable Lab supervisor's `report`, `exec`, `shell`,
and `serial` commands.

## Layout

```text
tests/functional/
├── framework/       QEMU, harness, pool, serial, socket, packet, and snapshot APIs
├── main/             primary functional test package and TestMain reference
├── testdata/         YAML and packet fixtures
├── Makefile          image, test, debug, and cleanup targets
└── README.md         this document
```

Related code references:

- `tests/functional/framework/harness.go` - `HarnessConfig`, setup, pool, and
  restore lifecycle.
- `tests/functional/framework/framework.go` - test scope, command, packet, and
  snapshot methods.
- `tests/functional/framework/pool.go` - pool sizing and template startup.
- `tests/functional/main/framework_test.go` - package-level TestMain example.
