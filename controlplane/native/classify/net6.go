//go:build linux && (amd64 || arm64)

package classify

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Net6RowMark marks a high half value that names a row of the table
// rather than a final value (FILTER_NET6_ROW_MARK).
const Net6RowMark = 0x40000000

// errDeepNesting reports networks nested deeper than the 65 prefix
// lengths of a half allow, which only masks that are not prefixes reach.
var errDeepNesting = errors.New("the networks nest too deep")

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

func (m network6) Hash() uint32 {
	hash := m.Hi.From*0x9e3779b97f4a7c15 ^ m.Lo.From*0xc2b2ae3d27d4eb4f ^ uint64(m.Hi.Bits)<<8 ^ uint64(m.Lo.Bits)
	return uint32(hash >> 32)
}

// IsAny reports whether the network has no mask at all.
func (m network6) IsAny() bool {
	return m.Hi.Bits == 0 && m.Lo.Bits == 0
}

// newHalf returns the half of an address and a mask, or an error when
// the mask is not a prefix.
func newHalf(addr, mask uint64) (half, error) {
	length := bits.OnesCount64(mask)
	if mask != ^uint64(0)<<(64-length) {
		return half{}, fmt.Errorf("mask half %#x is not a prefix", mask)
	}
	from := addr & mask
	return half{From: from, Bits: uint8(length)}, nil
}

// CompileNet6 compiles the IPv6 network attribute of a ruleset into a
// zeroed attribute in shared memory, as classify_net6_compile does, and
// returns its stage; a failed compile leaves the attribute zeroed.
//
// Each half of the address space is cut into classes on its own, as the
// IPv4 one is; a network covers an interval of high classes by an
// interval of low ones. A region is the set of networks covering a cell
// of the two, found by the sweep over the high half that C runs, and
// numbered in the order it writes them.
//
// The table is the bulk of the attribute, so the sweep writes its rows
// straight into shared memory, as C does, and they take their final
// values in place: the compile never holds the table twice. The trees of
// the halves follow, each in a child memory context of context named for
// the attribute.
func CompileNet6(rules []Net6Rule, attr *layout.ClassifyAttrNet6, context *layout.MemoryContext, name string) (*Stage, error) {
	stage, err := compileNet6(rules, attr, context, name)
	if err != nil {
		FreeNet6(attr)
		return nil, err
	}
	return stage, nil
}

