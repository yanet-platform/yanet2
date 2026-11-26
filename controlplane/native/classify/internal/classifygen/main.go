//go:build linux && (amd64 || arm64)

// Command classifygen compiles the classifiers of a fixed set of rulesets
// from Go into a file in /dev/shm, for the C test of lib/classify to
// check.
//
// Usage:
//
//	classifygen NAME
//
// The file, /dev/shm/NAME, starts with a header and a record per case
// naming its attribute, its rules and its region. The rules and the items
// they match follow as flat records C's getters read in place, then the
// regions. A region holds an allocator, its root memory context and the
// attributes Go committed at the offsets the header gives, then the arena
// they allocate from to its end. The C test,
// tests/common/classify_native_test.c, declares the layout again.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/classify/internal/classifytest"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// magic opens a file this command wrote ("classify").
	magic = 0x636c617373696679
	// regionSize is the size of a region.
	regionSize = 16 << 20
	// regionAlign is the alignment of a region within the file.
	regionAlign = 4096
	// nameSize is the size of a case's name, the terminating zero
	// included; the name also titles the compile's memory contexts.
	nameSize = 128
)

// The attributes a case compiles.
const (
	kindLine = 1
	kindNet4 = 2
)

// header opens the file and is followed by its cases.
type header struct {
	Magic uint64
	// RedZone is the guard the allocator puts on both sides of a block.
	RedZone    uint64
	RegionSize uint64
	// Allocator, Context, Line and Net4 are the offsets of the
	// allocator, the root memory context and the attributes within a
	// region.
	Allocator uint64
	Context   uint64
	Line      uint64
	Net4      uint64
	// Arena is the offset of the arena within a region; it runs to the
	// region's end.
	Arena     uint64
	CaseSize  uint64
	CaseCount uint64
}

// caseRecord is a case as the file holds it.
type caseRecord struct {
	Name [nameSize]byte
	Kind uint32
	// Domain is the size of a line's domain.
	Domain    uint32
	RuleCount uint64
	ItemCount uint64
	// Rules, Items and Region are file offsets.
	Rules  uint64
	Items  uint64
	Region uint64
	// Free is what the region's allocator held free before the commit.
	Free uint64
}

// ruleRecord is a rule as the file holds it: its items, a run of the
// case's items, or none for an absent rule.
type ruleRecord struct {
	First  uint32
	Count  uint32
	Absent uint32
	_      uint32
}

// net4Item is an IPv4 network as struct net4 holds it.
type net4Item struct {
	Addr [4]byte
	Mask [4]byte
}

// testCase is a ruleset of one attribute.
type testCase struct {
	Name   string
	Kind   uint32
	Domain uint32
	Lines  []classify.LineRule
	Nets   []classify.Net4Rule
}

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" || strings.Contains(os.Args[1], "/") {
		fmt.Fprintln(os.Stderr, "usage: classifygen NAME")
		os.Exit(2)
	}
	if err := generate(filepath.Join("/dev/shm", os.Args[1]), cases()); err != nil {
		fmt.Fprintf(os.Stderr, "classifygen: %v\n", err)
		os.Exit(1)
	}
}

// cases returns the rulesets the file holds: random ones of every shape
// the compiles tell apart, from fixed seeds, and the edge ones.
func cases() []testCase {
	var all []testCase
	for _, size := range []uint32{0x100, 0x10000} {
		all = append(all, testCase{Name: fmt.Sprintf("line of %#x points without rules", size), Kind: kindLine, Domain: size})
		for seed := range uint64(8) {
			all = append(all, testCase{
				Name:   fmt.Sprintf("line of %#x points, seed %d", size, seed),
				Kind:   kindLine,
				Domain: size,
				Lines:  randomLineRules(seed, 6*int(seed), size),
			})
		}
	}
	all = append(all, testCase{Name: "net4 without rules", Kind: kindNet4})
	for seed := range uint64(16) {
		all = append(all, testCase{Name: fmt.Sprintf("net4, seed %d", seed), Kind: kindNet4, Nets: randomNet4Rules(seed, 4*int(seed))})
	}
	all = append(all, testCase{
		Name: "net4 whose name is cut to leave room for the leaf " + strings.Repeat("x", 40),
		Kind: kindNet4,
		Nets: randomNet4Rules(100, 8),
	})
	return all
}

