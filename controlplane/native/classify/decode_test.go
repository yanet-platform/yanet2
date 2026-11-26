//go:build linux && (amd64 || arm64)

package classify_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_Decode_MapsEveryValueToTheFirstMemberRule verifies that the rule
// map of the stage of a random ruleset maps every value to the first
// member rule whose group reaches it, or to the invalid rule when none
// does.
func Test_Decode_MapsEveryValueToTheFirstMemberRule(t *testing.T) {
	for seed := range uint64(32) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 19))
			lines, err := classify.CompileLine(randomLineRules(seed, random.IntN(40), 0x100), 0x100)
			require.NoError(t, err)
			stage := lines.Stage()
			ruleGroups, groups := stage.RuleGroups(), classifytest.Groups(stage)
			members := make([]bool, len(ruleGroups))
			for rule := range members {
				members[rule] = random.IntN(4) != 0
			}
			memory := classifytest.LayOut(make([]byte, memorySize))
			line := classifytest.Alloc[layout.Vline](memory)
			require.NoError(t, stage.Decode(members, line, &memory.Context, "rules"))

			ruleMap := classifytest.Values(line)
			require.Len(t, ruleMap, int(stage.MaxValue())+1)
			for value, got := range ruleMap {
				want := classify.InvalidRule
				for rule, group := range ruleGroups {
					if members[rule] && group != classify.InvalidGroup && slices.Contains(groups[group], uint32(value)) {
						want = uint32(rule)
						break
					}
				}
				require.Equal(t, want, got, "the rule of value %d", value)
			}
		})
	}
}

// Test_Decode_RefusesMembersOfOtherRuleCount verifies that members of
// another count of rules than the stage groups are refused as invalid
// before the line is allocated.
func Test_Decode_RefusesMembersOfOtherRuleCount(t *testing.T) {
	devices, err := classify.CompileDevice(randomDeviceRules(1, 4))
	require.NoError(t, err)
	memory := classifytest.LayOut(make([]byte, memorySize))
	line := classifytest.Alloc[layout.Vline](memory)
	free := shmalloc.FreeSize(&memory.Allocator)

	err = devices.Stage().Decode(make([]bool, 3), line, &memory.Context, "rules")
	require.ErrorIs(t, err, classify.ErrInvalidRule)
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}

// Test_Decode_ExhaustedLeavesLineZeroed verifies that a decode refused
// any of its allocations fails as exhausted, leaves the line zeroed and
// gives the allocator back every byte it took.
func Test_Decode_ExhaustedLeavesLineZeroed(t *testing.T) {
	cases := []struct {
		name string
		// kept are the requests the allocator's free blocks serve.
		kept []uintptr
	}{
		{name: "no room for the memory context"},
		{name: "no room for the values", kept: []uintptr{unsafe.Sizeof(layout.MemoryContext{})}},
	}
	devices, err := classify.CompileDevice(randomDeviceRules(2, 8))
	require.NoError(t, err)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := classifytest.LayOut(make([]byte, memorySize))
			line := classifytest.Alloc[layout.Vline](memory)
			nativetest.LeaveOnly(t, &memory.Allocator, tc.kept...)
			free := shmalloc.FreeSize(&memory.Allocator)

			err := devices.Stage().Decode(make([]bool, 8), line, &memory.Context, "rules")
			require.ErrorIs(t, err, shmalloc.ErrNoMemory)
			requireZeroed(t, line, "the line")
			require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
			require.True(t, memory.Context.FirstChild.IsNil(), "the line's memory context is unlinked")
		})
	}
}

// forwardRule is a rule of a forward-shaped ruleset; one without networks
// is an L2 rule, one with source and destination networks an IPv6 rule,
// any other in neither projection.
type forwardRule struct {
	Devices      []uint32
	Vlans        []classify.LineRange
	Sources      []classify.Net6Network
	Destinations []classify.Net6Network
}

// L2 reports whether the rule is in the L2 projection.
func (m forwardRule) L2() bool {
	return len(m.Sources) == 0 && len(m.Destinations) == 0
}

// IP6 reports whether the rule is in the IPv6 projection.
func (m forwardRule) IP6() bool {
	return len(m.Sources) > 0 && len(m.Destinations) > 0
}

