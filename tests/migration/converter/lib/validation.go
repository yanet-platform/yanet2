package lib

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// ValidationResult represents the result of packet validation
type ValidationResult struct {
	TestName   string
	TotalTests int
	Passed     int
	Failed     int
	Errors     []ValidationError
}

// ValidationError represents a single validation error
type ValidationError struct {
	PacketIndex  int
	ErrorType    string
	Expected     []byte
	Got          []byte
	DetailedDiff string
}

// PacketValidator validates generated packets against reference PCAPs.
// It supports two comparison modes:
//   - raw byte mode (compareByLayer == false): strict byte-for-byte matching,
//     useful when debugging builder or serializer issues;
//   - layer-aware mode (compareByLayer == true): compares projected semantic
//     fields per layer and tolerates benign differences such as Ethernet
//     padding or checksum-only changes.
type PacketValidator struct {
	verbose        bool
	compareByLayer bool
}

// NewPacketValidator creates a new validator
func NewPacketValidator(verbose bool) *PacketValidator {
	return &PacketValidator{verbose: verbose}
}

// NewPacketValidatorWithMode creates a new validator with explicit comparison mode.
// When compareByLayer is true, the validator will compare packets semantically
// using layer projections (ignoring padding and checksum-only differences).
func NewPacketValidatorWithMode(verbose, compareByLayer bool) *PacketValidator {
	return &PacketValidator{
		verbose:        verbose,
		compareByLayer: compareByLayer,
	}
}

// ValidateAgainstPCAP validates generated packets against a PCAP file
func (v *PacketValidator) ValidateAgainstPCAP(packets []gopacket.Packet, pcapPath string) (*ValidationResult, error) {
	result := &ValidationResult{
		TestName: filepath.Base(pcapPath),
	}

	// Check if PCAP file exists
	if _, err := os.Stat(pcapPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("PCAP file does not exist: %s", pcapPath)
	}

	// Check if file is empty
	fileInfo, err := os.Stat(pcapPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat PCAP file: %w", err)
	}

	if fileInfo.Size() == 0 {
		// Empty PCAP - expect no packets
		if len(packets) == 0 {
			result.Passed = 1
			result.TotalTests = 1
			return result, nil
		}
		result.Failed = 1
		result.TotalTests = 1
		result.Errors = append(result.Errors, ValidationError{
			ErrorType:    "count_mismatch",
			DetailedDiff: fmt.Sprintf("Expected 0 packets (empty PCAP), got %d packets", len(packets)),
		})
		return result, nil
	}

	// Open PCAP file
	handle, err := pcap.OpenOffline(pcapPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open PCAP file: %w", err)
	}
	defer handle.Close()

	// Read expected packets from PCAP
	expectedPackets := make([]gopacket.Packet, 0)
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for pkt := range packetSource.Packets() {
		expectedPackets = append(expectedPackets, pkt)
	}

	result.TotalTests = len(expectedPackets)

	// Check packet count
	if len(packets) != len(expectedPackets) {
		result.Failed++
		result.Errors = append(result.Errors, ValidationError{
			ErrorType: "count_mismatch",
			DetailedDiff: fmt.Sprintf("Packet count mismatch: expected %d, got %d",
				len(expectedPackets), len(packets)),
		})
		// Continue to validate as many as possible
	}

	// Validate each packet byte-by-byte
	minCount := len(packets)
	if len(expectedPackets) < minCount {
		minCount = len(expectedPackets)
	}

	for i := 0; i < minCount; i++ {
		var err error
		if v.compareByLayer {
			err = v.validateSinglePacketByLayers(i, packets[i], expectedPackets[i], result)
		} else {
			err = v.validateSinglePacket(i, packets[i], expectedPackets[i], result)
		}
		if err != nil && v.verbose {
			fmt.Printf("Packet %d validation error: %v\n", i, err)
		}
	}

	return result, nil
}

