//go:build linux && (amd64 || arm64)

package classifytest

import (
	"fmt"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
)

// Joint is a join as the reference builds it: the class of every cell,
// the pair of every rule, the classes of every pair, ascending, and the
// largest class.
type Joint struct {
	Table      [][]uint32
	RuleGroups []uint32
	Groups     [][]uint32
	MaxValue   uint32
}

// JoinReference joins two stages cell by cell.
//
// A rule with a group on both sides belongs to the pair of its groups,
// pairs numbered by first appearance over the rules. A cell's class is
// the set of pairs covering it, numbered by first appearance in
// row-major order, and the joint lists every pair's classes ascending.
func JoinReference(left, right *classify.Stage) Joint {
	type pair struct {
		Left  uint32
		Right uint32
	}
	leftGroups, rightGroups := left.RuleGroups(), right.RuleGroups()
	numbers := map[pair]uint32{}
	var pairs []pair
	joint := Joint{RuleGroups: make([]uint32, len(leftGroups))}
	for rule := range joint.RuleGroups {
		groups := pair{Left: leftGroups[rule], Right: rightGroups[rule]}
		if groups.Left == classify.InvalidGroup || groups.Right == classify.InvalidGroup {
			joint.RuleGroups[rule] = classify.InvalidGroup
			continue
		}
		number, ok := numbers[groups]
		if !ok {
			number = uint32(len(pairs))
			numbers[groups] = number
			pairs = append(pairs, groups)
		}
		joint.RuleGroups[rule] = number
	}

	leftReaches, rightReaches := reaches(left), reaches(right)
	classOf := map[string]uint32{}
	// covering holds the pairs covering every class.
	var covering [][]uint32
	joint.Table = make([][]uint32, left.MaxValue()+1)
	for row := range joint.Table {
		joint.Table[row] = make([]uint32, right.MaxValue()+1)
		for column := range joint.Table[row] {
			var set []uint32
			for number, groups := range pairs {
				if leftReaches[groups.Left][row] && rightReaches[groups.Right][column] {
					set = append(set, uint32(number))
				}
			}
			key := fmt.Sprint(set)
			class, ok := classOf[key]
			if !ok {
				class = uint32(len(covering))
				classOf[key] = class
				covering = append(covering, set)
			}
			joint.Table[row][column] = class
		}
	}

	joint.MaxValue = uint32(len(covering) - 1)
	joint.Groups = make([][]uint32, len(pairs))
	for number := range pairs {
		joint.Groups[number] = []uint32{}
		for class, set := range covering {
			if slices.Contains(set, uint32(number)) {
				joint.Groups[number] = append(joint.Groups[number], uint32(class))
			}
		}
	}
	return joint
}

// Groups returns the values of every group of a stage.
func Groups(stage *classify.Stage) [][]uint32 {
	groups := make([][]uint32, stage.GroupCount())
	for group := range groups {
		groups[group] = stage.Group(uint32(group))
	}
	return groups
}

// reaches returns, per group of a stage, which values it reaches.
func reaches(stage *classify.Stage) [][]bool {
	reached := make([][]bool, stage.GroupCount())
	for group := range reached {
		reached[group] = make([]bool, stage.MaxValue()+1)
		for _, value := range stage.Group(uint32(group)) {
			reached[group][value] = true
		}
	}
	return reached
}
