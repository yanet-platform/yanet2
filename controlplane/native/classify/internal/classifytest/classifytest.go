//go:build linux && (amd64 || arm64)

// Package classifytest reads committed classifier artefacts as the
// dataplane reads them and holds the naive reference they are held to.
//
// The reference reads which rules match a key off the rules themselves.
// A compile is right when, for every key that starts or ends one of the
// elementary intervals its rules cut the domain into, its stage reaches
// the class the committed artefact gives the key from exactly the rules
// the reference matches; the first match of any subset of rules follows.
package classifytest

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Memory is an allocator, its root memory context and the attributes a
// compile commits, laid out at the start of a block of memory whose rest
// is the allocator's arena, so every relative pointer stays inside it.
type Memory struct {
	Allocator layout.BlockAllocator
	Context   layout.MemoryContext
	Line      layout.ClassifyAttrLine
	Net4      layout.ClassifyAttrNet4
	// OtherLine and OtherNet4 hold a second attribute of each kind, for
	// several compiles committed under the one root context.
	OtherLine layout.ClassifyAttrLine
	OtherNet4 layout.ClassifyAttrNet4
}

// LayOut lays out a memory at the start of the block, its root context
// named root, and returns it.
func LayOut(block []byte) *Memory {
	memory := (*Memory)(unsafe.Pointer(&block[0]))
	*memory = Memory{}
	arena := unsafe.Sizeof(*memory)
	shmalloc.Init(&memory.Allocator)
	shmalloc.PutArena(&memory.Allocator, unsafe.Pointer(&block[arena]), uintptr(len(block))-arena)
	shmalloc.ContextInit(&memory.Context, "root", &memory.Allocator)
	return memory
}

// Alloc carves a zeroed T out of the memory's arena, so the relative
// pointers a commit writes into it stay inside the block.
func Alloc[T any](memory *Memory) *T {
	size := unsafe.Sizeof(*new(T))
	block := shmalloc.ContextAlloc(&memory.Context, size)
	clear(unsafe.Slice((*byte)(block), size))
	return (*T)(block)
}

// LineMatches reports whether a rule matches a point of a line domain.
func LineMatches(rule classify.LineRule, point uint32) bool {
	if rule.Absent {
		return false
	}
	return len(rule.Ranges) == 0 || slices.ContainsFunc(rule.Ranges, func(span classify.LineRange) bool {
		return span.From <= point && point <= span.To
	})
}

// Net4Matches reports whether a rule matches an IPv4 address.
func Net4Matches(rule classify.Net4Rule, addr uint32) bool {
	if rule.Absent {
		return false
	}
	var key [4]byte
	binary.BigEndian.PutUint32(key[:], addr)
	return len(rule.Prefixes) == 0 || slices.ContainsFunc(rule.Prefixes, func(prefix netip.Prefix) bool {
		return prefix.Contains(netip.AddrFrom4(key))
	})
}

// DeviceMatches reports whether a rule matches a device.
func DeviceMatches(rule classify.DeviceRule, id uint32) bool {
	if rule.Absent {
		return false
	}
	return len(rule.IDs) == 0 || slices.Contains(rule.IDs, id)
}

// PortMatches reports whether a rule matches a port.
func PortMatches(rule classify.PortRule, port uint16) bool {
	if rule.Absent {
		return false
	}
	return len(rule.Ranges) == 0 || slices.ContainsFunc(rule.Ranges, func(span classify.PortRange) bool {
		return span.From <= port && port <= span.To
	})
}

// FragmentMatches reports whether a rule matches a packet, a fragment or
// a whole one.
func FragmentMatches(rule classify.FragmentRule, fragment bool) bool {
	if rule.Absent {
		return false
	}
	switch rule.Fragment {
	case classify.FragmentNone:
		return !fragment
	case classify.FragmentFrag:
		return fragment
	default:
		return true
	}
}

