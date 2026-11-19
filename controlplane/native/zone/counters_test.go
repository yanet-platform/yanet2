//go:build linux && (amd64 || arm64)

package zone_test

import (
	"fmt"
	"net"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Instance_CountersMatchCgoReader verifies that the native counter
// reader reports, for every tag predicate shape, the groups, counters and
// per-worker values the cgo reader reports, after packets ran on one
// worker only, with an object whose counter storages hold no counters.
func Test_Instance_CountersMatchCgoReader(t *testing.T) {
	harness, agent := newPublishedHarness(t)
	object, err := coracle.NewObject(agent.AsRawPtr(), "ring", "state", unsafe.Sizeof(layout.CpObject{}))
	require.NoError(t, err)
	require.NoError(t, coracle.UpdateObjects(agent.AsRawPtr(), []unsafe.Pointer{object}))
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := layers.IPv4{
		Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4,
		SrcIP: net.IPv4(10, 0, 0, 1), DstIP: net.IPv4(192, 168, 1, 1),
	}
	icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	_, err = harness.HandlePacketsOnWorker(1, xpacket.LayersToPacket(t, &eth, &ip, &icmp))
	require.NoError(t, err)
	native := nativeStorage(t, harness).Instances[0]
	cgo := harness.SharedMemory().DPConfig(0)
	module, err := native.Counters([]zone.CounterTag{{Key: "module_name", Value: "first"}, {Key: "kind", Value: "module"}})
	require.NoError(t, err)
	require.NotEmpty(t, module)
	counted := slices.ContainsFunc(module[0].Counters, func(counter zone.Counter) bool {
		return slices.ContainsFunc(counter.Values[1], func(value uint64) bool { return value != 0 })
	})
	require.True(t, counted, "the packet counts on worker 1")

	cases := []struct {
		name       string
		predicates []zone.CounterTag
	}{
		{"every storage", nil},
		{"module storages of a type", []zone.CounterTag{{Key: "kind", Value: "module"}, {Key: "module_type", Value: "blackhole"}}},
		{"one module by its position", []zone.CounterTag{
			{Key: "module_name", Value: "first"}, {Key: "chain", Value: "drop_chain"}, {Key: "kind", Value: "module"},
		}},
		{"storages with a key at any value", []zone.CounterTag{{Key: "chain", Value: "*"}}},
		{"storages without a key", []zone.CounterTag{{Key: "pipeline", Value: ""}}},
		{"a value no storage has", []zone.CounterTag{{Key: "module_name", Value: "missing"}}},
		{"object storages without counters", []zone.CounterTag{{Key: "kind", Value: "object"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := make([]ffi.CounterTag, len(tc.predicates))
			for idx, predicate := range tc.predicates {
				tags[idx] = ffi.CounterTag{Key: predicate.Key, Value: predicate.Value}
			}
			cgoGroups, err := cgo.CountersByTags(tags, nil)
			require.NoError(t, err)
			var want []zone.CounterGroup
			for _, group := range cgoGroups {
				converted := zone.CounterGroup{}
				for _, tag := range group.Tags {
					converted.Tags = append(converted.Tags, zone.CounterTag{Key: tag.Key, Value: tag.Value})
				}
				for _, counter := range group.Counters {
					converted.Counters = append(converted.Counters, zone.Counter{Name: counter.Name, Values: counter.Values})
				}
				want = append(want, converted)
			}
			got, err := native.Counters(tc.predicates)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

// Test_Instance_CountersRefusesRepeatedKey verifies that predicates
// naming one key twice are refused, as the cgo reader refuses them.
func Test_Instance_CountersRefusesRepeatedKey(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	native := nativeStorage(t, harness).Instances[0]
	_, err := native.Counters([]zone.CounterTag{{Key: "kind", Value: "module"}, {Key: "kind", Value: "device"}})
	require.Error(t, err)
}

// Test_Instance_CountersRefusesTooManyTags verifies that more predicates
// than a storage holds tags are refused, as the cgo reader refuses them.
func Test_Instance_CountersRefusesTooManyTags(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	native := nativeStorage(t, harness).Instances[0]
	predicates := make([]zone.CounterTag, len(layout.CpCounterStorage{}.Tags)+1)
	for idx := range predicates {
		predicates[idx] = zone.CounterTag{Key: fmt.Sprintf("key%d", idx), Value: "*"}
	}
	_, err := native.Counters(predicates)
	require.Error(t, err)
}

// Test_Instance_CurrentTimeMatchesCgoReader verifies that the native
// reader reports the dataplane's time as the cgo reader does.
func Test_Instance_CurrentTimeMatchesCgoReader(t *testing.T) {
	harness, _ := newPublishedHarness(t)
	harness.SetCurrentTime(time.Unix(1_700_000_000, 5))
	native := nativeStorage(t, harness).Instances[0]
	wantTime, wantOK := harness.SharedMemory().DPConfig(0).CurrentTime()
	gotTime, gotOK := native.CurrentTime()
	require.True(t, wantOK)
	require.Equal(t, wantOK, gotOK)
	require.Equal(t, wantTime, gotTime)
}
