package framework

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Regular expressions used for parsing command output
var (
	retCodeRegex       = regexp.MustCompile(`=(\d+)=`)
	errMarkersNotFound = errors.New("markers not found in output")
)

// cliManagerInner holds the shared connection state that should not be copied
// between CLIManager instances. This allows multiple CLIManager wrappers
// with different loggers to share the same underlying connection.
type cliManagerInner struct {
	qemu     *QEMUManager // QEMU virtual machine manager instance
	cmdMutex sync.Mutex   // Ensures sequential command execution
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
	inner := &cliManagerInner{
		qemu: qemu,
	}

	cm := &CLIManager{
		inner: inner,
		log:   zap.NewNop().Sugar(),
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

	// Check if we have a stdin pipe.
	stdin := c.inner.qemu.GetStdin()

	if stdin == nil {
		return "", fmt.Errorf("failed to connect to QEMU serial console")
	}

	c.log.Debugf("DEBUG: Executing command in VM %s: %s", c.inner.qemu.Name, command)

	// Send command to VM with a unique marker for better parsing.
	// We do NOT reset the serial buffer here to avoid a race with the
	// readSerial goroutine that may still be writing output from the
	// previous command. The marker parser handles stale data by finding
	// the LAST start marker before the end marker.
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

// ExecuteCommandWithTimeout is like ExecuteCommand but with a custom timeout.
func (c *CLIManager) ExecuteCommandWithTimeout(command string, timeout time.Duration) (string, error) {
	c.inner.cmdMutex.Lock()
	defer c.inner.cmdMutex.Unlock()
	if c.inner.qemu == nil || c.inner.qemu.Command == nil || c.inner.qemu.Command.Process == nil {
		return "", fmt.Errorf("QEMU VM is not running")
	}
	if !c.inner.qemu.IsVMReady() {
		return "", fmt.Errorf("VM not ready")
	}
	stdin := c.inner.qemu.GetStdin()
	if stdin == nil {
		return "", fmt.Errorf("failed to connect to QEMU serial console")
	}
	c.log.Debugf("DEBUG: Executing command in VM %s (timeout %v): %s", c.inner.qemu.Name, timeout, command)
	tm := time.Now().UnixNano()
	commandMarker := fmt.Sprintf("CMD_START_%d", tm)
	endMarker := fmt.Sprintf("CMD_END_%d", tm)
	fullCommand := fmt.Sprintf("echo '%s'; %s; echo \"=$?=%s\"\n", commandMarker, command, endMarker)
	_, err := stdin.Write([]byte(fullCommand))
	if err != nil {
		return "", fmt.Errorf("failed to send command to VM: %w", err)
	}
	return c.waitForCommandCompletionWithMarkers(command, fullCommand, commandMarker, endMarker, timeout)
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
	parseRetries := 0

	for time.Now().Before(deadline) {
	output := c.inner.qemu.serialBufferSnapshot()
	// Normalize \r\n → \n before stripping command echo so ReplaceAll
	// matches even when the shell echoes the command with CRLF line endings.
	output = strings.ReplaceAll(strings.ReplaceAll(output, "\r\n", "\n"), fullCommand, "")

		// Look for start marker
		if !foundStart && strings.Contains(output, startMarker) {
			foundStart = true
			c.log.Debugf("DEBUG: Found start marker for command: %s", command)
		}

		// Look for end marker after start marker found
		if foundStart && strings.Contains(output, endMarker) {
			result, err := c.extractCommandOutputWithMarkers(output, startMarker, endMarker)
			if err == nil {
				c.log.Debugf("DEBUG: Found end marker for command: %s", command)
				return result, nil
			}
			if errors.Is(err, errMarkersNotFound) {
				parseRetries++
				if parseRetries == 1 {
					c.log.Debugf("DEBUG: Marker parse race for command %q, retrying (buffer len=%d)", command, len(output))
				}
				time.Sleep(20 * time.Millisecond)
				continue
			}
			return result, err
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Return whatever output we have, even if incomplete.
	output := c.inner.qemu.serialBufferSnapshot()
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

	// Find the LAST occurrence of startMarker before endMarker.
	// This handles the case where a stale marker from a previous command
	// leaks into the buffer after resetSerialBuffer() because readSerial
	// had already appended the line before the reset took effect.
	lastStartIdx := -1
	endIdx := -1
	for i, line := range lines {
		cleaned := c.cleanControlCharacters(line)
		if strings.Contains(cleaned, startMarker) {
			lastStartIdx = i
		}
		// Require endIdx > lastStartIdx so the shell command echo (which
		// contains both markers on the same line) does not fool the parser.
		if strings.Contains(cleaned, endMarker) && lastStartIdx >= 0 && i > lastStartIdx {
			endIdx = i
			break
		}
	}

	if lastStartIdx < 0 || endIdx < 0 {
		return output, errMarkersNotFound
	}

	var resultLines []string
	retCode := 0

	for i := lastStartIdx + 1; i < endIdx; i++ {
		line := c.cleanControlCharacters(lines[i])
		if line == "" {
			continue
		}
		if !c.isShellPrompt(line) {
			resultLines = append(resultLines, line)
		}
	}

	// Extract return code from the end marker line
	endLine := c.cleanControlCharacters(lines[endIdx])
	if matches := retCodeRegex.FindStringSubmatch(endLine); len(matches) > 1 {
		if code, err := strconv.Atoi(matches[1]); err == nil {
			retCode = code
		}
	}

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
	// Update the serial reader's logger atomically so VM output is attributed
	// to the current test's logger.
	c.inner.qemu.setSerialLogger(log)

	return &CLIManager{
		inner: c.inner, // Share the same inner state (connection)
		log:   log,
	}
}
