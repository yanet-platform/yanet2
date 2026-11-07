package wlc_test

// The traces below model a proposed weight-sampling protocol. No sampler exists
// yet and #2764 stays open, so passing traces are not evidence for the
// production path.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// protocolRecord represents safe speculative data in sequential protocol traces.
//
// Integer keys stand in for hashes; fixture sizes are not production choices.
// Every speculative access here is safe by construction. Passing traces say
// nothing about C ordering, memory lifetime, identity suppression or throughput.
type protocolRecord struct {
	Key      int
	Value    int
	Deadline int
}

// protocolModel holds bounded roots and fixed logical groups for explicit traces.
type protocolModel struct {
	Roots    [][]protocolRecord
	Versions []uint64
	Guards   []bool
}

// newProtocolModel distributes fixture hashes across power-of-two root counts.
func newProtocolModel(groups, roots int, records ...protocolRecord) *protocolModel {
	m := &protocolModel{
		Roots:    make([][]protocolRecord, roots),
		Versions: make([]uint64, groups),
		Guards:   make([]bool, groups),
	}
	for _, record := range records {
		root := record.Key % roots
		m.Roots[root] = append(m.Roots[root], record)
	}
	return m
}

// GroupRoots covers all roots a fixed group can occupy in the fixture geometry.
//
// A group visits roots/gcd(groups, roots) distinct roots, so enumerating any
// shorter run of hashes misses reachable roots.
func (m *protocolModel) GroupRoots(group int) []int {
	groups, roots := len(m.Versions), len(m.Roots)
	cycle := roots / gcd(groups, roots)
	result := make([]int, 0, cycle)
	for step := range cycle {
		result = append(result, (group+step*groups)%roots)
	}
	return result
}

// gcd returns the greatest common divisor of two positive sizes.
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// RootGroups includes every enumerator, including groups unrelated to changed keys.
func (m *protocolModel) RootGroups(root int) []int {
	var groups []int
	for group := range m.Versions {
		for _, candidate := range m.GroupRoots(group) {
			if candidate == root {
				groups = append(groups, group)
			}
		}
	}
	return groups
}

// Begin excludes overlapping writers before making every affected group busy.
//
// End-only publication deliberately weakens the protocol for negative controls.
func (m *protocolModel) Begin(groups []int, endOnly bool) bool {
	for _, group := range groups {
		if m.Guards[group] {
			return false
		}
	}
	for _, group := range groups {
		m.Guards[group] = true
		if !endOnly {
			m.Versions[group]++
		}
	}
	return true
}

// Finish publishes fresh stable versions only after the scheduled mutation ends.
func (m *protocolModel) Finish(groups []int, endOnly bool) {
	for _, group := range groups {
		if endOnly {
			m.Versions[group] += 2
		} else {
			m.Versions[group]++
		}
		m.Guards[group] = false
	}
}

// protocolRead keeps one group's speculative records and source-time observation.
type protocolRead struct {
	Group   int
	Time    int
	Version uint64
	Records []protocolRecord
}

// Start brackets a complete group; source time is independent of caller budget.
func (m *protocolModel) Start(group, sourceTime int) protocolRead {
	return protocolRead{Group: group, Time: sourceTime, Version: m.Versions[group]}
}

// Observe filters shared-root records using the group's single observation time.
func (m *protocolRead) Observe(record protocolRecord, groups int) {
	if record.Key%groups == m.Group && record.Deadline > m.Time {
		m.Records = append(m.Records, record)
	}
}

// Collect enumerates every root belonging to this group without copying others.
func (m *protocolModel) Collect(read *protocolRead) {
	for _, root := range m.GroupRoots(read.Group) {
		for _, record := range m.Roots[root] {
			read.Observe(record, len(m.Versions))
		}
	}
}

// Accept requires an unchanged even version around the whole scheduled read.
func (m *protocolModel) Accept(read protocolRead) bool {
	return read.Version%2 == 0 && read.Version == m.Versions[read.Group]
}

// completeProtocolAttempt exposes nothing until all groups and terminal checks pass.
//
// This is local attempt bookkeeping, not a publisher or fallback implementation.
func completeProtocolAttempt(groups int, private map[int]protocolRead, budget, sourceValid bool) ([]protocolRead, bool) {
	if !budget || !sourceValid || len(private) != groups {
		return nil, false
	}
	result := make([]protocolRead, groups)
	for group := range groups {
		read, ok := private[group]
		if !ok {
			return nil, false
		}
		result[group] = read
	}
	return result, true
}

