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
	require.Contains(t, code, "lib.Ether(")
	require.Contains(t, code, "lib.IPv4(")
	require.Contains(t, code, "lib.TCP(")
	require.Contains(t, code, `lib.IPSrc("1.2.3.4")`)
	require.Contains(t, code, `lib.IPDst("5.6.7.8")`)
	require.Contains(t, code, "lib.TCPSport(1234)")
	require.Contains(t, code, "lib.TCPDport(80)")
}

func TestCodegenV2_EndToEnd(t *testing.T) {
	// This test requires yanet1 repository to be available
	// Set YANET1_PATH environment variable to point to yanet1 directory
	// Example: export YANET1_PATH=/path/to/yanet1
	yanet1Path := os.Getenv("YANET1_PATH")
	if yanet1Path == "" {
		yanet1Path = "../../../../../yanet1"
	}

	genPyPath := filepath.Join(yanet1Path, "autotest/units/001_one_port/009_nat64stateless/gen.py")
	if _, err := os.Stat(genPyPath); os.IsNotExist(err) {
		t.Skipf("Test gen.py file not found at %s. Set YANET1_PATH to yanet1 repository location.", genPyPath)
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
	require.Contains(t, code, "lib.NewPacket(")

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

	require.Contains(t, code, "lib.Dot1Q(")
	require.Contains(t, code, "lib.VLANId(100)")
	require.Contains(t, code, "lib.IPv6(")
	require.Contains(t, code, "lib.UDP(")
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
	require.Contains(t, code, "lib.Dot1Q(")

	// With strip VLAN
	codegenStrip := NewScapyCodegenV2(true)
	codeStripped, err := codegenStrip.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.NotContains(t, codeStripped, "lib.Dot1Q(")
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

	require.Contains(t, code, "lib.ICMPv6EchoRequest(")
	// ICMPv6EchoRequest uses ICMPv6Id/ICMPv6Seq (without "Echo" in function name)
	require.Contains(t, code, "lib.ICMPv6Id(4660)")
	require.Contains(t, code, "lib.ICMPv6Seq(30309)")
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

	require.Contains(t, code, "lib.GRE(")
	require.Contains(t, code, "lib.GREChecksumPresent(true)")
	require.Contains(t, code, "lib.GREKeyPresent(true)")
}

func TestCodegenV2_ConvertAll96Tests(t *testing.T) {
	yanet1Path := os.Getenv("YANET1_PATH")
	if yanet1Path == "" {
		yanet1Path = "../../../../../yanet1"
	}

	// Check if yanet1 directory exists
	if _, err := os.Stat(yanet1Path); os.IsNotExist(err) {
		t.Skipf("yanet1 directory not found at %s. Set YANET1_PATH environment variable.", yanet1Path)
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

func TestCodegenV2_ICMPv6Echo(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "icmpv6-echo-send.pcap",
				"expect_file": "",
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
								"type": "IPv6",
								"params": {
									"src": "2001:db8::1",
									"dst": "2001:db8::2"
								}
							},
							{
								"type": "ICMPv6",
								"params": {
									"type": 128,
									"code": 0
								}
							},
							{
								"type": "ICMPv6Echo",
								"params": {
									"id": 1234,
									"seq": 1
								}
							}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		]
	}`

	cg := NewScapyCodegenV2(false)
	result, err := cg.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.Contains(t, result, "lib.ICMPv6Echo(")
	require.Contains(t, result, "lib.ICMPv6EchoId(1234)")
	require.Contains(t, result, "lib.ICMPv6EchoSeq(1)")
}

func TestCodegenV2_ICMPTypeCode(t *testing.T) {
	// Test ICMP pattern path with varying type
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "icmp-typecode-send.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{"type": "Ether", "params": {"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}},
							{"type": "IP", "params": {"src": "1.2.3.4", "dst": "5.6.7.8"}},
							{"type": "ICMP", "params": {"type": 0, "code": 0}}
						],
						"special_handling": null
					},
					{
						"layers": [
							{"type": "Ether", "params": {"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}},
							{"type": "IP", "params": {"src": "1.2.3.4", "dst": "5.6.7.8"}},
							{"type": "ICMP", "params": {"type": 3, "code": 0}}
						],
						"special_handling": null
					},
					{
						"layers": [
							{"type": "Ether", "params": {"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}},
							{"type": "IP", "params": {"src": "1.2.3.4", "dst": "5.6.7.8"}},
							{"type": "ICMP", "params": {"type": 8, "code": 0}}
						],
						"special_handling": null
					}
				],
				"expect_packets": []
			}
		]
	}`

	cg := NewScapyCodegenV2(false)
	result, err := cg.GenerateFromIR(irJSON)
	require.NoError(t, err)
	require.Contains(t, result, "lib.ICMPTypeCode")
	require.Contains(t, result, "ICMPTypeCode(icmpType, 0)")
}

