package lib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// GoTestData contains data for generating Go test
type GoTestData struct {
	TestName           string
	OriginalTestName   string
	TestType           string
	Steps              []ConvertedStep
	ControlplaneConfig string
	ParsedConfig       *ControlplaneConfig // Parsed configuration
	PcapFiles          []PcapFileInfo
}

// ConvertedStep represents a converted test step
type ConvertedStep struct {
	Type         string
	GoCode       string
	Description  string
	Functions    []string         // Additional functions for this step
	PacketTests  []PacketTestCase // List of packet test cases
	OriginalYAML string           // Original YAML content from autotest.yaml for debugging
}

// PacketTestCase contains information about one packet test case
type PacketTestCase struct {
	SendPcap           string        // Send pcap file name
	ExpectPcap         string        // Expect pcap file name
	SendPackets        []*PacketInfo // Information about sent packets
	ExpectPackets      []*PacketInfo // Information about expected packets
	IsDropExpected     bool          // true if packet should be dropped (empty expect file)
	FunctionName       string        // Function name for packet creation (returns slice)
	PacketNumber       int           // Packet number
	ExpectFunctionName string        // Function name for expected packet creation
}

// PcapFileInfo contains information about pcap file
type PcapFileInfo struct {
	Name        string
	Path        string
	Type        string // "send" or "expect"
	Description string
	PacketInfo  *PacketInfo // Packet information
}

// TestInfo contains information about a discovered test
type TestInfo struct {
	Name string // Test directory name (e.g., "009_nat64stateless")
	Dir  string // Full path to test directory
	Path string // Full path to autotest.yaml
}

// StepInfo contains information about a test step
type StepInfo struct {
	Index   int         // 1-based step index
	Name    string      // Formatted step name (e.g., "Step_001")
	Type    string      // Step type (e.g., "sendPackets", "cli")
	Content interface{} // Step content from YAML
}

// StepCallback is called for each step during iteration
type StepCallback func(stepInfo StepInfo, testInfo TestInfo) error

// DiscoverTests discovers all tests in the given one-port directory
// Parameters:
//   - onePortDir: Path to the 001_one_port directory
//   - onlyTest: Optional test name filter (from ONLY_TEST env var)
//   - skipTests: Map of test names to skip reasons (e.g., map[string]string{"059_rib": "known YAML parsing issues"})
//
// Returns:
//   - []TestInfo: List of discovered tests
//   - error: Error if directory cannot be read
func DiscoverTests(onePortDir string, onlyTest string, skipTests map[string]string) ([]TestInfo, error) {
	entries, err := os.ReadDir(onePortDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", onePortDir, err)
	}

	var tests []TestInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		testName := entry.Name()
		if onlyTest != "" && testName != onlyTest {
			continue
		}

		testDir := filepath.Join(onePortDir, testName)
		autotestPath := filepath.Join(testDir, "autotest.yaml")
		if _, err := os.Stat(autotestPath); os.IsNotExist(err) {
			continue
		}

		// Check if test should be skipped
		if _, shouldSkip := skipTests[testName]; shouldSkip && onlyTest != testName {
			// Skip this test unless it's explicitly requested
			continue
		}

		tests = append(tests, TestInfo{
			Name: testName,
			Dir:  testDir,
			Path: autotestPath,
		})
	}

	return tests, nil
}

// IterateSteps iterates over steps in a test, calling the callback for each matching step
// Parameters:
//   - testInfo: Test information
//   - stepTypeFilter: Filter by step type (empty string means all types)
//   - onlyStep: Optional step filter (e.g., "003" for Step_003)
//   - callback: Function called for each matching step
//
// Returns:
//   - error: Error if test cannot be parsed or callback returns error
func IterateSteps(testInfo TestInfo, stepTypeFilter string, onlyStep string, callback StepCallback) error {
	test, err := ParseAutotestYAML(testInfo.Dir)
	if err != nil {
		return fmt.Errorf("failed to parse autotest.yaml: %w", err)
	}

	stepIndex := 0
	for _, step := range test.Steps {
		for stepType, content := range step {
			// Filter by step type if specified
			if stepTypeFilter != "" && stepType != stepTypeFilter {
				continue
			}

			stepIndex++
			stepName := fmt.Sprintf("Step_%03d", stepIndex)

			// Filter by step name if specified
			if onlyStep != "" {
				expectedStepName := fmt.Sprintf("Step_%s", onlyStep)
				if stepName != expectedStepName {
					continue
				}
			}

			stepInfo := StepInfo{
				Index:   stepIndex,
				Name:    stepName,
				Type:    stepType,
				Content: content,
			}

			if err := callback(stepInfo, testInfo); err != nil {
				return err
			}
		}
	}

	return nil
}

// IterateSendPacketsSteps iterates over sendPackets steps, extracting send/expect file pairs
// Parameters:
//   - testInfo: Test information
//   - onlyStep: Optional step filter (e.g., "003" for Step_003)
//   - callback: Function called for each packet in each sendPackets step
//     Parameters: stepInfo, sendFile, expectFile
//
// Returns:
//   - error: Error if test cannot be parsed or callback returns error
func IterateSendPacketsSteps(testInfo TestInfo, onlyStep string, callback func(stepInfo StepInfo, sendFile, expectFile string) error) error {
	return IterateSteps(testInfo, "sendPackets", onlyStep, func(stepInfo StepInfo, testInfo TestInfo) error {
		packets, ok := stepInfo.Content.([]interface{})
		if !ok {
			// Invalid format, skip this step
			return nil
		}

		for _, pkt := range packets {
			sendFile, expectFile := ParseSendExpectFiles(pkt)
			if sendFile == "" {
				continue
			}

			if err := callback(stepInfo, sendFile, expectFile); err != nil {
				return err
			}
		}

		return nil
	})
}

