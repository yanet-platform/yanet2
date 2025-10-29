package lib

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"gopkg.in/yaml.v3"
)

// TestPCAPEquivalence verifies that converter-generated packets match original yanet1 PCAPs
// allowing only differences due to framework MAC/IP adaptation.
func TestPCAPEquivalence(t *testing.T) {
	// Get yanet1 root from environment
	yanet1Root := os.Getenv("YANET1_ROOT")
	if yanet1Root == "" {
		yanet1Root = "../../../../../yanet1"
	}

	// Check if yanet1 directory exists
	onePortDir := filepath.Join(yanet1Root, "autotest/units/001_one_port")
	if _, err := os.Stat(onePortDir); os.IsNotExist(err) {
		t.Skipf("yanet1 directory not found: %s", onePortDir)
	}

	// Get test filters from environment
	onlyTest := os.Getenv("ONLY_TEST")
	onlyStep := os.Getenv("ONLY_STEP")
	limitStr := os.Getenv("LIMIT")
	runAll := os.Getenv("RUN_ALL") == "1"

	limit := 5 // Default: run only 5 tests
	if runAll {
		limit = 0 // No limit
	} else if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	// Discover tests
	entries, err := os.ReadDir(onePortDir)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", onePortDir, err)
	}

	testCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		testName := entry.Name()

		// Apply test filter
		if onlyTest != "" && testName != onlyTest {
			continue
		}

		testDir := filepath.Join(onePortDir, testName)
		autotestPath := filepath.Join(testDir, "autotest.yaml")

		if _, err := os.Stat(autotestPath); os.IsNotExist(err) {
			continue
		}

		// Apply limit
		if limit > 0 && testCount >= limit {
			break
		}
		testCount++

		t.Run(testName, func(t *testing.T) {
			runTestEquivalence(t, testDir, testName, onlyStep)
		})
	}

	if testCount == 0 {
		t.Skip("No tests found to run")
	}
}

