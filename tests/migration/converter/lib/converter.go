package lib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// Config contains the converter configuration
type Config struct {
	InputDir       string
	OutputDir      string
	Verbose        bool
	Debug          bool // Enable debug logging for conversions
	SkiplistPath   string
	ForceASTParser bool // Force use of AST parser (fail if unavailable)
	ForceLegacy    bool // Force use of legacy PCAP analyzer
}

// ConversionStats contains conversion statistics
type ConversionStats struct {
	TotalTests         int
	SuccessTests       int
	FailedTests        int
	SkippedTests       int
	TestsByType        map[string]int
	FailedTestsDetails []FailedTest
}

// FailedTest contains information about failed conversion
type FailedTest struct {
	Name  string
	Error string
}

// Print outputs statistics to console
func (s *ConversionStats) Print() {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("TEST CONVERSION STATISTICS")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total tests:        %d\n", s.TotalTests)

	successPct := 0.0
	failedPct := 0.0
	if s.TotalTests > 0 {
		successPct = float64(s.SuccessTests) / float64(s.TotalTests) * 100
		failedPct = float64(s.FailedTests) / float64(s.TotalTests) * 100
	}

	fmt.Printf("Successful:         %d (%.1f%%)\n", s.SuccessTests, successPct)
	fmt.Printf("Errors:             %d (%.1f%%)\n", s.FailedTests, failedPct)
	fmt.Printf("Skipped:            %d\n", s.SkippedTests)

	if len(s.TestsByType) > 0 {
		fmt.Println("\nTests by type:")
		for testType, count := range s.TestsByType {
			fmt.Printf("  %-15s: %d\n", testType, count)
		}
	}

	if len(s.FailedTestsDetails) > 0 {
		fmt.Println("\nFailed tests:")
		for _, failed := range s.FailedTestsDetails {
			fmt.Printf("  ❌ %s: %s\n", failed.Name, failed.Error)
		}
	}
	fmt.Println(strings.Repeat("=", 60))
}

// SaveToFile saves statistics to markdown file
func (s *ConversionStats) SaveToFile(filename string) error {
	var content strings.Builder

	content.WriteString("# Test conversion statistics yanet1 → yanet2\n\n")
	content.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	content.WriteString(fmt.Sprintf(`## General statistics

- **Total tests**: %d
`, s.TotalTests))

	successPct := 0.0
	failedPct := 0.0
	if s.TotalTests > 0 {
		successPct = float64(s.SuccessTests) / float64(s.TotalTests) * 100
		failedPct = float64(s.FailedTests) / float64(s.TotalTests) * 100
	}

	content.WriteString(fmt.Sprintf(`- **Successful**: %d (%.1f%%)
- **Errors**: %d (%.1f%%)
- **Skipped**: %d

`, s.SuccessTests, successPct, s.FailedTests, failedPct, s.SkippedTests))

	if len(s.TestsByType) > 0 {
		content.WriteString("## Tests by type\n\n")
		content.WriteString("| Type | Count |\n")
		content.WriteString("|------|-------|\n")
		for testType, count := range s.TestsByType {
			content.WriteString(fmt.Sprintf("| %s | %d |\n", testType, count))
		}
		content.WriteString("\n")
	}

	if len(s.FailedTestsDetails) > 0 {
		content.WriteString("## Failed tests\n\n")
		for _, failed := range s.FailedTestsDetails {
			content.WriteString(fmt.Sprintf("### %s\n\n", failed.Name))
			content.WriteString(fmt.Sprintf("```\n%s\n```\n\n", failed.Error))
		}
	}

	return os.WriteFile(filename, []byte(content.String()), 0644)
}

// findScapyASTParser locates scapy_ast_parser.py relative to converter
func findScapyASTParser() string {
	// Get executable directory
	ex, err := os.Executable()
	if err != nil {
		return ""
	}
	exDir := filepath.Dir(ex)

	candidates := []string{
		filepath.Join(exDir, "scapy_ast_parser.py"),       // Same dir as executable
		filepath.Join(exDir, "..", "scapy_ast_parser.py"), // One level up
		"./scapy_ast_parser.py",                           // Current working dir
		"scapy_ast_parser.py",                             // Current working dir
	}

	for _, path := range candidates {
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absPath); err == nil {
			return absPath
		}
	}

	return "" // Will fallback to PCAP analysis
}

// Converter performs conversion of yanet1 tests to yanet2
type Converter struct {
	config           *Config
	pcapAnalyzer     *PcapAnalyzer
	scapyASTParser   string          // Path to scapy_ast_parser.py
	scapyCodegenV2   *ScapyCodegenV2 // New code generator
	packetCounter    int             // Global counter for unique packet function names
	stepCounter      int             // Counter for unique step names
	skiplist         map[string]SkiplistEntry
	defaultStripVLAN bool
	moduleInventory  moduleInventory
}

type moduleInventory struct {
	nat64Module     string
	balancerModules map[string]struct{}
	defaultBalancer string
}

type ModuleNames struct {
	NAT64    string
	Balancer string
}

// StepState defines skiplist state per test/step
type StepState string

const (
	StateEnabled  StepState = "enabled"
	StateWoVLAN   StepState = "wovlan" // Strip VLAN headers from packets
	StateDisabled StepState = "disabled"
)

// SkiplistEntry describes per-test skip configuration
// state: enabled (normal), wovlan (strip VLAN layers), disabled (skip test)
// steps map overrides state per step (1-based index)
type SkiplistEntry struct {
	State StepState         `yaml:"state"`
	Steps map[int]StepState `yaml:"steps"`
}

// NewConverter creates a new converter instance
func NewConverter(config *Config) *Converter {
	// Initialize new AST-based system
	scapyASTParser := findScapyASTParser()

	c := &Converter{
		config:         config,
		pcapAnalyzer:   NewPcapAnalyzer(config.Verbose),
		scapyASTParser: scapyASTParser,
		scapyCodegenV2: NewScapyCodegenV2(false), // false = keep VLAN by default
		skiplist:       make(map[string]SkiplistEntry),
		moduleInventory: moduleInventory{
			balancerModules: make(map[string]struct{}),
		},
	}
	c.loadSkiplist()
	return c
}

// ErrTestSkipped is returned when a test is disabled by skiplist
var ErrTestSkipped = errors.New("test skipped by skiplist")

// debugLog outputs debug information if debug mode is enabled
func (c *Converter) debugLog(format string, args ...interface{}) {
	if c.config.Debug {
		fmt.Printf("[DEBUG] "+format+"\n", args...)
	}
}

// shouldUseASTParser determines if test should use new AST parser
func (c *Converter) shouldUseASTParser(testPath string) bool {
	// Force legacy if requested
	if c.config.ForceLegacy {
		c.debugLog("ForceLegacy is set, using legacy parser")
		return false
	}

	// Force AST parser if requested (will fail in AST method if unavailable)
	if c.config.ForceASTParser {
		c.debugLog("ForceASTParser is set, forcing AST parser")
		return true
	}

	// Check if gen.py exists
	genPyPath := filepath.Join(testPath, "gen.py")
	if _, err := os.Stat(genPyPath); err != nil {
		return false
	}

	// Check if AST parser is available
	if c.scapyASTParser == "" {
		return false
	}

	return true
}

// loadSkiplist loads skiplist YAML if provided; missing file is tolerated
func (c *Converter) loadSkiplist() {
	path := strings.TrimSpace(c.config.SkiplistPath)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		c.debugLog("Skiplist not loaded: %v", err)
		return
	}
	var m map[string]SkiplistEntry
	if err := yaml.Unmarshal(data, &m); err != nil {
		c.debugLog("Skiplist parse error: %v", err)
		return
	}
	c.skiplist = m
	c.debugLog("Loaded skiplist with %d entries", len(m))
}

// effectiveState returns the effective state for a step (1-based index).
// It checks step-level overrides first, then test-level state, then global default.
// This implements the skiplist precedence: steps[N] > test-level > global "*"
//
// Parameters:
//   - test: Test name (directory name from yanet1)
//   - stepIndex: 1-based step index (0 for test-level check)
//
// Returns:
//   - StepState: Effective state (enabled, wovlan, or disabled)
func (c *Converter) effectiveState(test string, stepIndex int) StepState {
	if e, ok := c.skiplist[test]; ok {
		if e.Steps != nil {
			if s, ok2 := e.Steps[stepIndex]; ok2 {
				return s
			}
		}
		if e.State != "" {
			return e.State
		}
	}
	// Global default via special key "*"
	if e, ok := c.skiplist["*"]; ok {
		if e.Steps != nil && stepIndex > 0 {
			if s, ok2 := e.Steps[stepIndex]; ok2 {
				return s
			}
		}
		if e.State != "" {
			return e.State
		}
	}
	return StateEnabled
}

