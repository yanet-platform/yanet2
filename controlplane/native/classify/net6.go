//go:build linux && (amd64 || arm64)

package classify

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"math/bits"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
)

// Net6RowMark marks a high half value that names a row of the table
// rather than a class (FILTER_NET6_ROW_MARK).
const Net6RowMark = 0x40000000

// errNet6Classes reports networks that cross into more classes than a
// high half value can tell from the row mark.
var errNet6Classes = fmt.Errorf("the networks make %d classes or more: %w", Net6RowMark, ErrInvalidRule)

// Net6Network is an IPv6 network as a rule spells it: an address and a
// mask, each 64-bit half of the mask a prefix of its own.
type Net6Network struct {
	Addr [16]byte
	Mask [16]byte
}

// Net6Rule is what one rule matches on an IPv6 network attribute.
type Net6Rule struct {
	// Networks are the IPv6 networks the rule matches; a rule without
	// any, or holding one without a mask, matches every address.
	Networks []Net6Network
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Net6 is a compiled IPv6 network attribute: both halves of the address
// space cut into stretches, the table of the high stretches the low half
// splits, and the stage a join consumes.
type Net6 struct {
	// hiStarts holds the first value of every high stretch, ascending;
	// hiValues its class, or the row mark and its row of the table.
	hiStarts []uint64
	hiValues []uint32
	// loStarts holds the first value of every low stretch, ascending;
	// loValues its column of the table.
	loStarts []uint64
	loValues []uint32
	// table holds the classes of every row, row after row.
	table   []uint32
	rows    uint32
	columns uint32
	stage   Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *Net6) Stage() *Stage {
	stage := m.stage
	return &stage
}

// half is one 64-bit half of a network: its first value and its prefix
// length.
type half struct {
	From uint64
	Bits uint8
}

// To returns the last value of the half.
func (m half) To() uint64 {
	return m.From | ^uint64(0)>>m.Bits
}

// network6 is an IPv6 network as the compile reads it, by its halves.
type network6 struct {
	Hi half
	Lo half
}

// IsAny reports whether the network has no mask at all.
func (m network6) IsAny() bool {
	return m.Hi.Bits == 0 && m.Lo.Bits == 0
}

// CompileNet6 compiles the IPv6 network attribute of a ruleset to the
// lookups and the stage classify_net6_compile produces.
//
// Each half of the address space is cut into classes as the IPv4 one is;
// a network covers a run of high intervals by a run of low ones. A region
// is the set of networks covering a cell of the two, found by the sweep
// over the high half C runs and numbered in the order it writes them.
func CompileNet6(rules []Net6Rule) (*Net6, error) {
	nets, starts, err := normalizeNet6Rules(rules)
	if err != nil {
		return nil, err
	}
	netsOf := func(rule int) []network6 {
		return nets[starts[rule]:starts[rule+1]]
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]network6, bool) {
		return netsOf(idx), rules[idx].Absent
	})

	// The networks of the groups, each distinct one once, and the one at
	// every place of a group's first rule.
	uniqueOf := map[network6]uint32{}
	var unique []network6
	uniqueAt := make([]uint32, len(nets))
	for _, first := range firsts {
		for place := starts[first]; place < starts[first+1]; place++ {
			number, ok := uniqueOf[nets[place]]
			if !ok {
				number = uint32(len(unique))
				uniqueOf[nets[place]] = number
				unique = append(unique, nets[place])
			}
			uniqueAt[place] = number
		}
	}

	hi, hiSpans := cutHalves(unique, func(net network6) half { return net.Hi })
	lo, loSpans := cutHalves(unique, func(net network6) half { return net.Lo })
	sweep := newNet6Sweep(unique, hiSpans, loSpans, &lo)
	rows := make([]uint32, hi.ClassCount)
	split := sweep.MarkSplit(rows, hi.Classes)
	table := make([]uint32, uint64(split)*uint64(sweep.Columns))
	if err := sweep.SweepRows(rows, hi.Classes, table); err != nil {
		return nil, err
	}
	regions := sweep.NetRegions(len(unique))

	regionValues := make([]uint32, sweep.RegionCount)
	refiner := newRefiner(sweep.RegionCount)
	for _, first := range firsts {
		if net6RuleIsAny(netsOf(int(first))) {
			continue
		}
		refiner.NewGeneration()
		for place := starts[first]; place < starts[first+1]; place++ {
			for _, region := range regions.Get(uniqueAt[place]) {
				regionValues[region] = refiner.Touch(regionValues[region])
			}
		}
	}
	numbers, count := refiner.Numbers()
	for region := range regionValues {
		regionValues[region] = numbers[regionValues[region]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		first := firsts[group]
		if net6RuleIsAny(netsOf(int(first))) {
			for _, value := range regionValues {
				builder.Reach(value)
			}
			return
		}
		for place := starts[first]; place < starts[first+1]; place++ {
			for _, region := range regions.Get(uniqueAt[place]) {
				builder.Reach(regionValues[region])
			}
		}
	})

	for idx, region := range table {
		table[idx] = regionValues[region]
	}
	var hiStarts, loStarts []uint64
	var hiValues, loValues []uint32
	for idx, start := range hi.Starts {
		value := rows[hi.Classes[idx]]
		if value&Net6RowMark == 0 {
			value = regionValues[value]
		}
		if count := len(hiValues); count == 0 || hiValues[count-1] != value {
			hiStarts = append(hiStarts, start)
			hiValues = append(hiValues, value)
		}
	}
	for idx, start := range lo.Starts {
		if count := len(loValues); count == 0 || loValues[count-1] != lo.Classes[idx] {
			loStarts = append(loStarts, start)
			loValues = append(loValues, lo.Classes[idx])
		}
	}
	return &Net6{
		hiStarts: hiStarts,
		hiValues: hiValues,
		loStarts: loStarts,
		loValues: loValues,
		table:    table,
		rows:     split,
		columns:  sweep.Columns,
		stage:    stage,
	}, nil
}

