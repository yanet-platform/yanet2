//go:build linux && (amd64 || arm64)

package decap_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/decap"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

// decapFixture is a dataplane whose port0 the cgo control plane wired
// through decap "d", which holds no prefixes, into a forward sink back to
// port0; the agent "owner" owns both configurations.
type decapFixture struct {
	*nativetest.Fixture
	cAgent *ffi.Agent
	owner  *agent.Agent
}

func newDecapFixture(t testing.TB) *decapFixture {
	t.Helper()
	memory := nativetest.NewFixture(t, dataplaneut.Config{
		WorkerCount:   2,
		Devices:       []string{"port0"},
		Modules:       []string{"decap", "forward"},
		DevicesToLoad: []string{"plain"},
	})
	cAgent, err := memory.Harness.SharedMemory().AgentAttach("owner", 0, 16*datasize.MB)
	require.NoError(t, err)
	config, err := cdecap.NewModuleConfig(cAgent, "d")
	require.NoError(t, err)
	require.NoError(t, cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
	_, err = forward.NewBackend(cAgent).Update("sink", forward.RuleSet{Compiled: []cforward.ForwardRule{{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: "sink4",
		Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
	}}})
	require.NoError(t, err)
	require.NoError(t, cAgent.UpdateFunction(ffi.FunctionConfig{
		Name: "decap",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "decap_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "decap", Name: "d"},
					{Type: "forward", Name: "sink"},
				},
			},
		}},
	}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "decap", Functions: []string{"decap"}}))
	require.NoError(t, cAgent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(cAgent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "decap", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
	owner, ok := agent.Find(memory.Instance(), "owner")
	require.True(t, ok)
	return &decapFixture{Fixture: memory, cAgent: cAgent, owner: owner}
}

// manyPrefixes returns count unaligned /25 prefixes spread over distinct
// second octets, enough to grow a tree past several page chunks.
func manyPrefixes(count int) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, count)
	for idx := range count {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7)))
	}
	return prefixes
}

// Test_NewConfig_MatchesC verifies that a decap configuration Go builds,
// its module header and both prefix trees, leaves the storage as the C
// decap api building it from the same prefixes does.
func Test_NewConfig_MatchesC(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []netip.Prefix
	}{
		{name: "no prefixes"},
		{name: "one ipv4 prefix", prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}},
		{name: "unmasked prefix address", prefixes: []netip.Prefix{netip.MustParsePrefix("10.1.2.3/8")}},
		{
			name: "overlapping ipv4 prefixes",
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("10.0.0.0/8"),
				netip.MustParsePrefix("10.1.2.128/25"),
				netip.MustParsePrefix("192.0.2.7/32"),
				netip.MustParsePrefix("0.0.0.0/0"),
				netip.MustParsePrefix("172.16.0.0/12"),
			},
		},
		{
			name: "ipv6 prefixes",
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("2001:db8::/32"),
				netip.MustParsePrefix("2001:db8:1::/48"),
				netip.MustParsePrefix("::1/128"),
				netip.MustParsePrefix("fe80::/10"),
			},
		},
		{name: "page chunks outgrown", prefixes: manyPrefixes(40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newDecapFixture(t)
			memory.CompareWithC(t,
				func() {
					config, err := cdecap.NewModuleConfig(memory.cAgent, "d")
					require.NoError(t, err)
					for _, prefix := range tc.prefixes {
						require.NoError(t, config.PrefixAdd(prefix))
					}
				},
				func() {
					_, err := decap.NewConfig(memory.owner, "d", tc.prefixes)
					require.NoError(t, err)
				},
			)
		})
	}
}

