//go:build linux && (amd64 || arm64)

package classify

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
)

// Net4Rule is what one rule matches on an IPv4 network attribute.
type Net4Rule struct {
	// Prefixes are the IPv4 networks the rule matches; a rule without
	// any, or whose first one is 0.0.0.0/0, matches every address.
	Prefixes []netip.Prefix
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Net4 is a compiled IPv4 network attribute: the address space cut into
// stretches of one value each, and the stage a join consumes.
type Net4 struct {
	// Starts holds the first address of every stretch, ascending.
	Starts []uint32
	// Values holds the region value of every stretch; neighbours differ.
	Values []uint32
	Stage  Stage
}

// network is an IPv4 prefix as the compile reads it: its first and last
// address and its length.
type network struct {
	From uint32
	To   uint32
	Bits uint8
}

func (m network) Bounds() (uint32, uint32) {
	return m.From, m.To
}

// newNetwork returns the network of a masked first address and a length.
func newNetwork(from uint32, bits uint8) network {
	return network{From: from, To: from | uint32(uint64(1)<<(32-bits)-1), Bits: bits}
}

// CompileNet4 compiles the IPv4 network attribute of a ruleset to the
// lookups and the stage classify_net4_compile produces.
//
// The address space is split into classes as range_collector splits it,
// and they are numbered alike: a class is one prefix's span with the
// prefixes nested in it carved out, so its pieces need not be adjacent.
// The tree committed holds the final values only, so neighbouring
// classes ending with one value share its slots.
func CompileNet4(rules []Net4Rule) (*Net4, error) {
	nets, starts, err := normalizeNet4Rules(rules)
	if err != nil {
		return nil, err
	}
	netsOf := func(idx int) []network {
		return nets[starts[idx]:starts[idx+1]]
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]network, bool) {
		return netsOf(idx), rules[idx].Absent
	})

	// A key packs the first address of a network, its length and its
	// place in nets: the sorted keys sort the networks by first address,
	// then length, and tell every place the distinct network it holds.
	if len(nets) > placeMask {
		return nil, fmt.Errorf("%d networks exceed %d", len(nets), placeMask)
	}
	var keys []uint64
	for _, first := range firsts {
		for place := starts[first]; place < starts[first+1]; place++ {
			if net := nets[place]; net.Bits > 0 {
				keys = append(keys, uint64(net.From)<<32|uint64(net.Bits)<<24|uint64(place))
			}
		}
	}
	slices.Sort(keys)
	var distinct []network
	distinctOf := make([]uint32, len(nets))
	for _, key := range keys {
		net := newNetwork(uint32(key>>32), uint8(key>>24))
		if count := len(distinct); count == 0 || distinct[count-1] != net {
			distinct = append(distinct, net)
		}
		distinctOf[key&placeMask] = uint32(len(distinct) - 1)
	}
	intervals := collectClasses(distinct)
	whole := intervalSpan{End: uint32(len(intervals.Starts))}
	// spanAt returns the intervals the network at a place covers.
	spanAt := func(place int) intervalSpan {
		if nets[place].Bits == 0 {
			return whole
		}
		return intervals.Spans[distinctOf[place]]
	}

	classValues := make([]uint32, intervals.ClassCount)
	regions := newRefiner(intervals.ClassCount)
	for _, first := range firsts {
		rule := netsOf(int(first))
		if net4RuleIsAny(rule) {
			continue
		}
		regions.NewGeneration()
		for place := starts[first]; place < starts[first+1]; place++ {
			span := spanAt(place)
			for idx := span.First; idx < span.End; idx++ {
				class := intervals.Classes[idx]
				classValues[class] = regions.Touch(classValues[class])
			}
		}
	}
	numbers, count := regions.Numbers()
	for class := range classValues {
		classValues[class] = numbers[classValues[class]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		rule := netsOf(int(firsts[group]))
		if net4RuleIsAny(rule) {
			for _, value := range classValues {
				builder.Reach(value)
			}
			return
		}
		for place := starts[firsts[group]]; place < starts[firsts[group]+1]; place++ {
			span := spanAt(place)
			for idx := span.First; idx < span.End; idx++ {
				builder.Reach(classValues[intervals.Classes[idx]])
			}
		}
	})

	compiled := &Net4{Stage: stage}
	for idx, start := range intervals.Starts {
		value := classValues[intervals.Classes[idx]]
		if count := len(compiled.Values); count > 0 && compiled.Values[count-1] == value {
			continue
		}
		compiled.Starts = append(compiled.Starts, start)
		compiled.Values = append(compiled.Values, value)
	}
	return compiled, nil
}

