package framework

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	CLIPipeline    = CLIBasePath + "/yanet-cli-pipeline"
	CLIFunction    = CLIBasePath + "/yanet-cli-function"
	CLIDevicePlain = CLIBasePath + "/yanet-cli-device-plain"
	CLIDecap       = CLIBasePath + "/yanet-cli-decap"
	CLIForward     = CLIBasePath + "/yanet-cli-forward"
	CLIGeneric     = CLIBasePath + "/yanet-cli"
)

var (
	CommonConfigCommands = []string{
		// Configure kni0 network interface
		"ip link set kni0 up",
		"ip nei add " + VMIPv6Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip nei add " + VMIPv4Gateway + " lladdr " + SrcMAC + " dev kni0",
		"ip addr add " + VMIPv4Host + "/24 dev kni0",

		// Configure L2 and L3 forwarding
		CLIForward + " l2-enable --cfg=forward0 --instances 0 --src 01:00.0 --dst virtio_user_kni0",
		CLIForward + " l2-enable --cfg=forward0 --instances 0 --src virtio_user_kni0 --dst 01:00.0",
		CLIForward + " l3-add --cfg=forward0 --instances 0 --src 01:00.0 --dst virtio_user_kni0 --net " + VMIPv4Host + "/32",
		CLIForward + " l3-add --cfg=forward0 --instances 0 --src 01:00.0 --dst virtio_user_kni0 --net " + VMIPv6Host + "/64",
		CLIForward + " l3-add --cfg=forward0 --instances 0 --src 01:00.0 --dst virtio_user_kni0 --net ff02::/16",
		CLIForward + " l3-add --cfg=forward0 --instances 0 --src virtio_user_kni0 --dst 01:00.0 --net 0.0.0.0/0",
		CLIForward + " l3-add --cfg=forward0 --instances 0 --src virtio_user_kni0 --dst 01:00.0 --net ::/0",

		// Configure routing
		CLIRoute + " insert --cfg route0 --instances 0 --via " + VMIPv6Gateway + " ::/0",
		CLIRoute + " insert --cfg route0 --instances 0 --via " + VMIPv4Gateway + " 0.0.0.0/0",

		CLIFunction + " update --name=virt --chains chain0:10=forward:forward0 --instance=0",
		CLIFunction + " update --name=test --chains chain2:1=forward:forward0,route:route0 --instance=0",

		CLIPipeline + " update --name=bootstrap --functions virt --instance=0",
		CLIPipeline + " update --name=test --functions test --instance=0",
		CLIPipeline + " update --name=dummy --instance=0",

		CLIDevicePlain + " update --instance=0 --name=01:00.0 --input test:1 --output dummy:1",
		CLIDevicePlain + " update --instance=0 --name=virtio_user_kni0 --input bootstrap:1 --output dummy:1",
	}
	DebugCommands = []string{
		"cp /var/log/yanet-controlplane.log /mnt/build/ 2>/dev/null || echo 'No controlplane log found'",
		"cp /var/log/yanet-dataplane.log /mnt/build/ 2>/dev/null || echo 'No dataplane log found'",
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

// TestFramework represents the main test framework structure for YANET functional testing.
// It orchestrates QEMU virtual machine management, CLI command execution, packet processing,
// and network socket communication to provide a comprehensive testing environment.
//
// The framework manages:
//   - QEMU virtual machine lifecycle and networking
//   - CLI command execution within the VM
//   - Packet parsing and analysis capabilities
//   - Socket-based network communication with VM interfaces
//   - Working directory for test artifacts and temporary files
//
// All operations are thread-safe through internal synchronization mechanisms.
type TestFramework struct {
	QEMU         *QEMUManager       // Virtual machine manager for test environment
	CLI          *CLIManager        // Command-line interface manager for VM operations
	PacketParser *PacketParser      // Network packet parsing and analysis engine
	WorkDir      string             // Working directory for test files and artifacts
	log          *zap.SugaredLogger // Logger for debugging and monitoring

	// Socket client cache for network interface communication
	socketClients map[int]*SocketClient // Cached socket clients indexed by interface number
	clientsMutex  sync.Mutex            // Protects concurrent access to socketClients map
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
	QEMUImage string // Path to the QEMU virtual machine image file
	WorkDir   string // Working directory for test artifacts (auto-created if empty)
}

// New creates and initializes a new TestFramework instance with the specified configuration
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
//   - *TestFramework: Fully initialized test framework instance
//   - error: An error if initialization fails or configuration is invalid
//
// Example:
//
//	config := &Config{
//	    QEMUImage: "/path/to/vm-image.qcow2",
//	    WorkDir:   "/tmp/yanet-tests",
//	}
//	fw, err := New(config, WithLog(logger))
//	if err != nil {
//	    log.Fatalf("Failed to create framework: %v", err)
//	}
func New(config *Config, opts ...FrameworkOption) (*TestFramework, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if config.WorkDir == "" {
		config.WorkDir = filepath.Join(os.TempDir(), "yanet-test")
	}
	if err := os.MkdirAll(config.WorkDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	// Create framework instance with default values
	fw := &TestFramework{
		WorkDir:       config.WorkDir,
		log:           zap.NewNop().Sugar(), // default noop logger
		socketClients: make(map[int]*SocketClient),
	}

	for _, opt := range opts {
		if err := opt(fw); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if fw.QEMU == nil {
		// Initialize QEMU manager
		qemu, err := NewQEMUManager(config.QEMUImage, fw.log)
		if err != nil {
			return nil, fmt.Errorf("failed to create QEMU manager: %w", err)
		}
		fw.QEMU = qemu
	}

	if fw.CLI == nil {
		// Initialize CLI manager
		cli, err := NewCLIManager(fw.QEMU, CLIWithLog(fw.log))
		if err != nil {
			return nil, fmt.Errorf("failed to create CLI manager: %w", err)
		}
		fw.CLI = cli
	}

	if fw.PacketParser == nil {
		fw.PacketParser = NewPacketParser()
	}

	return fw, nil
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
func (f *TestFramework) Start() error {
	// Start QEMU VM with socket networking
	if err := f.QEMU.Start(); err != nil {
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
func (f *TestFramework) Stop() error {
	var errs []error

	// Lock the mutex to safely access the socketClients map
	f.clientsMutex.Lock()
	// Close all socket clients
	for _, client := range f.socketClients {
		if err := client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close socket client: %w", err))
		}
	}
	// Clear the map
	f.socketClients = make(map[int]*SocketClient)
	f.clientsMutex.Unlock()

	// Close CLI connections
	if f.CLI != nil {
		if err := f.CLI.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close CLI: %w", err))
		}
	}

	// Stop QEMU VM
	if err := f.QEMU.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("failed to stop QEMU: %w", err))
	}

	// Cleanup work directory
	if err := os.RemoveAll(f.WorkDir); err != nil {
		errs = append(errs, fmt.Errorf("failed to cleanup work directory: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during cleanup: %v", errs)
	}
	return nil
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
//	response, err := fw.SendPacketAndCapture(0, 1, packetData, 5*time.Second)
//	if err != nil {
//	    log.Fatalf("Packet transmission failed: %v", err)
//	}
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

	// Connect to sockets
	if err := inputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to input socket: %w", err)
	}

	if err := outputClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to output socket: %w", err)
	}

	// Send packet on input interface
	if err := inputClient.SendPacket(packet); err != nil {
		return nil, fmt.Errorf("failed to send packet: %w", err)
	}

	return outputClient.ReceivePacket(timeout)
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
//	input, output, err := fw.SendPacketAndParse(0, 1, packetData, 5*time.Second)
//	if err != nil {
//	    log.Fatalf("Packet processing failed: %v", err)
//	}
//	log.Infof("Sent: %s, Received: %s", input.String(), output.String())
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

// GetSocketClient retrieves or creates a socket client for the specified network
// interface. The method implements caching to reuse existing connections and
// ensures thread-safe access to the socket client pool.
//
// The method handles:
//   - Interface index validation against available QEMU socket paths
//   - Thread-safe access to the socket client cache
//   - Automatic socket client creation for new interfaces
//   - Client caching for performance optimization
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
	if ifaceIndex >= len(f.QEMU.SocketPaths) {
		return nil, fmt.Errorf("interface index %d out of range, available interfaces: 0-%d", ifaceIndex, len(f.QEMU.SocketPaths)-1)
	}

	// Lock the mutex to safely access the socketClients map
	f.clientsMutex.Lock()
	defer f.clientsMutex.Unlock()

	// Check if we already have a client for this interface
	if client, exists := f.socketClients[ifaceIndex]; exists {
		return client, nil
	}

	// Create a new client
	socketPath := f.QEMU.SocketPaths[ifaceIndex]
	client, err := NewSocketClient(socketPath, SocketClientWithLog(f.log.With("interface", ifaceIndex)))
	if err != nil {
		return nil, fmt.Errorf("failed to create Unix socket client for interface %d (path %s): %w", ifaceIndex, socketPath, err)
	}

	// Store the client in the map
	f.socketClients[ifaceIndex] = client
	return client, nil
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
	f.log.Info("Starting YANET in VM...")

	if !f.QEMU.IsVMReady() {
		return fmt.Errorf("vm is not ready")
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
		output, err := f.CLI.ExecuteCommand(cmd)
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
		output, err := f.CLI.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("failed to load kernel modules: %w", err)
		}
		f.log.Debugf("Module command: %s\nOutput: %s", cmd, output)
	}

	f.log.Debug("Configuring network interfaces for DPDK...")

	// Check PCI devices status
	statusCmd := "/mnt/yanet2/subprojects/dpdk/usertools/dpdk-devbind.py --status"
	output, err := f.CLI.ExecuteCommand(statusCmd)
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
		output, err = f.CLI.ExecuteCommand(cmd)
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
		output, err := f.CLI.ExecuteCommand(cmd)
		if err != nil {
			return fmt.Errorf("config file verification failed: %w", err)
		}
		f.log.Debugf("Config verification: %s\nOutput: %s", cmd, output)
	}

	// Start dataplane in background using config from mounted directory
	f.log.Debug("Starting YANET dataplane...")
	dataplaneCmd := "bash -c 'nohup /mnt/build/dataplane/yanet-dataplane /mnt/config/dataplane.yaml > /var/log/yanet-dataplane.log 2>&1 &'"
	output, err = f.CLI.ExecuteCommand(dataplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}
	f.log.Infof("Dataplane started: %s", output)
	err = f.waitOutputPresent("ip link", func(output string) bool {
		return strings.Contains(output, "kni0")
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start dataplane: %w", err)
	}

	// Start controlplane in background using config from mounted directory
	f.log.Debug("Starting YANET controlplane...")
	controlplaneCmd := "bash -c 'nohup /mnt/build/controlplane/yanet-controlplane -c /mnt/config/controlplane.yaml > /var/log/yanet-controlplane.log 2>&1 &'"
	output, err = f.CLI.ExecuteCommand(controlplaneCmd)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}
	f.log.Infof("Controlplane started: %s", output)

	// Verify services are running
	f.log.Debug("Verifying YANET services are running...")

	err = f.waitOutputPresent("cat /var/log/yanet-controlplane.log", func(output string) bool {
		return strings.Contains(output, "updated nexthop cache")
	}, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to start controlplane: %w", err)
	}

	checkCmds := []string{
		"ps aux | grep yanet-dataplane | grep -v grep",
		"ps aux | grep yanet-controlplane | grep -v grep",
		"cat /var/log/yanet-dataplane.log",
		"cat /var/log/yanet-controlplane.log",
	}

	_, err = f.CLI.ExecuteCommands(checkCmds...)
	if err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	f.log.Info("YANET services started successfully")
	return nil
}

