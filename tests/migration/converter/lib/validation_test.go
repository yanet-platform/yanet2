package lib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/stretchr/testify/require"
)

func TestValidation_EmptyPCAP(t *testing.T) {
	// Create empty PCAP file
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "empty.pcap")

	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	f.Close()

	validator := NewPacketValidator(false)

	// Test with no packets - should pass
	result, err := validator.ValidateAgainstPCAP([]gopacket.Packet{}, pcapPath)
	require.NoError(t, err)
	require.True(t, result.IsSuccess())
	require.Equal(t, 1, result.Passed)

	// Test with packets - should fail
	pkt, _ := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
	)
	result, err = validator.ValidateAgainstPCAP([]gopacket.Packet{pkt}, pcapPath)
	require.NoError(t, err)
	require.False(t, result.IsSuccess())
	require.Equal(t, 1, result.Failed)
}

func TestValidation_ByteByByte(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "test.pcap")

	// Create a packet
	expectedPkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8"), IPTTL(64)),
		TCP(TCPSport(1234), TCPDport(80)),
	)
	require.NoError(t, err)

	// Write to PCAP
	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	err = writer.WritePacket(gopacket.CaptureInfo{
		CaptureLength: len(expectedPkt.Data()),
		Length:        len(expectedPkt.Data()),
	}, expectedPkt.Data())
	require.NoError(t, err)
	f.Close()

	validator := NewPacketValidator(true)

	// Test with identical packet - should pass
	gotPkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8"), IPTTL(64)),
		TCP(TCPSport(1234), TCPDport(80)),
	)
	require.NoError(t, err)

	result, err := validator.ValidateAgainstPCAP([]gopacket.Packet{gotPkt}, pcapPath)
	require.NoError(t, err)
	require.True(t, result.IsSuccess(), "Validation should pass for identical packets")
	require.Equal(t, 1, result.Passed)
	require.Equal(t, 0, result.Failed)
}

func TestValidation_ByteMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "test.pcap")

	// Create expected packet
	expectedPkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8"), IPTTL(64)),
		TCP(TCPSport(1234), TCPDport(80)),
	)
	require.NoError(t, err)

	// Write to PCAP
	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	err = writer.WritePacket(gopacket.CaptureInfo{
		CaptureLength: len(expectedPkt.Data()),
		Length:        len(expectedPkt.Data()),
	}, expectedPkt.Data())
	require.NoError(t, err)
	f.Close()

	validator := NewPacketValidator(false)

	// Test with different packet - should fail
	gotPkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8"), IPTTL(63)), // Different TTL
		TCP(TCPSport(1234), TCPDport(80)),
	)
	require.NoError(t, err)

	result, err := validator.ValidateAgainstPCAP([]gopacket.Packet{gotPkt}, pcapPath)
	require.NoError(t, err)
	require.False(t, result.IsSuccess())
	require.Equal(t, 0, result.Passed)
	require.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	require.Equal(t, "byte_mismatch", result.Errors[0].ErrorType)
}

func TestValidation_CountMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "test.pcap")

	// Create 2 packets in PCAP
	pkt1, _ := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
	)
	pkt2, _ := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.5"), IPDst("5.6.7.9")),
	)

	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	for _, pkt := range []gopacket.Packet{pkt1, pkt2} {
		err = writer.WritePacket(gopacket.CaptureInfo{
			CaptureLength: len(pkt.Data()),
			Length:        len(pkt.Data()),
		}, pkt.Data())
		require.NoError(t, err)
	}
	f.Close()

	validator := NewPacketValidator(false)

	// Test with only 1 packet - should report count mismatch
	result, err := validator.ValidateAgainstPCAP([]gopacket.Packet{pkt1}, pcapPath)
	require.NoError(t, err)
	require.False(t, result.IsSuccess())
	require.Contains(t, result.Errors[0].DetailedDiff, "count mismatch")
}

func TestValidation_MultiplePackets(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "multi.pcap")

	// Create multiple packets
	packets := make([]gopacket.Packet, 0)
	for i := 0; i < 5; i++ {
		pkt, err := NewPacket(nil,
			Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
			IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8"), IPId(uint16(i))),
			UDP(UDPSport(uint16(1000+i)), UDPDport(80)),
		)
		require.NoError(t, err)
		packets = append(packets, pkt)
	}

	// Write to PCAP
	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	for _, pkt := range packets {
		err = writer.WritePacket(gopacket.CaptureInfo{
			CaptureLength: len(pkt.Data()),
			Length:        len(pkt.Data()),
		}, pkt.Data())
		require.NoError(t, err)
	}
	f.Close()

	validator := NewPacketValidator(false)

	// Validate - should pass
	result, err := validator.ValidateAgainstPCAP(packets, pcapPath)
	require.NoError(t, err)
	require.True(t, result.IsSuccess())
	require.Equal(t, 5, result.Passed)
	require.Equal(t, 0, result.Failed)
}

