//go:build linux && (amd64 || arm64)

// Package acl builds ACL module configurations from Go, as
// acl_module_config_init does: the module header, the per-rule targets
// and counters, the device and fwstate links, and the classifiers and
// filters of the l2, IPv4 and IPv6 paths the dataplane matches packets
// through.
package acl

import (
	"fmt"
	"math"
	"net/netip"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

const (
	// maxActions is the most actions a rule carries (ACL_MAX_ACTIONS).
	maxActions = 8
	// objectLinkNone marks an absent fwstate link (ACL_OBJECT_LINK_NONE).
	objectLinkNone = math.MaxUint64

	fwstateMapV4 = "fwstate_map_v4"
	fwstateMapV6 = "fwstate_map_v6"
)

// The target actions the dataplane runs (ACTION_*), by the rule action
// kind they come from.
var targetActions = map[uint32]uint64{
	uint32(cacl.ActionAllow):       0,
	uint32(cacl.ActionDeny):        1,
	uint32(cacl.ActionCount):       2,
	uint32(cacl.ActionCheckState):  3,
	uint32(cacl.ActionCreateState): 4,
	uint32(cacl.ActionLog):         5,
}

// NewConfig builds an ACL configuration in the agent's memory that
// matches packets against the rules, first match winning, and links the
// fwstate maps of the names given, none for an empty name.
func NewConfig(owner *agent.Agent, name string, rules []cacl.ACLRule, fw4MapName, fw6MapName string) (*layout.CpModule, error) {
	size := unsafe.Sizeof(layout.AclModuleConfig{})
	block := shmalloc.ContextAlloc(&owner.Agent.MemoryContext, size)
	if block == nil {
		return nil, fmt.Errorf("allocate acl config: %w", cpconfig.ErrNoMemory)
	}
	config := (*layout.AclModuleConfig)(block)
	*config = layout.AclModuleConfig{}
	if err := cpconfig.InitModule(&config.CpModule, owner, "acl", name); err != nil {
		shmalloc.ContextFree(&owner.Agent.MemoryContext, block, size)
		return nil, err
	}
	config.V4ObjectLinkIdx = objectLinkNone
	config.V6ObjectLinkIdx = objectLinkNone
	if err := build(config, rules, fw4MapName, fw6MapName); err != nil {
		if destroyErr := Free(&config.CpModule); destroyErr != nil {
			return nil, fmt.Errorf("%w; free the partial config: %w", err, destroyErr)
		}
		return nil, err
	}
	return &config.CpModule, nil
}

// build fills a fresh configuration, as acl_module_config_init does
// after cp_module_init; on failure the configuration holds what it
// built so far, which Free releases.
func build(config *layout.AclModuleConfig, rules []cacl.ACLRule, fw4MapName, fw6MapName string) error {
	module := &config.CpModule
	for _, counter := range []struct {
		Name string
		Size uint64
		ID   *uint64
	}{
		{"acl_no_match", 1, &config.NoMatchCounterId},
		{"acl_action_allow", 1, &config.ActionAllowCounterId},
		{"acl_action_deny", 1, &config.ActionDenyCounterId},
		{"acl_action_check_pass", 1, &config.ActionCheckPassCounterId},
		{"acl_action_check_miss", 1, &config.ActionCheckMissCounterId},
		{"acl_action_create_state", 1, &config.ActionCreateStateCounterId},
		{"acl_action_invalid", 1, &config.ActionInvalidCounterId},
		{"acl_action_non_term", 1, &config.ActionNonTermCounterId},
		{"acl_sync_sent", 2, &config.SyncSentCounterId},
		{"acl_sync_overflow", 1, &config.SyncOverflowCounterId},
	} {
		id, err := cpconfig.RegisterCounter(&module.CounterRegistry, counter.Name, counter.Size)
		if err != nil {
			return fmt.Errorf("register counter %q: %w", counter.Name, err)
		}
		*counter.ID = id
	}

	ruleset, err := newRuleset(module, rules)
	if err != nil {
		return err
	}
	if err := buildTargets(config, rules); err != nil {
		return err
	}

	start := time.Now()
	if err := buildL2(config, ruleset); err != nil {
		return fmt.Errorf("init filter_l2: %w", err)
	}
	core4, err := buildIP4(config, ruleset)
	if err != nil {
		return fmt.Errorf("init filter_ip4: %w", err)
	}
	if err := buildPaths(module, ruleset, core4, paths4(config)); err != nil {
		return fmt.Errorf("init the ip4 paths: %w", err)
	}
	core6, err := buildIP6(config, ruleset)
	if err != nil {
		return fmt.Errorf("init filter_ip6: %w", err)
	}
	if err := buildPaths(module, ruleset, core6, paths6(config)); err != nil {
		return fmt.Errorf("init the ip6 paths: %w", err)
	}
	config.CompilationTimeNs = uint64(time.Since(start).Nanoseconds())

	if fw4MapName != "" {
		if config.V4ObjectLinkIdx, err = cpconfig.LinkObject(module, fwstateMapV4, fw4MapName); err != nil {
			return err
		}
	}
	if fw6MapName != "" {
		if config.V6ObjectLinkIdx, err = cpconfig.LinkObject(module, fwstateMapV6, fw6MapName); err != nil {
			return err
		}
	}
	return nil
}

// buildTargets writes the target of every rule - its actions and its
// counter in the module's "rules" counter registry - as
// acl_module_compile_rules does.
func buildTargets(config *layout.AclModuleConfig, rules []cacl.ACLRule) error {
	module := &config.CpModule
	context := &module.Entity.MemoryContext
	if len(rules) > 0 {
		size := unsafe.Sizeof(layout.AclTarget{}) * uintptr(len(rules))
		block := shmalloc.ContextAlloc(context, size)
		if block == nil {
			return fmt.Errorf("allocate targets: %w", cpconfig.ErrNoMemory)
		}
		clear(unsafe.Slice((*byte)(block), size))
		config.Targets.Set((*layout.AclTarget)(block))
		config.TargetCount = uint64(len(rules))
	}
	registry, index, err := cpconfig.ModuleCounterRegistry(module, "rules")
	if err != nil {
		return err
	}
	config.RulesRegistryIdx = index
	targets := unsafe.Slice(config.Targets.Get(), len(rules))
	for idx, rule := range rules {
		if len(rule.Actions) > maxActions {
			return fmt.Errorf("rule %d holds %d actions, at most %d", idx, len(rule.Actions), maxActions)
		}
		for action, item := range rule.Actions {
			kind, ok := targetActions[item.Kind]
			if !ok {
				return fmt.Errorf("rule %d: unknown action kind %d", idx, item.Kind)
			}
			targets[idx].Actions[action] = kind
		}
		targets[idx].ActionCount = uint64(len(rule.Actions))
		counter := rule.Counter
		if counter == "" {
			counter = fmt.Sprintf("rule %d", idx)
		}
		if targets[idx].CounterId, err = cpconfig.RegisterCounter(registry, counter, 2); err != nil {
			return fmt.Errorf("register counter %q of rule %d: %w", counter, idx, err)
		}
	}
	return nil
}

// ruleset is the rules as the classifier attributes read them, with the
// projections the paths select, one membership flag a rule.
type ruleset struct {
	Rules []rule
	// L2 holds the rules without a network, Has4 and Has6 those with
	// networks of the family on both sides.
	L2, Has4, Has6 []bool
	// Plain, TCP, UDP and ICMP hold the rules of each path.
	Plain, TCP, UDP, ICMP []bool
	// PortsRestricted holds the TCP and UDP rules restricting a port
	// side; TCPVacuous and UDPVacuous the rest of each path.
	PortsRestricted, TCPVacuous, UDPVacuous []bool
}

// rule is one rule as the classifier attributes read it.
type rule struct {
	Devices             []uint32
	Src4, Dst4          []netip.Prefix
	Src6, Dst6          []classify.Net6Network
	IPProto             []classify.LineRange
	TCPFlags, ICMPTypes []classify.LineRange
	SrcPorts, DstPorts  []classify.PortRange
	Fragment            classify.Fragment
}

// newRuleset converts the rules, linking every device a rule names to
// the module in rule order, as acl_module_compile_rules and the cacl
// binding do.
func newRuleset(module *layout.CpModule, rules []cacl.ACLRule) (*ruleset, error) {
	set := &ruleset{Rules: make([]rule, len(rules))}
	for _, mask := range []*[]bool{
		&set.L2, &set.Has4, &set.Has6, &set.Plain, &set.TCP, &set.UDP, &set.ICMP,
		&set.PortsRestricted, &set.TCPVacuous, &set.UDPVacuous,
	} {
		*mask = make([]bool, len(rules))
	}
	for idx := range rules {
		source := &rules[idx]
		converted := &set.Rules[idx]
		for _, device := range source.Devices {
			id, err := cpconfig.LinkDevice(module, device.Name)
			if err != nil {
				return nil, fmt.Errorf("link device %q of rule %d: %w", device.Name, idx, err)
			}
			converted.Devices = append(converted.Devices, uint32(id))
		}
		for _, network := range source.Src4s {
			converted.Src4 = append(converted.Src4, prefix4(network.Network().Addr().As4(), network.Network().Mask().As4()))
		}
		for _, network := range source.Dst4s {
			converted.Dst4 = append(converted.Dst4, prefix4(network.Network().Addr().As4(), network.Network().Mask().As4()))
		}
		for _, network := range source.Src6s {
			converted.Src6 = append(converted.Src6, classify.Net6Network{Addr: network.Network().Addr().As16(), Mask: network.Network().Mask().As16()})
		}
		for _, network := range source.Dst6s {
			converted.Dst6 = append(converted.Dst6, classify.Net6Network{Addr: network.Network().Addr().As16(), Mask: network.Network().Mask().As16()})
		}
		split := cacl.SplitProtoRanges(source.ProtoRanges)
		converted.IPProto = lineRanges(split.IpProto)
		converted.TCPFlags = lineRanges(split.TCPFlags)
		converted.ICMPTypes = lineRanges(split.ICMPTypes)
		converted.SrcPorts = portRanges(source.SrcPortRanges)
		converted.DstPorts = portRanges(source.DstPortRanges)
		converted.Fragment = fragment(source.Fragment)
		restricted := !portSideAny(converted.SrcPorts) || !portSideAny(converted.DstPorts)

		has4 := len(converted.Src4) > 0 && len(converted.Dst4) > 0
		has6 := len(converted.Src6) > 0 && len(converted.Dst6) > 0
		flags := cacl.RulePathMembership(source.ProtoRanges, source.SrcPortRanges, source.DstPortRanges, source.Fragment)
		set.L2[idx] = len(converted.Src4) == 0 && len(converted.Dst4) == 0 && len(converted.Src6) == 0 && len(converted.Dst6) == 0
		set.Has4[idx] = has4
		set.Has6[idx] = has6
		set.Plain[idx] = flags.Plain
		set.TCP[idx] = flags.TCP
		set.UDP[idx] = flags.UDP
		set.ICMP[idx] = flags.ICMP
		set.PortsRestricted[idx] = (flags.TCP || flags.UDP) && restricted
		set.TCPVacuous[idx] = flags.TCP && !restricted
		set.UDPVacuous[idx] = flags.UDP && !restricted
	}
	return set, nil
}

// prefix4 returns the IPv4 network of an address and a contiguous mask,
// the address as the rule spells it.
func prefix4(addr, mask [4]byte) netip.Prefix {
	bits := 0
	for _, octet := range mask {
		for ; octet&0x80 != 0; octet <<= 1 {
			bits++
		}
	}
	return netip.PrefixFrom(netip.AddrFrom4(addr), bits)
}

func lineRanges(ranges []cacl.LineRange) []classify.LineRange {
	out := make([]classify.LineRange, len(ranges))
	for idx, span := range ranges {
		out[idx] = classify.LineRange{From: span.From, To: span.To}
	}
	return out
}

func portRanges(ranges filter.PortRanges) []classify.PortRange {
	out := make([]classify.PortRange, len(ranges))
	for idx, span := range ranges {
		out[idx] = classify.PortRange{From: span.From, To: span.To}
	}
	return out
}

func fragment(value filter.Fragment) classify.Fragment {
	switch value {
	case filter.FragmentNone:
		return classify.FragmentNone
	case filter.FragmentFrag:
		return classify.FragmentFrag
	default:
		return classify.FragmentAny
	}
}

// portSideAny reports whether a port side is vacuous: no ranges, or a
// first range spanning the whole domain, as the u16 compile judges it.
func portSideAny(ranges []classify.PortRange) bool {
	return len(ranges) == 0 || ranges[0].To-ranges[0].From == 65535
}

// count returns the rules a projection holds.
func count(mask []bool) uint64 {
	var total uint64
	for _, member := range mask {
		if member {
			total++
		}
	}
	return total
}

// Devices returns the device attribute rules of a projection.
func (m *ruleset) Devices(mask []bool) []classify.DeviceRule {
	out := make([]classify.DeviceRule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.DeviceRule{IDs: m.Rules[idx].Devices, Absent: !mask[idx]}
	}
	return out
}

// Net4s returns one side's IPv4 network rules of a projection.
func (m *ruleset) Net4s(mask []bool, side func(*rule) []netip.Prefix) []classify.Net4Rule {
	out := make([]classify.Net4Rule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.Net4Rule{Prefixes: side(&m.Rules[idx]), Absent: !mask[idx]}
	}
	return out
}

// Net6s returns one side's IPv6 network rules of a projection.
func (m *ruleset) Net6s(mask []bool, side func(*rule) []classify.Net6Network) []classify.Net6Rule {
	out := make([]classify.Net6Rule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.Net6Rule{Networks: side(&m.Rules[idx]), Absent: !mask[idx]}
	}
	return out
}

// Lines returns the line attribute rules of a projection.
func (m *ruleset) Lines(mask []bool, side func(*rule) []classify.LineRange) []classify.LineRule {
	out := make([]classify.LineRule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.LineRule{Ranges: side(&m.Rules[idx]), Absent: !mask[idx]}
	}
	return out
}

// Ports returns one side's port rules of a projection.
func (m *ruleset) Ports(mask []bool, side func(*rule) []classify.PortRange) []classify.PortRule {
	out := make([]classify.PortRule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.PortRule{Ranges: side(&m.Rules[idx]), Absent: !mask[idx]}
	}
	return out
}

// Fragments returns the fragment attribute rules of a projection.
func (m *ruleset) Fragments(mask []bool) []classify.FragmentRule {
	out := make([]classify.FragmentRule, len(m.Rules))
	for idx := range m.Rules {
		out[idx] = classify.FragmentRule{Fragment: m.Rules[idx].Fragment, Absent: !mask[idx]}
	}
	return out
}

// buildL2 builds the l2 classifier and filter: the device attribute over
// the rules without networks, decoded straight into the rule map, as
// acl_module_build_l2 and acl_module_derive_l2 do.
func buildL2(config *layout.AclModuleConfig, set *ruleset) error {
	context := &config.CpModule.Entity.MemoryContext
	device := classify.CompileDevice(set.Devices(set.L2))
	if err := device.Commit(&config.ClassifierL2.DevAttr, context, "l2:device"); err != nil {
		return err
	}
	config.FilterRuleCountL2 = count(set.L2)
	return classify.Decode(&device.Stage, set.L2, &config.FilterL2.RuleMap, context, "l2:rules")
}

// core is the classifier of one family the paths join onto: the joint
// of the device, the networks and the protocol, and its stage.
type core struct {
	Joint *classify.Joint
	Stage *classify.Stage
}

// buildIP4 builds the IPv4 core classifier and the plain IPv4 filter, as
// acl_module_build_ip4_classifier and acl_module_derive_ip4 do.
func buildIP4(config *layout.AclModuleConfig, set *ruleset) (*core, error) {
	context := &config.CpModule.Entity.MemoryContext
	classifier := &config.ClassifierCore4
	device := classify.CompileDevice(set.Devices(set.Has4))
	if err := device.Commit(&classifier.DevAttr, context, "ip4:device"); err != nil {
		return nil, err
	}
	src, err := classify.CompileNet4(set.Net4s(set.Has4, func(item *rule) []netip.Prefix { return item.Src4 }))
	if err != nil {
		return nil, err
	}
	if err := src.Commit(&classifier.Net4SrcAttr, context, "ip4:net4_src"); err != nil {
		return nil, err
	}
	dst, err := classify.CompileNet4(set.Net4s(set.Has4, func(item *rule) []netip.Prefix { return item.Dst4 }))
	if err != nil {
		return nil, err
	}
	if err := dst.Commit(&classifier.Net4DstAttr, context, "ip4:net4_dst"); err != nil {
		return nil, err
	}
	return buildCore(context, set, &device.Stage, &src.Stage, &dst.Stage, coreTables{
		Prefix:     "ip4",
		Members:    set.Has4,
		IPProto:    &classifier.IpprotoAttr,
		Nets:       &classifier.NetsJoint,
		Mid:        &classifier.MidJoint,
		Proto:      &classifier.ProtoJoint,
		Fragment:   &config.ClassifierFrag4.FragAttr,
		Root:       &config.FilterIp4.RootJoint,
		RuleMap:    &config.FilterIp4.RuleMap,
		PlainCount: &config.FilterRuleCountIp4,
	})
}

// buildIP6 builds the IPv6 core classifier and the plain IPv6 filter, as
// acl_module_build_ip6_classifier and acl_module_derive_ip6 do.
func buildIP6(config *layout.AclModuleConfig, set *ruleset) (*core, error) {
	context := &config.CpModule.Entity.MemoryContext
	classifier := &config.ClassifierCore6
	device := classify.CompileDevice(set.Devices(set.Has6))
	if err := device.Commit(&classifier.DevAttr, context, "ip6:device"); err != nil {
		return nil, err
	}
	src, err := classify.CompileNet6(set.Net6s(set.Has6, func(item *rule) []classify.Net6Network { return item.Src6 }),
		&classifier.Net6SrcAttr, context, "ip6:net6_src",
	)
	if err != nil {
		return nil, err
	}
	dst, err := classify.CompileNet6(set.Net6s(set.Has6, func(item *rule) []classify.Net6Network { return item.Dst6 }),
		&classifier.Net6DstAttr, context, "ip6:net6_dst",
	)
	if err != nil {
		return nil, err
	}
	return buildCore(context, set, &device.Stage, src, dst, coreTables{
		Prefix:     "ip6",
		Members:    set.Has6,
		IPProto:    &classifier.IpprotoAttr,
		Nets:       &classifier.NetsJoint,
		Mid:        &classifier.MidJoint,
		Proto:      &classifier.ProtoJoint,
		Fragment:   &config.ClassifierFrag6.FragAttr,
		Root:       &config.FilterIp6.RootJoint,
		RuleMap:    &config.FilterIp6.RuleMap,
		PlainCount: &config.FilterRuleCountIp6,
	})
}

// coreTables are where a family's core classifier and plain filter go.
type coreTables struct {
	Prefix     string
	Members    []bool
	IPProto    *layout.ClassifyAttrLine
	Nets       *layout.ValueTable
	Mid        *layout.ValueTable
	Proto      *layout.ValueTable
	Fragment   *layout.ClassifyAttrIpfrag
	Root       *layout.ValueTable
	RuleMap    *layout.Vline
	PlainCount *uint64
}

// buildCore joins a family's devices, networks and protocols into its
// core, then joins the fragment attribute of the plain rules onto it
// into the plain filter's root and decodes its rule map.
func buildCore(context *layout.MemoryContext, set *ruleset, device, src, dst *classify.Stage, tables coreTables) (*core, error) {
	nets, err := classify.Join(src, dst, tables.Nets, context, tables.Prefix+":nets_joint")
	if err != nil {
		return nil, err
	}
	mid, err := classify.Join(device, nets.Stage(), tables.Mid, context, tables.Prefix+":mid_joint")
	if err != nil {
		return nil, err
	}
	ipproto := classify.CompileLine(set.Lines(tables.Members, func(item *rule) []classify.LineRange { return item.IPProto }), classify.LineDomain)
	if err := ipproto.Commit(tables.IPProto, context, tables.Prefix+":ipproto"); err != nil {
		return nil, err
	}
	proto, err := classify.Join(mid.Stage(), &ipproto.Stage, tables.Proto, context, tables.Prefix+":proto_joint")
	if err != nil {
		return nil, err
	}
	family := &core{Joint: proto, Stage: proto.Stage()}

	fragment := classify.CompileFragment(set.Fragments(set.Plain))
	if err := fragment.Commit(tables.Fragment, context, tables.Prefix+":ipfrag"); err != nil {
		return nil, err
	}
	root, err := classify.Join(family.Stage, &fragment.Stage, tables.Root, context, tables.Prefix+":root_joint")
	if err != nil {
		return nil, err
	}
	*tables.PlainCount = count(set.Plain)
	if err := classify.Decode(root.Stage(), set.Plain, tables.RuleMap, context, tables.Prefix+":rules"); err != nil {
		return nil, err
	}
	return family, nil
}

// pathTables are where a family's protocol paths go.
type pathTables struct {
	Prefix    string
	Ports     *layout.AclClassifierPorts
	TCP       *layout.AclClassifierTcp
	ICMP      *layout.AclClassifierIcmp
	TCPRoot   *layout.RuleTable
	TCPCount  *uint64
	UDPRoot   *layout.RuleTable
	UDPCount  *uint64
	ICMPRoot  *layout.RuleTable
	ICMPCount *uint64
	TCPVac    *layout.RuleTable
	UDPVacMap *layout.Vline
}

func paths4(config *layout.AclModuleConfig) pathTables {
	return pathTables{
		Prefix:    "ip4",
		Ports:     &config.ClassifierPorts4,
		TCP:       &config.ClassifierTcp4,
		ICMP:      &config.ClassifierIcmp4,
		TCPRoot:   &config.FilterIp4Tcp.RootJoint,
		TCPCount:  &config.FilterRuleCountIp4Tcp,
		UDPRoot:   &config.FilterIp4Udp.RootJoint,
		UDPCount:  &config.FilterRuleCountIp4Udp,
		ICMPRoot:  &config.FilterIp4Icmp.RootJoint,
		ICMPCount: &config.FilterRuleCountIp4Icmp,
		TCPVac:    &config.FilterIp4Tcp.VacJoint,
		UDPVacMap: &config.FilterIp4Udp.VacRuleMap,
	}
}

func paths6(config *layout.AclModuleConfig) pathTables {
	return pathTables{
		Prefix:    "ip6",
		Ports:     &config.ClassifierPorts6,
		TCP:       &config.ClassifierTcp6,
		ICMP:      &config.ClassifierIcmp6,
		TCPRoot:   &config.FilterIp6Tcp.RootJoint,
		TCPCount:  &config.FilterRuleCountIp6Tcp,
		UDPRoot:   &config.FilterIp6Udp.RootJoint,
		UDPCount:  &config.FilterRuleCountIp6Udp,
		ICMPRoot:  &config.FilterIp6Icmp.RootJoint,
		ICMPCount: &config.FilterRuleCountIp6Icmp,
		TCPVac:    &config.FilterIp6Tcp.VacJoint,
		UDPVacMap: &config.FilterIp6Udp.VacRuleMap,
	}
}

// buildPaths builds a family's protocol paths onto its core, as
// acl_module_build_paths does: the ports pair over the port restricted
// rules, the UDP, TCP and ICMP root rule tables, and the without-ports
// decodings of the vacuous port rules.
func buildPaths(module *layout.CpModule, set *ruleset, family *core, tables pathTables) error {
	context := &module.Entity.MemoryContext
	name := func(role string) string { return tables.Prefix + ":" + role }

	src := classify.CompilePorts(set.Ports(set.PortsRestricted, func(item *rule) []classify.PortRange { return item.SrcPorts }))
	if err := src.Commit(&tables.Ports.SrcAttr, context, name("src_port")); err != nil {
		return err
	}
	dst := classify.CompilePorts(set.Ports(set.PortsRestricted, func(item *rule) []classify.PortRange { return item.DstPorts }))
	if err := dst.Commit(&tables.Ports.DstAttr, context, name("dst_port")); err != nil {
		return err
	}
	ports, err := classify.Join(&src.Stage, &dst.Stage, &tables.Ports.Joint, context, name("ports_joint"))
	if err != nil {
		return err
	}
	portsStage := ports.Stage()

	*tables.UDPCount = count(set.UDP)
	if err := classify.JoinRules(family.Joint, portsStage, set.UDP, tables.UDPRoot, context, name("udp_root_joint")); err != nil {
		return err
	}

	flags := classify.CompileLine(set.Lines(set.TCP, func(item *rule) []classify.LineRange { return item.TCPFlags }), classify.LineDomain)
	if err := flags.Commit(&tables.TCP.FlagsAttr, context, name("tcp_flags")); err != nil {
		return err
	}
	tcpMid, err := classify.Join(portsStage, &flags.Stage, &tables.TCP.FlagsJoint, context, name("flags_joint"))
	if err != nil {
		return err
	}
	*tables.TCPCount = count(set.TCP)
	if err := classify.JoinRules(family.Joint, tcpMid.Stage(), set.TCP, tables.TCPRoot, context, name("tcp_root_joint")); err != nil {
		return err
	}

	types := classify.CompileLine(set.Lines(set.ICMP, func(item *rule) []classify.LineRange { return item.ICMPTypes }), classify.LineDomain)
	if err := types.Commit(&tables.ICMP.TypeAttr, context, name("icmp_type")); err != nil {
		return err
	}
	*tables.ICMPCount = count(set.ICMP)
	if err := classify.JoinRules(family.Joint, &types.Stage, set.ICMP, tables.ICMPRoot, context, name("icmp_root_joint")); err != nil {
		return err
	}

	if count(set.TCPVacuous) > 0 {
		if err := classify.JoinRules(family.Joint, &flags.Stage, set.TCPVacuous, tables.TCPVac, context, name("tcp_vac_joint")); err != nil {
			return err
		}
	}
	if count(set.UDPVacuous) > 0 {
		if err := classify.Decode(family.Stage, set.UDPVacuous, tables.UDPVacMap, context, name("udp_vac_rules")); err != nil {
			return err
		}
	}
	return nil
}
