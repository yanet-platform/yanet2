package nat64s_test

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cplain "github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	cforward "github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
	"github.com/yanet-platform/yanet2/modules/nat64s/controlplane"
	nat64spb "github.com/yanet-platform/yanet2/modules/nat64s/controlplane/nat64spb/v1"
)

// The endpoints of the scenarios: the client, the public address its flows
// are translated to, and the remote, as IPv4 and as embedded under the
// NAT64 prefix. The public address is also given in the wire form, a
// big-endian integer.
const (
	clientAddress   = "2001:db8::1"
	poolAddress     = "198.51.100.1"
	poolAddressWire = 0xc6336401
	remoteAddress4  = "192.0.2.34"
	remoteAddress6  = "64:ff9b::c000:222"
)

var (
	clientHardware  = xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01"))
	gatewayHardware = xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55"))
)

// mustIPv6Prefix builds the wire prefix of valid text, panicking on a
// programming error in the test data.
func mustIPv6Prefix(value string) *commonpb.IPv6Prefix {
	prefix, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(value))
	if err != nil {
		panic(err)
	}
	return prefix
}

// sessionConfig is a whole config under the well-known prefix, translated
// to the public address of the scenarios.
func sessionConfig(mapName string) *nat64spb.Config {
	return &nat64spb.Config{
		Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix("64:ff9b::/96")},
		Pool:     []*commonpb.IPv4Address{{Addr: poolAddressWire}},
		Map:      mapName,
	}
}

// sessionMapGone reports whether the dataplane no longer knows the session
// map of the given name.
func sessionMapGone(agent *ffi.Agent, name string) bool {
	return errors.Is(agent.DeleteObject(cnat64s.MapObjectType, name), ffi.ErrNotFound)
}

// setupNAT64sHarness wires the module end to end, down to its service
// over the real backend and map registry.
//
// The maps are created small: the default sizing needs a block as large
// as the allocator's largest, which a sanitizer build cannot provide.
func setupNAT64sHarness(
	t *testing.T,
) (*dataplaneut.Harness, *ffi.Agent, *nat64s.MapRegistry, *nat64s.NAT64sService) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"nat64s", "forward"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{cnat64s.MapObjectType},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"nat64s-functional-test",
		0,
		16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	registry := nat64s.NewMapRegistry(agent, nat64s.WithRegistryMapSizing(
		cnat64s.MapConfig{IndexSize: 1024, ExtraBucketCount: 64},
	))
	service := nat64s.NewNAT64sService(nat64s.NewBackend(agent, registry))

	return harness, agent, registry, service
}

// wireNAT64sPipeline routes a device's traffic through the nat64s config
// and out of the device again.
func wireNAT64sPipeline(t *testing.T, agent *ffi.Agent, configName string) {
	t.Helper()

	sinkName := configName + "-sink"
	sinkRules := []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink_l2",
			Devices: filter.Devices{{Name: "port0"}},
		},
	}
	sinkHandle, err := forward.NewBackend(agent).Update(
		sinkName,
		forward.RuleSet{Compiled: sinkRules},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "nat64s", Name: configName},
					{Type: "forward", Name: sinkName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

// tcpSegment carries the TCP fields the scenarios vary.
type tcpSegment struct {
	SrcPort layers.TCPPort
	DstPort layers.TCPPort
	SYN     bool
	ACK     bool
}

// frame6 builds an Ethernet frame from the client carrying an IPv6 packet
// with the TCP segment, its checksum computed over that IPv6 header.
func frame6(t *testing.T, src, dst string, segment tcpSegment) gopacket.Packet {
	t.Helper()

	ip := &layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP(src),
		DstIP:      net.ParseIP(dst),
	}
	tcp := tcpSegmentLayer(segment)
	tcp.SetNetworkLayerForChecksum(ip)

	return xpacket.LayersToPacket(
		t,
		&layers.Ethernet{
			SrcMAC:       clientHardware,
			DstMAC:       gatewayHardware,
			EthernetType: layers.EthernetTypeIPv6,
		},
		ip,
		tcp,
	)
}

