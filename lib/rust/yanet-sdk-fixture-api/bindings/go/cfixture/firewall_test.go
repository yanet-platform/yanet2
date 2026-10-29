//go:build sdk_rust_fixtures

package cfixture_test

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// firewallInput prepares canonical borrowed records before any native compile.
func firewallInput(t testing.TB, rules []cacl.ACLRule) (*cfixture.FirewallInput, error) {
	t.Helper()
	var input cfixture.FirewallInput
	for _, rule := range rules {
		if err := input.Add(rule); err != nil {
			return nil, err
		}
	}
	return &input, nil
}
func familyRule(ipv6 bool, actions ...uint32) cacl.ACLRule {
	rule := cacl.ACLRule{Counter: "selected"}
	if ipv6 {
		rule.Src6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
		rule.Dst6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
	} else {
		rule.Src4s = []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
		rule.Dst4s = []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
	}
	for _, action := range actions {
		rule.Actions = append(rule.Actions, cacl.ACLAction{Kind: action})
	}
	return rule
}
func transportFrame(ipv6 bool, protocol, subtype byte, source, destination uint16) []byte {
	var frame []byte
	start := 34
	if ipv6 {
		frame = ipv6Frame("2001:db8::2")
		start = 54
		frame[20] = protocol
	} else {
		frame = firewallFrame("10.2.3.4", "192.0.2.1")
		frame[23] = protocol
	}
	if protocol == 6 {
		frame = append(frame, make([]byte, 12)...)
		frame[start+12] = 0x50
		frame[start+13] = subtype
	} else if protocol == 1 || protocol == 58 {
		frame[start] = subtype
	}
	if protocol == 6 || protocol == 17 {
		binary.BigEndian.PutUint16(frame[start:start+2], source)
		binary.BigEndian.PutUint16(frame[start+2:start+4], destination)
	}
	if ipv6 {
		binary.BigEndian.PutUint16(frame[18:20], uint16(len(frame)-54))
	} else {
		binary.BigEndian.PutUint16(frame[16:18], uint16(len(frame)-14))
	}
	return frame
}

// compareFirewall checks packet identities and both counter words against C.
func compareFirewall(t testing.TB, rules []cacl.ACLRule, frames [][]byte, want []fate) {
	t.Helper()
	bed := newBed(
		t,
		func(config *dataplaneut.Config) {
			config.CPMemory = uint64(128 * datasize.MB)
			config.Modules = append(config.Modules, "acl", "firewall")
		},
	).withAgent("full", 64*datasize.MB)
	prepared, err := firewallInput(t, rules)
	require.NoError(t, err)

	native, err := cacl.NewModuleConfig(bed.agent, "native", rules, "", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = native.Free() })
	rust, err := cfixture.NewFirewallConfig(bed.agent, "rust", prepared)
	require.NoError(t, err)
	bed.freeAtEnd(rust)
	nativeInstance := &instance{moduleType: "acl", name: "native", config: native}
	rustInstance := &instance{moduleType: "firewall", name: "rust", config: rust}
	bed.serveBeforeForward(nativeInstance)
	nativeResult := bed.run(0, frames...)
	nativeFates := fatesOf(nativeResult, frames)
	pairs := map[string][][]uint64{}
	for idx, rule := range rules {
		name := rule.Counter
		if name == "" {
			name = "rule " + fmt.Sprint(idx)
		}
		pairs[name] = bed.runtimeCounter(nativeInstance, name)
	}
	bed.serveBeforeForward(rustInstance)
	result := bed.run(0, frames...)
	require.Equal(t, nativeFates, fatesOf(result, frames))
	if want != nil {
		require.Equal(t, want, nativeFates)
	}
	for name, pair := range pairs {
		require.Equal(t, pair, bed.runtimeCounter(rustInstance, name), "counter %s", name)
	}
}