// Test_ProtocolModel_Geometry verifies that root coverage and exact filtering
// survive growth without changing logical group membership.
func Test_ProtocolModel_Geometry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		groups int
		roots  [][]int
		keys   [][]int
	}{
		{"fewer groups than roots", 2, [][]int{{0, 2}, {1, 3}}, [][]int{{0, 2, 4, 6}, {1, 3, 5, 7}}},
		{"equal groups and roots", 4, [][]int{{0}, {1}, {2}, {3}}, [][]int{{0, 4}, {1, 5}, {2, 6}, {3, 7}}},
		{"more groups than roots", 8, [][]int{{0}, {1}, {2}, {3}, {0}, {1}, {2}, {3}}, [][]int{{0}, {1}, {2}, {3}, {4}, {5}, {6}, {7}}},
		{"groups do not divide roots", 3, [][]int{{0, 3, 2, 1}, {1, 0, 3, 2}, {2, 1, 0, 3}}, [][]int{{0, 3, 6}, {1, 4, 7}, {2, 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records []protocolRecord
			for key := range 8 {
				records = append(records, protocolRecord{key, key + 10, 100})
			}
			model := newProtocolModel(tc.groups, 4, records...)
			for group, roots := range tc.roots {
				require.Equal(t, roots, model.GroupRoots(group))
			}
			for _, rootCount := range []int{4, 8} {
				model = newProtocolModel(tc.groups, rootCount, records...)
				for group, keys := range tc.keys {
					read := model.Start(group, 10)
					model.Collect(&read)
					require.True(t, model.Accept(read))
					var expected []protocolRecord
					for _, key := range keys {
						expected = append(expected, protocolRecord{key, key + 10, 100})
					}
					require.ElementsMatch(t, expected, read.Records)
					require.Len(t, model.GroupRoots(group), rootCount/gcd(tc.groups, rootCount))
				}
			}
		})
	}
}

// Test_ProtocolModel_PausedWriter verifies that a partly changed row is read
// but never accepted until all affected groups return to fresh stable versions.
func Test_ProtocolModel_PausedWriter(t *testing.T) {
	model := newProtocolModel(4, 2, protocolRecord{0, 10, 100})
	groups := model.RootGroups(0)
	require.Equal(t, []int{0, 2}, groups)
	before := model.Start(0, 50)
	require.True(t, model.Begin(groups, false))
	require.Equal(t, []uint64{1, 0, 1, 0}, model.Versions)
	model.Roots[0][0].Value = 20
	for _, group := range groups {
		read := model.Start(group, 50)
		model.Collect(&read)
		require.False(t, model.Accept(read))
	}
	model.Collect(&before)
	require.Equal(t, []protocolRecord{{0, 20, 100}}, before.Records)
	require.False(t, model.Accept(before))
	model.Roots[0][0].Deadline = 200
	model.Finish(groups, false)
	require.Equal(t, []uint64{2, 0, 2, 0}, model.Versions)
	require.False(t, model.Accept(before))
	read := model.Start(0, 50)
	model.Collect(&read)
	require.True(t, model.Accept(read))
	require.Equal(t, []protocolRecord{{0, 20, 200}}, read.Records)
}

// Test_ProtocolModel_SharedRootReuse verifies that protecting only old and
// new key groups can accept an incomplete enumeration of a third party's data.
func Test_ProtocolModel_SharedRootReuse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		narrow bool
	}{
		{"all root enumerators protected", false},
		{"negative control only changed key group protected", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newProtocolModel(4, 2, protocolRecord{0, 10, 100}, protocolRecord{2, 30, 100})
			read := model.Start(2, 50)
			read.Observe(model.Roots[0][0], 4)
			groups := model.RootGroups(0)
			require.Equal(t, []int{0, 2}, groups)
			if tc.narrow {
				groups = []int{0}
			}
			require.True(t, model.Begin(groups, false))
			// Abstract membership replacement moves the unchanged record behind
			// the reader's cursor; both changed keys belong to another group.
			model.Roots[0] = []protocolRecord{{2, 30, 100}, {4, 20, 100}}
			model.Finish(groups, false)
			read.Observe(model.Roots[0][1], 4)
			require.Empty(t, read.Records)
			require.Equal(t, tc.narrow, model.Accept(read))
			fresh := model.Start(2, 50)
			model.Collect(&fresh)
			require.True(t, model.Accept(fresh))
			require.Equal(t, []protocolRecord{{2, 30, 100}}, fresh.Records)
		})
	}
}

// Test_ProtocolModel_WriterGuards verifies that separate roots do not allow
// overlapping same-group writers, while a disjoint group remains independent.
func Test_ProtocolModel_WriterGuards(t *testing.T) {
	model := newProtocolModel(2, 4, protocolRecord{0, 10, 100}, protocolRecord{2, 20, 100}, protocolRecord{1, 30, 100})
	require.Equal(t, []int{0, 2}, model.GroupRoots(0))
	first := model.RootGroups(0)
	second := model.RootGroups(2)
	disjoint := model.RootGroups(1)
	require.Equal(t, []int{1}, disjoint)
	require.True(t, model.Begin(first, false))
	model.Roots[0][0].Value = 11
	require.False(t, model.Begin(second, false))
	require.False(t, model.Begin([]int{1, 0}, false))
	require.Equal(t, []uint64{1, 0}, model.Versions)
	require.Equal(t, []bool{true, false}, model.Guards)
	require.True(t, model.Begin(disjoint, false))
	model.Roots[1][0].Value = 31
	model.Finish(disjoint, false)
	read := model.Start(1, 10)
	model.Collect(&read)
	require.True(t, model.Accept(read))
	require.Equal(t, []protocolRecord{{1, 31, 100}}, read.Records)
	model.Finish(first, false)
	require.True(t, model.Begin(second, false))
	model.Roots[2][0].Value = 21
	model.Finish(second, false)
	read = model.Start(0, 10)
	model.Collect(&read)
	require.True(t, model.Accept(read))
	require.Equal(t, []protocolRecord{{0, 11, 100}, {2, 21, 100}}, read.Records)
	require.Equal(t, []uint64{4, 2}, model.Versions)
}

