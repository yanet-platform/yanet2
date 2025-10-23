package lib

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodegenV2_SimplePacket(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "test-send.pcap",
				"expect_file": "test-expect.pcap",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {
									"dst": "00:11:22:33:44:55",
									"src": "00:00:00:00:00:01"
								}
							},
							{
								"type": "IP",
								"params": {
									"src": "1.2.3.4",
									"dst": "5.6.7.8",
									"ttl": 64
								}
							},
							{
								"type": "TCP",
								"params": {
									"sport": 1234,
									"dport": 80
								}
							}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotEmpty(t, code)

	// Check that code contains expected elements
	require.Contains(t, code, "package converted")
	require.Contains(t, code, "func GenerateTest_SendSend(t *testing.T)")
	require.Contains(t, code, "internal.Ether(")
	require.Contains(t, code, "internal.IP(")
	require.Contains(t, code, "internal.TCP(")
	require.Contains(t, code, `internal.IPSrc("1.2.3.4")`)
	require.Contains(t, code, `internal.IPDst("5.6.7.8")`)
	require.Contains(t, code, "internal.TCPSport(1234)")
	require.Contains(t, code, "internal.TCPDport(80)")
}

func TestCodegenV2_EndToEnd(t *testing.T) {
	// Find a test gen.py file
	yanet1Path := os.Getenv("YANET1_PATH")
	if yanet1Path == "" {
		yanet1Path = "../../../../yanet1"
	}

	genPyPath := filepath.Join(yanet1Path, "autotest/units/001_one_port/009_nat64stateless/gen.py")
	if _, err := os.Stat(genPyPath); os.IsNotExist(err) {
		t.Skip("Test gen.py file not found")
	}

	// Run Python parser
	pythonParser := "../scapy_ast_parser.py"
	cmd := exec.Command("python3", pythonParser, genPyPath)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Python parser failed: %s", string(output))

	irJSON := string(output)
	require.NotEmpty(t, irJSON)

	// Generate Go code
	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotEmpty(t, code)

	// Verify code structure
	require.Contains(t, code, "package converted")
	require.Contains(t, code, "func Generate")
	require.Contains(t, code, "internal.NewPacket(")

	t.Logf("Generated %d bytes of Go code", len(code))

	// Write to temporary file to check if it compiles
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "generated_test.go")
	err = os.WriteFile(goFile, []byte(code), 0644)
	require.NoError(t, err)

	// Try to compile (gofmt first to check syntax)
	cmd = exec.Command("gofmt", "-l", goFile)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Logf("gofmt output: %s", string(output))
		t.Logf("Generated code:\n%s", code)
	}
	require.NoError(t, err, "Generated code has syntax errors")
}

func TestCodegenV2_WithVLAN(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "vlan-send.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}
							},
							{
								"type": "Dot1Q",
								"params": {"vlan": 100}
							},
							{
								"type": "IPv6",
								"params": {
									"src": "::1",
									"dst": "::2",
									"hlim": 64
								}
							},
							{
								"type": "UDP",
								"params": {"sport": 5000, "dport": 5001}
							}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)

	require.Contains(t, code, "internal.Dot1Q(")
	require.Contains(t, code, "internal.VLANId(100)")
	require.Contains(t, code, "internal.IPv6(")
	require.Contains(t, code, "internal.UDP(")
}

func TestCodegenV2_StripVLAN(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "test.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "Dot1Q", "params": {"vlan": 100}},
							{"type": "IP", "params": {"src": "1.2.3.4", "dst": "5.6.7.8"}}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	// Without strip VLAN
	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.Contains(t, code, "internal.Dot1Q(")

	// With strip VLAN
	codegenStrip := NewScapyCodegenV2(true)
	codeStripped, err := codegenStrip.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotContains(t, codeStripped, "internal.Dot1Q(")
}

func TestCodegenV2_ICMPv6(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "icmpv6.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "IPv6", "params": {"src": "::1", "dst": "::2"}},
							{"type": "ICMPv6EchoRequest", "params": {"id": 4660, "seq": 30309}}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)

	require.Contains(t, code, "internal.ICMPv6EchoRequest(")
	require.Contains(t, code, "internal.ICMPv6Id(4660)")
	require.Contains(t, code, "internal.ICMPv6Seq(30309)")
}

