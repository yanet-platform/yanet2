package functional

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"go.uber.org/zap"
)

// Global VM pool used for test isolation. Works for any pool size >= 1.
var globalPool *framework.VMPool

func dataplaneConfig() string {
	return `
dataplane:
  storage: /dev/hugepages/yanet
  dpdk_memory: 128
  loglevel: trace
  instances:
    - dp_memory: 100663296
      cp_memory: 134217728
      numa_id: 0
  devices:
    - port_name: 01:00.0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
          num_mbufs: 2048
    - port_name: virtio_user_kni0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
          num_mbufs: 2048
  connections:
    - src_device_id: 0
      dst_device_id: 1
    - src_device_id: 1
      dst_device_id: 0
`
}

func controlplaneConfig() string {
	return `
logging:
  level: debug

gateway:
  server:
    endpoint: "0.0.0.0:8080"
  auth:
    disabled: true

modules:
  route:
    link_map:
      kni0: 01:00.0
    memory_requirements: 8MB
  route-mpls:
    memory_requirements: 8MB
  decap:
    memory_requirements: 8MB
  dscp:
    memory_requirements: 8MB
  forward:
    memory_requirements: 8MB
  nat64:
    memory_requirements: 8MB
  pdump:
    memory_requirements: 8MB
  acl:
    memory_requirements: 16MB

devices:
  plain:
    memory_requirements: 8MB
  vlan:
    memory_requirements: 8MB
`
}

func forwardConfig() string {
	return `
rules:
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - ` + framework.VMIPv4Host + `/32
      - ` + framework.VMIPv6Host + `/64
      - "ff02::0/16"
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_pass
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - "0.0.0.0/0"
      - "::/0"
    mode: None
    devices:
      - 01:00.0
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - virtio_user_kni0
`
}

func route0Config() string {
	return `
entries:
  - prefix: "0.0.0.0/0"
    nexthops:
      - dst_mac: "` + framework.SrcMAC + `"
        src_mac: "` + framework.DstMAC + `"
        device: "01:00.0"
  - prefix: "::/0"
    nexthops:
      - dst_mac: "` + framework.SrcMAC + `"
        src_mac: "` + framework.DstMAC + `"
        device: "01:00.0"
`
}

func dumpMemoryDiagnostics(fw *framework.TestFramework, log *zap.SugaredLogger) {
	diagCmds := []string{
		"echo '=== HUGEPAGES ===' && cat /proc/meminfo | grep -i huge",
		"echo '=== FREE ===' && free -h",
		"echo '=== PROCESS MEMORY ===' && ps aux | grep yanet",
		"echo '=== HUGEPAGE FILE ===' && ls -lh /dev/hugepages/yanet",
		"echo '=== DATAPLANE LOG ===' && cat /tmp/yanet/logs/yanet-dataplane.log",
		"echo '=== CONTROLPLANE LOG ===' && cat /tmp/yanet/logs/yanet-controlplane.log",
	}
	outputs, err := fw.ExecuteCommands(diagCmds...)
	if err != nil {
		log.Errorf("MEMORY DIAG: error collecting diagnostics: %v", err)
		return
	}
	for i, cmd := range diagCmds {
		log.Infof("MEMORY DIAG: %s\n%s\n---", cmd, outputs[i])
	}
}

// Baseline configs stored at package level so RestartYANET and the
// preyanet fallback in restoreBooted can access them.
var (
	baselineDP = dataplaneConfig()
	baselineCP = controlplaneConfig()
)

func configureBaseline(fw *framework.TestFramework, log *zap.SugaredLogger) error {
	// Write config files BEFORE starting YANET so the "preyanet"
	// snapshot captures them on disk without a running dataplane.
	if err := fw.CreateForwardConfig(forwardConfig()); err != nil {
		return err
	}
	if err := fw.CreateConfigFile("route0.yaml", route0Config()); err != nil {
		return err
	}

	// Save "preyanet" snapshot -- OS booted, binaries copied to
	// /tmp/yanet/, config files written, 9P unmounted, no YANET running.
	// Used as fallback when baseline restore fails.
	if err := fw.SaveSnapshotKeepUnmounted("preyanet"); err != nil {
		return err
	}
	log.Info("Pre-yanet snapshot saved")

	// Remount 9P -- CommonConfigCommands needs /mnt/config/route0.yaml.
	if err := fw.Mount9P(); err != nil {
		return err
	}

	// Start YANET and apply runtime configurations
	if err := fw.StartYANET(baselineDP, baselineCP); err != nil {
		return err
	}

	dumpMemoryDiagnostics(fw, log)

	if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
		return err
	}

	return nil
}

