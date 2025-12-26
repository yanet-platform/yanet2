package framework

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	// CLI tool paths
	CLIBasePath    = "/mnt/target/release"
	CLIRoute       = CLIBasePath + "/yanet-cli-route"
	CLIBalancer    = CLIBasePath + "/yanet-cli-balancer"
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

// Global atomic counter for generating unique log IDs
var logIDCounter atomic.Uint32

var (
	CommonConfigCommands = []string{
		// Configure kni0 network interface
		"ip link set kni0 up",
		"ip nei add " + VMIPv6Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip nei add " + VMIPv4Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip addr add " + VMIPv4Host + "/24 dev kni0",

		// Configure L2 and L3 forwarding
		CLIForward + " update --cfg=forward0 --rules /mnt/yanet2/forward.yaml",

		// Configure routing
		CLIRoute + " insert --cfg route0 --via " + VMIPv6Gateway + " ::/0",
		CLIRoute + " insert --cfg route0 --via " + VMIPv4Gateway + " 0.0.0.0/0",

		CLIFunction + " update --name=virt --chains chain0:10=forward:forward0",
		CLIFunction + " update --name=test --chains chain2:1=forward:forward0,route:route0",

		CLIPipeline + " update --name=bootstrap --functions virt",
		CLIPipeline + " update --name=test --functions test",
		CLIPipeline + " update --name=dummy",

		CLIDevicePlain + " update --name=01:00.0 --input test:1 --output dummy:1",
		CLIDevicePlain + " update --name=virtio_user_kni0 --input bootstrap:1 --output dummy:1",
	}
)

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
// The counter is a uint16, allowing for 65536 unique IDs (0000-FFFF).
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
	// Use only lower 16 bits to ensure 4-character hex output
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
type F struct {
	qemu         *QEMUManager       // Virtual machine manager for test environment
	cli          *CLIManager        // Command-line interface manager for VM operations
	PacketParser *PacketParser      // Network packet parsing and analysis engine
	log          *zap.SugaredLogger // Logger for debugging and monitoring

	// Socket client cache for network interface communication
	socketClients *socketClientsCache // Cached socket clients with mutex

	// Test context
	testName string     // Name of the current test (empty for global framework)
	t        *testing.T // Testing context (nil for global framework)
}

// GlobalFramework is a safe wrapper that prevents direct access to framework methods.
// It provides two ways to access the framework:
//   - Global() - returns TestFramework with testName="global" for global operations in TestMain
//   - ForTest(t) - returns TestFramework bound to *testing.T for test-specific operations
//
// This design ensures that framework methods cannot be called without proper context.
//
// Example usage:
//
//	var globalFramework *GlobalFramework
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
type GlobalFramework struct {
	inner *F
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
	return func(fw *F) error {
		fw.log = log
		return nil
	}
}

// FrameworkOption defines functional options for configuring TestFramework instances.
// This pattern enables flexible initialization with optional parameters while
// maintaining backward compatibility and clean API design.
type FrameworkOption func(*F) error

// Config contains essential configuration parameters for initializing the test framework.
// It specifies the QEMU virtual machine image and working directory for test execution.
type Config struct {
	Name      string
	QEMUImage string // Path to the QEMU virtual machine image file
}