// normalizeNet6Rules returns the networks of every rule in one slice,
// rule after rule, none for an absent one, and where every rule's
// networks begin, one more entry where the last rule's end.
func normalizeNet6Rules(rules []Net6Rule) ([]network6, []int, error) {
	var nets []network6
	starts := make([]int, 0, len(rules)+1)
	for idx, rule := range rules {
		starts = append(starts, len(nets))
		if rule.Absent {
			continue
		}
		for _, network := range rule.Networks {
			hi, hiOK := newHalf(binary.BigEndian.Uint64(network.Addr[:8]), binary.BigEndian.Uint64(network.Mask[:8]))
			lo, loOK := newHalf(binary.BigEndian.Uint64(network.Addr[8:]), binary.BigEndian.Uint64(network.Mask[8:]))
			if !hiOK || !loOK {
				return nil, nil, fmt.Errorf("rule %d: mask %x has a half that is not a prefix: %w", idx, network.Mask, ErrInvalidRule)
			}
			nets = append(nets, network6{Hi: hi, Lo: lo})
		}
	}
	starts = append(starts, len(nets))
	return nets, starts, nil
}

// newHalf returns the half of an address and a mask, and whether the
// mask is a prefix.
func newHalf(address, mask uint64) (half, bool) {
	length := bits.LeadingZeros64(^mask)
	return half{From: address & mask, Bits: uint8(length)}, mask == ^uint64(0)<<(64-length)
}

// net6RuleIsAny reports whether a rule matches every address, judged as
// C judges it: without networks or with one without a mask.
func net6RuleIsAny(nets []network6) bool {
	return len(nets) == 0 || slices.ContainsFunc(nets, network6.IsAny)
}

// cutHalves cuts one half of the address space by the halves of the
// networks and returns the intervals and the span of every network's
// half; a half without a mask spans them all.
func cutHalves(nets []network6, halfOf func(network6) half) (intervals[uint64], []intervalSpan) {
	order := make([]int, 0, len(nets))
	for idx, net := range nets {
		if halfOf(net).Bits > 0 {
			order = append(order, idx)
		}
	}
	slices.SortFunc(order, func(left, right int) int {
		leftHalf, rightHalf := halfOf(nets[left]), halfOf(nets[right])
		return cmp.Or(cmp.Compare(leftHalf.From, rightHalf.From), cmp.Compare(leftHalf.Bits, rightHalf.Bits))
	})
	var distinct []half
	distinctOf := make([]uint32, len(nets))
	for _, idx := range order {
		if count := len(distinct); count == 0 || distinct[count-1] != halfOf(nets[idx]) {
			distinct = append(distinct, halfOf(nets[idx]))
		}
		distinctOf[idx] = uint32(len(distinct) - 1)
	}
	cut := cutIntervals(len(distinct), func(idx int) (uint64, uint64) {
		return distinct[idx].From, distinct[idx].To()
	})
	spans := make([]intervalSpan, len(nets))
	for idx, net := range nets {
		spans[idx] = intervalSpan{End: uint32(len(cut.Starts))}
		if halfOf(net).Bits > 0 {
			spans[idx] = cut.Spans[distinctOf[idx]]
		}
	}
	return cut, spans
}

