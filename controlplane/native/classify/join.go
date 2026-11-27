//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// joinClassLimit is the count of classes a join stops at, as C's class
// index stops growing there.
const joinClassLimit = 1 << 30

// errJoinClasses reports a join whose cells hold more classes than C
// numbers.
var errJoinClasses = fmt.Errorf("the join makes %d classes or more: %w", joinClassLimit, ErrInvalidRule)

// Join joins the stage, its region values the rows, with the right
// one, its values the columns, into a value table of the class of every
// cell, as classify_join does, and returns the joint.
//
// A rule with a group on both sides belongs to the pair of its groups,
// pairs numbered by first appearance. A cell's class is the set of pairs
// covering it, numbered by first appearance in row-major order, and the
// joint lists every pair's classes ascending. The table takes a child
// memory context of context titled for the join; a failure frees it and
// leaves it zeroed, as C does.
func (m *Stage) Join(right *Stage, table *layout.ValueTable, context *layout.MemoryContext, name string) (*Stage, error) {
	if len(m.ruleGroups) != len(right.ruleGroups) {
		return nil, fmt.Errorf("the stages group %d and %d rules: %w", len(m.ruleGroups), len(right.ruleGroups), ErrInvalidRule)
	}
	ruleGroups, pairs := pairRules(m.ruleGroups, right.ruleGroups)
	joiner := m.newJoiner(right, pairs)
	rows, err := initValueTable(table, context, leafName(name, "table"), joiner.VDim, joiner.HDim)
	if err != nil {
		*table = layout.ValueTable{}
		return nil, err
	}
	if err := joiner.Paint(rows); err != nil {
		FreeJoin(table)
		return nil, err
	}
	return joiner.Stage(ruleGroups), nil
}

// FreeJoin releases the value table a join wrote and zeroes it, as
// value_table_free does; it is safe on a zeroed table.
func FreeJoin(table *layout.ValueTable) {
	freeValueTable(table)
	*table = layout.ValueTable{}
}

// groupPair is the left and the right group of a rule.
type groupPair struct {
	Left  uint32
	Right uint32
}

// pairRules returns the pair of every rule, or InvalidGroup for a rule
// without a group on either side, and the distinct pairs in order of
// first appearance.
func pairRules(left, right []uint32) ([]uint32, []groupPair) {
	ruleGroups := make([]uint32, len(left))
	numbers := map[groupPair]uint32{}
	var pairs []groupPair
	for idx := range left {
		if left[idx] == InvalidGroup || right[idx] == InvalidGroup {
			ruleGroups[idx] = InvalidGroup
			continue
		}
		groups := groupPair{Left: left[idx], Right: right[idx]}
		number, ok := numbers[groups]
		if !ok {
			number = uint32(len(pairs))
			numbers[groups] = number
			pairs = append(pairs, groups)
		}
		ruleGroups[idx] = number
	}
	return ruleGroups, pairs
}

// mergeHash hashes two numbers, as merge_group_pair_hash does.
func mergeHash(left, right uint32) uint32 {
	return (left*2654435761 ^ right*2246822519) * 3266489917
}

// pairHash hashes a pair, as join_pair_hash does; a set of pairs hashes
// as the xor of its pairs' hashes.
func pairHash(pair uint32) uint32 {
	hash := pair + 1
	hash ^= hash >> 16
	hash *= 0x85ebca6b
	hash ^= hash >> 13
	hash *= 0xc2b2ae35
	return hash ^ hash>>16
}

// xorInto writes the values held by exactly one of two ascending runs
// into out, ascending, and returns how many; out has room for them.
func xorInto(out, left, right []uint32) int {
	count, leftIdx, rightIdx := 0, 0, 0
	for leftIdx < len(left) && rightIdx < len(right) {
		switch value := left[leftIdx]; {
		case value < right[rightIdx]:
			out[count] = value
			count++
			leftIdx++
		case value > right[rightIdx]:
			out[count] = right[rightIdx]
			count++
			rightIdx++
		default:
			leftIdx++
			rightIdx++
		}
	}
	count += copy(out[count:], left[leftIdx:])
	return count + copy(out[count:], right[rightIdx:])
}

