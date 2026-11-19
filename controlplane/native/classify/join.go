//go:build linux && (amd64 || arm64)

package classify

import (
	"errors"
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// none marks the absence of a number: a free class slot, the list of an
// absent rule, a network with no enclosing low interval, a sweep level
// not taken and a region not yet numbered.
const none = ^uint32(0)

// errTooManyClasses reports a join whose classes outgrow 32-bit ids.
var errTooManyClasses = errors.New("the join has too many classes")

// Join joins two stages into the value table of the classes of every
// pair of their region values, written into shared memory, and returns
// the joint, as classify_join does.
//
// A rule with a group on both sides belongs to the pair of its groups;
// rules sharing a pair cover the same cells, so the join works on the
// distinct pairs, numbered by first appearance. A cell's class is the
// set of pairs covering it, numbered by first appearance in row-major
// order. The table goes into a child memory context of context named for
// the join; only it reaches shared memory, row by row, so the join holds
// two rows of it at a time.
func Join(left, right *Stage, table *layout.ValueTable, context *layout.MemoryContext, name string) (*Joint, error) {
	if len(left.RuleGroups) != len(right.RuleGroups) {
		return nil, fmt.Errorf("the stages group %d and %d rules", len(left.RuleGroups), len(right.RuleGroups))
	}
	ruleGroups, pairs := pairRules(left.RuleGroups, right.RuleGroups)
	joiner := newJoiner(left, right, pairs)
	if err := allocTable(table, context, leafName(name, "table"), joiner.VDim, joiner.HDim); err != nil {
		return nil, err
	}
	if err := joiner.PaintTable(table); err != nil {
		FreeJoin(table)
		return nil, err
	}
	return joiner.Joint(ruleGroups), nil
}

// groupPair is the left and the right group of a rule.
type groupPair struct {
	Left  uint32
	Right uint32
}

// Hash hashes a pair as merge_group_pair_hash does.
func (m groupPair) Hash() uint32 {
	return (m.Left*2654435761 ^ m.Right*2246822519) * 3266489917
}

// pairRules returns the pair of every rule, or InvalidGroup for a rule
// without a group on either side, and the distinct pairs in order of
// first appearance, found through a table of pair numbers plus one as
// groupRules finds the groups.
func pairRules(left, right []uint32) ([]uint32, []groupPair) {
	ruleGroups := make([]uint32, len(left))
	var pairs []groupPair
	var hashes []uint32
	table := make([]uint32, 16)
	for idx := range left {
		if left[idx] == InvalidGroup || right[idx] == InvalidGroup {
			ruleGroups[idx] = InvalidGroup
			continue
		}
		groups := groupPair{Left: left[idx], Right: right[idx]}
		hash := groups.Hash()
		mask := uint32(len(table) - 1)
		slot := hash & mask
		for table[slot] != 0 && pairs[table[slot]-1] != groups {
			slot = (slot + 1) & mask
		}
		if table[slot] != 0 {
			ruleGroups[idx] = table[slot] - 1
			continue
		}
		ruleGroups[idx] = uint32(len(pairs))
		pairs = append(pairs, groups)
		hashes = append(hashes, hash)
		table[slot] = uint32(len(pairs))
		if 2*len(pairs) > len(table) {
			table = groupTable(hashes, 2*len(table))
		}
	}
	return ruleGroups, pairs
}

// pairHash hashes one pair; a pair set hashes as the xor of its pairs.
func pairHash(pair uint32) uint32 {
	hash := pair + 1
	hash ^= hash >> 16
	hash *= 0x85ebca6b
	hash ^= hash >> 13
	hash *= 0xc2b2ae35
	return hash ^ hash>>16
}

// xorInto writes the values held by exactly one of two ascending runs
// into out, ascending, and returns how many; out has room for both.
func xorInto(out, left, right []uint32) int {
	count, idx, jdx := 0, 0, 0
	for idx < len(left) && jdx < len(right) {
		switch value := left[idx]; {
		case value < right[jdx]:
			out[count] = value
			count++
			idx++
		case value > right[jdx]:
			out[count] = right[jdx]
			count++
			jdx++
		default:
			idx++
			jdx++
		}
	}
	count += copy(out[count:], left[idx:])
	return count + copy(out[count:], right[jdx:])
}

// xorRuns returns the values held by exactly one of two ascending runs,
// ascending: one run as it is when the other is empty, else written into
// out, which has room for both.
func xorRuns(out, left, right []uint32) []uint32 {
	switch {
	case len(right) == 0:
		return left
	case len(left) == 0:
		return right
	}
	return out[:xorInto(out, left, right)]
}

// appendXor appends the values held by exactly one of two ascending runs
// to out, ascending.
func appendXor(out, left, right []uint32) []uint32 {
	out = slices.Grow(out, len(left)+len(right))
	idx, jdx := 0, 0
	for idx < len(left) && jdx < len(right) {
		switch value := left[idx]; {
		case value < right[jdx]:
			out = append(out, value)
			idx++
		case value > right[jdx]:
			out = append(out, right[jdx])
			jdx++
		default:
			idx++
			jdx++
		}
	}
	out = append(out, left[idx:]...)
	return append(out, right[jdx:]...)
}

// joinSide is a group as one side of its pairs.
type joinSide struct {
	Ready bool
	// Flipped marks a group holding over half of its dimension, kept as
	// the values it lacks.
	Flipped bool
	// Small holds the values of the group or, flipped, the values it
	// lacks.
	Small []uint32
	// Edges holds, for a column side, the values whose membership differs
	// from the value before.
	Edges []uint32
}

// Init fills the side of a group on its first use, a column side with
// its edges; marks must come clear and are left clear.
func (m *joinSide) Init(values []uint32, dim uint32, marks []bool, column bool) {
	if m.Ready {
		return
	}
	m.Ready = true
	m.Flipped = uint64(len(values))*2 > uint64(dim)
	m.Small = values
	if !m.Flipped && !column {
		return
	}
	for _, value := range values {
		marks[value] = true
	}
	if m.Flipped {
		m.Small = make([]uint32, 0, int(dim)-len(values))
		for value := range dim {
			if !marks[value] {
				m.Small = append(m.Small, value)
			}
		}
	}
	if column {
		// An edge lies between a member and a non-member, so a side and
		// its complement share them.
		m.Edges = make([]uint32, 0, 2*len(m.Small))
		for _, value := range m.Small {
			if value > 0 && marks[value-1] != marks[value] {
				m.Edges = append(m.Edges, value)
			}
			if value+1 < dim && marks[value+1] != marks[value] {
				m.Edges = append(m.Edges, value+1)
			}
		}
	}
	for _, value := range values {
		marks[value] = false
	}
}

// pairLists lists the pairs holding each value of a dimension,
// ascending.
type pairLists struct {
	Offsets []int
	Pairs   []uint32
}

// newPairLists lists the pairs of the dimension, values[pair] holding
// the values of the pair.
func newPairLists(dim uint32, values [][]uint32) pairLists {
	lists := pairLists{Offsets: make([]int, dim+1)}
	for _, pairValues := range values {
		for _, value := range pairValues {
			lists.Offsets[value]++
		}
	}
	for value := range dim {
		lists.Offsets[value+1] += lists.Offsets[value]
	}
	lists.Pairs = make([]uint32, lists.Offsets[dim])
	// Filling backward moves every end to its start.
	for pair, pairValues := range slices.Backward(values) {
		for _, value := range pairValues {
			lists.Offsets[value]--
			lists.Pairs[lists.Offsets[value]] = uint32(pair)
		}
	}
	return lists
}

// Get returns the pairs holding a value.
func (m *pairLists) Get(value uint32) []uint32 {
	return m.Pairs[m.Offsets[value]:m.Offsets[value+1]]
}

// paintNode is a pair painting one column of the prepared row, linked to
// the pair painted there before: a link is a node index plus one, zero
// ending the list, so clearing the heads empties every list.
type paintNode struct {
	Pair uint32
	Next uint32
}

// joiner paints the cells of a join row by row.
//
// With a' the small indicator of a side, a flipped side is a = 1 + a'
// over GF(2), so the pairs covering a cell are S = K + R(v) + C(h) +
// P(v, h): the pairs flipped on both sides, the column-flipped pairs
// painting the row, the row-flipped pairs painting the column and the
// pairs painting the cell. A class is keyed by S + K, the sum of the
// other three, which costs only the small sides.
//
// A cell differs from its left neighbour by the pairs covering the row
// with an edge at the column, and from its upper one by the pairs with an
// edge at the row covering the column; a cell with a neighbour alike
// takes its class without keying it.
type joiner struct {
	VDim uint32
	HDim uint32
	// Rows and Cols hold the sides of every pair.
	Rows []*joinSide
	Cols []*joinSide
	// Painted lists the pairs painting each row; ColTerms the C terms of
	// each column.
	Painted  pairLists
	ColTerms pairLists
	// Common holds the K terms.
	Common []uint32
	// ColHash holds the hash of the C terms of each column.
	ColHash []uint32
	// LeftBase counts at each column the row-flipped pairs with an edge
	// there, which cover every row they do not paint.
	LeftBase []uint32
	// ColTermIDs numbers the C terms of each column, columns with equal
	// terms alike; RowClasses holds, per number, the class the unpainted
	// cells with those terms take in the row it is stamped with.
	ColTermIDs []uint32
	RowClasses []rowClass
	Classes    classSet

	// The prepared row: the counts of the pairs telling each cell from
	// its left and upper neighbours, the upper ones as UpBase plus Up,
	// the paint lists of its columns, and its R terms with their hash.
	Left     []uint32
	Up       []uint32
	UpBase   uint32
	Heads    []uint32
	Nodes    []paintNode
	RowTerms []uint32
	RowHash  uint32
	// The scratch of the paint, the R and C terms and the key of one
	// cell, each the length of the pair count.
	CellPaint []uint32
	Terms     []uint32
	Key       []uint32
}

func newJoiner(left, right *Stage, pairs []groupPair) joiner {
	vDim, hDim := left.MaxValue+1, right.MaxValue+1
	m := joiner{
		VDim:     vDim,
		HDim:     hDim,
		Rows:     make([]*joinSide, len(pairs)),
		Cols:     make([]*joinSide, len(pairs)),
		ColHash:  make([]uint32, hDim),
		LeftBase: make([]uint32, hDim),
		Left:     make([]uint32, hDim),
		Up:       make([]uint32, hDim),
		Heads:    make([]uint32, hDim),
		// A pair set of a cell or a row holds every pair at most once.
		CellPaint: make([]uint32, len(pairs)),
		Terms:     make([]uint32, len(pairs)),
		Key:       make([]uint32, len(pairs)),
	}
	leftSides := make([]joinSide, left.GroupCount())
	rightSides := make([]joinSide, right.GroupCount())
	marks := make([]bool, max(vDim, hDim))
	rowValues := make([][]uint32, len(pairs))
	colValues := make([][]uint32, len(pairs))
	for pair, groups := range pairs {
		rows := &leftSides[groups.Left]
		cols := &rightSides[groups.Right]
		rows.Init(left.Group(groups.Left), vDim, marks, false)
		cols.Init(right.Group(groups.Right), hDim, marks, true)
		m.Rows[pair], m.Cols[pair] = rows, cols
		rowValues[pair] = rows.Small
		if !rows.Flipped {
			continue
		}
		if cols.Flipped {
			m.Common = append(m.Common, uint32(pair))
		}
		colValues[pair] = cols.Small
		hash := pairHash(uint32(pair))
		for _, h := range cols.Small {
			m.ColHash[h] ^= hash
		}
		for _, h := range cols.Edges {
			m.LeftBase[h]++
		}
	}
	m.Painted = newPairLists(vDim, rowValues)
	m.ColTerms = newPairLists(hDim, colValues)
	var termsCount uint32
	m.ColTermIDs, termsCount = numberLists(&m.ColTerms, hDim)
	m.RowClasses = make([]rowClass, termsCount)
	return m
}

// rowClass is the class of the unpainted cells with some C terms in the
// row stamped, the row plus one.
type rowClass struct {
	Row   uint32
	Class uint32
}

// numberLists numbers the pair lists of a dimension in order of first
// appearance, equal lists alike, and returns the numbers and their count.
func numberLists(lists *pairLists, dim uint32) ([]uint32, uint32) {
	numbers := make([]uint32, dim)
	index := map[string]uint32{}
	for value := range dim {
		pairs := lists.Get(value)
		key := unsafe.String((*byte)(unsafe.Pointer(unsafe.SliceData(pairs))), len(pairs)*int(unsafe.Sizeof(uint32(0))))
		number, ok := index[key]
		if !ok {
			number = uint32(len(index))
			index[key] = number
		}
		numbers[value] = number
	}
	return numbers, uint32(len(index))
}

// PrepareRow counts what tells every cell of a row from its neighbours
// and lists the pairs painting each of its columns. The counts wrap, the
// true ones being at most the pair count.
//
// The lists grow in locals stored back once a row: a store of a slice
// into the joiner is a pointer write, which costs a barrier check.
func (m *joiner) PrepareRow(v uint32) {
	painted := m.Painted.Get(v)
	left, heads := m.Left, m.Heads
	copy(left, m.LeftBase)
	clear(heads)
	clear(m.Up)
	nodes := m.Nodes[:0]
	rowTerms := m.RowTerms[:0]
	rowHash := uint32(0)
	for _, pair := range painted {
		cols := m.Cols[pair]
		if cols.Flipped {
			rowTerms = append(rowTerms, pair)
			rowHash ^= pairHash(pair)
		}
		sign := uint32(1)
		if m.Rows[pair].Flipped {
			sign = ^uint32(0)
		}
		for _, h := range cols.Edges {
			left[h] += sign
		}
		for _, h := range cols.Small {
			nodes = append(nodes, paintNode{Pair: pair, Next: heads[h]})
			heads[h] = uint32(len(nodes))
		}
	}
	m.Nodes, m.RowTerms, m.RowHash = nodes, rowTerms, rowHash
	// The first row reads no row above: every cell of it differs.
	if v == 0 {
		m.UpBase = 1
		return
	}
	// The pairs with an edge at the row paint one of it and the row above.
	changed := m.Key[:xorInto(m.Key, painted, m.Painted.Get(v-1))]
	m.UpBase = 0
	for _, pair := range changed {
		if m.Cols[pair].Flipped {
			m.UpBase++
		}
	}
	up := m.Up
	for _, pair := range changed {
		cols := m.Cols[pair]
		sign := uint32(1)
		if cols.Flipped {
			sign = ^uint32(0)
		}
		for _, h := range cols.Small {
			up[h] += sign
		}
	}
}

// CellClass returns the class of a cell of the prepared row v.
//
// The key of an unpainted cell is the R terms of its row and the C terms
// of its column alone, so the unpainted cells of a row with equal C terms
// share a class, keyed once.
func (m *joiner) CellClass(v, h uint32) (uint32, error) {
	unpainted := m.Heads[h] == 0
	cached := &m.RowClasses[m.ColTermIDs[h]]
	if unpainted && cached.Row == v+1 {
		return cached.Class, nil
	}
	// A list holds the latest pair first, so the paint fills from the
	// end of its scratch.
	paint := m.CellPaint[len(m.CellPaint):]
	hash := m.RowHash ^ m.ColHash[h]
	for node := m.Heads[h]; node != 0; node = m.Nodes[node-1].Next {
		pair := m.Nodes[node-1].Pair
		paint = m.CellPaint[len(m.CellPaint)-len(paint)-1:]
		paint[0] = pair
		hash ^= pairHash(pair)
	}
	terms := xorRuns(m.Terms, m.RowTerms, m.ColTerms.Get(h))
	key := xorRuns(m.Key, terms, paint)
	class, err := m.Classes.Intern(hash, key)
	if err == nil && unpainted {
		*cached = rowClass{Row: v + 1, Class: class}
	}
	return class, err
}

// PaintTable writes the class of every cell into the table, a row at a time.
func (m *joiner) PaintTable(table *layout.ValueTable) error {
	child := table.MemoryContext.Get()
	rows := unsafe.Slice(table.Values.Get(), m.VDim)
	var above []uint32
	for v := range m.VDim {
		chunk := shmalloc.ContextAlloc(child, uintptr(m.HDim)*unsafe.Sizeof(uint32(0)))
		if chunk == nil {
			return errNoMemory
		}
		rows[v].Set((*uint32)(chunk))
		cells := unsafe.Slice((*uint32)(chunk), m.HDim)
		m.PrepareRow(v)
		// The first row reads no row above, its up counts all nonzero.
		if v == 0 {
			above = cells
		}
		left, up, upper := m.Left[:len(cells)], m.Up[:len(cells)], above[:len(cells)]
		upBase := m.UpBase
		class := uint32(0)
		for h := range cells {
			if h == 0 || left[h] != 0 {
				if upBase+up[h] == 0 {
					class = upper[h]
				} else {
					var err error
					if class, err = m.CellClass(v, uint32(h)); err != nil {
						return err
					}
				}
			}
			cells[h] = class
		}
		above = cells
	}
	return nil
}

// Joint returns what the join numbered: the pair of every rule and the
// classes keyed by their pairs.
func (m *joiner) Joint(ruleGroups []uint32) *Joint {
	return &Joint{
		RuleGroups: ruleGroups,
		PairCount:  uint32(len(m.Rows)),
		Classes:    m.Classes,
		Common:     m.Common,
	}
}

// Joint is the stage a join produces, kept as the classes it numbered
// rather than as the classes of every pair: a class lists the pairs
// covering it, which is what the rule table reads of it, and the two
// forms hold the same count of entries, each class listing a pair the
// pair lists the class. Flipped pairs keep the listing short: the pairs
// covering a class are those either of its key or of Common, but not
// both.
type Joint struct {
	// RuleGroups holds the pair of every rule, or InvalidGroup for a rule
	// without a group on either side.
	RuleGroups []uint32
	PairCount  uint32
	Classes    classSet
	// Common holds the pairs flipped on both sides.
	Common []uint32
}

// ClassCount returns the count of classes; the largest class is one
// less.
func (m *Joint) ClassCount() uint32 {
	return m.Classes.Count()
}

// AppendPairs appends the pairs covering a class to out, ascending.
func (m *Joint) AppendPairs(out []uint32, class uint32) []uint32 {
	return appendXor(out, m.Classes.Key(class), m.Common)
}

// Stage returns the joint as the stage another join takes: the classes
// of every pair, ascending, as the registry of classify_join holds them.
func (m *Joint) Stage() *Stage {
	classCount := m.ClassCount()
	stage := &Stage{
		RuleGroups: m.RuleGroups,
		Starts:     make([]uint32, m.PairCount+1),
		MaxValue:   classCount - 1,
	}
	var pairs []uint32
	for class := range classCount {
		pairs = m.AppendPairs(pairs[:0], class)
		for _, pair := range pairs {
			stage.Starts[pair+1]++
		}
	}
	for pair := range m.PairCount {
		stage.Starts[pair+1] += stage.Starts[pair]
	}
	stage.Values = make([]uint32, stage.Starts[m.PairCount])
	filled := slices.Clone(stage.Starts[:m.PairCount])
	for class := range classCount {
		pairs = m.AppendPairs(pairs[:0], class)
		for _, pair := range pairs {
			stage.Values[filled[pair]] = class
			filled[pair]++
		}
	}
	return stage
}

// joinClass is a class with its key: up to two pairs in place, a free
// place none, or none and the pool offset of a longer key's count and
// pairs. A block list of ten thousand sources by ten thousand
// destinations makes a hundred million two-pair classes, so the short
// keys stay out of the pool.
type joinClass struct {
	Hash uint32
	Key  [2]uint32
}

// classSlot is a slot of the class index; a free one holds the id none.
type classSlot struct {
	Hash uint32
	ID   uint32
}

// classSet interns the classes by their exact keys, numbering each new
// one next; its slots stay at most half full.
type classSet struct {
	Items []joinClass
	Slots []classSlot
	Pool  []uint32
}

// Count returns the count of classes.
func (m *classSet) Count() uint32 {
	return uint32(len(m.Items))
}

// Key returns the pairs of a class, ascending.
func (m *classSet) Key(id uint32) []uint32 {
	key := m.Items[id].Key[:]
	switch {
	case key[0] != none && key[1] != none:
		return key
	case key[0] != none:
		return key[:1]
	case key[1] == none:
		return nil
	}
	offset := key[1]
	return m.Pool[offset+1 : offset+1+m.Pool[offset]]
}

// Intern returns the class of a key, numbering it next when new.
func (m *classSet) Intern(hash uint32, key []uint32) (uint32, error) {
	mask := uint32(len(m.Slots) - 1)
	if len(m.Slots) > 0 {
		for pos := hash & mask; m.Slots[pos].ID != none; pos = (pos + 1) & mask {
			slot := m.Slots[pos]
			if slot.Hash == hash && slices.Equal(m.Key(slot.ID), key) {
				return slot.ID, nil
			}
		}
	}
	if len(m.Items) >= len(m.Slots)/2 {
		if err := m.grow(); err != nil {
			return none, err
		}
		mask = uint32(len(m.Slots) - 1)
	}

	id := uint32(len(m.Items))
	class := joinClass{Hash: hash, Key: [2]uint32{none, none}}
	if len(key) <= 2 {
		copy(class.Key[:], key)
	} else {
		if uint64(len(m.Pool))+1+uint64(len(key)) >= uint64(none) {
			return none, errTooManyClasses
		}
		class.Key[1] = uint32(len(m.Pool))
		m.Pool = append(m.Pool, uint32(len(key)))
		m.Pool = append(m.Pool, key...)
	}
	m.Items = append(m.Items, class)
	m.place(classSlot{Hash: hash, ID: id}, mask)
	return id, nil
}

// grow doubles the slots and places every class anew.
func (m *classSet) grow() error {
	size := max(2, 2*len(m.Slots))
	if size > 1<<31 {
		return errTooManyClasses
	}
	m.Items = slices.Grow(m.Items, size/2-len(m.Items))
	m.Slots = make([]classSlot, size)
	for pos := range m.Slots {
		m.Slots[pos].ID = none
	}
	mask := uint32(size - 1)
	for id, class := range m.Items {
		m.place(classSlot{Hash: class.Hash, ID: uint32(id)}, mask)
	}
	return nil
}

// place puts a slot at the first free position of its probe path.
func (m *classSet) place(slot classSlot, mask uint32) {
	pos := slot.Hash & mask
	for m.Slots[pos].ID != none {
		pos = (pos + 1) & mask
	}
	m.Slots[pos] = slot
}

// allocTable allocates a table of vDim rows of hDim cells under context,
// its rows yet unset, as value_table_init does.
func allocTable(table *layout.ValueTable, context *layout.MemoryContext, name string, vDim, hDim uint32) error {
	childSize := unsafe.Sizeof(layout.MemoryContext{})
	block := shmalloc.ContextAlloc(context, childSize)
	if block == nil {
		return errNoMemory
	}
	child := (*layout.MemoryContext)(block)
	shmalloc.ContextInitFrom(child, context, name)
	table.MemoryContext.Set(child)
	table.VDim = vDim
	table.HDim = hDim
	rowsSize := uintptr(vDim) * unsafe.Sizeof(shm.Ptr[uint32]{})
	rows := shmalloc.ContextAlloc(child, rowsSize)
	if rows == nil {
		FreeJoin(table)
		return errNoMemory
	}
	clear(unsafe.Slice((*byte)(rows), rowsSize))
	table.Values.Set((*shm.Ptr[uint32])(rows))
	return nil
}

// FreeJoin releases what Join wrote into a value table; it is safe on a
// zeroed table.
func FreeJoin(table *layout.ValueTable) {
	child := table.MemoryContext.Get()
	if child == nil {
		return
	}
	if values := table.Values.Get(); values != nil {
		rows := unsafe.Slice(values, table.VDim)
		for v := range rows {
			if row := rows[v].Get(); row != nil {
				shmalloc.ContextFree(child, unsafe.Pointer(row), uintptr(table.HDim)*unsafe.Sizeof(uint32(0)))
				rows[v].Set(nil)
			}
		}
		shmalloc.ContextFree(child, unsafe.Pointer(values), uintptr(table.VDim)*unsafe.Sizeof(shm.Ptr[uint32]{}))
		table.Values.Set(nil)
	}
	parent := child.Parent.Get()
	shmalloc.ContextFini(child)
	shmalloc.ContextFree(parent, unsafe.Pointer(child), unsafe.Sizeof(layout.MemoryContext{}))
	table.MemoryContext.Set(nil)
}
