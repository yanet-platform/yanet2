// Package lib implements the yanet1 to yanet2 functional test converter.
//
// The converter translates yanet1 autotests (YAML + PCAP + gen.py) into Go-based
// functional tests for yanet2. It supports multiple conversion modes:
//
//   - AST-based conversion: Parses gen.py using Python AST to extract Scapy packet definitions
//   - PCAP-based conversion: Directly reads PCAP files when gen.py is not available
//   - Hybrid mode: Falls back to PCAP if AST parsing fails
//
// The converter handles:
//
//   - Module configuration (NAT64, balancer, route, forward, decap, ACL)
//   - Pipeline setup and CLI command generation
//   - IP address adaptation (yanet1 test addresses → yanet2 infrastructure)
//   - MAC address normalization (framework.SrcMAC, framework.DstMAC)
//   - Packet generation from PCAP or Scapy definitions
//   - VLAN handling (strip or preserve)
//   - Test skiplist management (enabled/wovlan/disabled states)
//
// Main entry points:
//
//   - NewConverter: Creates a new converter instance
//   - ConvertAllTestsWithStats: Batch convert all tests with statistics
//   - ConvertSingleTest: Convert one test
//
// The generated tests use the yanet2 functional test framework and follow
// the structure: Configure → Send packets → Validate responses.
package lib

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config contains the converter configuration
type Config struct {
	InputDir       string
	OutputDir      string
	Verbose        bool // Enable verbose output (user-facing progress messages)
	Debug          bool // Enable debug logging (technical details, automatically enables Verbose)
	SkiplistPath   string
	ForceASTParser bool // Force use of AST parser (fail if unavailable)
	ForcePCAP      bool // Force use of PCAP analyzer, even if AST is available
	StrictMode     bool // Fail on unsupported layers/special handling (for CI)
	TolerantMode   bool // Continue with warnings on unsupported features (default)
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
	markdown := s.FormatStats()
	// Convert markdown to simple console format
	console := s.markdownToConsole(markdown)
	fmt.Print(console)
}

// FormatStats formats statistics as markdown string
func (s *ConversionStats) FormatStats() string {
	var output strings.Builder

	output.WriteString("## General statistics\n\n")

	// Basic stats
	successPct := 0.0
	failedPct := 0.0
	if s.TotalTests > 0 {
		successPct = float64(s.SuccessTests) / float64(s.TotalTests) * 100
		failedPct = float64(s.FailedTests) / float64(s.TotalTests) * 100
	}

	output.WriteString(fmt.Sprintf("- **Total tests**: %d\n", s.TotalTests))
	output.WriteString(fmt.Sprintf("- **Successful**: %d (%.1f%%)\n", s.SuccessTests, successPct))
	output.WriteString(fmt.Sprintf("- **Errors**: %d (%.1f%%)\n", s.FailedTests, failedPct))
	output.WriteString(fmt.Sprintf("- **Skipped**: %d\n\n", s.SkippedTests))

	// Tests by type
	if len(s.TestsByType) > 0 {
		output.WriteString("## Tests by type\n\n")
		output.WriteString("| Type | Count |\n")
		output.WriteString("|------|-------|\n")
		for testType, count := range s.TestsByType {
			output.WriteString(fmt.Sprintf("| %s | %d |\n", testType, count))
		}
		output.WriteString("\n")
	}

	// Failed tests
	if len(s.FailedTestsDetails) > 0 {
		output.WriteString("## Failed tests\n\n")
		for _, failed := range s.FailedTestsDetails {
			output.WriteString(fmt.Sprintf("### %s\n\n", failed.Name))
			output.WriteString(fmt.Sprintf("```\n%s\n```\n\n", failed.Error))
		}
	}

	return output.String()
}

