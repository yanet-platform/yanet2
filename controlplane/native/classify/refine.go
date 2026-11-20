//go:build linux && (amd64 || arm64)

package classify

import (
	"cmp"
	"slices"
)

// refiner splits a domain into regions, numbering them as remap_table
// does.
//
// Every region starts with value 0. In each generation, every value a
// touched region holds moves to a fresh value of its own, shared by the
// regions of that value touched in the generation, so a region's final
// value names the set of generations that touched it. Compaction then
// numbers the values still held, in the order they were created.
//
// remap_table never reuses a value, so it grows with every generation;
// here a value no region holds any more gives its slot to the next fresh
// value, and every value keeps its creation stamp, which is all its
// compacted number depends on. A refiner never holds more slots than its
// domain has regions, plus one.
type refiner struct {
	gen     uint32
	created uint64
	// Per slot: the generation it last moved in, the slot it moved to
	// then, how many regions hold it, and when its value was created.
	gens   []uint32
	next   []uint32
	counts []uint32
	stamps []uint64
	free   []uint32
}

// newRefiner returns a refiner whose regions all hold value 0.
func newRefiner(regions uint32) *refiner {
	capacity := min(int(regions)+1, 1<<16)
	m := &refiner{
		gen:    1,
		gens:   make([]uint32, 1, capacity),
		next:   make([]uint32, 1, capacity),
		counts: make([]uint32, 1, capacity),
		stamps: make([]uint64, 1, capacity),
	}
	m.counts[0] = regions
	m.created = 1
	return m
}

// NewGeneration starts a generation.
func (m *refiner) NewGeneration() {
	m.gen++
}

// Touch moves one region holding slot in the current generation and
// returns the slot it holds now.
func (m *refiner) Touch(slot uint32) uint32 {
	if m.gens[slot] != m.gen {
		fresh := m.newSlot()
		m.gens[slot] = m.gen
		m.next[slot] = fresh
	}
	moved := m.next[slot]
	m.next[moved] = moved
	m.gens[moved] = m.gen
	m.counts[moved]++
	m.counts[slot]--
	if m.counts[slot] == 0 {
		m.free = append(m.free, slot)
	}
	return moved
}

// newSlot returns a slot for a fresh value, reusing one no region holds.
func (m *refiner) newSlot() uint32 {
	var slot uint32
	if count := len(m.free); count > 0 {
		slot = m.free[count-1]
		m.free = m.free[:count-1]
	} else {
		slot = uint32(len(m.counts))
		m.gens = append(m.gens, 0)
		m.next = append(m.next, 0)
		m.counts = append(m.counts, 0)
		m.stamps = append(m.stamps, 0)
	}
	m.gens[slot] = 0
	m.counts[slot] = 0
	m.stamps[slot] = m.created
	m.created++
	return slot
}

// Numbers returns the compacted number of every slot a region holds,
// counting in creation order, and the count of numbers. Touching is not
// allowed afterwards.
func (m *refiner) Numbers() ([]uint32, uint32) {
	held := make([]uint32, 0, len(m.counts))
	for slot, count := range m.counts {
		if count > 0 {
			held = append(held, uint32(slot))
		}
	}
	slices.SortFunc(held, func(left, right uint32) int {
		return cmp.Compare(m.stamps[left], m.stamps[right])
	})
	numbers := make([]uint32, len(m.counts))
	for number, slot := range held {
		numbers[slot] = uint32(number)
	}
	return numbers, uint32(len(held))
}
