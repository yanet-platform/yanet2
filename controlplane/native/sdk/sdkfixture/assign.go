//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// assignType is the dataplane module an assign configuration configures.
const assignType = "assign"

// trafficValues is the count of values of a traffic counter: packets and
// bytes.
const trafficValues = 2

// protoDomain is the domain of the protocol line: a byte.
const protoDomain = 0x100

// AssignFamily is the family of the packets an assign rule matches.
type AssignFamily uint8

const (
	// AssignAny matches packets of either family by protocol and port.
	AssignAny AssignFamily = iota
	// AssignIPv4 matches IPv4 packets.
	AssignIPv4
	// AssignIPv6 matches IPv6 packets.
	AssignIPv6
)

// AssignRule is a rule of an assign configuration.
type AssignRule struct {
	Family AssignFamily
	// Src and Dst are the networks of the rule's family the source and the
	// destination address are in; none matches every address. A rule of
	// either family has its networks not read.
	Src, Dst []netip.Prefix
	// Protos and Ports are the protocols and the destination ports; none
	// matches every value.
	Protos []classify.LineRange
	Ports  []classify.PortRange
	// Pass passes the packets the rule is the first match of; otherwise
	// it drops them.
	Pass bool
	// Counter names the counter of the rule's packets.
	Counter string
}

// transport is the joint of the protocol and the destination port.
type transport = Join[layout.ClassifyAttrLine, layout.ClassifyAttrPort]

// NewAssignConfig builds an assign configuration named name in the
// agent's memory, as the SDK's build of the fixture's assign module
// does: a classifier tree of each family's rules, one of the rules of
// either family, and an action with a counter per rule.
//
// A name its field cannot hold, a rule of an unknown family and a range
// that ends before it starts are refused before anything is allocated; a
// network of another family than its rule's, as its compile meets it.
// Nodes are allocated parent first, so a failure frees what the build
// took through the configuration's own free; an exhausted agent fails
// with the allocator's out-of-memory error.
func NewAssignConfig(owner *agent.Agent, name string, rules []AssignRule) (*layout.CpModule, error) {
	for idx, rule := range rules {
		if err := checkAssignRule(rule); err != nil {
			return nil, fmt.Errorf("rule %d of assign module %q: %w", idx, name, err)
		}
	}
	build := func(config *moduleBlock[AssignConfig]) error {
		return buildAssign(config, rules)
	}
	return newModule(owner, assignType, name, build, destroyAssign)
}

// FreeAssignConfig frees an assign configuration no generation holds.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func FreeAssignConfig(module *layout.CpModule) error {
	return freeModule(module, destroyAssign)
}

// checkAssignRule refuses a rule of an unknown family, and a range that
// ends before it starts, which the SDK's input views refuse and the
// compiles would read as matching nothing.
func checkAssignRule(rule AssignRule) error {
	if rule.Family > AssignIPv6 {
		return fmt.Errorf("unknown family %d: %w", rule.Family, classify.ErrInvalidRule)
	}
	for _, lines := range rule.Protos {
		if lines.From > lines.To {
			return fmt.Errorf("protocols %d-%d: %w", lines.From, lines.To, classify.ErrInvalidRule)
		}
	}
	for _, ports := range rule.Ports {
		if ports.From > ports.To {
			return fmt.Errorf("ports %d-%d: %w", ports.From, ports.To, classify.ErrInvalidRule)
		}
	}
	return nil
}

// buildAssign builds the body: the classifiers of IPv4, IPv6 and either
// family, then the actions.
func buildAssign(config *moduleBlock[AssignConfig], rules []AssignRule) error {
	context := &config.Header.Entity.MemoryContext
	body := &config.Body
	if err := buildFamily(context, &body.V4, "assign v4", rules, membersOf(rules, AssignIPv4), compileNet4); err != nil {
		return err
	}
	if err := buildFamily(context, &body.V6, "assign v6", rules, membersOf(rules, AssignIPv6), compileNet6); err != nil {
		return err
	}
	if err := buildAny(context, &body.Any, rules, membersOf(rules, AssignAny)); err != nil {
		return err
	}
	actions, err := newSlice(context, &body.Actions, uint64(len(rules)))
	if err != nil {
		return fmt.Errorf("actions: %w", err)
	}
	for idx, rule := range rules {
		id, err := cpconfig.RegisterCounter(&config.Header.CounterRegistry, rule.Counter, trafficValues)
		if err != nil {
			return fmt.Errorf("counter of rule %d: %w", idx, err)
		}
		actions[idx] = Action{Counter: Counter{ID: id}}
		if rule.Pass {
			actions[idx].Pass = 1
		}
	}
	return nil
}

