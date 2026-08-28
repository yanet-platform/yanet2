package functional

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// harness owns the baseline VM pool shared by every test in this package.
var harness *framework.Harness

const testPacketRecircLimit = uint16(5)

// globalPool is the baseline VM pool. It is a convenience alias for
// harness.Pool() used by the per-test isolation helpers below.
var globalPool *framework.VMPool

// withBootedVM acquires a VM from the pool and restores it to a working
// YANET state. See restoreBooted for the restore strategy.
func withBootedVM(t *testing.T, fn func(fw *framework.TestFramework)) {
	t.Helper()
	if harness == nil {
		t.Fatal("VM pool is not initialized")
	}
	harness.WithBootedVM(t, fn)
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

// restoreBooted restores the VM to a working YANET state.
//
// It tries the fast path (baseline snapshot with YANET already running)
// first, and falls back to the slow path (preyanet snapshot + fresh
// StartYANET) only when baseline restore fails.
func restoreBooted(t *testing.T, fw *framework.TestFramework) {
	t.Helper()
	harness.RestoreBooted(t, fw)
}

// TestMain is the entry point for running tests in this package.
// It wraps the standard testing.M.Run() with additional setup/teardown logic
// via testMainWrapper. The exit code from testMainWrapper is passed to os.Exit.
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

	h, cleanup, err := framework.SetupHarness(framework.HarnessConfig{
		PoolName:    "main",
		BaselineTag: "packet-recirc-limit-5",
		Dataplane: framework.DataplaneConfig(framework.DataplaneOptions{
			PacketRecircLimit: testPacketRecircLimit,
		}),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up functional-test harness: %v\n", err)
		return 1
	}
	defer cleanup()

	harness = h
	globalPool = h.Pool()

	defer func() {
		if err := h.Shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shut down VM pool: %v\n", err)
			code = 12
		}
		harness = nil
		globalPool = nil
	}()

	return m.Run()
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

		fw.Run("yanet-cli_dispatches_sibling", func(fw *framework.TestFramework, t *testing.T) {
			output, err := fw.ExecuteCommand(framework.CLIGeneric + " route --help")
			require.NoError(t, err)
			require.NotEmpty(t, output)
		})

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
