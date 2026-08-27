package framework

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// QEMUManager handles the complete lifecycle and operations of QEMU virtual machines
// for YANET functional testing. It provides comprehensive VM management including
// startup, networking configuration, filesystem sharing, and graceful shutdown.
//
// The manager supports:
//   - QEMU VM lifecycle management with proper resource cleanup
//   - Unix socket-based networking for packet injection and capture
//   - 9P filesystem sharing for host-VM file exchange
//   - Serial console and monitor interface access
//   - VM readiness detection and synchronization
//   - Parallel test execution with unique instance isolation
//
// All operations are thread-safe and support concurrent access patterns
// required for comprehensive network testing scenarios.
type QEMUManager struct {
	Name             string
	ImagePath        string
	WorkDir          string
	Command          *exec.Cmd
	LogsDir          string
	ConfigDir        string
	BuildDir         string
	TargetDir        string
	SerialPath       string
	MonitorPath      string
	SocketPaths      []string
	isReady          bool
	readySignal      chan bool
	Ninepmounted     atomic.Bool
	monitorConn      net.Conn
	serialConn       net.Conn
	serialBuffer     strings.Builder
	serialMutex      sync.Mutex
	serialLog        atomic.Value
	log              *zap.SugaredLogger
	readyMutex       sync.RWMutex
	instanceID       string
	sshPort          int
	serialReaderDone chan struct{}
	// processExit is closed when the QEMU process for the current Start()
	// call has exited. WaitForReady selects on it to fail immediately instead
	// of waiting for the readiness timeout. Each Start() creates a fresh channel.
	processExit chan struct{}
	// processExitMsg holds a human-readable classification of the process exit
	// (exit code, signal, core dump) written by the supervisor goroutine before
	// closing processExit. Safe to read after processExit is closed.
	processExitMsg string
	// TemplateOverlay is an optional path to a qcow2 overlay that already
	// contains a reusable VM snapshot. When set, Start() copies it instead
	// of creating a blank overlay, then boots with -loadvm TemplateSnapshotName.
	TemplateOverlay string
	// TemplateSnapshotName is the snapshot name loaded from TemplateOverlay.
	// When empty, Start() falls back to BootedSnapshotName.
	TemplateSnapshotName string
}

// NewQEMUManager creates and initializes a new QEMU manager instance for virtual
// machine testing. The manager sets up all necessary directories, generates unique
// instance identifiers for parallel execution, and configures filesystem sharing
// paths for host-VM communication.
//
// The initialization process includes:
//   - Unique instance ID generation for parallel test isolation
//   - Working directory creation in system temporary space
//   - Project root detection for build and target directory sharing
//   - Socket path configuration for VM networking and console access
//
// Parameters:
//   - name: Name of this manager
//   - imagePath: Path to the QEMU disk image file (must exist and be accessible)
//   - logger: Structured logger for debugging and monitoring VM operations
//
// Returns:
//   - *QEMUManager: Configured QEMU manager ready for VM startup
//   - error: An error if project root detection fails or paths are invalid
//
// Example:
//
//	manager, err := NewQEMUManager("main", "/path/to/vm-image.qcow2", logger)
//	if err != nil {
//	    log.Fatalf("Failed to create QEMU manager: %v", err)
//	}
func NewQEMUManager(name string, imagePath string, logger *zap.SugaredLogger) (*QEMUManager, error) {
	// Use /tmp directly to keep UNIX socket paths under the 104-byte limit.
	// The macOS TMPDIR (/var/folders/.../) is too long for socket paths.
	workDir, err := os.MkdirTemp("/tmp", fmt.Sprintf("yvm-%s-", name))
	if err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	instanceID := filepath.Base(workDir)

	// Determine project root directory
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to determine project root directory: %w", err)
	}
	buildDir := filepath.Join(projectRoot, "build")
	targetDir := filepath.Join(projectRoot, "target")

	qemuLog := logger.Named("QEMU")
	q := &QEMUManager{
		Name:             name,
		ImagePath:        imagePath,
		WorkDir:          workDir,
		LogsDir:          filepath.Join(workDir, "logs"),
		ConfigDir:        filepath.Join(workDir, "config"),
		BuildDir:         buildDir,
		TargetDir:        targetDir,
		readySignal:      make(chan bool, 1),
		log:              qemuLog,
		instanceID:       instanceID,
		SerialPath:       filepath.Join(workDir, "serial.sock"),
		MonitorPath:      filepath.Join(workDir, "monitor.sock"),
		serialReaderDone: make(chan struct{}),
	}
	q.serialLog.Store(qemuLog)
	return q, nil
}

const (
	// BootedSnapshotName is the name of the QEMU snapshot saved after a
	// clean boot. When a pool VM's overlay contains this snapshot, Start()
	// restores it instantly via -loadvm instead of waiting ~44s for boot.
	BootedSnapshotName    = "booted"
	bootedTemplateVersion = "v3"
)

// OverlayHasSnapshot returns true if the given qcow2 image contains a
// snapshot with the given name.
func OverlayHasSnapshot(imagePath, name string) bool {
	out, err := exec.Command("qemu-img", "snapshot", "-l", imagePath).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), name)
}