// Net4Keys returns both ends of every elementary interval the prefixes of
// the rules cut the IPv4 address space into, ascending.
func Net4Keys(rules []classify.Net4Rule) []uint32 {
	keys := []uint32{0, ^uint32(0)}
	for _, rule := range rules {
		for _, prefix := range rule.Prefixes {
			bytes := prefix.Masked().Addr().As4()
			from := binary.BigEndian.Uint32(bytes[:])
			to := from | uint32(uint64(1)<<(32-prefix.Bits())-1)
			keys = append(keys, from, from-1, to, to+1)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// LineKeys returns both ends of every elementary interval the ranges of
// the rules cut a line domain of size points into, ascending.
func LineKeys(rules []classify.LineRule, size uint32) []uint32 {
	keys := []uint32{0, size - 1}
	for _, rule := range rules {
		for _, span := range rule.Ranges {
			for _, key := range []uint32{span.From, span.From - 1, span.To, span.To + 1} {
				if key < size {
					keys = append(keys, key)
				}
			}
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// PortKeys returns both ends of every elementary interval the ranges of
// the rules cut the port domain into, ascending.
func PortKeys(rules []classify.PortRule) []uint16 {
	keys := []uint16{0, classify.PortDomain - 1}
	for _, rule := range rules {
		for _, span := range rule.Ranges {
			keys = append(keys, span.From, span.From-1, span.To, span.To+1)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// Values returns the values of a committed value line, as the dataplane
// reads them.
func Values(line *layout.Vline) []uint32 {
	return unsafe.Slice(line.Values.Get(), line.Size)
}

// Table returns the rows of a committed value table, as the dataplane
// reads them.
func Table(table *layout.ValueTable) [][]uint32 {
	chunks := unsafe.Slice(table.Values.Get(), table.VDim)
	rows := make([][]uint32, table.VDim)
	for row := range rows {
		rows[row] = unsafe.Slice(chunks[row].Get(), table.HDim)
	}
	return rows
}

// Stretches is the class of every stretch of keys a committed prefix
// tree maps to one class, read through a walk of the tree.
type Stretches[K uint32 | uint64] struct {
	starts  []K
	classes []uint32
}

// readTree walks a prefix tree whose keys are the big-endian bytes of a
// K.
func readTree[K uint32 | uint64](tree *layout.Lpm) Stretches[K] {
	var starts []K
	var classes []uint32
	lpm.Walk(tree, int(unsafe.Sizeof(K(0))), func(from, _ []byte, class uint32) {
		start := K(0)
		for _, octet := range from {
			start = start<<8 | K(octet)
		}
		starts = append(starts, start)
		classes = append(classes, class)
	})
	return Stretches[K]{starts: starts, classes: classes}
}

// ReadNet4 walks the prefix tree of a committed IPv4 network attribute.
func ReadNet4(attr *layout.ClassifyAttrNet4) Stretches[uint32] {
	return readTree[uint32](&attr.Lpm)
}

// Class returns the class of a key.
func (m Stretches[K]) Class(key K) uint32 {
	stretch, found := slices.BinarySearch(m.starts, key)
	if !found {
		stretch--
	}
	return m.classes[stretch]
}

// CheckStage returns an error naming the first key and rule for which
// the stage, reaching the class the artefact gives the key, disagrees
// with the reference on whether the rule matches.
func CheckStage[K any](stage *classify.Stage, keys []K, classOf func(key K) uint32, matches func(rule int, key K) bool) error {
	groupReaches := reaches(stage)
	for _, key := range keys {
		class := classOf(key)
		for rule, group := range stage.RuleGroups() {
			reached := group != classify.InvalidGroup && class <= stage.MaxValue() && groupReaches[group][class]
			if want := matches(rule, key); reached != want {
				return fmt.Errorf("key %#v of class %d: rule %d matches %t through the stage, want %t", key, class, rule, reached, want)
			}
		}
	}
	return nil
}