func runTestEquivalence(t *testing.T, testDir, testName, onlyStep string) {
	// Read autotest.yaml
	autotestPath := filepath.Join(testDir, "autotest.yaml")
	data, err := os.ReadFile(autotestPath)
	if err != nil {
		t.Fatalf("Failed to read autotest.yaml: %v", err)
	}

	var test struct {
		Steps []map[string]interface{} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(data, &test); err != nil {
		t.Fatalf("Failed to parse autotest.yaml: %v", err)
	}

	// Process sendPackets steps
	stepIndex := 0
	for _, step := range test.Steps {
		for stepType, content := range step {
			if stepType != "sendPackets" {
				continue
			}

			stepIndex++
			stepName := fmt.Sprintf("Step_%03d", stepIndex)

			// Apply step filter
			if onlyStep != "" && stepName != fmt.Sprintf("Step_%s", onlyStep) {
				continue
			}

			t.Run(stepName, func(t *testing.T) {
				packets, ok := content.([]interface{})
				if !ok {
					t.Skip("Invalid sendPackets format")
					return
				}

				for _, pkt := range packets {
					sendFile, expectFile := parseSendExpectFiles(pkt)
					if sendFile == "" {
						continue
					}

					// Test send packets
					sendPath := filepath.Join(testDir, sendFile)
					if _, err := os.Stat(sendPath); err == nil {
						t.Run(sendFile, func(t *testing.T) {
							verifyPCAPEquivalence(t, sendPath, false)
						})
					}

					// Test expect packets
					if expectFile != "" {
						expectPath := filepath.Join(testDir, expectFile)
						if _, err := os.Stat(expectPath); err == nil {
							t.Run(expectFile, func(t *testing.T) {
								verifyPCAPEquivalence(t, expectPath, true)
							})
						}
					}
				}
			})
		}
	}
}

func parseSendExpectFiles(packet interface{}) (sendFile, expectFile string) {
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

func verifyPCAPEquivalence(t *testing.T, pcapPath string, isExpect bool) {
	// Read original PCAP
	originalPackets, err := readPCAPBytes(pcapPath)
	if err != nil {
		t.Fatalf("Failed to read original PCAP: %v", err)
	}

	if len(originalPackets) == 0 {
		t.Skip("Empty PCAP file")
		return
	}

	// Generate packets using converter pipeline
	opts := CodegenOpts{
		UseFrameworkMACs: true,
		IsExpect:         isExpect,
		StripVLAN:        false, // Never strip VLAN for this test
	}

	generatedPackets, err := GeneratePacketsFromPCAP(pcapPath, opts)
	if err != nil {
		t.Fatalf("Failed to generate packets: %v", err)
	}

	if len(originalPackets) != len(generatedPackets) {
		t.Fatalf("Packet count mismatch: original=%d, generated=%d",
			len(originalPackets), len(generatedPackets))
	}

	// Compare each packet
	for i := range originalPackets {
		t.Run(fmt.Sprintf("Packet_%d", i), func(t *testing.T) {
			original := originalPackets[i]
			generated := generatedPackets[i]

			// Primary comparison: byte-for-byte
			if bytes.Equal(original, generated) {
				t.Logf("✓ Packet %d matches exactly (%d bytes)", i, len(original))
				return
			}

			// Mismatch detected - try adaptation check
			t.Logf("Packet %d differs (original=%d bytes, generated=%d bytes)",
				i, len(original), len(generated))

			// Apply framework mapping to original
			originalPkt := gopacket.NewPacket(original, layers.LayerTypeEthernet, gopacket.Default)
			adapted, err := ApplyFrameworkMapping(originalPkt, isExpect)
			if err != nil {
				t.Errorf("Failed to apply framework mapping: %v", err)
				printPacketDiff(t, original, generated, i)
				return
			}

			// Compare adapted vs generated
			if bytes.Equal(adapted, generated) {
				t.Logf("✓ Packet %d matches after MAC/IP adaptation", i)
				return
			}

			// Still differs - report detailed mismatch
			t.Errorf("Packet %d mismatch even after adaptation", i)
			printPacketDiff(t, original, generated, i)
			printPacketDiff(t, adapted, generated, i)
		})
	}
}

func readPCAPBytes(pcapPath string) ([][]byte, error) {
	handle, err := pcap.OpenOffline(pcapPath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	var packets [][]byte
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	for packet := range packetSource.Packets() {
		packets = append(packets, packet.Data())
	}

	return packets, nil
}

func printPacketDiff(t *testing.T, expected, actual []byte, index int) {
	t.Logf("\n=== Packet %d Comparison ===", index)
	t.Logf("Expected (%d bytes):\n%s", len(expected), hex.Dump(expected))
	t.Logf("Actual (%d bytes):\n%s", len(actual), hex.Dump(actual))

	// Find first difference
	minLen := len(expected)
	if len(actual) < minLen {
		minLen = len(actual)
	}

	for i := 0; i < minLen; i++ {
		if expected[i] != actual[i] {
			t.Logf("First difference at byte %d: expected 0x%02x, got 0x%02x",
				i, expected[i], actual[i])
			break
		}
	}

	if len(expected) != len(actual) {
		t.Logf("Length difference: expected %d, got %d", len(expected), len(actual))
	}

	// Parse and compare layers
	expPkt := gopacket.NewPacket(expected, layers.LayerTypeEthernet, gopacket.Default)
	actPkt := gopacket.NewPacket(actual, layers.LayerTypeEthernet, gopacket.Default)

	t.Logf("\n=== Layer Comparison ===")
	diff := cmp.Diff(expPkt.Layers(), actPkt.Layers(),
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
		// NOTE: NOT ignoring BaseLayer - padding is significant
	)

	if diff != "" {
		t.Logf("Layer differences:\n%s", diff)
	}
}
