package ffi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

func zeroCounter(name string, instances, size int) ffi.CounterInfo {
	values := make([][]uint64, 0, instances)
	for range instances {
		values = append(values, make([]uint64, size))
	}
	return ffi.CounterInfo{Name: name, Values: values}
}

func TestCounterInfo(t *testing.T) {
	values := [][]uint64{{1, 2, 4}, {2, 3, 5}}
	counter := ffi.CounterInfo{Name: "counter", Values: values}

	t.Run("Shape", func(t *testing.T) {
		assert.Equal(t, 2, counter.Instances())
		assert.Equal(t, 3, counter.Size())
	})

	t.Run("Values", func(t *testing.T) {
		for instance := range counter.Instances() {
			for idx := range counter.Size() {
				assert.Equal(t, values[instance][idx], counter.Value(instance, idx))
			}
		}
	})

	t.Run("InstanceValues", func(t *testing.T) {
		assert.ElementsMatch(t, values[0], counter.InstanceValues(0))
		assert.ElementsMatch(t, values[1], counter.InstanceValues(1))
	})

	t.Run("ZeroValue", func(t *testing.T) {
		var empty ffi.CounterInfo
		assert.Equal(t, 0, empty.Instances())
		assert.Equal(t, 0, empty.Size())
	})
}

func TestCounterSet(t *testing.T) {
	singleValue := ffi.CounterInfo{
		Name:   "single_value",
		Values: [][]uint64{{7}},
	}
	twoValues := ffi.CounterInfo{
		Name:   "two_values",
		Values: [][]uint64{{1, 2}},
	}
	duplicateName := ffi.CounterInfo{
		Name:   singleValue.Name,
		Values: [][]uint64{{2}},
	}
	twoInstances := ffi.CounterInfo{
		Name:   "two_instances",
		Values: [][]uint64{{1}, {2}},
	}

	t.Run("EmptySet", func(t *testing.T) {
		testCases := []struct {
			name     string
			counters []ffi.CounterInfo
		}{
			{name: "nil slice", counters: nil},
			{name: "empty slice", counters: []ffi.CounterInfo{}},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				set, err := ffi.NewCounterSet(testCase.counters)
				require.NoError(t, err)
				assert.Equal(t, 0, set.Instances())
				assert.NoError(t, set.Err())
			})
		}
	})

	t.Run("DuplicateName", func(t *testing.T) {
		_, err := ffi.NewCounterSet(
			[]ffi.CounterInfo{singleValue, twoValues, duplicateName},
		)
		require.Error(t, err, "a repeated counter name must be rejected")
		assert.Contains(t, err.Error(), duplicateName.Name)
	})

	t.Run("InstanceMismatch", func(t *testing.T) {
		_, err := ffi.NewCounterSet(
			[]ffi.CounterInfo{singleValue, twoValues, twoInstances},
		)
		require.Error(t, err, "counters disagreeing on instance count must be rejected")
	})

	t.Run("ResolvesPresentCounters", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue, twoValues})
		require.NoError(t, err)
		require.NoError(t, set.Err(), "a fresh set carries no misses")
		assert.Equal(t, 1, set.Instances())

		assert.Equal(t, singleValue, set.Lookup(singleValue.Name, 0).Require())
		assert.Equal(t, twoValues, set.Lookup(twoValues.Name, twoValues.Size()).Require())
		require.NoError(t, set.Err())
	})

	t.Run("MissingName", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue, twoValues})
		require.NoError(t, err)

		assert.Equal(t, zeroCounter("absent", 1, 0), set.Lookup("absent", 0).Require())

		err = set.Err()
		require.Error(t, err, "an absent counter must be recorded as a miss")
		assert.Contains(t, err.Error(), "absent")
	})

	t.Run("SizeMismatch", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue, twoValues})
		require.NoError(t, err)

		assert.Equal(
			t,
			zeroCounter(singleValue.Name, 1, 2),
			set.Lookup(singleValue.Name, 2).Require(),
		)

		err = set.Err()
		require.Error(t, err, "a counter of unexpected size must be recorded as a miss")
		assert.Contains(t, err.Error(), singleValue.Name)
	})

	t.Run("MissedCounterIsSafeToRead", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue})
		require.NoError(t, err)

		counter := set.Lookup("absent", 1).Require()
		require.Error(t, set.Err())

		assert.Zero(t, counter.Value(0, 0))
		assert.Equal(t, []uint64{0}, counter.InstanceValues(0))
	})

	t.Run("JoinsEveryMiss", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue, twoValues})
		require.NoError(t, err)

		set.Lookup(singleValue.Name, 2).Require()
		set.Lookup("absent", 0).Require()

		err = set.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), singleValue.Name)
		assert.Contains(t, err.Error(), "absent")
		assert.Contains(t, err.Error(), "; ")
	})

	t.Run("ErrSurvivesALaterHit", func(t *testing.T) {
		set, err := ffi.NewCounterSet([]ffi.CounterInfo{singleValue})
		require.NoError(t, err)

		set.Lookup("absent", 0).Require()
		require.Error(t, set.Err())

		set.Lookup(singleValue.Name, 1).Require()
		assert.Error(t, set.Err(), "a later hit must not clear a recorded miss")
	})
}
