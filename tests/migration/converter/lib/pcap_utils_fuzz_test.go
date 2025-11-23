package lib

import (
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// Fuzz ensures convertLayerToIR is resilient to arbitrary inputs (no panics).
func FuzzConvertLayerToIR(f *testing.F) {
	seed := []byte{0x00, 0x11, 0x22, 0x33, 0x44} // small seed
	f.Add(seed)
	f.Fuzz(func(t *testing.T, data []byte) {
		pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
		pa := NewPcapAnalyzer(false)
		for _, l := range pkt.Layers() {
			_, _ = pa.convertLayerToIR(l, CodegenOpts{})
		}
	})
}