// Start launches a QEMU virtual machine. When TemplateOverlay is set the
// overlay is copied from it and QEMU starts with -loadvm for the requested
// TemplateSnapshotName, skipping the slow cold boot path.
//
// Returns (true, nil) when booted from snapshot, (false, nil) when doing
// a full cold boot.
func (q *QEMUManager) Start() (bool, error) {
	// Check if there's already a running QEMU process with the same VM name
	vmName := "yanet-test-vm-" + q.Name
	if err := q.checkForExistingVM(vmName); err != nil {
		return false, err
	}

	// Check if QEMU is available
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		return false, fmt.Errorf("qemu-system-x86_64 not found in PATH: %w", err)
	}

	// Check if image file exists
	if _, err := os.Stat(q.ImagePath); err != nil {
		return false, fmt.Errorf("QEMU image %s not found: %w", q.ImagePath, err)
	}

	// Create working directories
	q.log.Debug("Creating logs directory...")
	if err := os.MkdirAll(q.LogsDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create logs directory: %w", err)
	}
	q.log.Debug("Logs directory created.")

	q.log.Debug("Creating config directory...")
	if err := os.MkdirAll(q.ConfigDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create config directory: %w", err)
	}
	q.log.Debug("Config directory created.")

	// Reset readiness and serial state for fresh start.
	q.isReady = false
	q.readySignal = make(chan bool, 1)
	q.serialReaderDone = make(chan struct{})

	// Generate socket paths for Unix stream interface
	q.log.Debug("Generating socket paths...")
	q.SocketPaths = make([]string, 2)
	for i := range q.SocketPaths {
		// Use /tmp/ directory like in working Makefile configuration
		q.SocketPaths[i] = filepath.Join("/tmp", fmt.Sprintf("yanetvm_%s_sockdev_%d.sock", q.instanceID, i))
	}
	q.log.Debug("Socket paths generated.")

	// Detect OS
	osType := runtime.GOOS

	// Create or copy the QCOW2 overlay. When TemplateOverlay is set,
	// copy it (it already contains the "booted" snapshot) and start
	// with -loadvm to skip the ~44s Linux boot. Otherwise create a
	// fresh blank overlay.
	overlayPath := filepath.Join(q.WorkDir, "overlay.qcow2")
	absImagePath, err := filepath.Abs(q.ImagePath)
	if err != nil {
		return false, fmt.Errorf("failed to resolve image path: %w", err)
	}

	fromSnapshot := false
	templateSnapshot := q.TemplateSnapshotName
	if templateSnapshot == "" {
		templateSnapshot = BootedSnapshotName
	}
	if q.TemplateOverlay != "" {
		// Copy the template overlay and load the requested snapshot from it.
		if err := copyFile(q.TemplateOverlay, overlayPath); err != nil {
			return false, fmt.Errorf("failed to copy template overlay: %w", err)
		}
		fromSnapshot = true
		q.log.Infof("Copied template overlay %s; will boot from %q snapshot", q.TemplateOverlay, templateSnapshot)
	} else {
		// Create a blank overlay backed by the base image.
		createOverlay := exec.Command("qemu-img", "create",
			"-f", "qcow2",
			"-F", "qcow2",
			"-b", absImagePath,
			overlayPath,
		)
		if out, err := createOverlay.CombinedOutput(); err != nil {
			return false, fmt.Errorf("failed to create QCOW2 overlay: %w\noutput: %s", err, out)
		}
		q.log.Debugf("Created QCOW2 overlay: %s -> %s", overlayPath, absImagePath)
	}

	// Base arguments
	args := []string{
		"-name", vmName,
		"-smp", "2",
		"-m", "1G",
		"-machine", "q35,kernel-irqchip=split",
		"-cpu", "max",
		"-device", "intel-iommu,intremap=on,device-iotlb=on",
		"-device", "ioh3420,id=pcie.1,chassis=1",
		"-device", "ioh3420,id=pcie.2,chassis=2",
	}

	// OS-specific configuration
	if osType == "linux" {
		if isKVMEnabled() {
			args = append(args, "-enable-kvm")
		}
	}

	// Drive configuration.
	args = append(args,
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", overlayPath),
	)

	// When booting from a template overlay, restore VM state instantly via -loadvm.
	if fromSnapshot {
		args = append(args, "-loadvm", templateSnapshot)
	}

	// Network interface configuration. SSH forwarding, when enabled, is
	// added over the monitor once the monitor and serial consoles are
	// connected, instead of baked into this netdev.
	args = append(args, "-netdev", "user,id=net0")

	args = append(args,
		"-device", "virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0",
		"-netdev", "stream,id=net1,server=on,addr.type=unix,addr.path="+q.SocketPaths[0],
		"-device", "virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10",
		"-netdev", "stream,id=net2,server=on,addr.type=unix,addr.path="+q.SocketPaths[1],
		"-device", "virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10",
	)

	// Add 9P filesystem sharing for YANET logs and configuration
	// This allows the VM to access host files for testing
	// Match the mount configuration used in Makefile
	args = append(args,
		// Share temporary directory for logs
		"-fsdev", "local,id=fsdev0,path="+q.LogsDir+",security_model=none",
		"-device", "virtio-9p-pci,fsdev=fsdev0,mount_tag=logs",
		// Share temporary directory for configuration
		"-fsdev", "local,id=fsdev1,path="+q.ConfigDir+",security_model=none",
		"-device", "virtio-9p-pci,fsdev=fsdev1,mount_tag=config",
		// Share build directory
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
		return false, fmt.Errorf("failed to create log file: %w", err)
	}

	// Start QEMU
	q.Command = exec.Command("qemu-system-x86_64", args...)

	q.log.Debugf("Starting QEMU with command: %s %s", q.Command.Path, strings.Join(args, " "))
	q.log.Debugf("QEMU logs will be written to: %s", logFile)

	// Create stderr pipe for logging
	stderr, err := q.Command.StderrPipe()
	if err != nil {
		_ = logWriter.Close()
		return false, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Initialise process-supervision state for this start after all setup that
	// can fail without starting a process has succeeded.
	q.processExit = make(chan struct{})
	q.processExitMsg = ""

	// Start QEMU process
	exitCh := q.processExit
	if err := q.Command.Start(); err != nil {
		_ = stderr.Close()
		_ = logWriter.Close()
		q.processExitMsg = fmt.Sprintf("start failed: %v", err)
		close(exitCh)
		return false, fmt.Errorf("failed to start QEMU: %w", err)
	}

	// Start goroutine to capture stderr for logging.
	go q.captureStderr(stderr, logWriter)

	// Supervise the QEMU process: call Wait exactly once and signal its exit.
	// WaitForReady selects on processExit so a dead QEMU fails immediately
	// instead of waiting for the readiness timeout. The command and channel
	// are captured here so a later Start() replacing q.Command cannot cause
	// a double wait or close the wrong channel.
	cmd := q.Command
	go func() {
		waitErr := cmd.Wait()
		q.processExitMsg = classifyProcessExit(cmd, waitErr)
		q.log.Debugf("QEMU process exited: %s", q.processExitMsg)
		close(exitCh)
	}()

	// Wait a bit and check if process is still running
	time.Sleep(1 * time.Second)

	select {
	case <-exitCh:
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return false, fmt.Errorf("QEMU exited early (%s). Log content: %s; QEMU log content: %s", q.processExitMsg, string(logContent), string(logContentQ))
	default:
	}

	// Check if process exited with error
	if q.Command.Process == nil || q.Command.Process.Pid == 0 {
		// Read log file to see what went wrong
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return false, fmt.Errorf("QEMU exited early. Log content: %s; QEMU log content: %s", string(logContent), string(logContentQ))
	}

	p, err := os.FindProcess(q.Command.Process.Pid)
	if err != nil || p == nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return false, fmt.Errorf("QEMU process not found. Log content: %s; QEMU log content: %s", string(logContent), string(logContentQ))
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return false, fmt.Errorf("failed to signal QEMU process(may be dead): %w. Log content: %s; QEMU log content: %s", err, string(logContent), string(logContentQ))
	}

	q.log.Debugf("QEMU process started with PID: %d", q.Command.Process.Pid)

	if err := q.connectToMonitor(); err != nil {
		logContent, _ := os.ReadFile(logFile)
		logContentQ, _ := os.ReadFile(qemuLogfile)
		return false, fmt.Errorf("failed to connect to monitor (may be dead): %w. Log content: %s; QEMU log content: %s", err, string(logContent), string(logContentQ))
	}
	q.log.Debugf("Successfully connected to monitor at %s", q.MonitorPath)

	// Connect to serial console via unix socket
	if err := q.connectToSerial(); err != nil {
		return false, fmt.Errorf("failed to connect to serial console: %w", err)
	}
	q.log.Debugf("Successfully connected to serial console at %s", q.SerialPath)

	// When booting from snapshot, the restored shell produces no output
	// until prompted. Poke the console once so the prompt-aware scanner
	// receives it. During cold boot the prompt arrives naturally and no
	// writes are needed.
	if fromSnapshot {
		if stdin := q.GetStdin(); stdin != nil {
			_, _ = stdin.Write([]byte("\n\n"))
		}
	}

	go q.readSerial()

	// The monitor gives no reply until a client has also connected to the
	// serial socket, so the forward is added only now that both consoles
	// are wired up.
	if ShouldKeepVMAlive() {
		q.addSSHHostForward()
	}

	return fromSnapshot, nil
}

