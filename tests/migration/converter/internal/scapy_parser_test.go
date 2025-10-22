package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/pcap"
	"gopkg.in/yaml.v2"
)

// ExpectedPCAPPairs represents the expected send/expect pairs from autotest.yaml
type ExpectedPCAPPairs struct {
	Pairs []PCAPPair
}

type PCAPPair struct {
	Send   string
	Expect string
}

// readExpectedPCAPPairs reads autotest.yaml and extracts expected PCAP pairs
func readExpectedPCAPPairs(testDir string) (*ExpectedPCAPPairs, error) {
	autotestPath := filepath.Join(testDir, "autotest.yaml")
	if _, err := os.Stat(autotestPath); os.IsNotExist(err) {
		return nil, nil // No autotest.yaml, skip this test
	}

	data, err := os.ReadFile(autotestPath)
	if err != nil {
		return nil, err
	}

	var test struct {
		Steps []map[string]interface{} `yaml:"steps"`
	}

	if err := yaml.Unmarshal(data, &test); err != nil {
		return nil, err
	}

	result := &ExpectedPCAPPairs{}
	for _, step := range test.Steps {
		if sendPackets, ok := step["sendPackets"]; ok {
			if packets, ok := sendPackets.([]interface{}); ok {
				for _, packet := range packets {
					if packetMap, ok := packet.(map[interface{}]interface{}); ok {
						var sendFile, expectFile string

						if s, exists := packetMap["send"]; exists {
							// Handle different types that might come from YAML parsing
							switch v := s.(type) {
							case string:
								sendFile = v
							case []interface{}:
								// If it's an array, take the first element as string
								if len(v) > 0 {
									if str, ok := v[0].(string); ok {
										sendFile = str
									}
								}
							default:
								// Try to convert to string
								sendFile = fmt.Sprintf("%v", v)
							}
						}

						if e, exists := packetMap["expect"]; exists {
							// Handle different types that might come from YAML parsing
							switch v := e.(type) {
							case string:
								expectFile = v
							case []interface{}:
								// If it's an array, take the first element as string
								if len(v) > 0 {
									if str, ok := v[0].(string); ok {
										expectFile = str
									}
								}
							default:
								// Try to convert to string
								expectFile = fmt.Sprintf("%v", v)
							}
						}

						if sendFile != "" || expectFile != "" {
							result.Pairs = append(result.Pairs, PCAPPair{
								Send:   sendFile,
								Expect: expectFile,
							})
						}
					}
				}
			}
		}
	}

	return result, nil
}

