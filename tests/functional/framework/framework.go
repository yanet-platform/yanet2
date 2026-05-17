package framework

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"go.uber.org/zap"
)

const (
	// MAC addresses used in test framework
	SrcMAC = "52:54:00:6b:ff:a1"
	DstMAC = "52:54:00:6b:ff:a5"

	// IP addresses used in test framework
	VMIPv4Host    = "203.0.113.14"
	VMIPv4Gateway = "203.0.113.1"
	VMIPv6Gateway = "fe80::1"
	VMIPv6Host    = "fe80::5054:ff:fe6b:ffa5"

	// CLI tool paths (9P defaults, used in single-VM mode)
	CLIBasePath    = "/mnt/target/release"
	CLIRoute       = CLIBasePath + "/yanet-cli-route"
	CLIRouteMPLS   = CLIBasePath + "/yanet-cli-route-mpls"
	CLINAT64       = CLIBasePath + "/yanet-cli-nat64"
	CLIACL         = CLIBasePath + "/yanet-cli-acl"
	CLIFWState     = CLIBasePath + "/yanet-cli-fwstate"
	CLIPipeline    = CLIBasePath + "/yanet-cli-pipeline"
	CLIFunction    = CLIBasePath + "/yanet-cli-function"
	CLIDevicePlain = CLIBasePath + "/yanet-cli-device-plain"
	CLIDecap       = CLIBasePath + "/yanet-cli-decap"
	CLIForward     = CLIBasePath + "/yanet-cli-forward"
	CLIGeneric     = CLIBasePath + "/yanet-cli"

	globalName = "global"
)

// CLIBinaryNames is the canonical list of all CLI binaries installed
// in the YANET target/release directory. Used by StartYANET
// verification, PrepareLocalStorage, and test assertions to keep
// binary lists in sync.
var CLIBinaryNames = []string{
	"yanet-cli",
	"yanet-cli-route", "yanet-cli-route-mpls",
	"yanet-cli-nat64", "yanet-cli-acl",
	"yanet-cli-fwstate", "yanet-cli-pipeline", "yanet-cli-function",
	"yanet-cli-device-plain", "yanet-cli-device-vlan",
	"yanet-cli-decap", "yanet-cli-forward",
	"yanet-cli-common", "yanet-cli-dscp", "yanet-cli-counters",
	"yanet-cli-pdump", "yanet-cli-inspect",
}

// GuestPaths holds all guest-side filesystem paths used by the framework.
// In single-VM mode these point to 9P mounts. In pool/snapshot mode
// they point to /tmp/yanet/ (local tmpfs) so that no YANET process holds
// open fids on 9P mounts, allowing savevm/loadvm to succeed.
type GuestPaths struct {
	CLIBase        string // directory with yanet-cli-* binaries
	BuildDir       string // directory with yanet-dataplane, yanet-controlplane
	ConfigDir      string // directory for config files
	LogDir         string // directory for log files
	DPDKDevbindDir string // directory containing dpdk-devbind.py
	ForwardYAML    string // path to forward.yaml
	LocalMode      bool   // true when paths are on guest tmpfs (pool/snapshot mode)
}

// DefaultGuestPaths returns the standard 9P-backed paths used in single-VM mode.
func DefaultGuestPaths() GuestPaths {
	return GuestPaths{
		CLIBase:        "/mnt/target/release",
		BuildDir:       "/mnt/build",
		ConfigDir:      "/mnt/config",
		LogDir:         "/mnt/logs",
		DPDKDevbindDir: "/mnt/yanet2/subprojects/dpdk/usertools",
		ForwardYAML:    "/mnt/config/forward.yaml",
		LocalMode:      false,
	}
}

// LocalGuestPaths returns paths on local tmpfs inside the guest.
// Used in pool/snapshot mode after PrepareLocalStorage() copies
// files from 9P mounts to /tmp/yanet/.
func LocalGuestPaths() GuestPaths {
	return GuestPaths{
		CLIBase:        "/tmp/yanet/cli",
		BuildDir:       "/tmp/yanet/build",
		ConfigDir:      "/tmp/yanet/config",
		LogDir:         "/tmp/yanet/logs",
		DPDKDevbindDir: "/tmp/yanet/tools",
		ForwardYAML:    "/tmp/yanet/forward.yaml",
		LocalMode:      true,
	}
}

// CLI returns the full path to a CLI binary by name.
func (p GuestPaths) CLI(name string) string {
	return p.CLIBase + "/" + name
}

// baselineSnapshotReady tracks whether the "baseline" VM snapshot
// was successfully created during TestMain setup. Tests check this
// before calling RunWith("baseline", ...) for per-test isolation.
var baselineSnapshotReady atomic.Bool

// Global atomic counter for generating unique log IDs
var logIDCounter atomic.Uint32

// MarkBaselineSaved records that the "baseline" VM snapshot was successfully
// created. Called once from TestMain after setup completes.
func MarkBaselineSaved() {
	baselineSnapshotReady.Store(true)
}

// HasBaselineSnapshot returns true if the "baseline" VM snapshot is available
// for per-test state isolation via RunWith("baseline", ...).
func HasBaselineSnapshot() bool {
	return baselineSnapshotReady.Load()
}

// CommonConfigCommands returns the shell commands that configure the
// baseline YANET state (kni0, forwarding, route FIB, pipelines, devices).
// Paths are resolved from f.Paths so the commands work with both 9P
// and local tmpfs layouts.
func (f *TestFramework) CommonConfigCommands() []string {
	p := f.Paths
	// Config files (route0.yaml, etc.) are always written via CreateConfigFile
	// to the host-side config dir, accessible in the guest as /mnt/config/.
	// Use the 9P guest path for rule files regardless of local/remote mode.
	return []string{
		// Configure kni0 network interface
		"ip link set kni0 up",
		"ip nei replace " + VMIPv6Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip nei replace " + VMIPv4Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip addr replace " + VMIPv4Host + "/24 dev kni0",

		// Configure L2 and L3 forwarding
		p.CLI("yanet-cli-forward") + " update --name=forward0 --rules " + p.ForwardYAML,

		// Bootstrap the default IPv4/IPv6 FIB for the "route0" config.
		// route0.yaml is always at /mnt/config/ (written via host 9P).
		p.CLI("yanet-cli-route") + " fib update --name=route0 --rules /mnt/config/route0.yaml",

		p.CLI("yanet-cli-function") + " update --name=virt --chains chain0:10=forward:forward0",
		p.CLI("yanet-cli-function") + " update --name=test --chains chain2:1=forward:forward0,route:route0",

		p.CLI("yanet-cli-pipeline") + " update --name=bootstrap --functions virt",
		p.CLI("yanet-cli-pipeline") + " update --name=test --functions test",
		p.CLI("yanet-cli-pipeline") + " update --name=dummy",

		p.CLI("yanet-cli-device-plain") + " update --name=01:00.0 --input test:1 --output dummy:1",
		p.CLI("yanet-cli-device-plain") + " update --name=virtio_user_kni0 --input bootstrap:1 --output dummy:1",
	}
}

// CommonConfigCommands is the package-level variable for backward compatibility.
// Uses default 9P paths. In pool mode tests use fw.CommonConfigCommands() instead.
var CommonConfigCommands = (&TestFramework{Paths: DefaultGuestPaths()}).CommonConfigCommands()

// MustParseMAC parses a MAC address string and panics if parsing fails.
// This utility function is designed for use with known-good MAC address constants
// where parsing failure indicates a programming error rather than runtime input error.
//
// Parameters:
//   - mac: MAC address string in standard format (e.g., "52:54:00:6b:ff:a1")
//
// Returns:
//   - net.HardwareAddr: Parsed hardware address
//
// Panics:
//   - If the MAC address string is malformed or invalid
//
// Example:
//
//	hwAddr := MustParseMAC("52:54:00:6b:ff:a1")
func MustParseMAC(mac string) net.HardwareAddr {
	hwAddr, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}
	return hwAddr
}