// membersOf marks the rules of the family.
func membersOf(rules []AssignRule, family AssignFamily) []bool {
	members := make([]bool, len(rules))
	for idx, rule := range rules {
		members[idx] = rule.Family == family
	}
	return members
}

// buildFamily builds a family's classifier into the box, as the SDK's
// fixture composes it: the source and the destination networks, the
// protocol and the port compiled, the two pairs joined, and the joints
// joined into the first member rule of every pair of their classes.
//
// The compile of the networks reads one side of each rule, the source or
// the destination, picked by its side argument.
func buildFamily[N any](
	context *layout.MemoryContext,
	box *Box[Rules[Join[N, N], transport]],
	prefix string,
	rules []AssignRule,
	members []bool,
	compileNet func(
		attr *N,
		rules []AssignRule,
		members []bool,
		side func(AssignRule) []netip.Prefix,
		context *layout.MemoryContext,
		name string,
	) (*classify.Stage, error),
) error {
	node, err := newNode(context, box)
	if err != nil {
		return err
	}
	nets, err := newJoin(context, &node.Left)
	if err != nil {
		return err
	}
	ports, err := newJoin(context, &node.Right)
	if err != nil {
		return err
	}
	src := func(rule AssignRule) []netip.Prefix { return rule.Src }
	srcStage, err := compileNet(nets.Left.Ptr.Get(), rules, members, src, context, prefix+" src")
	if err != nil {
		return err
	}
	dst := func(rule AssignRule) []netip.Prefix { return rule.Dst }
	dstStage, err := compileNet(nets.Right.Ptr.Get(), rules, members, dst, context, prefix+" dst")
	if err != nil {
		return err
	}
	transportStage, err := buildTransport(context, ports, prefix, rules, members)
	if err != nil {
		return err
	}
	netsStage, err := srcStage.Join(dstStage, &nets.Table, context, prefix+" nets")
	if err != nil {
		return err
	}
	return netsStage.JoinRules(transportStage, members, &node.Table, context, prefix+" rules")
}

// buildAny builds the classifier of the rules of either family into the
// box: the protocol and the port joined and decoded into the first
// member rule of every class.
func buildAny(context *layout.MemoryContext, box *Box[Decode[transport]], rules []AssignRule, members []bool) error {
	node, err := newNode(context, box)
	if err != nil {
		return err
	}
	input, err := newJoin(context, &node.Input)
	if err != nil {
		return err
	}
	stage, err := buildTransport(context, input, "assign any", rules, members)
	if err != nil {
		return err
	}
	return stage.Decode(members, &node.Map, context, "assign any rules")
}

// buildTransport compiles the protocols and the ports of the member
// rules into the joint's inputs and joins them.
func buildTransport(context *layout.MemoryContext, node *transport, prefix string, rules []AssignRule, members []bool) (*classify.Stage, error) {
	lines := make([]classify.LineRule, len(rules))
	ports := make([]classify.PortRule, len(rules))
	for idx, rule := range rules {
		lines[idx] = classify.LineRule{Ranges: rule.Protos, Absent: !members[idx]}
		ports[idx] = classify.PortRule{Ranges: rule.Ports, Absent: !members[idx]}
	}
	line, err := classify.CompileLine(lines, protoDomain)
	if err != nil {
		return nil, err
	}
	if err := line.Commit(node.Left.Ptr.Get(), context, prefix+" proto"); err != nil {
		return nil, err
	}
	port := classify.CompilePort(ports)
	if err := port.Commit(node.Right.Ptr.Get(), context, prefix+" port"); err != nil {
		return nil, err
	}
	return line.Stage().Join(port.Stage(), &node.Table, context, prefix+" transport")
}

// compileNet4 compiles one side's IPv4 networks of the member rules into
// the attribute; the compile refuses a network of another family.
func compileNet4(
	attr *layout.ClassifyAttrNet4,
	rules []AssignRule,
	members []bool,
	side func(AssignRule) []netip.Prefix,
	context *layout.MemoryContext,
	name string,
) (*classify.Stage, error) {
	nets := make([]classify.Net4Rule, len(rules))
	for idx, rule := range rules {
		nets[idx] = classify.Net4Rule{Prefixes: side(rule), Absent: !members[idx]}
	}
	compiled, err := classify.CompileNet4(nets)
	if err != nil {
		return nil, err
	}
	return compiled.Stage(), compiled.Commit(attr, context, name)
}

