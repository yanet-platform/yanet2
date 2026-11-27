//go:build linux && (amd64 || arm64)

package classify

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"unsafe"

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
// stretches of one class each, and the stage a join consumes.
type Net4 struct {
	// starts holds the first address of every stretch, ascending.
	starts []uint32
	// values holds the class of every stretch; neighbours differ.
	values []uint32
	stage  Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *Net4) Stage() *Stage {
	stage := m.stage
	return &stage
}

// network is an IPv4 prefix as the compile reads it: its first and last
// address and its length.
type network struct {
	From uint32
	To   uint32
	Bits uint8
}

// foldNetwork folds a network into a list's hash, as the IPv4 compile's
// hash does: the bytes of the masked address, then those of the mask.
func foldNetwork(hash uint32, item network) uint32 {
	mask := ^uint32(0) << (32 - item.Bits)
	for shift := 24; shift >= 0; shift -= 8 {
		hash = hash*31 + item.From>>shift&0xff
	}
	for shift := 24; shift >= 0; shift -= 8 {
		hash = hash*31 + mask>>shift&0xff
	}
	return hash
}

// CompileNet4 compiles the IPv4 network attribute of a ruleset to the
// lookups and the stage classify_net4_compile produces.
//
// The address space is cut into the elementary intervals of the networks
// and refined as C refines its classes, so every address gets C's class;
// neighbouring intervals of one class share a stretch of the tree.
func CompileNet4(rules []Net4Rule) (*Net4, error) {
	nets, starts, err := normalizeNet4Rules(rules)
	if err != nil {
		return nil, err
	}
	netsOf := func(rule int) []network {
		return nets[starts[rule]:starts[rule+1]]
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]network, bool) {
		return netsOf(idx), rules[idx].Absent
	}, foldNetwork)

	// The networks of the groups sorted by first address, then length,
	// each distinct one once.
	var places []int
	for _, first := range firsts {
		for place := starts[first]; place < starts[first+1]; place++ {
			places = append(places, place)
		}
	}
	slices.SortFunc(places, func(left, right int) int {
		return cmp.Or(cmp.Compare(nets[left].From, nets[right].From), cmp.Compare(nets[left].Bits, nets[right].Bits))
	})
	var distinct []network
	distinctOf := make([]uint32, len(nets))
	for _, place := range places {
		if count := len(distinct); count == 0 || distinct[count-1] != nets[place] {
			distinct = append(distinct, nets[place])
		}
		distinctOf[place] = uint32(len(distinct) - 1)
	}
	intervals := cutIntervals(len(distinct), func(idx int) (uint32, uint32) {
		return distinct[idx].From, distinct[idx].To
	})
	// spanAt returns the intervals the network at a place covers.
	spanAt := func(place int) intervalSpan {
		return intervals.Spans[distinctOf[place]]
	}

	values := make([]uint32, len(intervals.Starts))
	regions := newRefiner(uint32(len(values)))
	for _, first := range firsts {
		if net4RuleIsAny(netsOf(int(first))) {
			continue
		}
		regions.NewGeneration()
		for place := starts[first]; place < starts[first+1]; place++ {
			span := spanAt(place)
			for idx := span.First; idx < span.End; idx++ {
				values[idx] = regions.Touch(values[idx])
			}
		}
	}
	numbers, count := regions.Numbers()
	for idx := range values {
		values[idx] = numbers[values[idx]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		first := firsts[group]
		if net4RuleIsAny(netsOf(int(first))) {
			for _, value := range values {
				builder.Reach(value)
			}
			return
		}
		for place := starts[first]; place < starts[first+1]; place++ {
			span := spanAt(place)
			for _, value := range values[span.First:span.End] {
				builder.Reach(value)
			}
		}
	})

	var stretchStarts, stretchValues []uint32
	for idx, start := range intervals.Starts {
		if count := len(stretchValues); count > 0 && stretchValues[count-1] == values[idx] {
			continue
		}
		stretchStarts = append(stretchStarts, start)
		stretchValues = append(stretchValues, values[idx])
	}
	return &Net4{starts: stretchStarts, values: stretchValues, stage: stage}, nil
}

