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
	isReading    bool
	log          *zap.SugaredLogger
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
	commandMarker := fmt.Sprintf("CMD_START_%d", time.Now().UnixNano())
	endMarker := fmt.Sprintf("CMD_END_%d", time.Now().UnixNano())

	fullCommand := fmt.Sprintf("echo '%s'; %s; echo \"=$?=%s\"\n", commandMarker, command, endMarker)
	_, err := stdin.Write([]byte(fullCommand))
	if err != nil {
		return "", fmt.Errorf("failed to send command to VM: %w", err)
	}

	// Wait for command completion and collect output
	return c.waitForCommandCompletionWithMarkers(command, fullCommand, commandMarker, endMarker, 30*time.Second)
}

// readOutput continuously reads output from QEMU stdout
func (c *CLIManager) readOutput() {
	c.mutex.Lock()
	c.isReading = true
	c.mutex.Unlock()

	for c.reader.Scan() {
		line := c.reader.Text()

		c.mutex.Lock()
		c.outputBuffer.WriteString(line + "\n")
		c.mutex.Unlock()

		c.log.Debugf("DEBUG: VM output: %s", line)
	}

	c.mutex.Lock()
	c.isReading = false
	c.mutex.Unlock()

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
		output = strings.Replace(output, fullCommand, "", 1)

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
		line = strings.TrimSpace(line)

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
		if foundStart && line != "" {
			// Skip shell prompts and command echoes
			if !c.isShellPrompt(line) {
				// Clean ANSI escape sequences and control characters
				cleanLine := c.cleanControlCharacters(line)
				if cleanLine != "" {
					resultLines = append(resultLines, cleanLine)
				}
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
	return (strings.Contains(line, "root@") || strings.Contains(line, "ubuntu@")) &&
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
	c.isReading = false
	c.mutex.Unlock()

	return nil
}