// compileNet6 compiles one side's IPv6 networks of the member rules into
// the attribute, refusing a network of another family.
func compileNet6(
	attr *layout.ClassifyAttrNet6,
	rules []AssignRule,
	members []bool,
	side func(AssignRule) []netip.Prefix,
	context *layout.MemoryContext,
	name string,
) (*classify.Stage, error) {
	nets := make([]classify.Net6Rule, len(rules))
	for idx, rule := range rules {
		nets[idx].Absent = !members[idx]
		if nets[idx].Absent {
			continue
		}
		for _, prefix := range side(rule) {
			if !prefix.IsValid() || !prefix.Addr().Is6() {
				return nil, fmt.Errorf("rule %d: %s is not an IPv6 prefix: %w", idx, prefix, classify.ErrInvalidRule)
			}
			nets[idx].Networks = append(nets[idx].Networks, classify.Net6Network{
				Addr: prefix.Addr().As16(),
				Mask: [16]byte(net.CIDRMask(prefix.Bits(), 128)),
			})
		}
	}
	compiled, err := classify.CompileNet6(nets, attr, context, name)
	if err != nil {
		return nil, err
	}
	return compiled.Stage(), nil
}

// newJoin allocates a zeroed joint into the box and zeroed nodes of both
// its inputs.
func newJoin[L, R any](context *layout.MemoryContext, box *Box[Join[L, R]]) (*Join[L, R], error) {
	node, err := newNode(context, box)
	if err != nil {
		return nil, err
	}
	if _, err := newNode(context, &node.Left); err != nil {
		return nil, err
	}
	if _, err := newNode(context, &node.Right); err != nil {
		return nil, err
	}
	return node, nil
}

// newNode allocates a zeroed node from the context into the box, so that
// a later failure frees it with the box.
func newNode[T any](context *layout.MemoryContext, box *Box[T]) (*T, error) {
	node := shmalloc.AllocNode[T](context, 0)
	if node == nil {
		return nil, shmalloc.ErrNoMemory
	}
	*node = *new(T)
	box.Ptr.Set(node)
	return node, nil
}

// destroyAssign frees what the body holds in the SDK's release order,
// field by field, then the header and the block.
func destroyAssign(config *moduleBlock[AssignConfig]) {
	context := &config.Header.Entity.MemoryContext
	body := &config.Body
	freeFamily(context, &body.V4, classify.FreeNet4)
	freeFamily(context, &body.V6, classify.FreeNet6)
	freeBox(context, &body.Any, func(node *Decode[transport]) {
		classify.FreeDecode(&node.Map)
		freeTransport(context, &node.Input)
	})
	freeSlice(context, &body.Actions)
	finiModule(config)
}

// freeFamily frees a family's classifier the box holds.
func freeFamily[N any](context *layout.MemoryContext, box *Box[Rules[Join[N, N], transport]], freeNet func(*N)) {
	freeBox(context, box, func(node *Rules[Join[N, N], transport]) {
		classify.FreeRuleTable(&node.Table)
		freeTransport(context, &node.Right)
		freeBox(context, &node.Left, func(nets *Join[N, N]) {
			freeJoin(context, nets, freeNet, freeNet)
		})
	})
}

// freeTransport frees the joint of the protocol and the port the box
// holds.
func freeTransport(context *layout.MemoryContext, box *Box[transport]) {
	freeBox(context, box, func(node *transport) {
		freeJoin(context, node, classify.FreeLine, classify.FreePort)
	})
}

// freeJoin frees a joint's table, then its right input and its left one.
func freeJoin[L, R any](context *layout.MemoryContext, node *Join[L, R], freeLeft func(*L), freeRight func(*R)) {
	classify.FreeJoin(&node.Table)
	freeBox(context, &node.Right, freeRight)
	freeBox(context, &node.Left, freeLeft)
}

// freeBox frees what the node a box holds owns, then the node's block,
// and empties the box; an empty box is skipped.
func freeBox[T any](context *layout.MemoryContext, box *Box[T], free func(*T)) {
	node := box.Ptr.Get()
	if node == nil {
		return
	}
	free(node)
	shmalloc.FreeNode(context, node, 0)
	box.Ptr.Set(nil)
}
