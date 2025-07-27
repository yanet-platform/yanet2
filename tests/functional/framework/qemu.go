package framework

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// QEMUManager handles QEMU VM lifecycle and operations
type QEMUManager struct {
	ImagePath   string
	WorkDir     string
	Command     *exec.Cmd
	BinariesDir string
	ConfigDir   string
	BuildDir    string
	TargetDir   string
	SerialPath  string
	MonitorPath string
	SocketPaths []string
	isReady     bool
	readySignal chan bool
	monitorConn net.Conn
	serialConn  net.Conn
	log         *zap.SugaredLogger
	readyMutex  sync.RWMutex // Protects isReady field
	instanceID  string       // Unique ID for this VM instance
}

// NewQEMUManager creates a new QEMU manager instance
func NewQEMUManager(imagePath string, logger *zap.SugaredLogger) (*QEMUManager, error) {
	// Generate unique instance ID for parallel execution
	instanceID := fmt.Sprintf("yanet-vm-%d-%d", os.Getpid(), time.Now().UnixNano())
	workDir := filepath.Join(os.TempDir(), instanceID)

	// Determine project root directory
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, fmt.Errorf("Failed to determine project root directory: %w", err)
	}
	buildDir := filepath.Join(projectRoot, "build")
	targetDir := filepath.Join(projectRoot, "target")

	return &QEMUManager{
		ImagePath:   imagePath,
		WorkDir:     workDir,
		BinariesDir: filepath.Join(workDir, "bin"),
		ConfigDir:   filepath.Join(workDir, "config"),
		BuildDir:    buildDir,
		TargetDir:   targetDir,
		readySignal: make(chan bool, 1),
		log:         logger,
		instanceID:  instanceID,
		SerialPath:  filepath.Join(workDir, "serial.sock"),
		MonitorPath: filepath.Join(workDir, "monitor.sock"),
	}, nil
}