func saveBaselineSnapshot(fw *framework.TestFramework, log *zap.SugaredLogger) error {
	if err := fw.SaveSnapshotKeepUnmounted("baseline"); err != nil {
		return err
	}

	log.Info("Baseline snapshot saved successfully")
	return nil
}

func ensureBaselineTemplate(qemuImage string, bootedTemplate string, baselineTemplate string, log *zap.SugaredLogger) error {
	if framework.OverlayHasSnapshot(baselineTemplate, "baseline") {
		log.Infof("Using cached baseline template: %s", baselineTemplate)
		return nil
	}

	log.Infof("Baseline template %s not found; bootstrapping from booted template", baselineTemplate)

	prepPool, err := framework.NewVMPool(1, "baseline-prep", qemuImage, bootedTemplate, "", "", log)
	if err != nil {
		return fmt.Errorf("create baseline prep pool: %w", err)
	}
	defer func() {
		if err := prepPool.Shutdown(); err != nil {
			log.Errorf("Failed to shut down baseline prep pool: %v", err)
		}
	}()

	if err := prepPool.StartAll(); err != nil {
		return fmt.Errorf("start baseline prep pool: %w", err)
	}
	if err := prepPool.WaitAllReady(120 * time.Second); err != nil {
		return fmt.Errorf("baseline prep pool not ready: %w", err)
	}

	prepFW := prepPool.Acquire()
	defer prepPool.Release(prepFW)

	if err := prepFW.PrepareLocalStorage(); err != nil {
		return fmt.Errorf("prepare local storage: %w", err)
	}
	if err := configureBaseline(prepFW, log); err != nil {
		return fmt.Errorf("configure baseline: %w", err)
	}
	if err := saveBaselineSnapshot(prepFW, log); err != nil {
		return fmt.Errorf("save baseline snapshot: %w", err)
	}
	if err := prepFW.ExportCurrentOverlay(baselineTemplate); err != nil {
		return fmt.Errorf("export baseline template: %w", err)
	}
	if !framework.OverlayHasSnapshot(baselineTemplate, "baseline") {
		return fmt.Errorf("exported baseline template %s is missing snapshot %q", baselineTemplate, "baseline")
	}

	log.Infof("Baseline template cached at %s", baselineTemplate)
	return nil
}

// withBootedVM acquires a VM from the pool and restores it to a working
// YANET state. See restoreBooted for the restore strategy.
func withBootedVM(t *testing.T, fn func(fw *framework.TestFramework)) {
	t.Helper()
	if globalPool == nil {
		t.Fatal("VM pool is not initialized")
	}
	base := globalPool.Acquire()
	t.Cleanup(func() {
		globalPool.Release(base)
	})
	fw := base.ForTest(t)
	restoreBooted(t, fw)
	fn(fw)
}

// bootedRunner runs subtests each in their own isolated booted restore.
type bootedRunner struct {
	t *testing.T
}

// newBootedRunner creates a runner where each RunBooted call gets a fresh
// booted restore: acquire → RestoreBooted → run → release.
//
// Use this when each subtest must start from a clean state.
func newBootedRunner(t *testing.T) *bootedRunner {
	t.Helper()
	if globalPool == nil {
		t.Fatal("VM pool is not initialized")
	}
	return &bootedRunner{t: t}
}

// RunBooted acquires a VM slot, restores it to the booted snapshot, runs
// the named subtest, then releases the slot back to the pool.
func (r *bootedRunner) RunBooted(name string, fn func(fw *framework.TestFramework, t *testing.T)) bool {
	return r.t.Run(name, func(t *testing.T) {
		base := globalPool.Acquire()
		t.Cleanup(func() {
			globalPool.Release(base)
		})
		fw := base.ForTest(t)
		restoreBooted(t, fw)
		fn(fw, t)
	})
}

// testFramework is kept for backward compatibility. New tests should use
// withBootedVM or newBootedRunner instead.
func testFramework(t *testing.T) *framework.TestFramework {
	t.Helper()
	if globalPool == nil {
		t.Fatal("test pool is not initialized")
	}
	base := globalPool.Acquire()
	t.Cleanup(func() {
		globalPool.Release(base)
	})
	fw := base.ForTest(t)
	restoreBooted(t, fw)
	return fw
}