// addSSHHostForward asks the QEMU monitor to add a hostfwd rule for SSH,
// leaving port 0 for the kernel to assign. QEMU binds and holds that port
// itself, so no other process can race for it the way it could with a port
// picked ahead of time on the host.
//
// A failed hostfwd_add is local to the monitor command and never takes the
// VM down, so failure here only logs a warning: YANET_KEEP_VM_ALIVE exists
// to leave a VM for a human to inspect, and killing that VM because a
// debug convenience could not get a port would defeat its purpose.
func (q *QEMUManager) addSSHHostForward() {
	resp, err := q.SendMonitorCommand("hostfwd_add net0 tcp:127.0.0.1:0-:22")
	if err != nil {
		q.log.Warnf("Keep VM alive mode: hostfwd_add failed: %v; SSH access will be unavailable", err)
		return
	}
	if resp != "" {
		q.log.Warnf("Keep VM alive mode: hostfwd_add returned an error: %s; SSH access will be unavailable", resp)
		return
	}

	info, err := q.SendMonitorCommand("info usernet")
	if err != nil {
		q.log.Warnf("Keep VM alive mode: info usernet failed: %v; SSH access will be unavailable", err)
		return
	}
	port, err := parseUsernetSSHPort(info)
	if err != nil {
		q.log.Warnf("Keep VM alive mode: could not determine the forwarded SSH port: %v; SSH access will be unavailable", err)
		return
	}

	q.sshPort = port
	q.log.Infof("Keep VM alive mode enabled: SSH port forwarding 127.0.0.1:%d -> VM:22", q.sshPort)
}