// Test_Firewall_FamilyPathsAndActions verifies that packet fates and counters
// match C across family, transport, fragment and original-rule projections.
func Test_Firewall_FamilyPathsAndActions(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(
			fmt.Sprint(ipv6),
			func(t *testing.T) {
				t.Run(
					"plain",
					func(t *testing.T) {
						compareFirewall(t, []cacl.ACLRule{familyRule(ipv6, 2, 0)}, [][]byte{transportFrame(ipv6, 17, 0, 1234, 80)}, []fate{fateOutput})
					},
				)
				for _, protocol := range []byte{6, 17, 1, 58} {
					t.Run(
						fmt.Sprint(protocol),
						func(t *testing.T) {
							rule := familyRule(ipv6, 2, 0)
							rule.ProtoRanges = filter.ProtoRanges{filter.NewProtoRange(protocol, filter.ExactSubtype(2))}
							if protocol == 17 {
								rule.ProtoRanges = filter.ProtoRanges{filter.NewProtoRange(protocol, filter.AnySubtype())}
							}
							if protocol == 6 || protocol == 17 {
								rule.SrcPortRanges = filter.PortRanges{{From: 1234, To: 1234}}
								rule.DstPortRanges = filter.PortRanges{{From: 80, To: 90}}
							}
							compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{transportFrame(ipv6, protocol, 2, 1234, 80), transportFrame(ipv6, protocol, 3, 1234, 100)}, []fate{fateOutput, fateDrop})
						},
					)
				}
				t.Run(
					"vacuous-tcp",
					func(t *testing.T) {
						rule := familyRule(ipv6, 2, 0)
						rule.ProtoRanges = filter.ProtoRanges{filter.NewProtoRange(6, filter.ExactSubtype(2))}
						compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{transportFrame(ipv6, 6, 2, 999, 100), transportFrame(ipv6, 6, 3, 999, 100)}, []fate{fateOutput, fateDrop})
					},
				)
				t.Run(
					"count-only-priority",
					func(t *testing.T) {
						rule := familyRule(ipv6, 2)
						later := familyRule(ipv6, 2, 0)
						later.Counter = "later"
						compareFirewall(t, []cacl.ACLRule{rule, later}, [][]byte{transportFrame(ipv6, 17, 0, 1, 2)}, []fate{fateDrop})
					},
				)
			},
		)
	}
	t.Run(
		"l2",
		func(t *testing.T) {
			rule := cacl.ACLRule{Counter: "l2", Actions: []cacl.ACLAction{{Kind: 2}, {Kind: 0}}}
			frame := make([]byte, 64)
			frame[12] = 0x88
			frame[13] = 0xb5
			compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{frame}, []fate{fateOutput})
		},
	)
	t.Run(
		"default-device",
		func(t *testing.T) {
			rule := familyRule(false, 2, 0)
			rule.Devices = filter.Devices{{Name: ""}}
			compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{transportFrame(false, 17, 0, 1, 2)}, []fate{fateOutput})
		},
	)
	t.Run(
		"first-fragment",
		func(t *testing.T) {
			rule := familyRule(false, 2, 0)
			rule.ProtoRanges = filter.ProtoRanges{filter.NewProtoRange(17, filter.AnySubtype())}
			rule.DstPortRanges = filter.PortRanges{{From: 80, To: 80}}
			frame := transportFrame(false, 17, 0, 1, 80)
			frame[20] = 0x20
			compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{frame}, []fate{fateOutput})
		},
	)
	t.Run(
		"later-fragment",
		func(t *testing.T) {
			rule := familyRule(false, 2, 0)
			rule.Fragment = filter.FragmentFrag
			frame := transportFrame(false, 17, 0, 1, 80)
			frame[21] = 1
			compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{frame, transportFrame(false, 17, 0, 1, 80)}, []fate{fateOutput, fateDrop})
		},
	)
}

// Test_Firewall_LateFailureRestoresAllNodes verifies that a final projection
// error returns both families' completed native nodes to the allocator.
func Test_Firewall_LateFailureRestoresAllNodes(t *testing.T) {
	bed := newBed(
		t,
		func(config *dataplaneut.Config) {
			config.Modules = append(config.Modules, "firewall")
			config.CPMemory = uint64(64 * datasize.MB)
		},
	).withAgent("late", 16*datasize.MB)
	rules := []cacl.ACLRule{familyRule(false, 2, 0), familyRule(true, 2, 1)}
	input, err := firewallInput(t, rules)
	require.NoError(t, err)

	before := bed.agent.BlockAllocatorFreeSize()
	_, err = cfixture.NewFirewallLateFault(bed.agent, "late", input)
	require.ErrorContains(t, err, "late firewall projection fault")
	require.Equal(t, before, bed.agent.BlockAllocatorFreeSize())
	config, err := cfixture.NewFirewallConfig(bed.agent, "complete", input)
	require.NoError(t, err)
	require.NoError(t, config.Free())
	require.Equal(t, before, bed.agent.BlockAllocatorFreeSize())
}

