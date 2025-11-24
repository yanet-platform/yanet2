package lib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func TestICMPv6PayloadConversion(t *testing.T) {
	// This test verifies that ICMP packets with payload are correctly converted to IR
	// and that the payload is not lost during conversion.
	//
	// This test requires yanet1 repository to be available.
	// Set YANET1_ROOT environment variable to point to yanet1 directory.
	// Example: export YANET1_ROOT=/path/to/yanet1

	// Create analyzer
	analyzer := NewPcapAnalyzer(false)

	// Read ICMP packets from Test009
	yanet1Root := GetYanet1Root()
	pcapPath := filepath.Join(yanet1Root, "autotest/units/001_one_port/009_nat64stateless/007-send.pcap")
	if _, err := os.Stat(pcapPath); os.IsNotExist(err) {
		t.Skipf("PCAP file not found at %s. Set YANET1_ROOT to yanet1 repository location.", pcapPath)
	}

	packets, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	require.NoError(t, err)
	require.Equal(t, 2, len(packets), "Expected 2 packets in 007-send.pcap")

	// DEBUG: Check what gopacket sees
	pkt := gopacket.NewPacket(packets[0].RawData, layers.LayerTypeEthernet, gopacket.Default)
	for _, layer := range pkt.Layers() {
		t.Logf("gopacket Layer: %s, Contents: %d bytes, Payload: %d bytes",
			layer.LayerType(), len(layer.LayerContents()), len(layer.LayerPayload()))
		if icmpEcho, ok := layer.(*layers.ICMPv6Echo); ok {
			t.Logf("  ICMPv6Echo: Id=%d, Seq=%d, Payload=%q", icmpEcho.Identifier, icmpEcho.SeqNumber, string(icmpEcho.Payload))
		}
	}

	// Convert to IR
	opts := CodegenOpts{
		UseFrameworkMACs: true,
		IsExpect:         false,
		StripVLAN:        false,
	}

	ir, err := analyzer.ConvertPacketInfoToIR(packets, "007-send.pcap", "007-expect.pcap", opts)
	require.NoError(t, err)

	// Print IR as JSON for debugging
	jsonData, err := json.MarshalIndent(ir, "", "  ")
	require.NoError(t, err)
	t.Logf("IR JSON:\n%s", string(jsonData))

	// Check that we have send packets
	require.NotNil(t, ir.PCAPPairs)
	require.Equal(t, 1, len(ir.PCAPPairs))
	require.Equal(t, 2, len(ir.PCAPPairs[0].SendPackets))

	// Check first packet (ICMPv6 Echo Request)
	pkt1 := ir.PCAPPairs[0].SendPackets[0]
	t.Logf("Packet 1 has %d layers", len(pkt1.Layers))

	// Find ICMPv6 layer
	var icmpLayer *IRLayer
	var rawLayer *IRLayer
	for i := range pkt1.Layers {
		t.Logf("Layer %d: Type=%s", i, pkt1.Layers[i].Type)
		if pkt1.Layers[i].Type == "ICMPv6EchoRequest" {
			icmpLayer = &pkt1.Layers[i]
		}
		if pkt1.Layers[i].Type == "Raw" {
			rawLayer = &pkt1.Layers[i]
		}
	}

	require.NotNil(t, icmpLayer, "ICMPv6EchoRequest layer should exist")

	// THIS IS THE KEY TEST: Check if Raw/Payload layer exists
	require.NotNil(t, rawLayer, "Raw payload layer should exist for ICMP with payload")

	// Check payload content
	if rawLayer != nil {
		t.Logf("Raw layer params: %+v", rawLayer.Params)
		if arg0, ok := rawLayer.Params["_arg0"].(string); ok {
			require.Equal(t, "du hast vyacheslavich", arg0, "Payload content mismatch")
		}
	}
}
