package lib

import (
	"testing"
)

func BenchmarkNewPacket_ComplexStack(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = NewPacket(
			Ether(),
			Dot1Q(VLANId(100)),
			IPv6(IPv6Src("2001:db8::1"), IPv6Dst("2001:db8::2")),
			IPv6ExtHdrFragment(IPv6FragId(0x12345678), IPv6FragOffset(0), IPv6FragM(false)),
			TCP(TCPSport(1234), TCPDport(80), TCPFlags("S")),
			Raw([]byte("payload")),
		)
	}
}