// ipipPacket returns an IPv4-in-IPv4 echo request whose outer header is
// addressed to outer, and the inner packet alone.
func ipipPacket(t *testing.T, outer net.IP) (gopacket.Packet, gopacket.Packet) {
	t.Helper()
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	outerIP := layers.IPv4{
		Version: 4, Id: 1, TTL: 64, Protocol: layers.IPProtocolIPv4,
		SrcIP: net.IPv4(198, 51, 100, 1), DstIP: outer,
	}
	innerIP := layers.IPv4{
		Version: 4, Id: 2, TTL: 64, Protocol: layers.IPProtocolICMPv4,
		SrcIP: net.IPv4(198, 51, 100, 2), DstIP: net.IPv4(203, 0, 113, 9),
	}
	icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	return xpacket.LayersToPacket(t, &eth, &outerIP, &innerIP, &icmp),
		xpacket.LayersToPacket(t, &eth, &innerIP, &icmp)
}

// requireOutput fails unless the packet comes out of port0 as want.
func requireOutput(t *testing.T, memory *decapFixture, packet, want gopacket.Packet, message string) {
	t.Helper()
	result, err := memory.Harness.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Drop, message)
	require.Len(t, result.Output, 1, message)
	require.Equal(t, want.Data(), result.Output[0].RawData, message)
}

// Test_UpdateModules_DataplaneDecapsulatesByGoPrefixes verifies that a
// decap configuration whose prefix tree Go built, published from Go,
// changes what the dataplane does to packets: tunnels to a Go-inserted
// prefix come out decapsulated, others pass unchanged as before.
func Test_UpdateModules_DataplaneDecapsulatesByGoPrefixes(t *testing.T) {
	memory := newDecapFixture(t)
	memory.RunAssigner(t)
	matched, matchedInner := ipipPacket(t, net.IPv4(10, 1, 2, 3))
	other, _ := ipipPacket(t, net.IPv4(192, 0, 2, 1))

	requireOutput(t, memory, matched, matched, "the C configuration holds no prefixes")

	module, err := decap.NewConfig(memory.owner, "d", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module}))

	requireOutput(t, memory, matched, matchedInner, "a tunnel to the Go prefix is decapsulated")
	requireOutput(t, memory, other, other, "a tunnel elsewhere passes unchanged")
}

// benchmarkPrefixes returns 256 /24 prefixes over 16 second octets, a
// tree of 18 pages.
func benchmarkPrefixes() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, 256)
	for idx := range 256 {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", idx%16, idx/16)))
	}
	return prefixes
}

// Benchmark_NewConfig measures building a decap configuration of 256
// prefixes through the C decap api over cgo and natively.
func Benchmark_NewConfig(b *testing.B) {
	prefixes := benchmarkPrefixes()
	b.Run("cgo", func(b *testing.B) {
		memory := newDecapFixture(b)
		for b.Loop() {
			config, err := cdecap.NewModuleConfig(memory.cAgent, "d")
			require.NoError(b, err)
			for _, prefix := range prefixes {
				require.NoError(b, config.PrefixAdd(prefix))
			}
		}
	})
	b.Run("native", func(b *testing.B) {
		memory := newDecapFixture(b)
		for b.Loop() {
			_, err := decap.NewConfig(memory.owner, "d", prefixes)
			require.NoError(b, err)
		}
	})
}

// Benchmark_UpdateModules_Decap measures building a decap configuration
// of 256 prefixes and publishing it until every worker runs it, through
// the cgo control plane and through the native one, both delivering the
// workers' contexts on the publishing thread.
func Benchmark_UpdateModules_Decap(b *testing.B) {
	prefixes := benchmarkPrefixes()
	b.Run("cgo", func(b *testing.B) {
		memory := newDecapFixture(b)
		for b.Loop() {
			config, err := cdecap.NewModuleConfig(memory.cAgent, "d")
			require.NoError(b, err)
			for _, prefix := range prefixes {
				require.NoError(b, config.PrefixAdd(prefix))
			}
			require.NoError(b, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
		}
	})
	b.Run("native", func(b *testing.B) {
		memory := newDecapFixture(b)
		for b.Loop() {
			module, err := decap.NewConfig(memory.owner, "d", prefixes)
			require.NoError(b, err)
			require.NoError(b, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module},
				cpconfig.WithDelivery(nativetest.Deliver),
			))
		}
	})
}
