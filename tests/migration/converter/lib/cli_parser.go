package lib

import (
	"fmt"
	"strings"
)

// CLICheckParse holds parsed data from a cli_check block
type CLICheckParse struct {
	CommandPayloads []string
	ExpectedLines   []string
	Regexes         []string
	OriginalLines   []string
}

// ParseCLICheckBlock parses a cli_check block content:
// - lines beginning with YANET_FORMAT_COLUMNS= are treated as command payloads
// - EXPECT_BEGIN/EXPECT_END enclose expected output lines
// - EXPECT_REGEX:<pattern> appends regex patterns
func ParseCLICheckBlock(content string) (*CLICheckParse, error) {
	lines := strings.Split(content, "\n")
	var payloads []string
	var expected []string
	var regexes []string
	var original []string
	captureExpected := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			// terminate expected capture but keep structural blank lines in original comment
			captureExpected = false
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}

		original = append(original, line)

		switch {
		case strings.HasPrefix(line, "YANET_FORMAT_COLUMNS="):
			captureExpected = false
			payload := strings.TrimPrefix(line, "YANET_FORMAT_COLUMNS=")
			payloads = append(payloads, payload)
		case strings.HasPrefix(line, "EXPECT_REGEX:"):
			captureExpected = false
			regex := strings.TrimSpace(strings.TrimPrefix(line, "EXPECT_REGEX:"))
			if regex != "" {
				regexes = append(regexes, regex)
			}
		case strings.EqualFold(line, "EXPECT_BEGIN"):
			captureExpected = true
		case strings.EqualFold(line, "EXPECT_END"):
			captureExpected = false
		case strings.Contains(line, "---------"):
			if captureExpected {
				expected = append(expected, line)
			}
		case captureExpected:
			expected = append(expected, line)
		default:
			// heuristics: if no markers provided and line looks like output, track it
			if len(line) > 0 && !strings.HasPrefix(line, "module") && len(payloads) > 0 {
				expected = append(expected, line)
			}
		}
	}

	return &CLICheckParse{
		CommandPayloads: payloads,
		ExpectedLines:   expected,
		Regexes:         regexes,
		OriginalLines:   original,
	}, nil
}

// CLICommand represents a parsed CLI command with its components
type CLICommand struct {
	Command    string   // Base command (e.g., "balancer")
	Subcommand string   // Subcommand (e.g., "real")
	Parameters []string // List of parameters
	Raw        string   // Original raw command string
}

// ParseCLICommand parses a yanet1 CLI command into its components
func ParseCLICommand(cmdStr string) (*CLICommand, error) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return nil, fmt.Errorf("empty command")
	}

	// Split command by spaces, respecting quotes for multi-word parameters
	parts := splitCommandLine(cmdStr)
	if len(parts) < 1 {
		return nil, fmt.Errorf("invalid command format: %s", cmdStr)
	}

	cmd := &CLICommand{
		Raw:        cmdStr,
		Parameters: []string{},
	}

	if len(parts) == 1 {
		cmd.Command = parts[0]
	} else if len(parts) >= 2 {
		cmd.Command = parts[0]
		cmd.Subcommand = parts[1]
		if len(parts) > 2 {
			cmd.Parameters = parts[2:]
		}
	}

	return cmd, nil
}

// splitCommandLine splits a command line string into parts, respecting quoted arguments
func splitCommandLine(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return []string{}
	}

	var parts []string
	var current strings.Builder
	var inDoubleQuotes bool
	var inSingleQuotes bool
	var escapeNext bool

	for i, r := range cmd {
		if escapeNext {
			current.WriteRune(r)
			escapeNext = false
			continue
		}

		switch r {
		case '\\':
			if inDoubleQuotes || inSingleQuotes {
				escapeNext = true
			} else {
				current.WriteRune(r)
			}
		case '"':
			if !inSingleQuotes {
				inDoubleQuotes = !inDoubleQuotes
			} else {
				current.WriteRune(r)
			}
		case '\'':
			if !inDoubleQuotes {
				inSingleQuotes = !inSingleQuotes
			} else {
				current.WriteRune(r)
			}
		case ' ', '\t':
			if !inDoubleQuotes && !inSingleQuotes {
				if current.Len() > 0 {
					parts = append(parts, current.String())
					current.Reset()
				}
				continue
			}
			fallthrough
		default:
			current.WriteRune(r)
		}

		// Handle last character
		if i == len(cmd)-1 && current.Len() > 0 {
			parts = append(parts, current.String())
		}
	}

	if escapeNext {
		return []string{cmd} // Return original if escape sequence was incomplete
	}

	if inDoubleQuotes || inSingleQuotes {
		return []string{cmd} // Return original if quotes were not closed
	}

	return parts
}

// ExtractParameter extracts a parameter by name from command parameters
// Supports formats: "--param value" or "--param=value"
func ExtractParameter(cmd *CLICommand, paramName string) (string, bool) {
	paramPrefix := "--" + paramName

	for i, param := range cmd.Parameters {
		// Check for --param=value format
		if strings.HasPrefix(param, paramPrefix+"=") {
			return strings.TrimPrefix(param, paramPrefix+"="), true
		}

		// Check for --param value format
		if param == paramPrefix && i+1 < len(cmd.Parameters) {
			return cmd.Parameters[i+1], true
		}
	}

	return "", false
}