// restoreBooted restores the VM to a working YANET state. It tries the
// fast path (baseline snapshot with YANET already running) first, and
// falls back to the slow path (preyanet snapshot + fresh StartYANET)
// only when baseline restore fails.
//
// Fast path (~3-5s): loadvm to "baseline" (YANET running, configured).
//
//	Requires connection reset to clear stale host-side sockets.
//
// Slow path (~20-50s): loadvm to "preyanet" (no YANET), StartYANET from
//
//	scratch, configure. Used when DPDK device state is genuinely broken
//	after loadvm and the heartbeat cannot succeed.
func restoreBooted(t *testing.T, fw *framework.TestFramework) {
	t.Helper()

	if err := fw.RestoreAndReconnect("baseline"); err == nil {
		return
	}

	t.Logf("baseline restore failed, falling back to preyanet + fresh StartYANET")

	if err := fw.RestoreClean("preyanet"); err != nil {
		t.Fatalf("failed to restore VM to preyanet: %v", err)
	}
	if err := fw.StartYANET(baselineDP, baselineCP); err != nil {
		t.Fatalf("failed to start YANET: %v", err)
	}
	if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
		t.Fatalf("failed to configure YANET: %v", err)
	}

	fw.ResetConnections()

	const dpTimeout = 15 * time.Second
	if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
		t.Logf("dataplane not ready after %v, restarting YANET...", dpTimeout)
		if restartErr := fw.RestartYANET(); restartErr != nil {
			t.Fatalf("YANET restart failed: %v", restartErr)
		}
		fw.ResetConnections()
		if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
			t.Fatalf("dataplane not ready after preyanet restore + restart: %v", err)
		}
	}
}

// TestMain is the entry point for running tests in this package.
// It wraps the standard testing.M.Run() with additional setup/teardown logic
// via testMainWrapper. The exit code from testMainWrapper is passed to os.Exit.
func TestMain(m *testing.M) {
	os.Exit(testMainWrapper(m))
}

// testMainWrapper is a test framework wrapper function that:
// 1. Initializes logging based on YANET_TEST_DEBUG environment variable
// 2. Creates and configures test framework with QEMU image
// 3. Starts YANET with predefined dataplane and controlplane configurations
// 4. Executes common configuration commands
// 5. Runs all tests via testing.M.Run()
//
// The function handles framework lifecycle:
// - Starts framework and QEMU VM
// - Waits for VM readiness
// - Ensures proper cleanup on exit
// - Returns test execution status code
//
// Parameters:
//   - m: testing.M instance for running tests
//
// Returns:
//   - int: Test execution result code
func testMainWrapper(m *testing.M) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "testMainWrapper recovered panic: %v\n", r)
			code = 1
		}
	}()

	// Create logger for detailed logging
	lg := zap.NewDevelopmentConfig()
	if !framework.IsDebugEnabled() {
		// no env - set error level
		lg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	} else {
		// save debug log to test.log
		lg.OutputPaths = []string{"test.log"}
		lg.ErrorOutputPaths = []string{"stderr", "test.log"}
	}
	logger, err := lg.Build()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	// Get QEMU image path (relative to parent functional directory)
	qemuImage := os.Getenv("YANET_QEMU_IMAGE")
	if qemuImage == "" {
		qemuImage = "../yanet-test.qcow2"
	}
	// Booted template is stored alongside the base image.
	// It is created by 'make prepare-vm'; if missing it is bootstrapped at runtime.
	bootedTemplate := framework.BootedImagePath(qemuImage)
	baselineTemplate := framework.BaselineImagePath(qemuImage)

	if err := ensureBaselineTemplate(qemuImage, bootedTemplate, baselineTemplate, sugar); err != nil {
		sugar.Errorf("Failed to prepare baseline template: %v", err)
		return 1
	}
	framework.MarkBaselineSaved()

	sugar.Infof("Starting VM pool with size %d (baseline template: %s)", framework.PoolSize(), baselineTemplate)

	pool, err := framework.NewVMPool(framework.PoolSize(), "main", qemuImage, bootedTemplate, baselineTemplate, "baseline", sugar)
	if err != nil {
		sugar.Errorf("Failed to create VM pool: %v", err)
		return 1
	}
	globalPool = pool

	defer func() {
		if globalPool != nil {
			if err := globalPool.Shutdown(); err != nil {
				sugar.Errorf("Failed to shut down VM pool: %v", err)
				code = 12
			}
			globalPool = nil
		}
	}()

	if err := pool.StartAll(); err != nil {
		sugar.Errorf("Failed to start VM pool: %v", err)
		return 1
	}

	if err := pool.WaitAllReady(120 * time.Second); err != nil {
		sugar.Errorf("Failed to wait for VM pool readiness: %v", err)
		return 1
	}

	// Pause all VM CPUs now that baseline snapshots are saved.
	// Idle VMs would otherwise keep DPDK's busy-poll loop running and
	// consume host CPU, starving the active VM's packet processing.
	// Each VM resumes automatically when RestoreSnapshot calls loadvm+cont.
	pool.StopAllCPU()

	// Run tests
	code = m.Run()
	return code
}

