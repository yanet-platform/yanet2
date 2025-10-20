package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v2"
)

// VM infrastructure addresses from framework.go:16-46
const (
	VMSrcMAC      = "52:54:00:6b:ff:a1"
	VMDstMAC      = "52:54:00:6b:ff:a5"
	VMIPv4Host    = "203.0.113.14"
	VMIPv4Gateway = "203.0.113.1"
	VMIPv6Gateway = "fe80::1"
	VMIPv6Host    = "fe80::5054:ff:fe6b:ffa5"
	VMIPv6Zero    = "::"

	// NAT64 addresses from nat64_test.go (GOOD EXAMPLE)
	NAT64Prefix       = "2001:db8::/96"
	NAT64MappedIPv4_1 = "198.51.100.1"
	NAT64MappedIPv4_2 = "198.51.100.2"
	NAT64MappedIPv6_1 = "2001:db8::4"
	NAT64MappedIPv6_2 = "2001:db8::3"
	NAT64OuterIPv4    = "192.0.2.34"

	// Decap addresses from decap_test.go
	DecapIPv4Prefix = "4.5.6.7/32"
	DecapIPv6Prefix = "1:2:3:4::abcd/128"

	// Balancer addresses (flexible, can use any valid IPs)
	BalancerDefaultVIP = "10.0.0.16"
	BalancerRealBase   = "10.0.1."

	// CLI tool paths (configurable for different environments)
	CLIBasePath = "/mnt/target/release"
	CLIRoute    = CLIBasePath + "/yanet-cli-route"
	CLIBalancer = CLIBasePath + "/yanet-cli-balancer"
	CLINAT64    = CLIBasePath + "/yanet-cli-nat64"
	CLIACL      = CLIBasePath + "/yanet-cli-acl"
	CLIPipeline = CLIBasePath + "/yanet-cli-pipeline"
	CLIGeneric  = CLIBasePath + "/yanet-cli"
)