// generateLogID generates a short 4-character log ID using an atomic counter.
// This provides a unique, compact identifier for logging purposes.
// The counter is a uint32 wrapped at 16 bits, producing 4-character hex IDs (0000-FFFF).
//
// Parameters:
//   - testName: Full test name (e.g., "TestBalancer/TestCase1")
//
// Returns:
//   - string: 4-character hexadecimal log ID (e.g., "009F")
func generateLogID(testName string) string {
	if testName == "" || testName == globalName {
		return globalName
	}
	// Increment counter and get the value (wraps around at uint16 max)
	id := logIDCounter.Add(1)
	return fmt.Sprintf("%04X", uint16(id))
}

// socketClientsCache holds socket clients with thread-safe access
type socketClientsCache struct {
	clients map[int]*SocketClient
	mutex   sync.Mutex
}

// F represents the main test framework structure for YANET functional testing.
// It orchestrates QEMU virtual machine management, CLI command execution, packet processing,
// and network socket communication to provide a comprehensive testing environment.
//
// The framework manages:
//   - QEMU virtual machine lifecycle and networking
//   - CLI command execution within the VM
//   - Packet parsing and analysis capabilities
//   - Socket-based network communication with VM interfaces
//   - Test context via optional *testing.T field
//
// All operations are thread-safe through internal synchronization mechanisms.
// This type has private fields and cannot be constructed directly - use Global() and ForTest(t).
type TestFramework struct {
	qemu         *QEMUManager
	cli          *CLIManager
	PacketParser *PacketParser
	log          *zap.SugaredLogger
	Paths        GuestPaths // Guest-side filesystem paths (9P or local tmpfs)

	socketClients *socketClientsCache

	lastDataplaneConfig    string // Last dataplane config used by StartYANET (for RestartYANET)
	lastControlplaneConfig string // Last controlplane config used by StartYANET (for RestartYANET)

	testName string
	t        *testing.T
}

// Framework is a safe wrapper that prevents direct access to framework methods.
// It provides two ways to access the framework:
//   - Global() - returns TestFramework with testName="global" for global operations in TestMain
//   - ForTest(t) - returns TestFramework bound to *testing.T for test-specific operations
//
// This design ensures that framework methods cannot be called without proper context.
//
// Example usage:
//
//	var globalFramework *Framework
//
//	func TestMain(m *testing.M) {
//	    fw, _ := New(config)
//	    globalFramework = fw
//	    globalFramework.Global().Start()
//	    defer globalFramework.Global().Stop()
//	    m.Run()
//	}
//
//	func TestMyFeature(t *testing.T) {
//	    fw := globalFramework.ForTest(t)
//	    fw.Run("SubTest", func(fw *TestFramework, t *testing.T) {
//	        // Use fw for framework operations, t for assertions
//	    })
//	}
type Framework struct {
	inner *TestFramework
}

// writeToDumpFile appends data to a dump file if debug is enabled and path is not empty.
// If debug is disabled or path is empty, this function does nothing.
//
// Parameters:
//   - path: Path to the dump file (if empty, nothing is written)
//   - data: Data to append to the file
//
// Returns:
//   - error: An error if file operations fail, or nil if successful or skipped
func writeToDumpFile(path string, data []byte) error {
	if path == "" {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// WithLog configures the TestFramework to use the specified logger for debugging
// and monitoring test execution. This functional option allows detailed logging
// of framework operations, packet flows, and VM interactions.
//
// Parameters:
//   - log: A zap.SugaredLogger instance for structured logging
//
// Returns:
//   - FrameworkOption: A functional option that sets the logger
func WithLog(log *zap.SugaredLogger) FrameworkOption {
	return func(fw *TestFramework) error {
		fw.log = log
		return nil
	}
}

// FrameworkOption defines functional options for configuring TestFramework instances.
// This pattern enables flexible initialization with optional parameters while
// maintaining backward compatibility and clean API design.
type FrameworkOption func(*TestFramework) error

// Config contains essential configuration parameters for initializing the test framework.
// It specifies the QEMU virtual machine image and working directory for test execution.
type Config struct {
	Name      string
	QEMUImage string // Path to the QEMU virtual machine image file
}

// New creates and initializes a new Framework instance with the specified configuration
// and optional functional parameters. The framework sets up all necessary components
// including QEMU VM management, CLI operations, and packet processing capabilities.
//
// The initialization process includes:
//   - Working directory creation and validation
//   - QEMU manager setup with the specified VM image
//   - CLI manager initialization for VM command execution
//   - Packet parser setup for network analysis
//   - Socket client cache initialization
//
// Parameters:
//   - config: Required configuration containing VM image path and working directory
//   - opts: Optional functional options for customizing framework behavior
//
// Returns:
//   - *Framework: Fully initialized framework wrapper
//   - error: An error if initialization fails or configuration is invalid
//
// Example:
//
//	config := &Config{
//	    Name: "main",
//	    QEMUImage: "/path/to/vm-image.qcow2",
//	}
//	fw, err := New(config, WithLog(logger))
//	if err != nil {
//	    log.Fatalf("Failed to create framework: %v", err)
//	}
func New(config *Config, opts ...FrameworkOption) (*Framework, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	// Create framework instance with default values
	fw := &TestFramework{
		log:   zap.NewNop().Sugar(), // default noop logger
		Paths: DefaultGuestPaths(),
		socketClients: &socketClientsCache{
			clients: make(map[int]*SocketClient),
		},
	}

	for _, opt := range opts {
		if err := opt(fw); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if fw.qemu == nil {
		// Initialize QEMU manager
		qemu, err := NewQEMUManager(config.Name, config.QEMUImage, fw.log)
		if err != nil {
			return nil, fmt.Errorf("failed to create QEMU manager: %w", err)
		}
		fw.qemu = qemu
	}

	if fw.cli == nil {
		// Initialize CLI manager
		cli, err := NewCLIManager(fw.qemu, CLIWithLog(fw.log))
		if err != nil {
			return nil, fmt.Errorf("failed to create CLI manager: %w", err)
		}
		fw.cli = cli
	}

	if fw.PacketParser == nil {
		fw.PacketParser = NewPacketParser()
	}

	return &Framework{inner: fw}, nil
}

// Global returns the underlying TestFramework with testName="global" for global operations.
// This method should only be used in TestMain for framework lifecycle management
// (Start, Stop, StartYANET, etc.).
//
// Returns:
//   - *TestFramework: The internal framework instance with testName="global"
//
// Example:
//
//	func TestMain(m *testing.M) {
//	    fw, _ := New(config)
//	    globalFramework = fw
//	    gfw := fw.Global()
//	    gfw.Start()
//	    defer gfw.Stop()
//	    m.Run()
//	}
func (f *Framework) Global() *TestFramework {
	return f.inner.withTestName(globalName)
}

// ForTest creates a TestFramework instance bound to the provided *testing.T.
// This method should be called at the beginning of each test function to create
// a test-specific framework instance with automatic test name tracking.
//
// Parameters:
//   - t: The *testing.T instance for the current test
//
// Returns:
//   - *TestFramework: A test-bound framework instance
//
// Example:
//
//	func TestMyFeature(t *testing.T) {
//	    fw := globalFramework.ForTest(t)
//	    fw.Run("SubTest", func(fw *TestFramework, t *testing.T) {
//	        // Test code here
//	    })
//	}
func (f *Framework) ForTest(t *testing.T) *TestFramework {
	fwCopy := f.inner.withTestName(t.Name())
	fwCopy.t = t
	return fwCopy
}

// ForTest binds an already constructed framework instance to a specific test.
// This is used by pooled VMs, where tests acquire a shared base framework from
// VMPool and then need a test-scoped copy with the proper logger and *testing.T.
func (f *TestFramework) ForTest(t *testing.T) *TestFramework {
	fwCopy := f.withTestName(t.Name())
	fwCopy.t = t
	return fwCopy
}

// Start initializes and launches the complete test environment, including the QEMU
// virtual machine with configured networking. This method must be called before
// executing any tests or VM operations.
//
// The startup process includes:
//   - QEMU virtual machine launch with socket networking
//   - Network interface initialization
//   - VM readiness verification
//
// Returns:
//   - error: An error if VM startup fails or networking cannot be established
//
// Example:
//
//	if err := framework.Start(); err != nil {
//	    log.Fatalf("Failed to start test environment: %v", err)
//	}
func (f *TestFramework) Start() (bool, error) {
	fromSnapshot, err := f.qemu.Start()
	if err != nil {
		return false, fmt.Errorf("failed to start QEMU: %w", err)
	}

	return fromSnapshot, nil
}

// Stop performs cleanup of the test environment, closing socket clients
// and stopping the QEMU virtual machine.
//
// Multiple cleanup errors are collected and returned as a combined error.
func (f *TestFramework) Stop() error {
	var errs []error

	f.socketClients.mutex.Lock()
	for _, client := range f.socketClients.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close socket client: %w", err))
		}
	}
	f.socketClients.clients = make(map[int]*SocketClient)
	f.socketClients.mutex.Unlock()

	if err := f.qemu.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop QEMU: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during cleanup: %v", errs)
	}
	return nil
}