// Test that layer-aware validator tolerates Ethernet padding differences where
// the raw-byte validator reports a length mismatch.
func TestValidation_LayerMode_IgnoresEthernetPadding(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "padded.pcap")

	// Create a packet using the builder (this will typically serialize to >= 60 bytes)
	basePkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
		UDP(UDPSport(1234), UDPDport(80)),
	)
	require.NoError(t, err)

	raw := basePkt.Data()

	// Simulate original PCAP with shorter frame by trimming only trailing zero padding bytes.
	shortLen := len(raw)
	for shortLen > 0 && raw[shortLen-1] == 0 {
		shortLen--
	}
	require.Greater(t, len(raw), shortLen, "expected to trim at least some padding bytes")
	short := raw[:shortLen]

	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	err = writer.WritePacket(gopacket.CaptureInfo{
		CaptureLength: len(short),
		Length:        len(short),
	}, short)
	require.NoError(t, err)
	f.Close()

	// Byte-by-byte validator should report a length mismatch
	byteValidator := NewPacketValidator(false)
	byteResult, err := byteValidator.ValidateAgainstPCAP([]gopacket.Packet{basePkt}, pcapPath)
	require.NoError(t, err)
	require.False(t, byteResult.IsSuccess())
	require.Greater(t, byteResult.Failed, 0)

	// Layer-aware validator should treat this as success (padding-only difference)
	layerValidator := NewPacketValidatorWithMode(false, true)
	layerResult, err := layerValidator.ValidateAgainstPCAP([]gopacket.Packet{basePkt}, pcapPath)
	require.NoError(t, err)
	require.True(t, layerResult.IsSuccess())
	require.Equal(t, 1, layerResult.Passed)
	require.Equal(t, 0, layerResult.Failed)
}

// Test that layer-aware validator ignores checksum-only differences while
// byte-by-byte validator reports a mismatch.
func TestValidation_LayerMode_IgnoresChecksumDifferences(t *testing.T) {
	tmpDir := t.TempDir()
	pcapPath := filepath.Join(tmpDir, "checksum.pcap")

	// Create a base IPv4/TCP packet
	basePkt, err := NewPacket(nil,
		Ether(EtherDst("00:11:22:33:44:55"), EtherSrc("00:00:00:00:00:01")),
		IPv4(IPSrc("1.2.3.4"), IPDst("5.6.7.8")),
		TCP(TCPSport(1234), TCPDport(80)),
	)
	require.NoError(t, err)

	raw := append([]byte(nil), basePkt.Data()...)
	require.GreaterOrEqual(t, len(raw), 34, "expected IPv4 header to be present")

	// IPv4 checksum is at bytes 24-25 for a standard Ethernet+IPv4 packet (no options)
	csOffset := 14 + 10
	if csOffset+1 >= len(raw) {
		t.Skip("packet too short to safely mutate checksum")
	}
	raw[csOffset] ^= 0xFF
	raw[csOffset+1] ^= 0xFF

	f, err := os.Create(pcapPath)
	require.NoError(t, err)
	defer f.Close()

	writer := pcapgo.NewWriter(f)
	err = writer.WriteFileHeader(65536, layers.LinkTypeEthernet)
	require.NoError(t, err)

	err = writer.WritePacket(gopacket.CaptureInfo{
		CaptureLength: len(raw),
		Length:        len(raw),
	}, raw)
	require.NoError(t, err)
	f.Close()

	// Byte-by-byte validator should fail due to checksum bytes mismatch
	byteValidator := NewPacketValidator(false)
	byteResult, err := byteValidator.ValidateAgainstPCAP([]gopacket.Packet{basePkt}, pcapPath)
	require.NoError(t, err)
	require.False(t, byteResult.IsSuccess())
	require.Greater(t, byteResult.Failed, 0)

	// Layer-aware validator should succeed because projected semantics are unchanged
	layerValidator := NewPacketValidatorWithMode(false, true)
	layerResult, err := layerValidator.ValidateAgainstPCAP([]gopacket.Packet{basePkt}, pcapPath)
	require.NoError(t, err)
	require.True(t, layerResult.IsSuccess())
	require.Equal(t, 1, layerResult.Passed)
	require.Equal(t, 0, layerResult.Failed)
}