func compileNet6(rules []Net6Rule, attr *layout.ClassifyAttrNet6, context *layout.MemoryContext, name string) (*Stage, error) {
	// From here on firsts, starts and netsOf index the distinct lists.
	lists, numbers := internLists(len(rules), func(idx int) ([]Net6Network, bool) {
		return rules[idx].Networks, rules[idx].Absent
	})
	nets, starts, err := normalizeNet6Lists(lists)
	if err != nil {
		return nil, err
	}
	netsOf := func(list int) []network6 {
		return nets[starts[list]:starts[list+1]]
	}
	groups, firsts := groupLists(numbers, len(lists), netsOf)

	// The networks of the groups, each once, found through a table of
	// their numbers plus one as groupRules finds the groups, and the one
	// at every place of a group in nets.
	var unique []network6
	var hashes []uint32
	netTable := make([]uint32, 16)
	uniqueAt := make([]uint32, len(nets))
	for _, first := range firsts {
		for place := starts[first]; place < starts[first+1]; place++ {
			net := nets[place]
			hash := net.Hash()
			mask := uint32(len(netTable) - 1)
			slot := hash & mask
			for netTable[slot] != 0 && unique[netTable[slot]-1] != net {
				slot = (slot + 1) & mask
			}
			if netTable[slot] != 0 {
				uniqueAt[place] = netTable[slot] - 1
				continue
			}
			uniqueAt[place] = uint32(len(unique))
			unique = append(unique, net)
			hashes = append(hashes, hash)
			netTable[slot] = uint32(len(unique))
			if 2*len(unique) > len(netTable) {
				netTable = groupTable(hashes, 2*len(netTable))
			}
		}
	}

	hi, hiSpans := cutHalves(unique, func(net network6) half { return net.Hi })
	lo, loSpans := cutHalves(unique, func(net network6) half { return net.Lo })
	sweep := newNet6Sweep(unique, hiSpans, loSpans, &lo)
	rows := make([]uint32, hi.ClassCount)
	split := sweep.MarkSplit(rows, hi.Classes)
	var table [][]uint32
	if split != 0 {
		var err error
		if table, err = allocZeroedTable(&attr.Comb, context, leafName(name, "comb"), split, sweep.Columns); err != nil {
			return nil, err
		}
	}
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

	for _, cells := range table {
		for column, region := range cells {
			cells[column] = regionValues[region]
		}
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
	if err := commitHalf(&attr.Hi, context, leafName(name, "hi"), hiStarts, hiValues); err != nil {
		return nil, err
	}
	if err := commitHalf(&attr.Lo, context, leafName(name, "lo"), loStarts, loValues); err != nil {
		return nil, err
	}
	return &stage, nil
}

// allocZeroedTable allocates a table of rows by columns cells under
// context, every cell zero as value_table_init leaves it, and returns
// its rows.
func allocZeroedTable(table *layout.ValueTable, context *layout.MemoryContext, name string, rows, columns uint32) ([][]uint32, error) {
	if err := allocTable(table, context, name, rows, columns); err != nil {
		return nil, err
	}
	chunks := unsafe.Slice(table.Values.Get(), rows)
	cells := make([][]uint32, rows)
	for row := range chunks {
		chunk := shmalloc.ContextAlloc(table.MemoryContext.Get(), uintptr(columns)*unsafe.Sizeof(uint32(0)))
		if chunk == nil {
			return nil, errNoMemory
		}
		chunks[row].Set((*uint32)(chunk))
		cells[row] = unsafe.Slice((*uint32)(chunk), columns)
		clear(cells[row])
	}
	return cells, nil
}

// normalizeNet6Lists returns the networks of every list in one slice,
// list after list, and where every list's networks begin, one more entry
// where the last list's end.
func normalizeNet6Lists(lists [][]Net6Network) ([]network6, []int, error) {
	total := 0
	for _, list := range lists {
		total += len(list)
	}
	nets := make([]network6, 0, total)
	starts := make([]int, 0, len(lists)+1)
	for idx, list := range lists {
		starts = append(starts, len(nets))
		for _, network := range list {
			var net network6
			var err error
			net.Hi, err = newHalf(binary.BigEndian.Uint64(network.Addr[:8]), binary.BigEndian.Uint64(network.Mask[:8]))
			if err != nil {
				return nil, nil, fmt.Errorf("network list %d: %w", idx, err)
			}
			net.Lo, err = newHalf(binary.BigEndian.Uint64(network.Addr[8:]), binary.BigEndian.Uint64(network.Mask[8:]))
			if err != nil {
				return nil, nil, fmt.Errorf("network list %d: %w", idx, err)
			}
			nets = append(nets, net)
		}
	}
	starts = append(starts, len(nets))
	return nets, starts, nil
}

// net6RuleIsAny reports whether a rule matches every address, judged as
// C judges it: without networks or with one without a mask.
func net6RuleIsAny(nets []network6) bool {
	return len(nets) == 0 || slices.ContainsFunc(nets, network6.IsAny)
}

// cutHalves cuts one half of the address space by the halves of the
// networks and returns the intervals and the span of every network's
// half; a half without a mask spans them all.
func cutHalves(nets []network6, halfOf func(network6) half) (classIntervals[uint64], []intervalSpan) {
	type keyed struct {
		Half half
		Net  uint32
	}
	var keys []keyed
	for idx, net := range nets {
		if half := halfOf(net); half.Bits > 0 {
			keys = append(keys, keyed{Half: half, Net: uint32(idx)})
		}
	}
	slices.SortFunc(keys, func(left, right keyed) int {
		return cmp.Or(cmp.Compare(left.Half.From, right.Half.From), cmp.Compare(left.Half.Bits, right.Half.Bits))
	})
	var distinct []half
	distinctOf := make([]uint32, len(nets))
	for _, key := range keys {
		if count := len(distinct); count == 0 || distinct[count-1] != key.Half {
			distinct = append(distinct, key.Half)
		}
		distinctOf[key.Net] = uint32(len(distinct) - 1)
	}
	intervals := collectClasses(len(distinct), func(idx int) (uint64, uint64) {
		return distinct[idx].From, distinct[idx].To()
	})
	spans := make([]intervalSpan, len(nets))
	for idx, net := range nets {
		spans[idx] = intervalSpan{End: uint32(len(intervals.Starts))}
		if halfOf(net).Bits > 0 {
			spans[idx] = intervals.Spans[distinctOf[idx]]
		}
	}
	return intervals, spans
}

// treeNet is a masked network as the sweep reads it: the high and the
// low intervals it covers, the network it is, and the nearest network
// of its high interval enclosing its low one.
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

// classChunkBits sizes the chunks of the class store as
// FILTER_NET6_CLASS_CHUNK_BITS does.
const classChunkBits = 14

// classStore holds the classes in chunks that never move, so it grows
// without copying what it holds.
type classStore struct {
	Chunks [][]net6Class
	Count  uint32
}

// At returns a class.
func (m *classStore) At(class uint32) *net6Class {
	return &m.Chunks[class>>classChunkBits][class&(1<<classChunkBits-1)]
}

// Add stores a class and returns its number.
func (m *classStore) Add(class net6Class) uint32 {
	if m.Count%(1<<classChunkBits) == 0 {
		m.Chunks = append(m.Chunks, make([]net6Class, 1<<classChunkBits))
	}
	number := m.Count
	*m.At(number) = class
	m.Count++
	return number
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
	// Levels holds the entered high intervals: nested prefixes differ in
	// length, so at most one per length from 0 to 64.
	Levels      [65]sweepLevel
	Depth       uint32
	Uniform     uint32
	VectorLevel uint32
	Vector      []uint32
	// Moved counts the low intervals under the levels from the vector
	// one.
	Moved       uint32
	Classes     classStore
	RegionCount uint32
}

func newNet6Sweep(nets []network6, hiSpans, loSpans []intervalSpan, lo *classIntervals[uint64]) *net6Sweep {
	m := &net6Sweep{
		LoClasses:   lo.Classes,
		LoCount:     uint32(len(lo.Starts)),
		Columns:     lo.ClassCount,
		VectorLevel: none,
		Vector:      make([]uint32, lo.ClassCount),
		RegionCount: 1,
	}
	m.Classes.Add(net6Class{Parent: none, Net: none, Region: 0})
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
	if m.Classes.Count == Net6RowMark {
		return none, errTooManyClasses
	}
	return m.Classes.Add(net6Class{Parent: parent, Net: net, Region: none}), nil
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
		child := m.Classes.At(*class).Child
		if m.Classes.At(child).Net != net {
			created, err := m.newClass(*class, net)
			if err != nil {
				return err
			}
			m.Classes.At(*class).Child = created
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
	if int(m.Depth) == len(m.Levels) {
		return errDeepNesting
	}
	level := &m.Levels[m.Depth]
	*level = sweepLevel{First: first, End: first, FirstClass: m.Classes.Count}
	partial := false
	for level.End < uint32(len(m.Nets)) && m.Nets[level.End].Hi == m.Nets[first].Hi {
		if m.partial(&m.Nets[level.End]) {
			partial = true
		}
		level.End++
	}
	if m.VectorLevel == none && partial {
		for column := range m.Vector {
			m.Vector[column] = m.Uniform
		}
		m.VectorLevel = m.Depth
		m.Moved = 0
	}
	m.Depth++
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
	m.Depth--
	level := m.Levels[m.Depth]
	if m.VectorLevel == none {
		m.Uniform = m.Classes.At(m.Uniform).Parent
		return
	}
	if m.VectorLevel == m.Depth {
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
				*class = m.Classes.At(*class).Parent
			}
		}
	}
}