// withTestName creates a shallow copy of the framework with the specified test name.
// This allows each test to have its own context while sharing the underlying resources
// (QEMU, CLI, socket clients, etc.). The copy shares the same caches and mutexes for
// thread-safe access to shared resources.
//
// A unique 4-character log ID is generated from the test name for compact logging.
//
// Parameters:
//   - testName: Name of the test (typically from t.Name())
//
// Returns:
//   - *TestFramework: A new framework instance with the test name set
func (f *TestFramework) withTestName(testName string) *TestFramework {
	logID := generateLogID(testName)
	namedLog := f.log.Named(logID)
	if testName != globalName {
		namedLog.Infof("Test '%s' will use log ID: %s", testName, logID)
	}

	fWithName := &TestFramework{
		qemu:          f.qemu,
		cli:           f.cli.WithLog(namedLog),
		PacketParser:  f.PacketParser,
		log:           namedLog,
		Paths:         f.Paths,
		socketClients: f.socketClients, // shared cache with mutex
		testName:      testName,
	}

	if testName == globalName {
		return fWithName
	}

	// Log dump file paths once if they are configured
	inputDumpPath, outputDumpPath := fWithName.getDumpFilePaths()
	if inputDumpPath != "" {
		namedLog.Infof("Recording input socket data to: %s", inputDumpPath)
	}
	if outputDumpPath != "" {
		namedLog.Infof("Recording output socket data to: %s", outputDumpPath)
	}

	return fWithName
}

// SendPacketAndCapture sends a network packet through the specified input interface
// and captures any response from the output interface without performing packet
// verification or parsing. This is a low-level method for raw packet testing.
//
// The method handles:
//   - Socket client retrieval and connection management
//   - Packet transmission through the input interface
//   - Response capture from the output interface with timeout
//   - Automatic socket connection establishment
//   - Optional socket data dumping when debug mode is enabled
//
// Parameters:
//   - inputIfaceIndex: Index of the network interface to send the packet through
//   - outputIfaceIndex: Index of the network interface to capture response from
//   - packet: Raw packet data to transmit
//   - timeout: Maximum time to wait for response capture
//
// Returns:
//   - []byte: Raw response packet data, or nil if no response received
//   - error: An error if packet transmission fails or interfaces are unavailable
//
// Example:
//
//	fw := globalFramework.ForTest(t)
//	fw.Run("SendPacket", func(fw *TestFramework, t *testing.T) {
//	    response, err := fw.SendPacketAndCapture(0, 1, packetData, 5*time.Second)
//	    require.NoError(t, err)
//	})
func (f *TestFramework) SendPacketAndCapture(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) ([]byte, error) {
	f.log.Infof("Sending packet on interface %d and capturing response on interface %d", inputIfaceIndex, outputIfaceIndex)

	// Get socket clients
	inputClient, err := f.GetSocketClient(inputIfaceIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get input socket client: %w", err)
	}

	outputClient, err := f.GetSocketClient(outputIfaceIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get output socket client: %w", err)
	}

	// Get dump file paths for this test
	inputDumpPath, outputDumpPath := f.getDumpFilePaths()

	// Connect to sockets
	if err := inputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to input socket: %w", err)
	}

	if err := outputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to output socket: %w", err)
	}

	// Drain any stale packets from a previous test before sending.
	// Use a short deadline — we only want to flush already-buffered data,
	// not block waiting for new traffic.
	_, _ = outputClient.ReceiveAllPackets(50*time.Millisecond, outputDumpPath)

	// Send packet on input interface
	if err := inputClient.SendPacket(packet, inputDumpPath); err != nil {
		return nil, fmt.Errorf("failed to send packet: %w", err)
	}

	responseData, err := outputClient.ReceivePacket(timeout, outputDumpPath)
	return responseData, err
}

// SendPacketAndCaptureAll sends a network packet and captures all response packets.
func (f *TestFramework) SendPacketAndCaptureAll(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) ([][]byte, error) {
	inputClient, err := f.GetSocketClient(inputIfaceIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get input socket client: %w", err)
	}

	outputClient, err := f.GetSocketClient(outputIfaceIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get output socket client: %w", err)
	}

	inputDumpPath, outputDumpPath := f.getDumpFilePaths()

	if err := inputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to input socket: %w", err)
	}

	if err := outputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to output socket: %w", err)
	}

	if err := inputClient.SendPacket(packet, inputDumpPath); err != nil {
		return nil, fmt.Errorf("failed to send packet: %w", err)
	}

	return outputClient.ReceiveAllPackets(timeout, outputDumpPath)
}

// SendPacketAndParse sends a network packet, captures the response, and parses both
// the input and output packets into structured PacketInfo objects. This high-level
// method provides comprehensive packet analysis for detailed testing scenarios.
//
// The method performs:
//   - Input packet parsing and validation
//   - Packet transmission through the specified interfaces
//   - Response capture with timeout handling
//   - Output packet parsing and analysis
//   - Detailed logging of packet flow for debugging
//   - Optional socket data dumping when debug mode is enabled
//
// Parameters:
//   - inputIfaceIndex: Index of the network interface to send the packet through
//   - outputIfaceIndex: Index of the network interface to capture response from
//   - packet: Raw packet data to transmit
//   - timeout: Maximum time to wait for response capture
//
// Returns:
//   - *PacketInfo: Parsed information about the input packet
//   - *PacketInfo: Parsed information about the output packet (nil if no response)
//   - error: An error if packet processing, transmission, or parsing fails
//
// Example:
//
//	fw := globalFramework.ForTest(t)
//	fw.Run("ParsePacket", func(fw *TestFramework, t *testing.T) {
//	    input, output, err := fw.SendPacketAndParse(0, 1, packetData, 5*time.Second)
//	    require.NoError(t, err)
//	    t.Logf("Sent: %s, Received: %s", input.String(), output.String())
//	})
func (f *TestFramework) SendPacketAndParse(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) (*PacketInfo, *PacketInfo, error) {
	// Parse input packet
	inputPacketInfo, err := f.PacketParser.ParsePacket(packet)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse input packet: %w", err)
	}

	f.log.Debugf("Sending packet: %s", inputPacketInfo.String())

	// Send packet and capture response
	responseData, err := f.SendPacketAndCapture(inputIfaceIndex, outputIfaceIndex, packet, timeout)
	if err != nil {
		return inputPacketInfo, nil, fmt.Errorf("failed to send and capture: %w", err)
	}

	// Parse response packet
	outputPacketInfo, err := f.PacketParser.ParsePacket(responseData)
	if err != nil {
		return inputPacketInfo, nil, fmt.Errorf("failed to parse output packet: %w", err)
	}

	f.log.Debugf("Received packet: %s", outputPacketInfo.String())

	return inputPacketInfo, outputPacketInfo, nil
}

