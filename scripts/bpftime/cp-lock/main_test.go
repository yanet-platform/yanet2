//go:build cp_lock_bpftime

package main

import (
	"context"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"unicode/utf8"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// Test_Execute_Help verifies that option discovery needs no active session.
func Test_Execute_Help(t *testing.T) {
	arguments := os.Args
	t.Cleanup(func() { os.Args = arguments })
	t.Setenv("BPFTIME_GLOBAL_SHM_NAME", "invalid/name")
	for _, command := range []string{"setup", "report", "stop"} {
		os.Args = []string{"yanet-cp-lock", command, "--help"}
		require.NotPanics(t, execute)
	}
}

// Test_Execute_EmptyStop verifies that an absent session needs no cleanup.
func Test_Execute_EmptyStop(t *testing.T) {
	arguments := os.Args
	t.Cleanup(func() { os.Args = arguments })
	os.Args = []string{"yanet-cp-lock", "stop"}
	t.Setenv("BPFTIME_GLOBAL_SHM_NAME", fmt.Sprintf("cp_lock_absent_%d", os.Getpid()))
	require.NotPanics(t, execute)
}

// Test_Compatible_PreviousSchema verifies that old statistics can only be
// detached and reset, while unknown layouts remain unsupported.
func Test_Compatible_PreviousSchema(t *testing.T) {
	m := &Coordinator{Record: Record{ABI: offsetABI, Schema: 3}}
	require.Panics(t, func() { m.compatible(false) })
	require.NotPanics(t, func() { m.compatible(true) })
	require.EqualValues(t, 584, unsafe.Sizeof(Row{}.Stats))
	require.EqualValues(t, 8, unsafe.Offsetof(Row{}.Stats.total_max_ns))
	require.EqualValues(t, 72, unsafe.Offsetof(Row{}.Stats.hist))
	m.Record.ABI++
	require.Panics(t, func() { m.compatible(true) })
	m.Record.ABI = offsetABI
	m.Record.Schema = 2
	require.Panics(t, func() { m.compatible(true) })
}

// Test_Quote_UTF8 verifies valid labels and merging after invalid-byte repair.
func Test_Quote_UTF8(t *testing.T) {
	require.Equal(t, "a\\\"\\\\\\n\uFFFD", quote("a\"\\\n\xff"))
	require.True(t, utf8.ValidString(quote("\xff")))
	rows := mergeSites([]Row{{Name: "caller\xff", Line: "file:1"}, {Name: "caller\xfe", Line: "file:1"}})
	require.Len(t, rows, 1)
	require.Equal(t, "caller\uFFFD", rows[0].Name)
}

// Test_Stop_FailedReset verifies that a partial stop cannot be reused as
// active.
func Test_Stop_FailedReset(t *testing.T) {
	m, _ := statsFixture(t)
	var err error
	m.File, err = os.CreateTemp(t.TempDir(), "record")
	require.NoError(t, err)
	defer m.File.Close()
	identity, err := buildID("/proc/self/exe")
	require.NoError(t, err)
	copyText(m.Record.Build[:], identity)
	copyText(m.Record.Runtime[:], runtimeLabel)
	m.Helper = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(m.Helper, "setup"), []byte("#!/bin/sh\nexit 1\n"), 0755))
	controlFixture(t, os.Getpid())
	m.save()
	require.Panics(t, m.stop)
	var persisted Record
	_, err = m.File.ReadAt(unsafe.Slice((*byte)(unsafe.Pointer(&persisted)), int(unsafe.Sizeof(persisted))), 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, persisted.Active)
	require.EqualValues(t, 2, m.Record.Active)
}