// randomForwardRules returns count reproducible forward-shaped rules
// made of the random rules of each attribute, a quarter of them without
// networks.
func randomForwardRules(seed uint64, count int) []forwardRule {
	random := rand.New(rand.NewPCG(seed, 23))
	devices := randomDeviceRules(seed, count)
	vlans := randomLineRules(seed, count, 0x1000)
	sources := randomNet6Rules(seed, count)
	destinations := randomNet6Rules(seed+1000, count)
	rules := make([]forwardRule, count)
	for idx := range rules {
		rules[idx] = forwardRule{Devices: devices[idx].IDs, Vlans: vlans[idx].Ranges}
		if random.IntN(4) != 0 {
			rules[idx].Sources = sources[idx].Networks
			rules[idx].Destinations = destinations[idx].Networks
		}
	}
	return rules
}

// forwardShaped is what a forward-shaped composition commits: the device
// and VLAN attributes joined into the core, the IPv6 source and
// destination attributes joined into the networks, the core and the
// networks joined into the root, and the rule maps of the L2 projection
// over the core and of the IPv6 one over the root.
type forwardShaped struct {
	Device      layout.ClassifyAttrDevice
	Vlan        layout.ClassifyAttrLine
	Source      layout.ClassifyAttrNet6
	Destination layout.ClassifyAttrNet6
	Core        layout.ValueTable
	Nets        layout.ValueTable
	Root        layout.ValueTable
	L2Rules     layout.Vline
	IP6Rules    layout.Vline
}

// commitForwardShaped compiles the rules as the forward module does and
// commits the composition into a holder carved from the memory.
func commitForwardShaped(t *testing.T, rules []forwardRule, memory *classifytest.Memory) *forwardShaped {
	t.Helper()
	deviceRules := make([]classify.DeviceRule, len(rules))
	vlanRules := make([]classify.LineRule, len(rules))
	sourceRules := make([]classify.Net6Rule, len(rules))
	destinationRules := make([]classify.Net6Rule, len(rules))
	l2 := make([]bool, len(rules))
	ip6 := make([]bool, len(rules))
	for idx, rule := range rules {
		l2[idx], ip6[idx] = rule.L2(), rule.IP6()
		core := l2[idx] || ip6[idx]
		deviceRules[idx] = classify.DeviceRule{IDs: rule.Devices, Absent: !core}
		vlanRules[idx] = classify.LineRule{Ranges: rule.Vlans, Absent: !core}
		sourceRules[idx] = classify.Net6Rule{Networks: rule.Sources, Absent: !ip6[idx]}
		destinationRules[idx] = classify.Net6Rule{Networks: rule.Destinations, Absent: !ip6[idx]}
	}
	holder := classifytest.Alloc[forwardShaped](memory)
	context := &memory.Context

	devices, err := classify.CompileDevice(deviceRules)
	require.NoError(t, err)
	require.NoError(t, devices.Commit(&holder.Device, context, "l2:device"))
	vlans, err := classify.CompileLine(vlanRules, 0x1000)
	require.NoError(t, err)
	require.NoError(t, vlans.Commit(&holder.Vlan, context, "l2:vlan"))
	core, err := devices.Stage().Join(vlans.Stage(), &holder.Core, context, "l2:joint")
	require.NoError(t, err)

	sources, err := classify.CompileNet6(sourceRules)
	require.NoError(t, err)
	require.NoError(t, sources.Commit(&holder.Source, context, "ip6:net6_src"))
	destinations, err := classify.CompileNet6(destinationRules)
	require.NoError(t, err)
	require.NoError(t, destinations.Commit(&holder.Destination, context, "ip6:net6_dst"))
	nets, err := sources.Stage().Join(destinations.Stage(), &holder.Nets, context, "ip6:nets_joint")
	require.NoError(t, err)
	root, err := core.Join(nets, &holder.Root, context, "ip6:root_joint")
	require.NoError(t, err)

	require.NoError(t, core.Decode(l2, &holder.L2Rules, context, "l2:rules"))
	require.NoError(t, root.Decode(ip6, &holder.IP6Rules, context, "ip6:rules"))
	return holder
}

// forwardPacket is what a forward-shaped classifier reads of a packet.
type forwardPacket struct {
	Device      uint32
	Vlan        uint32
	Source      classifytest.Net6Key
	Destination classifytest.Net6Key
}

