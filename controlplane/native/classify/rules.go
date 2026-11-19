//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// InvalidRule marks a cell no rule matches (FILTER_RULE_INVALID).
const InvalidRule = ^uint32(0)

// JoinRules joins a joint and a stage into the rule table of their
// classes in a zeroed table in shared memory, as classify_join_rules
// does; a failed join releases what it wrote and clears the table's
// pointers, its dimensions left as rule_table_free leaves them.
//
// A member rule with a group on both sides belongs to the pair of its
// groups; the pairs are numbered by first appearance, each with its
// first rule. The row of a left class gives every right class the first
// rule of the pairs covering both, or InvalidRule. Equal rows are stored
// once, numbered by first appearance, and the class map gives every left
// class its row. The table lives in a child memory context of context
// named name itself, the map in its child named classes.
//
// The left side is read as the joint keeps it - the groups covering
// each class - so the classes of every group are never listed. A row
// goes to shared memory the first time it appears, and only the cells it
// set are reset for the next, where C holds every row twice and resets
// whole rows.
func JoinRules(left *Joint, right *Stage, members []bool, table *layout.RuleTable, context *layout.MemoryContext, name string) error {
	rows, err := joinRules(left, right, members, table, context, name)
	if err != nil {
		freeRows(table, rows)
		FreeRuleTable(table)
	}
	return err
}

// ruleRows interns the rows of a rule table, each stored in shared
// memory once.
type ruleRows struct {
	Context *layout.MemoryContext
	Columns uint32
	Stored  [][]uint32
	Hashes  []uint32
	// Table holds row numbers plus one, zero for a free slot, at most
	// half full.
	Table []uint32
}

// Store stores a row as the next one and returns its number.
func (m *ruleRows) Store(row []uint32, hash uint32) (uint32, error) {
	chunk := shmalloc.ContextAlloc(m.Context, uintptr(m.Columns)*unsafe.Sizeof(uint32(0)))
	if chunk == nil {
		return 0, errNoMemory
	}
	stored := unsafe.Slice((*uint32)(chunk), m.Columns)
	copy(stored, row)
	number := uint32(len(m.Stored))
	m.Stored = append(m.Stored, stored)
	m.Hashes = append(m.Hashes, hash)
	if 2*len(m.Stored) > len(m.Table) {
		m.Table = groupTable(m.Hashes, max(16, 2*len(m.Table)))
		return number, nil
	}
	m.place(number, hash)
	return number, nil
}

// Intern returns the number of the first stored row equal to a row,
// storing it when none is.
func (m *ruleRows) Intern(row []uint32, hash uint32) (uint32, error) {
	if len(m.Table) > 0 {
		mask := uint32(len(m.Table) - 1)
		for slot := hash & mask; m.Table[slot] != 0; slot = (slot + 1) & mask {
			number := m.Table[slot] - 1
			if m.Hashes[number] == hash && slices.Equal(m.Stored[number], row) {
				return number, nil
			}
		}
	}
	return m.Store(row, hash)
}

// place puts a row number at the first free slot of its probe path.
func (m *ruleRows) place(number, hash uint32) {
	mask := uint32(len(m.Table) - 1)
	slot := hash & mask
	for m.Table[slot] != 0 {
		slot = (slot + 1) & mask
	}
	m.Table[slot] = number + 1
}

