//go:build cp_lock_bpftime

package main

/*
#include "adapter.h"
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const recordMagic = 0x43504c4f434b3033
const runtimeLabel = "v0.9.0@sha256:6e315ea561d08e482159385a4775ee6564fc4a6f416f24ae40bfccb176191ed9"
const offsetABI = 0x02000000

type Record struct {
	Magic, Inode, Start, Session uint64
	Offsets                      [3]uint64
	ABI, Schema                  uint32
	PID, Active                  int32
	Boot                         [40]byte
	Build                        [129]byte
	Runtime                      [96]byte
	Binary, Debug                [4096]byte
	Padding                      [7]byte
	MapOffsets                   [2]uint64
}

type Coordinator struct {
	Record                      Record
	File                        *os.File
	Name, Path, Helper, Runtime string
}

func check(good bool, message string) {
	if !good {
		panic(fmt.Errorf("%s", message))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func text(value []byte) string {
	value, _, _ = bytes.Cut(value, []byte{0})
	return string(value)
}

func copyText(destination []byte, value string) {
	check(len(value) < len(destination), "metadata value too long")
	copy(destination, value)
}

func readLine(path string) string {
	data, err := os.ReadFile(path)
	must(err)
	return strings.TrimSuffix(string(data), "\n")
}

func environment(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func (m *Coordinator) inode() uint64 {
	info, err := os.Stat(m.Path)
	if os.IsNotExist(err) {
		return 0
	}
	must(err)
	return info.Sys().(*syscall.Stat_t).Ino
}

func (m *Coordinator) live() bool {
	if m.Record.PID <= 0 || m.Record.Start == 0 || readLine("/proc/sys/kernel/random/boot_id") != text(m.Record.Boot[:]) {
		return false
	}
	generation, err := startTime(int(m.Record.PID))
	must(err)
	return generation == m.Record.Start
}

func (m *Coordinator) save() {
	data := unsafe.Slice((*byte)(unsafe.Pointer(&m.Record)), int(unsafe.Sizeof(m.Record)))
	_, err := m.File.WriteAt(data, 0)
	must(err)
	must(m.File.Sync())
}

func (m *Coordinator) compatible() {
	check(m.Record.ABI == offsetABI && m.Record.Schema == C.CP_LOCK_SCHEMA,
		"unsupported runtime layout/statistics schema; an operator-managed CP restart is required to install this runtime")
}

func (m *Coordinator) residentCompatible() {
	if m.Record.Magic == recordMagic && m.live() && m.Record.Active >= 0 {
		check(text(m.Record.Runtime[:]) == runtimeLabel, "resident runtime identity mismatch; an operator-managed CP restart is required; handlers retained")
	}
}

func (m *Coordinator) owned() {
	check(m.Record.Inode != 0 && m.inode() == m.Record.Inode, "owned shared-memory inode changed or is missing")
}

func (m *Coordinator) reset(name string) {
	_, err := run([]string{m.Helper + "/setup", "--empty"}, "LD_PRELOAD="+m.Runtime+"/libbpftime-syscall-server.so", "BPFTIME_GLOBAL_SHM_NAME="+name)
	must(err)
}

func (m *Coordinator) stop() {
	runtimeEnvironment()
	m.compatible()
	m.owned()
	m.residentCompatible()
	if m.live() {
		mappedBuild(int(m.Record.PID), text(m.Record.Build[:]))
	}
	if m.Record.Active > 0 && m.live() {
		acknowledged, err := control(int(m.Record.PID), "detach")
		check(err == nil && acknowledged, fmt.Sprintf("CP %d did not acknowledge detach; handlers retained", m.Record.PID))
	}
	m.reset(m.Name)
	if m.Record.Active >= 0 {
		m.Record.Active = 0
	}
	m.save()
}

func verifiedBinary(image string) string {
	binary, err := os.Readlink(image)
	must(err)
	mapped, err := os.Stat(image)
	must(err)
	disk, err := os.Stat(binary)
	check(err == nil && os.SameFile(mapped, disk), "target executable deleted/replaced; matching bpftime requires its original on-disk image before setup")
	check(!strings.ContainsAny(binary, " \t\n\r\f\v"), "matching bpftime cannot attach executable paths containing whitespace")
	return binary
}

func symbols(binary, explicit string) (string, string, [3]uint64) {
	identity, err := buildID(binary)
	must(err)
	debug := explicit
	if debug != "" {
		debug, err = filepath.Abs(debug)
		must(err)
	}
	if debug == "" {
		if _, err := lockOffsets(binary, binary); err == nil {
			debug = binary
		}
	}
	if debug == "" {
		debug = "/usr/lib/debug/.build-id/" + identity[:2] + "/" + identity[2:] + ".debug"
	}
	debugIdentity, err := buildID(debug)
	check(err == nil && identity == debugIdentity, "missing/mismatched build-id debug symbols: "+debug)
	offsets, err := lockOffsets(binary, debug)
	must(err)
	return debug, identity, offsets
}

func verifyTarget(pid int, generation uint64, identity string) {
	current, err := startTime(pid)
	must(err)
	check(current != 0 && current == generation, "CP generation changed during setup; handlers retained")
	verifiedBinary(fmt.Sprintf("/proc/%d/exe", pid))
	mappedBuild(pid, identity)
}

func mappedBuild(pid int, identity string) {
	mapped, err := buildID(fmt.Sprintf("/proc/%d/exe", pid))
	check(err == nil && mapped == identity, "CP executable build-id changed; handlers retained")
}

func targetRuntime(pid int, name string, owned bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	must(err)
	entries := strings.Split(string(data), "\x00")
	checkRuntimeFlags(entries)
	targetName := "bpftime_maps_shm"
	for _, entry := range entries {
		if value, found := strings.CutPrefix(entry, "BPFTIME_GLOBAL_SHM_NAME="); found {
			targetName = value
			break
		}
	}
	check(targetName == name, "target bpftime shared-memory name differs; refusing setup")
	if !owned {
		maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
		must(err)
		for _, line := range strings.Split(string(maps), "\n") {
			_, path := mapFields(line)
			check(filepath.Base(strings.TrimSuffix(path, " (deleted)")) != "libbpftime-agent.so", "unowned resident bpftime agent; refusing refresh")
		}
	}
}

// gatedLaunch holds the initial exec until durable probes are ready.
func (m *Coordinator) gatedLaunch(arguments []string, image *os.File) (int, *os.File) {
	input, output, err := os.Pipe()
	must(err)
	defer input.Close()
	executable, err := os.Executable()
	must(err)
	command := exec.Command(executable, append([]string{"__launch", m.Runtime + "/libbpftime-agent.so"}, arguments...)...)
	command.ExtraFiles = []*os.File{input, image}
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	must(command.Start())
	return command.Process.Pid, output
}

func (m *Coordinator) setup(pid int, replace bool, debugFile string, launch []string) {
	runtimeEnvironment()
	exists := m.inode() != 0
	oldLive := m.Record.Magic == recordMagic && m.live()
	resident := oldLive && m.Record.Active >= 0
	check(exists || !resident, "owned shared memory is missing while its CP agent remains resident; an operator-managed CP exit is required before setup")
	m.residentCompatible()
	if exists && oldLive && m.Record.Active == 1 && int(m.Record.PID) == pid && !replace && len(launch) == 0 {
		m.owned()
		m.compatible()
		mappedBuild(pid, text(m.Record.Build[:]))
		check(m.live(), "CP generation changed during reuse; handlers retained")
		fmt.Println("compatible live session reused; counters preserved")
		return
	}

	var binary, image string
	var gate *os.File
	var pinned *os.File
	var generation uint64
	if len(launch) > 0 {
		check(!oldLive, "launch requires no live owned CP")
		resolved, err := exec.LookPath(launch[0])
		must(err)
		binary, err = filepath.EvalSymlinks(resolved)
		must(err)
		binary, err = filepath.Abs(binary)
		must(err)
		check(!strings.ContainsAny(binary, " \t\n\r\f\v"), "matching bpftime cannot attach executable paths containing whitespace")
		launch[0] = binary
		pinned, err = os.Open(binary)
		must(err)
		defer pinned.Close()
		image = fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), pinned.Fd())
	} else {
		var err error
		generation, err = startTime(pid)
		must(err)
		check(generation != 0, "target generation is unavailable")
		image = fmt.Sprintf("/proc/%d/exe", pid)
		binary = verifiedBinary(image)
		targetRuntime(pid, m.Name, resident && int(m.Record.PID) == pid)
	}

	debug, identity, offsets := symbols(image, debugFile)
	if debug == image {
		debug = binary
	}
	if pinned == nil {
		verifyTarget(pid, generation, identity)
	}

	if m.Record.Magic == recordMagic && exists {
		m.owned()
		if !oldLive {
			must(os.Remove(m.Path))
			exists = false
		} else {
			m.compatible()
			check(int(m.Record.PID) == pid && len(launch) == 0, "session belongs to another live CP generation")
			check(identity == text(m.Record.Build[:]), "owned CP executable build-id changed; handlers retained")
			m.stop()
		}
	} else {
		check(!exists, "foreign/unidentified bpftime shared memory; refusing mutation")
	}

	if !exists && m.Record.Magic == recordMagic && m.Record.Inode != 0 {
		temporary := m.Path + ".new-" + strconv.FormatUint(m.Record.Session, 10)
		if info, err := os.Stat(temporary); err == nil && info.Sys().(*syscall.Stat_t).Ino == m.Record.Inode {
			must(os.Remove(temporary))
		}
	}

	if pinned != nil {
		pid, gate = m.gatedLaunch(launch, pinned)
		defer gate.Close()
		var err error
		generation, err = startTime(pid)
		must(err)
		check(generation != 0, "target generation is unavailable")
	}

	m.Record = Record{Magic: recordMagic, ABI: offsetABI, Schema: C.CP_LOCK_SCHEMA, PID: int32(pid), Start: generation, Active: -1, Session: uint64(time.Now().UnixNano()), Offsets: offsets}
	if resident {
		m.Record.Active = 0
	}

	copyText(m.Record.Boot[:], readLine("/proc/sys/kernel/random/boot_id"))
	copyText(m.Record.Binary[:], binary)
	copyText(m.Record.Debug[:], debug)
	copyText(m.Record.Build[:], identity)
	copyText(m.Record.Runtime[:], runtimeLabel)
	if exists {
		m.Record.Inode = m.inode()
	}
	m.save()

	if !exists {
		temporary := m.Name + ".new-" + strconv.FormatUint(m.Record.Session, 10)
		m.reset(temporary)
		info, err := os.Stat("/dev/shm/" + temporary)
		must(err)
		m.Record.Inode = info.Sys().(*syscall.Stat_t).Ino
		m.save()
		must(os.Link("/dev/shm/"+temporary, m.Path))
		must(os.Remove("/dev/shm/" + temporary))
	}

	target := image
	if pinned != nil {
		target = binary
	}
	loader := []string{m.Helper + "/setup", m.Helper + "/cp_lock.bpf.o", strconv.Itoa(pid), target}
	for _, offset := range offsets {
		loader = append(loader, strconv.FormatUint(offset, 10))
	}
	if pinned == nil {
		verifyTarget(pid, generation, identity)
	}

	loaded, err := run(loader, "LD_PRELOAD="+m.Runtime+"/libbpftime-syscall-server.so", "BPFTIME_GLOBAL_SHM_NAME="+m.Name)
	if err != nil {
		m.owned()
		m.reset(m.Name)
		if resident {
			m.Record.Active = 0
		} else {
			m.Record.Active = -1
		}
		m.save()
		panic(fmt.Errorf("probe load failed; partial session cleaned: %w", err))
	}

	for _, line := range strings.Split(string(loaded), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "CP_LOCK_ARRAY_OFFSETS" {
			for idx := range m.Record.MapOffsets {
				value, err := strconv.ParseUint(fields[idx+1], 10, 64)
				must(err)
				m.Record.MapOffsets[idx] = value
			}
		}
	}

	check(m.Record.MapOffsets[0] != 0 && m.Record.MapOffsets[1] != 0, "runtime did not expose ARRAY payload offsets")
	if pinned == nil {
		verifyTarget(pid, generation, identity)
	}

	check(m.live(), "CP generation changed during setup")
	m.Record.Active = 2
	m.save()

	if gate != nil {
		_, err := gate.Write([]byte{'x'})
		must(err)
		must(gate.Close())
	} else if acknowledged, err := control(pid, "refresh shm_open_type=1"); err != nil || !acknowledged {
		check(!resident, "resident runtime refresh failed; handlers retained")
		_, err := run([]string{m.Runtime + "/bpftime", "--install-location", m.Runtime, "attach", strconv.Itoa(pid)})
		check(err == nil, "agent attach failed; handlers retained")
	}

	ready := false
	for range 100 {
		if m.live() {
			if acknowledged, err := control(pid, "refresh shm_open_type=1"); err == nil && acknowledged {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	check(ready && m.live(), "agent did not acknowledge hook installation; handlers retained")
	if pinned != nil {
		expected, err := pinned.Stat()
		must(err)
		actual, err := os.Stat(fmt.Sprintf("/proc/%d/exe", pid))
		must(err)
		check(os.SameFile(expected, actual), "launched executable changed during setup; handlers retained")
	}

	verifyTarget(pid, generation, identity)
	m.Record.Active = 1
	m.save()

	suffix := ""
	if replace {
		suffix = " replaced; counters reset with a collection gap"
	}

	fmt.Printf("session=%d pid=%d generation=%d%s\n", m.Record.Session, pid, m.Record.Start, suffix)
}

type Row struct {
	Stats      C.struct_cp_lock_site_stats
	Name, Line string
}

func quote(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(value)
}

func (m *Coordinator) symbolize(address uint64, maps []byte, cache map[string]*dwarf.Data) (string, string) {
	for _, line := range strings.Split(string(maps), "\n") {
		fields, path := mapFields(line)
		if path == "" {
			continue
		}

		firstText, lastText, good := strings.Cut(fields[0], "-")
		first, firstErr := strconv.ParseUint(firstText, 16, 64)
		last, lastErr := strconv.ParseUint(lastText, 16, 64)
		offset, offsetErr := strconv.ParseUint(fields[2], 16, 64)
		check(good && firstErr == nil && lastErr == nil && offsetErr == nil, "invalid CP maps")
		if address <= first || address > last {
			continue
		}

		path = strings.TrimSuffix(path, " (deleted)")
		image, debug := path, path
		if path == text(m.Record.Binary[:]) {
			image = fmt.Sprintf("/proc/%d/exe", m.Record.PID)
			debug = text(m.Record.Debug[:])
			if debug == text(m.Record.Binary[:]) {
				debug = image
			}
			targetID, err := buildID(image)
			must(err)
			debugID, err := buildID(debug)
			must(err)
			check(targetID == text(m.Record.Build[:]) && debugID == targetID, "live target/debug build-id changed")
		}

		executable, err := elf.Open(image)
		must(err)
		virtual, err := translate(executable, address-first+offset-1, true)
		executable.Close()
		must(err)

		output, err := run([]string{"addr2line", "-f", "-e", debug, fmt.Sprintf("0x%x", virtual)})
		must(err)
		lines := strings.Split(string(output), "\n")
		check(len(lines) >= 2, "invalid addr2line output")
		name := lines[0]
		location, _, _ := strings.Cut(lines[1], " (discriminator ")
		if strings.HasPrefix(location, "??:") {
			if line, err := sourceLine(debug, virtual, cache); err == nil {
				location = line
			}
		}

		if name == "??" {
			name = "unknown"
		}
		return name, location
	}
	return "unknown", "??:?"
}

func mergeSites(rows []Row) []Row {
	sites := map[[2]string]int{}
	merged := make([]Row, 0, len(rows))
	for _, row := range rows {
		key := [2]string{row.Name, row.Line}
		idx, found := sites[key]
		if !found {
			sites[key] = len(merged)
			merged = append(merged, row)
			continue
		}

		stats := &merged[idx].Stats
		stats.count += row.Stats.count
		stats.fail_count += row.Stats.fail_count
		stats.wait_sum_ns += row.Stats.wait_sum_ns
		stats.hold_sum_ns += row.Stats.hold_sum_ns
		stats.wait_max_ns = max(stats.wait_max_ns, row.Stats.wait_max_ns)
		stats.hold_max_ns = max(stats.hold_max_ns, row.Stats.hold_max_ns)
		for bucket := range stats.hist {
			stats.hist[bucket] += row.Stats.hist[bucket]
		}
	}
	return merged
}

func (m *Coordinator) report(prometheus bool) string {
	check(m.Record.Active == 1 && m.live(), "no active session on a live CP generation")
	m.compatible()
	m.owned()

	nativeName := C.CString(m.Name)
	defer C.free(unsafe.Pointer(nativeName))

	statistics := make([]C.struct_cp_lock_site_stats, C.CP_LOCK_MAX_SITES)
	var counts C.struct_cp_lock_counters
	var message [512]C.char
	check(C.cp_lock_snapshot(nativeName, C.uint64_t(m.Record.Inode), (*C.uint64_t)(unsafe.Pointer(&m.Record.MapOffsets[0])), &statistics[0], &counts, &message[0]) == 0, C.GoString(&message[0]))

	maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", m.Record.PID))
	must(err)

	rows := []Row{}
	cache := map[string]*dwarf.Data{}
	for _, stats := range statistics {
		if stats.address == 0 {
			continue
		}
		row := Row{Stats: stats}
		row.Name, row.Line = m.symbolize(uint64(stats.address), maps, cache)
		rows = append(rows, row)
	}

	rows = mergeSites(rows)
	sort.Slice(rows, func(left, right int) bool { return rows[left].Stats.hold_sum_ns > rows[right].Stats.hold_sum_ns })

	var output strings.Builder
	if !prometheus {
		fmt.Fprintf(&output, "pid=%d generation=%d session=%d\nsite count failed wait_sum_ms hold_sum_ms hold_max_ms file:line\n", m.Record.PID, m.Record.Start, m.Record.Session)
	} else {
		for _, metric := range []string{"cp_lock_acquisitions_total counter", "cp_lock_failed_try_total counter", "cp_lock_wait_seconds_total counter", "cp_lock_hold_seconds histogram", "cp_lock_wait_max_seconds gauge", "cp_lock_hold_max_seconds gauge"} {
			fmt.Fprintf(&output, "# TYPE %s\n", metric)
		}
		fmt.Fprintf(&output, "# TYPE cp_lock_session_info gauge\ncp_lock_session_info{pid=\"%d\",generation=\"%d\",boot_id=\"%s\",session=\"%d\",build_id=\"%s\"} 1\n", m.Record.PID, m.Record.Start, text(m.Record.Boot[:]), m.Record.Session, text(m.Record.Build[:]))
		fmt.Fprintf(&output, "# TYPE cp_lock_sample_time_seconds gauge\ncp_lock_sample_time_seconds %.17g\n", float64(time.Now().UnixNano())/1e9)
	}

	comm := readLine(fmt.Sprintf("/proc/%d/comm", m.Record.PID))
	for _, row := range rows {
		stats := row.Stats
		if !prometheus {
			fmt.Fprintf(&output, "%s %d %d %.17g %.17g %.17g %s\n", row.Name, uint64(stats.count), uint64(stats.fail_count), float64(stats.wait_sum_ns)/1e6, float64(stats.hold_sum_ns)/1e6, float64(stats.hold_max_ns)/1e6, row.Line)
			continue
		}
		labels := fmt.Sprintf("pid=\"%d\",site=\"%s\",comm=\"%s\"", m.Record.PID, quote(row.Name+"@"+row.Line), quote(comm))
		metric := func(name string, value float64) { fmt.Fprintf(&output, "%s{%s} %.17g\n", name, labels, value) }
		metric("cp_lock_acquisitions_total", float64(stats.count))
		metric("cp_lock_failed_try_total", float64(stats.fail_count))
		metric("cp_lock_wait_seconds_total", float64(stats.wait_sum_ns)/1e9)
		metric("cp_lock_wait_max_seconds", float64(stats.wait_max_ns)/1e9)
		metric("cp_lock_hold_max_seconds", float64(stats.hold_max_ns)/1e9)
		var cumulative uint64
		for idx, bucket := range stats.hist {
			cumulative += uint64(bucket)
			boundary := "+Inf"
			if idx < 63 {
				boundary = strconv.FormatFloat(float64((uint64(1)<<(idx+1))-1)/1e9, 'g', 17, 64)
			}
			fmt.Fprintf(&output, "cp_lock_hold_seconds_bucket{%s,le=\"%s\"} %d\n", labels, boundary, cumulative)
		}
		metric("cp_lock_hold_seconds_sum", float64(stats.hold_sum_ns)/1e9)
		metric("cp_lock_hold_seconds_count", float64(stats.count))
	}

	for _, counter := range []struct {
		Name  string
		Value uint64
	}{{"drops", uint64(counts.drops)}, {"site_overflow", uint64(counts.overflow)}} {
		if prometheus {
			fmt.Fprintf(&output, "# TYPE cp_lock_%s_total counter\ncp_lock_%s_total %d\n", counter.Name, counter.Name, counter.Value)
		} else {
			name := counter.Name
			if name == "site_overflow" {
				name = "overflow"
			}
			fmt.Fprintf(&output, "%s %d\n", name, counter.Value)
		}
	}

	check(m.live(), "CP exited during report")
	return output.String()
}

func coordinator(command string) (*Coordinator, int) {
	name := environment("BPFTIME_GLOBAL_SHM_NAME", "bpftime_maps_shm")
	check(name != "" && !strings.Contains(name, "/"), "invalid shm name")
	helper := environment("CP_LOCK_HELPER_DIR", "/usr/lib/yanet2/cp-lock")
	m := &Coordinator{Name: name, Path: "/dev/shm/" + name, Helper: helper, Runtime: helper + "/bpftime"}
	flags := syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	if command == "report" {
		flags = syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	}
	if command == "setup" {
		flags |= syscall.O_CREAT
	}

	descriptor, err := syscall.Open(m.Path+".cp-lock", flags, 0640)
	if command == "stop" && err == syscall.ENOENT {
		check(m.inode() == 0, "foreign/unidentified shared memory; refusing stop")
		return m, 0
	}
	must(err)
	m.File = os.NewFile(uintptr(descriptor), m.Path+".cp-lock")

	lock := syscall.LOCK_EX
	if command == "report" {
		lock = syscall.LOCK_SH
	}
	must(syscall.Flock(descriptor, lock))
	check(unsafe.Sizeof(m.Record) == 8552 && unsafe.Offsetof(m.Record.Offsets) == 32 &&
		unsafe.Offsetof(m.Record.ABI) == 56 && unsafe.Offsetof(m.Record.PID) == 64 && unsafe.Offsetof(m.Record.Boot) == 72 &&
		unsafe.Offsetof(m.Record.Build) == 112 && unsafe.Offsetof(m.Record.Runtime) == 241 &&
		unsafe.Offsetof(m.Record.Binary) == 337 && unsafe.Offsetof(m.Record.Debug) == 4433 && unsafe.Offsetof(m.Record.Padding) == 8529 && unsafe.Offsetof(m.Record.MapOffsets) == 8536,
		"ownership record layout mismatch")

	data := unsafe.Slice((*byte)(unsafe.Pointer(&m.Record)), int(unsafe.Sizeof(m.Record)))
	count, err := m.File.ReadAt(data, 0)
	if err != io.EOF {
		must(err)
	}
	check(count == 0 || count == len(data) && m.Record.Magic == recordMagic, "unsupported ownership record; no mutation performed")
	if command == "stop" && count == 0 {
		check(m.inode() == 0, "foreign/unidentified shared memory; refusing stop")
	}
	if count > 0 {
		for _, value := range [][]byte{m.Record.Boot[:], m.Record.Build[:], m.Record.Runtime[:], m.Record.Binary[:], m.Record.Debug[:]} {
			check(bytes.IndexByte(value, 0) >= 0, "unterminated ownership record")
		}
	}

	return m, count
}

func execute() {
	check(len(os.Args) >= 2, "usage: yanet-cp-lock setup --pid PID | setup --launch -- PROGRAM ARGS | report [--format prometheus] [--push URL] | stop")
	command := os.Args[1]
	if command == "__launch" {
		check(len(os.Args) >= 4, "invalid gated launch")
		gate := os.NewFile(3, "launch-gate")
		var token [1]byte
		count, err := gate.Read(token[:])
		gate.Close()
		check(err == nil && count == 1 && token[0] == 'x', "launch gate closed before handoff")
		must(os.Setenv("LD_PRELOAD", os.Args[2]))
		syscall.CloseOnExec(4)
		must(syscall.Exec("/proc/self/fd/4", os.Args[3:], os.Environ()))
	}

	check(command == "setup" || command == "report" || command == "stop", "unknown command")

	options := flag.NewFlagSet(command, flag.ContinueOnError)
	var pid int
	var replace, launch bool
	var debugFile, format, push string
	if command == "setup" {
		options.Func("pid", "", func(value string) error {
			parsed, err := strconv.ParseUint(value, 10, 31)
			if err != nil || parsed == 0 {
				return fmt.Errorf("--pid requires a positive decimal PID")
			}
			pid = int(parsed)
			return nil
		})
		options.BoolVar(&replace, "replace", false, "")
		options.BoolVar(&launch, "launch", false, "")
		options.StringVar(&debugFile, "debug-file", "", "")
	} else if command == "report" {
		options.StringVar(&format, "format", "table", "")
		options.StringVar(&push, "push", "", "")
	}

	must(options.Parse(os.Args[2:]))
	arguments := options.Args()
	if launch {
		check(slices.Contains(os.Args[2:], "--"), "--launch requires -- PROGRAM")
	}

	options.Visit(func(option *flag.Flag) { check(option.Name != "push" || push != "", "--push requires a URL") })
	check(command == "setup" && launch || len(arguments) == 0, "unexpected command arguments")
	if command == "setup" {
		check((pid > 0) != launch && (!launch || len(arguments) > 0), "setup requires one positive PID or launch command")
	}
	check(command != "report" || format == "table" || format == "prometheus", "unknown format")

	m, count := coordinator(command)
	if m.File != nil {
		defer m.File.Close()
	}
	switch command {
	case "setup":
		m.setup(pid, replace, debugFile, arguments)
	case "stop":
		if count > 0 {
			m.stop()
		}
	case "report":
		output := m.report(format == "prometheus" || push != "")
		if push != "" {
			request, err := http.NewRequest(http.MethodPost, push, strings.NewReader(output))
			must(err)
			request.Header.Set("Content-Type", "text/plain")
			response, err := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
			must(err)
			defer response.Body.Close()
			check(response.StatusCode >= 200 && response.StatusCode < 300, "push failed: "+response.Status)
			_, err = io.Copy(io.Discard, response.Body)
			must(err)
		}
		_, err := fmt.Print(output)
		must(err)
	}
}

func main() {
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintln(os.Stderr, "cp-lock:", failure)
			os.Exit(1)
		}
	}()
	execute()
}