// Test_Stop_UnreachableAgent verifies that absent agents permit cleanup while
// mapped agents without a detach acknowledgement retain their shared memory.
func Test_Stop_UnreachableAgent(t *testing.T) {
	for _, tc := range []struct {
		Name, Library string
		Resident      bool
	}{
		{"failed injection", "unrelated.so", false},
		{"unresponsive resident agent", "libbpftime-agent.so", true},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			m, _ := statsFixture(t)
			var err error
			m.File, err = os.CreateTemp(t.TempDir(), "record")
			require.NoError(t, err)
			defer m.File.Close()
			identity, err := buildID("/proc/self/exe")
			require.NoError(t, err)
			copyText(m.Record.Build[:], identity)
			copyText(m.Record.Runtime[:], runtimeLabel)
			m.Helper = t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(m.Helper, "setup"), []byte("#!/bin/sh\nexit 0\n"), 0755))
			library, err := os.Create(filepath.Join(t.TempDir(), tc.Library))
			require.NoError(t, err)
			defer library.Close()
			require.NoError(t, library.Truncate(4096))
			mapping, err := syscall.Mmap(int(library.Fd()), 0, 4096, syscall.PROT_READ, syscall.MAP_PRIVATE)
			require.NoError(t, err)
			defer syscall.Munmap(mapping)
			m.Record.Active = 2
			if tc.Resident {
				require.Panics(t, m.stop)
				require.EqualValues(t, 2, m.Record.Active)
			} else {
				require.NotPanics(t, m.stop)
				require.EqualValues(t, -1, m.Record.Active)
			}
			m.owned()
		})
	}
}

// statsFixture maps a private session payload with the production row layout.
func statsFixture(t *testing.T) (*Coordinator, []byte) {
	t.Helper()
	file, err := os.CreateTemp("/dev/shm", "cp-lock-reader-*")
	require.NoError(t, err)
	defer file.Close()
	t.Cleanup(func() { os.Remove(file.Name()) })

	const sitesOffset = 8
	countsOffset := sitesOffset + 1024*int(unsafe.Sizeof(Row{}.Stats))
	size := countsOffset + 16
	require.NoError(t, file.Truncate(int64(size)))
	info, err := file.Stat()
	require.NoError(t, err)
	memory, err := syscall.Mmap(int(file.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, syscall.Munmap(memory)) })

	m := &Coordinator{Name: filepath.Base(file.Name()), Path: file.Name()}
	generation, err := startTime(os.Getpid())
	require.NoError(t, err)
	m.Record = Record{Magic: recordMagic, ABI: offsetABI, Schema: 4, Active: 1, PID: int32(os.Getpid()), Start: generation}
	copyText(m.Record.Boot[:], readLine("/proc/sys/kernel/random/boot_id"))
	m.Record.Inode = info.Sys().(*syscall.Stat_t).Ino
	m.Record.MapOffsets = [2]uint64{sitesOffset, uint64(countsOffset)}
	return m, memory
}

// mappedValue exposes an aligned fixture payload using the native result type.
func mappedValue[T any](memory []byte, _ T) *T {
	return (*T)(unsafe.Pointer(&memory[0]))
}

// storeNative publishes one native-width field from the synthetic producer.
func storeNative[T ~uint64](destination *T, value uint64) {
	atomic.StoreUint64((*uint64)(unsafe.Pointer(destination)), value)
}

// Test_ReadStats_PausedWriter verifies that an unfinished row remains readable.
func Test_ReadStats_PausedWriter(t *testing.T) {
	m, memory := statsFixture(t)
	statistics, _ := m.readStats()
	source := mappedValue(memory[m.Record.MapOffsets[0]:], statistics[0])
	source.address = 0x1000
	source.writer = 1
	source.count = 2
	source.fail_count = 3
	source.wait_sum_ns = 5
	source.wait_max_ns = 15
	source.hold_sum_ns = 10
	source.hold_max_ns = 20
	source.total_max_ns = 28
	source.hist[3] = 1

	statistics, _ = m.readStats()
	rows := mergeSites([]Row{{Stats: statistics[0], Name: "caller", Line: "caller.c:42"}})
	require.EqualValues(t, 1, rows[0].Stats.count)
	require.EqualValues(t, 0x1000, rows[0].Stats.address)
	require.EqualValues(t, 3, rows[0].Stats.fail_count)
	require.EqualValues(t, 5, rows[0].Stats.wait_sum_ns)
	require.EqualValues(t, 15, rows[0].Stats.wait_max_ns)
	require.EqualValues(t, 10, rows[0].Stats.hold_sum_ns)
	require.EqualValues(t, 20, rows[0].Stats.hold_max_ns)
	require.EqualValues(t, 28, rows[0].Stats.total_max_ns)
	require.EqualValues(t, 1, source.writer, "report mutated producer state")
	output := m.report(true)
	require.Regexp(t, `(?m)^cp_lock_acquisitions_total\{[^}\n]*\} 1$`, output)
	require.Regexp(t, `(?m)^cp_lock_hold_seconds_count\{[^}\n]*\} 1$`, output)
	require.Regexp(t, `(?m)^cp_lock_hold_seconds_bucket\{[^}\n]*le="\+Inf"\} 1$`, output)

	source.wait_sum_ns = 25
	source.hold_sum_ns = 30
	source.hist[4] = 1
	source.writer = 0
	statistics, _ = m.readStats()
	rows = mergeSites([]Row{{Stats: statistics[0], Name: "caller", Line: "caller.c:42"}})
	require.EqualValues(t, 2, rows[0].Stats.count)
	require.EqualValues(t, 30, rows[0].Stats.hold_sum_ns)
	repeated, _ := m.readStats()
	require.Equal(t, statistics, repeated, "settled reads changed statistics")
}

