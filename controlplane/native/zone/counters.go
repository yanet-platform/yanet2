//go:build linux && (amd64 || arm64)

package zone

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// CounterTag is a key and a value a counter storage is registered under,
// or, as a predicate, a key and the value it must have: an empty value
// matches a storage without the key, "*" one with the key at any value.
type CounterTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// CounterGroup is the counters of the storages registered under one tag
// set, across the workers.
type CounterGroup struct {
	Tags     []CounterTag `json:"tags"`
	Counters []Counter    `json:"counters"`
}

// Counter is a counter's values, indexed by worker, then by value; a
// worker without the counter, or with a counter of another size than the
// first worker that has it, reads zeroes, as the cgo reader reports it.
type Counter struct {
	Name   string     `json:"name"`
	Values [][]uint64 `json:"values"`
}

// CurrentTime returns the latest time, in nanoseconds, a worker of the
// instance published, or false before any did, as
// dataplane_instance_current_time reads it.
func (m *Instance) CurrentTime() (uint64, bool) {
	first := m.Dp.Workers.Get()
	if first == nil {
		return 0, false
	}
	latest := uint64(0)
	pointers := unsafe.Slice(first, m.Dp.WorkerCount)
	for idx := range pointers {
		if worker := pointers[idx].Get(); worker != nil {
			latest = max(latest, atomic.LoadUint64(&worker.CurrentTime))
		}
	}
	return latest, latest != 0
}

// Counters returns the counters of every storage whose tags satisfy the
// predicates, read from each worker's contexts of the pinned generation
// as yanet_get_counters_by_tags_per_worker reads them, and merges a tag
// set's storages on all workers into one group.
//
// Predicates are refused as the cgo reader refuses them: a NUL byte, a
// key or value too long for the C tag, a key named twice, more predicates
// than a storage has tags. An empty key is a key like any other. Counters
// are not filtered by name: the counter-name patterns of the cgo reader's
// query are not supported.
func (m *Generation) Counters(predicates []CounterTag) ([]CounterGroup, error) {
	if len(predicates) > len(layout.CpCounterStorage{}.Tags) {
		return nil, fmt.Errorf("%d counter tags, more than %d", len(predicates), len(layout.CpCounterStorage{}.Tags))
	}
	predicates = slices.Clone(predicates)
	slices.SortFunc(predicates, func(left, right CounterTag) int { return cmp.Compare(left.Key, right.Key) })
	for idx := range predicates {
		tag := predicates[idx]
		switch {
		case strings.IndexByte(tag.Key, 0) >= 0 || strings.IndexByte(tag.Value, 0) >= 0:
			return nil, fmt.Errorf("counter tag %q=%q contains a NUL byte", tag.Key, tag.Value)
		case len(tag.Key) >= len(layout.CounterTag{}.Key):
			return nil, fmt.Errorf("counter tag key %q exceeds %d bytes", tag.Key, len(layout.CounterTag{}.Key)-1)
		case len(tag.Value) >= len(layout.CounterTag{}.Value):
			return nil, fmt.Errorf("counter tag value %q exceeds %d bytes", tag.Value, len(layout.CounterTag{}.Value)-1)
		case idx > 0 && predicates[idx-1].Key == tag.Key:
			return nil, fmt.Errorf("counter tag key %q appears more than once", tag.Key)
		}
	}

	gen := m.Gen
	if gen == nil {
		return nil, nil
	}
	dp := gen.DpConfig.Get()
	if dp == nil {
		return nil, nil
	}
	workerCount := int(dp.WorkerCount)
	var roots []shm.Ptr[layout.ConfigGenEctx]
	if first := gen.ConfigGenEctxs.Get(); first != nil {
		roots = unsafe.Slice(first, gen.ConfigGenEctxCount)
	}

	var groups []CounterGroup
	groupIndex := map[string]int{}
	for worker := range min(workerCount, len(roots)) {
		root := roots[worker].Get()
		if root == nil {
			continue
		}
		registry := root.CounterStorageRegistry.Get()
		if registry == nil || registry.Count == 0 || registry.Items.Get() == nil {
			continue
		}
		items := unsafe.Slice(registry.Items.Get(), registry.Count)
		for itemIdx := range items {
			item := &items[itemIdx]
			storage := item.Storage.Get()
			// A storage that holds no counters, as an object without
			// counters registers, makes no group; nor does one claiming
			// more tags than it has room for.
			if item.TagCount > uint64(len(item.Tags)) || storage == nil ||
				storage.Registry.Get() == nil || storage.Registry.Get().Count == 0 {
				continue
			}
			present := item.Tags[:item.TagCount]
			if !tagsMatch(predicates, present) {
				continue
			}
			// The registry refuses a second storage of a tag set, so a
			// worker holds at most one; were there two, the later one's
			// values would replace the earlier's, as the cgo reader's
			// merge replaces them.
			key := tagSetKey(present)
			idx, found := groupIndex[key]
			if !found {
				idx = len(groups)
				groupIndex[key] = idx
				groups = append(groups, CounterGroup{Tags: tagsOf(present)})
			}
			readStorage(&groups[idx], storage, worker, workerCount)
		}
	}
	for _, group := range groups {
		for _, counter := range group.Counters {
			size := 0
			if first := slices.IndexFunc(counter.Values, func(values []uint64) bool { return values != nil }); first >= 0 {
				size = len(counter.Values[first])
			}
			for worker := range counter.Values {
				if counter.Values[worker] == nil {
					counter.Values[worker] = make([]uint64, size)
				}
			}
		}
	}
	return groups, nil
}