// ParseSendExpectFiles extracts send and expect file names from a packet entry
// This is a standalone function that can be used in tests
func ParseSendExpectFiles(packet interface{}) (sendFile, expectFile string) {
	if packetMap, ok := packet.(map[interface{}]interface{}); ok {
		if s, exists := packetMap["send"]; exists {
			sendFile = fmt.Sprintf("%v", s)
		}
		if e, exists := packetMap["expect"]; exists {
			expectFile = fmt.Sprintf("%v", e)
		}
	} else if packetMap, ok := packet.(map[string]interface{}); ok {
		if s, exists := packetMap["send"]; exists {
			sendFile = fmt.Sprintf("%v", s)
		}
		if e, exists := packetMap["expect"]; exists {
			expectFile = fmt.Sprintf("%v", e)
		}
	}
	return sendFile, expectFile
}

// GetYanet1Root gets the yanet1 root directory path from environment variable
// Returns the default path "../../../../../yanet1" if YANET1_ROOT is not set
func GetYanet1Root() string {
	yanet1Root := os.Getenv("YANET1_ROOT")
	if yanet1Root == "" {
		yanet1Root = "../../../../../yanet1"
	}
	return yanet1Root
}

// GetYanet1OnePortDir gets the path to 001_one_port directory and validates it exists
// Returns an error if the directory doesn't exist
func GetYanet1OnePortDir() (string, error) {
	yanet1Root := GetYanet1Root()
	onePortDir := filepath.Join(yanet1Root, "autotest/units/001_one_port")
	if _, err := os.Stat(onePortDir); os.IsNotExist(err) {
		return "", fmt.Errorf("yanet1 directory not found at %s. Set YANET1_ROOT to yanet1 repository location", onePortDir)
	}
	return onePortDir, nil
}

// convertStepsWithSkip applies skiplist/test defaults and passes stripVLAN to sendPackets steps
func (c *Converter) convertStepsWithSkip(testName string, steps []map[string]interface{}, testPath string) []ConvertedStep {
	var converted []ConvertedStep
	c.debugLog("Converting %d steps for test %s", len(steps), testName)
	for i, step := range steps {
		stepIndex := i + 1
		state := c.effectiveState(testName, stepIndex)
		c.debugLog("Step %d (index %d): state=%s", stepIndex, i, state)
		if state == StateDisabled {
			c.debugLog("Skipping step %d due to skiplist: disabled", stepIndex)
			continue
		}
		for stepType, content := range step {
			c.debugLog("Processing step %d type: %s", stepIndex, stepType)
			convertedStep := c.convertStepWithState(stepType, content, testPath, state, testName)
			c.debugLog("Converted step %d: GoCode=%d bytes, PacketTests=%d", stepIndex, len(convertedStep.GoCode), len(convertedStep.PacketTests))
			if convertedStep.GoCode != "" || len(convertedStep.PacketTests) > 0 {
				converted = append(converted, convertedStep)
			}
		}
	}
	c.debugLog("Total converted steps: %d", len(converted))
	return converted
}

// convertStepWithState converts one step with state information
func (c *Converter) convertStepWithState(stepType string, content interface{}, testPath string, state StepState, testName string) ConvertedStep {
	switch stepType {
	case "ipv4Update":
		return c.convertRouteUpdate(content, stepType, false)
	case "ipv6Update":
		return c.convertRouteUpdate(content, stepType, true)
	case "ipv4LabelledUpdate":
		return c.convertIPv4LabelledUpdate(content, stepType)
	case "ipv4Remove":
		return c.convertRouteRemove(content, stepType, false)
	case "ipv4LabelledRemove":
		return c.convertRouteRemove(content, stepType, true)
	case "sendPackets":
		stripVLAN := (state == StateWoVLAN)
		return c.convertSendPacketsWithOptions(content, testPath, stripVLAN, testName)
	case "checkCounters":
		return c.convertCheckCounters(content)
	case "cli":
		return c.convertCLI(content)
	case "cli_check":
		return c.convertCLICheck(content)
	case "sleep":
		return c.convertSleep(content)
	default:
		return ConvertedStep{Type: stepType}
	}
}

// generateYAMLComment creates YAML comment header for route steps
func (c *Converter) generateYAMLComment(stepType string, items []string) string {
	var yaml strings.Builder
	yaml.WriteString("// Original autotest.yaml step:\n")
	yaml.WriteString(fmt.Sprintf("// %s:\n", stepType))
	for _, item := range items {
		yaml.WriteString(fmt.Sprintf("//   - \"%s\"\n", item))
	}
	return yaml.String()
}