// parseUsernetSSHPort extracts the host-side port of the guest-port-22
// hostfwd row from "info usernet" output, tolerating other forwards being
// listed alongside it. Row format:
//
//	Protocol[State]    FD  Source Address  Port   Dest. Address  Port RecvQ SendQ
//	TCP[HOST_FORWARD]  12       127.0.0.1 43007       10.0.2.15    22     0     0
func parseUsernetSSHPort(output string) (int, error) {
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasPrefix(fields[0], "TCP[") || fields[5] != "22" {
			continue
		}
		if port, err := strconv.Atoi(fields[3]); err == nil {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no SSH (guest port 22) host forward found in %q", output)
}

// Stop performs graceful termination of the QEMU virtual machine and comprehensive
// cleanup of all associated resources. This method ensures proper resource
// deallocation and prevents resource leaks in testing environments.
//
// The cleanup process includes:
//   - Graceful closure of monitor and serial console connections
//   - QEMU process termination with proper signal handling
//   - Working directory and temporary file cleanup
//   - Unix socket file removal from filesystem
//   - Error collection and reporting for failed cleanup operations
//
// Multiple cleanup errors are collected and returned as a combined error
// to provide comprehensive information about any cleanup failures.
//
// Returns:
//   - error: A combined error if any cleanup operations fail, or nil if successful
//
// Example:
//
//	if err := manager.Stop(); err != nil {
//	    log.Errorf("VM cleanup encountered errors: %v", err)
//	}
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

	// Kill QEMU process if still running (unless VM should be kept alive).
	// If the process already exited on its own, skip Kill and drain the
	// supervisor goroutine so it cannot write to a closed log file after
	// WorkDir cleanup.
	if ShouldKeepVMAlive() {
		if q.Command != nil && q.Command.Process != nil {
			q.log.Infof("Keeping VM alive (PID: %d) for manual debugging", q.Command.Process.Pid)
		}
		q.log.Infof("Serial console socket: %s", q.SerialPath)
		q.log.Infof("To connect: socat - UNIX-CONNECT:%s", q.SerialPath)
		if q.sshPort != 0 {
			q.log.Infof("SSH: ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@localhost -p %d", q.sshPort)
		}
	} else {
		alreadyExited := false
		if q.processExit != nil {
			select {
			case <-q.processExit:
				alreadyExited = true
			default:
			}
		}
		if !alreadyExited && q.Command != nil && q.Command.Process != nil {
			if err := q.Command.Process.Kill(); err != nil {
				// The "process already finished" error is not an error here.
				if !strings.Contains(err.Error(), "process already finished") {
					errs = append(errs, fmt.Errorf("failed to kill QEMU process: %w", err))
				}
			}
		}
		// Wait for the supervisor goroutine to finish so it does not race
		// with WorkDir cleanup below.
		if q.processExit != nil {
			<-q.processExit
		}
	}

	// Cleanup working directory and socket files (unless artifacts should be preserved)
	if IsDebugEnabled() {
		q.log.Infof("Preserving QEMU artifacts in: %s", q.WorkDir)
		q.log.Infof("Socket files preserved:")
		for i, path := range q.SocketPaths {
			q.log.Infof("  Interface %d: %s", i, path)
		}
	} else {
		if err := os.RemoveAll(q.WorkDir); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup working directory: %w", err))
		}

		// Only remove socket files if not preserving artifacts
		for _, path := range q.SocketPaths {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("failed to remove socket file %s: %w", path, err))
			}
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
		if err := q.connectToSerial(); err != nil {
			q.log.Errorf("Failed to connect to serial console: %v", err)
			return nil
		}
	}
	return q.serialConn
}

// resetSerialBuffer clears the accumulated serial console output buffer.
func (q *QEMUManager) resetSerialBuffer() {
	q.serialMutex.Lock()
	defer q.serialMutex.Unlock()
	q.serialBuffer.Reset()
}

// serialBufferSnapshot returns the current contents of the serial console output buffer.
func (q *QEMUManager) serialBufferSnapshot() string {
	q.serialMutex.Lock()
	defer q.serialMutex.Unlock()
	return q.serialBuffer.String()
}

// setSerialLogger atomically replaces the logger used by the readSerial goroutine.
func (q *QEMUManager) setSerialLogger(log *zap.SugaredLogger) {
	q.serialLog.Store(log)
}

// getSerialLog returns the current serial logger.
func (q *QEMUManager) getSerialLog() *zap.SugaredLogger {
	return q.serialLog.Load().(*zap.SugaredLogger)
}

// classifyProcessExit turns an exec.Cmd exit error into a stable diagnostic
// string distinguishing normal exit, non-zero exit code, and signal death
// (including core-dump state).
func classifyProcessExit(cmd *exec.Cmd, waitErr error) string {
	if waitErr == nil {
		return fmt.Sprintf("exit code %d", cmd.ProcessState.ExitCode())
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		ps := exitErr.ProcessState
		msg := fmt.Sprintf("exit code %d", ps.ExitCode())
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				sig := ws.Signal()
				msg = fmt.Sprintf("killed by signal %d (%s)", sig, sig)
				if ws.CoreDump() {
					msg += " (core dumped)"
				}
			}
		}
		return msg
	}
	return waitErr.Error()
}

// captureStderr captures QEMU stderr into the per-VM log file and mirrors
// every line through the harness logger so the retained test.log carries
// QEMU diagnostics without requiring a separate artifact upload.
func (q *QEMUManager) captureStderr(stderr io.ReadCloser, logWriter *os.File) {
	defer func() {
		if r := recover(); r != nil {
			q.log.Errorf("captureStderr recovered panic: %v", r)
		}
	}()
	defer func() {
		if err := stderr.Close(); err != nil {
			q.log.Errorf("Failed to close stderr pipe: %v", err)
		}
		if err := logWriter.Close(); err != nil {
			q.log.Errorf("Failed to close log file: %v", err)
		}
	}()
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if _, err := logWriter.WriteString("STDERR: " + line + "\n"); err != nil {
			q.log.Errorf("Failed to write stderr to log file: %v", err)
		}
		q.log.Debugf("QEMU stderr: %s", line)
	}
	if err := scanner.Err(); err != nil {
		q.log.Errorf("Error reading stderr: %v", err)
	}
}