// randomForwardPackets returns count reproducible packets over the
// devices of the line, every VLAN and the interval ends of the networks,
// half of them aimed at a random IPv6 rule.
func randomForwardPackets(seed uint64, count int, rules []forwardRule, devices uint32) []forwardPacket {
	random := rand.New(rand.NewPCG(seed, 29))
	sources := make([]classify.Net6Rule, len(rules))
	destinations := make([]classify.Net6Rule, len(rules))
	var aims []forwardRule
	for idx, rule := range rules {
		sources[idx].Networks, destinations[idx].Networks = rule.Sources, rule.Destinations
		if rule.IP6() {
			aims = append(aims, rule)
		}
	}
	sourceKeys, destinationKeys := classifytest.Net6Keys(sources), classifytest.Net6Keys(destinations)
	packets := make([]forwardPacket, count)
	for idx := range packets {
		packet := forwardPacket{
			Device:      random.Uint32N(devices),
			Vlan:        random.Uint32N(0x1000),
			Source:      sourceKeys[random.IntN(len(sourceKeys))],
			Destination: destinationKeys[random.IntN(len(destinationKeys))],
		}
		if len(aims) > 0 && random.IntN(2) == 0 {
			aim := aims[random.IntN(len(aims))]
			if len(aim.Devices) > 0 {
				packet.Device = aim.Devices[random.IntN(len(aim.Devices))]
			}
			if len(aim.Vlans) > 0 {
				packet.Vlan = min(aim.Vlans[0].From, 0xfff)
			}
			packet.Source = key6(aim.Sources[random.IntN(len(aim.Sources))].Addr)
			packet.Destination = key6(aim.Destinations[random.IntN(len(aim.Destinations))].Addr)
		}
		packets[idx] = packet
	}
	return packets
}

// Test_Decode_ForwardShapedResolvesFirstMatch verifies that packets
// classified through the lookups of a forward-shaped composition resolve
// to the first rule of each projection matching them.
func Test_Decode_ForwardShapedResolvesFirstMatch(t *testing.T) {
	for seed := range uint64(16) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			rules := randomForwardRules(seed, 24)
			memory := classifytest.LayOut(make([]byte, memorySize))
			holder := commitForwardShaped(t, rules, memory)

			deviceClasses := classifytest.Values(&holder.Device.Line)
			vlanClasses := classifytest.Values(&holder.Vlan.Line)
			core := classifytest.Table(&holder.Core)
			sources, destinations := classifytest.ReadNet6(&holder.Source), classifytest.ReadNet6(&holder.Destination)
			nets, root := classifytest.Table(&holder.Nets), classifytest.Table(&holder.Root)
			l2Rules, ip6Rules := classifytest.Values(&holder.L2Rules), classifytest.Values(&holder.IP6Rules)
			for _, packet := range randomForwardPackets(seed, 2000, rules, uint32(len(deviceClasses))) {
				coreClass := core[deviceClasses[packet.Device]][vlanClasses[packet.Vlan]]
				netsClass := nets[sources.Class(packet.Source)][destinations.Class(packet.Destination)]
				wantL2, wantIP6 := classify.InvalidRule, classify.InvalidRule
				for idx, rule := range slices.Backward(rules) {
					if !classifytest.DeviceMatches(classify.DeviceRule{IDs: rule.Devices}, packet.Device) ||
						!classifytest.LineMatches(classify.LineRule{Ranges: rule.Vlans}, packet.Vlan) {
						continue
					}
					if rule.L2() {
						wantL2 = uint32(idx)
					}
					if rule.IP6() &&
						classifytest.Net6Matches(classify.Net6Rule{Networks: rule.Sources}, packet.Source) &&
						classifytest.Net6Matches(classify.Net6Rule{Networks: rule.Destinations}, packet.Destination) {
						wantIP6 = uint32(idx)
					}
				}
				require.Equal(t, wantL2, l2Rules[coreClass], "the L2 rule of %+v", packet)
				require.Equal(t, wantIP6, ip6Rules[root[coreClass][netsClass]], "the IPv6 rule of %+v", packet)
			}
		})
	}
}

// coreShaped is what the two-stage composition commits: the core over
// the union of the projections, its decode of the IPv6 projection, the
// ports over the port-scoped projection and the root joining both with
// its decode; the flat build of the same attributes follows. Port value
// lines stand in for the port attribute and the root's decode for its
// rule table until those land.
type coreShaped struct {
	Device          layout.ClassifyAttrDevice
	Source          layout.ClassifyAttrNet6
	Destination     layout.ClassifyAttrNet6
	Protocol        layout.ClassifyAttrLine
	NetsJoint       layout.ValueTable
	MidJoint        layout.ValueTable
	ProtoJoint      layout.ValueTable
	IP6RuleMap      layout.Vline
	SourcePort      layout.ClassifyAttrLine
	DestinationPort layout.ClassifyAttrLine
	PortsJoint      layout.ValueTable
	RootJoint       layout.ValueTable
	PortRuleMap     layout.Vline
}

// coreRule is a rule of the two-stage composition: its networks and
// port windows.
type coreRule struct {
	Source, Destination         classify.Net6Network
	SourcePort, DestinationPort classify.LineRange
}