// parseRouteString parses a route string in format "prefix -> nexthop" or "prefix -> nexthop:label"
// Returns prefix, nexthop (adapted), and whether parsing succeeded
func (c *Converter) parseRouteString(routeStr string, stripLabel bool) (prefix, nexthop string, ok bool) {
	parts := strings.Split(routeStr, " -> ")
	if len(parts) != 2 {
		return "", "", false
	}

	prefix = strings.TrimSpace(parts[0])
	nexthopPart := strings.TrimSpace(parts[1])

	// Strip label if requested (for labelled routes)
	if stripLabel {
		labelParts := strings.Split(nexthopPart, ":")
		if len(labelParts) >= 1 {
			nexthopPart = strings.TrimSpace(labelParts[0])
		}
	}

	// Adapt nexthop IP address to yanet2 infrastructure
	nexthop = c.adaptIPAddress(nexthopPart)
	c.debugLog("  Adapted nexthop: %s -> %s", nexthopPart, nexthop)

	return prefix, nexthop, true
}

// generateRouteCommands generates CLI commands for route operations
func (c *Converter) generateRouteCommands(routeStrings []string, operation string, stripLabel bool) []string {
	var commands []string
	for _, routeStr := range routeStrings {
		prefix, nexthop, ok := c.parseRouteString(routeStr, stripLabel)
		if !ok {
			c.debugLog("  Skipping invalid route string: %s", routeStr)
			continue
		}

		var cmd string
		switch operation {
		case "insert":
			cmd = fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`,
				framework.CLIRoute, nexthop, prefix)
		case "remove":
			cmd = fmt.Sprintf(`"%s remove --cfg route0 --instances 0 %s"`,
				framework.CLIRoute, prefix)
		default:
			c.debugLog("  Unknown route operation: %s", operation)
			continue
		}

		commands = append(commands, cmd)
		c.debugLog("  Generated route %s: %s", operation, cmd)
	}
	return commands
}

// convertRouteUpdate is a unified function for both IPv4 and IPv6 route updates
func (c *Converter) convertRouteUpdate(content interface{}, stepType string, isIPv6 bool) ConvertedStep {
	protocol := "IPv4"
	if isIPv6 {
		protocol = "IPv6"
	}

	c.debugLog("Converting %s route update", protocol)
	var routeStrings []string

	// Handle as string or array
	switch v := content.(type) {
	case string:
		routeStrings = []string{v}
	case []interface{}:
		for _, route := range v {
			if routeStr, ok := route.(string); ok {
				routeStrings = append(routeStrings, routeStr)
			}
		}
	default:
		return NewSkipStep(stepType, fmt.Sprintf("Invalid %sUpdate format", protocol))
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	// Generate route commands using helper
	commands := c.generateRouteCommands(routeStrings, "insert", false)

	if len(commands) == 0 {
		return NewSkipStep(stepType, fmt.Sprintf("No valid %s routes found", protocol))
	}

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to configure %s routes")`, yamlComment, strings.Join(commands, ",\n\t\t"), protocol)

	return ConvertedStep{
		Type:         stepType,
		GoCode:       goCode,
		Description:  fmt.Sprintf("%s routes configuration", protocol),
		OriginalYAML: yamlComment,
	}
}

// convertIPv4Update converts ipv4Update step - wrapper around convertRouteUpdate
func (c *Converter) convertIPv4Update(content interface{}, stepType string) ConvertedStep {
	return c.convertRouteUpdate(content, stepType, false)
}

// convertIPv6Update converts ipv6Update step - wrapper around convertRouteUpdate
func (c *Converter) convertIPv6Update(content interface{}, stepType string) ConvertedStep {
	return c.convertRouteUpdate(content, stepType, true)
}

// convertIPv4LabelledUpdate converts ipv4LabelledUpdate step
func (c *Converter) convertIPv4LabelledUpdate(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv4LabelledUpdate step")
	routes, ok := content.([]interface{})
	if !ok {
		return NewSkipStep("ipv4LabelledUpdate", "Invalid ipv4LabelledUpdate format")
	}

	var routeStrings []string
	for _, route := range routes {
		if routeStr, ok := route.(string); ok {
			routeStrings = append(routeStrings, routeStr)
		}
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	// Generate route commands using helper (strip labels)
	commands := c.generateRouteCommands(routeStrings, "insert", true)

	if len(commands) == 0 {
		return NewSkipStep(stepType, "No valid labelled routes found")
	}

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to configure IPv4 labelled routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "ipv4LabelledUpdate",
		GoCode:       goCode,
		Description:  "IPv4 routes configuration with labels",
		OriginalYAML: yamlComment,
	}
}

// convertRouteRemove is a unified function for both regular and labelled route removals
func (c *Converter) convertRouteRemove(content interface{}, stepType string, isLabelled bool) ConvertedStep {
	c.debugLog("Converting %s route removal", stepType)
	routes, ok := content.([]interface{})
	if !ok {
		return NewSkipStep(stepType, fmt.Sprintf("Invalid %s format", stepType))
	}

	var routeStrings []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	var commands []string
	for _, routeStr := range routeStrings {
		c.debugLog("  Route to remove: %s", routeStr)
		// Parse "10.0.0.0/24 -> 192.168.1.1" or "10.0.0.0/24 -> 192.168.1.1 label:transport1"
		parts := strings.Split(routeStr, " -> ")
		if len(parts) >= 2 {
			prefix := strings.TrimSpace(parts[0])
			routeRest := strings.TrimSpace(parts[1])

			// Extract nexthop and possibly label
			nexthopParts := strings.Split(routeRest, " label:")
			nexthop := strings.TrimSpace(nexthopParts[0])
			var label string
			if len(nexthopParts) > 1 {
				label = strings.TrimSpace(nexthopParts[1])
			}

			// Adapt nexthop IP address to yanet2 infrastructure
			adaptedNexthop := c.adaptIPAddress(nexthop)
			c.debugLog("  Adapted nexthop: %s -> %s", nexthop, adaptedNexthop)

			// Generate remove command (yanet2 CLI supports remove operation)
			if isLabelled && label != "" {
				// Labelled route removal
				cmd := fmt.Sprintf(`"%s remove --cfg route0 --instances 0 --via %s %s --label %s"`, framework.CLIRoute, adaptedNexthop, prefix, label)
				commands = append(commands, cmd)
				c.debugLog("  Generated labelled route remove: %s", cmd)
			} else {
				// Regular route removal
				cmd := fmt.Sprintf(`"%s remove --cfg route0 --instances 0 --via %s %s"`, framework.CLIRoute, adaptedNexthop, prefix)
				commands = append(commands, cmd)
				c.debugLog("  Generated route remove: %s", cmd)
			}
		}
	}

	if len(commands) == 0 {
		description := "IPv4 routes removal"
		if isLabelled {
			description = "IPv4 routes removal with labels"
		}
		goCode := fmt.Sprintf(`%s// No valid routes found for removal
    t.Logf("No %s to process")`, yamlComment, strings.ToLower(description))

		return ConvertedStep{
			Type:         stepType,
			GoCode:       goCode,
			Description:  description,
			OriginalYAML: yamlComment,
		}
	}

	protocol := "IPv4"
	description := "IPv4 routes removal"
	if isLabelled {
		description = "IPv4 routes removal with labels"
	}

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to remove %s routes")`, yamlComment, strings.Join(commands, ",\n\t\t"), strings.ToLower(protocol))

	return ConvertedStep{
		Type:         stepType,
		GoCode:       goCode,
		Description:  description,
		OriginalYAML: yamlComment,
	}
}

// convertIPv4Remove converts ipv4Remove step - wrapper around convertRouteRemove
func (c *Converter) convertIPv4Remove(content interface{}, stepType string) ConvertedStep {
	return c.convertRouteRemove(content, stepType, false)
}

// convertIPv4LabelledRemove converts ipv4LabelledRemove step - wrapper around convertRouteRemove
func (c *Converter) convertIPv4LabelledRemove(content interface{}, stepType string) ConvertedStep {
	return c.convertRouteRemove(content, stepType, true)
}

// convertSendPacketsWithASTParser uses new AST-based parser for packet generation
func (c *Converter) convertSendPacketsWithASTParser(content interface{}, testPath string, testName string, stripVLAN bool) (ConvertedStep, error) {
	packets, ok := content.([]interface{})
	if !ok {
		return ConvertedStep{}, fmt.Errorf("invalid sendPackets format")
	}

	var functions []string
	var packetTests []PacketTestCase
	step := ConvertedStep{
		Type:        "sendPackets",
		Description: "Packet sending and validation",
	}

	// Parse gen.py with Python AST parser (with timeout)
	genPyPath := filepath.Join(testPath, "gen.py")

	// Validate gen.py file exists and check size
	genPyInfo, err := os.Stat(genPyPath)
	if err != nil {
		return ConvertedStep{}, fmt.Errorf("gen.py file not found at %s: %w", genPyPath, err)
	}
	// Check file size (limit to 10MB to prevent DoS)
	const maxFileSize = 10 * 1024 * 1024
	if genPyInfo.Size() > maxFileSize {
		return ConvertedStep{}, fmt.Errorf("gen.py file too large: %d bytes (max %d bytes)", genPyInfo.Size(), maxFileSize)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ASTParserTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", c.scapyASTParser, genPyPath)
	irJSON, err := cmd.CombinedOutput()
	if err != nil {
		// Provide more context about the failure
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ConvertedStep{}, fmt.Errorf("AST parser failed (exit code %d) for %s: %w\nOutput: %s",
				exitErr.ExitCode(), genPyPath, err, string(irJSON))
		}
		if ctx.Err() == context.DeadlineExceeded {
			return ConvertedStep{}, fmt.Errorf("AST parser timeout after 30s for %s: %w", genPyPath, err)
		}
		return ConvertedStep{}, fmt.Errorf("AST parser failed for %s: %w\nOutput: %s", genPyPath, err, string(irJSON))
	}

	c.debugLog("AST parser generated %d bytes of IR", len(irJSON))

	// For each packet in autotest.yaml sendPackets
	for i, packet := range packets {
		c.debugLog("Processing AST packet entry %d, type=%T", i, packet)

		sendFile, expectFile := c.parseSendExpectFiles(packet)
		if sendFile == "" {
			c.debugLog("AST packet entry %d has no send file, skipping", i)
			continue
		}

		c.debugLog("AST: Send file: %s, Expect file: %s", sendFile, expectFile)

		// Generate packet creation functions from IR
		c.packetCounter++
		funcName := fmt.Sprintf("create%sSendPacket%d", testName, c.packetCounter)

		funcCode, err := c.generatePacketFunctionFromIR(string(irJSON), sendFile, funcName, false, stripVLAN)
		if err != nil {
			return ConvertedStep{}, fmt.Errorf("failed to generate code for %s: %w", sendFile, err)
		}

		functions = append(functions, funcCode)

		// Handle expect packets
		var expectFuncName string
		isDropExpected := expectFile == ""

		if !isDropExpected {
			c.packetCounter++
			expectFuncName = fmt.Sprintf("create%sExpectPacket%d", testName, c.packetCounter)

			expectCode, err := c.generatePacketFunctionFromIR(string(irJSON), expectFile, expectFuncName, true, stripVLAN)
			if err != nil {
				return ConvertedStep{}, fmt.Errorf("failed to generate expect code for %s: %w", expectFile, err)
			}

			functions = append(functions, expectCode)
		}

		// Create test case
		testCase := PacketTestCase{
			SendPcap:           sendFile,
			ExpectPcap:         expectFile,
			IsDropExpected:     isDropExpected,
			FunctionName:       funcName,
			PacketNumber:       c.packetCounter,
			ExpectFunctionName: expectFuncName,
		}
		packetTests = append(packetTests, testCase)
	}

	step.Functions = functions
	step.PacketTests = packetTests
	return step, nil
}

// generatePacketFunctionFromIR extracts packets for a specific PCAP file from IR JSON
// and generates a Go function that creates those packets using the packet builder library.
//
// Parameters:
//   - irJSON: Complete IR JSON string from Python AST parser
//   - pcapFilename: Name of the PCAP file to extract packets for
//   - funcName: Name for the generated Go function
//   - isExpect: If true, generates expect packet function (with MAC swap)
//   - stripVLAN: If true, removes VLAN layers from generated code
//
// Returns:
//   - string: Generated Go function code
//   - error: Error if IR parsing fails or no packets found for the PCAP file
func (c *Converter) generatePacketFunctionFromIR(irJSON, pcapFilename, funcName string, isExpect bool, stripVLAN bool) (string, error) {
	// Parse IR to find packets for this specific PCAP file
	var ir struct {
		PCAPPairs []struct {
			SendFile      string        `json:"send_file"`
			ExpectFile    string        `json:"expect_file"`
			SendPackets   []IRPacketDef `json:"send_packets"`
			ExpectPackets []IRPacketDef `json:"expect_packets"`
		} `json:"pcap_pairs"`
	}

	if err := json.Unmarshal([]byte(irJSON), &ir); err != nil {
		return "", fmt.Errorf("failed to parse IR: %w", err)
	}

	// Find matching PCAP pair
	var packets []IRPacketDef
	for _, pair := range ir.PCAPPairs {
		if isExpect && pair.ExpectFile == pcapFilename {
			packets = pair.ExpectPackets
			break
		} else if !isExpect && pair.SendFile == pcapFilename {
			packets = pair.SendPackets
			break
		}
	}

	if len(packets) == 0 {
		return "", fmt.Errorf("no packets found for %s", pcapFilename)
	}

	// Generate function using ScapyCodegenV2
	codegen := NewScapyCodegenV2(stripVLAN)
	// Pass tracking maps to codegen if available
	if c.specialHandlingSkips != nil || c.unsupportedLayerTypes != nil {
		codegen.SetTrackingMaps(&c.specialHandlingSkips, &c.unsupportedLayerTypes)
	}
	codegen.SetStrictMode(c.config.StrictMode)

	return codegen.GeneratePacketFunction(funcName, packets, isExpect), nil
}

// convertSendPacketsWithOptions is like convertSendPackets but supports stripping VLAN at codegen time
func (c *Converter) convertSendPacketsWithOptions(content interface{}, testPath string, stripVLAN bool, testName string) ConvertedStep {
	c.debugLog("convertSendPacketsWithOptions: testPath=%s, stripVLAN=%v", testPath, stripVLAN)

	// Try AST parser first
	if c.shouldUseASTParser(testPath) {
		c.debugLog("Using AST parser for %s", testName)
		step, err := c.convertSendPacketsWithASTParser(content, testPath, testName, stripVLAN)
		if err == nil {
			c.debugLog("AST parser succeeded, returning step with %d packet tests", len(step.PacketTests))
			return step
		}

		// If ForceASTParser is set, fail instead of falling back
		if c.config.ForceASTParser {
			c.debugLog("AST parser failed for %s with ForceASTParser set: %v - NOT falling back", testName, err)
			return NewSkipStep("sendPackets", fmt.Sprintf("AST parser failed: %v", err))
		}

		c.debugLog("AST parser failed, using PCAP fallback: %v", err)
	} else {
		c.debugLog("AST parser not available, using PCAP fallback")
	}

	// Fallback to PCAP analysis
	c.debugLog("Using PCAP fallback for %s", testName)
	return c.convertSendPacketsWithOptionsLegacy(content, testPath, stripVLAN, testName)
}

// parseSendExpectFiles extracts send and expect file names from a packet entry
func (c *Converter) parseSendExpectFiles(packet interface{}) (sendFile, expectFile string) {
	if v, ok := GetStringFromAnyMap(packet, "send"); ok {
		sendFile = v
	}
	if v, ok := GetStringFromAnyMap(packet, "expect"); ok {
		expectFile = v
	}
	return sendFile, expectFile
}

// convertSendPacketsWithOptionsLegacy is the original PCAP-based converter
func (c *Converter) convertSendPacketsWithOptionsLegacy(content interface{}, testPath string, stripVLAN bool, testName string) ConvertedStep {
	c.debugLog("convertSendPacketsWithOptionsLegacy: content type=%T", content)
	packets, ok := content.([]interface{})
	if !ok {
		c.debugLog("Invalid sendPackets format: expected []interface{}, got %T", content)
		return NewSkipStep("sendPackets", fmt.Sprintf("Invalid sendPackets format: expected []interface{}, got %T", content))
	}
	c.debugLog("Found %d packet entries", len(packets))

	var functions []string
	var packetTests []PacketTestCase
	step := ConvertedStep{
		Type:        "sendPackets",
		Description: "Packet sending and validation",
	}

	for i, packet := range packets {
		c.debugLog("Processing packet entry %d, type=%T", i, packet)

		sendFile, expectFile := c.parseSendExpectFiles(packet)
		if sendFile == "" {
			c.debugLog("Packet entry %d has no send file, skipping", i)
			continue
		}

		c.debugLog("Send file: %s, Expect file: %s", sendFile, expectFile)

		// Read all send packets
		c.debugLog("Analyzing send pcap: %s", sendFile)
		sendPcapPath := filepath.Join(testPath, sendFile)
		sendPackets, err := c.pcapAnalyzer.ReadAllPacketsFromFile(sendPcapPath)
		if err != nil {
			c.verbose("Warning: failed to analyze pcap file %s: %v", sendFile, err)
			continue
		}
		if len(sendPackets) == 0 {
			c.debugLog("No packets found in %s", sendFile)
			continue
		}
		c.debugLog("Send packets count: %d", len(sendPackets))

		// Generate packet creation function (returns slice)
		c.packetCounter++
		funcName := fmt.Sprintf("create%sSendPacket%d", testName, c.packetCounter)
		tcpdumpComment, err := c.pcapAnalyzer.GenerateTcpdumpComment(sendPcapPath, sendPackets)
		if err != nil {
			c.debugLog("tcpdump failed for %s: %v", sendFile, err)
			tcpdumpComment = fmt.Sprintf("// tcpdump error: %v\n", err)
		}
		funcCode := tcpdumpComment + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(sendPackets, funcName, CodegenOpts{StripVLAN: stripVLAN, UseFrameworkMACs: true})
		functions = append(functions, funcCode)

		// Read expected packets
		var expectPackets []*PacketInfo
		isDropExpected := false
		expectPcapPath := filepath.Join(testPath, expectFile)

		fileInfo, err := os.Stat(expectPcapPath)
		if err == nil {
			expectPackets, err = c.pcapAnalyzer.ReadAllPacketsFromFile(expectPcapPath)
			if err != nil {
				if fileInfo.Size() <= 24 {
					isDropExpected = true
					c.verbose("Detected empty expect file %s - packet should be dropped", expectFile)
				} else {
					c.verbose("Warning: failed to analyze expect file %s: %v", expectFile, err)
				}
			} else {
				c.debugLog("Expect packets count: %d", len(expectPackets))
			}
		}

		// Generate expect packet function if applicable
		expectFuncName := ""
		if !isDropExpected && len(expectPackets) > 0 {
			expectFuncName = fmt.Sprintf("create%sExpectPacket%d", testName, c.packetCounter)
			tcpdumpExpect, err := c.pcapAnalyzer.GenerateTcpdumpComment(expectPcapPath, expectPackets)
			if err != nil {
				c.debugLog("tcpdump failed for expect %s: %v", expectFile, err)
				tcpdumpExpect = fmt.Sprintf("// tcpdump error: %v\n", err)
			}
			expectFuncCode := tcpdumpExpect + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(expectPackets, expectFuncName, CodegenOpts{StripVLAN: stripVLAN, IsExpect: true, UseFrameworkMACs: true})
			functions = append(functions, expectFuncCode)
		}

		packetTests = append(packetTests, PacketTestCase{
			SendPcap:           sendFile,
			ExpectPcap:         expectFile,
			SendPackets:        sendPackets,
			ExpectPackets:      expectPackets,
			IsDropExpected:     isDropExpected,
			FunctionName:       funcName,
			PacketNumber:       c.packetCounter,
			ExpectFunctionName: expectFuncName,
		})
	}

	step.Functions = functions
	step.PacketTests = packetTests
	return step
}

// convertCheckCounters converts checkCounters step
func (c *Converter) convertCheckCounters(content interface{}) ConvertedStep {
	// Parse counter validation content with proper error handling
	contentMap, ok := content.(map[interface{}]interface{})
	if !ok {
		return ConvertedStep{
			Type:        "skip",
			GoCode:      "// Skipping checkCounters: invalid content format\n",
			Description: "Check counters (skipped - invalid format)",
		}
	}

	var counterValidations []string
	var counterNames []string

	// Extract counters from the content
	for key, value := range contentMap {
		// Convert key to string (counter name/flow id)
		var counterName string
		switch k := key.(type) {
		case string:
			counterName = k
		case int:
			counterName = fmt.Sprintf("flow_%d", k)
		case int64:
			counterName = fmt.Sprintf("flow_%d", k)
		default:
			continue // Skip unsupported key types
		}

		// Convert value to expected count
		var expectedValue int
		switch v := value.(type) {
		case int:
			expectedValue = v
		case int64:
			expectedValue = int(v)
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				expectedValue = parsed
			} else {
				continue // Skip unparseable values
			}
		default:
			continue // Skip unsupported value types
		}

		// Generate validation code for this counter
		validation := fmt.Sprintf(
			"err = fw.ValidateCounter(%q, %d)",
			counterName,
			expectedValue,
		)
		counterValidations = append(counterValidations, validation)
		counterNames = append(counterNames, fmt.Sprintf("%s:%d", counterName, expectedValue))
	}

	// If no valid counters found, skip
	if len(counterValidations) == 0 {
		return ConvertedStep{
			Type:        "skip",
			GoCode:      "// Skipping checkCounters: no valid counters found\n",
			Description: "Check counters (skipped - no valid counters)",
		}
	}

	// Generate validation code with better error aggregation
	var goCode strings.Builder
	goCode.WriteString("// Validate counter values\n")
	goCode.WriteString("\t{\n")
	goCode.WriteString("\t\tvar counterErrors []string\n")

	for i, validation := range counterValidations {
		goCode.WriteString(fmt.Sprintf("\t\tif %s; err != nil {\n", validation))
		// Store fully formatted error message to avoid format string issues at runtime
		goCode.WriteString(fmt.Sprintf("\t\t\tcounterErrors = append(counterErrors, \"Counter %s: \"+err.Error())\n", counterNames[i]))
		goCode.WriteString("\t\t}\n")
	}

	goCode.WriteString("\t\tif len(counterErrors) > 0 {\n")
	goCode.WriteString("\t\t\tt.Errorf(\"Counter validation failed (%d/%d counters):\\n  - %%s\", len(counterErrors), ")
	goCode.WriteString(fmt.Sprintf("%d, strings.Join(counterErrors, \"\\n  - \"))\n", len(counterValidations)))
	goCode.WriteString("\t\t}\n")
	goCode.WriteString("\t}\n")

	return ConvertedStep{
		Type:        "checkCounters",
		GoCode:      goCode.String() + "\n",
		Description: fmt.Sprintf("Validate counters: %s", strings.Join(counterNames, ", ")),
	}
}

