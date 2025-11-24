package lib

import (
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func TestApplyFrameworkMapping_MACSwapAndPreserveVLAN(t *testing.T) {
	// Build a simple packet with VLAN to verify preservation
	pkt, err := NewPacket(nil,
		Ether(),
		Dot1Q(VLANId(7)),
		IPv4(IPSrc("192.0.2.1"), IPDst("198.51.100.1"), IPTTL(64), IPProto(layers.IPProtocolUDP)),
		UDP(UDPSport(1000), UDPDport(2000)),
		Raw([]byte("x")),
	)
	if err != nil {
		t.Fatalf("failed to build packet: %v", err)
	}

	// Map for "send" direction (client->yanet)
	mappedSend, err := ApplyFrameworkMapping(pkt, false)
	if err != nil {
		t.Fatalf("apply mapping failed: %v", err)
	}
	pSend := gopacket.NewPacket(mappedSend, layers.LayerTypeEthernet, gopacket.Default)
	eth := pSend.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	if eth.SrcMAC.String() != framework.SrcMAC || eth.DstMAC.String() != framework.DstMAC {
		t.Fatalf("unexpected MACs (send): %s -> %s", eth.SrcMAC, eth.DstMAC)
	}
	if pSend.Layer(layers.LayerTypeDot1Q) == nil {
		t.Fatalf("vlan should be preserved in send mapping")
	}

	// Map for "expect" direction (yanet->client), MACs should swap
	mappedExpect, err := ApplyFrameworkMapping(pkt, true)
	if err != nil {
		t.Fatalf("apply mapping failed: %v", err)
	}
	pExpect := gopacket.NewPacket(mappedExpect, layers.LayerTypeEthernet, gopacket.Default)
	ethE := pExpect.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	if ethE.SrcMAC.String() != framework.DstMAC || ethE.DstMAC.String() != framework.SrcMAC {
		t.Fatalf("unexpected MACs (expect): %s -> %s", ethE.SrcMAC, ethE.DstMAC)
	}
	if pExpect.Layer(layers.LayerTypeDot1Q) == nil {
		t.Fatalf("vlan should be preserved in expect mapping")
	}
}