// markdownToConsole converts markdown formatted stats to simple console format
func (s *ConversionStats) markdownToConsole(markdown string) string {
	var output strings.Builder

	lines := strings.Split(markdown, "\n")
	inCodeBlock := false

	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "## "):
			// Header
			title := strings.TrimPrefix(line, "## ")
			output.WriteString("\n" + strings.Repeat("=", 60) + "\n")
			output.WriteString(strings.ToUpper(title) + "\n")
			output.WriteString(strings.Repeat("=", 60) + "\n")
		case strings.HasPrefix(line, "- **") && strings.Contains(line, "**:"):
			// Bold list item
			parts := strings.SplitN(line, "**: ", 2)
			if len(parts) == 2 {
				label := strings.TrimPrefix(parts[0], "- **")
				value := parts[1]
				output.WriteString(fmt.Sprintf("%-20s %s\n", label+":", value))
			} else {
				output.WriteString(line + "\n")
			}
		case strings.HasPrefix(line, "| ") && strings.Contains(line, " | "):
			// Table row (skip header separator)
			if !strings.Contains(line, "---") {
				parts := strings.Split(strings.Trim(line, "| "), " | ")
				if len(parts) >= 2 {
					output.WriteString(fmt.Sprintf("  %-15s: %s\n", parts[0], parts[1]))
				}
			}
		case strings.HasPrefix(line, "### "):
			// Subheader for failed test
			name := strings.TrimPrefix(line, "### ")
			output.WriteString(fmt.Sprintf("%s:\n", name))
		case strings.HasPrefix(line, "```"):
			// Code block start/end
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				output.WriteString("  ")
			}
		case inCodeBlock:
			// Code block content
			output.WriteString("  " + line + "\n")
		case line == "":
			// Empty line
			output.WriteString("\n")
		default:
			// Regular text
			if line != "" {
				output.WriteString(line + "\n")
			}
		}
	}

	return output.String()
}

// SaveToFile saves statistics to markdown file
func (s *ConversionStats) SaveToFile(filename string) error {
	content := "# Test conversion statistics yanet1 → yanet2\n\n"
	content += fmt.Sprintf("Date: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
	content += s.FormatStats()

	return os.WriteFile(filename, []byte(content), 0644)
}

// findScapyASTParser locates scapy_ast_parser.py relative to converter
func findScapyASTParser() (string, error) {
	// Get executable directory
	ex, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine executable path: %w", err)
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
			log.Printf("[INFO] Found scapy_ast_parser.py at: %s", absPath)
			return absPath, nil
		}
	}

	// Not found - return error with helpful message
	return "", fmt.Errorf("scapy_ast_parser.py not found. Searched locations: %v. To install, run: just dsetup", candidates)
}