// treeNet is a masked network as the sweep reads it: the high and the
// low intervals it covers, the network it is, and the nearest network of
// its high interval enclosing its low one.
type treeNet struct {
	Hi       intervalSpan
	Lo       intervalSpan
	Net      uint32
	LoParent uint32
}

// sweepLevel is an entered high interval: its networks and the first
// class it made.
type sweepLevel struct {
	First      uint32
	End        uint32
	FirstClass uint32
}

// net6Class is a set of networks covering a cell: the networks of its
// parent class plus a tree network and the networks of its high interval
// enclosing it. Region is numbered when the sweep first writes the
// class; Child is the class the parent moved into under the network
// applied last.
type net6Class struct {
	Parent uint32
	Net    uint32
	Region uint32
	Child  uint32
}

// net6Sweep walks the high intervals of the networks, nested ones
// inside, keeping the class of every low class, as
// filter_net6_sweep_rows does.
//
// Class 0 is the empty set. While no entered network has a partial low
// half every low class shares the uniform class; otherwise the vector
// holds the class of every low class, and only the ones under the levels
// entered since it was filled may hold another class than the uniform.
type net6Sweep struct {
	// Nets holds the masked networks, the enclosing intervals first.
	Nets      []treeNet
	LoClasses []uint32
	LoCount   uint32
	// Columns is the count of low classes, the width of a table row.
	Columns uint32
	// Levels holds the entered high intervals, the innermost last.
	Levels      []sweepLevel
	Uniform     uint32
	VectorLevel uint32
	Vector      []uint32
	// Moved counts the low intervals under the levels from the vector
	// one.
	Moved       uint32
	Classes     []net6Class
	RegionCount uint32
}

func newNet6Sweep(nets []network6, hiSpans, loSpans []intervalSpan, lo *intervals[uint64]) *net6Sweep {
	m := &net6Sweep{
		LoClasses:   lo.Classes,
		LoCount:     uint32(len(lo.Starts)),
		Columns:     lo.ClassCount,
		VectorLevel: none,
		Vector:      make([]uint32, lo.ClassCount),
		Classes:     []net6Class{{Parent: none, Net: none}},
		RegionCount: 1,
	}
	for idx, net := range nets {
		if !net.IsAny() {
			m.Nets = append(m.Nets, treeNet{Hi: hiSpans[idx], Lo: loSpans[idx], Net: uint32(idx)})
		}
	}
	// Enclosing intervals precede the ones they enclose: first ascending,
	// end descending, the high half first.
	slices.SortFunc(m.Nets, func(left, right treeNet) int {
		return cmp.Or(
			cmp.Compare(left.Hi.First, right.Hi.First),
			cmp.Compare(right.Hi.End, left.Hi.End),
			cmp.Compare(left.Lo.First, right.Lo.First),
			cmp.Compare(right.Lo.End, left.Lo.End),
		)
	})
	// The nearest low interval enclosing a network of a high interval is
	// the previous network or one of the networks enclosing it.
	for idx := range m.Nets {
		parent := none
		if idx > 0 && m.Nets[idx-1].Hi == m.Nets[idx].Hi {
			parent = uint32(idx - 1)
		}
		for parent != none && m.Nets[parent].Lo.End <= m.Nets[idx].Lo.First {
			parent = m.Nets[parent].LoParent
		}
		m.Nets[idx].LoParent = parent
	}
	return m
}

// partial reports whether a network leaves part of the low half out.
func (m *net6Sweep) partial(net *treeNet) bool {
	return net.Lo.First != 0 || net.Lo.End != m.LoCount
}

// newClass adds the class of a parent class plus a network; the classes
// stay below the row mark.
func (m *net6Sweep) newClass(parent, net uint32) (uint32, error) {
	if len(m.Classes) == Net6RowMark {
		return none, errNet6Classes
	}
	m.Classes = append(m.Classes, net6Class{Parent: parent, Net: net, Region: none})
	return uint32(len(m.Classes) - 1), nil
}

