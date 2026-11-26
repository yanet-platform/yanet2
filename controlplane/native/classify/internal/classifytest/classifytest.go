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

// LineClasses returns the class of every point of a committed line
// attribute, as the dataplane reads them off its value line.
func LineClasses(attr *layout.ClassifyAttrLine) []uint32 {
	return unsafe.Slice(attr.Line.Values.Get(), attr.Line.Size)
}

// Net4Classes is the class of every stretch of addresses a committed
// IPv4 network attribute maps to one class, read through a walk of its
// prefix tree.
type Net4Classes struct {
	starts  []uint32
	classes []uint32
}

// ReadNet4 walks the prefix tree of a committed IPv4 network attribute.
func ReadNet4(attr *layout.ClassifyAttrNet4) Net4Classes {
	var starts, classes []uint32
	lpm.Walk(&attr.Lpm, 4, func(from, _ []byte, class uint32) {
		starts = append(starts, binary.BigEndian.Uint32(from))
		classes = append(classes, class)
	})
	return Net4Classes{starts: starts, classes: classes}
}

// Class returns the class of an address.
func (m Net4Classes) Class(addr uint32) uint32 {
	stretch, found := slices.BinarySearch(m.starts, addr)
	if !found {
		stretch--
	}
	return m.classes[stretch]
}

// CheckStage returns an error naming the first key and rule for which
// the stage, reaching the class the artefact gives the key, disagrees
// with the reference on whether the rule matches.
func CheckStage(stage *classify.Stage, keys []uint32, classOf func(key uint32) uint32, matches func(rule int, key uint32) bool) error {
	// reaches tells, per group, which classes it reaches.
	reaches := make([][]bool, stage.GroupCount())
	for group := range reaches {
		reaches[group] = make([]bool, stage.MaxValue+1)
		for _, class := range stage.Group(uint32(group)) {
			reaches[group][class] = true
		}
	}
	for _, key := range keys {
		class := classOf(key)
		for rule, group := range stage.RuleGroups {
			reached := group != classify.InvalidGroup && class <= stage.MaxValue && reaches[group][class]
			if want := matches(rule, key); reached != want {
				return fmt.Errorf("key %#x of class %d: rule %d matches %t through the stage, want %t", key, class, rule, reached, want)
			}
		}
	}
	return nil
}