// connectToMonitor connects to QEMU monitor interface via unix socket
func (q *QEMUManager) connectToMonitor() error {
	// Try multiple times to connect to monitor
	for i := range 10 {
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

		// The first read from an HMP monitor socket includes the banner and
		// initial prompt. Drain it once here so later commands only observe
		// command-specific output. Some environments do not send the banner
		// immediately, so a short timeout here is treated as a valid "nothing
		// to drain yet" outcome.
		if err := q.monitorConn.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
			_ = conn.Close()
			q.monitorConn = nil
			q.log.Debugf("Failed to set monitor banner drain deadline on attempt %d: %v", i+1, err)
			time.Sleep(1 * time.Second)
			continue
		}
		if _, err := q.readUntilMonitorPrompt(); err != nil {
			_ = q.monitorConn.SetDeadline(time.Time{})
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				q.log.Debug("Monitor banner not sent immediately; continuing without initial drain")
				return nil
			}
			_ = conn.Close()
			q.monitorConn = nil
			q.log.Debugf("Failed to drain monitor banner on attempt %d: %v", i+1, err)
			time.Sleep(1 * time.Second)
			continue
		}
		_ = q.monitorConn.SetDeadline(time.Time{})

		return nil
	}

	return fmt.Errorf("failed to connect to monitor after 10 attempts")
}

// stripMonitorEscapes removes ANSI escape sequences and readline control
// characters from QEMU HMP monitor output. QEMU monitor in readline mode
// echoes each character with backspace/kill sequences (e.g. "l\x1b[K\x08lo...")
// which must be stripped before parsing the response for the (qemu) prompt.
func stripMonitorEscapes(s string) string {
	// Remove ANSI escape sequences: ESC [ ... letter
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	s = ansi.ReplaceAllString(s, "")
	// Remove lone ESC sequences
	s = strings.ReplaceAll(s, "\x1b", "")
	// Remove carriage returns
	s = strings.ReplaceAll(s, "\r", "")
	// Remove backspace and kill-to-end-of-line sequences
	// These appear as \x08 (BS) and \x0b (VT), \x0c (FF), DEL
	ctrl := regexp.MustCompile(`[\x00-\x08\x0b-\x0c\x0e-\x1a\x1c-\x1f\x7f]`)
	s = ctrl.ReplaceAllString(s, "")
	return s
}

func (q *QEMUManager) readUntilMonitorPrompt() (string, error) {
	if q.monitorConn == nil {
		return "", fmt.Errorf("monitor connection is not established")
	}

	var raw strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := q.monitorConn.Read(buf)
		if n > 0 {
			raw.Write(buf[:n])
			// Strip readline escape sequences before checking for the prompt.
			cleaned := stripMonitorEscapes(raw.String())
			// One prompt, at the end, is the normal shape — e.g. the banner read
			// in connectToMonitor, itself exactly one prompt. Two prompts appear
			// when connectToMonitor's banner drain times out: the queued banner
			// prompt survives into the next command's reply. Wait for either
			// shape.
			count := strings.Count(cleaned, "(qemu)")
			if count >= 2 {
				break
			}
			if count == 1 && strings.HasSuffix(strings.TrimRight(cleaned, " \n"), "(qemu)") {
				break
			}
		}
		if err != nil {
			return raw.String(), err
		}
	}

	cleaned := stripMonitorEscapes(raw.String())
	first := strings.Index(cleaned, "(qemu)")
	last := strings.LastIndex(cleaned, "(qemu)")
	if first >= 0 && last > first {
		// Two prompts: content is between them, command echo + output.
		return stripEchoLine(cleaned[first+len("(qemu)") : last]), nil
	}
	if first >= 0 {
		// One prompt, at the end: content is what precedes it.
		return stripEchoLine(cleaned[:first]), nil
	}
	return strings.TrimSpace(cleaned), nil
}

// stripEchoLine drops the first line of s, the readline echo of the typed
// command, and returns the remaining trimmed output.
func stripEchoLine(s string) string {
	lines := strings.SplitN(strings.TrimLeft(s, " \r\n"), "\n", 2)
	if len(lines) > 1 {
		return strings.TrimSpace(lines[1])
	}
	return ""
}