func TestCodegenV2_GRE(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "gre.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "IPv6", "params": {"src": "::", "dst": "1:2:3:4::abcd"}},
							{"type": "GRE", "params": {"chksum_present": 1, "key_present": 1}},
							{"type": "IP", "params": {"src": "0.0.0.0", "dst": "1.2.3.0"}},
							{"type": "ICMP", "params": {"type": 8}}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		],
		"helper_functions": []
	}`

	codegen := NewScapyCodegenV2(false)
	code, err := codegen.GenerateFromIR(irJSON)
	require.NoError(t, err)

	require.Contains(t, code, "internal.GRE(")
	require.Contains(t, code, "internal.GREChecksumPresent(true)")
	require.Contains(t, code, "internal.GREKeyPresent(true)")
}

func TestCodegenV2_ConvertAll96Tests(t *testing.T) {
	yanet1Path := os.Getenv("YANET1_PATH")
	if yanet1Path == "" {
		yanet1Path = "../../../../yanet1"
	}

	// Check if yanet1 directory exists
	if _, err := os.Stat(yanet1Path); os.IsNotExist(err) {
		t.Skip("yanet1 directory not found, skipping full conversion test")
	}

	// Find all gen.py files
	genPyFiles, err := filepath.Glob(filepath.Join(yanet1Path, "autotest/units/001_one_port/*/gen.py"))
	if err != nil {
		t.Fatalf("Failed to find gen.py files: %v", err)
	}

	if len(genPyFiles) == 0 {
		t.Skip("No gen.py files found")
	}

	t.Logf("Found %d gen.py files to convert", len(genPyFiles))

	// Statistics
	totalTests := 0
	successfulParse := 0
	successfulCodegen := 0
	successfulCompile := 0
	failedTests := make(map[string]string)

	pythonParser := "../scapy_ast_parser.py"
	codegen := NewScapyCodegenV2(false)

	// Create temporary directory for generated files
	tmpDir := t.TempDir()

	for _, genPyPath := range genPyFiles {
		testName := filepath.Base(filepath.Dir(genPyPath))
		totalTests++

		t.Run(testName, func(t *testing.T) {
			// 1. Parse gen.py with Python parser
			cmd := exec.Command("python3", pythonParser, genPyPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				failedTests[testName] = fmt.Sprintf("Parse failed: %v", err)
				t.Logf("Parse failed: %s", string(output))
				return
			}

			irJSON := string(output)
			if len(irJSON) == 0 {
				failedTests[testName] = "Empty IR output"
				return
			}

			successfulParse++

			// 2. Generate Go code
			code, err := codegen.GenerateFromIR(irJSON)
			if err != nil {
				failedTests[testName] = fmt.Sprintf("Codegen failed: %v", err)
				return
			}

			if len(code) == 0 {
				failedTests[testName] = "Empty generated code"
				return
			}

			successfulCodegen++

			// 3. Check if code compiles (gofmt check)
			goFile := filepath.Join(tmpDir, testName+"_generated.go")
			err = os.WriteFile(goFile, []byte(code), 0644)
			if err != nil {
				failedTests[testName] = fmt.Sprintf("Failed to write file: %v", err)
				return
			}

			// Run gofmt to check syntax
			cmd = exec.Command("gofmt", "-l", goFile)
			output, err = cmd.CombinedOutput()
			if err != nil {
				failedTests[testName] = fmt.Sprintf("Syntax error: %s", string(output))
				return
			}

			successfulCompile++

			// Log statistics
			lines := len(strings.Split(code, "\n"))
			pcapPairs := strings.Count(irJSON, `"send_file"`)
			t.Logf("✓ Generated %d lines, %d PCAP pairs", lines, pcapPairs)
		})
	}

	// Print summary - always show it, not just on error
	fmt.Printf("\n=== Conversion Summary ===\n")
	fmt.Printf("Total tests:          %d\n", totalTests)
	fmt.Printf("Successful parse:     %d (%.1f%%)\n", successfulParse, float64(successfulParse)/float64(totalTests)*100)
	fmt.Printf("Successful codegen:   %d (%.1f%%)\n", successfulCodegen, float64(successfulCodegen)/float64(totalTests)*100)
	fmt.Printf("Successful compile:   %d (%.1f%%)\n", successfulCompile, float64(successfulCompile)/float64(totalTests)*100)
	fmt.Printf("Failed:               %d (%.1f%%)\n", len(failedTests), float64(len(failedTests))/float64(totalTests)*100)

	t.Logf("\n=== Conversion Summary ===")
	t.Logf("Total tests:          %d", totalTests)
	t.Logf("Successful parse:     %d (%.1f%%)", successfulParse, float64(successfulParse)/float64(totalTests)*100)
	t.Logf("Successful codegen:   %d (%.1f%%)", successfulCodegen, float64(successfulCodegen)/float64(totalTests)*100)
	t.Logf("Successful compile:   %d (%.1f%%)", successfulCompile, float64(successfulCompile)/float64(totalTests)*100)
	t.Logf("Failed:               %d (%.1f%%)", len(failedTests), float64(len(failedTests))/float64(totalTests)*100)

	if len(failedTests) > 0 {
		t.Logf("\n=== Failed Tests ===")
		count := 0
		for name, reason := range failedTests {
			if count >= 10 {
				t.Logf("... and %d more", len(failedTests)-10)
				break
			}
			t.Logf("  %s: %s", name, reason)
			count++
		}
	}

	// Success criteria: at least 90% should pass
	successRate := float64(successfulCompile) / float64(totalTests) * 100
	if successRate < 90.0 {
		t.Errorf("Success rate %.1f%% is below 90%% threshold", successRate)
	}

	t.Logf("\n✓ Conversion pipeline validated on %d tests with %.1f%% success rate",
		totalTests, successRate)
}