// ---- Skiplist in-place update support ----

const autoGeneratedMarker = "\n# ---- Auto-generated entries below (do not edit) ----\n"

// UpdateSkiplist scans yanet1 one-port tests, preserves existing entries, and
// updates skiplist.yaml in-place at the auto-generated marker with disabled steps.
func (c *Converter) UpdateSkiplist() error {
	if strings.TrimSpace(c.config.SkiplistPath) == "" {
		return fmt.Errorf("skiplist path is empty")
	}
	if strings.TrimSpace(c.config.InputDir) == "" {
		return fmt.Errorf("input dir is empty")
	}

	// Determine which tests are explicitly listed ABOVE the marker; only those are preserved as-is
	explicit, err := parseTopLevelKeysBeforeMarker(c.config.SkiplistPath)
	if err != nil {
		return fmt.Errorf("failed to parse skiplist top-level keys: %w", err)
	}

	onePortDir := filepath.Join(c.config.InputDir, "001_one_port")
	fi, err := os.Stat(onePortDir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("one-port directory not found: %s", onePortDir)
	}

	// Enumerate immediate subdirectories (tests)
	entries, err := os.ReadDir(onePortDir)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", onePortDir, err)
	}

	type step struct{ original []string }
	type testInfo struct{ steps []step }

	tests := make(map[string]testInfo)
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		if name == "*" {
			continue
		}
		if _, exists := explicit[name]; exists {
			// Already explicitly controlled in skiplist; do not auto-generate
			continue
		}
		autotestPath := filepath.Join(onePortDir, name, "autotest.yaml")
		if _, err := os.Stat(autotestPath); err != nil {
			continue
		}
		blocks := parseAutotestOriginalBlocks(autotestPath)
		if len(blocks) == 0 {
			continue
		}
		t := testInfo{steps: make([]step, 0, len(blocks))}
		for _, b := range blocks {
			t.steps = append(t.steps, step{original: b})
		}
		tests[name] = t
	}

	// Read skiplist and split at marker
	contentBytes, err := os.ReadFile(c.config.SkiplistPath)
	if err != nil {
		return fmt.Errorf("failed to read skiplist: %w", err)
	}
	content := string(contentBytes)
	var prefix string
	if strings.Contains(content, autoGeneratedMarker) {
		prefix = strings.Split(content, autoGeneratedMarker)[0]
		prefix = strings.TrimRight(prefix, "\n") + "\n"
	} else {
		prefix = strings.TrimRight(content, "\n") + "\n"
	}

	var sb strings.Builder
	sb.WriteString("# ---- Auto-generated entries below (do not edit) ----\n\n")
	// Deterministic order
	var names []string
	for name := range tests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sb.WriteString(fmt.Sprintf(`%s:
  state: disabled
  steps:
`, name))
		for i, st := range tests[name].steps {
			// Emit original step YAML as comments, indented with 4 spaces
			for _, ln := range st.original {
				sb.WriteString("    # ")
				sb.WriteString(ln)
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("    %d: disabled\n", i+1))
		}
		sb.WriteString("\n")
	}

	newContent := prefix + sb.String()
	if err := os.WriteFile(c.config.SkiplistPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write skiplist: %w", err)
	}
	return nil
}

// parseAutotestOriginalBlocks returns a slice of original YAML blocks for each
// step from a yanet1 autotest.yaml. It does a lightweight parse based on known structure.
func parseAutotestOriginalBlocks(path string) [][]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var blocks [][]string
	inSteps := false
	i := 0
	for i < len(lines) {
		line := lines[i]
		if !inSteps {
			if stepsHeaderRe.MatchString(line) {
				inSteps = true
			}
			i++
			continue
		}
		m := stepStartRe.FindStringSubmatch(line)
		if m == nil {
			// end if we see a new top-level mapping key
			if (line == strings.TrimLeft(line, " \t")) && topLevelMapLineRe.MatchString(line) && !strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ") {
				break
			}
			i++
			continue
		}
		baseIndent := len(m[1])
		var block []string
		block = append(block, line)
		i++
		for i < len(lines) {
			ln := lines[i]
			if strings.TrimSpace(ln) == "" {
				block = append(block, ln)
				i++
				continue
			}
			// next step or dedent
			if nextStepOrDedent(baseIndent).MatchString(ln) || (len(ln)-len(strings.TrimLeft(ln, " \t")) <= baseIndent-1) {
				break
			}
			block = append(block, ln)
			i++
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// parseTopLevelKeysBeforeMarker returns set of top-level YAML keys that appear
// before the auto-generated marker in skiplist.yaml.
func parseTopLevelKeysBeforeMarker(skiplistPath string) (map[string]struct{}, error) {
	data, err := os.ReadFile(skiplistPath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	before := content
	if strings.Contains(content, autoGeneratedMarker) {
		before = strings.Split(content, autoGeneratedMarker)[0]
	}
	result := make(map[string]struct{})
	lines := strings.Split(strings.ReplaceAll(before, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		ln := raw
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if ln != strings.TrimLeft(ln, " \t") {
			continue // not top-level
		}
		// match unquoted or quoted key ending with colon
		if m := topLevelKeyRe.FindStringSubmatch(ln); m != nil {
			result[m[1]] = struct{}{}
			continue
		}
		if m := quotedTopLevelKeyRe.FindStringSubmatch(ln); m != nil {
			result[m[1]] = struct{}{}
			continue
		}
	}
	return result, nil
}

// Precompiled regexes used by lightweight YAML parsing
var (
	stepsHeaderRe       = regexp.MustCompile(`^\s*steps:\s*$`)
	stepStartRe         = regexp.MustCompile(`^(\s*)-\s*([A-Za-z0-9_]+):\s*$`)
	topLevelMapLineRe   = regexp.MustCompile(`^[A-Za-z0-9_\"].*:\s*$`)
	topLevelKeyRe       = regexp.MustCompile(`^([A-Za-z0-9_\-]+):\s*$`)
	quotedTopLevelKeyRe = regexp.MustCompile(`^"([^"]+)":\s*$`)
)

func nextStepOrDedent(baseIndent int) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`^\s{0,%d}-\s+[A-Za-z0-9_]+:\s*$`, baseIndent))
}

// getAddressMappings is deprecated. Use AdaptIPAddress instead.
// This method is kept for backward compatibility but delegates to AdaptIPAddress.
func (c *Converter) getAddressMappings() map[string]string {
	// Return a copy of the mappings for backward compatibility
	// Note: This creates a new map each time, but it's only used for compatibility
	mappings := make(map[string]string)
	for k, v := range addressMappings {
		mappings[k] = v
	}
	return mappings
}

// YanetTest represents the structure of a yanet1 test
type YanetTest struct {
	Steps []map[string]interface{} `yaml:"steps"`
}

// TestStep represents one test step
type TestStep struct {
	Type    string
	Content interface{}
}

// ConvertAllTests converts all tests in the specified directory
func (c *Converter) ConvertAllTests() error {
	root := c.config.InputDir
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() || path == root || filepath.Base(path) == "001_one_port" {
			return nil
		}
		autotestPath := filepath.Join(path, "autotest.yaml")
		if _, statErr := os.Stat(autotestPath); statErr != nil {
			return filepath.SkipDir
		}
		testName := filepath.Base(path)
		if c.config.Verbose {
			fmt.Printf("Processing test: %s\n", testName)
		}
		if err := c.ConvertSingleTest(path, testName); err != nil {
			return err
		}
		return filepath.SkipDir
	})
}

