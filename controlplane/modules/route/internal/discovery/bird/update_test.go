package bird

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
)

// bird/lib/net.c#L60
func TestSizeAssert(t *testing.T) {
	require.EqualValues(t, unsafe.Sizeof(netAddrIP4{}), 8)
	require.EqualValues(t, unsafe.Sizeof(netAddrIP6{}), 20)
	require.EqualValues(t, unsafe.Sizeof(netAddrVPN4{}), 16)
	require.EqualValues(t, unsafe.Sizeof(netAddrVPN6{}), 32)
	require.EqualValues(t, unsafe.Sizeof(rib.LargeCommunity{}), 12)
}

var dataIPv6WithLargeCommunities = []byte{
	// NetAddrUnion 40 bytes
	// NetAddr type 0x2 == NetIP6
	0: 0x2,
	// Prefix len 0x23 == 35
	1: 0x23,
	// NetAddrUnion size 0x14 == 20
	2: 0x14, 0,
	// prefix as 4 LE u32 == "2001:200:c000::/35",
	4: 0, 0x2, 0x1, 0x20,
	8: 0, 0, 0, 0xc0,
	12: 0, 0, 0, 0,
	16: 0, 0, 0, 0,
	// padding 4 bytes
	20: 0, 0, 0, 0,
	// RD??? seems garbage...
	24: 0xc0, 0xd6, 0xa3, 0x35, 0x47, 0x59,
	30: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,

	// update.opType 4 bytes
	40: 0x1, 0, 0, 0,
	//  peer addr 16 bytes as 4 LE u32
	44: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1, 0, 0, 0,
	// attrsAreaSize 4 bytes LE u32 - 0x7a == 122
	60: 0x7a, 0, 0, 0,

	// attributes
	// AttrOrigin 0x01 - first byte;  65: PROTOCOL_BGP = 0x4, 0, 0 - packed LE int
	64: 0x1, 65: 0x4, 0, 0,
	// value of the AttrOrigin - LE u32
	68: 0x2, 0, 0, 0,

	//  AttrASPath = 0x2 : PROTOCOL_BGP = 0x4, 0, 0
	72: 0x2, 0x4, 0, 0,
	//  Complex attribute length LE u32 0xe == 14
	76: 0xe, 0, 0, 0,
	// Segment Type 0x2 = ASPathSequence; 81: Segment Size 0x3 = 3 AS
	80: 0x2, 81: 0x3,
	// AS#3 - PeerAS LE u32 - 0x0000c7fa = 51194 - nearest to us
	82: 0, 0, 0xc7, 0xf1,
	// AS#2 - next after origin AS = 0x00001d4c = 7500
	86: 0, 0, 0x1d, 0x4c,
	// AS#1 - OriginAS = 0x00005c52 = 23634
	90: 0, 0, 0x5c, 0x52,

	// AttrNextHop 0x3; PROTOCOL_BGP
	94: 0x3, 0x4, 0, 0,
	// Complex attribute length LE u32 0x10 == 16 - one ipv6 addr
	98: 0x10, 0, 0, 0,
	//  NextHop addr 16 bytes as 4 LE u32 == "2a02:2891:9:200::13"
	102: 0x91, 0x28, 0x2, 0x2a, 0, 0x2, 0x9, 0, 0, 0, 0, 0, 0x13, 0, 0, 0,

	// AttrLocalPref 0x5; PROTOCOL_BGP - simple u32 attributes
	118: 0x5, 0x4, 0, 0,
	// LocalPref 0x64 == 100
	122: 0x64, 0, 0, 0,
	// AttrCommunity 0x8; PROTOCOL_BGP
	126: 0x8, 0x4, 0, 0,
	// AttrCommunity length - 16
	130: 0x10, 0, 0, 0,
	134: 0x2, 0, 0xf1, 0xc7, 0xf6, 0x1, 0xf1, 0xc7, 0x9a, 0x2, 0xf1, 0xc7, 0x12, 0x8, 0xf1, 0xc7,

	// AttrLargeCommunity 0x20; PROTOCOL_BGP
	150: 0x20, 0x4, 0, 0,
	// Complex attribute length 0x18 = 24
	154: 0x18, 0, 0, 0,
	// 12 bytes
	158: 0xfa, 0xc9, 0, 0, 0xe8, 0x3, 0, 0, 0x1, 0, 0, 0,
	// 11 bytes
	170: 0xfa, 0xc9, 0, 0, 0xe9, 0x3, 0, 0, 0x1, 0, 0,
	// last byte
	// attributes data len respects the value of attrsAreaSize
	64 - /* - sizeOf(attrsAreaSize)*/ 4 + ( /*len - 1*/ 122 - 1): 0,
}