// New creates and initializes a new GlobalFramework instance with the specified configuration
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
//   - *GlobalFramework: Fully initialized framework wrapper
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
func New(config *Config, opts ...FrameworkOption) (*GlobalFramework, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	// Create framework instance with default values
	fw := &F{
		log: zap.NewNop().Sugar(), // default noop logger
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

	return &GlobalFramework{inner: fw}, nil
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
func (f *GlobalFramework) Global() *F {
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
func (f *GlobalFramework) ForTest(t *testing.T) *F {
	fwCopy := f.inner.withTestName(t.Name())
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
func (f *F) Start() error {
	// Start QEMU VM with socket networking
	if err := f.qemu.Start(); err != nil {
		return fmt.Errorf("failed to start QEMU: %w", err)
	}

	return nil
}

// Stop performs comprehensive cleanup of the test environment, ensuring proper
// resource deallocation and temporary file removal. This method should always
// be called when testing is complete to prevent resource leaks.
//
// The cleanup process includes:
//   - Closing all active socket client connections
//   - Terminating CLI manager connections
//   - Stopping and cleaning up the QEMU virtual machine
//   - Removing the working directory and all test artifacts

// Stop performs comprehensive cleanup of the test environment, ensuring proper
// resource deallocation and temporary file removal. This method should always
// be called when testing is complete to prevent resource leaks.
//
// The cleanup process includes:
//   - Closing all active socket client connections
//   - Terminating CLI manager connections
//   - Stopping and cleaning up the QEMU virtual machine
//   - Removing the working directory and all test artifacts
//
// Multiple cleanup errors are collected and returned as a combined error for
// comprehensive error reporting.
//
// Returns:
//   - error: A combined error if any cleanup operations fail, or nil if successful
//
// Example:
//
//	defer func() {
//	    if err := framework.Stop(); err != nil {
//	        log.Errorf("Cleanup failed: %v", err)
//	    }
//	}()
func (f *F) Stop() error {
	var errs []error

	// Lock the mutex to safely access the socketClients map
	f.socketClients.mutex.Lock()
	// Close all socket clients
	for _, client := range f.socketClients.clients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close socket client: %w", err))
		}
	}
	// Clear the map
	f.socketClients.clients = make(map[int]*SocketClient)
	f.socketClients.mutex.Unlock()

	// Close CLI connections
	if f.cli != nil {
		if err := f.cli.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close CLI: %w", err))
		}
	}

	// Stop QEMU VM
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
// A unique 8-character log ID is generated from the test name for compact logging.
//
// Parameters:
//   - testName: Name of the test (typically from t.Name())
//
// Returns:
//   - *TestFramework: A new framework instance with the test name set
func (f *F) withTestName(testName string) *F {
	logID := generateLogID(testName)
	namedLog := f.log.Named(logID)
	if testName != globalName {
		namedLog.Infof("Test '%s' will use log ID: %s", testName, logID)
	}

	fWithName := &F{
		qemu:          f.qemu,
		cli:           f.cli.WithLog(namedLog),
		PacketParser:  f.PacketParser,
		log:           namedLog,
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
func (f *F) SendPacketAndCapture(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) ([]byte, error) {
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

	// Send packet on input interface
	if err := inputClient.SendPacket(packet, inputDumpPath); err != nil {
		return nil, fmt.Errorf("failed to send packet: %w", err)
	}

	return outputClient.ReceivePacket(timeout, outputDumpPath)
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
func (f *F) SendPacketAndParse(inputIfaceIndex int, outputIfaceIndex int, packet []byte, timeout time.Duration) (*PacketInfo, *PacketInfo, error) {
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
func (f *F) GetSocketClient(ifaceIndex int) (*SocketClient, error) {
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
func (f *F) ExecuteCommand(command string) (string, error) {
	return f.cli.ExecuteCommand(command)
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
func (f *F) ExecuteCommands(commands ...string) ([]string, error) {
	return f.cli.ExecuteCommands(commands...)
}

// getDumpFilePaths returns dump file paths for a test.
// If debug is disabled or testName is empty, returns empty strings.
//
// Parameters:
//   - testName: Name of the test
//
// Returns:
//   - string: Input dump file path (empty if debug disabled)
//   - string: Output dump file path (empty if debug disabled)
func (f *F) getDumpFilePaths() (string, string) {
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
func (f *F) StartYANET(dataplaneConfig string, controlplaneConfig string) error {
	f.log.Info("Starting YANET in VM...")

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

	// Check if YANET binaries are available
	f.log.Debug("Checking YANET binary availability...")
	commands := []string{
		"ls -la /mnt/build/",
		"ls -la /mnt/build/dataplane/",
		"ls -la /mnt/build/controlplane/",
	}

	for _, cmd := range commands {
		output, err := f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("YANET binary check failed: %w", err)
		}
		if strings.Contains(output, "such") {
			return fmt.Errorf("YANET binary check failed: %s", output)
		}
		f.log.Debugf("Command: %s\nOutput: %s", cmd, output)
	}

	// Load required kernel modules
	f.log.Debug("Loading required kernel modules...")
	moduleCommands := []string{
		"sudo modprobe vfio-pci",
	}

	for _, cmd := range moduleCommands {
		output, err := f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to load kernel modules: %w", err)
		}
		f.log.Debugf("Module command: %s\nOutput: %s", cmd, output)
	}

	f.log.Debug("Configuring network interfaces for DPDK...")

	// Check PCI devices status
	statusCmd := "/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --status"
	output, err := f.cli.ExecuteCommand(statusCmd)
	if err != nil {
		return fmt.Errorf("DPDK devbind status check failed: %v", err)
	}
	f.log.Debugf("DPDK devices status: %s", output)

	// Bind network interfaces to DPDK driver
	// Based on the QEMU configuration, we need to bind the virtio interfaces
	bindCommands := []string{
		"/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 01:00.0",
		"/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --bind=vfio-pci 02:00.0",
	}

	for _, cmd := range bindCommands {
		output, err = f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("interface bind failed: %s, %w", cmd, err)
		}
		f.log.Debugf("DPDK bind command: %s\nOutput: %s", cmd, output)
	}

	// Verify that config files are accessible in VM
	f.log.Debug("Verifying config files are accessible in VM...")
	verifyCommands := []string{
		"ls -la /mnt/config/dataplane.yaml",
		"ls -la /mnt/config/controlplane.yaml",
	}

	for _, cmd := range verifyCommands {
		output, err := f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("config file verification failed: %w", err)
		}
		f.log.Debugf("Config verification: %s\nOutput: %s", cmd, output)
	}

	// Start dataplane in background using config from mounted directory
	f.log.Debug("Starting YANET dataplane...")
	dataplaneCmd := "bash -c 'nohup /mnt/build/dataplane/yanet-dataplane /mnt/config/dataplane.yaml > /mnt/logs/yanet-dataplane.log 2>&1 &'"
	output, err = f.cli.ExecuteCommand(dataplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}
	f.log.Infof("Dataplane started: %s", output)
	f.log.Infof("Wait for the kni0 device to appear")
	err = f.WaitOutputPresent("ip link", func(output string) bool {
		return strings.Contains(output, "kni0")
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}

	// Start controlplane in background using config from mounted directory
	f.log.Debug("Starting YANET controlplane...")
	controlplaneCmd := "bash -c 'nohup /mnt/build/controlplane/yanet-controlplane -c /mnt/config/controlplane.yaml > /mnt/logs/yanet-controlplane.log 2>&1 &'"
	output, err = f.cli.ExecuteCommand(controlplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}
	f.log.Infof("Controlplane started: %s", output)

	// Verify services are running
	f.log.Debug("Verifying YANET services are running...")

	err = f.WaitOutputPresent("cat /mnt/logs/yanet-controlplane.log", func(output string) bool {
		return strings.Contains(output, "updated nexthop cache")
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}

	checkCmds := []string{
		"ps awux | grep [y]anet-dataplane",
		"ps awux | grep [y]anet-controlplane",
		"cat /mnt/logs/yanet-dataplane.log",
		"cat /mnt/logs/yanet-controlplane.log",
	}

	_, err = f.cli.ExecuteCommands(checkCmds...)
	if err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	f.log.Info("YANET services started successfully")
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
func (f *F) WaitOutputPresent(cmd string, checker func(string) bool, timeout time.Duration) error {
	// Wait for flags to be applied
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		output, err := f.cli.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to check output: %w", err)
		}

		// Check if flags match expected state
		if checker(output) {
			return nil
		}
		// Wait before next check
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for output to be present: %s", cmd)
}

func (f *F) CreateConfigFile(name string, config string) error {
	// Get the config directory path from QEMU manager
	configDir := f.qemu.ConfigDir
	if configDir == "" {
		return fmt.Errorf("config directory not set in QEMU manager")
	}

	// Create dataplane config file
	configPath := filepath.Join(configDir, name)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write config to %s: %w", configPath, err)
	}
	f.log.Debugf("Created config: %s", configPath)
	return nil
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
func (f *F) createConfigFiles(dataplaneConfig string, controlplaneConfig string) error {
	f.log.Debug("Creating configuration files on host in mounted directory...")

	// Get the config directory path from QEMU manager
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

	f.log.Debug("Configuration files created successfully on host")
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
func (f *F) WaitForReady(timeout time.Duration) error {
	return f.qemu.WaitForReady(timeout)
}

func (f *F) GetSocketPaths() []string {
	return f.qemu.SocketPaths
}

// ValidateCounter validates a counter value against expected value.
// This method checks statistic counters from yanet modules using CLI commands.
//
// Parameters:
//   - counterName: Name/identifier of the counter to validate (e.g., "flow_1", "packets_received")
//   - expectedValue: Expected value for the counter
//
// Returns:
//   - error: Error if validation fails or counter cannot be accessed
//
// Note: Current implementation is a placeholder that logs the validation attempt.
// Full implementation will require CLI access to yanet statistics.
func (f *F) ValidateCounter(counterName string, expectedValue int) error {
	f.log.Debugf("Validating counter %s with expected value %d", counterName, expectedValue)

	// TODO: Implement actual counter validation using yanet CLI
	// This will require:
	// 1. CLI command to query counters (e.g., yanet-cli-stats)
	// 2. Parse response to get actual counter value
	// 3. Compare actual vs expected value
	// 4. Return error if mismatch

	// For now, just log the validation attempt
	f.log.Infof("Counter validation placeholder: %s = %d (actual validation not implemented)", counterName, expectedValue)

	// Simulate validation - always succeed for now
	return nil
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
func (f *F) Run(name string, fn func(fw *F, t *testing.T)) bool {
	if f.t == nil {
		panic("Run() can only be called on TestFramework created via ForTest()")
	}
	return f.t.Run(name, func(t *testing.T) {
		// Create a new TestFramework with the subtest's full name
		subFw := f.withTestName(t.Name())
		subFw.t = t
		fn(subFw, t)
	})
}