// ConvertAllTestsWithStats converts all tests and collects statistics
func (c *Converter) ConvertAllTestsWithStats() (*ConversionStats, error) {
	stats := &ConversionStats{
		TestsByType: make(map[string]int),
	}

	err := filepath.Walk(c.config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Look for test directories (not the 001_one_port directory itself)
		if info.IsDir() && path != c.config.InputDir {
			relPath, _ := filepath.Rel(c.config.InputDir, path)
			// Skip the 001_one_port directory itself
			if relPath == "." || filepath.Base(path) == "001_one_port" {
				return nil
			}

			// Check if autotest.yaml exists
			autotestPath := filepath.Join(path, "autotest.yaml")
			if _, err := os.Stat(autotestPath); os.IsNotExist(err) {
				return filepath.SkipDir
			}

			testName := filepath.Base(path)
			stats.TotalTests++

			if c.config.Verbose {
				fmt.Printf("Converting test %d: %s\n", stats.TotalTests, testName)
			}

			// Convert the test
			err := c.ConvertSingleTest(path, testName)
			if err != nil {
				if errors.Is(err, ErrTestSkipped) {
					stats.SkippedTests++
					if c.config.Verbose {
						fmt.Printf("  ⏭️  Skipped by skiplist\n")
					}
				} else {
					stats.FailedTests++
					stats.FailedTestsDetails = append(stats.FailedTestsDetails, FailedTest{
						Name:  testName,
						Error: err.Error(),
					})
					if c.config.Verbose {
						fmt.Printf("  ❌ Error: %v\n", err)
					}
				}
			} else {
				stats.SuccessTests++
				// Determine test type from name
				testType := "unknown"
				if strings.Contains(testName, "nat64") {
					testType = "nat64"
				} else if strings.Contains(testName, "route") {
					testType = "route"
				} else if strings.Contains(testName, "firewall") || strings.Contains(testName, "acl") {
					testType = "firewall"
				} else if strings.Contains(testName, "balancer") {
					testType = "balancer"
				} else if strings.Contains(testName, "decap") {
					testType = "decap"
				}
				stats.TestsByType[testType]++
				if c.config.Verbose {
					fmt.Printf("  ✅ Successful\n")
				}
			}

			return filepath.SkipDir
		}

		return nil
	})

	return stats, err
}

// ConvertSingleTest converts a single yanet1 test to yanet2 Go test format.
// It reads autotest.yaml and controlplane.conf from the test directory,
// analyzes PCAP files, converts steps, and generates a Go test file.
//
// Parameters:
//   - testPath: Full path to the test directory containing autotest.yaml
//   - testName: Name of the test (used for output file naming and function names)
//
// Returns:
//   - error: Returns ErrTestSkipped if test is disabled by skiplist,
//     or ConversionError with context if conversion fails
//
// The conversion process includes:
//   - Skiplist checking (test and step level)
//   - YAML parsing (autotest.yaml, controlplane.conf)
//   - PCAP file analysis (send/expect packets)
//   - Step conversion (routes, CLI commands, packets)
//   - Go code generation with proper formatting
func (c *Converter) ConvertSingleTest(testPath, testName string) error {
	// Reset counters for each test to avoid leakage between tests
	c.packetCounter = 0
	c.stepCounter = 0
	c.defaultStripVLAN = false
	c.moduleInventory = moduleInventory{balancerModules: make(map[string]struct{})}

	// Check test-level skip state
	testState := c.effectiveState(testName, 0)
	c.debugLog("Test %s effective state: %s", testName, testState)

	if testState == StateDisabled {
		if c.config.Verbose {
			fmt.Printf("Skipping test %s due to skiplist: disabled\n", testName)
		}
		return ErrTestSkipped
	}
	if testState == StateWoVLAN {
		c.defaultStripVLAN = true
		c.debugLog("Test %s: StripVLAN enabled (wovlan)", testName)
	}

	c.debugLog("ConvertSingleTest started for: %s", testName)
	c.debugLog("Test path: %s", testPath)
	c.debugLog("Output directory: %s", c.config.OutputDir)

	// Read autotest.yaml directly from testPath (it should already contain the test directory)
	autotestPath := filepath.Join(testPath, "autotest.yaml")
	c.debugLog("Looking for autotest.yaml at: %s", autotestPath)

	if _, err := os.Stat(autotestPath); os.IsNotExist(err) {
		return NewConversionError(testName, "", fmt.Sprintf("autotest.yaml not found at %s", autotestPath))
	}

	yamlData, err := os.ReadFile(autotestPath)
	if err != nil {
		return NewConversionErrorWrap(testName, "", err, fmt.Sprintf("error reading %s", autotestPath))
	}

	var test YanetTest
	if err := yaml.Unmarshal(yamlData, &test); err != nil {
		return NewConversionErrorWrap(testName, "", err, fmt.Sprintf("error parsing YAML %s", autotestPath))
	}

	// Read and parse controlplane.conf if it exists
	controlplaneConfig := ""
	var parsedConfig *ControlplaneConfig
	controlplanePath := filepath.Join(testPath, "controlplane.conf")
	if _, err := os.Stat(controlplanePath); err == nil {
		configData, err := os.ReadFile(controlplanePath)
		if err == nil {
			controlplaneConfig = string(configData)
			// Parse the configuration
			parsedConfig, err = c.parseControlplaneConfig(controlplanePath)
			if err != nil && c.config.Verbose {
				fmt.Printf("Warning: failed to parse controlplane.conf: %v\n", err)
			}
		}
	}

	if parsedConfig != nil {
		c.moduleInventory = c.extractModuleInventory(parsedConfig)
	}

	// Analyze pcap files
	pcapFiles, err := c.analyzePcapFiles(testPath)
	if err != nil {
		return NewConversionErrorWrap(testName, "", err, "error analyzing pcap files")
	}

	// Determine test type based on steps and configuration
	testType := c.determineTestType(test.Steps, controlplaneConfig, parsedConfig)

	// Generate Go test
	goTest := &GoTestData{
		TestName:           c.sanitizeTestName(testName),
		OriginalTestName:   testName,
		TestType:           testType,
		Steps:              c.convertStepsWithSkip(testName, test.Steps, testPath),
		ControlplaneConfig: controlplaneConfig,
		ParsedConfig:       parsedConfig,
		PcapFiles:          pcapFiles,
	}

	return c.generateGoTest(goTest)
}

// determineTestType determines test type based on parsed config and steps
func (c *Converter) determineTestType(steps []map[string]interface{}, controlplaneConfig string, parsedConfig *ControlplaneConfig) string {
	// First priority: use parsed configuration modules if available
	// Check in priority order: nat64 > balancer > decap > acl
	if parsedConfig != nil && parsedConfig.Modules != nil {
		// First pass: high-priority modules
		for _, module := range parsedConfig.Modules {
			switch module.Type {
			case "nat64stateful", "nat64stateless":
				return "nat64"
			case "balancer":
				return "balancer"
			case "decap":
				return "decap"
			}
		}
		// Second pass: lower-priority modules
		for _, module := range parsedConfig.Modules {
			switch module.Type {
			case "acl", "firewall":
				return "acl"
			}
		}
	}

	// Fallback: check for module types in configuration string
	if strings.Contains(controlplaneConfig, "nat64") {
		return "nat64"
	}
	if strings.Contains(controlplaneConfig, "balancer") {
		return "balancer"
	}
	if strings.Contains(controlplaneConfig, "acl") || strings.Contains(controlplaneConfig, "firewall") {
		return "acl"
	}
	if strings.Contains(controlplaneConfig, "decap") {
		return "decap"
	}
	if strings.Contains(controlplaneConfig, "route") {
		return "route"
	}

	// Analyze steps to determine type
	for _, step := range steps {
		for stepType := range step {
			switch stepType {
			case "ipv4Update", "ipv6Update", "ipv4LabelledUpdate", "ipv4Remove", "ipv4LabelledRemove":
				return "route"
			case "cli":
				// Check CLI command content
				return "balancer" // Default for CLI commands
			}
		}
	}

	return "unknown"
}

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

// ControlplaneConfig represents yanet1 controlplane configuration
type ControlplaneConfig struct {
	Modules map[string]Module `json:"modules"`
}