// SendPacketAndParseAll sends a network packet and captures ALL response packets.
func (f *TestFramework) SendPacketAndParseAll(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) ([]*PacketInfo, error) {
	inputPacketInfo, err := f.PacketParser.ParsePacket(packet)
	if err != nil {
		return nil, fmt.Errorf("failed to parse input packet: %w", err)
	}

	f.log.Debugf("Sending packet: %s", inputPacketInfo.String())
	_ = inputPacketInfo // Input packet info not needed for return

	// Send packet and capture all responses
	responses, err := f.SendPacketAndCaptureAll(inputIfaceIndex, outputIfaceIndex, packet, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to send and capture: %w", err)
	}

	// Parse all response packets
	var outputPacketInfos []*PacketInfo
	for i, responseData := range responses {
		outputPacketInfo, err := f.PacketParser.ParsePacket(responseData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse response packet %d: %w", i, err)
		}
		f.log.Debugf("Received packet %d: %s", i, outputPacketInfo.String())
		outputPacketInfos = append(outputPacketInfos, outputPacketInfo)
	}

	return outputPacketInfos, nil
}

func (f *TestFramework) SendPacketsAndParseAll(inputIfaceIndex int, outputIfaceIndex int, packets [][]byte, timeout time.Duration) ([]*PacketInfo, error) {
	f.log.Infof("Sending %d packets on interface %d and capturing responses on interface %d", len(packets), inputIfaceIndex, outputIfaceIndex)

	var responses [][]byte
	for idx, packet := range packets {
		packetResponses, err := f.SendPacketAndCaptureAll(inputIfaceIndex, outputIfaceIndex, packet, timeout)
		if err != nil {
			return nil, fmt.Errorf("failed to send packet %d and capture responses: %w", idx, err)
		}
		responses = append(responses, packetResponses...)
	}

	// Parse all response packets.
	var outputPacketInfos []*PacketInfo
	for i, responseData := range responses {
		outputPacketInfo, err := f.PacketParser.ParsePacket(responseData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse response packet %d: %w", i, err)
		}
		f.log.Debugf("Received packet %d: %s", i, outputPacketInfo.String())
		outputPacketInfos = append(outputPacketInfos, outputPacketInfo)
	}

	return outputPacketInfos, nil
}

// GetSocketClient retrieves or creates a socket client for the specified network
// interface. The method implements caching to reuse existing connections and
// ensures thread-safe access to the socket client pool.
//
// For test-specific frameworks (created via ForTest), the socket client
// is returned with a named logger using the test's log ID.
//
// The method handles:
//   - Interface index validation against available QEMU socket paths
//   - Thread-safe access to the socket client cache
//   - Automatic socket client creation for new interfaces
//   - Client caching for performance optimization
//   - Logger customization for test-specific contexts
//
// Parameters:
//   - ifaceIndex: Zero-based index of the network interface (must be < len(SocketPaths))
//
// Returns:
//   - *SocketClient: Socket client for the specified interface
//   - error: An error if the interface index is invalid or client creation fails
//
// Example:
//
//	client, err := fw.GetSocketClient(0)
//	if err != nil {
//	    log.Fatalf("Failed to get socket client: %v", err)
//	}
//	defer client.Close()
func (f *TestFramework) GetSocketClient(ifaceIndex int) (*SocketClient, error) {
	// For QEMU networking: Unix stream socket interfaces only
	if ifaceIndex >= len(f.qemu.SocketPaths) {
		return nil, fmt.Errorf("interface index %d out of range, available interfaces: 0-%d", ifaceIndex, len(f.qemu.SocketPaths)-1)
	}

	// Lock the mutex to safely access the socketClients map
	f.socketClients.mutex.Lock()
	defer f.socketClients.mutex.Unlock()

	// Check if we already have a client for this interface
	client, exists := f.socketClients.clients[ifaceIndex]
	if !exists {
		// Create a new client without logger (will be set via WithLog)
		socketPath := f.qemu.SocketPaths[ifaceIndex]
		var err error
		client, err = NewSocketClient(socketPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create Unix socket client for interface %d (path %s): %w", ifaceIndex, socketPath, err)
		}
		// Store the client in the map
		f.socketClients.clients[ifaceIndex] = client
	}

	// Return a new client instance with the current framework's logger
	// This shares the underlying connection (inner) but has its own logger
	return client.WithLog(f.log.With("interface", ifaceIndex)), nil
}

// ResetConnections closes and reconnects all socket clients.
// This ensures a clean state after a snapshot restore, preventing stale
// connections from causing false heartbeat failures. Unlike draining,
// this approach guarantees a completely clean stream by discarding any
// buffered data with the old connection.
//
// Must be called after loadvm and before any packet operations
// (WaitForDatapathReady, SendPacketAndCapture, etc.) because loadvm
// restores QEMU's internal stream-netdev state but the host-side UNIX
// socket connections are stale — Connect() short-circuits on the dead
// net.Conn and never reconnects.
//
// Errors during reset are logged but do not cause the operation to fail.
func (f *TestFramework) ResetConnections() {
	f.socketClients.mutex.Lock()
	defer f.socketClients.mutex.Unlock()

	for i, client := range f.socketClients.clients {
		if err := client.ResetConnection(); err != nil {
			f.log.Warnf("Failed to reset connection for interface %d: %v", i, err)
		} else {
			f.log.Debugf("Reset connection for interface %d", i)
		}
	}
}

// ExecuteCommand executes a single CLI command within the QEMU virtual machine
// via the serial console interface. This is a proxy method that delegates to the
// underlying CLI manager.
//
// For test-specific frameworks (created via ForTest), commands are logged
// with the test's unique log ID for easy debugging.
//
// Parameters:
//   - command: The shell command to execute in the virtual machine
//
// Returns:
//   - string: The cleaned command output (stdout/stderr combined)
//   - error: An error if the VM is not ready, command fails, or timeout occurs
//
// Example:
//
//	fw := globalFramework.ForTest(t)
//	fw.Run("ExecuteCommand", func(fw *TestFramework, t *testing.T) {
//	    output, err := fw.ExecuteCommand("ls -la /etc")
//	    require.NoError(t, err)
//	    t.Logf("Output: %s", output)
//	})
func (f *TestFramework) ExecuteCommand(command string) (string, error) {
	return f.cli.ExecuteCommand(command)
}

// ExecuteCommandWithTimeout executes a single CLI command with a custom
// timeout. Use this for operations that may take longer than the default
// 30s, such as copying large binaries on slow emulated VMs.
func (f *TestFramework) ExecuteCommandWithTimeout(command string, timeout time.Duration) (string, error) {
	return f.cli.ExecuteCommandWithTimeout(command, timeout)
}

// ExecuteCommands executes multiple CLI commands sequentially within the QEMU
// virtual machine. This is a proxy method that delegates to the underlying CLI
// manager.
//
// Each command is executed in order, and execution stops at the first command
// that returns an error.
//
// Parameters:
//   - commands: Variable number of shell commands to execute sequentially
//
// Returns:
//   - []string: Slice of command outputs in execution order (may be partial if error occurs)
//   - error: An error from the first failed command, or nil if all commands succeed
//
// Example:
//
//	fw := globalFramework.ForTest(t)
//	fw.Run("ExecuteCommands", func(fw *TestFramework, t *testing.T) {
//	    outputs, err := fw.ExecuteCommands(
//	        "mkdir -p /tmp/test",
//	        "echo 'hello' > /tmp/test/file.txt",
//	        "cat /tmp/test/file.txt",
//	    )
//	    require.NoError(t, err)
//	    t.Logf("Outputs: %v", outputs)
//	})
func (f *TestFramework) ExecuteCommands(commands ...string) ([]string, error) {
	return f.cli.ExecuteCommands(commands...)
}

// getDumpFilePaths returns dump file paths for a test.
// If debug is disabled or testName is empty, returns empty strings.
func (f *TestFramework) getDumpFilePaths() (string, string) {
	if !IsDebugEnabled() || f.testName == "" {
		return "", ""
	}

	inputDumpPath := filepath.Join(f.qemu.WorkDir, fmt.Sprintf("%s.in.dump", f.testName))
	outputDumpPath := filepath.Join(f.qemu.WorkDir, fmt.Sprintf("%s.out.dump", f.testName))

	return inputDumpPath, outputDumpPath
}