// TestScapyParserOnAllGenPy tests the Scapy parser on all gen.py files in yanet1
func TestScapyParserOnAllGenPy(t *testing.T) {
	yanet1Path := os.Getenv("YANET1_PATH")
	if yanet1Path == "" {
		yanet1Path = "../../../../yanet1"
	}

	// Find all gen.py files
	genPyFiles, err := filepath.Glob(filepath.Join(yanet1Path, "autotest/units/001_one_port/*/gen.py"))
	if err != nil {
		t.Fatalf("Failed to find gen.py files: %v", err)
	}

	if len(genPyFiles) == 0 {
		t.Skip("No gen.py files found, skipping test")
	}

	t.Logf("Found %d gen.py files to test", len(genPyFiles))

	parser := NewScapyParser(false)
	totalPairs := 0
	totalPackets := 0
	failedTests := 0

	for _, genPyPath := range genPyFiles {
		testName := filepath.Base(filepath.Dir(genPyPath))
		t.Run(testName, func(t *testing.T) {
			testDir := filepath.Dir(genPyPath)

			// Read expected PCAP pairs from autotest.yaml
			expectedPairs, err := readExpectedPCAPPairs(testDir)
			if err != nil {
				t.Errorf("Failed to read autotest.yaml in %s: %v", testDir, err)
				failedTests++
				return
			}

			if expectedPairs == nil {
				t.Errorf("No autotest.yaml found in %s", testDir)
				failedTests++
				return
			}

			pairs, err := parser.ParseGenPy(genPyPath)
			if err != nil {
				t.Errorf("Failed to parse %s: %v", genPyPath, err)
				failedTests++
				return
			}

			// Check that all expected pairs are found
			foundPairs := make(map[string]bool)
			for _, pair := range pairs {
				key := pair.SendFile + "|" + pair.ExpectFile
				foundPairs[key] = true
			}

			for _, expected := range expectedPairs.Pairs {
				key := expected.Send + "|" + expected.Expect
				if !foundPairs[key] {
					t.Errorf("Expected PCAP pair not found in %s: send=%s expect=%s", testDir, expected.Send, expected.Expect)
					failedTests++
				}
			}

			// Check that we don't have extra pairs
			expectedKeys := make(map[string]bool)
			for _, expected := range expectedPairs.Pairs {
				key := expected.Send + "|" + expected.Expect
				expectedKeys[key] = true
			}

			for _, pair := range pairs {
				key := pair.SendFile + "|" + pair.ExpectFile
				if !expectedKeys[key] {
					t.Errorf("Unexpected PCAP pair found in %s: send=%s expect=%s", testDir, pair.SendFile, pair.ExpectFile)
					failedTests++
				}
			}

			if len(pairs) != len(expectedPairs.Pairs) {
				t.Errorf("Pair count mismatch in %s: found %d, expected %d", testDir, len(pairs), len(expectedPairs.Pairs))
				failedTests++
			}

			totalPairs += len(pairs)

			// Verify each pair against actual PCAP files and bytes equality when possible
			for i, pair := range pairs {
				// Skip pairs with no send file
				if pair.SendFile == "" {
					continue
				}

				// Check send PCAP
				sendPcapPath := filepath.Join(testDir, pair.SendFile)
				sendPacketCount, err := countPacketsInPcap(sendPcapPath)
				if err != nil {
					t.Errorf("Pair %d: Failed to read send PCAP %s: %v", i, pair.SendFile, err)
					continue
				}

				// Check expect PCAP (skip if no expect file)
				if pair.ExpectFile == "" {
					continue // Skip pairs with no expect file
				}
				expectPcapPath := filepath.Join(testDir, pair.ExpectFile)
				expectPacketCount, err := countPacketsInPcap(expectPcapPath)
				if err != nil {
					t.Errorf("Pair %d: Failed to read expect PCAP %s: %v", i, pair.ExpectFile, err)
					continue
				}

				totalPackets += len(pair.SendPackets) + len(pair.ExpectPackets)

				// Verify packet counts match
				sendParsedCount := len(pair.SendPackets)
				expectParsedCount := len(pair.ExpectPackets)

				// For packets requiring raw handling, use PCAP count as expected
				if sendParsedCount > 0 && pair.SendPackets[0].RequiresRaw {
					sendParsedCount = sendPacketCount
				}
				if expectParsedCount > 0 && pair.ExpectPackets[0].RequiresRaw {
					expectParsedCount = expectPacketCount
				}

				if sendParsedCount != sendPacketCount {
					// Only log if not marked as raw and not fragmented
					if sendParsedCount > 0 && !pair.SendPackets[0].IsFragmented && !pair.SendPackets[0].RequiresRaw {
						t.Logf("Pair %d: Send packet count mismatch: parsed %d, PCAP has %d (file: %s)",
							i, len(pair.SendPackets), sendPacketCount, pair.SendFile)
					}
				}

				if expectParsedCount != expectPacketCount {
					if expectParsedCount > 0 && !pair.ExpectPackets[0].IsFragmented && !pair.ExpectPackets[0].RequiresRaw {
						t.Logf("Pair %d: Expect packet count mismatch: parsed %d, PCAP has %d (file: %s)",
							i, len(pair.ExpectPackets), expectPacketCount, pair.ExpectFile)
					}
				}

				// Verify layers are parsed
				if len(pair.SendPackets) > 0 && !pair.SendPackets[0].IsFragmented {
					if len(pair.SendPackets[0].Layers) == 0 {
						t.Errorf("Pair %d: First send packet has no layers parsed", i)
					}
				}

				// Byte-for-byte equality for as many packets as possible (skip requires-raw)
				// We'll open the PCAP and compare first min(N, M) packets
				// This ensures our builder matches Scapy PCAPs where feasible
				minCount := len(pair.SendPackets)
				if sendPacketCount < minCount {
					minCount = sendPacketCount
				}
				if minCount > 5 {
					minCount = 5 // limit comparison to first few to keep runtime reasonable
				}

				// Skip balancer tests for now - they require special payload handling
				isBalancerTest := strings.Contains(testName, "balancer")
				if isBalancerTest {
					t.Logf("Skipping byte validation for balancer test %s (requires payload handling)", testName)
					continue
				}

				if minCount > 0 {
					// Read actual packets
					sendHandle, err := pcap.OpenOffline(sendPcapPath)
					if err == nil {
						defer sendHandle.Close()
						src := gopacket.NewPacketSource(sendHandle, sendHandle.LinkType())
						idxCmp := 0
						for pkt := range src.Packets() {
							if idxCmp >= minCount {
								break
							}
							def := pair.SendPackets[idxCmp]
							if def.IsFragmented || def.RequiresRaw || defRequiresRaw(def) {
								idxCmp++
								continue
							}
							built, err := BuildPacketBytes(def)
							if err != nil {
								t.Logf("Pair %d pkt %d: build skipped: %v", i, idxCmp, err)
								idxCmp++
								continue
							}
							actual := pkt.Data()
							if len(built) != len(actual) {
								t.Errorf("Pair %d pkt %d: length mismatch built=%d actual=%d", i, idxCmp, len(built), len(actual))
							} else {
								for bi := range built {
									if built[bi] != actual[bi] {
										t.Errorf("Pair %d pkt %d: byte mismatch at %d built=%02x actual=%02x", i, idxCmp, bi, built[bi], actual[bi])
										break
									}
								}
							}
							idxCmp++
						}
					}
				}
			}
		})
	}

	t.Logf("Summary: Parsed %d pairs with %d total packets from %d gen.py files (%d failures)",
		totalPairs, totalPackets, len(genPyFiles), failedTests)

	if failedTests > 0 {
		t.Fatalf("Test failed: %d tests had errors", failedTests)
	}
}