// Module represents a module in yanet1 configuration
type Module struct {
	Type                    string                 `json:"type"`
	PhysicalPort            string                 `json:"physicalPort"`
	VlanId                  string                 `json:"vlanId"`
	MacAddress              string                 `json:"macAddress"`
	NextModule              string                 `json:"nextModule"`
	NextModules             []string               `json:"nextModules"`
	IPv6Prefixes            []string               `json:"ipv6_prefixes"`
	IPv4Prefixes            []string               `json:"ipv4_prefixes"`
	IPv6DestinationPrefixes []string               `json:"ipv6DestinationPrefixes"`
	IPv4DestinationPrefixes []string               `json:"ipv4DestinationPrefixes"`
	Translations            []NAT64Translation     `json:"translations"`
	Announces               []string               `json:"announces"`
	Interfaces              map[string]Interface   `json:"interfaces"`
	RawData                 map[string]interface{} `json:"-"` // For storing other fields
}

// NAT64Translation represents a NAT64 translation entry
type NAT64Translation struct {
	IPv6Address            string `json:"ipv6Address"`
	IPv6DestinationAddress string `json:"ipv6DestinationAddress"`
	IPv4Address            string `json:"ipv4Address"`
}

// Interface represents an interface in route module
type Interface struct {
	IPv6Prefix          string `json:"ipv6Prefix"`
	IPv4Prefix          string `json:"ipv4Prefix"`
	NeighborIPv6Address string `json:"neighborIPv6Address"`
	NeighborIPv4Address string `json:"neighborIPv4Address"`
	NeighborMacAddress  string `json:"neighborMacAddress"`
	NextModule          string `json:"nextModule"`
}

// parseControlplaneConfig parses yanet1 controlplane.conf file
func (c *Converter) parseControlplaneConfig(configPath string) (*ControlplaneConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read controlplane config: %w", err)
	}

	var config ControlplaneConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse controlplane config: %w", err)
	}

	// Validate required fields
	if config.Modules == nil {
		return nil, fmt.Errorf("controlplane config missing 'modules' field")
	}

	if len(config.Modules) == 0 {
		c.debugLog("Warning: controlplane config has empty modules map")
	}

	// Validate module structure
	for moduleName, module := range config.Modules {
		if module.Type == "" {
			return nil, fmt.Errorf("module %s has no type specified", moduleName)
		}
		c.debugLog("Found module: %s (type: %s)", moduleName, module.Type)
	}

	return &config, nil
}

// extractModuleInventory scans the parsed controlplane config and builds a module inventory
func (c *Converter) extractModuleInventory(config *ControlplaneConfig) moduleInventory {
	inv := moduleInventory{
		balancerModules: make(map[string]struct{}),
	}

	if config == nil || config.Modules == nil {
		return inv
	}

	for moduleName, module := range config.Modules {
		switch module.Type {
		case "nat64stateful":
			// Take the first NAT64 module found
			if inv.nat64Module == "" {
				inv.nat64Module = moduleName
			}
		case "balancer":
			inv.balancerModules[moduleName] = struct{}{}
			// Set first balancer as default
			if inv.defaultBalancer == "" {
				inv.defaultBalancer = moduleName
			}
		}
	}

	return inv
}

// validateModuleName checks if a module name exists in the inventory for the given type
func (c *Converter) validateModuleName(moduleName, moduleType string) error {
	switch moduleType {
	case "nat64":
		if c.moduleInventory.nat64Module == "" {
			return fmt.Errorf("NAT64 module not found in controlplane config")
		}
		if moduleName != "" && moduleName != c.moduleInventory.nat64Module {
			return fmt.Errorf("NAT64 module '%s' not found in config, available: '%s'",
				moduleName, c.moduleInventory.nat64Module)
		}
	case "balancer":
		if len(c.moduleInventory.balancerModules) == 0 {
			return fmt.Errorf("no balancer modules found in controlplane config")
		}
		if moduleName != "" {
			if _, exists := c.moduleInventory.balancerModules[moduleName]; !exists {
				var available []string
				for name := range c.moduleInventory.balancerModules {
					available = append(available, name)
				}
				return fmt.Errorf("balancer module '%s' not found in config, available: %v",
					moduleName, available)
			}
		}
	}
	return nil
}

// getDefaultModuleName returns the default module name for a given type
func (c *Converter) getDefaultModuleName(moduleType string) string {
	switch moduleType {
	case "nat64":
		return c.moduleInventory.nat64Module
	case "balancer":
		return c.moduleInventory.defaultBalancer
	}
	return ""
}

// generateForwardModuleCommands generates commands for forward module based on logicalPort
func (c *Converter) generateForwardModuleCommands(config *ControlplaneConfig) []string {
	// Forward module is already configured in framework.go, no additional commands needed
	return nil
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
		return c.convertIPv4Update(content, stepType)
	case "ipv6Update":
		return c.convertIPv6Update(content, stepType)
	case "ipv4LabelledUpdate":
		return c.convertIPv4LabelledUpdate(content, stepType)
	case "ipv4Remove":
		return c.convertIPv4Remove(content, stepType)
	case "ipv4LabelledRemove":
		return c.convertIPv4LabelledRemove(content, stepType)
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

// convertIPv4Update converts ipv4Update step
func (c *Converter) convertIPv4Update(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv4Update step")
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
		return NewSkipStep("ipv4Update", "Invalid ipv4Update format")
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	var commands []string
	for _, routeStr := range routeStrings {
		c.debugLog("  IPv4 route: %s", routeStr)
		// Parse "1.1.0.0/16 -> 200.0.0.1"
		parts := strings.Split(routeStr, " -> ")
		if len(parts) == 2 {
			prefix := strings.TrimSpace(parts[0])
			nexthop := strings.TrimSpace(parts[1])

			// Adapt nexthop IP address to yanet2 infrastructure
			adaptedNexthop := c.adaptIPAddress(nexthop)
			c.debugLog("  Adapted nexthop: %s -> %s", nexthop, adaptedNexthop)

			// yanet2 CLI format: insert --cfg <config> --instances <instances> --via <nexthop> <prefix>
			cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, framework.CLIRoute, adaptedNexthop, prefix)
			commands = append(commands, cmd)
			c.debugLog("  Generated route insert: %s", cmd)
		}
	}

	if len(commands) == 0 {
		return NewSkipStep("ipv4Update", "No valid IPv4 routes found")
	}

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to configure IPv4 routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "ipv4Update",
		GoCode:       goCode,
		Description:  "IPv4 routes configuration",
		OriginalYAML: yamlComment,
	}
}

// convertIPv6Update converts ipv6Update step
func (c *Converter) convertIPv6Update(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv6Update step")
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
		return NewSkipStep("ipv6Update", "Invalid ipv6Update format")
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	var commands []string
	for _, routeStr := range routeStrings {
		c.debugLog("  IPv6 route: %s", routeStr)
		// Parse "2000::/124 -> fe80::1"
		parts := strings.Split(routeStr, " -> ")
		if len(parts) == 2 {
			prefix := strings.TrimSpace(parts[0])
			nexthop := strings.TrimSpace(parts[1])

			// Adapt nexthop IP address to yanet2 infrastructure
			adaptedNexthop := c.adaptIPAddress(nexthop)
			c.debugLog("  Adapted nexthop: %s -> %s", nexthop, adaptedNexthop)

			cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, framework.CLIRoute, adaptedNexthop, prefix)
			commands = append(commands, cmd)
			c.debugLog("  Generated route insert: %s", cmd)
		}
	}

	if len(commands) == 0 {
		return NewSkipStep("ipv6Update", "No valid IPv6 routes found")
	}

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to configure IPv6 routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "ipv6Update",
		GoCode:       goCode,
		Description:  "IPv6 routes configuration",
		OriginalYAML: yamlComment,
	}
}

// convertIPv4LabelledUpdate converts ipv4LabelledUpdate step
func (c *Converter) convertIPv4LabelledUpdate(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv4LabelledUpdate step")
	routes, ok := content.([]interface{})
	if !ok {
		return NewSkipStep("ipv4LabelledUpdate", "Invalid ipv4LabelledUpdate format")
	}

	var routeStrings []string
	var commands []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)

		// Parse "200.1.1.1/32 -> 200.0.0.1:111"
		// In yanet2 there are no labeled routes, convert to regular routes without label
		parts := strings.Split(routeStr, " -> ")
		if len(parts) == 2 {
			prefix := strings.TrimSpace(parts[0])
			nexthopLabel := strings.TrimSpace(parts[1])
			labelParts := strings.Split(nexthopLabel, ":")
			if len(labelParts) >= 1 {
				nexthop := strings.TrimSpace(labelParts[0])

				// Adapt nexthop IP address to yanet2 infrastructure
				adaptedNexthop := c.adaptIPAddress(nexthop)
				c.debugLog("  Adapted nexthop: %s -> %s", nexthop, adaptedNexthop)

				// yanet2 doesn't support labels, use regular route insert
				cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, framework.CLIRoute, adaptedNexthop, prefix)
				commands = append(commands, cmd)
				c.debugLog("  Converted labeled route to regular route: %s (label ignored)", routeStr)
			}
		}
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to configure IPv4 labelled routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "ipv4LabelledUpdate",
		GoCode:       goCode,
		Description:  "IPv4 routes configuration with labels",
		OriginalYAML: yamlComment,
	}
}

