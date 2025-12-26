package framework

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Regular expressions used for parsing command output
var (
	retCodeRegex = regexp.MustCompile(`=(\d+)=`)
)

// cliManagerInner holds the shared connection state that should not be copied
// between CLIManager instances. This allows multiple CLIManager wrappers
// with different loggers to share the same underlying connection.
type cliManagerInner struct {
	qemu         *QEMUManager    // QEMU virtual machine manager instance
	outputBuffer strings.Builder // Buffer for collecting command output
	mutex        sync.Mutex      // Protects access to outputBuffer
	reader       *bufio.Scanner  // Scanner for reading VM stdout
	cmdMutex     sync.Mutex      // Ensures sequential command execution
	log          atomic.Value    // Shared logger (*zap.SugaredLogger), updated atomically
}

// getLog returns the current logger from atomic storage
func (inner *cliManagerInner) getLog() *zap.SugaredLogger {
	return inner.log.Load().(*zap.SugaredLogger)
}

// CLIManager handles YANET CLI operations within a QEMU virtual machine environment.
// It provides thread-safe command execution capabilities through the VM's serial console,
// with support for output buffering, command synchronization, and proper cleanup of
// control characters from terminal output.
//
// The manager uses markers to reliably detect command completion and extract return codes,
// ensuring accurate command execution status reporting even in concurrent scenarios.
type CLIManager struct {
	inner *cliManagerInner   // Shared connection state (not copied)
	log   *zap.SugaredLogger // Logger for debugging and monitoring (can be different per instance)
}

// CLIOption defines functional options for configuring CLIManager instances.
// This pattern allows for flexible initialization with optional parameters
// while maintaining backward compatibility.
type CLIOption func(*CLIManager) error

// CLIWithLog configures the CLIManager to use the specified logger for debugging
// and monitoring command execution. If not provided, a no-op logger is used by default.
//
// Parameters:
//   - log: A zap.SugaredLogger instance for logging CLI operations
//
// Returns:
//   - CLIOption: A functional option that sets the logger
func CLIWithLog(log *zap.SugaredLogger) CLIOption {
	return func(cm *CLIManager) error {
		cm.log = log
		return nil
	}
}