// ExtractQuotedParameter extracts a parameter value that was originally quoted
// Removes surrounding quotes and handles escape sequences
func ExtractQuotedParameter(cmd *CLICommand, paramName string) (string, bool) {
	value, found := ExtractParameter(cmd, paramName)
	if !found {
		return "", false
	}

	// Remove surrounding quotes if present
	if (strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
		(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		value = value[1 : len(value)-1]
		// Basic unescape
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\'`, `'`)
		value = strings.ReplaceAll(value, `\\`, `\`)
	}

	return value, true
}

// FormatYanet2Command formats a yanet2 CLI command with module and action
func FormatYanet2Command(module, action string, params map[string]string, instances ...int) string {
	var parts []string

	// Base command
	parts = append(parts, module)

	// Action
	if action != "" {
		parts = append(parts, action)
	}

	// Instance configuration
	if len(instances) > 0 {
		parts = append(parts, "--instances")
		instanceStrs := make([]string, len(instances))
		for i, inst := range instances {
			instanceStrs[i] = fmt.Sprintf("%d", inst)
		}
		parts = append(parts, strings.Join(instanceStrs, ","))
	}

	// Configuration parameters - sort keys for deterministic output
	var keys []string
	for key := range params {
		if params[key] != "" {
			keys = append(keys, key)
		}
	}
	
	// Sort keys alphabetically for consistent output
	sortKeys := func(keys []string) {
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				if keys[i] > keys[j] {
					keys[i], keys[j] = keys[j], keys[i]
				}
			}
		}
	}
	sortKeys(keys)

	for _, key := range keys {
		value := params[key]
		// Quote value if it contains spaces
		if strings.Contains(value, " ") {
			value = fmt.Sprintf(`"%s"`, value)
		}
		parts = append(parts, fmt.Sprintf("--%s", key), value)
	}

	return strings.Join(parts, " ")
}

// ValidateCLICommand validates that a CLI command is well-formed
func ValidateCLICommand(cmd *CLICommand) error {
	if cmd == nil {
		return fmt.Errorf("command is nil")
	}

	if cmd.Command == "" {
		return fmt.Errorf("command is empty")
	}

	// Validate known commands
	validCommands := map[string]bool{
		"balancer": true,
		"nat64":    true,
		"route":    true,
		"acl":      true,
		"pipeline": true,
		"decap":    true,
		"forward":  true,
	}

	if !validCommands[cmd.Command] {
		return fmt.Errorf("unknown command: %s", cmd.Command)
	}

	// Validate balancer subcommands
	if cmd.Command == "balancer" {
		validSubcommands := map[string]bool{
			"real":    true,
			"service": true,
			"module":  true,
		}

		if cmd.Subcommand != "" && !validSubcommands[cmd.Subcommand] {
			return fmt.Errorf("unknown balancer subcommand: %s", cmd.Subcommand)
		}
	}

	// Validate nat64 subcommands
	if cmd.Command == "nat64" {
		validSubcommands := map[string]bool{
			"prefix":  true,
			"mapping": true,
		}

		if cmd.Subcommand != "" && !validSubcommands[cmd.Subcommand] {
			return fmt.Errorf("unknown nat64 subcommand: %s", cmd.Subcommand)
		}
	}

	return nil
}

// containsAllParts checks if all required parts are present in command
func containsAllParts(cmd *CLICommand, requiredParts []string) bool {
	for _, part := range requiredParts {
		if cmd.Command != part && cmd.Subcommand != part {
			found := false
			for _, param := range cmd.Parameters {
				if strings.Contains(param, part) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// contains checks if a substring exists in any command part
func contains(cmd *CLICommand, substr string) bool {
	return strings.Contains(cmd.Command, substr) ||
		strings.Contains(cmd.Subcommand, substr) ||
		strings.Contains(cmd.Raw, substr)
}

// containsSubstring checks if any parameter contains a specific substring
func containsSubstring(cmd *CLICommand, substr string) bool {
	for _, param := range cmd.Parameters {
		if strings.Contains(param, substr) {
			return true
		}
	}
	return false
}

// CommandBuilder helps build yanet2 CLI commands step by step
type CommandBuilder struct {
	module    string
	action    string
	config    string
	instances []int
	params    map[string]string
}

// NewCommandBuilder creates a new command builder
func NewCommandBuilder(module string) *CommandBuilder {
	return &CommandBuilder{
		module: module,
		params: make(map[string]string),
	}
}

// Action sets the action for the command
func (cb *CommandBuilder) Action(action string) *CommandBuilder {
	cb.action = action
	return cb
}

// Config sets the configuration name
func (cb *CommandBuilder) Config(config string) *CommandBuilder {
	cb.config = config
	return cb
}

// Instances sets the instance numbers
func (cb *CommandBuilder) Instances(instances ...int) *CommandBuilder {
	cb.instances = instances
	return cb
}

// Param adds a parameter
func (cb *CommandBuilder) Param(key, value string) *CommandBuilder {
	cb.params[key] = value
	return cb
}

// Build constructs the final command string
func (cb *CommandBuilder) Build() string {
	params := make(map[string]string)

	// Add config as a parameter if specified
	if cb.config != "" {
		params["cfg"] = cb.config
	}

	// Add custom parameters
	for k, v := range cb.params {
		params[k] = v
	}

	return FormatYanet2Command(cb.module, cb.action, params, cb.instances...)
}