// convertIPv4Remove converts ipv4Remove step
func (c *Converter) convertIPv4Remove(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv4Remove step")
	routes, ok := content.([]interface{})
	if !ok {
		return NewSkipStep("ipv4Remove", "Invalid ipv4Remove format")
	}

	var routeStrings []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)

		// yanet2 CLI route doesn't support remove command
		c.debugLog("  Route remove commented out: %s", routeStr)
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	goCode := fmt.Sprintf(`%s// Route removal is not supported in yanet2 CLI; skipping execution
    t.Logf("Skipping IPv4 route removals")`, yamlComment)

	return ConvertedStep{
		Type:         "ipv4Remove",
		GoCode:       goCode,
		Description:  "IPv4 routes removal",
		OriginalYAML: yamlComment,
	}
}

// convertIPv4LabelledRemove converts ipv4LabelledRemove step
func (c *Converter) convertIPv4LabelledRemove(content interface{}, stepType string) ConvertedStep {
	c.debugLog("Converting ipv4LabelledRemove step")
	routes, ok := content.([]interface{})
	if !ok {
		return NewSkipStep("ipv4LabelledRemove", "Invalid ipv4LabelledRemove format")
	}

	var routeStrings []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)

		// yanet2 CLI route doesn't support remove command
		c.debugLog("  Labeled route remove commented out: %s", routeStr)
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	goCode := fmt.Sprintf(`%s// Labeled route removal is not supported in yanet2 CLI; skipping execution
    t.Logf("Skipping IPv4 labeled route removals")`, yamlComment)

	return ConvertedStep{
		Type:         "ipv4LabelledRemove",
		GoCode:       goCode,
		Description:  "IPv4 routes removal with labels",
		OriginalYAML: yamlComment,
	}
}