// validateSinglePacket validates a single packet byte-by-byte
func (v *PacketValidator) validateSinglePacket(index int, got, expected gopacket.Packet, result *ValidationResult) error {
	gotData := got.Data()
	expectedData := expected.Data()

	// Compare lengths
	if len(gotData) != len(expectedData) {
		result.Failed++
		result.Errors = append(result.Errors, ValidationError{
			PacketIndex: index,
			ErrorType:   "length_mismatch",
			Expected:    expectedData,
			Got:         gotData,
			DetailedDiff: fmt.Sprintf("Length mismatch: expected %d bytes, got %d bytes",
				len(expectedData), len(gotData)),
		})
		return fmt.Errorf("length mismatch")
	}

	// Byte-by-byte comparison
	if !bytes.Equal(gotData, expectedData) {
		result.Failed++

		// Find first difference
		firstDiff := -1
		for i := 0; i < len(gotData); i++ {
			if gotData[i] != expectedData[i] {
				firstDiff = i
				break
			}
		}

		// Create detailed diff
		diff := v.createDetailedDiff(gotData, expectedData, firstDiff)

		result.Errors = append(result.Errors, ValidationError{
			PacketIndex:  index,
			ErrorType:    "byte_mismatch",
			Expected:     expectedData,
			Got:          gotData,
			DetailedDiff: diff,
		})
		return fmt.Errorf("byte mismatch at offset %d", firstDiff)
	}

	result.Passed++
	return nil
}

// createDetailedDiff creates a human-readable diff of two byte arrays
func (v *PacketValidator) createDetailedDiff(got, expected []byte, firstDiff int) string {
	var diff bytes.Buffer

	// Validate firstDiff to prevent index out of range panic
	if firstDiff < 0 || firstDiff >= len(expected) || firstDiff >= len(got) {
		return fmt.Sprintf("Invalid diff offset: %d (expected len=%d, got len=%d)",
			firstDiff, len(expected), len(got))
	}

	diff.WriteString(fmt.Sprintf("First difference at byte %d (0x%x)\n", firstDiff, firstDiff))

	// Show context around the difference (HexDumpContextBytes before and after)
	start := firstDiff - HexDumpContextBytes
	if start < 0 {
		start = 0
	}
	end := firstDiff + HexDumpContextBytes
	if end > len(expected) {
		end = len(expected)
	}
	if end > len(got) {
		end = len(got)
	}

	diff.WriteString(fmt.Sprintf("\nExpected [%d:%d]:\n", start, end))
	diff.WriteString(fmt.Sprintf("  %s\n", formatHexDump(expected[start:end], start)))

	diff.WriteString(fmt.Sprintf("\nGot [%d:%d]:\n", start, end))
	diff.WriteString(fmt.Sprintf("  %s\n", formatHexDump(got[start:end], start)))

	// Show byte-by-byte comparison at the difference
	diff.WriteString(fmt.Sprintf("\nAt offset %d:\n", firstDiff))
	diff.WriteString(fmt.Sprintf("  Expected: 0x%02x (%d)\n", expected[firstDiff], expected[firstDiff]))
	diff.WriteString(fmt.Sprintf("  Got:      0x%02x (%d)\n", got[firstDiff], got[firstDiff]))

	return diff.String()
}

// validateSinglePacketByLayers validates a single packet using semantic,
// layer-based comparison. It first tolerates Ethernet padding differences,
// then compares projected semantic fields with cmp.Diff.
func (v *PacketValidator) validateSinglePacketByLayers(index int, got, expected gopacket.Packet, result *ValidationResult) error {
	gotData := got.Data()
	expectedData := expected.Data()

	// Fast path: exact byte match
	if bytes.Equal(gotData, expectedData) {
		result.Passed++
		return nil
	}

	// Allow Ethernet padding differences (e.g., original < 60 bytes, generated == 60)
	if bytesEqualIgnorePadding(expectedData, gotData) {
		result.Passed++
		return nil
	}

	// Semantic comparison via layer projection
	expProj := projectPacketForDiff(expected)
	gotProj := projectPacketForDiff(got)

	if diff := cmp.Diff(expProj, gotProj); diff != "" {
		result.Failed++
		result.Errors = append(result.Errors, ValidationError{
			PacketIndex:  index,
			ErrorType:    "semantic_mismatch",
			Expected:     expectedData,
			Got:          gotData,
			DetailedDiff: diff,
		})
		return fmt.Errorf("semantic mismatch for packet %d", index)
	}

	result.Passed++
	return nil
}