// convertCLI converts cli step
func (c *Converter) convertCLI(content interface{}) ConvertedStep {
	c.debugLog("Converting cli step")
	commands, ok := content.([]interface{})
	if !ok {
		return NewSkipStep("cli", "Invalid cli format")
	}

	var cliCommands []string
	var originalItems []string
	for _, cmd := range commands {
		cmdStr, ok := cmd.(string)
		if !ok {
			continue
		}
		// Convert yanet1 commands to yanet2 CLI
		convertedCmd := c.convertCLICommand(cmdStr)
		cliCommands = append(cliCommands, convertedCmd)
		originalItems = append(originalItems, cmdStr)
	}

	// Add original YAML comment
	yamlComment := c.generateYAMLComment("cli", originalItems)

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to execute CLI commands")`, yamlComment, strings.Join(cliCommands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "cli",
		GoCode:       goCode,
		Description:  "Execute CLI commands",
		OriginalYAML: yamlComment,
	}
}

// convertCLICheck converts cli_check step
func (c *Converter) convertCLICheck(content interface{}) ConvertedStep {
	c.debugLog("Converting cli_check step")

	checkContent, ok := content.(string)
	if !ok {
		return NewSkipStep("cli_check", "Invalid cli_check format")
	}

	parsed, _ := ParseCLICheckBlock(checkContent)
	var checkCommands []string
	for _, payload := range parsed.CommandPayloads {
		checkCommands = append(checkCommands, fmt.Sprintf(`"%s %s"`, framework.CLIGeneric, payload))
	}

	if len(checkCommands) == 0 {
		return NewSkipStep("cli_check", "cli_check has no commands to execute")
	}

	yamlComment := buildCLIStepComment("cli_check", parsed.OriginalLines)
	goCode := buildCLICommandBlock("cli_check", yamlComment, checkCommands, parsed.ExpectedLines, parsed.Regexes)

	return ConvertedStep{
		Type:         "cli_check",
		GoCode:       goCode,
		Description:  "Check CLI command output",
		OriginalYAML: yamlComment,
	}
}

