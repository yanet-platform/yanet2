//go:build linux && (amd64 || arm64)

package classify

import (
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
func (m *Stage) JoinRules(right *Stage, members []bool, table *layout.RuleTable, context *layout.MemoryContext, name string) error {
	if len(right.ruleGroups) != len(m.ruleGroups) || len(members) != len(m.ruleGroups) {
		return fmt.Errorf(
			"the stages group %d and %d rules of %d members: %w",
			len(m.ruleGroups), len(right.ruleGroups), len(members), ErrInvalidRule,
		)
	}
	rows, classMap := m.ruleRows(right, members)
	if err := commitRuleTable(table, context, name, rows, classMap, right.maxValue+1); err != nil {
		FreeRuleTable(table)
		return err
	}
	return nil
}

// ruleRows returns the distinct rows of first member rules, as the bytes
// of their cells in order of first appearance, and the row of every
// value of the stage.
//
// A row no member pair covers is stored the first time it appears,
// without looking for an equal one, as C stores it.
func (m *Stage) ruleRows(right *Stage, members []bool) ([]string, []uint32) {
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
	var rows []string
	numbers := map[string]uint32{}
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
				}
			}
		}
		number, ok := numbers[string(cells)]
		if !ok || len(pairsOfValue) == 0 {
			number = uint32(len(rows))
			rows = append(rows, string(cells))
			if !ok {
				numbers[rows[number]] = number
			}
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
	return rows, classMap
}

// valueBytes returns the bytes of a list of values in place.
func valueBytes(values []uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(values))), len(values)*4)
}

// commitRuleTable writes the rows and the class map into a rule table in
// shared memory as classify_join_rules publishes them: the rows first,
// the map once every row is in.
//
// A failure leaves what it wrote for the caller to free.
func commitRuleTable(table *layout.RuleTable, context *layout.MemoryContext, name string, rows []string, classMap []uint32, hDim uint32) error {
	*table = layout.RuleTable{}
	tableContext := registry.AllocNode[layout.MemoryContext](context, 0)
	if tableContext == nil {
		return errRuleTableMemory
	}
	shmalloc.ContextInitFrom(tableContext, context, name)
	table.MemoryContext.Set(tableContext)
	table.VDim = uint32(len(classMap))
	table.HDim = hDim

	pointers, ok := registry.AllocArray[shm.Ptr[uint32]](tableContext, uint64(len(rows)))
	if !ok {
		return errRuleTableMemory
	}
	// The free reads the rows set so far: the rest stay nil.
	clear(pointers)
	table.Rows.Set(unsafe.SliceData(pointers))
	table.ClassCount = uint32(len(rows))
	for number, cells := range rows {
		values, ok := registry.AllocArray[uint32](tableContext, uint64(hDim))
		if !ok {
			return errRuleTableMemory
		}
		copy(valueBytes(values), cells)
		pointers[number].Set(unsafe.SliceData(values))
	}

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