// Test_ReadStats_ConcurrentWrites verifies that samples stay monotonic and
// match completed writes after the producer stops.
func Test_ReadStats_ConcurrentWrites(t *testing.T) {
	m, memory := statsFixture(t)
	statistics, counts := m.readStats()
	source := mappedValue(memory[m.Record.MapOffsets[0]:], statistics[0])
	counters := mappedValue(memory[m.Record.MapOffsets[1]:], counts)
	source.address = 0x1000

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	defer func() { cancel(); group.Wait() }()
	started := make(chan struct{})
	progress := make(chan struct{})
	var completed uint64
	var bins [64]uint64
	group.Go(func() error {
		for ctx.Err() == nil {
			completed++
			storeNative(&source.writer, 1)
			storeNative(&source.count, completed)
			storeNative(&source.fail_count, completed/3)
			storeNative(&source.wait_sum_ns, completed*2)
			storeNative(&source.hold_sum_ns, completed*3)
			storeNative(&source.wait_max_ns, completed)
			storeNative(&source.hold_max_ns, completed)
			storeNative(&source.total_max_ns, completed*2)
			bucket := completed % uint64(len(bins))
			bins[bucket]++
			storeNative(&source.hist[bucket], bins[bucket])
			storeNative(&source.writer, 0)
			storeNative(&counters.drops, completed)
			storeNative(&counters.overflow, completed/7)
			if completed == 1 {
				close(started)
			}
			select {
			case progress <- struct{}{}:
			default:
			}
			runtime.Gosched()
		}
		return nil
	})
	<-started

	previous := Row{}
	var previousDrops, previousOverflow uint64
	for range 30 {
		statistics, counts = m.readStats()
		row := mergeSites([]Row{{Stats: statistics[0], Name: "caller", Line: "caller.c:42"}})[0]
		require.EqualValues(t, 0x1000, row.Stats.address)
		require.GreaterOrEqual(t, row.Stats.count, previous.Stats.count)
		require.GreaterOrEqual(t, row.Stats.fail_count, previous.Stats.fail_count)
		require.GreaterOrEqual(t, row.Stats.wait_sum_ns, previous.Stats.wait_sum_ns)
		require.GreaterOrEqual(t, row.Stats.hold_sum_ns, previous.Stats.hold_sum_ns)
		require.GreaterOrEqual(t, row.Stats.wait_max_ns, previous.Stats.wait_max_ns)
		require.GreaterOrEqual(t, row.Stats.hold_max_ns, previous.Stats.hold_max_ns)
		require.GreaterOrEqual(t, row.Stats.total_max_ns, previous.Stats.total_max_ns)
		require.GreaterOrEqual(t, uint64(counts.drops), previousDrops)
		require.GreaterOrEqual(t, uint64(counts.overflow), previousOverflow)
		for idx := range row.Stats.hist {
			require.GreaterOrEqual(t, row.Stats.hist[idx], previous.Stats.hist[idx])
		}
		previous = row
		previousDrops, previousOverflow = uint64(counts.drops), uint64(counts.overflow)
		<-progress
	}
	cancel()
	require.NoError(t, group.Wait())
	require.Greater(t, completed, uint64(1), "producer made no concurrent progress")

	statistics, counts = m.readStats()
	stats := statistics[0]
	require.EqualValues(t, completed, stats.count)
	require.EqualValues(t, completed/3, stats.fail_count)
	require.EqualValues(t, completed*2, stats.wait_sum_ns)
	require.EqualValues(t, completed*3, stats.hold_sum_ns)
	require.EqualValues(t, completed, stats.wait_max_ns)
	require.EqualValues(t, completed, stats.hold_max_ns)
	require.EqualValues(t, completed*2, stats.total_max_ns)
	require.EqualValues(t, completed, counts.drops)
	require.EqualValues(t, completed/7, counts.overflow)
	for idx, value := range bins {
		require.EqualValues(t, value, stats.hist[idx])
	}
	repeated, _ := m.readStats()
	require.Equal(t, statistics, repeated)
}