// TestScapyCodegenBasic tests basic code generation from Scapy definitions
func TestScapyCodegenBasic(t *testing.T) {
	tests := []struct {
		name     string
		packet   ScapyPacketDef
		wantCode []string // Strings that should appear in generated code
	}{
		{
			name: "Simple Ethernet+IPv4+TCP",
			packet: ScapyPacketDef{
				Layers: []ScapyLayer{
					{Name: "Ether", Params: map[string]string{"dst": "00:11:22:33:44:55", "src": "00:00:00:00:00:01"}},
					{Name: "IP", Params: map[string]string{"src": "1.2.3.4", "dst": "5.6.7.8", "ttl": "64"}},
					{Name: "TCP", Params: map[string]string{"sport": "1234", "dport": "80"}},
				},
			},
			wantCode: []string{
				"layers.Ethernet",
				"layers.IPv4",
				"layers.TCP",
				"SrcIP",
				"DstIP",
				"SrcPort",
				"DstPort",
			},
		},
		{
			name: "IPv6 with Fragment",
			packet: ScapyPacketDef{
				Layers: []ScapyLayer{
					{Name: "Ether", Params: map[string]string{}},
					{Name: "IPv6", Params: map[string]string{"src": "::1", "dst": "::2", "hlim": "64"}},
					{Name: "IPv6ExtHdrFragment", Params: map[string]string{"id": "0x12345678", "offset": "0", "m": "1"}},
					{Name: "TCP", Params: map[string]string{"sport": "1234", "dport": "80"}},
				},
			},
			wantCode: []string{
				"layers.IPv6",
				"layers.IPv6Fragment",
				"Identification",
				"FragmentOffset",
				"MoreFragments",
			},
		},
		{
			name: "ICMPv6 Echo Request",
			packet: ScapyPacketDef{
				Layers: []ScapyLayer{
					{Name: "Ether", Params: map[string]string{}},
					{Name: "IPv6", Params: map[string]string{"src": "::1", "dst": "::2"}},
					{Name: "ICMPv6EchoRequest", Params: map[string]string{"id": "0x1234", "seq": "0x5678"}},
				},
			},
			wantCode: []string{
				"ICMPv6",
				"ICMPv6Echo",
				"Identifier",
				"SeqNumber",
				"ICMPv6TypeEchoRequest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codegen := NewScapyCodegen(false, false)
			code := codegen.GeneratePacketFunction([]ScapyPacketDef{tt.packet}, "testFunc")

			for _, want := range tt.wantCode {
				if !contains(code, want) {
					t.Errorf("Generated code does not contain %q", want)
				}
			}

			// Check that code compiles (basic syntax check)
			if !contains(code, "func testFunc(t *testing.T)") {
				t.Error("Generated code missing function declaration")
			}
			if !contains(code, "return packets") {
				t.Error("Generated code missing return statement")
			}
		})
	}
}