// buildCLIStepComment generates a YAML comment block for CLI steps
func buildCLIStepComment(stepType string, originalLines []string) string {
	var comment strings.Builder
	comment.WriteString(fmt.Sprintf("// Original %s:\n", stepType))
	for _, line := range originalLines {
		comment.WriteString(fmt.Sprintf("// %s\n", line))
	}
	return comment.String()
}

// buildCLICommandBlock generates Go code for CLI command execution with validation
func buildCLICommandBlock(stepType, yamlComment string, commands, expectedLines, regexes []string) string {
	var code strings.Builder

	code.WriteString(yamlComment)
	code.WriteString(`{
	commands := []string{
`)
	for _, cmd := range commands {
		code.WriteString(fmt.Sprintf("\t\t%s,\n", cmd))
	}
	code.WriteString(`	}

	for _, cmd := range commands {
		output, err := fw.ExecuteCommand(cmd)
		if err != nil {
			t.Fatalf("CLI command failed: %v", err)
		}

`)

	if len(expectedLines) > 0 {
		code.WriteString(`
		// Check expected output
		expectedOutput := []string{
`)
		for _, line := range expectedLines {
			// Escape quotes in expected output
			escapedLine := strings.ReplaceAll(line, `"`, `\"`)
			code.WriteString(fmt.Sprintf("\t\t\t\"%s\",\n", escapedLine))
		}
		code.WriteString(`		}
		var missingLines []string
		for _, expected := range expectedOutput {
			if !strings.Contains(output, expected) {
				missingLines = append(missingLines, expected)
			}
		}
		if len(missingLines) > 0 {
			// Show first 3 missing lines and output preview
			preview := strings.Join(missingLines[:min(3, len(missingLines))], "\n  - ")
			if len(missingLines) > 3 {
				preview += fmt.Sprintf("\n  ... and %d more", len(missingLines)-3)
			}
			outputPreview := output
			if len(outputPreview) > 500 {
				outputPreview = outputPreview[:500] + "... (truncated)"
			}
			t.Errorf("Expected output not found (%d lines missing):\n  - %s\n\nActual output:\n%s", 
				len(missingLines), preview, outputPreview)
		}

`)
	}

	if len(regexes) > 0 {
		code.WriteString(`
		// Check regex patterns
		regexPatterns := []string{
`)
		for _, regex := range regexes {
			// Escape quotes and backslashes in regex
			escapedRegex := strings.ReplaceAll(regex, `\`, `\\`)
			escapedRegex = strings.ReplaceAll(escapedRegex, `"`, `\"`)
			code.WriteString(fmt.Sprintf("\t\t\t\"%s\",\n", escapedRegex))
		}
		code.WriteString(`		}
		for _, pattern := range regexPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				t.Fatalf("Invalid regex pattern: %s (error: %v)", pattern, err)
			}
			if !re.MatchString(output) {
				// Show first 500 chars of output for context
				outputPreview := output
				if len(outputPreview) > 500 {
					outputPreview = outputPreview[:500] + "... (truncated)"
				}
				t.Errorf("Regex pattern not matched: %s\nOutput preview:\n%s", pattern, outputPreview)
			}
		}
`)
	}

	code.WriteString(`	}
}
`)

	return code.String()
}

// adaptIPAddress adapts a single IP address to yanet2 infrastructure.
// This method delegates to the unified AdaptIPAddress function.
func (c *Converter) adaptIPAddress(ipAddr string) string {
	return AdaptIPAddress(ipAddr)
}

// convertCLICommand converts yanet1 command to yanet2 CLI command using CLI parsing utilities
func (c *Converter) convertCLICommand(cmd string) string {
	c.debugLog("Converting CLI command: %s", cmd)

	// Parse the command using new utilities
	parsedCmd, err := ParseCLICommand(cmd)
	if err != nil {
		c.debugLog("Failed to parse CLI command: %v", err)
		return fmt.Sprintf(`"# ERROR: Could not parse command: %s"`, cmd)
	}

	// Validate the command structure
	if err := ValidateCLICommand(parsedCmd); err != nil {
		c.debugLog("CLI command validation failed: %v", err)
	}

	// Convert based on command type
	switch parsedCmd.Command {
	case "balancer":
		return c.convertBalancerCommand(parsedCmd)
	case "nat64":
		return c.convertNat64Command(parsedCmd)
	case "route":
		return c.convertRouteCommand(parsedCmd)
	default:
		c.debugLog("Unsupported command type: %s", parsedCmd.Command)
		return fmt.Sprintf(`"# Unsupported command: %s"`, parsedCmd.Raw)
	}
}

// convertSleep converts sleep step
func (c *Converter) convertSleep(content interface{}) ConvertedStep {
	c.debugLog("Converting sleep step: %v seconds", content)
	seconds, ok := content.(int)
	if !ok {
		return NewSkipStep("sleep", "Invalid sleep format")
	}

	goCode := fmt.Sprintf("// Original autotest.yaml step:\n// sleep: %d\n"+"time.Sleep(%d * time.Second)", seconds, seconds)

	return ConvertedStep{
		Type:         "sleep",
		GoCode:       goCode,
		Description:  fmt.Sprintf("Wait %d seconds", seconds),
		OriginalYAML: fmt.Sprintf("# Original autotest.yaml step:\n# sleep: %d\n", seconds),
	}
}

// analyzePcapFiles analyzes pcap files in test directory
func (c *Converter) analyzePcapFiles(testPath string) ([]PcapFileInfo, error) {
	var pcapFiles []PcapFileInfo

	files, err := os.ReadDir(testPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range files {
		fileName := entry.Name()
		if strings.HasSuffix(fileName, ".pcap") {
			pcapType := "unknown"
			if strings.Contains(fileName, "send") {
				pcapType = "send"
			} else if strings.Contains(fileName, "expect") {
				pcapType = "expect"
			}

			pcapPath := filepath.Join(testPath, fileName)
			packetInfo, err := c.pcapAnalyzer.AnalyzePcapFile(pcapPath)
			if err != nil {
				c.verbose("Warning: failed to analyze pcap file %s: %v", fileName, err)
				packetInfo = nil
			}

			pcapFiles = append(pcapFiles, PcapFileInfo{
				Name:        fileName,
				Path:        pcapPath,
				Type:        pcapType,
				Description: fmt.Sprintf("PCAP file: %s", fileName),
				PacketInfo:  packetInfo,
			})
		}
	}

	return pcapFiles, nil
}
