package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// TestGenPyEquivalence verifies that packets generated from gen.py AST match original PCAP files.
// This test parses gen.py files using scapy_ast_parser.py, generates packets from the IR,
// and compares them byte-by-byte with the original PCAP files.
//
// This test requires yanet1 repository to be available.
// Set YANET1_ROOT environment variable to point to yanet1 directory.
// Example: export YANET1_ROOT=/path/to/yanet1
func TestGenPyEquivalence(t *testing.T) {
	// Get test filter from environment
	onlyTest := os.Getenv("ONLY_TEST")

	if onlyTest == "" {
		t.Skip("ast parser variant not completed use ONLY_TEST environment variable to run a specific test")
		return
	}

	onePortDir, err := GetYanet1OnePortDir()
	if err != nil {
		t.Skipf("Failed to get one port directory (yanet1 repository not available): %v", err)
		return
	}

	// Discover tests with gen.py files
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
		genPyPath := filepath.Join(testDir, "gen.py")

		// Skip tests without gen.py
		if _, err := os.Stat(genPyPath); os.IsNotExist(err) {
			continue
		}

		testCount++

		t.Run(testName, func(t *testing.T) {
			verifyGenPyEquivalence(t, testDir, genPyPath)
		})
	}

	if testCount == 0 {
		t.Skip("No tests with gen.py found to run")
	}

	t.Logf("Validated %d tests with gen.py files", testCount)
}

// verifyGenPyEquivalence verifies that packets generated from gen.py match the original PCAP files
func verifyGenPyEquivalence(t *testing.T, testDir, genPyPath string) {
	// Generate packets from gen.py using AST parser
	generatedPackets, err := GeneratePacketsFromGenPy(genPyPath, CodegenOpts{
		UseFrameworkMACs: false,
		StripVLAN:        false,
	})
	if err != nil {
		t.Fatalf("Failed to generate packets from gen.py: %v", err)
	}

	if len(generatedPackets) == 0 {
		t.Skip("No PCAP pairs generated from gen.py")
		return
	}

	// Verify each PCAP file
	for pcapFile, packets := range generatedPackets {
		pcapPath := filepath.Join(testDir, pcapFile)

		// Check if PCAP file exists
		if _, err := os.Stat(pcapPath); os.IsNotExist(err) {
			t.Logf("PCAP file not found: %s (skipping)", pcapFile)
			continue
		}

		t.Run(pcapFile, func(t *testing.T) {
			verifyGenPyPackets(t, pcapPath, packets)
		})
	}
}

// verifyGenPyPackets compares generated packets with original PCAP file
func verifyGenPyPackets(t *testing.T, pcapPath string, generatedPackets [][]byte) {
	// Read original PCAP
	originalPackets, err := readPCAPBytes(pcapPath)
	if err != nil {
		t.Fatalf("Failed to read original PCAP: %v", err)
	}

	if len(originalPackets) == 0 {
		t.Skip("Empty PCAP file")
		return
	}

	// Compare packet counts
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
				t.Logf("Packet %d matches exactly (%d bytes)", i, len(original))
				return
			}

			// Secondary comparison: ignore trailing zero padding (Ethernet min frame)
			if bytesEqualIgnoringPadding(original, generated) {
				t.Logf("Packet %d matches ignoring Ethernet padding (expected=%d, actual=%d)",
					i, len(original), len(generated))
				return
			}

			// Tertiary comparison: ignore MAC-only differences
			if packetsMatchIgnoringMACs(original, generated) {
				t.Logf("Packet %d matches after ignoring MAC differences", i)
				return
			}

			// Still differs - report detailed mismatch
			t.Errorf("Packet %d mismatch (non-MAC layer differences)", i)
			printPacketDiff(t, original, generated, i)
		})
	}
}