// Converter performs conversion of yanet1 tests to yanet2
type Converter struct {
	config                *Config
	pcapAnalyzer          *PcapAnalyzer
	scapyASTParser        string          // Path to scapy_ast_parser.py
	scapyCodegenV2        *ScapyCodegenV2 // New code generator
	packetCounter         int             // Global counter for unique packet function names
	stepCounter           int             // Counter for unique step names
	skiplist              map[string]SkiplistEntry
	defaultStripVLAN      bool
	moduleInventory       moduleInventory
	specialHandlingSkips  map[string]int // Track special handling types that were skipped
	unsupportedLayerTypes map[string]int // Track unsupported layer types
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
func NewConverter(config *Config) (*Converter, error) {
	// Initialize optional AST-based system (PCAP path is default)
	scapyASTParser, err := findScapyASTParser()
	if err != nil {
		// In strict mode or when AST is forced, fail immediately
		if config.StrictMode || config.ForceASTParser {
			return nil, fmt.Errorf("scapy AST parser required but not found: %w", err)
		}
		// In tolerant mode, log warning and continue with PCAP-based conversion
		if config.Verbose {
			log.Printf("[WARNING] %v", err)
			log.Printf("[INFO] Falling back to PCAP-based analysis")
		}
		scapyASTParser = "" // Use PCAP path only
	}

	c := &Converter{
		config:                config,
		pcapAnalyzer:          NewPcapAnalyzer(config.Verbose),
		scapyASTParser:        scapyASTParser,
		scapyCodegenV2:        NewScapyCodegenV2(false), // false = keep VLAN by default
		skiplist:              make(map[string]SkiplistEntry),
		specialHandlingSkips:  make(map[string]int),
		unsupportedLayerTypes: make(map[string]int),
		moduleInventory: moduleInventory{
			balancerModules: make(map[string]struct{}),
		},
	}
	// Connect tracking maps to code generator
	c.scapyCodegenV2.SetTrackingMaps(&c.specialHandlingSkips, &c.unsupportedLayerTypes)
	c.scapyCodegenV2.SetStrictMode(config.StrictMode)

	if err := c.loadSkiplist(); err != nil {
		return nil, fmt.Errorf("failed to load skiplist: %w", err)
	}

	return c, nil
}

// ErrTestSkipped is returned when a test is disabled by skiplist
var ErrTestSkipped = errors.New("test skipped by skiplist")

// debugLog outputs debug information if debug mode is enabled
func (c *Converter) debugLog(format string, args ...interface{}) {
	if c.config.Debug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// verbose outputs verbose information if verbose mode is enabled
func (c *Converter) verbose(format string, args ...interface{}) {
	if c.config.Verbose {
		log.Printf(format, args...)
	}
}

// printConversionSummary prints a summary of special handling and unsupported layers
func (c *Converter) printConversionSummary() {
	if len(c.specialHandlingSkips) == 0 && len(c.unsupportedLayerTypes) == 0 {
		return // Nothing to report
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("CONVERSION WARNINGS SUMMARY")
	fmt.Println(strings.Repeat("=", 60))

	if len(c.specialHandlingSkips) > 0 {
		fmt.Println("\nSpecial handling types skipped:")
		for handlingType, count := range c.specialHandlingSkips {
			fmt.Printf("  - %-20s: %d packet(s)\n", handlingType, count)
		}
		fmt.Println("\nNote: Packets with special handling were commented out in generated tests.")
		fmt.Println("      Uncomment and implement if needed for your test scenarios.")
	}

	if len(c.unsupportedLayerTypes) > 0 {
		fmt.Println("\nUnsupported layer types encountered:")
		for layerType, count := range c.unsupportedLayerTypes {
			fmt.Printf("  - %-20s: %d occurrence(s)\n", layerType, count)
		}
		fmt.Println("\nNote: Unsupported layers may result in incomplete packet generation.")
		fmt.Println("      Check generated code and packet_builder.go for implementation.")
	}

	fmt.Println(strings.Repeat("=", 60))
}

// shouldUseASTParser determines if test should use the AST-based pipeline.
// Default behaviour is to use the PCAP analyzer; AST is only used when
// explicitly forced via configuration.
func (c *Converter) shouldUseASTParser(testPath string) bool {
	// Force AST parser if requested (will fail in AST method if unavailable)
	if c.config.ForceASTParser {
		c.debugLog("ForceASTParser is set, forcing AST parser")
		return true
	}
	// Default: always use PCAP-based analyzer
	return false
}

// loadSkiplist loads skiplist YAML if provided; missing file is tolerated
func (c *Converter) loadSkiplist() error {
	path := strings.TrimSpace(c.config.SkiplistPath)
	if path == "" {
		return nil // No skiplist specified, not an error
	}

	// Validate file size before reading
	fileInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat skiplist file %s: %w", path, err)
	}
	if fileInfo.Size() > MaxSkiplistFileSize {
		return fmt.Errorf("skiplist file too large: %d bytes (max %d)", fileInfo.Size(), MaxSkiplistFileSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read skiplist file %s: %w", path, err)
	}

	if len(data) == 0 {
		return fmt.Errorf("skiplist file %s is empty", path)
	}

	var m map[string]SkiplistEntry
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("failed to parse skiplist YAML: %w", err)
	}
	c.skiplist = m
	c.debugLog("Loaded skiplist with %d entries", len(m))
	return nil
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
	lines, err := ReadNormalizedLinesFromFile(path)
	if err != nil {
		return nil
	}
	return ScanAutotestOriginalBlocksFromLines(lines)
}

// parseTopLevelKeysBeforeMarker returns set of top-level YAML keys that appear
// before the auto-generated marker in skiplist.yaml.
func parseTopLevelKeysBeforeMarker(skiplistPath string) (map[string]struct{}, error) {
	data, err := os.ReadFile(skiplistPath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	result := make(map[string]struct{})
	keys := ParseTopLevelKeysBeforeMarkerFromContent(content, autoGeneratedMarker)
	for _, k := range keys {
		result[k] = struct{}{}
	}
	return result, nil
}

// note: lightweight YAML regex helpers moved to yaml_scan.go

// YanetTest represents the structure of a yanet1 test
type YanetTest struct {
	Steps []map[string]interface{} `yaml:"steps"`
}

// ParseAutotestYAML reads and parses autotest.yaml from the given test directory
func ParseAutotestYAML(testDir string) (*YanetTest, error) {
	if testDir == "" {
		return nil, fmt.Errorf("testDir cannot be empty")
	}

	autotestPath := filepath.Join(testDir, "autotest.yaml")

	data, err := os.ReadFile(autotestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read autotest.yaml: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("autotest.yaml is empty")
	}

	var test YanetTest
	if err := yaml.Unmarshal(data, &test); err != nil {
		return nil, fmt.Errorf("failed to parse autotest.yaml: %w", err)
	}

	// Validate that test has steps
	if test.Steps == nil {
		return nil, fmt.Errorf("autotest.yaml has no steps")
	}
	if len(test.Steps) == 0 {
		return nil, fmt.Errorf("autotest.yaml has empty steps array")
	}

	return &test, nil
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
		c.verbose("Processing test: %s", testName)
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

			c.verbose("Converting test %d: %s", stats.TotalTests, testName)

			// Convert the test
			err := c.ConvertSingleTest(path, testName)
			if err != nil {
				if errors.Is(err, ErrTestSkipped) {
					stats.SkippedTests++
					c.verbose("  ⏭️  Skipped by skiplist")
				} else {
					stats.FailedTests++
					stats.FailedTestsDetails = append(stats.FailedTestsDetails, FailedTest{
						Name:  testName,
						Error: err.Error(),
					})
					c.verbose("  ❌ Error: %v", err)
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
				c.verbose("  ✅ Successful")
			}

			return filepath.SkipDir
		}

		return nil
	})

	// Print summary of special handling and unsupported layers
	c.printConversionSummary()

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
		c.verbose("Skipping test %s due to skiplist: disabled", testName)
		return ErrTestSkipped
	}
	if testState == StateWoVLAN {
		c.defaultStripVLAN = true
		c.debugLog("Test %s: StripVLAN enabled (wovlan)", testName)
	}

	c.debugLog("ConvertSingleTest started for: %s", testName)
	c.debugLog("Test path: %s", testPath)
	c.debugLog("Output directory: %s", c.config.OutputDir)

	// Parse autotest.yaml using shared function
	c.debugLog("Looking for autotest.yaml at: %s", testPath)

	test, err := ParseAutotestYAML(testPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewConversionError(testName, "", fmt.Sprintf("autotest.yaml not found in %s", testPath))
		}
		return NewConversionErrorWrap(testName, "", err, "error parsing autotest.yaml")
	}

	// Read and parse controlplane.conf if it exists
	controlplaneConfig := ""
	var parsedConfig *ControlplaneConfig
	controlplanePath := filepath.Join(testPath, "controlplane.conf")
	if _, err := os.Stat(controlplanePath); err == nil {
		configData, err := os.ReadFile(controlplanePath)
		if err != nil {
			return NewConversionErrorWrap(testName, "", err, "failed to read controlplane.conf")
		}
		controlplaneConfig = string(configData)
		// Parse the configuration
		parsedConfig, err = c.parseControlplaneConfig(controlplanePath)
		if err != nil {
			return NewConversionErrorWrap(testName, "", err, "failed to parse controlplane.conf")
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

// sanitizeTestName cleans test name for use in Go and prevents path traversal
func (c *Converter) sanitizeTestName(name string) string {
	// Security: Remove any path separators to prevent path traversal
	// Use filepath.Base to get only the base name, removing any directory components
	name = filepath.Base(name)

	// Additional security: remove any remaining path traversal attempts
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")

	// Validate that the name is not empty after sanitization
	if name == "" || name == "." || name == ".." {
		name = "InvalidTestName"
	}

	// Replace invalid characters for Go identifiers
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")

	// Remove any non-alphanumeric characters except underscore
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteRune('_')
		}
	}
	name = builder.String()

	// Ensure name doesn't start with a number (invalid Go identifier)
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = "Test_" + name
	}

	// Remove prefix if it looks like full path (e.g., "001_one_port_002_decap_default")
	name = strings.TrimPrefix(name, "001_one_port_")

	// Make first letter uppercase
	if len(name) > 0 {
		name = strings.ToUpper(name[:1]) + name[1:]
	}

	// Final validation: ensure name is not empty
	if name == "" {
		name = "InvalidTestName"
	}

	return name
}
