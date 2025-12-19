package xpacket

import (
	"fmt"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func PacketsGoPayload(packets ...gopacket.Packet) [][]byte {
	payload := make([][]byte, 0, len(packets))
	for idx := range packets {
		payload = append(payload, packets[idx].Data())
	}
	return payload
}

func LayersToPacket(t *testing.T, lyrs ...gopacket.SerializableLayer) gopacket.Packet {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	require.NoError(t, gopacket.SerializeLayers(buf, opts, lyrs...))

	pkt := gopacket.NewPacket(
		buf.Bytes(),
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
	require.Empty(t, pkt.ErrorLayer(), "%#+v", lyrs)
	return pkt
}

func LayersToPacketChecked(lyrs ...gopacket.SerializableLayer) (gopacket.Packet, error) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	if err := gopacket.SerializeLayers(buf, opts, lyrs...); err != nil {
		return nil, fmt.Errorf("failed to serialize layers: %v", err)
	}

	pkt := gopacket.NewPacket(
		buf.Bytes(),
		layers.LayerTypeEthernet,
		gopacket.Default,
	)

	if pkt.ErrorLayer() != nil {
		return nil, fmt.Errorf("failed to parse packet: %v", pkt.ErrorLayer())
	}

	return pkt, nil
}

func ParseEtherPacket(data []byte) gopacket.Packet {
	// Pad the packet with zero bytes to align its size at 60 bytes
	// https://github.com/google/gopacket/issues/361
	// github.com/gopacket/gopacket@v1.3.1/layers/ethernet.go#L95
	if len(data) < 60 {
		var zeros [60]byte
		data = append(data, zeros[:60-len(data)]...)
	}

	return gopacket.NewPacket(
		data,
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
}