// Config contains the converter configuration
type Config struct {
	InputDir     string
	OutputDir    string
	Verbose      bool
	Debug        bool // Enable debug logging for conversions
	SkiplistPath string
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

	content.WriteString("## General statistics\n\n")
	content.WriteString(fmt.Sprintf("- **Total tests**: %d\n", s.TotalTests))

	successPct := 0.0
	failedPct := 0.0
	if s.TotalTests > 0 {
		successPct = float64(s.SuccessTests) / float64(s.TotalTests) * 100
		failedPct = float64(s.FailedTests) / float64(s.TotalTests) * 100
	}

	content.WriteString(fmt.Sprintf("- **Successful**: %d (%.1f%%)\n", s.SuccessTests, successPct))
	content.WriteString(fmt.Sprintf("- **Errors**: %d (%.1f%%)\n", s.FailedTests, failedPct))
	content.WriteString(fmt.Sprintf("- **Skipped**: %d\n\n", s.SkippedTests))

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

// Converter performs conversion of yanet1 tests to yanet2
type Converter struct {
	config           *Config
	pcapAnalyzer     *PcapAnalyzer
	packetCounter    int // Global counter for unique packet function names
	stepCounter      int // Counter for unique step names
	skiplist         map[string]SkiplistEntry
	defaultStripVLAN bool
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
	c := &Converter{
		config:       config,
		pcapAnalyzer: NewPcapAnalyzer(config.Verbose),
		skiplist:     make(map[string]SkiplistEntry),
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

// effectiveState returns effective state for a step (1-based index)
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
	contentBytes, err := ioutil.ReadFile(c.config.SkiplistPath)
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
		sb.WriteString(name)
		sb.WriteString(":\n")
		sb.WriteString("  state: disabled\n")
		sb.WriteString("  steps:\n")
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
			if regexp.MustCompile(`^\s*steps:\s*$`).MatchString(line) {
				inSteps = true
			}
			i++
			continue
		}
		m := regexp.MustCompile(`^(\s*)-\s*([A-Za-z0-9_]+):\s*$`).FindStringSubmatch(line)
		if m == nil {
			// end if we see a new top-level mapping key
			if (line == strings.TrimLeft(line, " \t")) && regexp.MustCompile(`^[A-Za-z0-9_\"].*:\s*$`).MatchString(line) && !strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ") {
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
			if regexp.MustCompile(fmt.Sprintf(`^\s{0,%d}-\s+[A-Za-z0-9_]+:\s*$`, baseIndent)).MatchString(ln) || (len(ln)-len(strings.TrimLeft(ln, " \t")) <= baseIndent-1) {
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
		if m := regexp.MustCompile(`^([A-Za-z0-9_\-]+):\s*$`).FindStringSubmatch(ln); m != nil {
			result[m[1]] = struct{}{}
			continue
		}
		if m := regexp.MustCompile(`^"([^"]+)":\s*$`).FindStringSubmatch(ln); m != nil {
			result[m[1]] = struct{}{}
			continue
		}
	}
	return result, nil
}

// getAddressMappings returns a map of yanet1 to yanet2 address conversions
func (c *Converter) getAddressMappings() map[string]string {
	return map[string]string{
		// IPv4 gateway addresses adaptation
		"200.0.0.1": VMIPv4Gateway, // 203.0.113.1
		"200.0.0.2": VMIPv4Host,    // 203.0.113.14

		// IPv6 gateway addresses adaptation
		"fe80::1": VMIPv6Gateway, // fe80::1 (same)
		"fe80::2": VMIPv6Host,    // fe80::5054:ff:fe6b:ffa5

		// Common test addresses from yanet1 that should be adapted
		// These are frequently used in yanet1 tests
		"aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:1": VMIPv6Gateway,
		"aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:2": VMIPv6Host,

		// Add more mappings as discovered during conversion
	}
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
	return filepath.Walk(c.config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() && strings.Contains(path, "001_one_port") {
			// Found test directory
			testName := filepath.Base(path)
			if c.config.Verbose {
				fmt.Printf("Processing test: %s\n", testName)
			}
			return c.ConvertSingleTest(path, testName)
		}

		return nil
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

// ConvertSingleTest converts a single test
// testPath should be the full path to the test directory containing autotest.yaml
// testName is used for output file naming
func (c *Converter) ConvertSingleTest(testPath, testName string) error {
	// Reset counters for each test to avoid leakage between tests
	c.packetCounter = 0
	c.stepCounter = 0
	c.defaultStripVLAN = false

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
		return fmt.Errorf("autotest.yaml not found at %s", autotestPath)
	}

	yamlData, err := os.ReadFile(autotestPath)
	if err != nil {
		return fmt.Errorf("error reading %s: %w", autotestPath, err)
	}

	var test YanetTest
	if err := yaml.Unmarshal(yamlData, &test); err != nil {
		return fmt.Errorf("error parsing YAML %s: %w", autotestPath, err)
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

	// Analyze pcap files
	pcapFiles, err := c.analyzePcapFiles(testPath)
	if err != nil {
		return fmt.Errorf("error analyzing pcap files: %w", err)
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

	return &config, nil
}

// generateForwardModuleCommands generates commands for forward module based on logicalPort
func (c *Converter) generateForwardModuleCommands(config *ControlplaneConfig) []string {
	// Forward module is already configured in framework.go, no additional commands needed
	return nil
}

// convertStepsWithSkip applies skiplist/test defaults and passes stripVLAN to sendPackets steps
func (c *Converter) convertStepsWithSkip(testName string, steps []map[string]interface{}, testPath string) []ConvertedStep {
	var converted []ConvertedStep
	for i, step := range steps {
		stepIndex := i + 1
		state := c.effectiveState(testName, stepIndex)
		if state == StateDisabled {
			c.debugLog("Skipping step %d due to skiplist: disabled", stepIndex)
			continue
		}
		for stepType, content := range step {
			convertedStep := c.convertStepWithState(stepType, content, testPath, state, testName)
			if convertedStep.GoCode != "" || len(convertedStep.PacketTests) > 0 {
				converted = append(converted, convertedStep)
			}
		}
	}
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
		return ConvertedStep{Type: "ipv4Update", GoCode: "// TODO: Invalid ipv4Update format"}
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
			cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, CLIRoute, adaptedNexthop, prefix)
			commands = append(commands, cmd)
			c.debugLog("  Generated route insert: %s", cmd)
		}
	}

	if len(commands) == 0 {
		return ConvertedStep{Type: "ipv4Update", GoCode: "// TODO: No valid IPv4 routes found"}
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
		return ConvertedStep{Type: "ipv6Update", GoCode: "// TODO: Invalid ipv6Update format"}
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

			cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, CLIRoute, adaptedNexthop, prefix)
			commands = append(commands, cmd)
			c.debugLog("  Generated route insert: %s", cmd)
		}
	}

	if len(commands) == 0 {
		return ConvertedStep{Type: "ipv6Update", GoCode: "// TODO: No valid IPv6 routes found"}
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
		return ConvertedStep{Type: "ipv4LabelledUpdate", GoCode: "// TODO: Invalid ipv4LabelledUpdate format"}
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
				cmd := fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, CLIRoute, adaptedNexthop, prefix)
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
		return ConvertedStep{Type: "ipv4Remove", GoCode: "// TODO: Invalid ipv4Remove format"}
	}

	var routeStrings []string
	var commands []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)

		// yanet2 CLI route doesn't support remove command, comment it out
		cmd := fmt.Sprintf(`// Route remove not supported in yanet2: "%s remove --cfg route0 --instances 0 %s"`, CLIRoute, strings.TrimSpace(routeStr))
		commands = append(commands, cmd)
		c.debugLog("  Route remove commented out: %s", routeStr)
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to remove IPv4 routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

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
		return ConvertedStep{Type: "ipv4LabelledRemove", GoCode: "// TODO: Invalid ipv4LabelledRemove format"}
	}

	var routeStrings []string
	var commands []string
	for _, route := range routes {
		routeStr, ok := route.(string)
		if !ok {
			continue
		}
		routeStrings = append(routeStrings, routeStr)

		// yanet2 CLI route doesn't support remove command, comment it out
		cmd := fmt.Sprintf(`// Route remove not supported in yanet2: "%s remove --cfg route0 --instances 0 %s"`, CLIRoute, strings.TrimSpace(routeStr))
		commands = append(commands, cmd)
		c.debugLog("  Labeled route remove commented out: %s", routeStr)
	}

	// Generate YAML comment
	yamlComment := c.generateYAMLComment(stepType, routeStrings)

	goCode := fmt.Sprintf(`%scommands := []string{
		%s,
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to remove IPv4 labelled routes")`, yamlComment, strings.Join(commands, ",\n\t\t"))

	return ConvertedStep{
		Type:         "ipv4LabelledRemove",
		GoCode:       goCode,
		Description:  "IPv4 routes removal with labels",
		OriginalYAML: yamlComment,
	}
}