// normalizeNet4Rules returns the networks of every rule in one slice,
// rule after rule, and where every rule's networks begin, one more entry
// where the last rule's end.
func normalizeNet4Rules(rules []Net4Rule) ([]network, []int, error) {
	total := 0
	for _, rule := range rules {
		total += len(rule.Prefixes)
	}
	nets := make([]network, 0, total)
	starts := make([]int, 0, len(rules)+1)
	for idx, rule := range rules {
		starts = append(starts, len(nets))
		for _, prefix := range rule.Prefixes {
			if !prefix.Addr().Is4() {
				return nil, nil, fmt.Errorf("rule %d: %s is not an IPv4 prefix", idx, prefix)
			}
			addr := prefix.Masked().Addr().As4()
			nets = append(nets, newNetwork(binary.BigEndian.Uint32(addr[:]), uint8(prefix.Bits())))
		}
	}
	starts = append(starts, len(nets))
	return nets, starts, nil
}

// net4RuleIsAny reports whether a rule matches every address, judged as
// C judges it: by its first network alone.
func net4RuleIsAny(nets []network) bool {
	return len(nets) == 0 || nets[0].Bits == 0
}

// placeMask masks the place of a network in a sort key.
const placeMask = 1<<24 - 1

// classIntervals is the address space cut into intervals, ascending,
// each with the class it belongs to, and the intervals every network
// covers.
type classIntervals struct {
	Starts     []uint32
	Classes    []uint32
	ClassCount uint32
	Spans      []intervalSpan
}

// intervalSpan is a run of intervals, from First up to End.
type intervalSpan struct {
	First uint32
	End   uint32
}

// collectClasses cuts the address space by distinct networks sorted by
// first address then length, numbering the classes as
// range_collector_collect does.
//
// A stack holds the networks containing the walk position, innermost on
// top, with the class each has emitted under; the class of a network is
// numbered when it first emits an interval, and a network ending where
// the one around it ends renews that one's class instead of stacking.
// A network covers the intervals from its push up to its frame's pop.
func collectClasses(nets []network) classIntervals {
	const unnumbered = ^uint32(0)
	type frame struct {
		Class uint32
		To    uint64
		// Pending is where the networks the frame ends begin in pending.
		Pending int
	}
	collected := classIntervals{Spans: make([]intervalSpan, len(nets))}
	stack := make([]frame, 0, 33)
	var pending []uint32
	position := uint64(0)
	emit := func(to uint64) {
		top := &stack[len(stack)-1]
		if top.Class == unnumbered {
			top.Class = collected.ClassCount
			collected.ClassCount++
		}
		collected.Starts = append(collected.Starts, uint32(position))
		collected.Classes = append(collected.Classes, top.Class)
		position = to + 1
	}
	pop := func() {
		top := stack[len(stack)-1]
		emit(top.To)
		for _, net := range pending[top.Pending:] {
			collected.Spans[net].End = uint32(len(collected.Starts))
		}
		pending = pending[:top.Pending]
		stack = stack[:len(stack)-1]
	}
	stack = append(stack, frame{Class: unnumbered, To: 1<<32 - 1})
	for idx, net := range nets {
		from, to := uint64(net.From), uint64(net.To)
		for stack[len(stack)-1].To < from {
			pop()
		}
		if position < from {
			emit(from - 1)
		}
		collected.Spans[idx].First = uint32(len(collected.Starts))
		if top := &stack[len(stack)-1]; top.To == to {
			top.Class = unnumbered
		} else {
			stack = append(stack, frame{Class: unnumbered, To: to, Pending: len(pending)})
		}
		pending = append(pending, uint32(idx))
	}
	for len(stack) > 0 {
		pop()
	}
	return collected
}

// Value returns the region value of an address.
func (m *Net4) Value(addr netip.Addr) uint32 {
	key := addr.As4()
	stretch, found := slices.BinarySearch(m.Starts, binary.BigEndian.Uint32(key[:]))
	if !found {
		stretch--
	}
	return m.Values[stretch]
}

// Commit writes the attribute into shared memory as the C compile
// commits it: a prefix tree of every address's value with its memory
// context a child of context named for the attribute.
func (m *Net4) Commit(attr *layout.ClassifyAttrNet4, context *layout.MemoryContext, name string) error {
	tree := &attr.Lpm
	if err := lpm.Init(tree, context, leafName(name, "lpm")); err != nil {
		return err
	}
	var from, to [4]byte
	for idx, start := range m.Starts {
		end := ^uint32(0)
		if idx+1 < len(m.Starts) {
			end = m.Starts[idx+1] - 1
		}
		binary.BigEndian.PutUint32(from[:], start)
		binary.BigEndian.PutUint32(to[:], end)
		if err := lpm.Insert(tree, from[:], to[:], m.Values[idx]); err != nil {
			lpm.Free(tree)
			return err
		}
	}
	return nil
}

// FreeNet4 releases what Commit wrote into an IPv4 network attribute.
func FreeNet4(attr *layout.ClassifyAttrNet4) {
	lpm.Free(&attr.Lpm)
}
