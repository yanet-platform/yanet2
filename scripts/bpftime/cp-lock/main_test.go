//go:build cp_lock_bpftime

package main

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
	first.Stats.count = 2
	first.Stats.fail_count = 1
	first.Stats.wait_sum_ns = 12
	first.Stats.wait_max_ns = 8
	first.Stats.hold_sum_ns = 20
	first.Stats.hold_max_ns = 10
	first.Stats.hist[3] = 2

	second := Row{Name: first.Name, Line: first.Line}
	second.Stats.address = 0x2000
	second.Stats.count = 1
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