// frame4 builds an Ethernet frame from the gateway carrying an IPv4 packet
// with the TCP segment, its checksum computed over that IPv4 header.
func frame4(t *testing.T, src, dst string, segment tcpSegment) gopacket.Packet {
	t.Helper()

	ip := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP(src),
		DstIP:    net.ParseIP(dst),
	}
	tcp := tcpSegmentLayer(segment)
	tcp.SetNetworkLayerForChecksum(ip)

	return xpacket.LayersToPacket(
		t,
		&layers.Ethernet{
			SrcMAC:       gatewayHardware,
			DstMAC:       clientHardware,
			EthernetType: layers.EthernetTypeIPv4,
		},
		ip,
		tcp,
	)
}

// tcpSegmentLayer builds the segment's header.
func tcpSegmentLayer(segment tcpSegment) *layers.TCP {
	return &layers.TCP{
		SrcPort: segment.SrcPort,
		DstPort: segment.DstPort,
		SYN:     segment.SYN,
		ACK:     segment.ACK,
		Window:  0xffff,
	}
}

// soleOutput asserts the round emitted exactly one packet and returns it
// parsed.
func soleOutput(t *testing.T, result *dataplaneut.Result) gopacket.Packet {
	t.Helper()

	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)

	return xpacket.ParseEtherPacket(result.Output[0].RawData)
}

// awaitError returns the result sent on the channel, failing the test when
// nothing arrives in time instead of hanging it.
func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the operation did not finish")
		return nil
	}
}

// awaitSignal returns once the channel is closed, failing the test when
// that does not happen in time instead of hanging it.
func awaitSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the operation did not finish")
	}
}

// Test_NAT64sService_UpdateConfig_PublishesTranslatingConfig verifies that
// a config published through the service translates a client's packet from
// its public address and routes the remote's reply back to the client: the
// session tables the config names are linked, and the public address
// reaches the dataplane as the address that was sent.
func Test_NAT64sService_UpdateConfig_PublishesTranslatingConfig(t *testing.T) {
	harness, agent, _, service := setupNAT64sHarness(t)

	_, err := service.UpdateConfig(t.Context(), &nat64spb.UpdateConfigRequest{
		Name:   "t0",
		Config: sessionConfig("sessions"),
	})
	require.NoError(t, err)
	wireNAT64sPipeline(t, agent, "t0")

	// The client's port lies below the default window, so the session's
	// port is a translated one.
	result, err := harness.HandlePackets(frame6(
		t,
		clientAddress,
		remoteAddress6,
		tcpSegment{SrcPort: 80, DstPort: 443, SYN: true},
	))
	require.NoError(t, err)
	translated := soleOutput(t, result)
	ipv4, ok := translated.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	require.True(t, ok, "the client's packet must leave as IPv4")
	require.Equal(t, poolAddress, ipv4.SrcIP.String())
	require.Equal(t, remoteAddress4, ipv4.DstIP.String())
	outbound, ok := translated.Layer(layers.LayerTypeTCP).(*layers.TCP)
	require.True(t, ok, "the client's packet must keep its TCP segment")
	require.Equal(t, layers.TCPPort(443), outbound.DstPort)
	require.GreaterOrEqual(t, uint16(outbound.SrcPort), uint16(1024))

	result, err = harness.HandlePackets(frame4(
		t,
		remoteAddress4,
		poolAddress,
		tcpSegment{SrcPort: 443, DstPort: outbound.SrcPort, SYN: true, ACK: true},
	))
	require.NoError(t, err)
	returned := soleOutput(t, result)
	ipv6, ok := returned.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	require.True(t, ok, "the remote's reply must leave as IPv6")
	require.Equal(t, clientAddress, ipv6.DstIP.String())
	require.Equal(t, remoteAddress6, ipv6.SrcIP.String())
	inbound, ok := returned.Layer(layers.LayerTypeTCP).(*layers.TCP)
	require.True(t, ok, "the remote's reply must keep its TCP segment")
	require.Equal(t, layers.TCPPort(80), inbound.DstPort)
	require.Equal(t, layers.TCPPort(443), inbound.SrcPort)
}