// Start launches a QEMU VM with the specified configuration
func (q *QEMUManager) Start() error {
	// Check if QEMU is available
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		return fmt.Errorf("qemu-system-x86_64 not found in PATH: %w", err)
	}

	// Check if image file exists
	if _, err := os.Stat(q.ImagePath); err != nil {
		return fmt.Errorf("QEMU image %s not found: %w", q.ImagePath, err)
	}

	// Create working directories
	q.log.Debug("Creating binaries directory...")
	if err := os.MkdirAll(q.BinariesDir, 0755); err != nil {
		return fmt.Errorf("failed to create binaries directory: %w", err)
	}
	q.log.Debug("Binaries directory created.")

	q.log.Debug("Creating config directory...")
	if err := os.MkdirAll(q.ConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	fmt.Println("DEBUG: Config directory created.")

	// Generate socket paths for Unix stream interface
	fmt.Println("DEBUG: Generating socket paths...")
	q.SocketPaths = make([]string, 2) // Assuming 2 interfaces for now
	for i := range q.SocketPaths {
		// Use /tmp/ directory like in working Makefile configuration
		q.SocketPaths[i] = filepath.Join("/tmp", fmt.Sprintf("yanetvm_%s_sockdev_%d.sock", q.instanceID, i))
	}
	fmt.Println("DEBUG: Socket paths generated.")

	// Detect OS
	osType := runtime.GOOS

	// Base arguments
	args := []string{
		"-name", "yanet-test-vm",
		"-smp", "4",
		"-m", "8G",
		"-machine", "q35,kernel-irqchip=split",
		"-cpu", "max",
		"-device", "intel-iommu,intremap=on,device-iotlb=on",
		"-device", "ioh3420,id=pcie.1,chassis=1",
		"-device", "ioh3420,id=pcie.2,chassis=2",
	}

	// OS-specific configuration
	if osType == "linux" {
		args = append(args, "-enable-kvm")
	}

	// Drive configuration
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", q.ImagePath),
	)
	// Network interface configuration
	args = append(args,
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0",
		"-netdev", "stream,id=net1,server=on,addr.type=unix,addr.path="+q.SocketPaths[0],
		"-device", "virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10",
		"-netdev", "stream,id=net2,server=on,addr.type=unix,addr.path="+q.SocketPaths[1],
		"-device", "virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10",
	)

	// Add 9P filesystem sharing for YANET binaries and configuration
	// This allows the VM to access host files for testing
	// Match the mount configuration used in Makefile
	args = append(args,
		// Share temporary directory for binaries
		"-fsdev", "local,id=fsdev0,path="+q.BinariesDir+",security_model=none",
		"-device", "virtio-9p-pci,fsdev=fsdev0,mount_tag=binaries",
		// Share temporary directory for configuration
		"-fsdev", "local,id=fsdev1,path="+q.ConfigDir+",security_model=none",
		"-device", "virtio-9p-pci,fsdev=fsdev1,mount_tag=config",
		// Share build directory
		//"-fsdev", "local,id=fsdev2,path="+q.BuildDir+",security_model=none,readonly=on",
		"-fsdev", "local,id=fsdev2,path="+q.BuildDir+",security_model=none",
		"-device", "virtio-9p-pci,fsdev=fsdev2,mount_tag=build",
		// Share target directory
		"-fsdev", "local,id=fsdev3,path="+q.TargetDir+",security_model=none,readonly=on",
		"-device", "virtio-9p-pci,fsdev=fsdev3,mount_tag=target",
		// Share all code directory
		"-fsdev", "local,id=fsdev4,path="+q.TargetDir+"/..,security_model=none,readonly=on",
		"-device", "virtio-9p-pci,fsdev=fsdev4,mount_tag=yanet2",
	)

	qemuLogfile := filepath.Join(q.WorkDir, "yanet-test-vm.log")
	// Logging and display options - using unix sockets
	args = append(args,
		"-D", qemuLogfile,
		"-serial", fmt.Sprintf("unix:%s,server=on", q.SerialPath),
		"-monitor", fmt.Sprintf("unix:%s,server=on", q.MonitorPath),
		"-display", "none",
		"-no-reboot",
	)
	// Create log file for QEMU output
	logFile := filepath.Join(q.WorkDir, "qemu-output.log")
	logWriter, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logWriter.Close()

	// Start QEMU
	q.Command = exec.Command("qemu-system-x86_64", args...)

	q.log.Debugf("Starting QEMU with command: %s %s", q.Command.Path, strings.Join(args, " "))
	q.log.Debugf("QEMU logs will be written to: %s", logFile)

	// Create stderr pipe for logging
	stderr, err := q.Command.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start goroutine to capture stderr for logging
	go q.captureStderr(stderr, logWriter)

	// Start QEMU process
	if err := q.Command.Start(); err != nil {
		return fmt.Errorf("failed to start QEMU: %w", err)
	}

	// Wait a bit and check if process is still running
	time.Sleep(1 * time.Second)

	// Check if process exited with error
	if q.Command.Process == nil || q.Command.Process.Pid == 0 || (q.Command.ProcessState != nil && q.Command.ProcessState.Exited()) {
		// Read log file to see what went wrong
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return fmt.Errorf("QEMU exited early. Log content: %s; QEMU log content: %s", string(logContent), string(logContentQ))
	}

	p, err := os.FindProcess(q.Command.Process.Pid)
	if err != nil || p == nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return fmt.Errorf("QEMU proccess not found. Log content: %s; QEMU log content: %s", string(logContent), string(logContentQ))
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return fmt.Errorf("failed to signal QEMU process(may be dead): %w. Log content: %s; QEMU log content: %s", err, string(logContent), string(logContentQ))
	}

	q.log.Debugf("QEMU process started with PID: %d", q.Command.Process.Pid)
	q.log.Debugf("QEMU process state: %v", q.Command.ProcessState)

	if err := q.connectToMonitor(); err != nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return fmt.Errorf("failed to connect to monitor (may be dead): %w. Log content: %s; QEMU log content: %s", err, string(logContent), string(logContentQ))
	}
	q.log.Debugf("Successfully connected to monitor at %s", q.MonitorPath)

	// Connect to serial console via unix socket
	if err := q.connectToSerial(); err != nil {
		return fmt.Errorf("failed to connect to serial console: %w", err)
	}
	q.log.Debugf("Successfully connected to serial console at %s", q.SerialPath)

	go q.monitorVMReadiness()

	return nil
}

// Stop terminates the QEMU VM and cleans up resources
func (q *QEMUManager) Stop() error {
	var errs []error

	// Close connections first (avoid double closing)
	if q.monitorConn != nil {
		if err := q.monitorConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close monitor connection: %w", err))
		}
		q.monitorConn = nil
	}
	if q.serialConn != nil {
		if err := q.serialConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close serial connection: %w", err))
		}
		q.serialConn = nil
	}

	// Kill QEMU process if running
	if q.Command != nil && q.Command.Process != nil {
		if err := q.Command.Process.Kill(); err != nil {
			errs = append(errs, fmt.Errorf("failed to kill QEMU process: %w", err))
		}
	}

	// Cleanup working directory
	if err := os.RemoveAll(q.WorkDir); err != nil {
		errs = append(errs, fmt.Errorf("failed to cleanup working directory: %w", err))
	}

	for _, path := range q.SocketPaths {
		if err := os.Remove(path); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove socket file: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during cleanup: %v", errs)
	}
	return nil
}