// waitOutputPresent repeatedly executes a command until the output satisfies the
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
//	err := fw.waitOutputPresent("ps aux | grep yanet", func(output string) bool {
//	    return strings.Contains(output, "yanet-dataplane")
//	}, 30*time.Second)
func (f *TestFramework) waitOutputPresent(cmd string, checker func(string) bool, timeout time.Duration) error {
	// Wait for flags to be applied
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		output, err := f.CLI.ExecuteCommand(cmd)
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
	f.log.Debug("Creating configuration files on host in mounted directory...")

	// Get the config directory path from QEMU manager
	configDir := f.QEMU.ConfigDir
	if configDir == "" {
		return fmt.Errorf("config directory not set in QEMU manager")
	}

	// Create dataplane config file
	dataplaneConfigPath := filepath.Join(configDir, "dataplane.yaml")
	if err := os.WriteFile(dataplaneConfigPath, []byte(dataplaneConfig), 0644); err != nil {
		return fmt.Errorf("failed to write dataplane config to %s: %w", dataplaneConfigPath, err)
	}
	f.log.Debugf("Created dataplane config: %s", dataplaneConfigPath)

	// Create controlplane config file
	controlplaneConfigPath := filepath.Join(configDir, "controlplane.yaml")
	if err := os.WriteFile(controlplaneConfigPath, []byte(controlplaneConfig), 0644); err != nil {
		return fmt.Errorf("failed to write controlplane config to %s: %w", controlplaneConfigPath, err)
	}
	f.log.Debugf("Created controlplane config: %s", controlplaneConfigPath)

	// Verify files were created successfully
	if _, err := os.Stat(dataplaneConfigPath); err != nil {
		return fmt.Errorf("dataplane config file not found after creation: %w", err)
	}
	if _, err := os.Stat(controlplaneConfigPath); err != nil {
		return fmt.Errorf("controlplane config file not found after creation: %w", err)
	}

	f.log.Debug("Configuration files created successfully on host")
	return nil
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
func (f *TestFramework) ValidateCounter(counterName string, expectedValue int) error {
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
