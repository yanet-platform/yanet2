package decap_test

import (
	"net/netip"
	"testing"

	"tests/common"

	"github.com/stretchr/testify/require"
)

func FuzzDecap_IP6IP_noVlan(f *testing.F) {
	prefixes := []netip.Prefix{
		common.Unwrap(netip.ParsePrefix("0.0.0.0/0")),
		common.Unwrap(netip.ParsePrefix("::/0")),
	}
	memCtx := memCtxCreate()
	m := decapModuleConfig(prefixes, memCtx)

	f.Fuzz(func(t *testing.T, pkt []byte) {
		if len(pkt) == 0 {
			return
		}

		pf := common.PacketFrontFromPayload([][]byte{pkt})
		err := common.ParsePackets(pf)
		if err != nil {
			return
		}
		t.Logf("pass to handler")
		result := cDecapHandlePackets(m, pf)
		if len(result.Output) > 0 {
			resultPkt := common.ParseEtherPacket(result.Output[0])
			require.Nil(t, resultPkt.ErrorLayer())
		} else {
			t.Logf("got output")
		}
	})
}