// GetStdin returns the stdin pipe for the QEMU process
func (q *QEMUManager) GetStdin() io.WriteCloser {
	// Try to connect if not already connected
	if q.serialConn == nil {
		q.connectToSerial()
	}
	return q.serialConn
}

// GetStdout returns the stdout pipe for the QEMU process
func (q *QEMUManager) GetStdout() io.ReadCloser {
	// Try to connect if not already connected
	if q.serialConn == nil {
		q.connectToSerial()
	}
	return q.serialConn
}

// captureStderr captures QEMU stderr for logging
func (q *QEMUManager) captureStderr(stderr io.ReadCloser, logWriter *os.File) {
	go func() {
		defer stderr.Close()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logWriter.WriteString("STDERR: " + line + "\n")
		}
	}()
}

// connectToMonitor connects to QEMU monitor interface via unix socket
func (q *QEMUManager) connectToMonitor() error {
	// Try multiple times to connect to monitor
	for i := 0; i < 10; i++ {
		// Create connection for monitoring
		conn, err := net.Dial("unix", q.MonitorPath)
		if err != nil {
			q.log.Debugf("Monitor connection attempt %d to %s failed: %v", i+1, q.MonitorPath, err)
			time.Sleep(1 * time.Second)
			continue
		}

		// Use connection
		q.monitorConn = conn
		q.log.Debugf("Successfully connected to monitor at %s", q.MonitorPath)

		return nil
	}

	return fmt.Errorf("failed to connect to monitor after 10 attempts")
}

// connectToSerial connects to QEMU serial console via unix socket
func (q *QEMUManager) connectToSerial() error {
	// Try multiple times to connect to serial console
	for i := 0; i < 10; i++ {
		// Create connection for commands
		conn, err := net.Dial("unix", q.SerialPath)
		if err != nil {
			q.log.Debugf("Serial connection attempt %d to %s failed: %v", i+1, q.SerialPath, err)
			time.Sleep(1 * time.Second)
			continue
		}

		// Use connection
		q.serialConn = conn
		q.log.Debugf("Successfully connected to serial console at %s", q.SerialPath)

		return nil
	}

	return fmt.Errorf("failed to connect to serial console after 10 attempts")
}

// monitorVMReadiness monitors serial console output to detect when VM is ready
func (q *QEMUManager) monitorVMReadiness() {
	if q.serialConn == nil {
		log.Default().Println("fail to monitor VM readiness")
		return
	}

	scanner := bufio.NewScanner(q.serialConn)

	for scanner.Scan() {
		line := scanner.Text()
		q.log.Debugf("VM output: %s", line)

		// If we see the unminimize message, send Enter to activate prompt
		if strings.Contains(line, "To restore this content, you can run the 'unminimize' command") {
			q.log.Debug("Unminimize message seen, sending Enter to activate prompt")
			if q.serialConn != nil {
				q.serialConn.Write([]byte("\n"))
			}
		}

		// Check if VM is ready - look for shell prompt
		if strings.Contains(line, "root@yanet-vm:~#") {
			q.setVMReady(true)
			q.log.Debug("VM is ready!")
			close(q.readySignal)
			return
		}
	}
}

// WaitForReady waits for VM to become ready with timeout
func (q *QEMUManager) WaitForReady(timeout time.Duration) error {
	if q.IsVMReady() {
		q.log.Debug("VM is already ready")
		return nil
	}

	select {
	case <-q.readySignal:
		q.log.Debug("Got ready signal")
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("VM did not become ready within %v", timeout)
	}
}

// IsVMReady returns whether the VM is ready
func (q *QEMUManager) IsVMReady() bool {
	q.readyMutex.RLock()
	defer q.readyMutex.RUnlock()
	return q.isReady
}

// setVMReady sets the VM ready state
func (q *QEMUManager) setVMReady(ready bool) {
	q.readyMutex.Lock()
	defer q.readyMutex.Unlock()
	q.isReady = ready
}