// move moves the low classes of the low intervals from up to to into
// the classes of a network of the level entered last. A low class
// repeats only in the gaps around a nested low interval, where the
// innermost network is the same, so one the level already moved keeps
// its class.
func (m *net6Sweep) move(net, from, to, firstClass uint32) error {
	for idx := from; idx < to; idx++ {
		class := &m.Vector[m.LoClasses[idx]]
		if *class >= firstClass {
			continue
		}
		child := m.Classes[*class].Child
		if m.Classes[child].Net != net {
			created, err := m.newClass(*class, net)
			if err != nil {
				return err
			}
			m.Classes[*class].Child = created
			child = created
		}
		*class = child
	}
	return nil
}

// Push enters the high interval of the networks from first: every low
// class under them moves into a class of its own for the level, the
// class of the innermost network over it. The networks nested in one
// follow it, so a network moves the gaps between the networks directly
// nested in it.
func (m *net6Sweep) Push(first uint32) error {
	level := sweepLevel{First: first, End: first, FirstClass: uint32(len(m.Classes))}
	partial := false
	for level.End < uint32(len(m.Nets)) && m.Nets[level.End].Hi == m.Nets[first].Hi {
		if m.partial(&m.Nets[level.End]) {
			partial = true
		}
		level.End++
	}
	depth := uint32(len(m.Levels))
	m.Levels = append(m.Levels, level)
	if m.VectorLevel == none && partial {
		for column := range m.Vector {
			m.Vector[column] = m.Uniform
		}
		m.VectorLevel = depth
		m.Moved = 0
	}
	if m.VectorLevel == none {
		// The only network of the level covers the whole low half.
		uniform, err := m.newClass(m.Uniform, first)
		m.Uniform = uniform
		return err
	}

	for net := first; net < level.End; net++ {
		span := m.Nets[net].Lo
		if m.Nets[net].LoParent == none {
			m.Moved += span.End - span.First
		}
		from := span.First
		for child := net + 1; from < span.End; child++ {
			// The network end closes the gap after the last child.
			to, next := span.End, span.End
			if child < level.End && m.Nets[child].Lo.First < span.End {
				if m.Nets[child].LoParent != net {
					continue
				}
				to, next = m.Nets[child].Lo.First, m.Nets[child].Lo.End
			}
			if err := m.move(net, from, to, level.FirstClass); err != nil {
				return err
			}
			from = next
		}
	}
	return nil
}

// Pop leaves the innermost level: every low class it moved returns to
// the parent of its class.
func (m *net6Sweep) Pop() {
	depth := uint32(len(m.Levels) - 1)
	level := m.Levels[depth]
	m.Levels = m.Levels[:depth]
	if m.VectorLevel == none {
		m.Uniform = m.Classes[m.Uniform].Parent
		return
	}
	if m.VectorLevel == depth {
		// The vector dies with the level that filled it.
		m.VectorLevel = none
		return
	}
	for net := level.First; net < level.End; net++ {
		if m.Nets[net].LoParent != none {
			continue
		}
		span := m.Nets[net].Lo
		m.Moved -= span.End - span.First
		for idx := span.First; idx < span.End; idx++ {
			class := &m.Vector[m.LoClasses[idx]]
			if *class >= level.FirstClass {
				*class = m.Classes[*class].Parent
			}
		}
	}
}

// region numbers a class the first time the sweep writes it.
func (m *net6Sweep) region(class uint32) uint32 {
	region := &m.Classes[class].Region
	if *region == none {
		*region = m.RegionCount
		m.RegionCount++
	}
	return *region
}

// writeRow writes the regions of a split row into its zeroed cells. A
// row with few low intervals under the levels from the vector one writes
// the uniform region over the rest, and only when it is not the empty
// set one, then the regions under those levels; the order numbers the
// regions as C numbers them.
func (m *net6Sweep) writeRow(cells []uint32) {
	if uint64(m.Moved)*8 >= uint64(m.Columns) {
		for column := range cells {
			cells[column] = m.region(m.Vector[column])
		}
		return
	}
	if uniform := m.region(m.Uniform); uniform != 0 {
		for column := range cells {
			cells[column] = uniform
		}
	}
	for depth := uint32(len(m.Levels)); depth > m.VectorLevel; {
		depth--
		level := m.Levels[depth]
		for net := level.First; net < level.End; net++ {
			if m.Nets[net].LoParent != none {
				continue
			}
			span := m.Nets[net].Lo
			for idx := span.First; idx < span.End; idx++ {
				column := m.LoClasses[idx]
				cells[column] = m.region(m.Vector[column])
			}
		}
	}
}