// region numbers a class the first time the sweep writes it.
func (m *net6Sweep) region(class uint32) uint32 {
	region := &m.Classes.At(class).Region
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
	for depth := m.Depth; depth > m.VectorLevel; {
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
func (m *net6Sweep) SweepRows(rows, hiClasses []uint32, table [][]uint32) error {
	next := uint32(0)
	tableRow := uint32(0)
	for interval, class := range hiClasses {
		for m.Depth != 0 && m.Nets[m.Levels[m.Depth-1].First].Hi.End <= uint32(interval) {
			m.Pop()
		}
		for next < uint32(len(m.Nets)) && m.Nets[next].Hi.First == uint32(interval) {
			if err := m.Push(next); err != nil {
				return err
			}
			next = m.Levels[m.Depth-1].End
		}
		row := &rows[class]
		switch {
		case *row == none:
			*row = Net6RowMark | tableRow
			m.writeRow(table[tableRow])
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
func (m *net6Sweep) NetRegions(count int) pairLists {
	visit := func(each func(net, region uint32)) {
		for first := uint32(1); first < m.Classes.Count; first++ {
			region := m.Classes.At(first).Region
			if region == none {
				continue
			}
			for class := first; class != 0; class = m.Classes.At(class).Parent {
				for net := m.Classes.At(class).Net; net != none; net = m.Nets[net].LoParent {
					each(m.Nets[net].Net, region)
				}
			}
		}
	}
	lists := pairLists{Offsets: make([]int, count+1)}
	visit(func(net, _ uint32) {
		lists.Offsets[net+1]++
	})
	for net := range count {
		lists.Offsets[net+1] += lists.Offsets[net]
	}
	lists.Pairs = make([]uint32, lists.Offsets[count])
	filled := slices.Clone(lists.Offsets[:count])
	visit(func(net, region uint32) {
		lists.Pairs[filled[net]] = region
		filled[net]++
	})
	return lists
}

// commitHalf builds the prefix tree of a half from its stretches.
func commitHalf(tree *layout.Lpm, context *layout.MemoryContext, name string, starts []uint64, values []uint32) error {
	if err := lpm.Init(tree, context, name); err != nil {
		return err
	}
	var from, to [8]byte
	for idx, start := range starts {
		end := ^uint64(0)
		if idx+1 < len(starts) {
			end = starts[idx+1] - 1
		}
		binary.BigEndian.PutUint64(from[:], start)
		binary.BigEndian.PutUint64(to[:], end)
		if err := lpm.Insert(tree, from[:], to[:], values[idx]); err != nil {
			return err
		}
	}
	return nil
}

// FreeNet6 releases what CompileNet6 wrote into an IPv6 network
// attribute; it is safe on a zeroed attribute.
func FreeNet6(attr *layout.ClassifyAttrNet6) {
	lpm.Free(&attr.Hi)
	lpm.Free(&attr.Lo)
	FreeJoin(&attr.Comb)
}
