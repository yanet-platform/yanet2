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
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

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
	m.Record = Record{Magic: recordMagic, ABI: offsetABI, Schema: 3, Active: 1, PID: int32(os.Getpid()), Start: generation}
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
	require.EqualValues(t, 1, source.writer, "report mutated producer state")
	output := m.report(true)
	require.Regexp(t, `(?m)^cp_lock_acquisitions_total\{[^}\n]*\} 1$`, output)
	require.Regexp(t, `(?m)^cp_lock_hold_seconds_count\{[^}\n]*\} 1$`, output)
	require.Regexp(t, `(?m)^cp_lock_hold_seconds_bucket\{[^}\n]*le="\+Inf"\} 1$`, output)

	source.wait_sum_ns = 25
	source.hold_sum_ns = 30
	source.hist[4] = 1
	source.generation++
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
			bucket := completed % uint64(len(bins))
			bins[bucket]++
			storeNative(&source.hist[bucket], bins[bucket])
			storeNative(&source.generation, completed)
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

// Test_Symbolize_ReturnAddressFallback verifies translated callers when GNU lacks a line.
func Test_Symbolize_ReturnAddressFallback(t *testing.T) {
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

	for _, tc := range []struct {
		Name, GNU, Expected string
	}{
		{"unknown source line", "??:?", "fixture.go:3"},
		{"zero source line", "??:0", "fixture.go:3"},
		{"GNU discriminator", "fixture.go:42 (discriminator 2)", "fixture.go:42"},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			directory := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' 'nativeCaller' '" + tc.GNU + "'\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "addr2line"), []byte(script), 0755))
			t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
			m := Coordinator{}
			name, line := m.symbolize(relocation+address+1, maps, map[string]*dwarf.Data{})
			require.Equal(t, "nativeCaller", name)
			require.Equal(t, tc.Expected, filepath.Base(line))
		})
	}
}

// Test_MergeSites_SourceIdentity verifies that equal source sites form one coherent histogram.
func Test_MergeSites_SourceIdentity(t *testing.T) {
	first := Row{Name: "caller", Line: "caller.c:42"}
	first.Stats.address = 0x1000
	first.Stats.count = 9
	first.Stats.fail_count = 1
	first.Stats.wait_sum_ns = 12
	first.Stats.wait_max_ns = 8
	first.Stats.hold_sum_ns = 20
	first.Stats.hold_max_ns = 10
	first.Stats.hist[3] = 2

	second := Row{Name: first.Name, Line: first.Line}
	second.Stats.address = 0x2000
	second.Stats.count = 7
	second.Stats.fail_count = 2
	second.Stats.wait_sum_ns = 3
	second.Stats.wait_max_ns = 3
	second.Stats.hold_sum_ns = 16
	second.Stats.hold_max_ns = 16
	second.Stats.hist[4] = 1

	distinct := Row{Name: first.Name, Line: "caller.c:43"}
	rows := mergeSites([]Row{first, second, distinct})
	require.Len(t, rows, 2)
	require.Equal(t, distinct, rows[1], "different source line merged")
	stats := rows[0].Stats
	require.EqualValues(t, 3, stats.count)
	require.EqualValues(t, 3, stats.fail_count)
	require.EqualValues(t, 15, stats.wait_sum_ns)
	require.EqualValues(t, 8, stats.wait_max_ns)
	require.EqualValues(t, 36, stats.hold_sum_ns)
	require.EqualValues(t, 16, stats.hold_max_ns)
	require.EqualValues(t, 2, stats.hist[3])
	require.EqualValues(t, 1, stats.hist[4])
}