// readStorage sets a worker's values of a storage's counters in the
// group, adding the counters it does not hold yet. A counter whose values
// cannot be reached reads as zeroes for the worker.
func readStorage(group *CounterGroup, storage *layout.CounterStorage, worker, workerCount int) {
	registry := storage.Registry.Get()
	if registry == nil || registry.Count == 0 || registry.Names.Get() == nil {
		return
	}
	names := unsafe.Slice(registry.Names.Get(), registry.Count)
	var handles []shm.Ptr[byte]
	if first := storage.CounterValueHandles.Get(); first != nil {
		handles = unsafe.Slice(first, registry.Count)
	}
	for idx := range names {
		name := shm.String(names[idx].Name[:])
		counter := slices.IndexFunc(group.Counters, func(counter Counter) bool { return counter.Name == name })
		if counter < 0 {
			counter = len(group.Counters)
			group.Counters = append(group.Counters, Counter{Name: name, Values: make([][]uint64, workerCount)})
		}
		size := names[idx].Size
		known := slices.IndexFunc(group.Counters[counter].Values, func(values []uint64) bool { return values != nil })
		if known >= 0 && uint64(len(group.Counters[counter].Values[known])) != size {
			continue
		}
		if size == 0 {
			group.Counters[counter].Values[worker] = []uint64{}
			continue
		}
		if handles == nil || handles[idx].Get() == nil {
			group.Counters[counter].Values[worker] = make([]uint64, size)
			continue
		}
		values := unsafe.Slice((*uint64)(unsafe.Pointer(handles[idx].Get())), size)
		group.Counters[counter].Values[worker] = slices.Clone(values)
	}
}

// tagsMatch reports whether a storage's tags, sorted by key as they are
// registered, satisfy the predicates, sorted by key, as check_match does.
func tagsMatch(predicates []CounterTag, present []layout.CounterTag) bool {
	next := 0
	for _, predicate := range predicates {
		for next < len(present) && shm.String(present[next].Key[:]) < predicate.Key {
			next++
		}
		found := next < len(present) && shm.String(present[next].Key[:]) == predicate.Key
		switch predicate.Value {
		case "":
			if found {
				return false
			}
		case "*":
			if !found {
				return false
			}
		default:
			if !found || shm.String(present[next].Value[:]) != predicate.Value {
				return false
			}
		}
	}
	return true
}

func tagsOf(present []layout.CounterTag) []CounterTag {
	tags := make([]CounterTag, len(present))
	for idx := range present {
		tags[idx] = CounterTag{Key: shm.String(present[idx].Key[:]), Value: shm.String(present[idx].Value[:])}
	}
	return tags
}

func tagSetKey(present []layout.CounterTag) string {
	var key strings.Builder
	for idx := range present {
		key.WriteString(shm.String(present[idx].Key[:]))
		key.WriteByte(0)
		key.WriteString(shm.String(present[idx].Value[:]))
		key.WriteByte(0)
	}
	return key.String()
}
