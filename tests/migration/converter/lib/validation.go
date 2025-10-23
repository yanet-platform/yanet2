package lib

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gopacket/gopacket"
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

// PacketValidator validates generated packets against PCAP files
type PacketValidator struct {
	verbose bool
}

// NewPacketValidator creates a new validator
func NewPacketValidator(verbose bool) *PacketValidator {
	return &PacketValidator{verbose: verbose}
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
		if err := v.validateSinglePacket(i, packets[i], expectedPackets[i], result); err != nil {
			if v.verbose {
				fmt.Printf("Packet %d validation error: %v\n", i, err)
			}
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

	diff.WriteString(fmt.Sprintf("First difference at byte %d (0x%x)\n", firstDiff, firstDiff))

	// Show context around the difference (16 bytes before and after)
	start := firstDiff - 16
	if start < 0 {
		start = 0
	}
	end := firstDiff + 16
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