// projectPacketForDiff extracts only key semantic fields per layer for stable cmp.Diff.
// This intentionally ignores checksum-only differences and low-level serialization details.
func projectPacketForDiff(pkt gopacket.Packet) []interface{} {
	var out []interface{}
	for _, l := range pkt.Layers() {
		switch v := l.(type) {
		case *layers.Ethernet:
			out = append(out, struct {
				Type layers.EthernetType
			}{Type: v.EthernetType})
		case *layers.Dot1Q:
			out = append(out, struct {
				VLAN uint16
				Type layers.EthernetType
			}{VLAN: v.VLANIdentifier, Type: v.Type})
		case *layers.IPv4:
			out = append(out, struct {
				Src, Dst string
				TTL      uint8
				Proto    layers.IPProtocol
			}{Src: v.SrcIP.String(), Dst: v.DstIP.String(), TTL: v.TTL, Proto: v.Protocol})
		case *layers.IPv6:
			out = append(out, struct {
				Src, Dst string
				Hop      uint8
				NH       layers.IPProtocol
			}{Src: v.SrcIP.String(), Dst: v.DstIP.String(), Hop: v.HopLimit, NH: v.NextHeader})
		case *layers.TCP:
			flags := struct {
				SYN, ACK, FIN, RST, PSH, URG bool
			}{v.SYN, v.ACK, v.FIN, v.RST, v.PSH, v.URG}
			out = append(out, struct {
				Src, Dst uint16
				Flags    interface{}
			}{Src: uint16(v.SrcPort), Dst: uint16(v.DstPort), Flags: flags})
		case *layers.UDP:
			out = append(out, struct {
				Src, Dst uint16
			}{Src: uint16(v.SrcPort), Dst: uint16(v.DstPort)})
		case *layers.ICMPv6:
			out = append(out, struct {
				Type uint8
				Code uint8
			}{Type: uint8(v.TypeCode.Type()), Code: uint8(v.TypeCode.Code())})
		case *layers.ICMPv4:
			out = append(out, struct {
				Type uint8
				Code uint8
			}{Type: uint8(v.TypeCode.Type()), Code: uint8(v.TypeCode.Code())})
		case *layers.MPLS:
			out = append(out, struct {
				Label      uint32
				TTL        uint8
				StackBit   bool
				TrafficCls uint8
			}{Label: v.Label, TTL: v.TTL, StackBit: v.StackBottom, TrafficCls: v.TrafficClass})
		case *layers.ARP:
			out = append(out, struct {
				Operation    uint16
				SrcHwAddr    string
				SrcProtoAddr string
				DstHwAddr    string
				DstProtoAddr string
			}{
				Operation:    v.Operation,
				SrcHwAddr:    net.HardwareAddr(v.SourceHwAddress).String(),
				SrcProtoAddr: net.IP(v.SourceProtAddress).String(),
				DstHwAddr:    net.HardwareAddr(v.DstHwAddress).String(),
				DstProtoAddr: net.IP(v.DstProtAddress).String(),
			})
		default:
			// Ignore other layers in validator
		}
	}
	return out
}

