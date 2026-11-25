//go:build linux && (amd64 || arm64)

// Command lpmgen builds the prefix trees of a fixed set of cases from Go
// into a file in /dev/shm, for the C test of common/lpm.h to check.
//
// Usage:
//
//	lpmgen NAME
//
// The file, /dev/shm/NAME, starts with a header naming, for every case,
// its inserts and two regions: one holds the tree as built, the other the
// same tree after the Go free. A region holds an allocator, its root
// memory context and the tree at the offsets the header gives, then the
// arena they allocate from to its end. Regions start aligned to their
// size, so a region laid out the same way anywhere else cuts its arena
// into the same blocks. The C test, tests/common/lpm_native_test.c,
// declares the header's layout again.
package main

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// magic opens a file this command wrote ("lpmtrees").
	magic = 0x6c706d7472656573
	// filler is the byte a region starts filled with, so a byte one side
	// leaves unwritten differs from one the other writes.
	filler = 0xa5
	// regionSize is the size of a region and its alignment.
	regionSize = 1 << 20
	// arenaOffset is where a region's arena starts, past what it holds
	// before it.
	arenaOffset = 4096
	// maxKeySize is the longest key a case inserts, in bytes.
	maxKeySize = 16
	// maxInserts is the most inserts a case makes.
	maxInserts = 64
	// nameSize is the size of a case's name, the terminating zero
	// included.
	nameSize = 64
)

// header opens the file and is followed by its cases.
type header struct {
	Magic uint64
	// RedZone is the guard the allocator puts on both sides of a block.
	RedZone    uint64
	Filler     uint64
	RegionSize uint64
	// Allocator, Context and Tree are the offsets of the allocator, the
	// root memory context and the tree within a region.
	Allocator uint64
	Context   uint64
	Tree      uint64
	// Arena is the offset of the arena within a region; it runs to the
	// region's end.
	Arena     uint64
	CaseSize  uint64
	CaseCount uint64
}

// caseRecord is a case as the file holds it.
type caseRecord struct {
	Name    [nameSize]byte
	KeySize uint64
	// Built and Freed are the file offsets of the region holding the tree
	// as built and of the one holding it after the Go free.
	Built       uint64
	Freed       uint64
	InsertCount uint64
	Inserts     [maxInserts]insertRecord
}

// insertRecord is an insert as the file holds it, its keys in their first
// key size bytes.
type insertRecord struct {
	From  [maxKeySize]byte
	To    [maxKeySize]byte
	Value uint32
	_     uint32
}

// region is what a region holds before its arena.
type region struct {
	Allocator layout.BlockAllocator
	Context   layout.MemoryContext
	Tree      layout.Lpm
}

// keyRange is an insert of the keys from the start through the end, both
// included, mapped to the value.
type keyRange struct {
	From  []byte
	To    []byte
	Value uint32
}

// prefixRange returns the insert of every address of a prefix.
func prefixRange(prefix string, value uint32) keyRange {
	parsed := netip.MustParsePrefix(prefix)
	from := parsed.Masked().Addr().AsSlice()
	to := parsed.Masked().Addr().AsSlice()
	for bit := parsed.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	return keyRange{From: from, To: to, Value: value}
}

// chunkedRanges returns count /25 ranges over distinct second octets,
// each splitting two pages off the tree, so 40 of them outgrow five page
// chunks and grow the chunk directory three times.
func chunkedRanges(count int) []keyRange {
	ranges := make([]keyRange, 0, count)
	for idx := range count {
		ranges = append(ranges, prefixRange(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7), uint32(idx)))
	}
	return ranges
}

// treeCases are the trees the file holds, each built from its ranges in
// order; a case without ranges has 4-byte keys.
var treeCases = []struct {
	Name   string
	Ranges []keyRange
}{
	{Name: "no ranges"},
	{Name: "one ipv4 range", Ranges: []keyRange{prefixRange("10.0.0.0/8", 1)}},
	{Name: "value with its top bit set", Ranges: []keyRange{prefixRange("10.0.0.0/8", 0x80000001)}},
	{
		Name: "overlapping ipv4 ranges, the later winning",
		Ranges: []keyRange{
			prefixRange("10.0.0.0/8", 1),
			prefixRange("10.1.2.128/25", 2),
			prefixRange("0.0.0.0/0", 3),
			prefixRange("10.1.0.0/16", 4),
		},
	},
	{
		Name:   "range off prefix boundaries",
		Ranges: []keyRange{{From: []byte{10, 0, 0, 5}, To: []byte{10, 0, 3, 250}, Value: 7}},
	},
	{
		Name:   "start past the end",
		Ranges: []keyRange{{From: []byte{10, 0, 0, 9}, To: []byte{10, 0, 0, 1}, Value: 7}},
	},
	{
		Name: "ipv6 ranges",
		Ranges: []keyRange{
			prefixRange("2001:db8::/32", 1),
			prefixRange("2001:db8:1::/48", 2),
			prefixRange("::1/128", 3),
			prefixRange("fe80::/10", 4),
		},
	},
	{Name: "chunk directory grown", Ranges: chunkedRanges(40)},
}

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" || strings.Contains(os.Args[1], "/") {
		fmt.Fprintln(os.Stderr, "usage: lpmgen NAME")
		os.Exit(2)
	}
	if err := generate(filepath.Join("/dev/shm", os.Args[1])); err != nil {
		fmt.Fprintf(os.Stderr, "lpmgen: %v\n", err)
		os.Exit(1)
	}
}

