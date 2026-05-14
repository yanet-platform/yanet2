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
  balancer:
    memory_requirements: 16MB
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

func dumpMemoryDiagnostics(fw *framework.F, log *zap.SugaredLogger) {
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

func configureBaseline(fw *framework.F, log *zap.SugaredLogger) error {
	if err := fw.StartYANET(dataplaneConfig(), controlplaneConfig()); err != nil {
		return err
	}

	dumpMemoryDiagnostics(fw, log)

	// Write forward.yaml to the path that CommonConfigCommands will reference.
	// In 9P mode this is /mnt/config/forward.yaml (via host filesystem).
	// In local mode this is /tmp/yanet/forward.yaml (via serial console).
	if err := fw.CreateForwardConfig(forwardConfig()); err != nil {
		return err
	}

	if err := fw.CreateConfigFile("route0.yaml", route0Config()); err != nil {
		return err
	}

	if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
		return err
	}

	return nil
}

func saveBaselineSnapshot(fw *framework.F, log *zap.SugaredLogger) error {
	if err := fw.SaveSnapshotKeepUnmounted("baseline"); err != nil {
		return err
	}

	framework.MarkBaselineSaved()
	log.Info("Baseline snapshot saved successfully")
	return nil
}

// withBootedVM acquires a VM from the pool, restores it to the booted snapshot
// (which includes YANET running with baseline config), then calls fn with the
// test framework. All fw.Run(...) calls inside fn share the same VM session.
// The VM is released back to the pool when t finishes.
//
// Use this when subtests share state (e.g. configure → test1 → test2).
func withBootedVM(t *testing.T, fn func(fw *framework.F)) {
	t.Helper()
	if globalPool == nil {
		t.Fatal("VM pool is not initialized")
	}
	base := globalPool.Acquire()
	t.Cleanup(func() {
		globalPool.Release(base)
	})
	fw := base.ForTest(t)
	if err := fw.RestoreAndReconnect("baseline"); err != nil {
		t.Fatalf("failed to restore VM to baseline: %v", err)
	}
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
func (r *bootedRunner) RunBooted(name string, fn func(fw *framework.F, t *testing.T)) bool {
	return r.t.Run(name, func(t *testing.T) {
		base := globalPool.Acquire()
		t.Cleanup(func() {
			globalPool.Release(base)
		})
		fw := base.ForTest(t)
		if err := fw.RestoreAndReconnect("baseline"); err != nil {
			t.Fatalf("failed to restore VM to baseline for subtest %q: %v", name, err)
		}
		fn(fw, t)
	})
}

// testFramework is kept for backward compatibility. New tests should use
// withBootedVM or newBootedRunner instead.
func testFramework(t *testing.T) *framework.F {
	t.Helper()
	if globalPool == nil {
		t.Fatal("test pool is not initialized")
	}
	base := globalPool.Acquire()
	t.Cleanup(func() {
		globalPool.Release(base)
	})
	fw := base.ForTest(t)
	if err := fw.RestoreAndReconnect("baseline"); err != nil {
		t.Fatalf("failed to restore baseline snapshot: %v", err)
	}
	return fw
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

	sugar.Infof("Starting VM pool with size %d (booted template: %s)", framework.PoolSize(), bootedTemplate)

	pool, err := framework.NewVMPool(framework.PoolSize(), "main", qemuImage, bootedTemplate, sugar)
	if err != nil {
		sugar.Errorf("Failed to create VM pool: %v", err)
		return 1
	}
	globalPool = pool

	if err := pool.StartAll(); err != nil {
		sugar.Errorf("Failed to start VM pool: %v", err)
		return 1
	}

	defer func() {
		if globalPool != nil {
			if err := globalPool.Shutdown(); err != nil {
				sugar.Errorf("Failed to shut down VM pool: %v", err)
				code = 12
			}
		}
	}()

	if err := pool.WaitAllReady(120 * time.Second); err != nil {
		sugar.Errorf("Failed to wait for VM pool readiness: %v", err)
		return 1
	}

	if err := pool.ForEachParallel(func(idx int, fw *framework.F) error {
		// Copy YANET binaries from 9P mounts to guest tmpfs so that
		// no YANET process holds open fids on 9P. This makes savevm work.
		if err := fw.PrepareLocalStorage(); err != nil {
			return fmt.Errorf("vm %d local storage prep failed: %w", idx, err)
		}
		if err := configureBaseline(fw, sugar); err != nil {
			return fmt.Errorf("vm %d baseline config failed: %w", idx, err)
		}
		if err := saveBaselineSnapshot(fw, sugar); err != nil {
			return fmt.Errorf("vm %d baseline snapshot failed: %w", idx, err)
		}
		return nil
	}); err != nil {
		sugar.Errorf("Failed to configure VM pool baseline: %v", err)
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
	withBootedVM(t, func(fw *framework.F) {
		testFrameworkSuite(t, fw)
	})
}

func testFrameworkSuite(t *testing.T, fw *framework.F) {

	// Test 1: Check basic command execution
	fw.Run("Basic_Commands", func(fw *framework.F, t *testing.T) {
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
			fw.Run(cmd.name, func(fw *framework.F, t *testing.T) {
				output, err := fw.ExecuteCommand(cmd.command)
				require.NoError(t, err, "Command %s failed", cmd.command)
				require.True(t, cmd.check(output), "Command %s output validation failed: %s", cmd.command, output)
			})
		}
	})

	// Test 3: Check filesystem and mounting
	fw.Run("Filesystem_Check", func(fw *framework.F, t *testing.T) {
		// Check main directories
		directories := []string{
			"/mnt/logs",
			"/mnt/config",
			"/mnt/build",
			"/mnt/target",
		}

		for _, dir := range directories {
			fw.Run("check_"+strings.ReplaceAll(dir, "/", "_"), func(fw *framework.F, t *testing.T) {
				output, err := fw.ExecuteCommand("ls -la " + dir)
				require.NoError(t, err, "Failed to list directory %s", dir)
				require.NotEmpty(t, output, "Directory %s appears to be empty", dir)
				require.NotContains(t, output, "such")
				output, err = fw.ExecuteCommand("mount | grep " + dir)
				require.NoError(t, err, "Failed to check mount point %s", dir)
				require.NotEmpty(t, output, "Mount point %s not found", dir)
			})
		}
	})

	// Test 4: Check YANET binaries availability
	fw.Run("YANET_Binaries", func(fw *framework.F, t *testing.T) {
		// Check CLI binaries
		cliBinaries := []struct {
			name string
			path string
		}{
			{"main_cli", "/mnt/target/release/yanet-cli"},
			{"common_cli", "/mnt/target/release/yanet-cli-common"},
			{"decap_cli", "/mnt/target/release/yanet-cli-decap"},
			{"dscp_cli", "/mnt/target/release/yanet-cli-dscp"},
			{"forward_cli", "/mnt/target/release/yanet-cli-forward"},
			{"nat64_cli", "/mnt/target/release/yanet-cli-nat64"},
			{"route_cli", "/mnt/target/release/yanet-cli-route"},
			{"pipeline_cli", "/mnt/target/release/yanet-cli-pipeline"},
			{"acl_cli", "/mnt/target/release/yanet-cli-acl"},
			{"fwstate_cli", "/mnt/target/release/yanet-cli-fwstate"},
		}

		for _, binary := range cliBinaries {
			fw.Run(binary.name, func(fw *framework.F, t *testing.T) {
				// Check file existence
				output, err := fw.ExecuteCommand("ls -la " + binary.path)
				require.NoError(t, err, "⚠️  Binary %s check failed: %v", binary.name, err)
				require.NotContainsf(t, output, "such", "⚠️  Binary %s not found: %v", binary.name)
				require.Contains(t, output, binary.path, "Binary file not found in listing")

				// Check binary help
				helpOutput, helpErr := fw.ExecuteCommand(binary.path + " --help")
				require.NoError(t, helpErr, "Binary %s help check failed: %v", binary.name, helpErr)
				require.NotEmpty(t, helpOutput, "Binary %s help check failed: %v", binary.name, helpErr)
			})
		}

		// Check main YANET components
		fw.Run("yanet_components", func(fw *framework.F, t *testing.T) {
			components := []string{
				"/mnt/build/dataplane/yanet-dataplane",
				"/mnt/build/controlplane/yanet-controlplane",
			}

			for _, component := range components {
				output, err := fw.ExecuteCommand("ls -la " + component)
				require.NoError(t, err, "Component %s not found", component)
				require.NotContains(t, output, "such")
			}
		})
	})

	// Test 5: Check network interfaces and socket devices
	fw.Run("Network_Interfaces", func(fw *framework.F, t *testing.T) {
		// Check network interfaces
		output, err := fw.ExecuteCommand("ip link show")
		require.NoError(t, err)
		require.Contains(t, output, "lo", "Loopback interface should be present")

		// Check framework socket clients
		fw.Run("socket_clients", func(fw *framework.F, t *testing.T) {
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
	fw.Run("PacketParser", func(fw *framework.F, t *testing.T) {
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
	fw.Run("System_Resources", func(fw *framework.F, t *testing.T) {
		// Check memory
		fw.Run("memory", func(fw *framework.F, t *testing.T) {
			output, err := fw.ExecuteCommand("free -h")
			require.NoError(t, err)
			require.Contains(t, output, "Mem:", "Memory information should be available")
		})

		// Check CPU
		fw.Run("cpu", func(fw *framework.F, t *testing.T) {
			output, err := fw.ExecuteCommand("nproc")
			require.NoError(t, err)
			require.NotEmpty(t, strings.TrimSpace(output), "CPU count should be available")
		})

		// Check hugepages (important for DPDK)
		fw.Run("hugepages", func(fw *framework.F, t *testing.T) {
			output, err := fw.ExecuteCommand("cat /proc/meminfo | grep -i huge")
			require.NoErrorf(t, err, "Failed to get hugepages info: %s", output)
		})
	})
}