// normalizeNet4Rules returns the networks of every rule in one slice,
// rule after rule, none for an absent one, and where every rule's
// networks begin, one more entry where the last rule's end.
func normalizeNet4Rules(rules []Net4Rule) ([]network, []int, error) {
	var nets []network
	starts := make([]int, 0, len(rules)+1)
	for idx, rule := range rules {
		starts = append(starts, len(nets))
		if rule.Absent {
			continue
		}
		for _, prefix := range rule.Prefixes {
			if !prefix.IsValid() || !prefix.Addr().Is4() {
				return nil, nil, fmt.Errorf("rule %d: %s is not an IPv4 prefix: %w", idx, prefix, ErrInvalidRule)
			}
			addr := prefix.Masked().Addr().As4()
			from := binary.BigEndian.Uint32(addr[:])
			bits := uint8(prefix.Bits())
			nets = append(nets, network{From: from, To: from | uint32(uint64(1)<<(32-bits)-1), Bits: bits})
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

// intervals is a key space cut into elementary intervals, ascending by
// first key, and the intervals every network covers.
type intervals[K uint32 | uint64] struct {
	Starts []K
	// Classes holds the class of every interval, numbered as
	// range_collector_collect numbers them: the innermost network over
	// the interval, its parts on both sides of a nested one alike.
	Classes    []uint32
	ClassCount uint32
	Spans      []intervalSpan
}

// intervalSpan is a run of intervals, from First up to End.
type intervalSpan struct {
	First uint32
	End   uint32
}

// cutIntervals cuts a key space at the bounds of count distinct networks
// sorted by first key, then length, as range_collector_collect cuts it;
// bounds returns a network's first and last key.
//
// A stack holds the networks containing the walk position, innermost on
// top, with the class the top emits; a network ending where the one
// around it ends shares its frame and renews its class, and a network
// covers the intervals from its push up to its frame's pop.
func cutIntervals[K uint32 | uint64](count int, bounds func(idx int) (K, K)) intervals[K] {
	type frame struct {
		To    K
		Class uint32
		// Pending is where the networks the frame ends begin in pending.
		Pending int
	}
	cut := intervals[K]{Spans: make([]intervalSpan, count)}
	var stack []frame
	var pending []uint32
	position := K(0)
	// emit cuts an interval from the position up to a key; past the last
	// key the position wraps to zero only after the last one.
	emit := func(to K) {
		top := &stack[len(stack)-1]
		if top.Class == none {
			top.Class = cut.ClassCount
			cut.ClassCount++
		}
		cut.Starts = append(cut.Starts, position)
		cut.Classes = append(cut.Classes, top.Class)
		position = to + 1
	}
	pop := func() {
		top := stack[len(stack)-1]
		emit(top.To)
		for _, net := range pending[top.Pending:] {
			cut.Spans[net].End = uint32(len(cut.Starts))
		}
		pending = pending[:top.Pending]
		stack = stack[:len(stack)-1]
	}
	stack = append(stack, frame{To: ^K(0), Class: none})
	for idx := range count {
		from, to := bounds(idx)
		for stack[len(stack)-1].To < from {
			pop()
		}
		if position < from {
			emit(from - 1)
		}
		cut.Spans[idx].First = uint32(len(cut.Starts))
		if top := &stack[len(stack)-1]; top.To == to {
			top.Class = none
		} else {
			stack = append(stack, frame{To: to, Class: none, Pending: len(pending)})
		}
		pending = append(pending, uint32(idx))
	}
	for len(stack) > 0 {
		pop()
	}
	return cut
}

// Commit writes the attribute into shared memory as C's commit leaves
// it: a prefix tree of every address's class, its memory context a child
// of context titled for the attribute.
//
// A failure frees the tree and leaves the attribute zeroed, as C does.
func (m *Net4) Commit(attr *layout.ClassifyAttrNet4, context *layout.MemoryContext, name string) error {
	if err := insertStretches(&attr.Lpm, context, leafName(name, "lpm"), m.starts, m.values); err != nil {
		FreeNet4(attr)
		return err
	}
	return nil
}

// insertStretches builds a prefix tree in a child memory context of the
// parent named name, mapping the keys from every start up to the next to
// that start's value; a key is the big-endian bytes of a K.
func insertStretches[K uint32 | uint64](tree *layout.Lpm, parent *layout.MemoryContext, name string, starts []K, values []uint32) error {
	if err := lpm.Init(tree, parent, name); err != nil {
		return err
	}
	size := int(unsafe.Sizeof(K(0)))
	var from, to [8]byte
	for idx, start := range starts {
		end := ^K(0)
		if idx+1 < len(starts) {
			end = starts[idx+1] - 1
		}
		// The key's bytes lead the buffers.
		binary.BigEndian.PutUint64(from[:], uint64(start)<<(64-8*size))
		binary.BigEndian.PutUint64(to[:], uint64(end)<<(64-8*size))
		if err := lpm.Insert(tree, from[:size], to[:size], values[idx]); err != nil {
			return err
		}
	}
	return nil
}

// FreeNet4 releases what a commit wrote into an IPv4 network attribute
// and zeroes it, as classify_attr_net4_free does; it is safe on a zeroed
// attribute.
func FreeNet4(attr *layout.ClassifyAttrNet4) {
	lpm.Free(&attr.Lpm)
	*attr = layout.ClassifyAttrNet4{}
}
