//go:build linux && (amd64 || arm64)

package classifytest

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// RuleTableReference returns the first member rule of every cell of two
// stages, rows the left stage's values and columns the right one's, or
// the invalid rule where no member rule reaches both, and the stored row
// of every left value.
//
// Rows are numbered by first appearance of their cells, except that the
// rows no member rule reaches share one numbered anew where the first of
// them appears, as classify_join_rules stores it without looking for an
// equal one.
func RuleTableReference(left, right *classify.Stage, members []bool) ([][]uint32, []uint32) {
	leftGroups, rightGroups := left.RuleGroups(), right.RuleGroups()
	leftReaches, rightReaches := reaches(left), reaches(right)
	cells := make([][]uint32, left.MaxValue()+1)
	classMap := make([]uint32, len(cells))
	numbers := map[string]uint32{}
	stored := uint32(0)
	uncovered := ^uint32(0)
	for row := range cells {
		covered := false
		cells[row] = slices.Repeat([]uint32{classify.InvalidRule}, int(right.MaxValue())+1)
		for rule, member := range slices.Backward(members) {
			leftGroup, rightGroup := leftGroups[rule], rightGroups[rule]
			if !member || leftGroup == classify.InvalidGroup || rightGroup == classify.InvalidGroup || !leftReaches[leftGroup][row] {
				continue
			}
			covered = true
			for column := range cells[row] {
				if rightReaches[rightGroup][column] {
					cells[row][column] = uint32(rule)
				}
			}
		}
		if !covered && uncovered != ^uint32(0) {
			classMap[row] = uncovered
			continue
		}
		key := fmt.Sprint(cells[row])
		number, ok := numbers[key]
		if !ok || !covered {
			number = stored
			stored++
			if !ok {
				numbers[key] = number
			}
		}
		if !covered {
			uncovered = number
		}
		classMap[row] = number
	}
	return cells, classMap
}

// RuleTable returns the row of every left value of a committed rule
// table, as rule_table_get reads it: the stored row its class map picks.
func RuleTable(table *layout.RuleTable) [][]uint32 {
	pointers := unsafe.Slice(table.Rows.Get(), table.ClassCount)
	rows := make([][]uint32, table.VDim)
	for row, number := range Values(&table.ClassMap) {
		rows[row] = unsafe.Slice(pointers[number].Get(), table.HDim)
	}
	return rows
}