// xorRuns returns the values held by exactly one of two ascending runs,
// ascending: one run as it is when the other is empty, else written into
// out, which has room for them.
func xorRuns(out, left, right []uint32) []uint32 {
	switch {
	case len(right) == 0:
		return left
	case len(left) == 0:
		return right
	}
	return out[:xorInto(out, left, right)]
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

// lists holds a list of numbers for every index, list after list.
type lists struct {
	Offsets []int
	Items   []uint32
}

// newLists lists, for every value of a dimension, the indexes whose
// values hold it, ascending.
func newLists(dim uint32, values [][]uint32) lists {
	listed := lists{Offsets: make([]int, dim+1)}
	for _, indexValues := range values {
		for _, value := range indexValues {
			listed.Offsets[value]++
		}
	}
	for value := range dim {
		listed.Offsets[value+1] += listed.Offsets[value]
	}
	listed.Items = make([]uint32, listed.Offsets[dim])
	// Filling backward moves every end to its start.
	for idx, indexValues := range slices.Backward(values) {
		for _, value := range indexValues {
			listed.Offsets[value]--
			listed.Items[listed.Offsets[value]] = uint32(idx)
		}
	}
	return listed
}

// Get returns the list of an index.
func (m *lists) Get(idx uint32) []uint32 {
	return m.Items[m.Offsets[idx]:m.Offsets[idx+1]]
}

// paintNode is a pair painting one column of the prepared row, linked to
// the pair painted there before: a link is a node index plus one, zero
// ending the list, so clearing the heads empties every list.
type paintNode struct {
	Pair uint32
	Next uint32
}

// rowClass is the class of the unpainted cells with some column terms
// in the row stamped, the row plus one.
type rowClass struct {
	Row   uint32
	Class uint32
}

// joiner paints the cells of a join row by row, as C's join does.
//
// With a' the small indicator of a side, a flipped side is a = 1 + a'
// over GF(2), so the pairs covering a cell are S = K + R(v) + C(h) +
// P(v, h): the pairs flipped on both sides, the column-flipped pairs
// painting the row, the row-flipped pairs painting the column and the
// pairs painting the cell. A class is keyed by S + K, the sum of the
// other three, which costs only the small sides. A cell differs from its
// left neighbour by the pairs covering the row with an edge at the
// column, and from its upper one by the pairs with an edge at the row
// covering the column; a cell with a neighbour alike takes its class
// without keying it.
type joiner struct {
	VDim uint32
	HDim uint32
	// Rows and Columns hold the sides of every pair.
	Rows    []*joinSide
	Columns []*joinSide
	// Painted lists the pairs painting each row; ColumnTerms the C terms of
	// each column, and ColumnHashes their hash.
	Painted      lists
	ColumnTerms  lists
	ColumnHashes []uint32
	// Common holds the K terms.
	Common []uint32
	// LeftBase counts at each column the row-flipped pairs with an edge
	// there, which cover every row they do not paint.
	LeftBase []uint32
	// ColumnTermIDs numbers the C terms of each column, columns with equal
	// terms alike; RowClasses holds, per number, the class the unpainted
	// cells with those terms take in the row it is stamped with.
	ColumnTermIDs []uint32
	RowClasses    []rowClass
	// Classes numbers the classes by the hashes of their keys; Keys
	// holds the key of every class.
	Classes hashIndex
	Keys    lists

	// The prepared row: the counts of the pairs telling each cell from
	// its left and upper neighbours, the paint lists of its columns, and
	// its R terms with their hash.
	Left     []uint32
	Up       []uint32
	Heads    []uint32
	Nodes    []paintNode
	RowTerms []uint32
	RowHash  uint32
	// The scratch of the paint, the terms and the key of one cell, each
	// the length of the pair count.
	CellPaint []uint32
	Terms     []uint32
	Key       []uint32
}

// newJoiner prepares the join of the pairs, the stage's values the rows
// and the right one's the columns.
func (m *Stage) newJoiner(right *Stage, pairs []groupPair) *joiner {
	vDim, hDim := m.maxValue+1, right.maxValue+1
	joined := &joiner{
		VDim:         vDim,
		HDim:         hDim,
		Rows:         make([]*joinSide, len(pairs)),
		Columns:      make([]*joinSide, len(pairs)),
		LeftBase:     make([]uint32, hDim),
		ColumnHashes: make([]uint32, hDim),
		Classes:      newHashIndex(),
		Keys:         lists{Offsets: []int{0}},
		Left:         make([]uint32, hDim),
		Up:           make([]uint32, hDim),
		Heads:        make([]uint32, hDim),
		// A pair set of a cell or a row holds every pair at most once.
		CellPaint: make([]uint32, len(pairs)),
		Terms:     make([]uint32, len(pairs)),
		Key:       make([]uint32, len(pairs)),
	}
	leftSides := make([]joinSide, m.GroupCount())
	rightSides := make([]joinSide, right.GroupCount())
	marks := make([]bool, max(vDim, hDim))
	rowValues := make([][]uint32, len(pairs))
	columnValues := make([][]uint32, len(pairs))
	for pair, groups := range pairs {
		rows := &leftSides[groups.Left]
		columns := &rightSides[groups.Right]
		rows.Init(m.group(groups.Left), vDim, marks, false)
		columns.Init(right.group(groups.Right), hDim, marks, true)
		joined.Rows[pair], joined.Columns[pair] = rows, columns
		rowValues[pair] = rows.Small
		if !rows.Flipped {
			continue
		}
		if columns.Flipped {
			joined.Common = append(joined.Common, uint32(pair))
		}
		columnValues[pair] = columns.Small
		hash := pairHash(uint32(pair))
		for _, column := range columns.Small {
			joined.ColumnHashes[column] ^= hash
		}
		for _, column := range columns.Edges {
			joined.LeftBase[column]++
		}
	}
	joined.Painted = newLists(vDim, rowValues)
	joined.ColumnTerms = newLists(hDim, columnValues)

	// The columns are numbered by their terms, each number's first column
	// holding them.
	ids := newHashIndex()
	var firsts []uint32
	joined.ColumnTermIDs = make([]uint32, hDim)
	for column := range hDim {
		terms := joined.ColumnTerms.Get(column)
		hash := joined.ColumnHashes[column]
		id := ids.Find(hash, func(id uint32) bool {
			return slices.Equal(joined.ColumnTerms.Get(firsts[id]), terms)
		})
		if id == none {
			id = ids.Add(hash)
			firsts = append(firsts, column)
		}
		joined.ColumnTermIDs[column] = id
	}
	joined.RowClasses = make([]rowClass, len(firsts))
	return joined
}

// PrepareRow counts what tells every cell of a row from its neighbours
// and lists the pairs painting each of its columns. The counts wrap, the
// true ones being at most the pair count.
func (m *joiner) PrepareRow(row uint32) {
	painted := m.Painted.Get(row)
	// The row works on local slices, stored back once: storing a slice
	// into the joiner per item costs a write barrier check.
	left, heads, up := m.Left, m.Heads, m.Up
	nodes, rowTerms := m.Nodes[:0], m.RowTerms[:0]
	rowHash := uint32(0)
	copy(left, m.LeftBase)
	clear(heads)
	for _, pair := range painted {
		columns := m.Columns[pair]
		if columns.Flipped {
			rowTerms = append(rowTerms, pair)
			rowHash ^= pairHash(pair)
		}
		sign := uint32(1)
		if m.Rows[pair].Flipped {
			sign = ^uint32(0)
		}
		for _, column := range columns.Edges {
			left[column] += sign
		}
		for _, column := range columns.Small {
			nodes = append(nodes, paintNode{Pair: pair, Next: heads[column]})
			heads[column] = uint32(len(nodes))
		}
	}
	m.Nodes, m.RowTerms, m.RowHash = nodes, rowTerms, rowHash
	if row == 0 {
		return
	}
	// The pairs with an edge at the row paint one of it and the row above.
	changed := m.Key[:xorInto(m.Key, painted, m.Painted.Get(row-1))]
	base := uint32(0)
	for _, pair := range changed {
		if m.Columns[pair].Flipped {
			base++
		}
	}
	for column := range up {
		up[column] = base
	}
	for _, pair := range changed {
		columns := m.Columns[pair]
		sign := uint32(1)
		if columns.Flipped {
			sign = ^uint32(0)
		}
		for _, column := range columns.Small {
			up[column] += sign
		}
	}
}

// CellClass returns the class of a cell of the prepared row.
//
// The key of an unpainted cell is the R terms of its row and the C terms
// of its column alone, so the unpainted cells of a row with equal C terms
// share a class, keyed once.
func (m *joiner) CellClass(row, column uint32) (uint32, error) {
	unpainted := m.Heads[column] == 0
	cached := &m.RowClasses[m.ColumnTermIDs[column]]
	if unpainted && cached.Row == row+1 {
		return cached.Class, nil
	}
	// A list holds the latest pair first, so the paint fills from the
	// end of its scratch. The key's hash is the xor of its terms' and its
	// paint's: a pair in both cancels out of either.
	start := len(m.CellPaint)
	hash := m.RowHash ^ m.ColumnHashes[column]
	for node := m.Heads[column]; node != 0; node = m.Nodes[node-1].Next {
		start--
		m.CellPaint[start] = m.Nodes[node-1].Pair
		hash ^= pairHash(m.CellPaint[start])
	}
	terms := xorRuns(m.Terms, m.RowTerms, m.ColumnTerms.Get(column))
	key := xorRuns(m.Key, terms, m.CellPaint[start:])
	class := m.Classes.Find(hash, func(class uint32) bool {
		return slices.Equal(m.Keys.Get(class), key)
	})
	if class == none {
		if len(m.Classes.Hashes) == joinClassLimit {
			return none, errJoinClasses
		}
		class = m.Classes.Add(hash)
		m.Keys.Items = append(m.Keys.Items, key...)
		m.Keys.Offsets = append(m.Keys.Offsets, len(m.Keys.Items))
	}
	if unpainted {
		*cached = rowClass{Row: row + 1, Class: class}
	}
	return class, nil
}

// Paint writes the class of every cell into the rows of the table.
func (m *joiner) Paint(rows [][]uint32) error {
	for row, cells := range rows {
		m.PrepareRow(uint32(row))
		left, up := m.Left[:len(cells)], m.Up[:len(cells)]
		var above []uint32
		if row > 0 {
			above = rows[row-1][:len(cells)]
		}
		class := uint32(0)
		for column := range cells {
			if column == 0 || left[column] != 0 {
				if row > 0 && up[column] == 0 {
					class = above[column]
				} else {
					var err error
					if class, err = m.CellClass(uint32(row), uint32(column)); err != nil {
						return err
					}
				}
			}
			cells[column] = class
		}
	}
	return nil
}

// pairsOf returns the pairs covering a class, ascending, in out: those
// either of its key or of the K terms, but not both.
func (m *joiner) pairsOf(out []uint32, class uint32) []uint32 {
	return out[:xorInto(out, m.Keys.Get(class), m.Common)]
}

// Stage returns the joint stage: the pair of every rule and the classes
// of every pair, ascending, as join_collect lists them.
func (m *joiner) Stage(ruleGroups []uint32) *Stage {
	pairCount := uint32(len(m.Rows))
	starts := make([]uint32, pairCount+1)
	pairs := make([]uint32, pairCount)
	for class := range uint32(len(m.Classes.Hashes)) {
		for _, pair := range m.pairsOf(pairs, class) {
			starts[pair+1]++
		}
	}
	for pair := range pairCount {
		starts[pair+1] += starts[pair]
	}
	values := make([]uint32, starts[pairCount])
	filled := slices.Clone(starts[:pairCount])
	for class := range uint32(len(m.Classes.Hashes)) {
		for _, pair := range m.pairsOf(pairs, class) {
			values[filled[pair]] = class
			filled[pair]++
		}
	}
	return &Stage{ruleGroups: ruleGroups, values: values, starts: starts, maxValue: uint32(len(m.Classes.Hashes) - 1)}
}
