//go:build linux && (amd64 || arm64)

package classify

import (
	"bytes"
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// errRuleTableMemory reports that the memory of a rule table ran out.
var errRuleTableMemory = fmt.Errorf("rule table memory: %w", shmalloc.ErrNoMemory)

// JoinRules joins the stage, its region values the rows, with the right
// one, its values the columns, into a rule table of the first member
// rule of every cell, as classify_join_rules does.
//
// A member rule with a group on both sides belongs to the pair of its
// groups, pairs numbered by first appearance. A cell takes the first
// rule of the pairs covering it, or InvalidRule. Equal rows are stored
// once, numbered by first appearance, and the class map gives every row
// its stored one. The table takes a child memory context of context
// named name, its map a child of that titled classes; a failure frees
// the table and leaves it zeroed, as C does.
//
// Every distinct row goes into the table's memory as the join finds it;
// the row array and the class map follow once every row is in.
func (m *Stage) JoinRules(right *Stage, members []bool, table *layout.RuleTable, context *layout.MemoryContext, name string) error {
	if len(right.ruleGroups) != len(m.ruleGroups) || len(members) != len(m.ruleGroups) {
		return fmt.Errorf(
			"the stages group %d and %d rules of %d members: %w",
			len(m.ruleGroups), len(right.ruleGroups), len(members), ErrInvalidRule,
		)
	}
	*table = layout.RuleTable{}
	tableContext := registry.AllocNode[layout.MemoryContext](context, 0)
	if tableContext == nil {
		return errRuleTableMemory
	}
	shmalloc.ContextInitFrom(tableContext, context, name)
	table.MemoryContext.Set(tableContext)
	table.VDim = m.maxValue + 1
	table.HDim = right.maxValue + 1

	rows, classMap, err := m.ruleRows(right, members, tableContext)
	if err == nil {
		err = commitRuleTable(table, rows, classMap)
	}
	if err != nil {
		// The table owns the rows once its row array is in.
		if table.Rows.IsNil() {
			for _, row := range rows {
				registry.FreeArray(tableContext, row)
			}
		}
		FreeRuleTable(table)
		return err
	}
	return nil
}

// ruleRows stores the distinct rows of first member rules in the table's
// memory context, in order of first appearance, and returns them and the
// row of every value of the stage.
//
// A row no member pair covers is stored the first time it appears,
// without looking for an equal one, as C stores it. A failure returns
// the rows stored so far for the caller to free.
func (m *Stage) ruleRows(right *Stage, members []bool, context *layout.MemoryContext) ([][]uint32, []uint32, error) {
	memberGroups := make([]uint32, len(m.ruleGroups))
	for rule, group := range m.ruleGroups {
		memberGroups[rule] = InvalidGroup
		if members[rule] {
			memberGroups[rule] = group
		}
	}
	rulePairs, pairs := pairRules(memberGroups, right.ruleGroups)
	// Pairs are numbered as they first appear, so a pair's first rule is
	// the one that numbers it and ascends with the pair.
	firstRules := make([]uint32, 0, len(pairs))
	for rule, pair := range rulePairs {
		if pair != InvalidGroup && int(pair) == len(firstRules) {
			firstRules = append(firstRules, uint32(rule))
		}
	}
	leftValues := make([][]uint32, len(pairs))
	for pair, groups := range pairs {
		leftValues[pair] = m.group(groups.Left)
	}
	covering := newLists(m.maxValue+1, leftValues)

	row := slices.Repeat([]uint32{InvalidRule}, int(right.maxValue)+1)
	cells := valueBytes(row)
	var rows [][]uint32
	// The index numbers the stored rows by the hashes of their cells.
	numbers := newHashIndex()
	classMap := make([]uint32, m.maxValue+1)
	// paintedIn stamps every right group with the value whose row it
	// painted last, plus one.
	paintedIn := make([]uint32, right.GroupCount())
	var touched []uint32
	uncovered := none
	for value := range m.maxValue + 1 {
		pairsOfValue := covering.Get(value)
		if len(pairsOfValue) == 0 && uncovered != none {
			classMap[value] = uncovered
			continue
		}
		// A row hashes as C hashes it: the xor of the hashes of its
		// painted cells, each by its column and its rule.
		hash := uint32(0)
		for _, pair := range pairsOfValue {
			// The cells of a right group painted in this row already hold
			// an earlier or the same rule.
			group := pairs[pair].Right
			if paintedIn[group] == value+1 {
				continue
			}
			paintedIn[group] = value + 1
			for _, column := range right.group(group) {
				if row[column] == InvalidRule {
					row[column] = firstRules[pair]
					touched = append(touched, column)
					hash ^= mergeHash(column, firstRules[pair])
				}
			}
		}
		number := none
		if len(pairsOfValue) != 0 {
			number = numbers.Find(hash, func(number uint32) bool {
				return bytes.Equal(valueBytes(rows[number]), cells)
			})
		}
		if number == none {
			stored, ok := registry.AllocArray[uint32](context, uint64(len(row)))
			if !ok {
				return rows, nil, errRuleTableMemory
			}
			copy(stored, row)
			number = numbers.Add(hash)
			rows = append(rows, stored)
		}
		if len(pairsOfValue) == 0 {
			uncovered = number
		}
		classMap[value] = number
		for _, column := range touched {
			row[column] = InvalidRule
		}
		touched = touched[:0]
	}
	return rows, classMap, nil
}

// valueBytes returns the bytes of a list of values in place.
func valueBytes(values []uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(values))), len(values)*4)
}

// commitRuleTable hands the stored rows and the class map to a rule
// table as classify_join_rules publishes them: the row array first, the
// map once every row is in.
//
// A failure leaves what it wrote for the caller to free.
func commitRuleTable(table *layout.RuleTable, rows [][]uint32, classMap []uint32) error {
	tableContext := table.MemoryContext.Get()
	pointers, ok := registry.AllocArray[shm.Ptr[uint32]](tableContext, uint64(len(rows)))
	if !ok {
		return errRuleTableMemory
	}
	for number, values := range rows {
		pointers[number].Set(unsafe.SliceData(values))
	}
	table.Rows.Set(unsafe.SliceData(pointers))
	table.ClassCount = uint32(len(rows))

	values, err := initVline(&table.ClassMap, tableContext, "classes", uint32(len(classMap)))
	if err != nil {
		return err
	}
	copy(values, classMap)
	return nil
}

// FreeRuleTable releases what a join wrote into a rule table and zeroes
// it, as rule_table_free does; it is safe on a zeroed table.
func FreeRuleTable(table *layout.RuleTable) {
	context := table.MemoryContext.Get()
	if context == nil {
		*table = layout.RuleTable{}
		return
	}
	pointers := registry.Slice(table.Rows.Get(), uint64(table.ClassCount))
	for idx := range pointers {
		registry.FreeArray(context, registry.Slice(pointers[idx].Get(), uint64(table.HDim)))
	}
	registry.FreeArray(context, pointers)
	freeVline(&table.ClassMap)
	parent := context.Parent.Get()
	shmalloc.ContextFini(context)
	registry.FreeNode(parent, context, 0)
	*table = layout.RuleTable{}
}