// convertSendPackets converts sendPackets step
func (c *Converter) convertSendPackets(content interface{}, testPath string, testName string) ConvertedStep {
	packets, ok := content.([]interface{})
	if !ok {
		return ConvertedStep{Type: "sendPackets", GoCode: "// TODO: Invalid sendPackets format"}
	}

	var functions []string
	var packetTests []PacketTestCase
	step := ConvertedStep{
		Type:        "sendPackets",
		Description: "Packet sending and validation",
	}

	for _, packet := range packets {
		packetMap, ok := packet.(map[interface{}]interface{})
		if !ok {
			continue
		}

		var sendFile, expectFile string
		if s, exists := packetMap["send"]; exists {
			sendFile = fmt.Sprintf("%v", s)
		}
		if e, exists := packetMap["expect"]; exists {
			expectFile = fmt.Sprintf("%v", e)
		}

		// Analyze send pcap file
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

		c.packetCounter++
		funcName := fmt.Sprintf("create%sSendPacket%d", testName, c.packetCounter)
		tcpdumpComment, err := c.pcapAnalyzer.GenerateTcpdumpComment(sendPcapPath, sendPackets)
		if err != nil {
			c.debugLog("tcpdump failed for %s: %v", sendFile, err)
			tcpdumpComment = fmt.Sprintf("// tcpdump error: %v\n", err)
		}
		funcCode := tcpdumpComment + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(sendPackets, funcName, CodegenOpts{StripVLAN: c.defaultStripVLAN})
		functions = append(functions, funcCode)

		var expectPackets []*PacketInfo
		isDropExpected := false
		expectPcapPath := filepath.Join(testPath, expectFile)

		_, err = os.Stat(expectPcapPath)
		if err == nil {
			expectPackets, err = c.pcapAnalyzer.ReadAllPacketsFromFile(expectPcapPath)
			if err != nil {
				c.debugLog("Failed to analyze expect %s: %v", expectFile, err)
			}
			c.debugLog("Expect packets count: %d", len(expectPackets))
			if len(expectPackets) == 0 {
				isDropExpected = true
				c.debugLog("Empty expect file %s - packet should be dropped", expectFile)
			}
		}

		expectFuncName := ""
		if !isDropExpected && len(expectPackets) > 0 {
			expectFuncName = fmt.Sprintf("create%sExpectPacket%d", testName, c.packetCounter)
			tcpdumpExpect, err := c.pcapAnalyzer.GenerateTcpdumpComment(expectPcapPath, expectPackets)
			if err != nil {
				c.debugLog("tcpdump failed for expect %s: %v", expectFile, err)
				tcpdumpExpect = fmt.Sprintf("// tcpdump error: %v\n", err)
			}
			expectFuncCode := tcpdumpExpect + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(expectPackets, expectFuncName, CodegenOpts{StripVLAN: c.defaultStripVLAN, IsExpect: true})
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
	// GoCode will be generated later in generateSendPacketsSteps

	return step
}

// convertSendPacketsWithOptions is like convertSendPackets but supports stripping VLAN at codegen time
func (c *Converter) convertSendPacketsWithOptions(content interface{}, testPath string, stripVLAN bool, testName string) ConvertedStep {
	packets, ok := content.([]interface{})
	if !ok {
		return ConvertedStep{Type: "sendPackets", GoCode: "// TODO: Invalid sendPackets format"}
	}

	var functions []string
	var packetTests []PacketTestCase
	step := ConvertedStep{
		Type:        "sendPackets",
		Description: "Packet sending and validation",
	}

	for _, packet := range packets {
		packetMap, ok := packet.(map[interface{}]interface{})
		if !ok {
			continue
		}

		var sendFile, expectFile string
		if s, exists := packetMap["send"]; exists {
			sendFile = fmt.Sprintf("%v", s)
		}
		if e, exists := packetMap["expect"]; exists {
			expectFile = fmt.Sprintf("%v", e)
		}

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
		funcCode := tcpdumpComment + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(sendPackets, funcName, CodegenOpts{StripVLAN: stripVLAN})
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
			expectFuncCode := tcpdumpExpect + c.pcapAnalyzer.GeneratePacketCreationCodeWithOptions(expectPackets, expectFuncName, CodegenOpts{StripVLAN: stripVLAN, IsExpect: true})
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
		return ConvertedStep{Type: "cli", GoCode: "// TODO: Invalid cli format"}
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
		return ConvertedStep{Type: "cli_check", GoCode: "// TODO: Invalid cli_check format"}
	}

	// Parse multiline content for checking
	lines := strings.Split(checkContent, "\n")
	var checkCommands []string
	var expectedOutput string
	var originalLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		originalLines = append(originalLines, line)
		if strings.HasPrefix(line, "YANET_FORMAT_COLUMNS=") {
			// This is a command to execute
			checkCommands = append(checkCommands, fmt.Sprintf(`"%s %s"`, CLIGeneric, strings.TrimPrefix(line, "YANET_FORMAT_COLUMNS=")))
		} else if strings.Contains(line, "---------") {
			// This is a table header, skip
			continue
		} else if len(line) > 10 && !strings.HasPrefix(line, "module") {
			// This is expected output
			expectedOutput = line
		}
	}

	var commandsStr string
	if len(checkCommands) > 0 {
		commandsStr = "\n\t\t" + strings.Join(checkCommands, ",\n\t\t") + ","
	}

	// YAML comment
	var yamlCommentBuilder strings.Builder
	yamlCommentBuilder.WriteString("// Original autotest.yaml step:\n")
	yamlCommentBuilder.WriteString("// cli_check:\n")
	for _, l := range originalLines {
		yamlCommentBuilder.WriteString(fmt.Sprintf("//   %s\n", l))
	}
	yamlComment := yamlCommentBuilder.String()

	goCode := fmt.Sprintf(`%s// Check CLI command output
	commands := []string{%s
	}
	_, err := fw.CLI.ExecuteCommands(commands...)
	require.NoError(t, err, "Failed to execute CLI check commands")

    // Execute and validate CLI output contains expected snippet (best-effort)
    outputs, err := fw.CLI.ExecuteCommands(commands...)
    require.NoError(t, err, "Failed to execute CLI check commands")
    combined := strings.Join(outputs, "\n")
    require.Contains(t, combined, %q)`, yamlComment, commandsStr, expectedOutput)

	return ConvertedStep{
		Type:         "cli_check",
		GoCode:       goCode,
		Description:  "Check CLI command output",
		OriginalYAML: yamlComment,
	}
}

// adaptBalancerIPAddressInCommand adapts IP addresses in balancer commands
// Note: Currently unused, but may be needed for future IP address adaptation logic
func (c *Converter) adaptBalancerIPAddressInCommand(cmd string) string {
	c.debugLog("Adapting balancer IP addresses in command")
	// Map for replacing IP addresses in balancer commands
	ipReplacements := map[string]string{
		// Virtual IPs - use constants
		"10.0.0.16": BalancerDefaultVIP,
		// Real IPs - use 10.0.1.0/24 range for servers
		"100.0.0.1": BalancerRealBase + "1",
		"100.0.0.2": BalancerRealBase + "2",
		"100.0.0.3": BalancerRealBase + "3",
		"100.0.0.4": BalancerRealBase + "4",
		// IPv6 addresses - can be left flexible for balancer
		"2001::1": "2001:db8::1",
		"2001::2": "2001:db8::2",
		"2000::1": "2001:db8:1::1",
		"2000::2": "2001:db8:1::2",
		"2000::3": "2001:db8:1::3",
		"2000::4": "2001:db8:1::4",
	}

	// Regular expression for finding IPv4 addresses
	re := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	cmd = re.ReplaceAllStringFunc(cmd, func(ip string) string {
		if replacement, exists := ipReplacements[ip]; exists {
			return replacement
		}
		// If address is not in replacement map, leave as is
		return ip
	})

	// Similarly for IPv6
	re6 := regexp.MustCompile(`\b([0-9a-fA-F]{1,4}:){1,7}[0-9a-fA-F]{1,4}\b`)
	cmd = re6.ReplaceAllStringFunc(cmd, func(ip string) string {
		if replacement, exists := ipReplacements[ip]; exists {
			return replacement
		}
		return ip
	})

	return cmd
}

// adaptIPAddress adapts a single IP address to yanet2 infrastructure
func (c *Converter) adaptIPAddress(ipAddr string) string {
	ipAddr = strings.TrimSpace(ipAddr)

	// First check the address mapping for known yanet1 -> yanet2 conversions
	mappings := c.getAddressMappings()
	if newIP, ok := mappings[ipAddr]; ok {
		return newIP
	}

	// Keep original address if not in the mapping
	return ipAddr
}

// adaptGenericIPAddressInCommand adapts IP addresses in generic commands
// Note: Currently unused, but may be needed for future IP address adaptation logic
func (c *Converter) adaptGenericIPAddressInCommand(cmd string) string {
	c.debugLog("Adapting generic IP addresses in command")

	// First use the address mapping for known yanet1 -> yanet2 conversions
	mappings := c.getAddressMappings()
	for oldIP, newIP := range mappings {
		cmd = strings.ReplaceAll(cmd, oldIP, newIP)
		c.debugLog("  Mapped %s -> %s", oldIP, newIP)
	}

	// Do not rewrite other IPs generically; preserve originals unless explicitly mapped

	return cmd
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
			virtualIP := parts[4]
			proto := parts[5]
			virtualPort := parts[6]
			realIP := parts[7]
			realPort := "any"
			if len(parts) > 8 {
				realPort = parts[8]
			}

			result := fmt.Sprintf(`"%s real enable --cfg %s --instances 0 --virtual-ip %s --proto %s --virtual-port %s --real-ip %s --real-port %s"`,
				CLIBalancer, module, virtualIP, proto, virtualPort, realIP, realPort)
			c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
			return result
		}
	}

	if strings.HasPrefix(cmd, "balancer real disable") {
		// Example: "balancer real disable balancer0 203.0.113.1 tcp 80 2000::1 80"
		parts := strings.Fields(cmd)
		if len(parts) >= 8 {
			module := parts[3]
			virtualIP := parts[4]
			proto := parts[5]
			virtualPort := parts[6]
			realIP := parts[7]
			realPort := "any"
			if len(parts) > 8 {
				realPort = parts[8]
			}

			result := fmt.Sprintf(`"%s real disable --cfg %s --instances 0 --virtual-ip %s --proto %s --virtual-port %s --real-ip %s --real-port %s"`,
				CLIBalancer, module, virtualIP, proto, virtualPort, realIP, realPort)
			c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
			return result
		}
	}

	if strings.HasPrefix(cmd, "balancer real flush") {
		result := fmt.Sprintf(`"%s real flush --cfg balancer0 --instances 0"`, CLIBalancer)
		c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
		return result
	}

	// NAT64 command conversion
	if strings.HasPrefix(cmd, "nat64") {
		// NAT64 commands need more complex transformation
		// Examples:
		// "nat64 prefix add 64:ff9b::/96"
		// "nat64 mapping add 1.1.1.1 2001::1.1.1.1"

		// Remove "nat64" prefix and add CLI prefix
		nat64Cmd := strings.TrimPrefix(cmd, "nat64")
		nat64Cmd = strings.TrimSpace(nat64Cmd)

		// Transform specific commands
		if strings.HasPrefix(nat64Cmd, "prefix add") {
			// "prefix add 64:ff9b::/96" -> "prefix add --cfg nat64_0 --instances 0 --prefix 64:ff9b::/96"
			parts := strings.Fields(nat64Cmd)
			if len(parts) >= 3 {
				prefix := parts[2]
				return fmt.Sprintf(`"%s prefix add --cfg nat64_0 --instances 0 --prefix %s"`, CLINAT64, prefix)
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
				return fmt.Sprintf(`"%s mapping add --cfg nat64_0 --instances 0 --ipv4 %s --ipv6 %s --prefix-index 0"`, CLINAT64, ipv4, ipv6)
			}
		} else if strings.HasPrefix(nat64Cmd, "drop") {
			// "nat64 drop enable" -> "drop --cfg nat64_0 --instances 0 --enable"
			dropCmd := strings.TrimPrefix(nat64Cmd, "drop")
			dropCmd = strings.TrimSpace(dropCmd)
			return fmt.Sprintf(`"%s drop --cfg nat64_0 --instances 0 %s"`, CLINAT64, dropCmd)
		}

		// Default for NAT64 commands
		return fmt.Sprintf(`"%s %s"`, CLINAT64, nat64Cmd)
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
				return fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s %s"`, CLIRoute, via, prefix)
			} else if len(parts) >= 6 && parts[2] == "label" && parts[4] == "via" {
				// "insert 200.1.1.1/32 label 111 via 200.0.0.1" -> "insert --cfg route0 --instances 0 --via 200.0.0.1 --label 111 200.1.1.1/32"
				prefix := parts[1]
				label := parts[3]
				via := parts[5]
				return fmt.Sprintf(`"%s insert --cfg route0 --instances 0 --via %s --label %s %s"`, CLIRoute, via, label, prefix)
			}
		} else if strings.HasPrefix(routeCmd, "remove") {
			// "remove 1.1.1.0/24" -> "remove --cfg route0 --instances 0 1.1.1.0/24"
			parts := strings.Fields(routeCmd)
			if len(parts) >= 2 {
				prefix := parts[1]
				return fmt.Sprintf(`"%s remove --cfg route0 --instances 0 %s"`, CLIRoute, prefix)
			}
		}

		// Default for route commands
		return fmt.Sprintf(`"%s %s"`, CLIRoute, routeCmd)
	}

	// ACL command conversion
	if strings.HasPrefix(cmd, "acl") {
		// Examples:
		// "acl rule add ..."

		aclCmd := strings.TrimPrefix(cmd, "acl")
		aclCmd = strings.TrimSpace(aclCmd)
		return fmt.Sprintf(`"%s %s"`, CLIACL, aclCmd)
	}

	// By default return as is, with yanet-cli prefix
	result := fmt.Sprintf(`"%s %s"`, CLIGeneric, cmd)
	c.debugLog("Converted CLI command: %s -> %s", originalCmd, result)
	return result
}

