package common

import (
	"encoding/binary"
	"math/big"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func Unwrap[T any](t T, e error) T {
	if e != nil {
		panic(e)
	}
	return t
}

func PacketsToPaylod(packets []gopacket.Packet) [][]byte {
	payload := make([][]byte, 0, len(packets))
	for _, p := range packets {
		payload = append(payload, p.Data())
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

func ToBroadCast(prefix netip.Prefix) netip.Addr {
	ip := prefix.Addr()
	bits := prefix.Bits()

	if prefix.Addr().Is4() {
		v4b := ip.As4()
		addrBits := binary.BigEndian.Uint32(v4b[:])
		wildcardBits := uint32(1<<(32-bits) - 1)
		broadCastBits := addrBits | wildcardBits

		binary.BigEndian.PutUint32(v4b[:], broadCastBits)
		return netip.AddrFrom4(v4b)
	} else {
		v6b := ip.As16()
		addrBits := new(big.Int).SetBytes(v6b[:])
		wildcardBits := new(big.Int).Sub(
			big.NewInt(1).Lsh(big.NewInt(1), uint(128-bits)),
			big.NewInt(1),
		)
		broadCastBits := new(big.Int).Or(addrBits, wildcardBits)

		copy(v6b[:], broadCastBits.Bytes())
		return netip.AddrFrom16(v6b)
	}
}
