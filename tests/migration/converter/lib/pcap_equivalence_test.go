package lib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"github.com/stretchr/testify/require"
)

// TestPCAPEquivalence verifies that the converter IR pipeline correctly processes PCAP files
// This test validates the PCAP → IR → Packet pipeline, not byte-for-byte equivalence.
// For detailed semantic comparison with cmp.Diff, see ir_pipeline_test.go
//
// This test requires yanet1 repository to be available.
// Set YANET1_ROOT environment variable to point to yanet1 directory.
// Example: export YANET1_ROOT=/path/to/yanet1
func TestPCAPEquivalence(t *testing.T) {
	onePortDir, err := GetYanet1OnePortDir()
	if err != nil {
		t.Skipf("Failed to get one port directory (yanet1 repository not available): %v", err)
		return
	}

	// Get test filters from environment
	onlyTest := os.Getenv("ONLY_TEST")
	onlyStep := os.Getenv("ONLY_STEP")

	// Skip tests with known issues (malformed autotest.yaml, no steps, etc.)
	skipTests := map[string]string{
		"056_balancer_icmp_rate_limit": "autotest.yaml has no steps",
		"059_rib":                      "autotest.yaml has YAML syntax error at line 1809",
	}

	// Discover tests
	tests, err := DiscoverTests(onePortDir, onlyTest, skipTests)
	if err != nil {
		t.Fatalf("Failed to discover tests: %v", err)
	}

	require.NotEmpty(t, tests, "No tests found to run")

	for _, testInfo := range tests {
		testInfo := testInfo // capture loop variable
		t.Run(testInfo.Name, func(t *testing.T) {
			runTestEquivalence(t, testInfo, onlyStep)
		})
	}
}

func runTestEquivalence(t *testing.T, testInfo TestInfo, onlyStep string) {
	err := IterateSendPacketsSteps(testInfo, onlyStep, func(stepInfo StepInfo, sendFile, expectFile string) error {
		t.Run(stepInfo.Name, func(t *testing.T) {
			// Test send packets
			sendPath := filepath.Join(testInfo.Dir, sendFile)
			if _, err := os.Stat(sendPath); err == nil {
				t.Run(sendFile, func(t *testing.T) {
					verifyPCAPEquivalence(t, sendPath, false)
				})
			}

			// Test expect packets
			if expectFile != "" {
				expectPath := filepath.Join(testInfo.Dir, expectFile)
				if _, err := os.Stat(expectPath); err == nil {
					t.Run(expectFile, func(t *testing.T) {
						verifyPCAPEquivalence(t, expectPath, true)
					})
				}
			}
		})
		return nil
	})
	require.NoError(t, err, "Failed to iterate send packets steps")
}

// verifyPCAPEquivalence validates the full pipeline: PCAP → IR → Code Generation → Packet Builder → Semantic Comparison
func verifyPCAPEquivalence(t *testing.T, pcapPath string, isExpect bool) {
	// Read and analyze PCAP → PacketInfo
	analyzer := NewPcapAnalyzer(false)
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	if err != nil {
		t.Fatalf("Failed to read packet infos: %v", err)
	}
	if len(packetInfos) == 0 {
		t.Skip("Empty PCAP file")
		return
	}

	// Convert PCAP → IR
	opts := CodegenOpts{
		UseFrameworkMACs: false,
		IsExpect:         isExpect,
		StripVLAN:        false,
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

	if len(packetInfos) != len(irPackets) {
		t.Fatalf("Packet count mismatch: original=%d, IR=%d", len(packetInfos), len(irPackets))
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
	for i, info := range packetInfos {
		originalBytes := info.RawData
		expPkt := gopacket.NewPacket(originalBytes, layers.LayerTypeEthernet, gopacket.Default)
		actPkt := generatedPackets[i]

		generatedBytes := actPkt.Data()

		if bytesEqualIgnorePadding(originalBytes, generatedBytes) {
			continue
		}

		diff := cmp.Diff(expPkt.Layers(), actPkt.Layers(), CmpStdOpts...)
		require.Emptyf(t, diff, "Packet layers mismatch for index %d", i)
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

// generatePacketFromIRExact builds a packet with lengths/checksums preserved (no auto-fix)
func generatePacketFromIRExact(irPkt IRPacketDef, opts CodegenOpts) (gopacket.Packet, error) {
	var layerBuilders []LayerBuilder

	for _, layer := range irPkt.Layers {
		builder := buildLayerFromIR(layer, opts.IsExpect)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}

	// Use FixLengths: false to preserve explicit length/checksum values from custom layers
	// Custom layers (customIPv4Layer, customIPv6Layer, etc.) handle lengths explicitly
	// Setting FixLengths: true would overwrite our explicit (potentially invalid) values
	serializeOpts := gopacket.SerializeOptions{
		FixLengths:       false,
		ComputeChecksums: false,
	}
	return NewPacket(&serializeOpts, layerBuilders...)
}