// GeneratePacketsFromGenPy parses gen.py file and generates packets from IR
// Returns a map of PCAP filename -> packet bytes
func GeneratePacketsFromGenPy(genPyPath string, opts CodegenOpts) (map[string][][]byte, error) {
	// Run Python AST parser
	scapyASTParser := filepath.Join(filepath.Dir(genPyPath), "../../scapy_ast_parser.py")

	// Try relative path from test directory
	if _, err := os.Stat(scapyASTParser); os.IsNotExist(err) {
		// Try from lib directory
		scapyASTParser = "../scapy_ast_parser.py"
	}

	cmd := exec.Command("python3", scapyASTParser, genPyPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Python AST parser failed: %w\nOutput: %s", err, string(output))
	}

	// Parse IR JSON
	var ir IRJSON
	if err := json.Unmarshal(output, &ir); err != nil {
		return nil, fmt.Errorf("failed to parse IR JSON: %w\nJSON: %s", err, string(output))
	}

	if len(ir.PCAPPairs) == 0 {
		return nil, fmt.Errorf("no PCAP pairs found in IR")
	}

	// Generate packets for each PCAP pair
	result := make(map[string][][]byte)

	for _, pair := range ir.PCAPPairs {
		// Generate send packets
		if len(pair.SendPackets) > 0 && pair.SendFile != "" {
			var sendPackets [][]byte
			for _, irPkt := range pair.SendPackets {
				pktBytes, err := generatePacketFromIR(irPkt, opts)
				if err != nil {
					return nil, fmt.Errorf("failed to generate send packet from IR: %w", err)
				}
				sendPackets = append(sendPackets, pktBytes)
			}
			result[pair.SendFile] = sendPackets
		}

		// Generate expect packets
		if len(pair.ExpectPackets) > 0 && pair.ExpectFile != "" {
			var expectPackets [][]byte
			expectOpts := opts
			expectOpts.IsExpect = true
			for _, irPkt := range pair.ExpectPackets {
				pktBytes, err := generatePacketFromIR(irPkt, expectOpts)
				if err != nil {
					return nil, fmt.Errorf("failed to generate expect packet from IR: %w", err)
				}
				expectPackets = append(expectPackets, pktBytes)
			}
			result[pair.ExpectFile] = expectPackets
		}
	}

	return result, nil
}

// bytesEqualIgnoringPadding compares byte slices ignoring trailing zero padding
func bytesEqualIgnoringPadding(a, b []byte) bool {
	// Ensure a is the shorter (expected), b is the longer (actual)
	if len(a) > len(b) {
		a, b = b, a
	}
	// Compare common prefix
	if !bytes.Equal(a, b[:len(a)]) {
		return false
	}
	// Remaining bytes in the longer slice must be zeros
	for _, v := range b[len(a):] {
		if v != 0x00 {
			return false
		}
	}
	return true
}

// packetsMatchIgnoringMACs compares packets ignoring MAC address differences
func packetsMatchIgnoringMACs(expected, actual []byte) bool {
	expPkt := gopacket.NewPacket(expected, layers.LayerTypeEthernet, gopacket.Default)
	actPkt := gopacket.NewPacket(actual, layers.LayerTypeEthernet, gopacket.Default)

	projectLayers := cmp.Transformer("projectLayers", func(in []gopacket.Layer) []any {
		out := make([]any, len(in))
		for idx, layer := range in {
			switch l := layer.(type) {
			case *layers.Ethernet:
				ethCopy := *l
				ethCopy.SrcMAC = nil
				ethCopy.DstMAC = nil
				if len(ethCopy.BaseLayer.Contents) >= 12 {
					contentsCopy := make([]byte, len(ethCopy.BaseLayer.Contents))
					copy(contentsCopy, ethCopy.BaseLayer.Contents)
					for j := 0; j < 12; j++ {
						contentsCopy[j] = 0
					}
					ethCopy.BaseLayer.Contents = contentsCopy
				}
				out[idx] = &ethCopy
			case *layers.IPv6HopByHop:
				out[idx] = struct{ Contents, Payload []byte }{l.BaseLayer.Contents, l.BaseLayer.Payload}
			case *layers.IPv6Routing:
				out[idx] = struct{ Contents, Payload []byte }{l.BaseLayer.Contents, l.BaseLayer.Payload}
			case *layers.IPv6Destination:
				out[idx] = struct{ Contents, Payload []byte }{l.BaseLayer.Contents, l.BaseLayer.Payload}
			default:
				out[idx] = layer
			}
		}
		return out
	})

	// Combine standard options with custom projection
	opts := append(CmpStdOpts, projectLayers)
	diff := cmp.Diff(expPkt.Layers(), actPkt.Layers(), opts...)

	return diff == ""
}