// TestFramework - comprehensive test for checking all yanet functionality
func TestFramework(t *testing.T) {
	t.Parallel()
	withBootedVM(t, func(fw *framework.TestFramework) {
		testFrameworkSuite(t, fw)
	})
}

func testFrameworkSuite(t *testing.T, fw *framework.TestFramework) {

	// Test 1: Check basic command execution
	fw.Run("Basic_Commands", func(fw *framework.TestFramework, t *testing.T) {
		// Check basic system commands
		basicCommands := []struct {
			name    string
			command string
			check   func(string) bool
		}{
			{
				name:    "whoami",
				command: "whoami",
				check:   func(output string) bool { return strings.Contains(output, "root") },
			},
			{
				name:    "pwd",
				command: "pwd",
				check:   func(output string) bool { return strings.Contains(output, "/root") },
			},
			{
				name:    "date",
				command: "date",
				check:   func(output string) bool { return len(strings.TrimSpace(output)) > 10 },
			},
			{
				name:    "uname",
				command: "uname -a",
				check:   func(output string) bool { return strings.Contains(strings.ToLower(output), "linux") },
			},
			{
				name:    "memory_info",
				command: "cat /proc/meminfo | head -5",
				check:   func(output string) bool { return strings.Contains(output, "MemTotal") },
			},
		}

		for _, cmd := range basicCommands {
			fw.Run(cmd.name, func(fw *framework.TestFramework, t *testing.T) {
				output, err := fw.ExecuteCommand(cmd.command)
				require.NoError(t, err, "Command %s failed", cmd.command)
				require.True(t, cmd.check(output), "Command %s output validation failed: %s", cmd.command, output)
			})
		}
	})

	// Test 3: Check filesystem and mounting
	fw.Run("Filesystem_Check", func(fw *framework.TestFramework, t *testing.T) {
		// Check main directories
		directories := []string{
			"/mnt/logs",
			"/mnt/config",
			"/mnt/build",
			"/mnt/target",
		}

		for _, dir := range directories {
			fw.Run("check_"+strings.ReplaceAll(dir, "/", "_"), func(fw *framework.TestFramework, t *testing.T) {
				_, err := fw.ExecuteCommand("test -d " + dir)
				require.NoError(t, err, "Directory %s does not exist", dir)

				output, err := fw.ExecuteCommand("mount | grep " + dir)
				require.NoError(t, err, "Failed to check mount point %s", dir)
				require.NotEmpty(t, output, "Mount point %s not found", dir)
			})
		}
	})

	// Test 4: Check YANET binaries availability
	fw.Run("YANET_Binaries", func(fw *framework.TestFramework, t *testing.T) {
		// Check CLI binaries
		cliBinaries := make([]struct {
			name string
			path string
		}, 0, len(framework.CLIBinaryNames))
		for _, name := range framework.CLIBinaryNames {
			cliBinaries = append(cliBinaries, struct {
				name string
				path string
			}{name, "/mnt/target/release/" + name})
		}

		for _, binary := range cliBinaries {
			fw.Run(binary.name, func(fw *framework.TestFramework, t *testing.T) {
				_, err := fw.ExecuteCommand("test -e " + binary.path)
				require.NoError(t, err, "Binary %s not found at %s", binary.name, binary.path)

				_, err = fw.ExecuteCommand("test -x " + binary.path)
				require.NoError(t, err, "Binary %s not executable", binary.name)

				helpOutput, helpErr := fw.ExecuteCommand(binary.path + " --version")
				require.NoError(t, helpErr, "Binary %s --version failed: %v", binary.name, helpErr)
				require.NotEmpty(t, helpOutput, "Binary %s --version returned empty output", binary.name)
			})
		}

		// Check main YANET components
		fw.Run("yanet_components", func(fw *framework.TestFramework, t *testing.T) {
			components := []string{
				"/mnt/build/dataplane/yanet-dataplane",
				"/mnt/build/controlplane/yanet-controlplane",
			}

			for _, component := range components {
				_, err := fw.ExecuteCommand("test -e " + component)
				require.NoError(t, err, "Component %s not found", component)

				_, err = fw.ExecuteCommand("test -x " + component)
				require.NoError(t, err, "Component %s not executable", component)
			}
		})
	})

	// Test 5: Check network interfaces and socket devices
	fw.Run("Network_Interfaces", func(fw *framework.TestFramework, t *testing.T) {
		// Check network interfaces
		output, err := fw.ExecuteCommand("ip link show")
		require.NoError(t, err)
		require.Contains(t, output, "lo", "Loopback interface should be present")

		// Check framework socket clients
		fw.Run("socket_clients", func(fw *framework.TestFramework, t *testing.T) {
			socketPaths := fw.GetSocketPaths()
			for i := range 2 {
				// Check if socket path exists
				socketPath := socketPaths[i]

				// Check if socket file exists
				_, err := os.Stat(socketPath)
				os.IsNotExist(err)
				require.NoError(t, err, "Failed to check socket file %s", socketPath)

				client, err := fw.GetSocketClient(i)
				require.NoError(t, err, "Failed to get socket client %d", i)
				require.NotNil(t, client, "Socket client %d should not be nil", i)
			}
		})
	})

	// Test 6: Check PacketParser
	fw.Run("PacketParser", func(fw *framework.TestFramework, t *testing.T) {
		require.NotNil(t, fw.PacketParser, "PacketParser should be initialized")

		// Create simple test packet
		testPacket := []byte{
			// Ethernet header (14 bytes)
			0x52, 0x54, 0x00, 0x11, 0x00, 0x01, // dst MAC
			0x52, 0x54, 0x00, 0x11, 0x00, 0x02, // src MAC
			0x08, 0x00, // EtherType IPv4
			// IPv4 header (20 bytes minimum)
			0x45, 0x00, 0x00, 0x1c, // version, IHL, TOS, length
			0x00, 0x01, 0x40, 0x00, // ID, flags, fragment offset
			0x40, 0x01, 0x00, 0x00, // TTL, protocol (ICMP), checksum
			0xc0, 0xa8, 0x01, 0x01, // source IP (192.168.1.1)
			0xc0, 0xa8, 0x01, 0x02, // dest IP (192.168.1.2)
		}

		// Pad to minimum Ethernet frame size
		if len(testPacket) < 60 {
			padding := make([]byte, 60-len(testPacket))
			testPacket = append(testPacket, padding...)
		}

		packetInfo, err := fw.PacketParser.ParsePacket(testPacket)
		require.NoError(t, err, "Failed to parse test packet")
		require.NotNil(t, packetInfo, "PacketInfo should not be nil")
		require.True(t, packetInfo.IsIPv4, "Packet should be IPv4")
		require.Equal(t, "192.168.1.1", packetInfo.SrcIP.String())
		require.Equal(t, "192.168.1.2", packetInfo.DstIP.String())
	})

	// Test 7: Check system resources
	fw.Run("System_Resources", func(fw *framework.TestFramework, t *testing.T) {
		// Check memory
		fw.Run("memory", func(fw *framework.TestFramework, t *testing.T) {
			output, err := fw.ExecuteCommand("free -h")
			require.NoError(t, err)
			require.Contains(t, output, "Mem:", "Memory information should be available")
		})

		// Check CPU
		fw.Run("cpu", func(fw *framework.TestFramework, t *testing.T) {
			output, err := fw.ExecuteCommand("nproc")
			require.NoError(t, err)
			require.NotEmpty(t, strings.TrimSpace(output), "CPU count should be available")
		})

		// Check hugepages (important for DPDK)
		fw.Run("hugepages", func(fw *framework.TestFramework, t *testing.T) {
			output, err := fw.ExecuteCommand("cat /proc/meminfo | grep -i huge")
			require.NoErrorf(t, err, "Failed to get hugepages info: %s", output)
		})
	})
}
