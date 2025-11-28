package xpacket

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

func PacketMSS(packet gopacket.Packet) (uint16, error) {
	tcpLayer := packet.Layer(layers.LayerTypeTCP)
	if tcpLayer == nil {
		return 0, fmt.Errorf("not tcp packet")
	}
	tcp, _ := tcpLayer.(*layers.TCP)

	for _, opt := range tcp.Options {
		if opt.OptionType == layers.TCPOptionKindMSS {
			if len(opt.OptionData) != 2 {
				continue
			}
			mss := binary.BigEndian.Uint16(opt.OptionData)
			return mss, nil
		}
	}

	return 0, fmt.Errorf("no mss option")
}