// Test_Firewall_AllocationFailureRestoresAllNodes verifies that failed builds
// return every block while capacity rises by allocator quanta until success.
func Test_Firewall_AllocationFailureRestoresAllNodes(t *testing.T) {
	bed := newBed(
		t,
		func(config *dataplaneut.Config) {
			config.Modules = append(config.Modules, "firewall")
			config.CPMemory = uint64(128 * datasize.MB)
		},
	)
	input, err := firewallInput(t, []cacl.ACLRule{familyRule(false, 2, 0), familyRule(true, 2, 1)})
	require.NoError(t, err)

	fits := false
	for budget := 0; budget <= 8<<20; budget += cfixture.NativeAllocatorQuantum {
		complete := func() bool {
			agent, err := bed.harness.SharedMemory().AgentAttach(fmt.Sprintf("full-fault-%d", budget), 0, datasize.ByteSize(budget))
			if err != nil {
				return false
			}
			defer func() { require.NoError(t, agent.CleanUp()) }()
			before := agent.BlockAllocatorFreeSize()
			config, err := cfixture.NewFirewallConfig(agent, "full", input)
			if err == nil {
				require.NoError(t, config.Free())
			}
			require.Equal(t, before, agent.BlockAllocatorFreeSize(), "budget %d", budget)
			return err == nil
		}()
		if complete {
			fits = true
			break
		}
	}
	require.True(t, fits)
}

// Test_Firewall_SegmentedByteCountersMatchC verifies that accounting uses
// the first segment length of a chained input packet.
func Test_Firewall_SegmentedByteCountersMatchC(t *testing.T) {
	bed := newBed(
		t,
		func(config *dataplaneut.Config) {
			config.CPMemory = uint64(128 * datasize.MB)
			config.Modules = append(config.Modules, "acl", "firewall")
		},
	).withAgent("segmented", 64*datasize.MB)
	rules := []cacl.ACLRule{familyRule(false, 2, 0)}
	prepared, err := firewallInput(t, rules)
	require.NoError(t, err)

	native, err := cacl.NewModuleConfig(bed.agent, "native-segmented", rules, "", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = native.Free() })
	rust, err := cfixture.NewFirewallConfig(bed.agent, "rust-segmented", prepared)
	require.NoError(t, err)
	bed.freeAtEnd(rust)
	head := transportFrame(false, 17, 0, 1234, 80)
	tail := []byte("payload!")
	binary.BigEndian.PutUint16(head[16:18], uint16(len(head)-14+len(tail)))
	binary.BigEndian.PutUint16(head[38:40], uint16(8+len(tail)))
	for _, module := range []*instance{{moduleType: "acl", name: "native-segmented", config: native}, {moduleType: "firewall", name: "rust-segmented", config: rust}} {
		bed.serveBeforeForward(module)
		result, err := bed.harness.HandleSegmentedPacketsOnDevice(0, 0, [][]byte{head, tail})
		require.NoError(t, err)
		require.Equal(t, [][]byte{head}, result.Output)
		require.Equal(t, []uint64{1, uint64(len(head))}, bed.runtimeCounter(module, "selected")[0])
	}
}

// Test_Firewall_FirstMatchAndDeviceScope verifies that the first applicable
// rule wins across overlapping prefixes and device-scoped projections.
func Test_Firewall_FirstMatchAndDeviceScope(t *testing.T) {
	t.Run(
		"original priority",
		func(t *testing.T) {
			first := familyRule(false, 2, 1)
			first.Counter = "first"
			first.Src4s = []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.1.0.0/16")}
			later := familyRule(false, 2, 0)
			later.Counter = "later"
			later.Src4s = []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.0/8")}
			compareFirewall(t, []cacl.ACLRule{first, later}, [][]byte{firewallFrame("10.1.2.3", "192.0.2.1"), firewallFrame("10.2.2.3", "192.0.2.1"), firewallFrame("11.2.2.3", "192.0.2.1")}, []fate{fateDrop, fateOutput, fateDrop})
		},
	)
	t.Run(
		"first-used name order",
		func(t *testing.T) {
			other := familyRule(false, 2, 1)
			other.Counter = "other"
			other.Devices = filter.Devices{{Name: "port1"}}
			own := familyRule(false, 2, 0)
			own.Counter = "own"
			own.Devices = filter.Devices{{Name: "port0"}, {Name: "port0"}}
			compareFirewall(t, []cacl.ACLRule{other, own}, [][]byte{transportFrame(false, 17, 0, 1000, 80)}, []fate{fateOutput})
		},
	)
}