func (c *Converter) convertSendPackets(content interface{}, testPath string, testName string) ConvertedStep {
	return c.convertSendPacketsWithOptions(content, testPath, false, testName)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", c.scapyASTParser, genPyPath)
	irJSON, err := cmd.CombinedOutput()
	if err != nil {
		return ConvertedStep{}, fmt.Errorf("AST parser failed: %w: %s", err, string(irJSON))
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
	return codegen.GeneratePacketFunction(funcName, packets, isExpect), nil
}

// convertSendPacketsLegacy is the original PCAP-based packet converter (fallback)
// convertSendPacketsLegacy removed - was just a wrapper around convertSendPacketsWithOptionsLegacy

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
	// Try both map types (YAML parsers may return either)
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
			c.debugLog("Failed to analyze %s: %v", sendFile, err)
			if c.config.Verbose {
				fmt.Printf("Warning: failed to analyze pcap file %s: %v\n", sendFile, err)
			}
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
					c.debugLog("Empty expect file %s - packet should be dropped", expectFile)
					if c.config.Verbose {
						fmt.Printf("Detected empty expect file %s - packet should be dropped\n", expectFile)
					}
				} else {
					c.debugLog("Failed to analyze expect %s: %v", expectFile, err)
					if c.config.Verbose {
						fmt.Printf("Warning: failed to analyze expect file %s: %v\n", expectFile, err)
					}
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
	// Best-effort: parse expected counters and log them. Detailed validation TBD.
	expected := make(map[int]int)
	if m, ok := content.(map[interface{}]interface{}); ok {
		for k, v := range m {
			// keys are typically numeric (flow ids), values expected counts
			var keyInt, valInt int
			switch kt := k.(type) {
			case int:
				keyInt = kt
			case int64:
				keyInt = int(kt)
			case string:
				if n, err := strconv.Atoi(kt); err == nil {
					keyInt = n
				}
			}
			switch vt := v.(type) {
			case int:
				valInt = vt
			case int64:
				valInt = int(vt)
			case string:
				if n, err := strconv.Atoi(vt); err == nil {
					valInt = n
				}
			}
			if keyInt != 0 {
				expected[keyInt] = valInt
			}
		}
	}

	// Generate minimal Go code that logs expectations; real CLI-based checks can be added later
	var pairs []string
	for k, v := range expected {
		pairs = append(pairs, fmt.Sprintf("%d:%d", k, v))
	}
	sort.Strings(pairs)
	joined := strings.Join(pairs, ", ")

	goCode := fmt.Sprintf(`t.Logf("Expected counters: %s")`, joined)

	return ConvertedStep{
		Type:        "checkCounters",
		GoCode:      goCode,
		Description: "Counters checking (logging expectations)",
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
	_, err := fw.CLI.ExecuteCommands(commands...)
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

	lines := strings.Split(checkContent, "\n")
	var checkCommands []string
	var expectedLines []string
	var regexes []string
	var originalLines []string
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

		originalLines = append(originalLines, line)

		switch {
		case strings.HasPrefix(line, "YANET_FORMAT_COLUMNS="):
			// command line; stop capturing expected output for separated sections
			captureExpected = false
			payload := strings.TrimPrefix(line, "YANET_FORMAT_COLUMNS=")
			checkCommands = append(checkCommands, fmt.Sprintf(`"%s %s"`, framework.CLIGeneric, payload))
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
			// separators – include in expected block if capturing, otherwise skip
			if captureExpected {
				expectedLines = append(expectedLines, line)
			}
		case captureExpected:
			expectedLines = append(expectedLines, line)
		default:
			// heuristics: if no markers provided and line looks like output, track it
			if len(line) > 0 && !strings.HasPrefix(line, "module") && len(checkCommands) > 0 {
				expectedLines = append(expectedLines, line)
			}
		}
	}

	if len(checkCommands) == 0 {
		return NewSkipStep("cli_check", "cli_check has no commands to execute")
	}

	yamlComment := buildCLIStepComment("cli_check", originalLines)
	goCode := buildCLICommandBlock("cli_check", yamlComment, checkCommands, expectedLines, regexes)

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
		output, err := fw.CLI.ExecuteCommand(cmd)
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
		for _, expected := range expectedOutput {
			if !strings.Contains(output, expected) {
				t.Errorf("Expected output not found: %s", expected)
			}
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
			re := regexp.MustCompile(pattern)
			if !re.MatchString(output) {
				t.Errorf("Regex pattern not matched: %s", pattern)
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

// convertCLICommand converts yanet1 command to yanet2 CLI command
func (c *Converter) convertCLICommand(cmd string) string {
	c.debugLog("Converting CLI command: %s", cmd)
	originalCmd := cmd
	cmd = strings.TrimSpace(cmd)

	// Balancer command conversion
	if strings.HasPrefix(cmd, "balancer real enable") {
		// Example: "balancer real enable balancer0 10.0.0.16 tcp any 100.0.0.1 any"
		parts := strings.Fields(cmd)
		if len(parts) >= 8 {
			module := parts[3]

			// Validate module name
			if err := c.validateModuleName(module, "balancer"); err != nil {
				c.debugLog("Warning: %v, using module anyway", err)
			}

			virtualIP := parts[4]
			proto := parts[5]
			virtualPort := parts[6]
			realIP := parts[7]
			realPort := "any"
			if len(parts) > 8 {
				realPort = parts[8]
			}

			result := fmt.Sprintf(`"%s real enable --cfg %s --instances 0 --virtual-ip %s --proto %s --virtual-port %s --real-ip %s --real-port %s"`,
				framework.CLIBalancer, module, virtualIP, proto, virtualPort, realIP, realPort)
			c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
			return result
		}
	}

	if strings.HasPrefix(cmd, "balancer real disable") {
		// Example: "balancer real disable balancer0 203.0.113.1 tcp 80 2000::1 80"
		parts := strings.Fields(cmd)
		if len(parts) >= 8 {
			module := parts[3]

			// Validate module name
			if err := c.validateModuleName(module, "balancer"); err != nil {
				c.debugLog("Warning: %v, using module anyway", err)
			}

			virtualIP := parts[4]
			proto := parts[5]
			virtualPort := parts[6]
			realIP := parts[7]
			realPort := "any"
			if len(parts) > 8 {
				realPort = parts[8]
			}

			result := fmt.Sprintf(`"%s real disable --cfg %s --instances 0 --virtual-ip %s --proto %s --virtual-port %s --real-ip %s --real-port %s"`,
				framework.CLIBalancer, module, virtualIP, proto, virtualPort, realIP, realPort)
			c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
			return result
		}
	}

	if strings.HasPrefix(cmd, "balancer real flush") {
		// Use default balancer module or extract from command if provided
		module := c.getDefaultModuleName("balancer")
		if module == "" {
			module = "balancer0" // fallback
		}

		result := fmt.Sprintf(`"%s real flush --cfg %s --instances 0"`, framework.CLIBalancer, module)
		c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
		return result
	}

	// NAT64 command conversion
	if strings.HasPrefix(cmd, "nat64") {
		// NAT64 commands need more complex transformation
		// Examples:
		// "nat64 prefix add 64:ff9b::/96"
		// "nat64 mapping add 1.1.1.1 2001::1.1.1.1"

		// Get NAT64 module name from inventory
		nat64Module := c.getDefaultModuleName("nat64")
		if nat64Module == "" {
			nat64Module = "nat64_0" // fallback
			c.debugLog("Warning: NAT64 module not found in config, using fallback: %s", nat64Module)
		}

		// Remove "nat64" prefix and add CLI prefix
		nat64Cmd := strings.TrimPrefix(cmd, "nat64")
		nat64Cmd = strings.TrimSpace(nat64Cmd)

		// Transform specific commands
		if strings.HasPrefix(nat64Cmd, "prefix add") {
			// "prefix add 64:ff9b::/96" -> "prefix add --cfg nat64_0 --instances 0 --prefix 64:ff9b::/96"
			parts := strings.Fields(nat64Cmd)
			if len(parts) >= 3 {
				prefix := parts[2]
				return fmt.Sprintf(`"%s prefix add --cfg %s --instances 0 --prefix %s"`, framework.CLINAT64, nat64Module, prefix)
			}
		} else if strings.HasPrefix(nat64Cmd, "mapping add") {
			// "mapping add 1.1.1.1 2001::1.1.1.1" -> "mapping add --cfg nat64_0 --instances 0 --ipv4 1.1.1.1 --ipv6 2001::1.1.1.1 --prefix-index 0"
			parts := strings.Fields(nat64Cmd)
			if len(parts) >= 3 {
				ipv4 := parts[2]
				ipv6 := ""
				if len(parts) >= 4 {
					ipv6 = parts[3]
				}
				return fmt.Sprintf(`"%s mapping add --cfg %s --instances 0 --ipv4 %s --ipv6 %s --prefix-index 0"`, framework.CLINAT64, nat64Module, ipv4, ipv6)
			}
		} else if strings.HasPrefix(nat64Cmd, "drop") {
			// "nat64 drop enable" -> "drop --cfg nat64_0 --instances 0 --enable"
			dropCmd := strings.TrimPrefix(nat64Cmd, "drop")
			dropCmd = strings.TrimSpace(dropCmd)
			return fmt.Sprintf(`"%s drop --cfg %s --instances 0 %s"`, framework.CLINAT64, nat64Module, dropCmd)
		}

		// Default for NAT64 commands
		return fmt.Sprintf(`"%s %s"`, framework.CLINAT64, nat64Cmd)
	}

	// Route command conversion
	if strings.HasPrefix(cmd, "route") {
		// Examples:
		// "route insert 1.1.1.0/24 via 10.0.0.1"
		// "route remove 1.1.1.0/24"

		routeCmd := strings.TrimPrefix(cmd, "route")
		routeCmd = strings.TrimSpace(routeCmd)

		if strings.HasPrefix(routeCmd, "insert") {
			// "insert 1.1.1.0/24 via 10.0.0.1" -> "insert --cfg route0 --instances 0 --via 10.0.0.1 1.1.1.0/24"
			parts := strings.Fields(routeCmd)
			if len(parts) >= 4 && parts[2] == "via" {
				prefix := parts[1]
				via := parts[3]
				return fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, framework.CLIRoute, via, prefix)
			} else if len(parts) >= 6 && parts[2] == "label" && parts[4] == "via" {
				// "insert 200.1.1.1/32 label 111 via 200.0.0.1" -> "insert --cfg route0 --instances 0 --via 200.0.0.1 --label 111 200.1.1.1/32"
				prefix := parts[1]
				label := parts[3]
				via := parts[5]
				return fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s --label %s %s"`, framework.CLIRoute, via, label, prefix)
			}
		} else if strings.HasPrefix(routeCmd, "remove") {
			// "remove 1.1.1.0/24" -> "remove --cfg route0 --instances 0 1.1.1.0/24"
			parts := strings.Fields(routeCmd)
			if len(parts) >= 2 {
				prefix := parts[1]
				return fmt.Sprintf(`"%s remove --cfg route0 --instances 0 %s"`, framework.CLIRoute, prefix)
			}
		}

		// Default for route commands
		return fmt.Sprintf(`"%s %s"`, framework.CLIRoute, routeCmd)
	}

	// ACL command conversion
	if strings.HasPrefix(cmd, "acl") {
		// Examples:
		// "acl rule add ..."

		aclCmd := strings.TrimPrefix(cmd, "acl")
		aclCmd = strings.TrimSpace(aclCmd)
		return fmt.Sprintf(`"%s %s"`, framework.CLIACL, aclCmd)
	}

	// By default return as is, with yanet-cli prefix
	result := fmt.Sprintf(`"%s %s"`, framework.CLIGeneric, cmd)
	c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
	return result
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
				if c.config.Verbose {
					fmt.Printf("Warning: failed to analyze pcap file %s: %v\n", fileName, err)
				}
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

// sanitizeTestName cleans test name for use in Go and prevents path traversal
func (c *Converter) sanitizeTestName(name string) string {
	// Security: Remove any path separators to prevent path traversal
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	// Replace invalid characters
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")

	// Remove prefix if it looks like full path (e.g., "001_one_port_002_decap_default")
	name = strings.TrimPrefix(name, "001_one_port_")

	// Make first letter uppercase
	if len(name) > 0 {
		name = strings.ToUpper(name[:1]) + name[1:]
	}

	return name
}

// validateGeneratedCode validates the correctness of generated test data before writing to file.
// It checks for required fields and validates CLI command formats.
//
// Parameters:
//   - testData: Generated test data structure
//
// Returns:
//   - error: Validation error if critical issues found (empty test name, etc.)
func (c *Converter) validateGeneratedCode(testData *GoTestData) error {
	c.debugLog("Validating generated test data for: %s", testData.TestName)

	// Check that all required fields are present
	if testData.TestName == "" {
		return fmt.Errorf("test name is empty")
	}

	// Check that CLI commands are in correct format
	for _, step := range testData.Steps {
		if step.Type == "cli" && strings.Contains(step.GoCode, "yanet-cli ") {
			if !strings.Contains(step.GoCode, "--cfg") {
				c.debugLog("WARNING: CLI command may be in old format in step type %s: %s", step.Type, step.GoCode)
			}
		}
	}

	return nil
}

// formatGoCode formats generated Go code using go/format
func (c *Converter) formatGoCode(code string) string {
	// Use go/format to properly format the code
	formatted, err := format.Source([]byte(code))
	if err != nil {
		// If formatting fails, log warning and return original
		if c.config.Verbose {
			fmt.Printf("Warning: failed to format code with go/format: %v\n", err)
		}
		return code
	}
	return string(formatted)
}

// generateGoTest generates a complete Go test file from the converted test data.
// It selects the appropriate template based on test type (NAT64, balancer, route, etc.),
// formats the code with go/format, and writes it to the output directory.
//
// Parameters:
//   - testData: Complete test data including steps, packets, and configuration
//
// Returns:
//   - error: Error if template generation, formatting, or file writing fails
//
// The generated test file includes:
//   - Package declaration and imports
//   - Test function with framework initialization
//   - Configuration steps (module setup)
//   - Packet test cases with validation
//   - Helper functions for packet creation
func (c *Converter) generateGoTest(testData *GoTestData) error {
	c.debugLog("Generating Go test for: %s", testData.TestName)

	// Validate before generating
	if err := c.validateGeneratedCode(testData); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	// Collect all functions from all steps
	var allFunctions []string
	for _, step := range testData.Steps {
		allFunctions = append(allFunctions, step.Functions...)
	}

	// Choose template based on test type
	var tmpl string
	switch testData.TestType {
	case "nat64":
		tmpl = c.generateNAT64TestTemplate(testData, allFunctions)
	case "balancer":
		tmpl = c.generateBalancerTestTemplate(testData, allFunctions)
	case "route":
		tmpl = c.generateRouteTestTemplate(testData, allFunctions)
	case "acl":
		tmpl = c.generateACLTestTemplate(testData, allFunctions)
	case "decap":
		tmpl = c.generateDecapTestTemplate(testData, allFunctions)
	default:
		tmpl = c.generateGenericTestTemplate(testData, allFunctions)
	}

	t, err := template.New("gotest").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("template creation error: %w", err)
	}

	// Generate code to string first for formatting
	var codeBuffer strings.Builder
	if err := t.Execute(&codeBuffer, testData); err != nil {
		return fmt.Errorf("code generation error: %w", err)
	}

	// Format the generated code
	formattedCode := c.formatGoCode(codeBuffer.String())

	// Create output directory
	c.debugLog("Creating output directory: %s", c.config.OutputDir)
	if err := os.MkdirAll(c.config.OutputDir, 0755); err != nil {
		return fmt.Errorf("output directory creation error: %w", err)
	}

	// Create test file
	outputFile := filepath.Join(c.config.OutputDir, fmt.Sprintf("%s_test.go", strings.ToLower(testData.TestName)))
	c.debugLog("Creating test file: %s", outputFile)
	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("file creation error %s: %w", outputFile, err)
	}
	defer file.Close()

	// Write formatted code to file
	c.debugLog("Writing %d bytes to file", len(formattedCode))
	if _, err := file.WriteString(formattedCode); err != nil {
		return fmt.Errorf("error writing formatted code to file: %w", err)
	}

	c.debugLog("Successfully generated test file: %s", outputFile)
	if c.config.Verbose {
		fmt.Printf("Generated test: %s\n", outputFile)
	}

	return nil
}

// generateTestStepsInOrder generates all test steps in the order they appear in autotest.yaml
func (c *Converter) generateTestStepsInOrder(steps []ConvertedStep) string {
	var result strings.Builder
	routeCounter := 0
	packetCounter := 0
	isFirstPacketTest := true

	for _, step := range steps {
		// Skip empty steps (skipped due to skiplist rules)
		if step.Type == "" {
			continue
		}

		// Handle route configuration steps
		if step.Type == "ipv4Update" || step.Type == "ipv6Update" || step.Type == "ipv4LabelledUpdate" || step.Type == "ipv4Remove" || step.Type == "ipv4LabelledRemove" {
			routeCounter++
			result.WriteString(fmt.Sprintf(`
	t.Run("Step_%03d_Configure_Routes", func(t *testing.T) {
		// %s
		%s
	})`, routeCounter, step.Description, step.GoCode))
		} else if step.Type == "sendPackets" {
			// Add delay before the first packet test (after all configuration steps)
			if isFirstPacketTest {
				result.WriteString(`

	// Wait 3 seconds for configuration changes to take effect (pipeline updates are asynchronous)
	time.Sleep(3 * time.Second)
`)
				isFirstPacketTest = false
			}

			// Generate test cases for each packet group (per PCAP entry)
			for _, testCase := range step.PacketTests {
				packetCounter++
				// Build the test code using strings.Builder

				// Note: Error handling removed - new socket-based code handles drops gracefully

				result.WriteString(fmt.Sprintf(`
	t.Run("Step_%03d_Test_Packet", func(t *testing.T) {
		// Test case: %s -> %s
		sendPackets := %s
		require.NotNil(t, sendPackets)
		require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

		%s

		// Get socket client
		client, err := fw.GetSocketClient(0)
		require.NoError(t, err, "Failed to get socket client")
		defer client.Close()
		require.NoError(t, client.Connect(), "Failed to connect to socket")

		var receivedPackets []gopacket.Packet
		for idx, pkt := range sendPackets {
			t.Logf("Sending packet %%d of %%d from `+testCase.SendPcap+`", idx+1, len(sendPackets))
			packetBytes := pkt.Data()

			// Send packet
			require.NoError(t, client.SendPacket(packetBytes), "Failed to send packet %%d", idx)

			// Receive packet (ignore errors - packet may be dropped)
			responseData, _ := client.ReceivePacket(100 * time.Millisecond)
			if responseData != nil {
				receivedPkt := gopacket.NewPacket(responseData, layers.LayerTypeEthernet, gopacket.Default)
				receivedPackets = append(receivedPackets, receivedPkt)
			}

			// Small delay to prevent socket buffer overflow when sending many packets rapidly
			// This gives the dataplane time to process packets before the socket buffer fills up
			if idx < len(sendPackets)-1 {
				time.Sleep(1 * time.Millisecond)
			}
		}

`,
					packetCounter, testCase.SendPcap, testCase.ExpectPcap,
					c.generatePacketFunctionCall(testCase.FunctionName),
					c.generateExpectedPacketSetup(&testCase),
				))

				// Add packet validation after all packets are sent and received
				if !testCase.IsDropExpected {
					result.WriteString(c.generateBatchPacketValidation(&testCase))
				}
				result.WriteString(`
	})`)
			}
			continue
		} else {
			// Handle other step types (cli, checkCounters, etc.)
			result.WriteString(fmt.Sprintf(`
	t.Run("Step_%s", func(t *testing.T) {
		// %s
		%s
	})`, step.Type, step.Description, step.GoCode))
		}
	}
	return result.String()
}

// generatePacketFunctions generates functions for packet creation
func (c *Converter) generatePacketFunctions(testData *GoTestData) string {
	var result strings.Builder

	// Generate functions only from steps (they are already created in convertSendPackets with correct numbers)
	for _, step := range testData.Steps {
		for _, fn := range step.Functions {
			result.WriteString(fn + "\n\n")
		}
	}

	return result.String()
}

// generateTestHeader creates unified header for all test types
func (c *Converter) generateTestHeader(testName, originalTestName, testType string) string {
	// Always include full imports for packet testing
	imports := `import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/migration/converter/lib"
)`

	silenceCode := `
	// Silence potentially unused imports PCAP vs AST parser
	_ = cmp.Diff
	_ = cmpopts.IgnoreUnexported
	_ = lib.NewPacket
	_ = net.ParseIP
	_ = strings.Join`

	return fmt.Sprintf(`package converted

%s

// Test%s - automatically generated test from yanet1
// Original test: %s
// Test type: %s
func Test%s(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")%s
`, imports, testName, originalTestName, testType, testName, silenceCode)
}

// generateNAT64TestTemplate generates template for NAT64 tests
func (c *Converter) generateNAT64TestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)

	// Generate NAT64 configuration commands from parsed config
	var nat64Commands []string
	var nat64ModuleName string
	if testData.ParsedConfig != nil {
		for moduleName, module := range testData.ParsedConfig.Modules {
			if module.Type == "nat64stateless" || module.Type == "nat64stateful" {
				nat64ModuleName = moduleName
				// Collect unique prefixes from translations
				prefixMap := make(map[string]int) // prefix -> index
				var prefixes []string

				for _, trans := range module.Translations {
					if trans.IPv6DestinationAddress != "" {
						// Normalize prefix: ensure it ends with /96 if no mask specified
						prefix := trans.IPv6DestinationAddress
						if !strings.Contains(prefix, "/") {
							prefix = prefix + "/96"
						}
						if _, exists := prefixMap[prefix]; !exists {
							prefixMap[prefix] = len(prefixes)
							prefixes = append(prefixes, prefix)
						}
					}
				}

				// Generate prefix add commands
				for _, prefix := range prefixes {
					nat64Commands = append(nat64Commands,
						fmt.Sprintf(`"%s prefix add --cfg %s --instances 0 --prefix %s"`, framework.CLINAT64, moduleName, prefix))
				}

				// Generate mapping add commands
				for _, trans := range module.Translations {
					prefix := trans.IPv6DestinationAddress
					if !strings.Contains(prefix, "/") {
						prefix = prefix + "/96"
					}
					prefixIndex := prefixMap[prefix]
					nat64Commands = append(nat64Commands,
						fmt.Sprintf(`"%s mapping add --cfg %s --instances 0 --ipv4 %s --ipv6 %s --prefix-index %d"`,
							framework.CLINAT64, moduleName, trans.IPv4Address, trans.IPv6Address, prefixIndex))
				}
				break // Use the first NAT64 module found
			}
		}
	}

	// Fallback to default name if no NAT64 module found
	if nat64ModuleName == "" {
		nat64ModuleName = "nat64_0"
	}

	var nat64Cmds string
	if len(nat64Commands) > 0 {
		nat64Cmds = "\n\t\t\t" + strings.Join(nat64Commands, ",\n\t\t\t") + ",\n"
	}

	return fmt.Sprintf(`%s
	t.Run("Step_000_Configure_NAT64_Environment", func(t *testing.T) {
		// Configure NAT64 module
		commands := []string{%s%s
			"%s update --name=test --modules forward:forward0 --modules nat64:%s --modules route:route0 --instance=0",
		}
		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure NAT64 module")

	})

%s
}

%s
`, header,
		c.generateForwardModuleConfig(testData.ParsedConfig),
		nat64Cmds,
		framework.CLIPipeline,
		nat64ModuleName,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateForwardModuleConfig generates forward module configuration
func (c *Converter) generateForwardModuleConfig(config *ControlplaneConfig) string {
	commands := c.generateForwardModuleCommands(config)
	if len(commands) == 0 {
		return ""
	}

	var result strings.Builder
	for _, cmd := range commands {
		result.WriteString(fmt.Sprintf("\t\t\t\"%s\",\n", cmd))
	}
	return result.String()
}

// generateBalancerTestTemplate generates template for balancer tests
func (c *Converter) generateBalancerTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	t.Run("Step_000_Configure_Balancer_Environment", func(t *testing.T) {
		// Configure balancer module
		commands := []string{
			"%s service add --cfg balancer0 --instances 0 --virtual-ip 10.0.0.16 --proto tcp --virtual-port any",
			"%s update --name=test --modules balancer:balancer0 --modules route:route0 --instance=0",
		}
		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure balancer module")

	})

%s
}

