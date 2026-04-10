package balancer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ---------------------------------------------------------------------------
// Test helpers — minimal valid proto objects reusable across tests.
// ---------------------------------------------------------------------------

func makeValidReal() *balancerpb.Real {
	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   []byte{10, 0, 0, 1},
			Port: 0,
		},
		Weight: 1,
		Src: &filterpb.IPNet{
			Addr: []byte{10, 0, 0, 0},
			Mask: []byte{255, 255, 255, 0},
		},
	}
}

func makeValidVS() *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  []byte{1, 1, 1, 1},
			Port:  80,
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
		Flags:     &balancerpb.VsFlags{},
		Reals:     []*balancerpb.Real{makeValidReal()},
	}
}

func makeValidPacketHandlerConfig() *balancerpb.PacketHandlerConfig {
	return &balancerpb.PacketHandlerConfig{
		SourceAddressV4: []byte{5, 5, 5, 5},
		SourceAddressV6: make([]byte, 16),
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    10,
			TcpFin:    10,
			Tcp:       10,
			Udp:       10,
		},
		Vs: []*balancerpb.VirtualService{makeValidVS()},
	}
}

func makeValidStateConfig() *balancerpb.StateConfig {
	cap := uint64(1024)
	lf := float32(0.7)
	power := uint64(2)
	maxWeight := uint32(100)
	return &balancerpb.StateConfig{
		SessionTableCapacity:      &cap,
		SessionTableMaxLoadFactor: &lf,
		Wlc: &balancerpb.WlcConfig{
			Power:     &power,
			MaxWeight: &maxWeight,
		},
		RefreshPeriod: durationpb.New(1000000000), // 1s
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestValidateMask4(t *testing.T) {
	tests := []struct {
		name    string
		mask    []byte
		wantErr bool
	}{
		{"slash-16", []byte{0xFF, 0xFF, 0x00, 0x00}, false},
		{"slash-32", []byte{0xFF, 0xFF, 0xFF, 0xFF}, false},
		{"slash-0", []byte{0x00, 0x00, 0x00, 0x00}, false},
		{"slash-24", []byte{0xFF, 0xFF, 0xFF, 0x00}, false},
		{"non-contiguous", []byte{0xFF, 0x00, 0xFF, 0x00}, true},
		{"hole-at-start", []byte{0x00, 0xFF, 0x00, 0x00}, true},
		{"single-bit-gap", []byte{0xFF, 0xFE, 0x01, 0x00}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMask4(tt.mask)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsContiguous8(t *testing.T) {
	tests := []struct {
		name string
		mask []byte
		want bool
	}{
		{"all-ones", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, true},
		{"all-zeros", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, true},
		{"half-ones", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00}, true},
		{"47-bits", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE, 0x00, 0x00}, true},
		{"non-contiguous", []byte{0xFF, 0x00, 0xFF, 0x00, 0x00, 0x00, 0x00, 0x00}, false},
		{"hole-in-low", []byte{0x00, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x00}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isContiguous8(tt.mask))
		})
	}
}

func TestValidateMask6(t *testing.T) {
	tests := []struct {
		name    string
		mask    []byte
		wantErr bool
	}{
		{
			"slash-64",
			[]byte{
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
			false,
		},
		{
			"slash-128",
			[]byte{
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
			},
			false,
		},
		{
			"slash-0",
			make([]byte, 16),
			false,
		},
		{
			"non-contiguous-high",
			[]byte{
				0xFF,
				0x00,
				0xFF,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
			true,
		},
		{
			"non-contiguous-low",
			[]byte{
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0xFF,
				0x00,
				0xFF,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMask6(tt.mask)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateNet(t *testing.T) {
	tests := []struct {
		name    string
		net     *filterpb.IPNet
		isV6    bool
		wantErr bool
	}{
		{
			"valid-ipv4",
			&filterpb.IPNet{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0xFF, 0xFF, 0x00}},
			false, false,
		},
		{
			"valid-ipv6",
			&filterpb.IPNet{
				Addr: make([]byte, 16),
				Mask: []byte{
					0xFF,
					0xFF,
					0xFF,
					0xFF,
					0xFF,
					0xFF,
					0xFF,
					0xFF,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
			},
			true, false,
		},
		{
			"ipv4-wrong-addr-len",
			&filterpb.IPNet{Addr: []byte{10, 0, 0}, Mask: []byte{0xFF, 0xFF, 0xFF, 0x00}},
			false, true,
		},
		{
			"ipv4-wrong-mask-len",
			&filterpb.IPNet{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0xFF}},
			false, true,
		},
		{
			"ipv6-wrong-addr-len",
			&filterpb.IPNet{Addr: make([]byte, 15), Mask: make([]byte, 16)},
			true, true,
		},
		{
			"ipv6-wrong-addr-len-2",
			&filterpb.IPNet{Addr: make([]byte, 4), Mask: make([]byte, 16)},
			true, true,
		},
		{
			"ipv6-wrong-mask-len",
			&filterpb.IPNet{Addr: make([]byte, 16), Mask: make([]byte, 4)},
			true, true,
		},
		{
			"ipv4-non-contiguous-mask",
			&filterpb.IPNet{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0x00, 0xFF, 0x00}},
			false, true,
		},
		{
			"ipv6-non-contiguous-mask",
			&filterpb.IPNet{
				Addr: make([]byte, 16),
				Mask: []byte{
					0xFF,
					0xFE,
					0xFF,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
					0x00,
				},
			},
			true, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNet(tt.net, tt.isV6)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidatePortRange(t *testing.T) {
	tests := []struct {
		name    string
		pr      *filterpb.PortRange
		wantErr bool
	}{
		{"full-range", &filterpb.PortRange{From: 0, To: 65535}, false},
		{"single-port", &filterpb.PortRange{From: 80, To: 80}, false},
		{"normal-range", &filterpb.PortRange{From: 1024, To: 2048}, false},
		{"from-greater-than-to", &filterpb.PortRange{From: 100, To: 50}, true},
		{"to-exceeds-max", &filterpb.PortRange{From: 0, To: 65536}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePortRange(tt.pr)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateSessionsTimeouts(t *testing.T) {
	t.Run("all-at-max", func(t *testing.T) {
		err := validateSessionsTimeouts(&balancerpb.SessionsTimeouts{
			TcpSynAck: MaxSessionTimeout,
			TcpSyn:    MaxSessionTimeout,
			TcpFin:    MaxSessionTimeout,
			Tcp:       MaxSessionTimeout,
			Udp:       MaxSessionTimeout,
		})
		require.NoError(t, err)
	})

	fields := []struct {
		name string
		make func() *balancerpb.SessionsTimeouts
	}{
		{"tcp_syn_ack", func() *balancerpb.SessionsTimeouts {
			return &balancerpb.SessionsTimeouts{TcpSynAck: MaxSessionTimeout + 1}
		}},
		{"tcp_syn", func() *balancerpb.SessionsTimeouts {
			return &balancerpb.SessionsTimeouts{TcpSyn: MaxSessionTimeout + 1}
		}},
		{"tcp_fin", func() *balancerpb.SessionsTimeouts {
			return &balancerpb.SessionsTimeouts{TcpFin: MaxSessionTimeout + 1}
		}},
		{"tcp", func() *balancerpb.SessionsTimeouts {
			return &balancerpb.SessionsTimeouts{Tcp: MaxSessionTimeout + 1}
		}},
		{"udp", func() *balancerpb.SessionsTimeouts {
			return &balancerpb.SessionsTimeouts{Udp: MaxSessionTimeout + 1}
		}},
	}
	for _, f := range fields {
		t.Run(f.name+"-exceeds-max", func(t *testing.T) {
			require.Error(t, validateSessionsTimeouts(f.make()))
		})
	}
}

func TestValidateWlcConfig(t *testing.T) {
	power := uint64(2)
	maxWeight := uint32(100)

	t.Run("valid", func(t *testing.T) {
		require.NoError(t, validateWlcConfig(&balancerpb.WlcConfig{
			Power: &power, MaxWeight: &maxWeight,
		}))
	})
	t.Run("nil-power", func(t *testing.T) {
		require.Error(t, validateWlcConfig(&balancerpb.WlcConfig{
			MaxWeight: &maxWeight,
		}))
	})
	t.Run("nil-max-weight", func(t *testing.T) {
		require.Error(t, validateWlcConfig(&balancerpb.WlcConfig{
			Power: &power,
		}))
	})
}

func TestValidateStateConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		require.NoError(t, validateStateConfig(makeValidStateConfig()))
	})

	t.Run("nil-session-table-capacity", func(t *testing.T) {
		cfg := makeValidStateConfig()
		cfg.SessionTableCapacity = nil
		require.Error(t, validateStateConfig(cfg))
	})

	t.Run("zero-session-table-capacity", func(t *testing.T) {
		cfg := makeValidStateConfig()
		zero := uint64(0)
		cfg.SessionTableCapacity = &zero
		require.Error(t, validateStateConfig(cfg))
	})

	t.Run("nil-refresh-period", func(t *testing.T) {
		cfg := makeValidStateConfig()
		cfg.RefreshPeriod = nil
		require.Error(t, validateStateConfig(cfg))
	})

	t.Run("nil-session-table-max-load-factor", func(t *testing.T) {
		cfg := makeValidStateConfig()
		cfg.SessionTableMaxLoadFactor = nil
		require.Error(t, validateStateConfig(cfg))
	})

	loadFactorTests := []struct {
		name    string
		lf      float32
		wantErr bool
	}{
		{"zero", 0, true},
		{"half", 0.5, false},
		{"one", 1.0, false},
		{"above-one", 1.1, true},
		{"negative", -0.1, true},
	}
	for _, tt := range loadFactorTests {
		t.Run("load-factor-"+tt.name, func(t *testing.T) {
			cfg := makeValidStateConfig()
			cfg.SessionTableMaxLoadFactor = &tt.lf
			err := validateStateConfig(cfg)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("nil-wlc", func(t *testing.T) {
		cfg := makeValidStateConfig()
		cfg.Wlc = nil
		require.Error(t, validateStateConfig(cfg))
	})

	t.Run("invalid-wlc", func(t *testing.T) {
		cfg := makeValidStateConfig()
		cfg.Wlc = &balancerpb.WlcConfig{}
		require.Error(t, validateStateConfig(cfg))
	})
}

func TestValidateReal(t *testing.T) {
	t.Run("valid-ipv4", func(t *testing.T) {
		require.NoError(t, validateReal(makeValidReal()))
	})

	t.Run("valid-ipv6", func(t *testing.T) {
		r := &balancerpb.Real{
			Id: &balancerpb.RelativeRealIdentifier{
				Ip:   make([]byte, 16),
				Port: 0,
			},
			Weight: 1,
			Src: &filterpb.IPNet{
				Addr: make([]byte, 16),
				Mask: make([]byte, 16),
			},
		}
		require.NoError(t, validateReal(r))
	})

	t.Run("nil-id", func(t *testing.T) {
		r := makeValidReal()
		r.Id = nil
		require.Error(t, validateReal(r))
	})

	t.Run("wrong-ip-length", func(t *testing.T) {
		r := makeValidReal()
		r.Id.Ip = []byte{10, 0, 0}
		require.Error(t, validateReal(r))
	})

	t.Run("non-zero-port", func(t *testing.T) {
		r := makeValidReal()
		r.Id.Port = 8080
		require.Error(t, validateReal(r))
	})

	t.Run("nil-src", func(t *testing.T) {
		r := makeValidReal()
		r.Src = nil
		require.Error(t, validateReal(r))
	})

	t.Run("mismatched-src-addr-length", func(t *testing.T) {
		r := makeValidReal()
		r.Src.Addr = make([]byte, 16) // IPv6 addr but IPv4 id
		require.Error(t, validateReal(r))
	})

	t.Run("mismatched-src-mask-length", func(t *testing.T) {
		r := makeValidReal()
		r.Src.Mask = make([]byte, 16) // IPv6 mask but IPv4 id
		require.Error(t, validateReal(r))
	})
}

func TestValidateReals(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		r1 := makeValidReal()
		r2 := makeValidReal()
		r2.Id.Ip = []byte{10, 0, 0, 2}
		require.NoError(t, validateReals([]*balancerpb.Real{r1, r2}))
	})

	t.Run("empty", func(t *testing.T) {
		require.NoError(t, validateReals([]*balancerpb.Real{}))
	})

	t.Run("nil-entry", func(t *testing.T) {
		err := validateReals([]*balancerpb.Real{nil})
		require.Error(t, err)
		require.Contains(t, err.Error(), "index 0")
	})

	t.Run("duplicate", func(t *testing.T) {
		r1 := makeValidReal()
		r2 := makeValidReal() // same IP = duplicate
		err := validateReals([]*balancerpb.Real{r1, r2})
		require.Error(t, err)
	})
}

func TestValidateAllowedSrc(t *testing.T) {
	t.Run("valid-with-nets-ports-tag", func(t *testing.T) {
		tag := "test"
		src := &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0xFF, 0x00, 0x00}},
			},
			Ports: []*filterpb.PortRange{
				{From: 1024, To: 2048},
			},
			Tag: &tag,
		}
		require.NoError(t, validateAllowedSrc(src, false))
	})

	t.Run("valid-empty", func(t *testing.T) {
		require.NoError(t, validateAllowedSrc(&balancerpb.AllowedSources{}, false))
	})

	t.Run("invalid-net", func(t *testing.T) {
		src := &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: []byte{10, 0}, Mask: []byte{0xFF, 0xFF}}, // wrong length for IPv4
			},
		}
		require.Error(t, validateAllowedSrc(src, false))
	})

	t.Run("invalid-port-range", func(t *testing.T) {
		src := &balancerpb.AllowedSources{
			Ports: []*filterpb.PortRange{
				{From: 100, To: 50},
			},
		}
		require.Error(t, validateAllowedSrc(src, false))
	})

	t.Run("tag-too-long", func(t *testing.T) {
		longTag := strings.Repeat("a", int(AllowedSourceMaxTagLength)+1)
		src := &balancerpb.AllowedSources{
			Tag: &longTag,
		}
		require.Error(t, validateAllowedSrc(src, false))
	})

	t.Run("tag-at-max-length", func(t *testing.T) {
		maxTag := strings.Repeat("a", int(AllowedSourceMaxTagLength))
		src := &balancerpb.AllowedSources{
			Tag: &maxTag,
		}
		require.NoError(t, validateAllowedSrc(src, false))
	})
}

func TestValidateAllowedSources(t *testing.T) {
	t.Run("squashes-duplicates", func(t *testing.T) {
		src := &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0xFF, 0x00, 0x00}},
			},
		}
		srcDup := &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xFF, 0xFF, 0x00, 0x00}},
			},
		}
		result, err := validateAllowedSources([]*balancerpb.AllowedSources{src, srcDup}, false)
		require.NoError(t, err)
		require.Len(t, result, 1)
	})

	t.Run("nil-entry", func(t *testing.T) {
		_, err := validateAllowedSources([]*balancerpb.AllowedSources{nil}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "index 0")
	})

	t.Run("empty-ok", func(t *testing.T) {
		result, err := validateAllowedSources(nil, false)
		require.NoError(t, err)
		require.Empty(t, result)
	})
}