// commitCoreShaped compiles the rules as the ACL module's IPv6 core and
// ports do, the core over every rule and the ports over the rules of
// their projection, decodes each filter's members and commits the
// composition into a holder carved from the memory.
func commitCoreShaped(t *testing.T, memory *classifytest.Memory, rules []coreRule, ip6, ports, portMembers []bool) *coreShaped {
	t.Helper()
	// The rules name no devices and no protocols, so they match any.
	devices := make([]classify.DeviceRule, len(rules))
	protocols := make([]classify.LineRule, len(rules))
	sources := make([]classify.Net6Rule, len(rules))
	destinations := make([]classify.Net6Rule, len(rules))
	sourcePorts := make([]classify.LineRule, len(rules))
	destinationPorts := make([]classify.LineRule, len(rules))
	for idx, rule := range rules {
		sources[idx] = classify.Net6Rule{Networks: []classify.Net6Network{rule.Source}}
		destinations[idx] = classify.Net6Rule{Networks: []classify.Net6Network{rule.Destination}}
		sourcePorts[idx] = classify.LineRule{Ranges: []classify.LineRange{rule.SourcePort}, Absent: !ports[idx]}
		destinationPorts[idx] = classify.LineRule{Ranges: []classify.LineRange{rule.DestinationPort}, Absent: !ports[idx]}
	}
	holder := classifytest.Alloc[coreShaped](memory)
	context := &memory.Context

	device, err := classify.CompileDevice(devices)
	require.NoError(t, err)
	require.NoError(t, device.Commit(&holder.Device, context, "test_device"))
	source, err := classify.CompileNet6(sources)
	require.NoError(t, err)
	require.NoError(t, source.Commit(&holder.Source, context, "test_net6_src"))
	destination, err := classify.CompileNet6(destinations)
	require.NoError(t, err)
	require.NoError(t, destination.Commit(&holder.Destination, context, "test_net6_dst"))
	nets, err := source.Stage().Join(destination.Stage(), &holder.NetsJoint, context, "nets_joint")
	require.NoError(t, err)
	mid, err := device.Stage().Join(nets, &holder.MidJoint, context, "mid_joint")
	require.NoError(t, err)
	protocol, err := classify.CompileLine(protocols, 0x100)
	require.NoError(t, err)
	require.NoError(t, protocol.Commit(&holder.Protocol, context, "test_ipproto"))
	coreStage, err := mid.Join(protocol.Stage(), &holder.ProtoJoint, context, "proto_joint")
	require.NoError(t, err)
	require.NoError(t, coreStage.Decode(ip6, &holder.IP6RuleMap, context, "rule_map"))

	sourcePort, err := classify.CompileLine(sourcePorts, 0x10000)
	require.NoError(t, err)
	require.NoError(t, sourcePort.Commit(&holder.SourcePort, context, "test_src_port"))
	destinationPort, err := classify.CompileLine(destinationPorts, 0x10000)
	require.NoError(t, err)
	require.NoError(t, destinationPort.Commit(&holder.DestinationPort, context, "test_dst_port"))
	portStage, err := sourcePort.Stage().Join(destinationPort.Stage(), &holder.PortsJoint, context, "joint")
	require.NoError(t, err)
	root, err := coreStage.Join(portStage, &holder.RootJoint, context, "root_joint")
	require.NoError(t, err)
	require.NoError(t, root.Decode(portMembers, &holder.PortRuleMap, context, "root_rules"))
	return holder
}

// coreClassifier resolves packets through a committed two-stage
// composition.
type coreClassifier struct {
	device, protocol, sourcePort, destinationPort []uint32
	source, destination                           classifytest.Net6Classes
	netsJoint, midJoint, protoJoint, ports        [][]uint32
	root                                          [][]uint32
	ip6Rules, portRules                           []uint32
}

// readCoreShaped reads a committed two-stage composition.
func readCoreShaped(holder *coreShaped) coreClassifier {
	return coreClassifier{
		device:          classifytest.Values(&holder.Device.Line),
		protocol:        classifytest.Values(&holder.Protocol.Line),
		sourcePort:      classifytest.Values(&holder.SourcePort.Line),
		destinationPort: classifytest.Values(&holder.DestinationPort.Line),
		source:          classifytest.ReadNet6(&holder.Source),
		destination:     classifytest.ReadNet6(&holder.Destination),
		netsJoint:       classifytest.Table(&holder.NetsJoint),
		midJoint:        classifytest.Table(&holder.MidJoint),
		protoJoint:      classifytest.Table(&holder.ProtoJoint),
		ports:           classifytest.Table(&holder.PortsJoint),
		root:            classifytest.Table(&holder.RootJoint),
		ip6Rules:        classifytest.Values(&holder.IP6RuleMap),
		portRules:       classifytest.Values(&holder.PortRuleMap),
	}
}