%s
`, header,
		framework.CLIBalancer,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateRouteTestTemplate generates template for route tests
func (c *Converter) generateRouteTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
%s
}

%s
`, header,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateACLTestTemplate generates template for ACL tests
func (c *Converter) generateACLTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	t.Run("Step_000_Configure_ACL_Environment", func(t *testing.T) {
		// Configure ACL module
		commands := []string{
			"%s update --name=test --modules acl:acl0 --modules route:route0 --instance=0",
		}
		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure ACL module")

	})

%s
}

%s
`, header,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateDecapTestTemplate generates template for Decap tests
func (c *Converter) generateDecapTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)

	// Generate decap configuration commands from parsed config
	var decapCommands []string
	if testData.ParsedConfig != nil {
		for moduleName, module := range testData.ParsedConfig.Modules {
			if module.Type == "decap" {
				// Add IPv4 destination prefixes
				for _, prefix := range module.IPv4DestinationPrefixes {
					decapCommands = append(decapCommands,
						fmt.Sprintf(`"%s prefix-add --cfg %s --instances 0 -p %s"`, framework.CLIDecap, moduleName, prefix))
				}
				// Add IPv6 destination prefixes
				for _, prefix := range module.IPv6DestinationPrefixes {
					decapCommands = append(decapCommands,
						fmt.Sprintf(`"%s prefix-add --cfg %s --instances 0 -p %s"`, framework.CLIDecap, moduleName, prefix))
				}
			}
		}
	}

	var decapCmds string
	if len(decapCommands) > 0 {
		decapCmds = "\n\t\t\t" + strings.Join(decapCommands, ",\n\t\t\t") + ",\n"
	}

	return fmt.Sprintf(`%s
	t.Run("Step_000_Configure_Decap_Environment", func(t *testing.T) {
		// Configure Decap module
		commands := []string{%s
			"%s update --name=test --modules forward:forward0 --modules decap:decap0 --modules route:route0 --instance=0",
		}
		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure Decap module")
	})