// Test_ReadStats_InvalidMapping verifies that approximate reads retain memory
// and session checks.
func Test_ReadStats_InvalidMapping(t *testing.T) {
	for _, tc := range []struct {
		Name   string
		Change func(*Coordinator)
	}{
		{"incompatible schema", func(m *Coordinator) { m.Record.Schema++ }},
		{"unsupported offset ABI", func(m *Coordinator) { m.Record.ABI++ }},
		{"different inode", func(m *Coordinator) { m.Record.Inode++ }},
		{"out of bounds", func(m *Coordinator) { m.Record.MapOffsets[0] = ^uint64(0) }},
		{"unaligned payload", func(m *Coordinator) { m.Record.MapOffsets[0]++ }},
		{"unaligned counters", func(m *Coordinator) { m.Record.MapOffsets[1]++ }},
		{"truncated counters", func(m *Coordinator) { m.Record.MapOffsets[1] += 8 }},
		{"overlapping payloads", func(m *Coordinator) { m.Record.MapOffsets[1] = m.Record.MapOffsets[0] }},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			m, _ := statsFixture(t)
			tc.Change(m)
			require.Panics(t, func() { m.report(true) })
			if tc.Name == "different inode" {
				require.Panics(t, func() { m.readStats() })
			}
		})
	}
}

// Test_Symbolize_TrustedTool verifies native callers resolve without executing
// a replacement tool from the caller's search path.
func Test_Symbolize_TrustedTool(t *testing.T) {
	path, debug := symbolFixture(t)
	address := symbolAddress(t, debug, "nativeCaller")
	image, err := elf.Open(path)
	require.NoError(t, err)
	defer image.Close()

	const relocation = 0x100000000
	var maps []byte
	for _, segment := range image.Progs {
		if segment.Type == elf.PT_LOAD && address >= segment.Vaddr && address-segment.Vaddr < segment.Filesz {
			maps = fmt.Appendf(nil, "%x-%x r-xp %x 00:00 0 %s\n", relocation+segment.Vaddr, relocation+segment.Vaddr+segment.Filesz, segment.Off, path)
			break
		}
	}
	require.NotEmpty(t, maps)

	directory := t.TempDir()
	marker := filepath.Join(directory, "executed")
	script := "#!/bin/sh\nprintf executed > \"" + marker + "\"\nexit 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "addr2line"), []byte(script), 0755))
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	m := Coordinator{}
	name, line := m.symbolize(relocation+address+1, maps, map[string]*dwarf.Data{})
	require.Equal(t, "nativeCaller", name)
	require.Equal(t, "fixture.go:3", filepath.Base(line))
	require.NoFileExists(t, marker)
}