func TestDecodeUpdate(t *testing.T) {
	cases := []struct {
		name      string
		data      []byte
		expected  rib.Route
		errNew    error
		errDecode error
	}{
		{
			name: "OK ipv6 update",
			data: dataIPv6WithLargeCommunities,
			expected: rib.Route{
				Prefix:    netip.MustParsePrefix("2001:200:c000::/35"),
				NextHop:   netip.MustParseAddr("2a02:2891:9:200::13"),
				Peer:      netip.MustParseAddr("::1"),
				PeerAS:    51185,
				OriginAS:  23634,
				Med:       0,
				Pref:      100,
				ASPathLen: 3,
				LargeCommunities: []rib.LargeCommunity{
					{
						GlobalAdministrator: 51706,
						LocalDataPart1:      1000,
						LocalDataPart2:      1,
					},
					{
						GlobalAdministrator: 51706,
						LocalDataPart1:      1001,
						LocalDataPart2:      1,
					},
				},
			},
		},
		{
			name: "OK no attrs",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x2,     // NetAddr type NetIP6
				1: 0x30,    // prefix len == 48
				2: 0x14, 0, // NetAddr struct size
				4: 0xb8, 0xd, 0x7, 0x23, 0, 0, 0x4, 0, 0, 0, 0, 0, 0, 0, 0, 0, // prefix
				// garbage
				20: 0, 0, 0, 0, 0x98, 0xd2, 0xa3, 0x35, 0x47, 0x59,
				30: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,

				// update type LE u32
				40: 0x1, 0, 0, 0,
				// peer addr
				44: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
				// attrsAreaSize including sizeof(attrsAreaSize) - 0x4 => no attributes
				60: 0x4, 0, 0, 0x0,
			},
			expected: rib.Route{
				Prefix: netip.MustParsePrefix("2307:db8:4::/48"),
				Peer:   netip.IPv6Unspecified(),
			},
		},
		{
			name: "OK ipv4",
			data: []byte{
				// NetIP4
				0: 0x1,
				// prefix len
				1: 0x16,
				// union struct size == 8
				2: 0x8, 0,
				// ipv4 prefix ad LE u32
				4: 0, 0x4, 0, 0x1,
				// ... garbage ...
				// update.opType
				40: 0x1, 0, 0, 0,
				// peer addr 16 byte as 4 LE u32 = ::1
				44: 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1, 0, 0, 0,

				// attributes
				// attrsAreaSize 0x72 == 114 bytes
				60: 0x72, 0, 0, 0,
				// attributes data
				// ORIGIN
				64: 0x1, 0x4, 0, 0 /* origin value */, 0, 0, 0, 0,
				// ASPath
				72: 0x2, 0x4, 0, 0,
				76: 0x1e, 0, 0, 0, // ASPath area size = 30 bytes
				80: 0x2, 81: 0x7, // 80: ASPathSequence; 81: ASPathLen == 0x7
				0, 0, 0xbb, 0xc6, // 1
				0, 0, 0x5, 0x13, // 2
				0, 0, 0x1d, 0x79, // 3
				0, 0, 0xa, 0xcc, // 4
				0, 0, 0x97, 0x93, // 5
				0, 0, 0x97, 0x93, // 6
				0, 0, 0x97, 0x93, // 7
				110: 0x3, 0x4, 0, 0, // 0x3 = AttrNextHop; PROTOCOL_BGP
				114: 0x10, 0, 0, 0, // NextHop size LE u32 = 16 bytes
				// nextHop ::ffff:c342:e2fe == ::ffff:195.66.226.254
				118: 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 0xfe, 0xe2, 0x42, 0xc3,
				134: 0x5, 0x4, 0, 0, // AttrLocalPref = 0x5; PROTOCOL_BGP
				138: 0x64, 0, 0, 0, // LE u32 == 100
				142: 0x8, 0x4, 0, 0, // AttrCommunity; PROTOCOL_BGP
				0x4, 0, 0, 0, // 4 bytes
				0x64, 0, 0xc6, 0xbb,
				0x20, 0x4, 0, 0, // AttrLargeCommunity; PROTOCOL_BGP
				0xc, 0, 0, 0, // 12 bytes == 3 u32
				0xc6, 0xbb, 0, 0,
				0x64, 0, 0, 0,
				0x13, 0x5, 0, 0x0,
			},
			expected: rib.Route{
				Prefix:    netip.MustParsePrefix("1.0.4.0/22"),
				NextHop:   netip.AddrFrom16([16]byte{10: 0xff, 11: 0xff, 12: 0xc3, 0x42, 0xe2, 0xfe}),
				Peer:      netip.IPv6Loopback(),
				PeerAS:    0x0000bbc6,
				OriginAS:  0x00009793,
				Pref:      0x64,
				ASPathLen: 0x7,
				LargeCommunities: []rib.LargeCommunity{
					{
						GlobalAdministrator: 48070,
						LocalDataPart1:      100,
						LocalDataPart2:      1299,
					},
				},
			},
		},
		{
			name: "OK ToRemove",
			data: []byte{
				1, 0x18, 8, 5: 7, 7: 1, 24: 0x10, 0x92, 0x53, 9, 0x1c, 0x5b, 32: 1,
				0x11, 8, 0, 0, 0x80, 0, 1, 2, 56: 1, 60: 0x52, 0, 0, 0, 1, 4,
				72: 2, 4, 0, 0, 0x12, 0, 0, 0, 2, 4, 0, 6, 0x14, 0x81, 0, 0, 5,
				89: 0x13, 0, 0, 0x1d, 0x79, 0, 0, 0x97, 0x93, 3, 4, 0, 0, 0x10,
				114: 0xff, 0xff, 0, 0, 0xb9, 0x68, 0x52, 0xce, 5, 4, 0, 0, 0x64,
				0, 0, 0, 8, 4, 0, 0, 4, 0, 0, 0, 0xb8, 0x88, 0x13, 0x5,
			},
			expected: rib.Route{
				Prefix:    netip.MustParsePrefix("1.0.7.0/24"),
				NextHop:   netip.MustParseAddr("::ffff:206.82.104.185"),
				Peer:      netip.IPv6Loopback(),
				PeerAS:    398465,
				OriginAS:  38803,
				Pref:      100,
				ASPathLen: 4,
				ToRemove:  true, // NOTE: ToRemove test
			},
		},
		{
			name:   "ERR Empty",
			data:   []byte{},
			errNew: ErrDataTooSmall,
		},
		{
			name: "ERR PrefixLen",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x4,     // NetAddr type NetVPN6
				1: 0xff,    // ERROR: prefix len overflow
				2: 0x14, 0, // NetAddr struct size
				4: 0xb8, 0xd, 0x7, 0x23, 0, 0, 0x4, 0, 0, 0, 0, 0, 0, 0, 0, 0, // prefix
				// RD LE u64 (bytes in the reverse order as described in the RFC)
				24: 0, 0, 0, 0, 0, 0, 0x1, 0,
				40: 0x1, 0, 0, 0, // update type LE u32
				44: 0,            // ... peer addr all zero
				60: 0x4, 0, 0, 0, // no attrs
			},
			errDecode: ErrBadPrefix,
		},
		{
			name: "ERR unknown NetAddrUinion size",
			data: []byte{
				0: 0x4,     // NetAddr type NetVPN6
				1: 0x8,     // prefix len
				2: 0x40, 0, // ERROR: unknown NetAddrUinion struct size
				attrAreaSizeOffset: 4, 0, 0, 0, // attrsAreaSize
			},
			errDecode: ErrUnknownAddrUnion,
		},
		{
			name: "ERR unsupported prefix",
			data: []byte{
				0: 0xa,     // ERROR: NetMAX unsupported prefix
				1: 0x8,     // prefix len
				2: 0x14, 0, // NetAddr struct size
				60: 4, 0, 0, 0, // attrsAreaSize
			},
			errDecode: ErrUnsupportedPrefix,
		},
		{
			name: "ERR unexpected end of attributes data ",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x3,     // NetVPN4
				1: 0x8,     // prefix len
				2: 0x14, 0, // NetAddr struct size
				8: 0, 0, 0, 0, 0, 0, 0x1, 0, // RD LE u64
				// attrsAreaSize expected at idx == 60
				60: 6,    // ERROR attrsAreaSize < 4+sizeoftype(4)  unexpected end of data
				64: 0, 0, // truncated attrs data
			},
			errDecode: ErrAttrsUnexpectedEOD,
		},
		{
			name: "ERR U32 attribute truncated",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x3,     // NetVPN4
				1: 0x8,     // prefix len
				2: 0x14, 0, // NetAddr struct size
				8: 0, 0, 0, 0, 0, 0, 0x1, 0, // RD LE u64
				60: 4 + 4 + /* ERROR: not enought storage or U32 attribute */ 2,
				64: 0x1 /* < ORIGIN: PROTOCOL_BGP > */, 0x4, 0, 0,
				68: 0, 0, // ... truncated
			},
			errDecode: ErrAttributesTruncated,
		},
		{
			name: "ERR Complex attribute truncated",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x3,     // NetVPN4
				1: 0x8,     // prefix len
				2: 0x14, 0, // NetAddr struct size
				8: 0, 0, 0, 0, 0, 0, 0x1, 0, // RD LE u64
				60: 4 + 4 + 4 + 4,
				64: 0x2 /* < AS_PATH: PROTOCOL_BGP > */, 0x4, 0, 0,
				68: 100, 5, 0, 0, // ERROR: size of complex attribute too big
				72: 0, 0, 0, 0,
			},
			errDecode: ErrAttributesTruncated,
		},
		{
			name: "ERR ASPath attribute truncated",
			data: []byte{
				// NetAddrUnion 40 bytes
				0: 0x3,     // NetVPN4
				1: 0x8,     // prefix len
				2: 0x14, 0, // NetAddr struct size
				60: 4 + 4 + 4 + 4,
				64: 0x2 /* < AS_PATH: PROTOCOL_BGP > */, 0x4, 0, 0,
				68: 2, 0, 0, 0, // size of ASPath attribute
				72: 2, 100, // ERROR: 100 segents but not enought data
			},
			errNew: ErrAttributesTruncated,
		},
	}

	for idx, c := range cases {
		t.Run(fmt.Sprintf("case #%d %s", idx, c.name), func(t *testing.T) {
			update, err := newUpdate(c.data)
			if c.errNew != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, c.errNew, "err from newUpdate")
				return
			}
			require.NoError(t, err)
			actual := &rib.Route{}
			err = update.Decode(actual)
			if c.errDecode != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, c.errDecode, "err from update.Decode")
				return
			}
			require.Equal(t, &c.expected, actual, "%s != %s", c.expected.Prefix, actual.Prefix)
		})
	}

}

