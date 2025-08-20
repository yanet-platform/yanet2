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
	SrcMAC = "52:54:00:6b:ff:a1"
	DstMAC = "52:54:00:6b:ff:a5"
)

var (
	CommonConfigCommands = []string{
		// Configure kni0 network interface
		"ip link set kni0 up",
		"ip nei add fe80::1 lladdr " + SrcMAC + " dev kni0",
		"ip nei add 203.0.113.1 lladdr " + SrcMAC + " dev kni0",
		"ip addr add 203.0.113.14/24 dev kni0",

		// Configure L2 and L3 forwarding
		"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 0 --dst 1",
		"/mnt/target/release/yanet-cli-forward l2-enable --cfg=forward0 --instances 0 --src 1 --dst 0",
		"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net 203.0.113.14/32",
		"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net fe80::5054:ff:fe6b:ffa5/64",
		"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 0 --dst 1 --net ff02::/16",
		"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net 0.0.0.0/0",
		"/mnt/target/release/yanet-cli-forward l3-add --cfg=forward0 --instances 0 --src 1 --dst 0 --net ::/0",

		// Configure routing
		"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via fe80::1 ::/0",
		"/mnt/target/release/yanet-cli-route insert --cfg route0 --instances 0 --via 203.0.113.1 0.0.0.0/0",
	}
	DebugCommands = []string{
		"cp /var/log/yanet-controlplane.log /mnt/build/ 2>/dev/null || echo 'No controlplane log found'",
		"cp /var/log/yanet-dataplane.log /mnt/build/ 2>/dev/null || echo 'No dataplane log found'",
	}
)

func MustParseMAC(mac string) net.HardwareAddr {
	hwAddr, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}
	return hwAddr
}

// TestFramework represents the main test framework structure
type TestFramework struct {
	QEMU         *QEMUManager
	CLI          *CLIManager
	PacketParser *PacketParser
	WorkDir      string
	log          *zap.SugaredLogger

	// Socket client cache
	socketClients map[int]*SocketClient
	clientsMutex  sync.Mutex
}

// WithLog sets the logger for the TestFramework
func WithLog(log *zap.SugaredLogger) FrameworkOption {
	return func(fw *TestFramework) error {
		fw.log = log
		return nil
	}
}

// FrameworkOption defines functional options for TestFramework
type FrameworkOption func(*TestFramework) error

// Config contains test framework configuration
type Config struct {
	QEMUImage string
	WorkDir   string
}

// New creates a new test framework instance
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

// Start initializes the test environment
func (f *TestFramework) Start() error {
	// Start QEMU VM with socket networking
	if err := f.QEMU.Start(); err != nil {
		return fmt.Errorf("failed to start QEMU: %w", err)
	}

	return nil
}

// Stop cleans up the test environment
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

// SendPacketAndCapture sends a packet and captures any response (without verification)
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

// SendPacketAndParse sends a packet, captures the response, and parses both packets
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

// GetSocketClient returns a socket client for the specified interface
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

// StartYANET starts and configures YANET services in the VM using provided dataplane and controlplane configuration files
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
	f.log.Info("Verifying config files are accessible in VM...")
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

// createConfigFiles creates configuration files in the mounted config directory on the host
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