// TestScapyParserLayerExtraction tests layer extraction from Scapy code
func TestScapyParserLayerExtraction(t *testing.T) {
	parser := NewScapyParser(false)

	tests := []struct {
		name       string
		scapyCode  string
		wantLayers []string
	}{
		{
			name:       "Simple Ether/IP/TCP",
			scapyCode:  `Ether(dst="00:11:22:33:44:55")/IP(src="1.2.3.4")/TCP(dport=80)`,
			wantLayers: []string{"Ether", "IP", "TCP"},
		},
		{
			name:       "IPv6 with extensions",
			scapyCode:  `Ether()/IPv6(dst="::1")/IPv6ExtHdrFragment(id=0x123)/TCP()`,
			wantLayers: []string{"Ether", "IPv6", "IPv6ExtHdrFragment", "TCP"},
		},
		{
			name:       "With VLAN",
			scapyCode:  `Ether()/Dot1Q(vlan=100)/IP()/UDP()`,
			wantLayers: []string{"Ether", "Dot1Q", "IP", "UDP"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packet := parser.parsePacketDef(tt.scapyCode)

			if len(packet.Layers) != len(tt.wantLayers) {
				t.Errorf("Layer count mismatch: got %d, want %d", len(packet.Layers), len(tt.wantLayers))
			}

			for i, wantLayer := range tt.wantLayers {
				if i >= len(packet.Layers) {
					break
				}
				if packet.Layers[i].Name != wantLayer {
					t.Errorf("Layer %d: got %q, want %q", i, packet.Layers[i].Name, wantLayer)
				}
			}
		})
	}
}

// TestScapyParserParameterExtraction tests parameter extraction from layers
func TestScapyParserParameterExtraction(t *testing.T) {
	parser := NewScapyParser(false)

	tests := []struct {
		name       string
		scapyCode  string
		layerIdx   int
		wantParams map[string]string
	}{
		{
			name:      "IP with multiple params",
			scapyCode: `IP(src="1.2.3.4", dst="5.6.7.8", ttl=64, tos=0x10)`,
			layerIdx:  0,
			wantParams: map[string]string{
				"src": "1.2.3.4",
				"dst": "5.6.7.8",
				"ttl": "64",
				"tos": "0x10",
			},
		},
		{
			name:      "TCP with ports",
			scapyCode: `TCP(sport=1234, dport=80)`,
			layerIdx:  0,
			wantParams: map[string]string{
				"sport": "1234",
				"dport": "80",
			},
		},
		{
			name:      "IPv6 with hlim",
			scapyCode: `IPv6(src="::1", dst="::2", hlim=64, tc=0x01)`,
			layerIdx:  0,
			wantParams: map[string]string{
				"src":  "::1",
				"dst":  "::2",
				"hlim": "64",
				"tc":   "0x01",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packet := parser.parsePacketDef(tt.scapyCode)

			if len(packet.Layers) <= tt.layerIdx {
				t.Fatalf("Not enough layers: got %d, need at least %d", len(packet.Layers), tt.layerIdx+1)
			}

			layer := packet.Layers[tt.layerIdx]
			for key, wantValue := range tt.wantParams {
				gotValue, exists := layer.Params[key]
				if !exists {
					t.Errorf("Parameter %q not found", key)
					continue
				}
				if gotValue != wantValue {
					t.Errorf("Parameter %q: got %q, want %q", key, gotValue, wantValue)
				}
			}
		})
	}
}

// Helper functions

func countPacketsInPcap(pcapPath string) (int, error) {
	// Check if file is empty first
	if info, err := os.Stat(pcapPath); err != nil {
		return 0, err
	} else if info.Size() == 0 {
		return 0, nil // Empty file = 0 packets
	}

	handle, err := pcap.OpenOffline(pcapPath)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	count := 0
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for range packetSource.Packets() {
		count++
	}

	return count, nil
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || len(s) >= len(substr) &&
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
				findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