// convertSleep converts sleep step
func (c *Converter) convertSleep(content interface{}) ConvertedStep {
	c.debugLog("Converting sleep step: %v seconds", content)
	seconds, ok := content.(int)
	if !ok {
		return ConvertedStep{Type: "sleep", GoCode: "// TODO: Invalid sleep format"}
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

// sanitizeTestName cleans test name for use in Go
func (c *Converter) sanitizeTestName(name string) string {
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

// validateGeneratedCode validates correctness of generated data
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

// generateGoTest generates Go test file
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
			// Generate test cases for each packet group (per PCAP entry)
			for _, testCase := range step.PacketTests {
				packetCounter++
				// Build the test code using strings.Builder

				// Generate error assertion based on drop expectation
				var errorAssertion string
				if testCase.IsDropExpected {
					errorAssertion = `require.Error(t, err, "Packet should be dropped")
			require.NotNil(t, inputPacket, "Input packet should be parsed")
			require.Nil(t, outputPacket, "Output packet should be absent")`
				} else {
					errorAssertion = `require.NoError(t, err, "Failed to send packet %d from ` + testCase.SendPcap + `", idx)
			require.NotNil(t, inputPacket, "Input packet should be parsed")
			require.NotNil(t, outputPacket, "Output packet should be present")`
				}

				// Add 3-second delay before the first packet test
				var delayCode string
				if isFirstPacketTest {
					delayCode = `
		// Wait 3 seconds for configuration changes to take effect
		time.Sleep(3 * time.Second)`
					isFirstPacketTest = false
				}

				result.WriteString(fmt.Sprintf(`
	t.Run("Step_%03d_Test_Packet", func(t *testing.T) {
		// Test case: %s -> %s
		sendPackets := %s
		require.NotNil(t, sendPackets)
		require.NotEqual(t, 0, len(sendPackets), "Expected at least one packet to send")

		%s%s

		for idx, pkt := range sendPackets {
			t.Logf("Sending packet %%d of %%d from `+testCase.SendPcap+`", idx, len(sendPackets))
			packetBytes := pkt.Data()
			inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packetBytes, 100*time.Millisecond)
			%s

`,
					packetCounter, testCase.SendPcap, testCase.ExpectPcap,
					c.generatePacketFunctionCall(testCase.FunctionName),
					c.generateExpectedPacketSetup(&testCase),
					delayCode,
					errorAssertion))

				// Only add packet validation for non-drop cases
				if !testCase.IsDropExpected {
					result.WriteString(c.generatePerPacketValidation(&testCase))
				}
				result.WriteString(`
		}
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

// generateRouteConfigurationSteps generates route configuration steps (DEPRECATED: use generateTestStepsInOrder)
func (c *Converter) generateRouteConfigurationSteps(steps []ConvertedStep) string {
	var result strings.Builder
	routeCounter := 0
	for _, step := range steps {
		if step.Type == "ipv4Update" || step.Type == "ipv6Update" || step.Type == "ipv4LabelledUpdate" || step.Type == "ipv4Remove" || step.Type == "ipv4LabelledRemove" {
			routeCounter++
			result.WriteString(fmt.Sprintf(`
	t.Run("Configure_Routes_%d", func(t *testing.T) {
		// %s
		%s
	})`, routeCounter, step.Description, step.GoCode))
		}
	}
	return result.String()
}

// generateAdditionalSteps generates additional steps
func (c *Converter) generateAdditionalSteps(steps []ConvertedStep) string {
	var result strings.Builder
	additionalCounter := 0
	for _, step := range steps {
		if step.Type != "ipv4Update" && step.Type != "ipv6Update" && step.Type != "ipv4LabelledUpdate" && step.Type != "ipv4Remove" && step.Type != "ipv4LabelledRemove" && step.Type != "sendPackets" {
			additionalCounter++
			result.WriteString(fmt.Sprintf(`
	t.Run("Additional_Step_%s_%d", func(t *testing.T) {
		// %s
		%s
	})`, step.Type, additionalCounter, step.Description, step.GoCode))
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
			result.WriteString(fn)
			result.WriteString("\n\n")
		}
	}

	return result.String()
}

// generateTestHeader creates unified header for all test types
func (c *Converter) generateTestHeader(testName, originalTestName, testType string) string {
	return fmt.Sprintf(`package converted

import (
	"net"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

// Test%s - automatically generated test from yanet1
// Original test: %s
// Test type: %s
func Test%s(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	// Silence potentially unused imports when no diffs are printed
	_ = cmp.Diff
	_ = cmpopts.IgnoreUnexported
`, testName, originalTestName, testType, testName)
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
						fmt.Sprintf(`"%s prefix add --cfg %s --instances 0 --prefix %s"`, CLINAT64, moduleName, prefix))
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
							CLINAT64, moduleName, trans.IPv4Address, trans.IPv6Address, prefixIndex))
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
		CLIPipeline,
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
		CLIBalancer,
		CLIPipeline,
		c.generateTestStepsInOrder(testData.Steps),
		c.generatePacketFunctions(testData))
}

// generateBalancerCLISteps generates CLI command steps for balancer
func (c *Converter) generateBalancerCLISteps(steps []ConvertedStep) string {
	var result strings.Builder
	for _, step := range steps {
		if step.Type == "cli" {
			result.WriteString(fmt.Sprintf(`
	t.Run("Configure_Balancer_Reals", func(t *testing.T) {
		// %s
		%s
	})`, step.Description, step.GoCode))
		}
	}
	return result.String()
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
		CLIPipeline,
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
						fmt.Sprintf(`"/mnt/target/release/yanet-cli-decap prefix-add --cfg %s --instances 0 -p %s"`, moduleName, prefix))
				}
				// Add IPv6 destination prefixes
				for _, prefix := range module.IPv6DestinationPrefixes {
					decapCommands = append(decapCommands,
						fmt.Sprintf(`"/mnt/target/release/yanet-cli-decap prefix-add --cfg %s --instances 0 -p %s"`, moduleName, prefix))
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
		CLIPipeline,
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
		CLIPipeline,
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
	if testCase.IsDropExpected || len(testCase.ExpectPackets) == 0 || testCase.ExpectFunctionName == "" {
		return "// No expected packets needed (drop test or empty expect file)"
	}
	return fmt.Sprintf(`expectedPackets := %s(t)
	require.NotNil(t, expectedPackets)
	require.Equalf(t, len(sendPackets), len(expectedPackets), "Mismatch between sent and expected packets for %s", %q)`, testCase.ExpectFunctionName, testCase.ExpectPcap, testCase.ExpectPcap)
}

func (c *Converter) generatePerPacketValidation(testCase *PacketTestCase) string {
	if testCase.IsDropExpected {
		return "require.Nil(t, outputPacket, \"Packet should be dropped\")"
	}
	if testCase.ExpectFunctionName == "" || len(testCase.ExpectPackets) == 0 {
		return "require.NotNil(t, outputPacket, \"Output packet should be present\")"
	}
	return `require.NotNilf(t, expectedPackets[idx], "Expected packet should be present for index %d", idx)

	actualPkt := gopacket.NewPacket(outputPacket.RawData, layers.LayerTypeEthernet, gopacket.Default)
	require.NotNilf(t, actualPkt, "Actual packet should be parseable for index %d", idx)

	expectedPkt := expectedPackets[idx]

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
		),
	)
	require.Emptyf(t, diff, "Packet layers mismatch for index %d", idx)`
}