// Test_ProtocolModel_Refresh verifies that same-identity deadline changes
// invalidate crossed reads and equality with observation time means expired.
func Test_ProtocolModel_Refresh(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deadline int
		expected []protocolRecord
	}{
		{"extension retains identity", 200, []protocolRecord{{0, 10, 200}}},
		{"shortening expires record", 40, nil},
		{"deadline equality expires record", 50, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newProtocolModel(2, 1, protocolRecord{0, 10, 100}, protocolRecord{1, 20, 100})
			read := model.Start(0, 50)
			model.Collect(&read)
			require.Equal(t, []protocolRecord{{0, 10, 100}}, read.Records)
			require.True(t, model.Begin([]int{0}, false))
			model.Roots[0][0].Deadline = tc.deadline
			require.Equal(t, []uint64{1, 0}, model.Versions)
			require.False(t, model.Accept(read))
			model.Finish([]int{0}, false)
			require.False(t, model.Accept(read))
			require.Equal(t, []uint64{2, 0}, model.Versions)
			fresh := model.Start(0, 50)
			model.Collect(&fresh)
			require.True(t, model.Accept(fresh))
			require.Equal(t, tc.expected, fresh.Records)
		})
	}
}

// Test_ProtocolModel_EndOnlyCounterexample verifies that delayed publication
// can accept a vector absent from every state in a handwritten writer history.
func Test_ProtocolModel_EndOnlyCounterexample(t *testing.T) {
	for _, tc := range []struct {
		name    string
		endOnly bool
	}{
		{"busy before mutation rejects impossible vector", false},
		{"negative control end-only accepts impossible vector", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newProtocolModel(1, 2, protocolRecord{0, 0, 100}, protocolRecord{1, 0, 100})
			read := model.Start(0, 50)
			read.Observe(model.Roots[0][0], 1)
			require.True(t, model.Begin([]int{0}, tc.endOnly))
			model.Roots[0][0].Value = 1
			model.Roots[1][0].Value = 1
			read.Observe(model.Roots[1][0], 1)
			require.Equal(t, []protocolRecord{{0, 0, 100}, {1, 1, 100}}, read.Records)
			history := [][]protocolRecord{
				{{0, 0, 100}, {1, 0, 100}},
				{{0, 1, 100}, {1, 0, 100}},
				{{0, 1, 100}, {1, 1, 100}},
			}
			require.NotContains(t, history, read.Records)
			require.Equal(t, tc.endOnly, model.Accept(read))
			model.Finish([]int{0}, tc.endOnly)
			require.False(t, model.Accept(read))
			fresh := model.Start(0, 50)
			model.Collect(&fresh)
			require.True(t, model.Accept(fresh))
			require.Equal(t, history[2], fresh.Records)
		})
	}
}

// Test_ProtocolModel_AttemptCompletion verifies that retries retain private
// groups with distinct observation times but terminal loss exposes no sample.
func Test_ProtocolModel_AttemptCompletion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		budget      bool
		sourceValid bool
		complete    bool
	}{
		{"local retry completes both groups", true, true, true},
		{"caller budget exhausted", false, true, false},
		{"source validity lost", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newProtocolModel(2, 2, protocolRecord{0, 10, 15}, protocolRecord{1, 20, 15})
			private := map[int]protocolRead{}
			first := model.Start(0, 10)
			model.Collect(&first)
			require.True(t, model.Accept(first))
			private[0] = first
			require.True(t, model.Begin([]int{1}, false))
			conflicted := model.Start(1, 12)
			model.Collect(&conflicted)
			require.False(t, model.Accept(conflicted))
			require.Len(t, private, 1)
			// The set is incomplete, so every row returns false here; the
			// complete-set call below is what exercises the budget and source guard.
			result, complete := completeProtocolAttempt(2, private, tc.budget, tc.sourceValid)
			require.False(t, complete)
			require.Nil(t, result)
			model.Finish([]int{1}, false)
			retry := model.Start(1, 20)
			model.Collect(&retry)
			require.True(t, model.Accept(retry))
			private[1] = retry
			require.Equal(t, protocolRead{0, 10, 0, []protocolRecord{{0, 10, 15}}}, private[0])
			require.Equal(t, protocolRead{1, 20, 2, nil}, private[1])
			result, complete = completeProtocolAttempt(2, private, tc.budget, tc.sourceValid)
			require.Equal(t, tc.complete, complete)
			if tc.complete {
				require.Equal(t, []protocolRead{
					{0, 10, 0, []protocolRecord{{0, 10, 15}}},
					{1, 20, 2, nil},
				}, result)
			} else {
				require.Nil(t, result)
			}
		})
	}
}