// Classify returns the rules a UDP packet of device zero resolves to
// through the IPv6 filter and through the port-scoped one.
func (m coreClassifier) Classify(source, destination string, sourcePort, destinationPort uint32) (uint32, uint32) {
	nets := m.netsJoint[m.source.Class(key6(addr6(source)))][m.destination.Class(key6(addr6(destination)))]
	core := m.protoJoint[m.midJoint[m.device[0]][nets]][m.protocol[17]]
	ports := m.ports[m.sourcePort[sourcePort]][m.destinationPort[destinationPort]]
	return m.ip6Rules[core], m.portRules[m.root[core][ports]]
}

// Test_Decode_CoreComposedAgreesWithFlat verifies that each filter of a
// two-stage composition resolves its own projection over the shared
// core, and that the port-scoped filter agrees with a flat build of the
// same attributes over the union of the rules.
func Test_Decode_CoreComposedAgreesWithFlat(t *testing.T) {
	netA, netB := net6Network(0x2a02_0000_0000_0000, 32, 0, 0), net6Network(0x2a0d_0000_0000_0000, 32, 0, 0)
	full, window := classify.LineRange{From: 0, To: 0xffff}, classify.LineRange{From: 100, To: 200}
	// The first rule has full port windows and lies in the IPv6
	// projection only; the others restrict a port and lie in the
	// port-scoped projection only.
	rules := []coreRule{
		{Source: netA, Destination: netB, SourcePort: full, DestinationPort: full},
		{Source: netA, Destination: netB, SourcePort: window, DestinationPort: full},
		{Source: netB, Destination: netA, SourcePort: full, DestinationPort: window},
	}
	union := []bool{true, true, true}
	ip6, ports := []bool{true, false, false}, []bool{false, true, true}
	memory := classifytest.LayOut(make([]byte, memorySize))
	free := shmalloc.FreeSize(&memory.Allocator)
	composed := commitCoreShaped(t, memory, rules, ip6, ports, ports)
	flat := commitCoreShaped(t, memory, rules, ip6, union, ports)

	invalid := classify.InvalidRule
	probes := []struct {
		name                        string
		source, destination         string
		sourcePort, destinationPort uint32
		ip6, port                   uint32
	}{
		{name: "source port inside the window", source: "2a02::", destination: "2a0d::", sourcePort: 150, destinationPort: 1000, ip6: 0, port: 1},
		{name: "source port outside the window", source: "2a02::", destination: "2a0d::", sourcePort: 500, destinationPort: 1000, ip6: 0, port: invalid},
		{name: "destination port inside the window", source: "2a0d::", destination: "2a02::", sourcePort: 150, destinationPort: 150, ip6: invalid, port: 2},
		{name: "destination port outside the window", source: "2a0d::", destination: "2a02::", sourcePort: 150, destinationPort: 300, ip6: invalid, port: invalid},
	}
	composedClassifier, flatClassifier := readCoreShaped(composed), readCoreShaped(flat)
	for _, tc := range probes {
		ip6Rule, portRule := composedClassifier.Classify(tc.source, tc.destination, tc.sourcePort, tc.destinationPort)
		require.Equal(t, tc.ip6, ip6Rule, "%s: the IPv6 filter's rule", tc.name)
		require.Equal(t, tc.port, portRule, "%s: the port-scoped filter's rule", tc.name)
		_, flatRule := flatClassifier.Classify(tc.source, tc.destination, tc.sourcePort, tc.destinationPort)
		require.Equal(t, portRule, flatRule, "%s: the flat build's rule", tc.name)
	}

	for _, holder := range []*coreShaped{composed, flat} {
		classify.FreeDecode(&holder.PortRuleMap)
		classify.FreeDecode(&holder.IP6RuleMap)
		for _, table := range []*layout.ValueTable{&holder.RootJoint, &holder.PortsJoint, &holder.ProtoJoint, &holder.MidJoint, &holder.NetsJoint} {
			classify.FreeJoin(table)
		}
		classify.FreeLine(&holder.SourcePort)
		classify.FreeLine(&holder.DestinationPort)
		classify.FreeLine(&holder.Protocol)
		classify.FreeNet6(&holder.Source)
		classify.FreeNet6(&holder.Destination)
		classify.FreeDevice(&holder.Device)
		shmalloc.ContextFree(&memory.Context, unsafe.Pointer(holder), unsafe.Sizeof(*holder))
	}
	require.Equal(t, free, shmalloc.FreeSize(&memory.Allocator), "the allocator's free bytes")
}