// MarkSplit marks the row of every high class under a high interval
// with a partial low half as pending a table row, and returns how many
// it marked; the intervals nest, so every high interval is marked once.
func (m *net6Sweep) MarkSplit(rows, hiClasses []uint32) uint32 {
	count := uint32(0)
	end := uint32(0)
	for idx := range m.Nets {
		span := m.Nets[idx].Hi
		if !m.partial(&m.Nets[idx]) || span.First < end {
			continue
		}
		for interval := span.First; interval < span.End; interval++ {
			row := &rows[hiClasses[interval]]
			if *row != none {
				count++
			}
			*row = none
		}
		end = span.End
	}
	return count
}

// SweepRows sweeps the high intervals over the nested networks and
// writes the row of every high class: a pending one once into the next
// table row, any other into its region.
func (m *net6Sweep) SweepRows(rows, hiClasses []uint32, table []uint32) error {
	next := uint32(0)
	tableRow := uint32(0)
	for interval, class := range hiClasses {
		for len(m.Levels) != 0 && m.Nets[m.Levels[len(m.Levels)-1].First].Hi.End <= uint32(interval) {
			m.Pop()
		}
		for next < uint32(len(m.Nets)) && m.Nets[next].Hi.First == uint32(interval) {
			if err := m.Push(next); err != nil {
				return err
			}
			next = m.Levels[len(m.Levels)-1].End
		}
		row := &rows[class]
		switch {
		case *row == none:
			*row = Net6RowMark | tableRow
			m.writeRow(table[int(tableRow)*int(m.Columns):][:m.Columns])
			tableRow++
		case *row&Net6RowMark == 0:
			*row = m.region(m.Uniform)
		}
	}
	return nil
}

// NetRegions lists the regions of the classes holding each of count
// networks, in the order of the classes, as filter_net6_sweep_assign
// does; a network without a mask lists none.
func (m *net6Sweep) NetRegions(count int) lists {
	visit := func(each func(net, region uint32)) {
		for first := 1; first < len(m.Classes); first++ {
			region := m.Classes[first].Region
			if region == none {
				continue
			}
			for class := uint32(first); class != 0; class = m.Classes[class].Parent {
				for net := m.Classes[class].Net; net != none; net = m.Nets[net].LoParent {
					each(m.Nets[net].Net, region)
				}
			}
		}
	}
	regions := lists{Offsets: make([]int, count+1)}
	visit(func(net, _ uint32) {
		regions.Offsets[net+1]++
	})
	for net := range count {
		regions.Offsets[net+1] += regions.Offsets[net]
	}
	regions.Items = make([]uint32, regions.Offsets[count])
	filled := slices.Clone(regions.Offsets[:count])
	visit(func(net, region uint32) {
		regions.Items[filled[net]] = region
		filled[net]++
	})
	return regions
}

// Commit writes the attribute into shared memory as C's commit leaves
// it: the table of the split rows, then a prefix tree of each half, each
// in a child memory context of context titled for the attribute.
//
// A failure frees what the commit wrote and leaves the attribute zeroed,
// as C does.
func (m *Net6) Commit(attr *layout.ClassifyAttrNet6, context *layout.MemoryContext, name string) error {
	if err := m.commit(attr, context, name); err != nil {
		FreeNet6(attr)
		return err
	}
	return nil
}

// commit writes the table and the trees of the halves.
//
// The attribute starts empty, as C's commit clears it, so a failure
// frees only what the commit wrote.
func (m *Net6) commit(attr *layout.ClassifyAttrNet6, context *layout.MemoryContext, name string) error {
	*attr = layout.ClassifyAttrNet6{}
	if m.rows > 0 {
		rows, err := initValueTable(&attr.Comb, context, leafName(name, "comb"), m.rows, m.columns)
		if err != nil {
			return err
		}
		for row, cells := range rows {
			copy(cells, m.table[row*int(m.columns):])
		}
	}
	if err := insertStretches(&attr.Hi, context, leafName(name, "hi"), m.hiStarts, m.hiValues); err != nil {
		return err
	}
	return insertStretches(&attr.Lo, context, leafName(name, "lo"), m.loStarts, m.loValues)
}

// FreeNet6 releases what a commit wrote into an IPv6 network attribute
// and zeroes it, as classify_attr_net6_free does; it is safe on a zeroed
// attribute.
func FreeNet6(attr *layout.ClassifyAttrNet6) {
	lpm.Free(&attr.Hi)
	lpm.Free(&attr.Lo)
	freeValueTable(&attr.Comb)
	*attr = layout.ClassifyAttrNet6{}
}