// bytesEqualIgnorePadding compares two byte slices, ignoring trailing zero bytes (Ethernet padding).
func bytesEqualIgnorePadding(a, b []byte) bool {
	isValidPadding := len(a) < 60 && len(b) == 60
	if !isValidPadding {
		return false
	}
	// Find the actual content length (without trailing zeros) for both
	aLen := len(a)
	for aLen > 0 && a[aLen-1] == 0 {
		aLen--
	}

	bLen := len(b)
	for bLen > 0 && b[bLen-1] == 0 {
		bLen--
	}

	// Compare the actual content (without padding)
	if aLen != bLen {
		return false
	}

	for i := 0; i < aLen; i++ {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// formatHexDump formats bytes as a hex dump
func formatHexDump(data []byte, offset int) string {
	var buf bytes.Buffer

	for i := 0; i < len(data); i += 16 {
		// Offset
		buf.WriteString(fmt.Sprintf("%04x: ", offset+i))

		// Hex bytes
		for j := 0; j < 16; j++ {
			if i+j < len(data) {
				buf.WriteString(fmt.Sprintf("%02x ", data[i+j]))
			} else {
				buf.WriteString("   ")
			}
			if j == 7 {
				buf.WriteString(" ")
			}
		}

		// ASCII representation
		buf.WriteString(" |")
		for j := 0; j < 16 && i+j < len(data); j++ {
			b := data[i+j]
			if b >= 32 && b < 127 {
				buf.WriteByte(b)
			} else {
				buf.WriteByte('.')
			}
		}
		buf.WriteString("|")

		if i+16 < len(data) {
			buf.WriteString("\n  ")
		}
	}

	return buf.String()
}

// ValidateTestDirectory validates all PCAP pairs in a test directory
func (v *PacketValidator) ValidateTestDirectory(testDir string, generateFunc func(string) ([]gopacket.Packet, error)) (*ValidationResult, error) {
	result := &ValidationResult{
		TestName: filepath.Base(testDir),
	}

	// Find all PCAP files in directory
	pcapFiles, err := filepath.Glob(filepath.Join(testDir, "*.pcap"))
	if err != nil {
		return nil, fmt.Errorf("failed to find PCAP files: %w", err)
	}

	for _, pcapFile := range pcapFiles {
		// Generate packets for this PCAP
		packets, err := generateFunc(pcapFile)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ValidationError{
				ErrorType:    "generation_error",
				DetailedDiff: fmt.Sprintf("Failed to generate packets: %v", err),
			})
			continue
		}

		// Validate against PCAP
		pcapResult, err := v.ValidateAgainstPCAP(packets, pcapFile)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ValidationError{
				ErrorType:    "validation_error",
				DetailedDiff: fmt.Sprintf("Failed to validate %s: %v", filepath.Base(pcapFile), err),
			})
			continue
		}

		// Merge results
		result.TotalTests += pcapResult.TotalTests
		result.Passed += pcapResult.Passed
		result.Failed += pcapResult.Failed
		result.Errors = append(result.Errors, pcapResult.Errors...)
	}

	return result, nil
}

// PrintReport prints a validation report
func (v *ValidationResult) PrintReport() {
	fmt.Printf("\n=== Validation Report for %s ===\n", v.TestName)
	fmt.Printf("Total packets: %d\n", v.TotalTests)
	fmt.Printf("Passed: %d (%.1f%%)\n", v.Passed, float64(v.Passed)/float64(v.TotalTests)*100)
	fmt.Printf("Failed: %d (%.1f%%)\n", v.Failed, float64(v.Failed)/float64(v.TotalTests)*100)

	if len(v.Errors) > 0 {
		fmt.Printf("\n=== Errors ===\n")
		for i, err := range v.Errors {
			if i >= 10 {
				fmt.Printf("... and %d more errors\n", len(v.Errors)-10)
				break
			}
			fmt.Printf("\nError %d:\n", i+1)
			if err.PacketIndex >= 0 {
				fmt.Printf("  Packet: %d\n", err.PacketIndex)
			}
			fmt.Printf("  Type: %s\n", err.ErrorType)
			fmt.Printf("  Details:\n%s\n", err.DetailedDiff)
		}
	}

	fmt.Println()
}

// IsSuccess returns true if all tests passed
func (v *ValidationResult) IsSuccess() bool {
	return v.Failed == 0 && v.TotalTests > 0
}