// StartYANET initializes and launches the complete YANET network processing stack
// within the virtual machine environment. This comprehensive method handles all
// aspects of YANET deployment including configuration, kernel module loading,
// network interface binding, and service startup.
//
// The startup process includes:
//   - Configuration file creation and validation
//   - YANET binary availability verification
//   - Required kernel module loading (vfio-pci)
//   - Network interface binding to DPDK drivers
//   - YANET dataplane service startup with background execution
//   - YANET controlplane service startup and readiness verification
//   - Service health checks and log monitoring
//
// Parameters:
//   - dataplaneConfig: YAML configuration content for the YANET dataplane service
//   - controlplaneConfig: YAML configuration content for the YANET controlplane service
//
// Returns:
//   - error: An error if any step of the YANET startup process fails
//
// Example:
//
//	dataplaneYAML := `
//	interfaces:
//	  - name: "eth0"
//	    pci: "01:00.0"
//	`
//	controlplaneYAML := `
//	modules:
//	  - name: "forward"
//	`
//	if err := fw.StartYANET(dataplaneYAML, controlplaneYAML); err != nil {
//	    log.Fatalf("YANET startup failed: %v", err)
//	}
func (f *TestFramework) StartYANET(dataplaneConfig string, controlplaneConfig string) error {
	yanetStart := time.Now()
	f.log.Info("Starting YANET in VM...")

	f.lastDataplaneConfig = dataplaneConfig
	f.lastControlplaneConfig = controlplaneConfig

	if !f.qemu.IsVMReady() {
		return fmt.Errorf("vm is not ready")
	}
	if ShouldKeepVMAlive() {
		_, err := f.cli.ExecuteCommand("service ssh start")
		if err != nil {
			f.log.Warnf("Failed to start debug ssh server: %v", err)
		}
	}

	// Validate configurations
	if dataplaneConfig == "" {
		return fmt.Errorf("dataplane configurations are required")
	}

	// Create configuration files in the mounted config directory on the host
	f.log.Debug("Creating configuration files in mounted config directory...")
	if err := f.createConfigFiles(dataplaneConfig, controlplaneConfig); err != nil {
		return fmt.Errorf("failed to create config files: %w", err)
	}

	p := f.Paths

	// Batch-check all required binaries in a single compound command.
	f.log.Debug("Checking YANET binary availability...")
	allBinaries := []string{
		p.BuildDir + "/dataplane/yanet-dataplane",
		p.BuildDir + "/controlplane/yanet-controlplane",
	}
	for _, name := range CLIBinaryNames {
		allBinaries = append(allBinaries, p.CLI(name))
	}
	checkCmd := "test -x " + strings.Join(allBinaries, " && test -x ")
	if _, err := f.cli.ExecuteCommand(checkCmd); err != nil {
		return fmt.Errorf(
			"required binary not found in %s or %s\n"+
				"Run 'just dbuild' + 'just dcli' to build YANET",
			p.BuildDir, p.CLIBase,
		)
	}
	f.log.Debug("All binaries present")

	// Load required kernel modules
	f.log.Debug("Loading kernel modules and binding DPDK interfaces...")
	devbind := p.DPDKDevbindDir + "/dpdk-devbind.py"
	_, err := f.cli.ExecuteCommand(fmt.Sprintf(
		"sudo modprobe vfio-pci && %s --bind=vfio-pci 01:00.0 && %s --bind=vfio-pci 02:00.0",
		devbind, devbind,
	))
	if err != nil {
		return fmt.Errorf("DPDK setup failed: %w", err)
	}
	f.log.Debug("DPDK interfaces bound")

	// Start dataplane in background
	f.log.Debug("Starting YANET dataplane...")
	dataplaneCmd := fmt.Sprintf(
		"bash -c 'nohup %s/dataplane/yanet-dataplane %s/dataplane.yaml > %s/yanet-dataplane.log 2>&1 &'",
		p.BuildDir, p.ConfigDir, p.LogDir,
	)
	output, err := f.cli.ExecuteCommand(dataplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}
	f.log.Infof("Dataplane started: %s (took %v)", output, time.Since(yanetStart).Round(time.Millisecond))
	f.log.Infof("Wait for the kni0 device to appear")
	dpStart := time.Now()
	err = f.WaitOutputPresent("ip link", func(output string) bool {
		return strings.Contains(output, "kni0")
	}, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}
	f.log.Infof("kni0 appeared after %v", time.Since(dpStart).Round(time.Millisecond))

	// Start controlplane in background
	f.log.Debug("Starting YANET controlplane...")
	controlplaneCmd := fmt.Sprintf(
		"bash -c 'nohup %s/controlplane/yanet-controlplane -c %s/controlplane.yaml > %s/yanet-controlplane.log 2>&1 &'",
		p.BuildDir, p.ConfigDir, p.LogDir,
	)
	output, err = f.cli.ExecuteCommand(controlplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}
	f.log.Infof("Controlplane started: %s (total YANET startup: %v)", output, time.Since(yanetStart).Round(time.Millisecond))

	// Verify services are running
	f.log.Debug("Verifying YANET services are running...")

	err = f.WaitOutputPresent("cat "+p.LogDir+"/yanet-controlplane.log", func(output string) bool {
		return strings.Contains(output, "all built-in modules ready")
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}

	checkCmds := []string{
		"ps awux | grep [y]anet-dataplane",
		"ps awux | grep [y]anet-controlplane",
		"cat " + p.LogDir + "/yanet-dataplane.log",
		"cat " + p.LogDir + "/yanet-controlplane.log",
	}

	_, err = f.cli.ExecuteCommands(checkCmds...)
	if err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	f.log.Infof("YANET services started successfully (total: %v)", time.Since(yanetStart).Round(time.Millisecond))
	return nil
}

func (f *TestFramework) RestartYANET() error {
	f.log.Info("Restarting YANET (kill + fresh start)...")

	killCmd := "kill $(pidof yanet-dataplane) $(pidof yanet-controlplane) 2>/dev/null; sleep 1; kill -9 $(pidof yanet-dataplane) $(pidof yanet-controlplane) 2>/dev/null; true"
	if _, err := f.ExecuteCommand(killCmd); err != nil {
		f.log.Debugf("Kill command returned error (non-fatal): %v", err)
	}

	if _, err := f.ExecuteCommand("ip link del kni0 2>/dev/null; true"); err != nil {
		f.log.Debugf("kni0 cleanup returned error (non-fatal): %v", err)
	}

	if err := f.StartYANET(f.lastDataplaneConfig, f.lastControlplaneConfig); err != nil {
		return fmt.Errorf("YANET restart failed: %w", err)
	}

	if _, err := f.ExecuteCommands(f.CommonConfigCommands()...); err != nil {
		return fmt.Errorf("YANET reconfigure after restart failed: %w", err)
	}

	f.log.Info("YANET restarted and reconfigured successfully")
	return nil
}

// WaitOutputPresent repeatedly executes a command until the output satisfies the
// provided checker function or the timeout expires. This utility method is used
// for waiting on asynchronous operations and service readiness verification.
//
// The method polls the command output at regular intervals (100ms) and applies
// the checker function to determine if the expected condition has been met.
// This is particularly useful for waiting on service startup, configuration
// application, or system state changes.
//
// Parameters:
//   - cmd: Shell command to execute repeatedly
//   - checker: Function that returns true when the desired condition is met
//   - timeout: Maximum time to wait for the condition
//
// Returns:
//   - error: An error if the timeout expires or command execution fails
//
// Example:
//
//	err := fw.WaitOutputPresent("ps aux | grep yanet", func(output string) bool {
//	    return strings.Contains(output, "yanet-dataplane")
//	}, 30*time.Second)
func (f *TestFramework) WaitOutputPresent(cmd string, checker func(string) bool, timeout time.Duration) error {
	// Wait for flags to be applied
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		output, err := f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to check output: %w", err)
		}

		if checker(output) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for output to be present: %s", cmd)
}

func (f *TestFramework) CreateConfigFile(name string, config string) error {
	// Always write to the host filesystem via the 9P-shared config directory.
	// In pool mode, 9P is remounted after loadvm so /mnt/config is available.
	// Tests reference /mnt/config/ in CLI commands, so this must be consistent.
	configDir := f.qemu.ConfigDir
	if configDir == "" {
		return fmt.Errorf("config directory not set in QEMU manager")
	}

	configPath := filepath.Join(configDir, name)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write config to %s: %w", configPath, err)
	}
	f.log.Debugf("Created config: %s", configPath)
	return nil
}