// generate writes the file at path, which must not exist, and removes it
// again on a failure.
func generate(path string) (err error) {
	if unsafe.Sizeof(region{}) > arenaOffset {
		return fmt.Errorf("a region's allocator, context and tree take %d bytes, past its arena at %d",
			unsafe.Sizeof(region{}), arenaOffset,
		)
	}
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

	// The spare region lets the regions start aligned wherever the file is
	// mapped.
	headerSize := unsafe.Sizeof(header{}) + uintptr(len(treeCases))*unsafe.Sizeof(caseRecord{})
	size := headerSize + regionSize + uintptr(2*len(treeCases))*regionSize
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

	base := uintptr(unsafe.Pointer(&mapping[0]))
	first := (base+headerSize+regionSize-1)&^(regionSize-1) - base
	*(*header)(unsafe.Pointer(&mapping[0])) = header{
		Magic:      magic,
		RedZone:    shmalloc.RedZone,
		Filler:     filler,
		RegionSize: regionSize,
		Allocator:  uint64(unsafe.Offsetof(region{}.Allocator)),
		Context:    uint64(unsafe.Offsetof(region{}.Context)),
		Tree:       uint64(unsafe.Offsetof(region{}.Tree)),
		Arena:      arenaOffset,
		CaseSize:   uint64(unsafe.Sizeof(caseRecord{})),
		CaseCount:  uint64(len(treeCases)),
	}
	records := unsafe.Slice((*caseRecord)(unsafe.Pointer(&mapping[unsafe.Sizeof(header{})])), len(treeCases))

	for idx, treeCase := range treeCases {
		record := &records[idx]
		if err := describe(record, treeCase.Name, treeCase.Ranges); err != nil {
			return err
		}
		record.Built = uint64(first + uintptr(2*idx)*regionSize)
		record.Freed = record.Built + regionSize

		if _, err := build(mapping[record.Built:][:regionSize], treeCase.Ranges); err != nil {
			return fmt.Errorf("%s: %w", treeCase.Name, err)
		}
		freed, err := build(mapping[record.Freed:][:regionSize], treeCase.Ranges)
		if err != nil {
			return fmt.Errorf("%s: %w", treeCase.Name, err)
		}
		lpm.Free(freed)
	}
	return nil
}

// describe writes a case's name, key size and inserts into its record.
func describe(record *caseRecord, name string, ranges []keyRange) error {
	if len(name) >= nameSize || len(ranges) > maxInserts {
		return fmt.Errorf("%s: a case holds a name under %d bytes and at most %d inserts", name, nameSize, maxInserts)
	}
	copy(record.Name[:], name)
	record.KeySize = 4
	if len(ranges) > 0 {
		record.KeySize = uint64(len(ranges[0].From))
	}
	record.InsertCount = uint64(len(ranges))
	for idx, insert := range ranges {
		if len(insert.From) > maxKeySize || len(insert.To) > maxKeySize {
			return fmt.Errorf("%s: a case's keys hold at most %d bytes", name, maxKeySize)
		}
		copy(record.Inserts[idx].From[:], insert.From)
		copy(record.Inserts[idx].To[:], insert.To)
		record.Inserts[idx].Value = insert.Value
	}
	return nil
}

// build fills the region with filler, lays out its allocator over its
// arena and its root memory context, and builds the tree from the ranges,
// in the order the C test takes the same steps.
func build(memory []byte, ranges []keyRange) (*layout.Lpm, error) {
	for idx := range memory {
		memory[idx] = filler
	}
	start := (*region)(unsafe.Pointer(&memory[0]))
	shmalloc.Init(&start.Allocator)
	shmalloc.PutArena(&start.Allocator, unsafe.Pointer(&memory[arenaOffset]), uintptr(len(memory)-arenaOffset))
	shmalloc.ContextInit(&start.Context, "root", &start.Allocator)

	if err := lpm.Init(&start.Tree, &start.Context, "tree"); err != nil {
		return nil, err
	}
	for _, insert := range ranges {
		if err := lpm.Insert(&start.Tree, insert.From, insert.To, insert.Value); err != nil {
			return nil, err
		}
	}
	return &start.Tree, nil
}