// connectToSerial connects to QEMU serial console via unix socket
func (q *QEMUManager) connectToSerial() error {
	// Try multiple times to connect to serial console
	for i := range 10 {
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

// shellPromptMarker is the autologin bash prompt that does not end with a
// newline. The scanner split function detects it so readiness works without
// writing periodic newlines into the serial console during boot.
const shellPromptMarker = "root@yanet-vm:~#"

// promptAwareSplit is a bufio.SplitFunc that emits normal newline-terminated
// lines and also emits the unterminated shell prompt as soon as it arrives,
// so the reader detects readiness directly from received bytes.
func promptAwareSplit(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	// Check for the shell prompt marker anywhere in the buffer, even without
	// a trailing newline. This is what makes readiness detection reliable
	// during cold boot: the prompt arrives as a bare token at the end of
	// serial output with no terminator.
	if idx := bytes.Index(data, []byte(shellPromptMarker)); idx >= 0 {
		end := idx + len(shellPromptMarker)
		// Return everything up to and including the prompt as one token.
		return end, data[:end], nil
	}
	// Normal newline-terminated line.
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	// Not enough data yet and not at EOF.
	if !atEOF {
		return 0, nil, nil
	}
	// At EOF with remaining non-prompt data: return it as a final token.
	if len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// readSerial is the serial reader goroutine for one VM slot.
//
// Invariant: exactly one readSerial goroutine is active per VM slot at any
// time. Both conn and readySignal are captured at goroutine start so that a
// reconnect (which replaces q.serialConn and q.readySignal) cannot race with
// a running goroutine from the previous connection.
func (q *QEMUManager) readSerial() {
	defer func() {
		if r := recover(); r != nil {
			q.log.Errorf("readSerial recovered panic: %v", r)
		}
	}()

	conn := q.serialConn
	if conn == nil {
		q.log.Error("readSerial: serial connection is nil")
		close(q.serialReaderDone)
		return
	}
	readySig := q.readySignal
	done := q.serialReaderDone

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	scanner.Split(promptAwareSplit)
	readyOnce := false

	defer close(done)

	for scanner.Scan() {
		line := scanner.Text()

		q.serialMutex.Lock()
		q.serialBuffer.WriteString(line + "\n")
		q.serialMutex.Unlock()

		q.getSerialLog().Debugf("VM output: %s", line)

		if strings.Contains(line, "To restore this content, you can run the 'unminimize' command") {
			q.log.Debug("Unminimize message seen, sending Enter to activate prompt")
			if _, err := conn.Write([]byte("\n")); err != nil {
				q.log.Errorf("Failed to send Enter to serial console: %v", err)
			}
		}

		// Detect readiness exactly once; keep reading after signalling.
		if !readyOnce && strings.Contains(line, shellPromptMarker) {
			readyOnce = true
			q.setVMReady(true)
			q.log.Debug("VM is ready!")
			select {
			case <-readySig:
			default:
				close(readySig)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		q.log.Debugf("readSerial: connection closed: %v", err)
	}
}

// stopSerialReader stops the current serial reader goroutine by closing
// the serial connection (which unblocks scanner.Scan) and waits for the
// goroutine to exit. Must be called before starting a new readSerial.
func (q *QEMUManager) stopSerialReader() {
	// Check if already stopped.
	select {
	case <-q.serialReaderDone:
		q.log.Debug("Serial reader already stopped")
		q.serialReaderDone = make(chan struct{})
		return
	default:
	}

	// Close the connection to unblock scanner.Scan() in the goroutine.
	if q.serialConn != nil {
		q.serialConn.Close()
		q.serialConn = nil
	}

	// Wait for the goroutine to exit.
	<-q.serialReaderDone
	q.log.Debug("Serial reader stopped")

	// Prepare done channel for the next reader.
	q.serialReaderDone = make(chan struct{})
}

// WaitForReady blocks until the virtual machine becomes ready for command
// execution or the specified timeout expires. This method provides synchronous
// waiting for VM readiness with proper timeout handling.
//
// The method first checks if the VM is already ready to avoid unnecessary
// waiting. If not ready, it waits for the readiness signal from the background
// monitoring goroutine.
//
// Parameters:
//   - timeout: Maximum time to wait for VM readiness
//
// Returns:
//   - error: An error if the timeout expires before VM becomes ready, or nil if ready
//
// Example:
//
//	if err := manager.WaitForReady(60 * time.Second); err != nil {
//	    log.Fatalf("VM failed to become ready: %v", err)
//	}
func (q *QEMUManager) WaitForReady(timeout time.Duration) error {
	if q.IsVMReady() {
		q.log.Debug("VM is already ready")
		return nil
	}

	select {
	case <-q.readySignal:
		q.log.Debug("Got ready signal")
		return nil
	case <-q.processExit:
		logContent, _ := os.ReadFile(filepath.Join(q.WorkDir, "qemu-output.log"))
		return fmt.Errorf("QEMU process exited before VM became ready (%s). stderr: %s", q.processExitMsg, string(logContent))
	case <-time.After(timeout):
		return fmt.Errorf("VM did not become ready within %v", timeout)
	}
}

// IsVMReady returns the current readiness state of the virtual machine in a
// thread-safe manner. This method can be called from multiple goroutines
// without synchronization concerns.
//
// VM readiness indicates that the virtual machine has completed its boot
// process and is ready to accept and execute commands through the serial console.
//
// Returns:
//   - bool: True if the VM is ready for command execution, false otherwise
//
// Example:
//
//	if manager.IsVMReady() {
//	    // Safe to execute commands
//	    output, err := cli.ExecuteCommand("ls -la")
//	}
func (q *QEMUManager) IsVMReady() bool {
	q.readyMutex.RLock()
	defer q.readyMutex.RUnlock()
	return q.isReady
}

// setVMReady updates the VM readiness state in a thread-safe manner. This method
// is used internally by the readiness monitoring goroutine to update the VM
// state when readiness conditions are detected.
//
// The method uses a write lock to ensure exclusive access during state updates
// and prevent race conditions with concurrent readiness checks.
//
// Parameters:
//   - ready: New readiness state to set (true when VM is ready, false otherwise)
func (q *QEMUManager) setVMReady(ready bool) {
	q.readyMutex.Lock()
	defer q.readyMutex.Unlock()
	q.isReady = ready
}

// SendMonitorCommand sends a command to the QEMU monitor (HMP) and returns
// the response. The monitor socket must already be connected via Start().
// Commands are terminated with a newline; the method waits for the next
// "(qemu) " prompt to determine when the response is complete.
func (q *QEMUManager) SendMonitorCommand(cmd string) (string, error) {
	if q.monitorConn == nil {
		return "", fmt.Errorf("monitor connection is not established")
	}

	// Drain any residual buffered data from previous commands.
	// Without this, readUntilMonitorPrompt may count stale (qemu)
	// prompts as part of the response and return early.
	_ = q.monitorConn.SetDeadline(time.Now().Add(50 * time.Millisecond))
	drainBuf := make([]byte, 4096)
	for {
		_, err := q.monitorConn.Read(drainBuf)
		if err != nil {
			break // timeout or EOF — buffer drained
		}
	}

	// Set a generous timeout for monitor operations (snapshots can be slow).
	if err := q.monitorConn.SetDeadline(time.Now().Add(120 * time.Second)); err != nil {
		return "", fmt.Errorf("failed to set monitor deadline: %w", err)
	}
	defer func() {
		_ = q.monitorConn.SetDeadline(time.Time{})
	}()

	// Send the command.
	if _, err := fmt.Fprintf(q.monitorConn, "%s\n", cmd); err != nil {
		return "", fmt.Errorf("failed to send monitor command %q: %w", cmd, err)
	}

	resp, err := q.readUntilMonitorPrompt()
	if err != nil {
		return resp, fmt.Errorf("error reading monitor response for %q: %w", cmd, err)
	}

	return resp, nil
}

// SaveSnapshot creates a named QEMU VM snapshot via the monitor. The
// snapshot captures the full VM state (CPU, RAM, devices) and can be
// restored later with RestoreSnapshot. This is the key primitive for
// per-test state isolation.
func (q *QEMUManager) SaveSnapshot(name string) error {
	q.log.Infof("Saving VM snapshot %q...", name)
	start := time.Now()
	resp, err := q.SendMonitorCommand("savevm " + name)
	if err != nil {
		return fmt.Errorf("savevm %q failed: %w", name, err)
	}
	elapsed := time.Since(start)
	// The savevm command prints nothing on success. Any output indicates an error.
	if resp != "" {
		return fmt.Errorf("savevm %q returned unexpected output: %s", name, resp)
	}
	q.log.Infof("Snapshot %q saved in %v", name, elapsed)
	return nil
}

// RestoreSnapshot restores the VM to a previously saved snapshot. After
// restore, the serial console connection is broken (the guest-side state
// reverts) and must be re-established via ReconnectSerial.
//
// The monitor connection itself survives because it is external to the
// guest state.
func (q *QEMUManager) RestoreSnapshot(name string) error {
	q.log.Infof("Restoring VM snapshot %q...", name)
	start := time.Now()
	resp, err := q.SendMonitorCommand("loadvm " + name)
	if err != nil {
		return fmt.Errorf("loadvm %q failed: %w", name, err)
	}
	if resp != "" {
		return fmt.Errorf("loadvm %q returned unexpected output: %s", name, resp)
	}
	// Resume the VM after loadvm. If the VM was paused (e.g. by StopAllCPU),
	// loadvm restores the snapshot state but may not automatically resume the
	// CPU. Sending "cont" ensures the VM runs regardless of prior state.
	if _, err := q.SendMonitorCommand("cont"); err != nil {
		q.log.Debugf("cont after loadvm returned error (VM may already be running): %v", err)
	}
	elapsed := time.Since(start)
	q.log.Infof("Snapshot %q restored in %v", name, elapsed)
	return nil
}

// ReconnectSerial closes the current serial connection and opens a new one.
// The caller is responsible for resetting readySignal and launching a new
// readSerial goroutine after this returns.
func (q *QEMUManager) ReconnectSerial() error {
	// Close old connection — the running readSerial goroutine will detect
	// EOF/error and exit cleanly.
	if q.serialConn != nil {
		q.serialConn.Close()
		q.serialConn = nil
	}
	return q.connectToSerial()
}

// RestoreBooted restores the VM to the "booted" snapshot without going through
// the full framework-level RestoreAndReconnect. It handles the monitor loadvm,
// serial reconnect, and ready wait at the QEMUManager level.
//
// Callers are responsible for unmounting 9P before calling and remounting after.
func (q *QEMUManager) RestoreBooted() error {
	q.log.Infof("Restoring booted snapshot...")
	start := time.Now()

	// Restore the booted snapshot via monitor.
	if _, err := q.SendMonitorCommand("loadvm " + BootedSnapshotName); err != nil {
		return fmt.Errorf("loadvm booted: %w", err)
	}
	// Resume the VM (loadvm may leave it paused).
	if _, err := q.SendMonitorCommand("cont"); err != nil {
		q.log.Debugf("cont after loadvm booted: %v (non-fatal)", err)
	}

	// Stop the old serial reader goroutine (also closes serialConn).
	q.stopSerialReader()

	// Reset readiness state and serial buffer for the fresh connection.
	q.setVMReady(false)
	q.readySignal = make(chan bool, 1)
	q.resetSerialBuffer()

	// Open new serial connection.
	if err := q.connectToSerial(); err != nil {
		close(q.serialReaderDone)
		return fmt.Errorf("reconnect serial after booted restore: %w", err)
	}

	// Poke the console to flush the prompt through the scanner.
	if stdin := q.GetStdin(); stdin != nil {
		_, _ = stdin.Write([]byte("\n\n"))
	}

	// Launch the new reader goroutine.
	go q.readSerial()

	// Wait for shell readiness.
	if err := q.WaitForReady(VMReadyTimeout()); err != nil {
		return fmt.Errorf("wait for ready after booted restore: %w", err)
	}

	q.log.Infof("Booted snapshot restored in %v", time.Since(start))
	return nil
}

// SaveSnapshotOverlay saves the named snapshot to the VM's current overlay
// and returns the overlay file path. The caller can copy this path to a
// cache location and use it as TemplateOverlay for other VMs.
//
// The 9P shares must be unmounted before calling (savevm blocks when
// VirtFS mounts are active).
func (q *QEMUManager) SaveSnapshotOverlay(name string) (string, error) {
	overlayPath := filepath.Join(q.WorkDir, "overlay.qcow2")
	if err := q.SaveSnapshot(name); err != nil {
		return "", err
	}
	return overlayPath, nil
}

// SaveBootedOverlay saves the "booted" snapshot to the VM's current
// overlay and returns the overlay file path. The caller can copy this
// path to a cache location and use it as TemplateOverlay for other VMs.
func (q *QEMUManager) SaveBootedOverlay() (string, error) {
	return q.SaveSnapshotOverlay(BootedSnapshotName)
}

// BootedImagePath returns the versioned path to the booted snapshot image.
func BootedImagePath(baseImagePath string) string {
	return SnapshotImagePath(baseImagePath, BootedSnapshotName+"-"+bootedTemplateVersion)
}

// BaselineImagePath returns the versioned path to the cached baseline snapshot.
func BaselineImagePath(baseImagePath string) string {
	return SnapshotImagePath(baseImagePath, "baseline-"+baselineTemplateVersion)
}

// SnapshotImagePath returns the path to the cached image containing the
// named snapshot next to the base image.
func SnapshotImagePath(baseImagePath string, snapshotName string) string {
	dir := filepath.Dir(baseImagePath)
	base := filepath.Base(baseImagePath)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, name+"-"+snapshotName+".qcow2")
}

// HasBootedSnapshot checks if the given overlay file contains a
// snapshot named "booted". Returns true if the snapshot exists.
func HasBootedSnapshot(overlayPath string) bool {
	return OverlayHasSnapshot(overlayPath, BootedSnapshotName)
}

// copyFile copies src to dst using a buffered file copy.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return out.Sync()
}

// isKVMEnabled reports whether QEMU can open the KVM device.
func isKVMEnabled() bool {
	return isKVMDeviceAccessible("/dev/kvm")
}

func isKVMDeviceAccessible(path string) bool {
	device, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer device.Close()
	return true
}

// existingVMPattern builds the pgrep -f pattern that detects a real QEMU
// process running the given VM name. The opening [q] class prevents pgrep
// from matching its own command line on macOS and Linux alike, and the
// trailing ([[:space:]]|$) anchor stops "yanet-test-vm-foo" from matching
// "yanet-test-vm-foobar".
func existingVMPattern(vmName string) string {
	return "[q]emu-system-x86_64.*[[:space:]]-name[[:space:]]+" +
		regexp.QuoteMeta(vmName) + "([[:space:]]|$)"
}

// regexpMatch is a tiny indirection used by the unit tests so they can
// compare a POSIX ERE pattern against a representative command line. The
// patterns we ship only rely on literal tokens, [[:space:]] classes, and
// basic quantifiers that Go's regexp engine handles equivalently.
func regexpMatch(pattern, s string) (bool, error) {
	return regexp.MatchString(pattern, s)
}

// pgrepRunner abstracts exec.Command so unit tests can substitute a fake.
// It mirrors pgrep's contract: ("", nil) when nothing matches, a populated
// stdout on matches, and a non-nil error when pgrep itself cannot run.
type pgrepRunner func(pattern string) (string, error)

// realPgrepRunner is the production runner: shell out to pgrep -f. Exit
// status 1 is pgrep's "no process matched" and is not an error; anything
// else (missing binary, exit 2, signal) must surface to the caller instead
// of silently passing the duplicate-VM check.
func realPgrepRunner(pattern string) (string, error) {
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("pgrep -f %q: %w", pattern, err)
	}
	return string(out), nil
}

// checkForExistingVM checks if there's already a running QEMU process with the given VM name.
// This prevents conflicts when running tests in parallel or when a previous test didn't clean up properly.
func (q *QEMUManager) checkForExistingVM(vmName string) error {
	return checkForExistingVMRun(q, vmName, realPgrepRunner)
}

// checkForExistingVMRun is the testable core. When pgrep finds no matches it
// returns an empty string and we treat that as success. Any non-empty result
// is an existing QEMU with our name and we return a duplicate-name error. A
// runner error means the check itself failed and is propagated so a host
// without pgrep fails loudly instead of racing a second VM into existence.
func checkForExistingVMRun(
	q *QEMUManager,
	vmName string,
	run pgrepRunner,
) error {
	output, err := run(existingVMPattern(vmName))
	if err != nil {
		return fmt.Errorf(
			"cannot check for existing VM '%s': %w", vmName, err,
		)
	}
	if len(output) == 0 {
		return nil
	}
	processes := strings.TrimSpace(output)
	q.log.Errorf("Found existing QEMU process(es) with VM name '%s':", vmName)
	q.log.Errorf("%s", processes)
	return fmt.Errorf(
		"cannot start VM '%s': a QEMU process with this name is already running. Please stop the existing VM or use a different name. Process details:\n%s",
		vmName, processes,
	)
}