// createGuestFile writes content to a file inside the guest VM via serial
// console. Uses base64 encoding to avoid heredoc echo/marker confusion
// on the serial terminal.
func (f *TestFramework) createGuestFile(guestPath string, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := fmt.Sprintf("echo '%s' | base64 -d > %s", encoded, guestPath)
	if _, err := f.ExecuteCommand(cmd); err != nil {
		return fmt.Errorf("failed to write guest file %s: %w", guestPath, err)
	}
	f.log.Debugf("Created guest config: %s", guestPath)
	return nil
}

// CreateForwardConfig writes forward.yaml to the path referenced by
// f.Paths.ForwardYAML. In 9P mode this writes to the host filesystem;
// in local mode it writes via serial console to the guest tmpfs.
func (f *TestFramework) CreateForwardConfig(config string) error {
	if f.Paths.LocalMode {
		// Local mode: write directly into guest filesystem via serial console.
		return f.createGuestFile(f.Paths.ForwardYAML, config)
	}
	// 9P mode: write to host filesystem, accessible via 9P mount.
	return f.CreateConfigFile("forward.yaml", config)
}

// createConfigFiles creates YANET configuration files in the host filesystem
// within the mounted config directory that is accessible from the virtual machine.
// This method handles the host-side file creation for VM-accessible configuration.
//
// The method performs:
//   - Configuration directory validation from QEMU manager
//   - Dataplane configuration file creation (dataplane.yaml)
//   - Controlplane configuration file creation (controlplane.yaml)
//   - File creation verification and error handling
//   - Proper file permissions setting for VM access
//
// Parameters:
//   - dataplaneConfig: YAML configuration content for YANET dataplane
//   - controlplaneConfig: YAML configuration content for YANET controlplane
//
// Returns:
//   - error: An error if configuration directory is unavailable or file creation fails
//
// Note: This is an internal method used by StartYANET and should not be called directly.
func (f *TestFramework) createConfigFiles(dataplaneConfig string, controlplaneConfig string) error {
	p := f.Paths
	f.log.Debug("Creating configuration files...")

	if p.ConfigDir != "/mnt/config" {
		// Local tmpfs mode (pool): write configs into guest via serial console.
		if err := f.createGuestFile(p.ConfigDir+"/dataplane.yaml", dataplaneConfig); err != nil {
			return err
		}
		if err := f.createGuestFile(p.ConfigDir+"/controlplane.yaml", controlplaneConfig); err != nil {
			return err
		}
		f.log.Debug("Configuration files created in guest tmpfs")
		return nil
	}

	// Default (9P mode): write to host filesystem via shared directory.
	configDir := f.qemu.ConfigDir
	if configDir == "" {
		return fmt.Errorf("config directory not set in QEMU manager")
	}

	if err := f.CreateConfigFile("dataplane.yaml", dataplaneConfig); err != nil {
		return err
	}
	if err := f.CreateConfigFile("controlplane.yaml", controlplaneConfig); err != nil {
		return err
	}

	f.log.Debug("Configuration files created on host")
	return nil
}

// WaitForReady blocks until the QEMU virtual machine becomes ready for command
// execution or the specified timeout expires. This is a proxy method for QEMU.WaitForReady.
//
// Parameters:
//   - timeout: Maximum time to wait for VM readiness
//
// Returns:
//   - error: An error if the timeout expires before VM becomes ready, or nil if ready
//
// Example:
//
//	if err := fw.WaitForReady(60 * time.Second); err != nil {
//	    log.Fatalf("VM failed to become ready: %v", err)
//	}
func (f *TestFramework) WaitForReady(timeout time.Duration) error {
	return f.qemu.WaitForReady(timeout)
}

func (f *TestFramework) GetSocketPaths() []string {
	return f.qemu.SocketPaths
}