func joinRules(left *Joint, right *Stage, members []bool, table *layout.RuleTable, context *layout.MemoryContext, name string) (*ruleRows, error) {
	ruleCount := len(left.RuleGroups)
	if len(right.RuleGroups) != ruleCount || len(members) != ruleCount {
		return nil, fmt.Errorf("the stages group %d and %d rules of %d", ruleCount, len(right.RuleGroups), len(members))
	}
	vDim, hDim := left.ClassCount(), right.MaxValue+1
	if vDim == 0 || hDim == 0 {
		return nil, fmt.Errorf("a rule table of %d by %d classes", vDim, hDim)
	}

	// The pairs of the member rules, the first rule of each, and the
	// pairs of every left group, ascending.
	memberGroups := make([]uint32, ruleCount)
	for idx, group := range left.RuleGroups {
		memberGroups[idx] = InvalidGroup
		if members[idx] {
			memberGroups[idx] = group
		}
	}
	rulePairs, pairs := pairRules(memberGroups, right.RuleGroups)
	firstRule := make([]uint32, len(pairs))
	for rule, pair := range slices.Backward(rulePairs) {
		if pair != InvalidGroup {
			firstRule[pair] = uint32(rule)
		}
	}
	leftGroups := make([][]uint32, len(pairs))
	for pair, groups := range pairs {
		leftGroups[pair] = []uint32{groups.Left}
	}
	pairsOf := newPairLists(left.PairCount, leftGroups)

	block := shmalloc.ContextAlloc(context, unsafe.Sizeof(layout.MemoryContext{}))
	if block == nil {
		return nil, errNoMemory
	}
	tableContext := (*layout.MemoryContext)(block)
	shmalloc.ContextInitFrom(tableContext, context, name)
	table.MemoryContext.Set(tableContext)
	table.VDim = vDim
	table.HDim = hDim

	rows := &ruleRows{Context: tableContext, Columns: hDim}
	classMap := make([]uint32, vDim)
	row := slices.Repeat([]uint32{InvalidRule}, int(hDim))
	var touched, groups, covering []uint32
	// paintedIn stamps every right group with the row it last painted,
	// plus one.
	paintedIn := make([]uint32, right.GroupCount())
	emptyRow := none
	for v := range vDim {
		groups = left.AppendPairs(groups[:0], v)
		covering = covering[:0]
		for _, group := range groups {
			covering = append(covering, pairsOf.Get(group)...)
		}
		// A left class no member pair covers takes the empty row, stored
		// once, as C stores it, without looking for an equal one.
		if len(covering) == 0 {
			if emptyRow == none {
				var err error
				if emptyRow, err = rows.Store(row, 0); err != nil {
					return rows, err
				}
			}
			classMap[v] = emptyRow
			continue
		}
		// A pair has one left group, so the lists hold distinct pairs, and
		// a pair's first rule ascends with the pair.
		slices.Sort(covering)
		hash := uint32(0)
		for _, pair := range covering {
			// A right group painted in the row already has each of its
			// cells taken by the rule that painted it or an earlier one;
			// production rows meet their right groups four times over.
			group := pairs[pair].Right
			if paintedIn[group] == v+1 {
				continue
			}
			paintedIn[group] = v + 1
			first := firstRule[pair]
			for _, h := range right.Group(group) {
				if row[h] == InvalidRule {
					row[h] = first
					touched = append(touched, h)
					hash ^= groupPair{Left: h, Right: first}.Hash()
				}
			}
		}
		number, err := rows.Intern(row, hash)
		if err != nil {
			return rows, err
		}
		classMap[v] = number
		for _, h := range touched {
			row[h] = InvalidRule
		}
		touched = touched[:0]
	}

	pointersSize := uintptr(len(rows.Stored)) * unsafe.Sizeof(shm.Ptr[uint32]{})
	pointers := shmalloc.ContextAlloc(tableContext, pointersSize)
	if pointers == nil {
		return rows, errNoMemory
	}
	stored := unsafe.Slice((*shm.Ptr[uint32])(pointers), len(rows.Stored))
	for number := range stored {
		stored[number].Set(&rows.Stored[number][0])
	}
	table.Rows.Set(&stored[0])
	table.ClassCount = uint32(len(rows.Stored))
	if err := commitVline(&table.ClassMap, tableContext, "classes", classMap); err != nil {
		return nil, err
	}
	return nil, nil
}

// freeRows releases the rows a failed join stored before the table took
// them.
func freeRows(table *layout.RuleTable, rows *ruleRows) {
	if rows == nil || table.Rows.Get() != nil {
		return
	}
	for _, row := range rows.Stored {
		shmalloc.ContextFree(rows.Context, unsafe.Pointer(&row[0]), uintptr(rows.Columns)*unsafe.Sizeof(uint32(0)))
	}
}

// FreeRuleTable releases what JoinRules wrote into a rule table, as
// rule_table_free does; it is safe on a zeroed table.
func FreeRuleTable(table *layout.RuleTable) {
	context := table.MemoryContext.Get()
	if context == nil {
		return
	}
	if pointers := table.Rows.Get(); pointers != nil {
		rows := unsafe.Slice(pointers, table.ClassCount)
		for number := range rows {
			if row := rows[number].Get(); row != nil {
				shmalloc.ContextFree(context, unsafe.Pointer(row), uintptr(table.HDim)*unsafe.Sizeof(uint32(0)))
				rows[number].Set(nil)
			}
		}
		shmalloc.ContextFree(context, unsafe.Pointer(pointers), uintptr(table.ClassCount)*unsafe.Sizeof(shm.Ptr[uint32]{}))
		table.Rows.Set(nil)
	}
	table.ClassCount = 0
	freeVline(&table.ClassMap)
	parent := context.Parent.Get()
	shmalloc.ContextFini(context)
	shmalloc.ContextFree(parent, unsafe.Pointer(context), unsafe.Sizeof(layout.MemoryContext{}))
	table.MemoryContext.Set(nil)
}