// cpu: 13th Gen Intel(R) Core(TM) i7-13700H
// Benchmark_update_Decode-20      30660841                39.61 ns/op            0 B/op          0 allocs/op
func Benchmark_update_Decode(b *testing.B) {
	route := &rib.Route{} // memset(0)
	result := 0

	b.ResetTimer()
	for b.Loop() {
		u, err := newUpdate(dataIPv6WithLargeCommunities)
		if err != nil {
			b.Logf("unexpected error: %v", err)
			b.FailNow()
		}
		*route = rib.Route{} // memset(0)
		if route.Pref != 0 {
			b.FailNow() // memset did not work?
		}
		u.Decode(route)
		// Expected NextHop "2a02:2891:9:200::13"
		result += int(route.Pref) + int(route.NextHop.As16()[15] /*+ 0x13 == 19*/)
	}
	b.StopTimer()

	expectedResult := (int(route.Pref) + 0x13) * b.N
	if result != expectedResult {
		b.Logf("unexpected result: %d != %d", result, expectedResult)
		b.FailNow()
	}
	b.Logf("pref sum %12d == %d", result, expectedResult)
}

// NOTE: In case of an error `fuzzing process hung or terminated unexpectedly: exit status 2`,
// see: https://github.com/golang/go/issues/56238
// And try to run again with: `go test -v -parallel=1 -fuzz ./...`
func Fuzz_update_Decode(f *testing.F) {
	f.Add([]byte{
		// OK remove route event
		1, 0x18, 8, 5: 7, 7: 1, 24: 0x10, 0x92, 0x53, 9, 0x1c, 0x5b, 32: 1,
		0x11, 8, 0, 0, 0x80, 0, 1, 2, 56: 1, 60: 0x52, 0, 0, 0, 1, 4,
		72: 2, 4, 0, 0, 0x12, 0, 0, 0, 2, 4, 0, 6, 0x14, 0x81, 0, 0, 5,
		89: 0x13, 0, 0, 0x1d, 0x79, 0, 0, 0x97, 0x93, 3, 4, 0, 0, 0x10,
		114: 0xff, 0xff, 0, 0, 0xb9, 0x68, 0x52, 0xce, 5, 4, 0, 0, 0x64,
		0, 0, 0, 8, 4, 0, 0, 4, 0, 0, 0, 0xb8, 0x88, 0x13, 0x5,
	})
	f.Add(dataIPv6WithLargeCommunities)
	f.Add(([]byte)(nil))

	f.Fuzz(func(t *testing.T, data []byte) {
		u, err := newUpdate(data)
		if u != nil {
			route := &rib.Route{}
			err = u.Decode(route)
		}
		if err != nil && !errors.Is(err, ErrUpdateDecode) {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
