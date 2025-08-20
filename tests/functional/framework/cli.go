package framework

import (
	"bufio"
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
	retCodeRegex = regexp.MustCompile(`=(\d+)=`)
)

// CLIManager handles YANET CLI operations
type CLIManager struct {
	qemu         *QEMUManager
	outputBuffer strings.Builder
	mutex        sync.Mutex
	reader       *bufio.Scanner
	log          *zap.SugaredLogger
	cmdMutex     sync.Mutex
}

// CLIOption defines functional options for CLIManager
type CLIOption func(*CLIManager) error

// CLIWithLog sets the logger for the CLIManager
func CLIWithLog(log *zap.SugaredLogger) CLIOption {
	return func(cm *CLIManager) error {
		cm.log = log
		return nil
	}
}

// NewCLIManager creates a new CLI manager instance
func NewCLIManager(qemu *QEMUManager, opts ...CLIOption) (*CLIManager, error) {
	cm := &CLIManager{
		qemu: qemu,
		log:  zap.NewNop().Sugar(), // default noop logger
	}

	// Apply functional options
	for _, opt := range opts {
		if err := opt(cm); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	return cm, nil
}

// ExecuteCommand executes a CLI command in the QEMU VM via serial console
func (c *CLIManager) ExecuteCommand(command string) (string, error) {
	c.cmdMutex.Lock()
	defer c.cmdMutex.Unlock()
	if c.qemu == nil || c.qemu.Command == nil || c.qemu.Command.Process == nil {
		return "", fmt.Errorf("QEMU VM is not running")
	}

	// Wait for VM to be ready first
	if !c.qemu.IsVMReady() {
		return "", fmt.Errorf("VM not ready")
	}

	// Check if we have stdin/stdout pipes
	stdin := c.qemu.GetStdin()
	stdout := c.qemu.GetStdout()

	if stdin == nil || stdout == nil {
		return "", fmt.Errorf("failed to connect to QEMU serial console")
	}

	c.log.Debugf("DEBUG: Executing command in VM via serial console: %s", command)

	// Initialize reader if not already done
	if c.reader == nil {
		c.reader = bufio.NewScanner(stdout)
		// Start background reader to capture output
		go c.readOutput()
	}

	// Clear output buffer
	c.mutex.Lock()
	c.outputBuffer.Reset()
	c.mutex.Unlock()

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

func (c *CLIManager) ExecuteCommands(commands ...string) ([]string, error) {
	outputs := make([]string, 0, len(commands))
	for _, cmd := range commands {
		output, err := c.ExecuteCommand(cmd)
		outputs = append(outputs, output)
		if err != nil {
			return outputs, fmt.Errorf("failed to execute common config command '%s': %w", cmd, err)
		}
	}
	return outputs, nil
}

// readOutput continuously reads output from QEMU stdout
func (c *CLIManager) readOutput() {
	for c.reader.Scan() {
		line := c.reader.Text()

		c.mutex.Lock()
		c.outputBuffer.WriteString(line + "\n")
		c.mutex.Unlock()

		c.log.Debugf("DEBUG: VM output: %s", line)
	}

	if err := c.reader.Err(); err != nil {
		c.log.Debugf("DEBUG: Error reading VM output: %v", err)
	}
}

// waitForCommandCompletionWithMarkers waits for command completion using start/end markers
func (c *CLIManager) waitForCommandCompletionWithMarkers(command, fullCommand, startMarker, endMarker string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	foundStart := false

	for time.Now().Before(deadline) {
		c.mutex.Lock()
		output := c.outputBuffer.String()
		c.mutex.Unlock()
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
	c.mutex.Lock()
	output := c.outputBuffer.String()
	c.mutex.Unlock()

	return output, fmt.Errorf("command timeout after %v (start found: %v)", timeout, foundStart)
}

// extractCommandOutputWithMarkers extracts command output between start and end markers
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

		// Collect lines between markers
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

// isShellPrompt checks if a line is a shell prompt
func (c *CLIManager) isShellPrompt(line string) bool {
	return (strings.HasPrefix(line, "root@yanet-vm") || strings.HasPrefix(line, "ubuntu@yanet-vm")) &&
		(strings.Contains(line, "# ") || strings.Contains(line, "$ "))
}

// cleanControlCharacters removes ANSI escape sequences and control characters
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

// Close closes any connections
func (c *CLIManager) Close() error {
	// Stop the background reader
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return nil
}
