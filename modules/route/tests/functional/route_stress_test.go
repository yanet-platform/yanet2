package route_test

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

const (
	stressConfigName = "stress"
	stressVersions   = 4
	// stressGrowthVersion is the one version whose FIB also names a
	// device the module has never linked, exercising the table-growth
	// path. Nothing wires that device, so a packet routed to it drops as
	// device_unresolved — an accepted outcome here, not a failure.
	stressGrowthVersion = 2

	stressPrefixV4 = "10.0.0.0/24"
	stressPrefixV6 = "2001:db8::/32"
)

// stressPrefixV4Addr and stressPrefixV6Addr are the parsed network
// addresses of the stress prefixes, computed once rather than on every
// reader iteration.
var (
	stressPrefixV4Addr = netip.MustParsePrefix(stressPrefixV4).Addr()
	stressPrefixV6Addr = netip.MustParsePrefix(stressPrefixV6).Addr()
)

// stressVersion is one FIB version cycled through by the stress test's
// writers: same prefixes, distinct nexthops, so a reader can tell which
// version a snapshot came from by its dst MAC alone.
type stressVersion struct {
	entries []FIBEntry
}

// buildStressVersions builds the stress test's fixed set of FIB versions.
//
// Every version resolves the same two prefixes to its own dst MAC, ending
// in its version index, plus its own counter name. stressGrowthVersion's
// FIB additionally names a device nothing wires, forcing a module rebuild
// the first time a writer applies it.
func buildStressVersions() []stressVersion {
	versions := make([]stressVersion, stressVersions)
	for v := range stressVersions {
		dstMAC := net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0x00, byte(v)}
		srcMAC := net.HardwareAddr{0xca, 0xfe, 0xba, 0xbe, 0x00, byte(v)}
		// v4 and v6 share one hardware identity (same MACs and device),
		// and the service rejects two counter names for one identity, so
		// both entries must carry the same name.
		counter := fmt.Sprintf("nexthop_stress_v%d", v)

		entries := []FIBEntry{
			{
				Prefix: netip.MustParsePrefix(stressPrefixV4),
				Nexthops: []FIBNexthop{{
					DstMAC: dstMAC, SrcMAC: srcMAC, Device: "port0", Counter: counter,
				}},
			},
			{
				Prefix: netip.MustParsePrefix(stressPrefixV6),
				Nexthops: []FIBNexthop{{
					DstMAC: dstMAC, SrcMAC: srcMAC, Device: "port0", Counter: counter,
				}},
			},
		}
		if v == stressGrowthVersion {
			entries = append(entries, FIBEntry{
				Prefix: netip.MustParsePrefix("172.16.0.0/24"),
				Nexthops: []FIBNexthop{{
					DstMAC: net.HardwareAddr{0xde, 0xad, 0xbe, 0xef, 0xff, byte(v)},
					SrcMAC: srcMAC, Device: "port1",
					Counter: fmt.Sprintf("nexthop_stress_v%d_growth", v),
				}},
			})
		}

		versions[v] = stressVersion{entries: entries}
	}
	return versions
}

// stressUpdateRequest builds the UpdateFIBRequest for one version.
func stressUpdateRequest(tb testing.TB, version stressVersion) *routepb.UpdateFIBRequest {
	tb.Helper()

	return &routepb.UpdateFIBRequest{
		ModuleName: stressConfigName,
		Entries:    toFIBEntries(tb, version.entries),
	}
}

// unwireRouteInput clears the device's pipeline bindings, then deletes
// the stress and dummy pipelines and the stress function, leaving the
// route config unreferenced by any live chain so DeleteConfig can delete
// it, a module a chain still names refuses to delete. Returns the device
// handles the caller owns from then on.
func unwireRouteInput(tb testing.TB, agent *ffi.Agent, deviceName string) []*plain.DeviceConfig {
	tb.Helper()

	devices, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: deviceName}})
	require.NoError(tb, err)
	require.NoError(tb, agent.DeletePipeline(stressConfigName))
	require.NoError(tb, agent.DeletePipeline("dummy"))
	require.NoError(tb, agent.DeleteFunction(stressConfigName))
	return devices
}