// ValidateCounter queries a named counter via yanet-cli-counters and
// compares its value against expect. Sums all instances for counters
// with multiple instances. Returns an error if the counter is not found
// or the value does not match.
func (f *TestFramework) ValidateCounter(name string, expect uint64) error {
	cmd := f.Paths.CLI("yanet-cli-counters") +
		" pipeline --device-name kni0 --pipeline-name test"
	output, err := f.ExecuteCommand(cmd)
	if err != nil {
		return fmt.Errorf("counters query failed: %w", err)
	}

	var resp struct {
		Counters []struct {
			Name      string `json:"name"`
			Instances []struct {
				Values []uint64 `json:"values"`
			} `json:"instances"`
		} `json:"counters"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return fmt.Errorf("parse counters response: %w", err)
	}

	for _, c := range resp.Counters {
		if c.Name == name {
			var total uint64
			for _, inst := range c.Instances {
				for _, v := range inst.Values {
					total += v
				}
			}
			if total != expect {
				return fmt.Errorf("counter %s: expected %d, got %d", name, expect, total)
			}
			return nil
		}
	}
	return fmt.Errorf("counter %q not found", name)
}

// Run executes a subtest with the given name and function. This method wraps
// t.Run() and automatically creates a new TestFramework instance with the
// correct test name. This ensures that all framework operations within the
// subtest are properly tracked and logged.
//
// The callback function receives two parameters:
//   - fw: A new *TestFramework instance with the correct test name set
//   - t: The *testing.T instance for the subtest
//
// This design separates framework operations (via fw) from test assertions (via t),
// making the code more explicit and preventing accidental use of the wrong test context.
//
// Parameters:
//   - name: The name of the subtest
//   - fn: The test function that receives fw and t
//
// Returns:
//   - bool: True if the test passed, false otherwise (same as t.Run)
//
// Example:
//
//	func TestMyFeature(t *testing.T) {
//	    fw := globalFramework.ForTest(t)
//
//	    fw.Run("BasicTest", func(fw *TestFramework, t *testing.T) {
//	        input, output, err := fw.SendPacketAndParse(0, 0, packet, timeout)
//	        require.NoError(t, err)
//	    })
//
//	    fw.Run("AdvancedTest", func(fw *TestFramework, t *testing.T) {
//	        _, err := fw.ExecuteCommand("some command")
//	        require.NoError(t, err)
//	    })
//	}
func (f *TestFramework) Run(name string, fn func(fw *TestFramework, t *testing.T)) bool {
	if f.t == nil {
		panic("Run() can only be called on TestFramework created via ForTest()")
	}

	f.log.Debugf("Resetting socket connections before test '%s'", name)
	f.ResetConnections()

	return f.t.Run(name, func(t *testing.T) {
		// Create a new TestFramework with the subtest's full name
		subFw := f.withTestName(t.Name())
		subFw.t = t
		fn(subFw, t)
	})
}

// guest9PMountPoints lists the 9P mount points inside the guest VM
// in the order they appear in fstab. These must be unmounted before
// savevm because QEMU registers a migration blocker for each mounted
// VirtFS/9P export.
var guest9PMountPoints = []string{
	"/mnt/logs",
	"/mnt/config",
	"/mnt/build",
	"/mnt/target",
	"/mnt/yanet2",
}

// Unmount9P unmounts all 9P shares inside the guest VM.
// This removes the QEMU migration blockers so that savevm/loadvm
// can proceed. When PrepareLocalStorage() has been called first,
// no process holds open fids on 9P mounts and plain umount works.
// Log tailer processes (tail -f >> /mnt/logs/...) are killed first
// because they hold open fids on /mnt/logs via write-append.
func (f *TestFramework) Unmount9P() error {
	if !f.qemu.Ninepmounted.Load() {
		f.log.Debug("9P mounts already unmounted, skipping")
		return nil
	}
	// Batch all umounts into a single command to avoid 6 round-trips
	// through the serial console (~600ms → ~100ms).
	cmd := "umount"
	for _, mp := range guest9PMountPoints {
		cmd += " " + mp
	}
	cmd += " 2>/dev/null; true"
	if _, err := f.ExecuteCommand(cmd); err != nil {
		f.log.Debugf("batch umount returned error (may be already unmounted): %v", err)
	}
	f.qemu.Ninepmounted.Store(false)
	f.log.Debug("All 9P mounts unmounted")
	return nil
}

// PrepareLocalStorage copies all YANET binaries, CLI tools, configs and
// scripts from 9P mounts to /tmp/yanet/ inside the guest. After this
// call, YANET can be started from local tmpfs paths so that no process
// holds open fids on 9P mounts, making savevm/loadvm work cleanly.
//
// This also switches f.Paths to LocalGuestPaths().
func (f *TestFramework) PrepareLocalStorage() error {
	f.log.Info("Copying YANET files from 9P mounts to local tmpfs...")

	// Ensure 9P mounts are available (they may be unmounted after a
	// snapshot restore from a booted overlay).
	if !f.qemu.Ninepmounted.Load() {
		f.log.Debug("Mounting 9P shares before PrepareLocalStorage...")
		if err := f.Mount9P(); err != nil {
			return fmt.Errorf("failed to mount 9P shares: %w", err)
		}
	}

	// Copy files step by step to avoid serial terminal line-length limits.
	// Each command is kept short enough for reliable serial transmission.
	copyCommands := []string{
		"mkdir -p /tmp/yanet/build/dataplane /tmp/yanet/build/controlplane /tmp/yanet/cli /tmp/yanet/config /tmp/yanet/logs /tmp/yanet/tools",
		"cp /mnt/build/dataplane/yanet-dataplane /tmp/yanet/build/dataplane/",
		"cp /mnt/build/controlplane/yanet-controlplane /tmp/yanet/build/controlplane/",
		"cp /mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py /tmp/yanet/tools/",
	}

	// Copy CLI binaries individually -- serial terminals truncate long lines.
	for _, name := range CLIBinaryNames {
		copyCommands = append(copyCommands,
			"cp /mnt/target/release/"+name+" /tmp/yanet/cli/")
	}
	copyCommands = append(copyCommands,
		"chmod +x /tmp/yanet/cli/* /tmp/yanet/build/dataplane/* /tmp/yanet/build/controlplane/*",
	)

	for _, cmd := range copyCommands {
		if _, err := f.ExecuteCommandWithTimeout(cmd, 60*time.Second); err != nil {
			return fmt.Errorf("PrepareLocalStorage failed on %q: %w", cmd, err)
		}
	}

	f.Paths = LocalGuestPaths()
	// 9P shares are still mounted (we just copied from them).
	f.qemu.Ninepmounted.Store(true)
	f.log.Info("Local storage prepared, switched to local paths")
	return nil
}

// StartLogTailers starts background processes that tail YANET log files
// from local tmpfs to the 9P-mounted /mnt/logs/ directory, making logs
// visible on the host in real time. Must be called after YANET is
// running and 9P mounts are available.
func (f *TestFramework) StartLogTailers() error {
	tailers := []string{
		"bash -c 'nohup tail -f /tmp/yanet/logs/yanet-dataplane.log >> /mnt/logs/yanet-dataplane.log 2>/dev/null &'",
		"bash -c 'nohup tail -f /tmp/yanet/logs/yanet-controlplane.log >> /mnt/logs/yanet-controlplane.log 2>/dev/null &'",
	}
	for _, cmd := range tailers {
		if _, err := f.ExecuteCommand(cmd); err != nil {
			return fmt.Errorf("failed to start log tailer: %w", err)
		}
	}
	f.log.Debug("Log tailers started (tmpfs -> 9P)")
	return nil
}

// Mount9P remounts all 9P shares inside the guest VM.
// Uses mount -a for speed, then verifies each mount point is present.
func (f *TestFramework) Mount9P() error {
	if _, err := f.ExecuteCommand("mount -a 2>/dev/null; true"); err != nil {
		return fmt.Errorf("mount -a failed: %w", err)
	}

	// Verify all expected mount points are present in a single command.
	output, err := f.ExecuteCommand("mount")
	if err != nil {
		return fmt.Errorf("failed to list mounts: %w", err)
	}
	for _, mp := range guest9PMountPoints {
		if !strings.Contains(output, mp) {
			return fmt.Errorf("9P mount point %s not mounted after mount -a", mp)
		}
	}

	f.qemu.Ninepmounted.Store(true)
	f.log.Debug("All 9P mounts restored and verified")
	return nil
}

// SaveSnapshot saves a named VM snapshot that captures the full machine
// state (CPU, RAM, devices). Subsequent calls to RestoreAndReconnect can
// revert the VM to this point. This is typically called from TestMain
// after YANET is configured to create a "baseline" snapshot.
//
// The method unmounts all 9P shares before savevm (to remove QEMU
// migration blockers) and remounts them afterward. This only works
// cleanly when PrepareLocalStorage() was called first so that no
// process holds open fids on 9P mounts.
func (f *TestFramework) SaveSnapshot(name string) error {
	if err := f.Unmount9P(); err != nil {
		return fmt.Errorf("pre-savevm unmount failed: %w", err)
	}
	if err := f.qemu.SaveSnapshot(name); err != nil {
		_ = f.Mount9P()
		return err
	}
	if err := f.Mount9P(); err != nil {
		return fmt.Errorf("post-savevm remount failed: %w", err)
	}
	f.log.Infof("Snapshot %q saved successfully", name)
	return nil
}

// SaveSnapshotKeepUnmounted saves a snapshot like SaveSnapshot but does NOT
// remount 9P shares afterward. Use this for baseline snapshots in pool mode:
// since RestoreAndReconnect will loadvm immediately, the intermediate remount
// would just be unmounted again before loadvm, wasting ~4s per test.
func (f *TestFramework) SaveSnapshotKeepUnmounted(name string) error {
	if err := f.Unmount9P(); err != nil {
		return fmt.Errorf("pre-savevm unmount failed: %w", err)
	}
	if err := f.qemu.SaveSnapshot(name); err != nil {
		_ = f.Mount9P()
		return err
	}
	// ninepmounted remains false — caller knows 9P is unmounted.
	f.log.Infof("Snapshot %q saved (9P unmounted, ready for pool use)", name)
	return nil
}

// ExportCurrentOverlay copies the VM's current qcow2 overlay to dst. This is
// used to cache prepared template overlays (for example a prebuilt baseline)
// and start future pool VMs from the same snapshot source.
func (f *TestFramework) ExportCurrentOverlay(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create overlay cache dir: %w", err)
	}
	src := filepath.Join(f.qemu.WorkDir, "overlay.qcow2")
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("copy overlay %s -> %s: %w", src, dst, err)
	}
	f.log.Infof("Exported current overlay to %s", dst)
	return nil
}

// restoreSnapshotCore is the shared low-level sequence for both RestoreClean
// and RestoreAndReconnect: unmount 9P → loadvm → serial reconnect → wait for
// shell prompt → mount 9P. It does not reset socket connections or run a
// heartbeat check — callers add that on top as needed.
func (f *TestFramework) restoreSnapshotCore(snapshot string) error {
	if err := f.Unmount9P(); err != nil {
		f.log.Debugf("Pre-restore unmount (may be already unmounted): %v", err)
	}

	if err := f.qemu.RestoreSnapshot(snapshot); err != nil {
		return fmt.Errorf("failed to restore snapshot %q: %w", snapshot, err)
	}

	f.qemu.stopSerialReader()

	if err := f.qemu.ReconnectSerial(); err != nil {
		return fmt.Errorf("failed to reconnect serial after restore: %w", err)
	}

	f.qemu.setVMReady(false)
	f.qemu.readySignal = make(chan bool, 1)
	f.qemu.resetSerialBuffer()
	go f.qemu.readSerial()

	stdin := f.qemu.GetStdin()
	if stdin == nil {
		return fmt.Errorf("serial console unavailable after restore")
	}
	for range 3 {
		_, _ = stdin.Write([]byte{0x03})
		time.Sleep(20 * time.Millisecond)
	}
	const restoreTimeout = 30 * time.Second
	deadline := time.Now().Add(restoreTimeout)
	_, _ = stdin.Write([]byte("\n\n"))
	for !f.qemu.IsVMReady() && time.Now().Before(deadline) {
		select {
		case <-f.qemu.readySignal:
		case <-time.After(1 * time.Second):
			if !f.qemu.IsVMReady() {
				_, _ = stdin.Write([]byte("\n\n"))
			}
		}
	}
	if !f.qemu.IsVMReady() {
		return fmt.Errorf("VM did not respond within %v after restoring %q", restoreTimeout, snapshot)
	}

	if err := f.Mount9P(); err != nil {
		return fmt.Errorf("post-restore remount failed: %w", err)
	}

	return nil
}

// RestoreClean reverts the VM to a previously saved snapshot and
// re-establishes serial console and 9P mounts WITHOUT running the
// dataplane heartbeat check. Use this for snapshots where YANET is
// not yet running (e.g. "preyanet") -- StartYANET will be called
// separately after restore.
func (f *TestFramework) RestoreClean(snapshot string) error {
	f.log.Infof("Restoring snapshot %q (clean, no heartbeat)...", snapshot)
	if err := f.restoreSnapshotCore(snapshot); err != nil {
		return err
	}
	f.log.Infof("Snapshot %q clean restore complete", snapshot)
	return nil
}

// RestoreAndReconnect reverts the VM to a previously saved snapshot and
// re-establishes the serial console and socket connections that break
// when the guest state is rolled back.
//
// Call sequence after restore:
//  1. Unmount 9P shares
//  2. loadvm via QEMU monitor (monitor socket survives)
//  3. Reconnect serial console
//  4. Wait for shell prompts
//  5. Mount 9P shares
//  6. Reset socket connections (close stale connections, reconnect)
//  7. Wait for dataplane ready (ICMP heartbeat)
//
// Socket connections MUST be reset before the heartbeat because loadvm
// restores QEMU's internal stream-netdev state but the host-side UNIX
// socket connections are stale. Without reset, Connect() short-circuits
// on the dead net.Conn and heartbeat fails silently.
func (f *TestFramework) RestoreAndReconnect(snapshot string) error {
	f.log.Infof("Restoring snapshot %q...", snapshot)
	if err := f.restoreSnapshotCore(snapshot); err != nil {
		return err
	}

	f.ResetConnections()

	const heartbeatTimeout = 10 * time.Second
	if err := f.WaitForDatapathReady(heartbeatTimeout); err != nil {
		f.log.Warnf("Heartbeat failed after loadvm: %v", err)
		f.runKni0Diagnostic("pre-restart")

		f.log.Info("Restarting YANET to reinitialize DPDK device state...")
		if restartErr := f.RestartYANET(); restartErr != nil {
			return fmt.Errorf("heartbeat failed, restart also failed: %w", restartErr)
		}

		f.ResetConnections()

		if err := f.WaitForDatapathReady(heartbeatTimeout); err != nil {
			return fmt.Errorf("heartbeat failed after YANET restart: %w", err)
		}
	}

	f.log.Infof("Snapshot %q restore complete", snapshot)
	return nil
}

// WaitForDatapathReady sends ICMP heartbeat packets until the dataplane
// responds, confirming DPDK virtio-user reconnect after a snapshot restore.
// No operstate check — on macOS TCG and Linux KVM, kni0 operstate stays DOWN
// even when DPDK is actively forwarding. Only an end-to-end packet test works.
func (f *TestFramework) WaitForDatapathReady(timeout time.Duration) error {
	f.log.Debug("Waiting for dataplane to be ready...")
	start := time.Now()

	srcIP := net.ParseIP(VMIPv4Gateway)
	dstIP := net.ParseIP(VMIPv4Host)
	if srcIP == nil || dstIP == nil {
		return fmt.Errorf("invalid VM IP addresses for heartbeat")
	}

	srcMAC := MustParseMAC(SrcMAC)
	dstMAC := MustParseMAC(DstMAC)
	eth := layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       dstMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}
	payload := []byte("hb")

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, &eth, &ip4, &icmp, gopacket.Payload(payload)); err != nil {
		return fmt.Errorf("failed to serialize heartbeat packet: %w", err)
	}
	heartbeat := buf.Bytes()

	deadline := time.Now().Add(timeout)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		elapsed := time.Since(start).Round(time.Millisecond)

		attemptTimeout := 2 * time.Second
		if attempt <= 3 {
			attemptTimeout = 500 * time.Millisecond
		}

		f.log.Debugf("Heartbeat attempt %d (elapsed=%v, timeout=%v)", attempt, elapsed, attemptTimeout)

		_, err := f.SendPacketAndCapture(0, 0, heartbeat, attemptTimeout)
		if err == nil {
			f.log.Debugf("Dataplane ready after %v (attempt %d)", elapsed, attempt)
			return nil
		}

		f.log.Debugf("Heartbeat attempt %d failed (elapsed=%v): %v", attempt, elapsed, err)
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("dataplane not ready after %v (%d attempts)", time.Since(start).Round(time.Millisecond), attempt)
}

// runKni0Diagnostic dumps kni0 state to help debug restore failures.
// Logs kni0 link state, operstate, yanet process list.
func (f *TestFramework) runKni0Diagnostic(label string) {
	f.log.Debugf("=== kni0 diagnostic [%s] ===", label)

	output, err := f.ExecuteCommand("ip link show kni0 2>&1")
	if err != nil {
		f.log.Warnf("  kni0: failed to get link state: %v (output: %s)", err, output)
	} else {
		f.log.Debugf("  kni0 link: %s", output)
	}

	output, err = f.ExecuteCommand("cat /sys/class/net/kni0/operstate 2>&1")
	if err != nil {
		f.log.Warnf("  kni0 operstate: failed: %v", err)
	} else {
		f.log.Debugf("  kni0 operstate: %s", strings.TrimSpace(output))
	}

	output, err = f.ExecuteCommand("ps awux | grep [y]anet")
	if err != nil {
		f.log.Warnf("  yanet processes: failed to list: %v", err)
	} else if len(output) == 0 {
		f.log.Warn("  yanet processes: none found")
	} else {
		f.log.Debugf("  yanet processes: %s", output)
	}

	f.log.Debugf("=== End diagnostic [%s] ===", label)
}

// RestoreBooted restores the VM to the "booted" snapshot and re-establishes
// all connections. This is the primary per-test isolation primitive.
//
// Unlike RestoreAndReconnect("baseline"), RestoreBooted does not depend on a
// baseline snapshot being created at test startup. It only requires that the
// VM pool was started from a pre-prepared booted template overlay.
func (f *TestFramework) RestoreBooted() error {
	f.log.Infof("Restoring booted snapshot...")

	// Unmount 9P before loadvm (QEMU blocks loadvm when VirtFS is active).
	if err := f.Unmount9P(); err != nil {
		f.log.Debugf("Pre-restore 9P unmount (may be already unmounted): %v", err)
	}

	// Restore via monitor + serial reconnect.
	if err := f.qemu.RestoreBooted(); err != nil {
		return fmt.Errorf("TestFramework.RestoreBooted: %w", err)
	}

	// Remount 9P for test access to binaries and config files.
	if err := f.Mount9P(); err != nil {
		return fmt.Errorf("post-restore 9P mount: %w", err)
	}

	// Reset socket connections (forces DPDK virtio-user reconnect).
	f.ResetConnections()

	f.log.Infof("Booted restore complete")
	return nil
}

// RunWith is like Run but restores the named VM snapshot before executing
// the test function. This gives each subtest a guaranteed clean VM state
// matching the configuration at snapshot time.
//
// Use Run() (without snapshot restore) for subtests that intentionally
// build on the state left by previous subtests. Use RunWith() when each
// subtest needs full isolation.
func (f *TestFramework) RunWith(snapshot string, name string, fn func(fw *TestFramework, t *testing.T)) bool {
	if f.t == nil {
		panic("RunWith() can only be called on a framework created via ForTest()")
	}

	return f.t.Run(name, func(t *testing.T) {
		subFw := f.withTestName(t.Name())
		subFw.t = t

		if err := subFw.RestoreAndReconnect(snapshot); err != nil {
			t.Fatalf("failed to restore snapshot %q before test: %v", snapshot, err)
		}

		fn(subFw, t)
	})
}