// Test_Backend_Update_FailedPublishGivesMapReferenceBack verifies that a
// config whose publish fails is left holding no map: the reference the
// update took is given back, so the map it created is reclaimed.
func Test_Backend_Update_FailedPublishGivesMapReferenceBack(t *testing.T) {
	_, agent, registry, _ := setupNAT64sHarness(t)

	// A prefix of the wrong length fails the config's build.
	config := &nat64s.NAT64sConfig{
		Prefixes: [][]byte{{0x00, 0x64, 0xff, 0x9b}},
		MapName:  "sessions",
	}
	_, err := nat64s.NewBackend(agent, registry).Update("t0", config)
	require.Error(t, err)
	require.Empty(t, registry.MapOf("t0"))

	registry.ReclaimUnreferenced()
	require.True(t, sessionMapGone(agent, "sessions"))
}

// Test_NAT64sService_DeleteConfig_ReleasesSessionMap verifies that deleting
// a config lets go of its session map, which the next reclaim round then
// deletes from the dataplane.
func Test_NAT64sService_DeleteConfig_ReleasesSessionMap(t *testing.T) {
	_, agent, registry, service := setupNAT64sHarness(t)

	_, err := service.UpdateConfig(t.Context(), &nat64spb.UpdateConfigRequest{
		Name:   "t0",
		Config: sessionConfig("sessions"),
	})
	require.NoError(t, err)
	require.Equal(t, "sessions", registry.MapOf("t0"))

	_, err = service.DeleteConfig(t.Context(), &nat64spb.DeleteConfigRequest{
		Name: "t0",
	})
	require.NoError(t, err)
	require.Empty(t, registry.MapOf("t0"))

	registry.ReclaimUnreferenced()
	require.True(t, sessionMapGone(agent, "sessions"))
}

// Test_MapRegistry_ReclaimUnreferenced_KeepsReferenceTakenMeanwhile
// verifies that a reference a config takes while a reclaim round is under
// way survives the round.
//
// The round is parked inside its deletion by the agent's configuration
// lock, which the deletion needs, while the reference is taken. A round
// that judged the map unreferenced and then deleted it regardless would
// take the new reference down with it.
func Test_MapRegistry_ReclaimUnreferenced_KeepsReferenceTakenMeanwhile(t *testing.T) {
	_, agent, registry, _ := setupNAT64sHarness(t)
	require.NoError(t, registry.Acquire("t0", "sessions"))
	registry.Release("t0", "sessions")

	roundDone := make(chan struct{})
	reference := make(chan error, 1)
	returned := false
	withConfigLock(t, agent, func() {
		go func() {
			registry.ReclaimUnreferenced()
			close(roundDone)
		}()
		// The round reaches the deletion, and the lock it waits for.
		time.Sleep(50 * time.Millisecond)

		go func() {
			reference <- registry.Acquire("t1", "sessions")
		}()
		// A round that let go of the registry between judging and
		// deleting lets the reference complete here; one that holds the
		// registry until the deletion is over keeps it waiting.
		select {
		case err := <-reference:
			returned = true
			require.NoError(t, err)
		case <-time.After(200 * time.Millisecond):
		}
		// Returning releases the agent's lock: the round runs out.
	})
	awaitSignal(t, roundDone)
	if !returned {
		require.NoError(t, awaitError(t, reference))
	}

	require.Equal(t, "sessions", registry.MapOf("t1"))
	// The map the reference names is the one the dataplane holds.
	require.NoError(t, agent.DeleteObject(cnat64s.MapObjectType, "sessions"))
}