%s
}

%s
`, header,
		decapCmds,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateGenericTestTemplate generates generic template for tests
func (c *Converter) generateGenericTestTemplate(testData *GoTestData, functions []string) string {
	header := c.generateTestHeader(testData.TestName, testData.OriginalTestName, testData.TestType)
	return fmt.Sprintf(`%s
	t.Run("Step_000_Configure_Test_Environment", func(t *testing.T) {
		// Configure test environment with forward (required for packet processing)
		commands := []string{
			"%s update --name=test --modules forward:forward0 --modules route:route0 --instance=0",
		}
		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure test environment")
	})

%s
}

%s
`, header,
		framework.CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

func (c *Converter) generatePacketFunctionCall(functionName string) string {
	if functionName == "" {
		return "nil"
	}
	return functionName + "(t)"
}

func (c *Converter) generateExpectedPacketSetup(testCase *PacketTestCase) string {
	// Check if this is a drop test or no expect function was generated
	if testCase.IsDropExpected || testCase.ExpectFunctionName == "" {
		return "// No expected packets needed (drop test or empty expect file)"
	}
	// Generate expected packets without checking count (fragmentation may change packet count)
	return fmt.Sprintf(`expectedPackets := %s(t)
	require.NotNil(t, expectedPackets)`, testCase.ExpectFunctionName)
}

func (c *Converter) generatePerPacketValidation(testCase *PacketTestCase) string {
	if testCase.IsDropExpected {
		return "require.Nil(t, outputPacket, \"Packet should be dropped\")"
	}
	// If no expect function, just check output exists
	if testCase.ExpectFunctionName == "" {
		return "require.NotNil(t, outputPacket, \"Output packet should be present\")"
	}
	// Generate full validation with expectedPackets
	return `// Validate against expected packet
		if idx < len(expectedPackets) {
			expectedPkt := expectedPackets[idx]
			actualPkt := gopacket.NewPacket(outputPacket.RawData, layers.LayerTypeEthernet, gopacket.Default)
			
			diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(),
				cmpopts.IgnoreUnexported(
					layers.Ethernet{},
					layers.Dot1Q{},
					layers.IPv4{},
					layers.IPv6{},
					layers.TCP{},
					layers.UDP{},
					layers.ICMPv4{},
					layers.ICMPv6{},
					gopacket.DecodeFailure{},
				),
				cmpopts.IgnoreFields(layers.Ethernet{}, "BaseLayer"),
			)
			if diff != "" {
				t.Logf("Packet %d mismatch:\n%s", idx, diff)
			}
			require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)
		}`
}

func (c *Converter) generateBatchPacketValidation(testCase *PacketTestCase) string {
	// If no expect function, just check we received packets
	if testCase.ExpectFunctionName == "" {
		return `
		require.NotEmpty(t, receivedPackets, "Should have received at least one packet")`
	}

	// Generate batch validation comparing all received packets with expected
	return `
		// Validate all received packets against expected packets
		t.Logf("Received %d packets, expected %d packets", len(receivedPackets), len(expectedPackets))
		
		require.Equalf(t, len(expectedPackets), len(receivedPackets), 
			"Packet count mismatch: expected %d, received %d", len(expectedPackets), len(receivedPackets))
		
		for idx, expectedPkt := range expectedPackets {
			actualPkt := receivedPackets[idx]
			
			diff := cmp.Diff(expectedPkt.Layers(), actualPkt.Layers(),
				cmpopts.IgnoreUnexported(
					layers.Ethernet{},
					layers.Dot1Q{},
					layers.IPv4{},
					layers.IPv6{},
					layers.TCP{},
					layers.UDP{},
					layers.ICMPv4{},
					layers.ICMPv6{},
					gopacket.DecodeFailure{},
				),
				cmpopts.IgnoreFields(layers.Ethernet{}, "BaseLayer"),
			)
			if diff != "" {
				t.Logf("Packet %d mismatch:\n%s", idx, diff)
				require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)
			}
		}`
}