// randomLineRules returns count reproducible rules over a domain of size
// points: absent, empty, whole-domain and repeated rules, and intervals
// that reach past the domain or end before they start.
func randomLineRules(seed uint64, count int, size uint32) []classify.LineRule {
	random := rand.New(rand.NewPCG(seed, uint64(size)))
	rules := make([]classify.LineRule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.LineRule{Absent: true}
		case shape == 1:
			rules[idx] = classify.LineRule{}
		case shape == 2:
			rules[idx] = classify.LineRule{Ranges: []classify.LineRange{{From: 0, To: size + 44}}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			spans := make([]classify.LineRange, 1+random.IntN(3))
			for spanIdx := range spans {
				from := random.Uint32N(size + size/8)
				spans[spanIdx] = classify.LineRange{From: from, To: from + random.Uint32N(size/6) - 4}
			}
			rules[idx] = classify.LineRule{Ranges: spans}
		}
	}
	return rules
}

// randomNet4Rules returns count reproducible IPv4 network rules: nested
// prefixes, including one ending where its parent ends, unmasked
// addresses, and absent, empty, whole-space and repeated rules.
func randomNet4Rules(seed uint64, count int) []classify.Net4Rule {
	random := rand.New(rand.NewPCG(seed, seed^0x7f4a7c15))
	var known []netip.Prefix
	prefix := func() netip.Prefix {
		if len(known) > 0 && random.IntN(3) == 0 {
			parent := known[random.IntN(len(known))]
			bits := min(32, parent.Bits()+1+random.IntN(12))
			addr := parent.Addr().As4()
			value := binary.BigEndian.Uint32(addr[:])
			if random.IntN(4) == 0 {
				value |= ^uint32(0) >> parent.Bits()
			} else {
				value |= random.Uint32() >> parent.Bits()
			}
			binary.BigEndian.PutUint32(addr[:], value)
			return netip.PrefixFrom(netip.AddrFrom4(addr), bits)
		}
		var addr [4]byte
		binary.BigEndian.PutUint32(addr[:], random.Uint32())
		created := netip.PrefixFrom(netip.AddrFrom4(addr), 4+random.IntN(29))
		known = append(known, created.Masked())
		return created
	}
	rules := make([]classify.Net4Rule, count)
	for idx := range rules {
		switch shape := random.IntN(20); {
		case shape == 0:
			rules[idx] = classify.Net4Rule{Absent: true}
		case shape == 1:
			rules[idx] = classify.Net4Rule{}
		case shape == 2:
			rules[idx] = classify.Net4Rule{Prefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), prefix()}}
		case shape < 6 && idx > 0:
			rules[idx] = rules[random.IntN(idx)]
		default:
			prefixes := make([]netip.Prefix, 1+random.IntN(4))
			for netIdx := range prefixes {
				prefixes[netIdx] = prefix()
			}
			rules[idx] = classify.Net4Rule{Prefixes: prefixes}
		}
	}
	return rules
}