// Test_CleanupTemporary_Ownership verifies interrupted publication is reclaimed
// whether the final name exists, while foreign files and symlinks survive.
func Test_CleanupTemporary_Ownership(t *testing.T) {
	for _, name := range []string{"final present", "final absent", "foreign regular", "foreign symlink"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "segment")
			require.NoError(t, os.WriteFile(path, []byte("owned"), 0600))
			info, err := os.Stat(path)
			require.NoError(t, err)
			m := Coordinator{Path: path, Record: Record{Magic: recordMagic, Inode: info.Sys().(*syscall.Stat_t).Ino, Session: 7}}
			temporary := path + ".new-7"
			switch name {
			case "foreign regular":
				require.NoError(t, os.WriteFile(temporary, []byte("foreign"), 0600))
			case "foreign symlink":
				require.NoError(t, os.Symlink(path, temporary))
			default:
				require.NoError(t, os.Link(path, temporary))
			}
			if name == "final absent" {
				require.NoError(t, os.Remove(path))
			}
			m.cleanupTemporary()
			m.cleanupTemporary()
			_, err = os.Lstat(temporary)
			if name == "foreign regular" || name == "foreign symlink" {
				require.NoError(t, err)
			} else {
				require.True(t, os.IsNotExist(err))
			}
			if name != "final absent" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "owned", string(data))
			}
		})
	}
}

// Test_MergeSites_SourceIdentity verifies that equal source sites form one
// coherent histogram.
func Test_MergeSites_SourceIdentity(t *testing.T) {
	first := Row{Name: "caller", Line: normalizeLocation("/checkout/modules/route/caller.c:42", "/checkout", "/checkout/build")}
	first.Stats.address = 0x1000
	first.Stats.count = 9
	first.Stats.fail_count = 1
	first.Stats.wait_sum_ns = 12
	first.Stats.wait_max_ns = 8
	first.Stats.hold_sum_ns = 20
	first.Stats.hold_max_ns = 10
	first.Stats.total_max_ns = 15
	first.Stats.hist[3] = 2

	second := Row{Name: first.Name, Line: normalizeLocation("../modules/route/caller.c:42", "/checkout", "/checkout/build")}
	second.Stats.address = 0x2000
	second.Stats.count = 7
	second.Stats.fail_count = 2
	second.Stats.wait_sum_ns = 3
	second.Stats.wait_max_ns = 3
	second.Stats.hold_sum_ns = 16
	second.Stats.hold_max_ns = 16
	second.Stats.total_max_ns = 18
	second.Stats.hist[4] = 1

	distinct := Row{Name: first.Name, Line: "modules/route/caller.c:43"}
	distinctFile := Row{Name: first.Name, Line: normalizeLocation("../modules/forward/caller.c:42", "/checkout", "/checkout/build")}
	rows := mergeSites([]Row{first, second, distinct, distinctFile})
	require.Len(t, rows, 3)
	require.Equal(t, distinct, rows[1], "different source line merged")
	require.Equal(t, distinctFile, rows[2], "different source file merged")
	stats := rows[0].Stats
	require.EqualValues(t, 3, stats.count)
	require.EqualValues(t, 3, stats.fail_count)
	require.EqualValues(t, 15, stats.wait_sum_ns)
	require.EqualValues(t, 8, stats.wait_max_ns)
	require.EqualValues(t, 36, stats.hold_sum_ns)
	require.EqualValues(t, 16, stats.hold_max_ns)
	require.EqualValues(t, 18, stats.total_max_ns)
	require.EqualValues(t, 2, stats.hist[3])
	require.EqualValues(t, 1, stats.hist[4])
	require.Equal(t, uint64(31), rows[0].holdPercentile95())
}

// Test_HoldPercentile95_BucketRanks verifies nearest-rank selection across
// histogram boundaries, including an empty sample and the unbounded last bin.
func Test_HoldPercentile95_BucketRanks(t *testing.T) {
	for _, tc := range []struct {
		Name      string
		Histogram [64]uint64
		Want      uint64
	}{
		{"empty sample", [64]uint64{}, 0},
		{"single smallest observation", [64]uint64{0: 1}, 1},
		{"one observation", [64]uint64{10: 1}, 2047},
		{"exact 95 percent boundary", [64]uint64{3: 19, 17: 1}, 15},
		{"round rank upward", [64]uint64{3: 19, 17: 2}, 262143},
		{"last bucket", [64]uint64{63: 1}, ^uint64(0)},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			row := Row{}
			var count uint64
			for idx, observations := range tc.Histogram {
				storeNative(&row.Stats.hist[idx], observations)
				count += observations
			}
			storeNative(&row.Stats.count, count)
			require.Equal(t, tc.Want, row.holdPercentile95())
		})
	}
}