// Test_Firewall_NonterminalActionsAndUnknownKinds verifies that state and log
// actions continue, unknown actions drop, and the first terminal action wins.
func Test_Firewall_NonterminalActionsAndUnknownKinds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []uint32
		want    fate
	}{
		{"state and log continue", []uint32{3, 4, 5, 2, 0}, fateOutput},
		{"unknown before allow drops", []uint32{255, 0}, fateDrop},
		{"allow terminates before unknown", []uint32{2, 0, 255}, fateOutput},
		{"deny terminates before allow", []uint32{2, 1, 0}, fateDrop},
	} {
		t.Run(
			tc.name,
			func(t *testing.T) {
				rule := familyRule(false, tc.actions...)
				rule.Counter = ""
				unknown := false
				for _, action := range tc.actions {
					unknown = unknown || action > 5
				}
				if !unknown {
					compareFirewall(t, []cacl.ACLRule{rule}, [][]byte{transportFrame(false, 17, 0, 1234, 80)}, []fate{tc.want})
					return
				}
				// C rejects unknown kinds during installation; exercise the safe handler's
				// ordered fallback directly through its canonical API and shared harness.
				input, err := firewallInput(t, []cacl.ACLRule{rule})
				require.NoError(t, err)
				bed := newBed(t, func(config *dataplaneut.Config) {
					config.Modules = append(config.Modules, "firewall")
					config.CPMemory = uint64(64 * datasize.MB)
				}).withAgent("unknown-action", 16*datasize.MB)
				config, err := cfixture.NewFirewallConfig(bed.agent, "unknown", input)
				require.NoError(t, err)
				bed.freeAtEnd(config)
				module := &instance{moduleType: "firewall", name: "unknown", config: config}
				bed.serveBeforeForward(module)
				frame := transportFrame(false, 17, 0, 1234, 80)
				require.Equal(t, []fate{tc.want}, fatesOf(bed.run(0, frame), [][]byte{frame}))
			},
		)
	}
}

// Test_Firewall_MixedBatchKeepsPacketAssociation verifies that family results
// stay with their original packets across multiple bounded query chunks.
func Test_Firewall_MixedBatchKeepsPacketAssociation(t *testing.T) {
	allow := familyRule(false, 2, 0)
	allow.Counter = "v4"
	deny := familyRule(true, 2, 1)
	deny.Counter = "ipv6"
	var frames [][]byte
	var expected []fate
	for idx := range 160 {
		ipv6 := idx%2 != 0
		frames = append(frames, transportFrame(ipv6, 17, 0, uint16(1000+idx), 80))
		if ipv6 {
			expected = append(expected, fateDrop)
		} else {
			expected = append(expected, fateOutput)
		}
	}
	compareFirewall(t, []cacl.ACLRule{allow, deny}, frames, expected)
}

// Test_Firewall_FreeRestoresCapacity verifies that canonical device links,
// classifier tables and named counters all return to the original capacity.
func Test_Firewall_FreeRestoresCapacity(t *testing.T) {
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.Modules = append(config.Modules, "firewall")
		config.CPMemory = uint64(64 * datasize.MB)
	}).withAgent("canonical-free", 16*datasize.MB)
	rule := familyRule(false, 2, 0)
	rule.Devices = filter.Devices{{Name: "port0"}, {Name: ""}}
	input, err := firewallInput(t, []cacl.ACLRule{rule, familyRule(true, 2, 1)})
	require.NoError(t, err)
	before := bed.agent.BlockAllocatorFreeSize()
	config, err := cfixture.NewFirewallConfig(bed.agent, "free", input)
	require.NoError(t, err)
	require.NoError(t, config.Free())
	require.Equal(t, before, bed.agent.BlockAllocatorFreeSize())
	_, err = cfixture.NewFirewallNamesDrop(bed.agent, "names-drop", input)
	require.ErrorContains(t, err, "device names callback destructor")
	require.Equal(t, before, bed.agent.BlockAllocatorFreeSize())
}

// Test_FirewallInput_AddFailureIsAtomic verifies that a rejected action or name
// cannot leave an earlier malformed rule ahead of the next accepted rule.
func Test_FirewallInput_AddFailureIsAtomic(t *testing.T) {
	var input cfixture.FirewallInput
	action := familyRule(false, 256)
	require.ErrorContains(t, input.Add(action), "does not fit")
	device := familyRule(false, 0)
	device.Devices = filter.Devices{{Name: strings.Repeat("p", 80)}}
	require.Error(t, input.Add(device))
	require.NoError(t, input.Add(familyRule(false, 2, 0)))
	bed := newBed(t, func(config *dataplaneut.Config) {
		config.Modules = append(config.Modules, "firewall")
		config.CPMemory = uint64(64 * datasize.MB)
	}).withAgent("atomic-input", 16*datasize.MB)
	config, err := cfixture.NewFirewallConfig(bed.agent, "atomic", &input)
	require.NoError(t, err)
	bed.freeAtEnd(config)
	module := &instance{moduleType: "firewall", name: "atomic", config: config}
	bed.serveBeforeForward(module)
	frame := transportFrame(false, 17, 0, 1234, 80)
	require.Equal(t, [][]byte{frame}, bed.run(0, frame).Output)
	require.Equal(t, []uint64{1, uint64(len(frame))}, bed.runtimeCounter(module, "selected")[0])
}