func TestValidateVS(t *testing.T) {
	t.Run("valid-ipv4", func(t *testing.T) {
		require.NoError(t, validateVS(makeValidVS()))
	})

	t.Run("valid-ipv6", func(t *testing.T) {
		vs := makeValidVS()
		vs.Id.Addr = make([]byte, 16)
		// Fix reals to match IPv6.
		vs.Reals[0].Id.Ip = make([]byte, 16)
		vs.Reals[0].Src = &filterpb.IPNet{
			Addr: make([]byte, 16),
			Mask: make([]byte, 16),
		}
		require.NoError(t, validateVS(vs))
	})

	t.Run("nil-id", func(t *testing.T) {
		vs := makeValidVS()
		vs.Id = nil
		require.Error(t, validateVS(vs))
	})

	t.Run("wrong-addr-length", func(t *testing.T) {
		vs := makeValidVS()
		vs.Id.Addr = []byte{1, 1, 1}
		require.Error(t, validateVS(vs))
	})

	t.Run("invalid-proto", func(t *testing.T) {
		vs := makeValidVS()
		vs.Id.Proto = balancerpb.TransportProto(99)
		require.Error(t, validateVS(vs))
	})

	t.Run("nil-flags", func(t *testing.T) {
		vs := makeValidVS()
		vs.Flags = nil
		require.Error(t, validateVS(vs))
	})

	t.Run("pure-l3-with-port", func(t *testing.T) {
		vs := makeValidVS()
		vs.Flags.PureL3 = true
		vs.Id.Port = 80
		require.Error(t, validateVS(vs))
	})

	t.Run("pure-l3-port-zero", func(t *testing.T) {
		vs := makeValidVS()
		vs.Flags.PureL3 = true
		vs.Id.Port = 0
		require.NoError(t, validateVS(vs))
	})

	t.Run("invalid-scheduler", func(t *testing.T) {
		vs := makeValidVS()
		vs.Scheduler = balancerpb.VsScheduler(99)
		require.Error(t, validateVS(vs))
	})

	t.Run("round-robin-scheduler", func(t *testing.T) {
		vs := makeValidVS()
		vs.Scheduler = balancerpb.VsScheduler_ROUND_ROBIN
		require.NoError(t, validateVS(vs))
	})

	t.Run("bad-peer-length", func(t *testing.T) {
		vs := makeValidVS()
		vs.Peers = [][]byte{{1, 2, 3}} // not 4 or 16
		require.Error(t, validateVS(vs))
	})

	t.Run("valid-peers", func(t *testing.T) {
		vs := makeValidVS()
		vs.Peers = [][]byte{{1, 1, 1, 1}, make([]byte, 16)}
		require.NoError(t, validateVS(vs))
	})
}

func TestValidatePacketHandlerConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		require.NoError(t, validatePacketHandlerConfig(makeValidPacketHandlerConfig()))
	})

	t.Run("wrong-source-v4-length", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.SourceAddressV4 = []byte{1, 2, 3}
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("wrong-source-v6-length", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.SourceAddressV6 = []byte{1, 2, 3}
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("nil-sessions-timeouts", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.SessionsTimeouts = nil
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("invalid-sessions-timeouts", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.SessionsTimeouts.TcpSynAck = MaxSessionTimeout + 1
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("invalid-decap-address-length", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.DecapAddresses = [][]byte{{1, 2, 3}} // not 4 or 16
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("decap-addresses-sorted", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		v6 := make([]byte, 16)
		v6[0] = 0xFE
		v4 := []byte{10, 0, 0, 1}
		cfg.DecapAddresses = [][]byte{v6, v4} // v6 first
		require.NoError(t, validatePacketHandlerConfig(cfg))
		// After validation, v4 should sort before v6.
		require.Len(t, cfg.DecapAddresses[0], 4)
		require.Len(t, cfg.DecapAddresses[1], 16)
	})

	t.Run("duplicate-vs", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.Vs = append(cfg.Vs, makeValidVS()) // same VS id
		require.Error(t, validatePacketHandlerConfig(cfg))
	})

	t.Run("nil-vs-entry", func(t *testing.T) {
		cfg := makeValidPacketHandlerConfig()
		cfg.Vs = []*balancerpb.VirtualService{nil}
		require.Error(t, validatePacketHandlerConfig(cfg))
	})
}

func TestValidateBalancerConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg := &balancerpb.BalancerConfig{
			PacketHandler: makeValidPacketHandlerConfig(),
			State:         makeValidStateConfig(),
		}
		require.NoError(t, validateBalancerConfig(cfg))
	})

	t.Run("nil-config", func(t *testing.T) {
		require.Error(t, validateBalancerConfig(nil))
	})

	t.Run("nil-packet-handler", func(t *testing.T) {
		cfg := &balancerpb.BalancerConfig{
			State: makeValidStateConfig(),
		}
		require.Error(t, validateBalancerConfig(cfg))
	})

	t.Run("nil-state", func(t *testing.T) {
		cfg := &balancerpb.BalancerConfig{
			PacketHandler: makeValidPacketHandlerConfig(),
		}
		require.Error(t, validateBalancerConfig(cfg))
	})

	t.Run("invalid-packet-handler", func(t *testing.T) {
		cfg := &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{},
			State:         makeValidStateConfig(),
		}
		require.Error(t, validateBalancerConfig(cfg))
	})

	t.Run("invalid-state", func(t *testing.T) {
		cfg := &balancerpb.BalancerConfig{
			PacketHandler: makeValidPacketHandlerConfig(),
			State:         &balancerpb.StateConfig{},
		}
		require.Error(t, validateBalancerConfig(cfg))
	})
}