// freeSuperseded frees every device handle whose config a later publish
// replaced and keeps the rest, the one still published refusing as it
// should.
func freeSuperseded(handles []*plain.DeviceConfig) []*plain.DeviceConfig {
	kept := handles[:0]
	for _, handle := range handles {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	return kept
}

// stressMACVersion returns the version index a dst MAC's trailing byte
// names, or -1 when it names none of them.
func stressMACVersion(mac net.HardwareAddr) int {
	if len(mac) != 6 {
		return -1
	}
	v := int(mac[5])
	if v < 0 || v >= stressVersions {
		return -1
	}
	return v
}

// TestRouteStress_ConcurrentUpdatesReadsAndPacketsLeaveNoLeak drives
// concurrent FIB updates, ShowFIB/Metrics reads, and packet forwarding
// against one route config, then verifies every read only ever observed
// one whole version, never a torn mix of two, and that deleting the
// config and draining the deferred frees returns the arena to the size a
// first, quiet cycle left it at.
func TestRouteStress_ConcurrentUpdatesReadsAndPacketsLeaveNoLeak(t *testing.T) {
	h, agent, backend := setupRouteHarness(t, "port0")
	service := route.NewRouteService(backend)
	versions := buildStressVersions()
	ctx := t.Context()

	// A quiet cycle first settles what the harness keeps published past
	// a teardown, the device config with its bindings cleared, so the
	// baseline is what a full cycle returns the arena to. Device handles
	// are kept and freed once a later publish supersedes them.
	var devices []*plain.DeviceConfig
	_, err := service.UpdateFIB(ctx, stressUpdateRequest(t, versions[0]))
	require.NoError(t, err)
	devices = append(devices, wirePipeline(t, agent, "port0", stressConfigName)...)
	devices = append(devices, unwireRouteInput(t, agent, "port0")...)
	_, err = service.DeleteConfig(ctx, &routepb.DeleteConfigRequest{Name: stressConfigName})
	require.NoError(t, err)
	service.ReclaimDeferred()
	devices = freeSuperseded(devices)
	baseline := agent.BlockAllocatorFreeSize()

	_, err = service.UpdateFIB(ctx, stressUpdateRequest(t, versions[0]))
	require.NoError(t, err)

	devices = append(devices, wirePipeline(t, agent, "port0", stressConfigName)...)

	stop := make(chan struct{})
	var writers sync.WaitGroup
	var readers sync.WaitGroup

	// Writers: cycle UpdateFIB through every version. One of them also
	// asks for a delete every ~20 iterations, which the live chain naming
	// the module must refuse, and the refusal must leave the store as it
	// was, so the update right after it succeeds like any other.
	const iterationsPerWriter = 150
	writer := func(deleteEvery int) {
		defer writers.Done()
		for idx := range iterationsPerWriter {
			version := versions[idx%stressVersions]
			if deleteEvery > 0 && idx > 0 && idx%deleteEvery == 0 {
				_, err := service.DeleteConfig(ctx, &routepb.DeleteConfigRequest{Name: stressConfigName})
				assert.Error(t, err, "a config a live chain names must refuse to delete")
			}
			_, err := service.UpdateFIB(ctx, stressUpdateRequest(t, version))
			assert.NoError(t, err)
		}
	}
	writers.Add(2)
	go writer(0)
	go writer(20)

	// Readers: ShowFIB must never fail, the config is published for the
	// whole run, and must never mix two versions' nexthops in one
	// response. At least one read must come back whole for the check to
	// mean anything.
	var wholeReads atomic.Int64
	showFIBReader := func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			resp, err := service.ShowFIB(ctx, &routepb.ShowFIBRequest{Name: stressConfigName})
			if !assert.NoError(t, err) {
				continue
			}

			var v4, v6 net.HardwareAddr
			for _, entry := range resp.GetEntries() {
				start, _, err := entry.GetRange().ToRange()
				if !assert.NoError(t, err) {
					continue
				}
				eui := entry.GetNexthops()[0].GetDstMac().EUI48()
				switch start {
				case stressPrefixV4Addr:
					v4 = net.HardwareAddr(eui[:])
				case stressPrefixV6Addr:
					v6 = net.HardwareAddr(eui[:])
				}
			}
			if v4 == nil || v6 == nil {
				continue
			}
			wholeReads.Add(1)
			v4Version, v6Version := stressMACVersion(v4), stressMACVersion(v6)
			assert.NotEqual(t, -1, v4Version, "v4 nexthop MAC must belong to a known version")
			assert.Equal(t, v4Version, v6Version, "a single ShowFIB response must never mix two versions")
		}
	}

	// One reader scrapes Metrics concurrently, exercising that path for
	// panics and data races rather than asserting specific values, which
	// the concurrent churn makes unstable.
	metricsReader := func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, err := service.Metrics()
			assert.NoError(t, err)
		}
	}

	// The packet injector asserts every forwarded packet's dst MAC
	// belongs to some version's set, never garbage, never a MAC no
	// version ever carried, and that forwarding never stops altogether.
	var forwarded atomic.Int64
	packetInjector := func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			pkt := buildRouteIPv4Packet(t, "10.0.0.5", 64)
			result, err := h.HandlePackets(pkt)
			if !assert.NoError(t, err) {
				continue
			}
			for _, out := range result.Output {
				forwarded.Add(1)
				resultPkt := xpacket.ParseEtherPacket(out.RawData)
				ethLayer, ok := resultPkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
				if !assert.True(t, ok) {
					continue
				}
				assert.NotEqual(t, -1, stressMACVersion(ethLayer.DstMAC),
					"forwarded packet dst MAC must belong to some version")
			}
		}
	}

	readers.Add(6)
	for range 4 {
		go showFIBReader()
	}
	go metricsReader()
	go packetInjector()

	writers.Wait()
	close(stop)
	readers.Wait()
	require.Positive(t, wholeReads.Load(), "no ShowFIB read came back whole, the torn-read check ran on nothing")
	require.Positive(t, forwarded.Load(), "no packet was forwarded during the run")

	devices = append(devices, unwireRouteInput(t, agent, "port0")...)

	_, err = service.DeleteConfig(ctx, &routepb.DeleteConfigRequest{Name: stressConfigName})
	if err != nil {
		// A writer's own delete+republish cycle may have already left
		// the config deleted; anything else is a genuine failure.
		require.ErrorContains(t, err, "not found")
	}

	var free uint64
	const reclaimAttempts = 50
	for range reclaimAttempts {
		service.ReclaimDeferred()
		devices = freeSuperseded(devices)
		free = agent.BlockAllocatorFreeSize()
		if free == baseline {
			break
		}
	}
	require.Equal(t, baseline, free,
		"the arena must drain back to the size the quiet cycle left once every deferred free retires")

	list, err := service.ListConfigs(ctx, &routepb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Empty(t, list.GetConfigs())
}