// NewCLIManager creates and initializes a new CLI manager instance for executing
// commands within a QEMU virtual machine. The manager provides thread-safe command
// execution with proper output handling and error reporting.
//
// Parameters:
//   - qemu: A QEMUManager instance representing the target virtual machine
//   - opts: Optional functional options for customizing the CLI manager behavior
//
// Returns:
//   - *CLIManager: A configured CLI manager instance
//   - error: An error if initialization fails or options cannot be applied
func NewCLIManager(qemu *QEMUManager, opts ...CLIOption) (*CLIManager, error) {
	defaultLog := zap.NewNop().Sugar()
	inner := &cliManagerInner{
		qemu: qemu,
	}
	inner.log.Store(defaultLog)

	cm := &CLIManager{
		inner: inner,
		log:   defaultLog,
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(cm); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return cm, nil
}

// ExecuteCommand executes a single CLI command within the QEMU virtual machine
// via the serial console interface. The method ensures thread-safe execution,
// proper output collection, and reliable command completion detection using
// unique markers.
//
// The execution process includes:
//   - VM readiness verification
//   - Command wrapping with start/end markers for reliable parsing
//   - Output buffering and cleanup of control characters
//   - Return code extraction and error handling
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
//	output, err := cli.ExecuteCommand("ls -la /etc")
//	if err != nil {
//	    log.Fatalf("Command failed: %v", err)
//	}
//	fmt.Println("Directory listing:", output)
func (c *CLIManager) ExecuteCommand(command string) (string, error) {
	c.inner.cmdMutex.Lock()
	defer c.inner.cmdMutex.Unlock()
	if c.inner.qemu == nil || c.inner.qemu.Command == nil || c.inner.qemu.Command.Process == nil {
		return "", fmt.Errorf("QEMU VM is not running")
	}

	// Wait for VM to be ready first
	if !c.inner.qemu.IsVMReady() {
		return "", fmt.Errorf("VM not ready")
	}

	// Check if we have stdin/stdout pipes
	stdin := c.inner.qemu.GetStdin()
	stdout := c.inner.qemu.GetStdout()

	if stdin == nil || stdout == nil {
		return "", fmt.Errorf("failed to connect to QEMU serial console")
	}

	c.log.Debugf("DEBUG: Executing command in VM %s: %s", c.inner.qemu.Name, command)

	// Initialize reader if not already done
	if c.inner.reader == nil {
		c.inner.reader = bufio.NewScanner(stdout)
		// Start background reader to capture output
		go c.readOutput()
	}

	// Clear output buffer
	c.inner.mutex.Lock()
	c.inner.outputBuffer.Reset()
	c.inner.mutex.Unlock()

	// Send command to VM with a unique marker for better parsing
	tm := time.Now().UnixNano()
	commandMarker := fmt.Sprintf("CMD_START_%d", tm)
	endMarker := fmt.Sprintf("CMD_END_%d", tm)

	fullCommand := fmt.Sprintf("echo '%s'; %s; echo \"=$?=%s\"\n", commandMarker, command, endMarker)
	_, err := stdin.Write([]byte(fullCommand))
	if err != nil {
		return "", fmt.Errorf("failed to send command to VM: %w", err)
	}

	// Wait for command completion and collect output
	return c.waitForCommandCompletionWithMarkers(command, fullCommand, commandMarker, endMarker, 30*time.Second)
}

// ExecuteCommands executes multiple CLI commands sequentially within the QEMU
// virtual machine. Each command is executed in order, and execution stops at
// the first command that returns an error.
//
// This method is useful for executing a series of related commands where each
// command may depend on the success of previous ones, such as configuration
// setup or multi-step operations.
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
//	outputs, err := cli.ExecuteCommands(
//	    "mkdir -p /tmp/test",
//	    "echo 'hello' > /tmp/test/file.txt",
//	    "cat /tmp/test/file.txt",
//	)
//	if err != nil {
//	    log.Fatalf("Command sequence failed: %v", err)
//	}
func (c *CLIManager) ExecuteCommands(commands ...string) ([]string, error) {
	outputs := make([]string, 0, len(commands))
	for _, cmd := range commands {
		output, err := c.ExecuteCommand(cmd)
		outputs = append(outputs, output)
		if err != nil {
			return outputs, fmt.Errorf("failed to execute command '%s': %w", cmd, err)
		}
	}
	return outputs, nil
}

// readOutput continuously reads and buffers output from the QEMU virtual machine's
// stdout stream in a separate goroutine. This background process ensures that all
// VM output is captured and made available for command parsing.
//
// The method runs indefinitely until the scanner encounters an error or EOF,
// thread-safely appending each line to the output buffer. All captured output
// is logged at debug level for troubleshooting purposes.
//
// This is an internal method that should not be called directly by users.
func (c *CLIManager) readOutput() {
	for c.inner.reader.Scan() {
		line := c.inner.reader.Text()

		c.inner.mutex.Lock()
		c.inner.outputBuffer.WriteString(line + "\n")
		c.inner.mutex.Unlock()

		// Use shared logger from inner (updated atomically via WithLog)
		c.inner.getLog().Debugf("DEBUG: VM output: %s", line)
	}

	if err := c.inner.reader.Err(); err != nil {
		c.inner.getLog().Debugf("DEBUG: Error reading VM output: %v", err)
	}
}

// waitForCommandCompletionWithMarkers waits for command completion by monitoring
// the output buffer for specific start and end markers. This approach provides
// reliable command boundary detection even when multiple commands are executed
// concurrently or when output contains complex formatting.
//
// The method polls the output buffer at regular intervals, looking for the start
// marker first, then the end marker. Once both markers are found, it extracts
// the command output and return code.
//
// Parameters:
//   - command: The original command string (for logging purposes)
//   - fullCommand: The complete command with markers as sent to the VM
//   - startMarker: Unique string marking the beginning of command output
//   - endMarker: Unique string marking the end of command output
//   - timeout: Maximum time to wait for command completion
//
// Returns:
//   - string: The extracted command output between markers
//   - error: An error if timeout occurs or command parsing fails
func (c *CLIManager) waitForCommandCompletionWithMarkers(command, fullCommand, startMarker, endMarker string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	foundStart := false

	for time.Now().Before(deadline) {
		c.inner.mutex.Lock()
		output := c.inner.outputBuffer.String()
		c.inner.mutex.Unlock()
		output = strings.ReplaceAll(output, fullCommand, "")

		// Look for start marker
		if !foundStart && strings.Contains(output, startMarker) {
			foundStart = true
			c.log.Debugf("DEBUG: Found start marker for command: %s", command)
		}

		// Look for end marker after start marker found
		if foundStart && strings.Contains(output, endMarker) {
			c.log.Debugf("DEBUG: Found end marker for command: %s", command)
			return c.extractCommandOutputWithMarkers(output, startMarker, endMarker)
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Return whatever output we have, even if incomplete
	c.inner.mutex.Lock()
	output := c.inner.outputBuffer.String()
	c.inner.mutex.Unlock()

	return output, fmt.Errorf("command timeout after %v (start found: %v)", timeout, foundStart)
}

// extractCommandOutputWithMarkers parses the raw VM output to extract the actual
// command output between the specified start and end markers. The method also
// extracts the command's exit code and filters out shell prompts and control
// characters to provide clean, usable output.
//
// The parsing process includes:
//   - Locating start and end markers in the output stream
//   - Filtering out shell prompts and command echoes
//   - Cleaning control characters and ANSI escape sequences
//   - Extracting return code from the end marker line
//   - Validating command success based on exit code
//
// Parameters:
//   - output: Raw output buffer containing the complete command execution
//   - startMarker: Unique string that marks the beginning of command output
//   - endMarker: Unique string that marks the end of command output
//
// Returns:
//   - string: Clean command output with control characters removed
//   - error: An error if the command failed (non-zero exit code)
func (c *CLIManager) extractCommandOutputWithMarkers(output, startMarker, endMarker string) (string, error) {
	lines := strings.Split(output, "\n")
	var resultLines []string
	foundStart := false
	retCode := 0

	for _, line := range lines {
		line = c.cleanControlCharacters(line)
		if line == "" {
			continue
		}

		// Look for start marker
		if !foundStart && strings.Contains(line, startMarker) {
			foundStart = true
			continue
		}

		// Look for end marker
		if foundStart && strings.Contains(line, endMarker) {
			// Extract return code if present in the format =<code>=
			if matches := retCodeRegex.FindStringSubmatch(line); len(matches) > 1 {
				if code, err := strconv.Atoi(matches[1]); err == nil {
					retCode = code
				}
			}
			break
		}

		if foundStart {
			// Skip shell prompts and command echoes
			if !c.isShellPrompt(line) {
				resultLines = append(resultLines, line)
			}
		}
	}

	// Check if command failed based on return code
	if retCode != 0 {
		return strings.Join(resultLines, "\n"), fmt.Errorf("command failed with exit code %d", retCode)
	}

	return strings.Join(resultLines, "\n"), nil
}

// isShellPrompt checks if a line is a shell prompt in the virtual machine.
// Detects command line prompts by the following criteria:
// - Line starts with "root@yanet-vm" or "ubuntu@yanet-vm"
// - Contains "# " or "$ " (prompt ending marker)
// Examples of matching strings:
//
//	"root@yanet-vm# "
//	"ubuntu@yanet-vm$ "
//
// Parameters:
//
//	line - console output line to check
//
// Returns:
//
//	true if the line is a shell prompt, false otherwise
func (c *CLIManager) isShellPrompt(line string) bool {
	return (strings.HasPrefix(line, "root@yanet-vm") || strings.HasPrefix(line, "ubuntu@yanet-vm")) &&
		(strings.Contains(line, "# ") || strings.Contains(line, "$ "))
}

// cleanControlCharacters removes ANSI escape sequences, control characters,
// and other terminal formatting artifacts from a line of text. This ensures
// that command output is clean and suitable for programmatic processing.
//
// The cleaning process removes:
//   - ANSI escape sequences (color codes, cursor positioning, etc.)
//   - Carriage returns and null bytes
//   - All control characters in the range 0x00-0x1f and 0x7f
//   - Leading and trailing whitespace
//
// Parameters:
//   - line: Raw text line from the virtual machine console
//
// Returns:
//   - string: Cleaned text with control characters and formatting removed
//
// Example:
//
//	raw := "\x1b[32mHello\x1b[0m World\r\n"
//	clean := cli.cleanControlCharacters(raw)
//	// Result: "Hello World"
func (c *CLIManager) cleanControlCharacters(line string) string {
	// Remove ANSI escape sequences (like \x1b[?2004l)
	re := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	cleaned := re.ReplaceAllString(line, "")

	// Remove carriage returns and other control characters
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")

	// Remove any remaining control characters
	re2 := regexp.MustCompile(`[\x00-\x1f\x7f]`)
	cleaned = re2.ReplaceAllString(cleaned, "")

	// Trim whitespace
	cleaned = strings.TrimSpace(cleaned)

	return cleaned
}

// Close performs cleanup operations for the CLI manager, ensuring proper
// resource deallocation and stopping background processes. This method should
// be called when the CLI manager is no longer needed to prevent resource leaks.
//
// Currently, this method primarily handles mutex cleanup and prepares for
// future resource management needs. It's safe to call multiple times.
//
// Returns:
//   - error: Always returns nil in the current implementation, but the error
//     return type is maintained for future compatibility and consistency
//     with the io.Closer interface pattern
func (c *CLIManager) Close() error {
	// Stop the background reader
	c.inner.mutex.Lock()
	defer c.inner.mutex.Unlock()

	return nil
}

// WithLog creates a new CLIManager instance with a different logger
// while sharing the same underlying connection (inner state).
// This allows each test to have its own logging context while sharing
// the CLI manager connection.
//
// Parameters:
//   - log: Logger to use for this CLI manager instance
//
// Returns:
//   - *CLIManager: A new CLI manager instance with the specified logger
//
// Example:
//
//	namedCLI := cli.WithLog(logger.Named("test1"))
func (c *CLIManager) WithLog(log *zap.SugaredLogger) *CLIManager {
	// Update shared logger atomically so background goroutine uses it
	c.inner.log.Store(log)

	return &CLIManager{
		inner: c.inner, // Share the same inner state (connection)
		log:   log,
	}
}