// generate writes the file of the cases at path, which must not exist,
// and removes it again on a failure.
func generate(path string, all []testCase) (err error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	recordsEnd := unsafe.Sizeof(header{}) + uintptr(len(all))*unsafe.Sizeof(caseRecord{})
	dataEnd := recordsEnd
	for _, testCase := range all {
		rules, items := testCase.Counts()
		dataEnd += uintptr(rules)*unsafe.Sizeof(ruleRecord{}) + uintptr(items)*unsafe.Sizeof(net4Item{})
	}
	regions := (dataEnd + regionAlign - 1) &^ (regionAlign - 1)
	size := regions + uintptr(len(all))*regionSize
	if err := file.Truncate(int64(size)); err != nil {
		return err
	}
	mapping, err := syscall.Mmap(int(file.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("map %s: %w", path, err)
	}
	defer func() {
		err = errors.Join(err, syscall.Munmap(mapping))
	}()

	*(*header)(unsafe.Pointer(&mapping[0])) = header{
		Magic:      magic,
		RedZone:    shmalloc.RedZone,
		RegionSize: regionSize,
		Allocator:  uint64(unsafe.Offsetof(classifytest.Memory{}.Allocator)),
		Context:    uint64(unsafe.Offsetof(classifytest.Memory{}.Context)),
		Line:       uint64(unsafe.Offsetof(classifytest.Memory{}.Line)),
		Net4:       uint64(unsafe.Offsetof(classifytest.Memory{}.Net4)),
		Arena:      uint64(unsafe.Sizeof(classifytest.Memory{})),
		CaseSize:   uint64(unsafe.Sizeof(caseRecord{})),
		CaseCount:  uint64(len(all)),
	}
	records := unsafe.Slice((*caseRecord)(unsafe.Pointer(&mapping[unsafe.Sizeof(header{})])), len(all))
	data := uint64(recordsEnd)
	for idx, testCase := range all {
		record := &records[idx]
		if len(testCase.Name) >= nameSize {
			return fmt.Errorf("%s: a case's name holds under %d bytes", testCase.Name, nameSize)
		}
		copy(record.Name[:], testCase.Name)
		record.Kind = testCase.Kind
		record.Domain = testCase.Domain
		record.Region = uint64(regions) + uint64(idx)*regionSize
		data = testCase.Describe(record, mapping, data)
		if err := testCase.Commit(record, mapping[record.Region:][:regionSize]); err != nil {
			return fmt.Errorf("%s: %w", testCase.Name, err)
		}
	}
	return nil
}

// Counts returns how many rules and items the case holds.
func (m testCase) Counts() (int, int) {
	items := 0
	for _, rule := range m.Lines {
		items += len(rule.Ranges)
	}
	for _, rule := range m.Nets {
		items += len(rule.Prefixes)
	}
	return len(m.Lines) + len(m.Nets), items
}

// Describe writes the case's rules and items into the file at data and
// returns where the next case's start.
//
// An item takes eight bytes: a line interval as struct classify_line_range
// holds it, or a network as struct net4 does.
func (m testCase) Describe(record *caseRecord, mapping []byte, data uint64) uint64 {
	ruleCount, itemCount := m.Counts()
	record.RuleCount = uint64(ruleCount)
	record.ItemCount = uint64(itemCount)
	record.Rules = data
	record.Items = data + uint64(ruleCount)*uint64(unsafe.Sizeof(ruleRecord{}))
	rules := unsafe.Slice((*ruleRecord)(unsafe.Pointer(&mapping[record.Rules])), ruleCount)
	items := unsafe.Pointer(&mapping[record.Items])
	first := 0
	if m.Kind == kindLine {
		spans := unsafe.Slice((*classify.LineRange)(items), itemCount)
		for idx, rule := range m.Lines {
			rules[idx] = ruleRecord{First: uint32(first), Count: uint32(len(rule.Ranges))}
			if rule.Absent {
				rules[idx].Absent = 1
			}
			first += copy(spans[first:], rule.Ranges)
		}
	} else {
		nets := unsafe.Slice((*net4Item)(items), itemCount)
		for idx, rule := range m.Nets {
			rules[idx] = ruleRecord{First: uint32(first), Count: uint32(len(rule.Prefixes))}
			if rule.Absent {
				rules[idx].Absent = 1
			}
			for _, prefix := range rule.Prefixes {
				nets[first].Addr = prefix.Addr().As4()
				binary.BigEndian.PutUint32(nets[first].Mask[:], ^uint32(0)<<(32-prefix.Bits()))
				first++
			}
		}
	}
	return record.Items + uint64(itemCount)*uint64(unsafe.Sizeof(net4Item{}))
}

// Commit lays out the region, compiles the case and commits its attribute
// into the region.
func (m testCase) Commit(record *caseRecord, region []byte) error {
	memory := classifytest.LayOut(region)
	record.Free = uint64(shmalloc.FreeSize(&memory.Allocator))
	if m.Kind == kindLine {
		return classify.CompileLine(m.Lines, m.Domain).Commit(&memory.Line, &memory.Context, m.Name)
	}
	nets, err := classify.CompileNet4(m.Nets)
	if err != nil {
		return err
	}
	return nets.Commit(&memory.Net4, &memory.Context, m.Name)
}
