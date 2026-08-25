package ffi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type (
	CounterTag   = ffi.CounterTag
	CounterInfo  = ffi.CounterInfo
	CounterGroup = ffi.CounterGroup
)

func workerGroup(tags []CounterTag, counters ...CounterInfo) CounterGroup {
	return CounterGroup{Tags: tags, Counters: counters}
}

// perWorkerCounter wraps one worker's snapshot: a single instance.
func perWorkerCounter(name string, values ...uint64) CounterInfo {
	return CounterInfo{Name: name, Values: [][]uint64{values}}
}

func TestMergeWorkerCounterGroups(t *testing.T) {
	deviceTags := []CounterTag{{Key: "device", Value: "dev0"}, {Key: "kind", Value: "device"}}
	runtimeTags := []CounterTag{{Key: "kind", Value: "runtime"}, {Key: "config", Value: "rules"}}

	t.Run("SingleWorkerKeepsValues", func(t *testing.T) {
		merged := ffi.MergeWorkerCounterGroups(1, [][]CounterGroup{{
			workerGroup(deviceTags,
				perWorkerCounter("input", 7, 9),
			),
		}})

		assert.Len(t, merged, 1)
		assert.Equal(t, deviceTags, merged[0].Tags)
		assert.Equal(t, []CounterInfo{
			{Name: "input", Values: [][]uint64{{7, 9}}},
		}, merged[0].Counters)
	})

	t.Run("AbsentWorkerIsZeroFilled", func(t *testing.T) {
		merged := ffi.MergeWorkerCounterGroups(2, [][]CounterGroup{
			{workerGroup(deviceTags, perWorkerCounter("input", 11))},
			nil,
		})

		assert.Equal(t, []CounterInfo{
			{Name: "input", Values: [][]uint64{{11}, {0}}},
		}, merged[0].Counters)
	})

	t.Run("UnionKeepsEveryWorkersValues", func(t *testing.T) {
		merged := ffi.MergeWorkerCounterGroups(3, [][]CounterGroup{
			{workerGroup(runtimeTags, perWorkerCounter("rule0", 1))},
			{workerGroup(runtimeTags, perWorkerCounter("rule0", 2), perWorkerCounter("rule1", 3))},
			{workerGroup(runtimeTags, perWorkerCounter("rule1", 4))},
		})

		assert.Len(t, merged, 1)
		assert.Equal(t, []CounterInfo{
			{Name: "rule0", Values: [][]uint64{{1}, {2}, {0}}},
			{Name: "rule1", Values: [][]uint64{{0}, {3}, {4}}},
		}, merged[0].Counters)
	})

	t.Run("TagOrderDoesNotSplitGroups", func(t *testing.T) {
		reordered := []CounterTag{{Key: "kind", Value: "device"}, {Key: "device", Value: "dev0"}}

		merged := ffi.MergeWorkerCounterGroups(2, [][]CounterGroup{
			{workerGroup(deviceTags, perWorkerCounter("input", 5))},
			{workerGroup(reordered, perWorkerCounter("input", 6))},
		})

		assert.Len(t, merged, 1, "the same tag set in another order is the same group")
		assert.Equal(t, []CounterInfo{
			{Name: "input", Values: [][]uint64{{5}, {6}}},
		}, merged[0].Counters)
	})

	t.Run("DistinctTagSetsStayDistinct", func(t *testing.T) {
		other := []CounterTag{{Key: "device", Value: "dev1"}, {Key: "kind", Value: "device"}}

		merged := ffi.MergeWorkerCounterGroups(2, [][]CounterGroup{
			{workerGroup(deviceTags, perWorkerCounter("input", 1))},
			{workerGroup(other, perWorkerCounter("input", 2))},
		})

		assert.Len(t, merged, 2)
		assert.Equal(t, [][]uint64{{1}, {0}}, merged[0].Counters[0].Values)
		assert.Equal(t, [][]uint64{{0}, {2}}, merged[1].Counters[0].Values)
	})

	t.Run("SizeDisagreementKeepsFirstSize", func(t *testing.T) {
		merged := ffi.MergeWorkerCounterGroups(2, [][]CounterGroup{
			{workerGroup(deviceTags, perWorkerCounter("hist", 1, 2))},
			{workerGroup(deviceTags, perWorkerCounter("hist", 3, 4, 5))},
		})

		assert.Equal(t, []CounterInfo{
			{Name: "hist", Values: [][]uint64{{1, 2}, {0, 0}}},
		}, merged[0].Counters)
	})

	t.Run("ZeroWorkersYieldNoGroups", func(t *testing.T) {
		assert.Empty(t, ffi.MergeWorkerCounterGroups(0, nil))
	})
}
