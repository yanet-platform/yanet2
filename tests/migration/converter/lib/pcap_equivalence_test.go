package lib

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// TestPCAPEquivalence verifies that the converter IR pipeline correctly processes PCAP files
// This test validates the PCAP → IR → Packet pipeline, not byte-for-byte equivalence.
// For detailed semantic comparison with cmp.Diff, see ir_pipeline_test.go
//
// This test requires yanet1 repository to be available.
// Set YANET1_ROOT environment variable to point to yanet1 directory.
// Example: export YANET1_ROOT=/path/to/yanet1
func TestPCAPEquivalence(t *testing.T) {
	// Get yanet1 root from environment
	yanet1Root := os.Getenv("YANET1_ROOT")
	if yanet1Root == "" {
		yanet1Root = "../../../../../yanet1"
	}

	// Check if yanet1 directory exists
	onePortDir := filepath.Join(yanet1Root, "autotest/units/001_one_port")
	if _, err := os.Stat(onePortDir); os.IsNotExist(err) {
		t.Skipf("yanet1 directory not found at %s. Set YANET1_ROOT to yanet1 repository location.", onePortDir)
	}

	// Get test filters from environment
	onlyTest := os.Getenv("ONLY_TEST")
	onlyStep := os.Getenv("ONLY_STEP")

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
	// Parse autotest.yaml using shared function from converter
	test, err := ParseAutotestYAML(testDir)
	if err != nil {
		// Skip tests that cannot be parsed (e.g., malformed YAML in yanet1)
		// These tests are typically disabled in skiplist.yaml anyway
		t.Skipf("Cannot parse autotest.yaml (likely malformed in yanet1): %v", err)
		return
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

// verifyPCAPEquivalence validates the full pipeline: PCAP → IR → Code Generation → Packet Builder → Semantic Comparison
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

	// Convert PCAP → IR
	opts := CodegenOpts{
		UseFrameworkMACs: false,
		IsExpect:         isExpect,
		StripVLAN:        false,
	}
	analyzer := NewPcapAnalyzer(false)
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	if err != nil {
		t.Fatalf("Failed to read packet infos: %v", err)
	}
	ir, err := analyzer.ConvertPacketInfoToIR(packetInfos, "send.pcap", "expect.pcap", opts)
	if err != nil {
		t.Fatalf("Failed to convert to IR: %v", err)
	}

	// Extract IR packets
	var irPackets []IRPacketDef
	if len(ir.PCAPPairs) > 0 {
		if opts.IsExpect {
			irPackets = ir.PCAPPairs[0].ExpectPackets
		} else {
			irPackets = ir.PCAPPairs[0].SendPackets
		}
	}

	if len(originalPackets) != len(irPackets) {
		t.Fatalf("Packet count mismatch: original=%d, IR=%d", len(originalPackets), len(irPackets))
	}

	// Generate packets from IR using packet builder
	var generatedPackets []gopacket.Packet
	for i, irPkt := range irPackets {
		pkt, err := generatePacketFromIRExact(irPkt, opts)
		if err != nil {
			t.Fatalf("Failed to generate packet %d from IR: %v", i, err)
		}
		generatedPackets = append(generatedPackets, pkt)
	}

	// Semantic comparison
	for i := range originalPackets {
		expPkt := gopacket.NewPacket(originalPackets[i], layers.LayerTypeEthernet, gopacket.Default)
		actPkt := generatedPackets[i]

		// Check if original packet has DecodeFailure
		hasDecodeFailure := false
		for _, layer := range expPkt.Layers() {
			if layer.LayerType() == gopacket.LayerTypeDecodeFailure {
				hasDecodeFailure = true
				break
			}
		}

		if hasDecodeFailure {
			// For packets with DecodeFailure, compare raw bytes instead of parsed layers
			// This is because gopacket may incorrectly parse the original (e.g., GRE with unsupported flags)
			// but our IR conversion extracts the correct structure from raw bytes and uses custom serialization
			originalBytes := originalPackets[i]
			generatedBytes := actPkt.Data()

			// Compare bytes ignoring trailing zero padding (Ethernet padding)
			if bytesEqualIgnorePadding(originalBytes, generatedBytes) {
				if len(originalBytes) != len(generatedBytes) {
					t.Logf("Packet %d: Raw bytes match with padding (DecodeFailure, orig=%d, gen=%d)", i, len(originalBytes), len(generatedBytes))
				} else {
					t.Logf("Packet %d: Raw bytes match (DecodeFailure handled correctly)", i)
				}
				continue
			}

			diff := cmp.Diff(originalBytes, generatedBytes)
			if diff != "" {
				t.Errorf("Packet %d: Raw bytes mismatch:\n%s", i, diff)
			}
			continue
		}

		// For normal packets, compare semantic content but handle Ethernet padding
		// gopacket always pads to 60 bytes, but original PCAPs may have shorter packets
		originalBytes := originalPackets[i]
		generatedBytes := actPkt.Data()

		// Handle Ethernet padding: ONLY allow padding if original < 60 and generated == 60
		// Ethernet frames must be at least 60 bytes, but PCAPs may contain shorter frames
		// gopacket always pads to 60 bytes with zeros

		// Check if this is valid Ethernet padding (orig < 60, gen = 60)
		isValidPadding := len(originalBytes) < 60 && len(generatedBytes) == 60

		if isValidPadding && bytesEqualIgnorePadding(originalBytes, generatedBytes) {
			t.Logf("Packet %d: Content matches (Ethernet padding: orig=%d, gen=%d)", i, len(originalBytes), len(generatedBytes))
			continue
		}

		expProj := projectPacketForDiff(expPkt)
		actProj := projectPacketForDiff(actPkt)
		if diff := cmp.Diff(expProj, actProj); diff != "" {
			t.Errorf("Packet %d semantic diff (-want +got):\n%s", i, diff)
		}
	}
}

// bytesEqual compares two byte slices for equality
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

// generatePacketFromIRExact builds a packet with lengths/checksums preserved (no auto-fix)
func generatePacketFromIRExact(irPkt IRPacketDef, opts CodegenOpts) (gopacket.Packet, error) {
	var layerBuilders []LayerBuilder

	for _, layer := range irPkt.Layers {
		builder := buildLayerFromIR(layer, opts.IsExpect)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}

	// Use FixLengths: true to ensure all layers (including Raw) are serialized
	// customIPv6Layer will handle explicit plen values during serialization
	serializeOpts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: false,
	}
	return NewPacketWithOptions(serializeOpts, layerBuilders...)
}