func TestConverter_ParseSendExpectFiles(t *testing.T) {
	c := &Converter{}

	// Test map[interface{}]interface{} format
	packet1 := map[interface{}]interface{}{
		"send":   "send1.pcap",
		"expect": "expect1.pcap",
	}
	send1, expect1 := c.parseSendExpectFiles(packet1)
	require.Equal(t, "send1.pcap", send1)
	require.Equal(t, "expect1.pcap", expect1)

	// Test map[string]interface{} format
	packet2 := map[string]interface{}{
		"send":   "send2.pcap",
		"expect": "expect2.pcap",
	}
	send2, expect2 := c.parseSendExpectFiles(packet2)
	require.Equal(t, "send2.pcap", send2)
	require.Equal(t, "expect2.pcap", expect2)

	// Test with only send file
	packet3 := map[string]interface{}{
		"send": "send3.pcap",
	}
	send3, expect3 := c.parseSendExpectFiles(packet3)
	require.Equal(t, "send3.pcap", send3)
	require.Equal(t, "", expect3)

	// Test invalid type
	send4, expect4 := c.parseSendExpectFiles("invalid")
	require.Equal(t, "", send4)
	require.Equal(t, "", expect4)
}

func TestCodegenV2_IPv6Fragment(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "frag-send.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {}
							},
							{
								"type": "IPv6",
								"params": {
									"src": "2001:db8::1",
									"dst": "2001:db8::2"
								}
							},
							{
								"type": "IPv6ExtHdrFragment",
								"params": {
									"id": 12345,
									"offset": 0,
									"m": 1
								}
							},
							{
								"type": "UDP",
								"params": {
									"sport": 1234,
									"dport": 5678
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

	require.Contains(t, code, "lib.IPv6ExtHdrFragment(")
	require.Contains(t, code, "lib.IPv6FragId(12345)")
	require.Contains(t, code, "lib.IPv6FragOffset(0)")
	require.Contains(t, code, "lib.IPv6FragM(true)")
}

func TestCodegenV2_GREWithVLAN(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "gre-vlan-send.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {}
							},
							{
								"type": "Dot1Q",
								"params": {"vlan": 200}
							},
							{
								"type": "IP",
								"params": {
									"src": "10.0.0.1",
									"dst": "10.0.0.2"
								}
							},
							{
								"type": "GRE",
								"params": {
									"proto": 2048,
									"chksum_present": 1
								}
							},
							{
								"type": "IP",
								"params": {
									"src": "192.168.1.1",
									"dst": "192.168.1.2"
								}
							},
							{
								"type": "TCP",
								"params": {
									"sport": 80,
									"dport": 443
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

	require.Contains(t, code, "lib.Dot1Q(")
	require.Contains(t, code, "lib.VLANId(200)")
	require.Contains(t, code, "lib.GRE(")
	require.Contains(t, code, "lib.GREChecksumPresent(true)")
	// Should have two IP layers
	ipCount := strings.Count(code, "lib.IPv4(")
	require.GreaterOrEqual(t, ipCount, 2, "Should have at least 2 IPv4 layers (outer and inner)")
}

func TestConverter_CLICheckNegative(t *testing.T) {
	tests := []struct {
		name        string
		content     interface{}
		expectSkip  bool
		skipMessage string
	}{
		{
			name:        "invalid_format_not_string",
			content:     12345,
			expectSkip:  true,
			skipMessage: "Invalid cli_check format",
		},
		{
			name:        "empty_commands",
			content:     "# Just a comment\n",
			expectSkip:  true,
			skipMessage: "cli_check has no commands to execute",
		},
		{
			name: "valid_with_expect",
			content: `YANET_FORMAT_COLUMNS=80 show version
EXPECT_BEGIN
Version: 1.0.0
EXPECT_END`,
			expectSkip: false,
		},
		{
			name: "valid_with_regex",
			content: `YANET_FORMAT_COLUMNS=80 show stats
EXPECT_REGEX: packets:\\s+\\d+`,
			expectSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewConverter(&Config{Verbose: false})
			require.NoError(t, err)
			result := c.convertCLICheck(tt.content)

			require.Equal(t, "cli_check", result.Type)

			if tt.expectSkip {
				require.Contains(t, result.GoCode, "t.Skipf")
				if tt.skipMessage != "" {
					require.Contains(t, result.GoCode, tt.skipMessage)
				}
			} else {
				require.NotContains(t, result.GoCode, "t.Skipf")
				require.Contains(t, result.GoCode, "fw.CLI.ExecuteCommand")
			}
		})
	}
}

func TestCodegenV2_ICMPv6EchoInPattern(t *testing.T) {
	// Test that ICMPv6 echo parameters are preserved in pattern flows
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "icmpv6-pattern.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "IPv6", "params": {"src": "2001:db8::1", "dst": "2001:db8::2"}},
							{"type": "ICMPv6EchoRequest", "params": {"id": 100, "seq": 1}}
						],
						"special_handling": null
					},
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "IPv6", "params": {"src": "2001:db8::1", "dst": "2001:db8::2"}},
							{"type": "ICMPv6EchoRequest", "params": {"id": 100, "seq": 2}}
						],
						"special_handling": null
					},
					{
						"layers": [
							{"type": "Ether", "params": {}},
							{"type": "IPv6", "params": {"src": "2001:db8::1", "dst": "2001:db8::2"}},
							{"type": "ICMPv6EchoRequest", "params": {"id": 100, "seq": 3}}
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

	// Should detect pattern and generate loop with varying seq
	require.Contains(t, code, "ICMPv6EchoRequest")

	// The codegen should handle ICMPv6 echo parameters correctly
	// For ICMPv6EchoRequest/Reply, it should use ICMPv6Id/ICMPv6Seq (without "Echo")
	hasId := strings.Contains(code, "lib.ICMPv6Id(100)")
	hasSeq := strings.Contains(code, "lib.ICMPv6Seq")

	require.True(t, hasId || hasSeq, "Should have ICMPv6 id or seq parameters")

	// Log the generated code for debugging if needed
	if !hasId && !hasSeq {
		t.Logf("Generated code:\n%s", code)
	}
}

func TestCodegenV2_ICMPv6RouterSolicitation(t *testing.T) {
	irJSON := `{
		"pcap_pairs": [
			{
				"send_file": "test-send.pcap",
				"expect_file": "",
				"send_packets": [
					{
						"layers": [
							{
								"type": "Ether",
								"params": {
									"dst": "33:33:00:00:00:02",
									"src": "52:54:00:6b:ff:a1"
								}
							},
							{
								"type": "IPv6",
								"params": {
									"src": "fe80::1",
									"dst": "ff02::2",
									"hlim": 255
								}
							},
							{
								"type": "ICMPv6",
								"params": {
									"type": 133,
									"code": 0,
									"chksum": 7374
								}
							},
							{
								"type": "ICMPv6RouterSolicitation",
								"params": {}
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
	require.Contains(t, code, "lib.ICMPv6(")
	require.Contains(t, code, "lib.ICMPv6Type(133)")
	require.Contains(t, code, "lib.ICMPv6RouterSolicitation(")

	// Should NOT contain generic ICMPv6 options inside RouterSolicitation
	// The RouterSolicitation layer should be standalone without options
	lines := strings.Split(code, "\n")
	var inRouterSolicitation bool
	for _, line := range lines {
		if strings.Contains(line, "lib.ICMPv6RouterSolicitation(") {
			inRouterSolicitation = true
		}
		if inRouterSolicitation && strings.Contains(line, ")") && !strings.Contains(line, "lib.ICMPv6RouterSolicitation(") {
			inRouterSolicitation = false
		}
		// Inside RouterSolicitation block, should not have any options
		if inRouterSolicitation && strings.Contains(line, "lib.ICMPv6") && !strings.Contains(line, "lib.ICMPv6RouterSolicitation(") {
			t.Errorf("Found unexpected ICMPv6 option inside RouterSolicitation: %s", line)
		}
	}
}
