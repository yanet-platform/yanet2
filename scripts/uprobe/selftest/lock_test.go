//go:build yanet_uprobe

package selftest_test

import (
	"bufio"
	"bytes"
	"debug/elf"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	workload "github.com/yanet-platform/yanet2/scripts/uprobe/selftest"
)

//go:embed lock.go
var lockSource string

// Test_Workload_Child verifies that every critical section stays on one C
// thread.
func Test_Workload_Child(t *testing.T) {
	if os.Getenv("YANET_UPROBE_CHILD") == "" {
		return
	}
	workload.PermitInjection()
	commands := bufio.NewScanner(os.Stdin)
	fmt.Println("ready")
	for commands.Scan() {
		if commands.Text() == "try" {
			workload.Try()
		} else if commands.Text() == "failed" {
			workload.Failed()
		} else if commands.Text() == "total" {
			workload.Total()
		} else if commands.Text() == "contend" {
			workload.Contend()
		} else if commands.Text() == "release" {
			workload.Release()
		} else {
			count, err := strconv.Atoi(commands.Text())
			if err != nil {
				os.Exit(2)
			}
			workload.Batch(count)
		}
		fmt.Println("done")
	}
	os.Exit(0)
}

// child starts only a workload owned by this test and returns its command
// channel.
func child(t *testing.T) (*exec.Cmd, io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "workload")
	data, err := os.ReadFile(os.Args[0])
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(executable, data, 0755))
	if os.Getenv("YANET_UPROBE_SMOKE") != "" {
		image, err := elf.Open(executable)
		require.NoError(t, err)
		note, err := image.Section(".note.gnu.build-id").Data()
		image.Close()
		require.False(t, err != nil || len(note) < 16, "workload build-id missing")
		identity := hex.EncodeToString(note[16:])
		debug := "/usr/lib/debug/.build-id/" + identity[:2] + "/" + identity[2:] + ".debug"
		require.NoError(t, os.MkdirAll(filepath.Dir(debug), 0755))
		if output, err := exec.Command("objcopy", "--only-keep-debug", executable, debug).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, output)
		}
		if output, err := exec.Command("strip", "--strip-all", executable).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, output)
		}
	}
	command := exec.Command(executable, "-test.run=^Test_Workload_Child$")
	command.Env = append(os.Environ(), "YANET_UPROBE_CHILD=1")
	input, err := command.StdinPipe()
	require.NoError(t, err)
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	t.Cleanup(func() { input.Close(); command.Process.Kill(); command.Wait() })
	scanner := bufio.NewScanner(output)
	require.True(t, scanner.Scan() && scanner.Text() == "ready", "workload did not start")
	return command, input, scanner
}

// sample enforces cross-field bounds only for completed workloads.
func sample(t *testing.T, output string, settled bool) float64 {
	t.Helper()
	require.NotContains(t, output, "address=", "process addresses must not identify metric series")
	require.NotRegexp(t, `site="[^"\n]*@0x`, output)
	buckets := map[string]int{}
	counts, sums, maxima, histogram := map[string]float64{}, map[string]float64{}, map[string]float64{}, map[string]float64{}
	histogramCounts, waitMaxima := map[string]float64{}, map[string]float64{}
	require.Contains(t, output, "# TYPE cp_lock_hold_seconds histogram\n")
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "{") {
			continue
		}
		name, remainder, _ := strings.Cut(line, "{")
		labels, number, _ := strings.Cut(remainder, "} ")
		value, err := strconv.ParseFloat(number, 64)
		require.NoError(t, err)
		switch name {
		case "cp_lock_acquisitions_total":
			require.NotContains(t, counts, labels, "duplicate source site")
			counts[labels] = value
		case "cp_lock_hold_seconds_sum":
			sums[labels] = value
		case "cp_lock_hold_seconds_count":
			histogramCounts[labels] = value
		case "cp_lock_wait_max_seconds":
			waitMaxima[labels] = value
		case "cp_lock_hold_max_seconds":
			maxima[labels] = value
		case "cp_lock_hold_seconds_bucket":
			base, boundary, _ := strings.Cut(labels, ",le=")
			position := buckets[base]
			buckets[base]++
			if position == 63 {
				require.False(t, boundary != "\"+Inf\"", "final histogram boundary missing")
			} else {
				bound, err := strconv.ParseFloat(strings.Trim(boundary, "\""), 64)
				expected := float64((uint64(1)<<(position+1))-1) / 1e9
				require.False(t, err != nil || bound != expected, "log2 inclusive boundary changed")
			}
			if position < 19 && (strings.Contains(base, "cp_lock_test_first") || strings.Contains(base, "cp_lock_test_second")) {
				require.Zero(t, value, "1ms holder appeared in a sub-millisecond bucket")
			}
			require.False(t, value < histogram[base], "histogram decreases")
			histogram[base] = value
		}
	}
	var total float64
	for labels, count := range counts {
		maximum, present := maxima[labels]
		require.True(t, present, "hold maximum missing")
		_, present = waitMaxima[labels]
		require.True(t, present, "wait maximum missing")
		histogramCount, present := histogramCounts[labels]
		require.True(t, present, "histogram count missing")
		require.Equal(t, count, histogramCount, "histogram count disagrees with acquisitions")
		if histogram[labels] != count || settled && maxima[labels] > sums[labels] || buckets[labels] != 64 {
			t.Fatalf("incoherent row %s", labels)
		}
		require.False(t, settled && (strings.Contains(labels, "cp_lock_test_first") || strings.Contains(labels, "cp_lock_test_second")) && count > 0 && sums[labels] < count*0.001, "real critical section hold bound lost")
		if settled && (strings.Contains(labels, "cp_lock_test_first") || strings.Contains(labels, "cp_lock_test_second")) && count > 0 {
			require.GreaterOrEqual(t, maximum, 0.001, "real hold maximum lower bound lost")
		}
		total += count
	}
	return total
}

// installedSymbols checks that the stripped package has matching lock symbols.
func installedSymbols(t *testing.T) {
	t.Helper()
	image, err := elf.Open("/usr/bin/yanet-controlplane")
	require.NoError(t, err)
	defer image.Close()
	note, err := image.Section(".note.gnu.build-id").Data()
	require.False(t, err != nil || len(note) < 16, "target build-id missing")
	identity := hex.EncodeToString(note[16:])
	debug, err := elf.Open("/usr/lib/debug/.build-id/" + identity[:2] + "/" + identity[2:] + ".debug")
	require.NoError(t, err)
	defer debug.Close()
	other, err := debug.Section(".note.gnu.build-id").Data()
	require.False(t, err != nil || string(note) != string(other), "target/debug build-id mismatch")
	symbols, err := debug.Symbols()
	require.NoError(t, err)
	count := 0
	for _, symbol := range symbols {
		if symbol.Name == "cp_config_lock" || symbol.Name == "cp_config_try_lock" || symbol.Name == "cp_config_unlock" {
			count++
		}
	}
	require.False(t, count != 3, "packaged target lock symbols missing")
}

// Test_Session_ContinuousRealLock verifies that counters survive reader
// death and CP survives explicit detach.
func Test_Session_ContinuousRealLock(t *testing.T) {
	helper := os.Getenv("YANET_UPROBE_HELPER_DIR")
	tool := filepath.Join(helper, "yanet-uprobe")
	if helper == "" {
		tool = "/usr/bin/yanet-uprobe"
	}
	if _, err := os.Stat(tool); err != nil {
		if os.Getenv("YANET_UPROBE_SMOKE") != "" {
			t.Fatal(err)
		}
		t.Skip("build opt-in yanet-uprobe before running self-check")
	}
	runtimeDirectory := filepath.Join(helper, "bpftime")
	if helper == "" {
		runtimeDirectory = "/usr/lib/yanet2/uprobe/bpftime"
	}
	if _, err := os.Stat(filepath.Join(runtimeDirectory, "libbpftime-agent.so")); err != nil {
		if os.Getenv("YANET_UPROBE_SMOKE") != "" {
			t.Fatal("installed smoke requires the packaged runtime: ", err)
		}
		t.Skip("prepare the prebuilt bpftime release before self-check")
	}
	if os.Getenv("YANET_UPROBE_SMOKE") != "" {
		installedSymbols(t)
	}
	t.Setenv("BPFTIME_GLOBAL_SHM_NAME", fmt.Sprintf("cp_lock_test_%d", os.Getpid()))
	name := os.Getenv("BPFTIME_GLOBAL_SHM_NAME")
	t.Cleanup(func() { os.Remove("/dev/shm/" + name); os.Remove("/dev/shm/" + name + ".uprobe") })
	command, input, scanner := child(t)
	uprobe := func(arguments ...string) *exec.Cmd {
		return exec.Command(tool, append([]string{"cp-lock"}, arguments...)...)
	}
	run := func(arguments ...string) string {
		t.Helper()
		output, err := uprobe(arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
		return string(output)
	}
	for _, command := range []string{"setup", "report", "stop"} {
		run(command, "--help")
	}
	require.Error(t, uprobe("setup", "--launch", "--", "/bin/true").Run())
	require.Error(t, uprobe("__launch").Run())
	setup := func() { run("setup", "--pid", strconv.Itoa(command.Process.Pid)) }
	batch := func(count string) {
		fmt.Fprintln(input, count)
		require.True(t, scanner.Scan() && scanner.Text() == "done", "workload failed")
	}
	report := func() string { return run("report", "--format", "prometheus") }
	if os.Getenv("YANET_UPROBE_SMOKE") == "" {
		helperDirectory := helper
		if helperDirectory == "" {
			helperDirectory = "/usr/lib/yanet2/uprobe"
		}
		failingHelper := t.TempDir()
		failingRuntime := filepath.Join(failingHelper, "bpftime")
		require.NoError(t, os.Mkdir(failingRuntime, 0755))
		for _, file := range []string{"libbpftime-agent.so", "libbpftime-syscall-server.so", "BUILD"} {
			require.NoError(t, os.Symlink(filepath.Join(runtimeDirectory, file), filepath.Join(failingRuntime, file)))
		}
		for _, file := range []string{"setup", "cp_lock.bpf.o"} {
			require.NoError(t, os.Symlink(filepath.Join(helperDirectory, file), filepath.Join(failingHelper, file)))
		}
		require.NoError(t, os.WriteFile(filepath.Join(failingRuntime, "bpftime"), []byte("#!/bin/sh\nexit 1\n"), 0755))
		for _, recovery := range []string{"stop", "replace"} {
			failedAttach := uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid))
			failedAttach.Env = append(os.Environ(), "YANET_UPROBE_HELPER_DIR="+failingHelper)
			require.Error(t, failedAttach.Run(), "failed injection was accepted")
			if recovery == "stop" {
				run("stop")
			} else {
				run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace")
				batch("2")
				require.Equal(t, float64(4), sample(t, report(), true), "failed attach could not be replaced")
				run("stop")
			}
		}
	}
	setup()
	batch("5")
	first := sample(t, report(), true)
	require.Equal(t, float64(10), first, "completed real acquisitions")
	require.True(t, strings.Contains(report(), "cp_lock_test_first") && strings.Contains(report(), "cp_lock_test_second"), "named sites missing")
	for _, name := range []string{"cp_lock_test_first", "cp_lock_test_second"} {
		inside := false
		var expected int
		for idx, line := range strings.Split(lockSource, "\n") {
			if strings.HasPrefix(line, name+"(") {
				inside = true
			}
			if inside && strings.Contains(line, "cp_config_lock(&configuration)") {
				expected = idx + 1
				break
			}
		}
		require.NotZero(t, expected, "fixture acquisition line missing")
		require.Regexp(t, fmt.Sprintf(`site="%s@[^"\n]*lock\.go:%d"`, name, expected), report())
	}
	require.NotContains(t, run("report"), "@0x", "raw addresses must not appear in the table")
	batch("try")
	require.Equal(t, first+1, sample(t, report(), true), "successful trylock did not count acquisition")
	batch("failed")
	require.True(t, strings.Contains(report(), "cp_lock_failed_try_total{pid="), "failed try missing")
	failed := false
	for _, line := range strings.Split(report(), "\n") {
		if strings.HasPrefix(line, "cp_lock_failed_try_total{") && strings.Contains(line, "cp_lock_test_try") && strings.HasSuffix(line, "} 1") {
			failed = true
		}
	}
	require.True(t, failed, "failed try did not count exactly one failed acquisition")
	waited, waitMaximum := false, false
	for _, line := range strings.Split(report(), "\n") {
		if (strings.HasPrefix(line, "cp_lock_wait_seconds_total{") || strings.HasPrefix(line, "cp_lock_wait_max_seconds{")) && strings.Contains(line, "cp_lock_test_second") {
			_, number, _ := strings.Cut(line, "} ")
			value, err := strconv.ParseFloat(number, 64)
			if strings.HasPrefix(line, "cp_lock_wait_max_seconds{") {
				waitMaximum = err == nil && value >= 0.010
			} else {
				waited = err == nil && value >= 0.010
			}
		}
	}
	require.True(t, waited && waitMaximum, "contended real lock wait sum/maximum lower bound lost")
	if os.Getenv("YANET_UPROBE_SMOKE") == "" {
		debug := filepath.Join(t.TempDir(), "longer-matching-debug-image")
		image, err := os.ReadFile(command.Path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(debug, image, 0600))
		run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--debug-file", debug)
		require.NoError(t, os.Remove(debug))
		require.Error(t, uprobe("report").Run(), "removed symbols remained readable")
		run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--debug-file", command.Path)
		require.Equal(t, first+3, sample(t, report(), true), "updating symbols reset counters")
		recordPath := "/dev/shm/" + name + ".uprobe"
		before, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		for _, invalid := range []string{"/nonexistent-cp-lock-symbols", "/bin/false"} {
			require.Error(t, uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid), "--debug-file", invalid).Run())
			after, err := os.ReadFile(recordPath)
			require.NoError(t, err)
			require.Equal(t, before, after, "invalid symbols changed the live session")
		}

		// Offset readers do not consult installed runtime files.
		metadata, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		temporary := "/dev/shm/" + name + ".new-" + strconv.FormatUint(binary.NativeEndian.Uint64(metadata[24:32]), 10)
		require.NoError(t, os.Link("/dev/shm/"+name, temporary))
		t.Cleanup(func() { os.Remove(temporary) })
		setup()
		require.NoFileExists(t, temporary, "reuse left an interrupted publication link")
		require.Equal(t, first+3, sample(t, report(), true), "temporary-link cleanup reset counters")
		require.Error(t, uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace", "--debug-file", "/nonexistent-cp-lock-symbols").Run())
		unchangedReplacement, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Equal(t, metadata, unchangedReplacement, "invalid replacement mutated ownership")
		require.Equal(t, first+3, sample(t, report(), true), "invalid replacement cleared counters")
		require.NoError(t, os.WriteFile(recordPath, nil, 0640))
		require.Error(t, uprobe("stop").Run(), "empty ownership record accepted foreign shm")
		refused, err := uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid)).CombinedOutput()
		require.Error(t, err, "unowned resident agent accepted")
		require.Contains(t, string(refused), "unowned resident bpftime agent")
		require.NoError(t, os.WriteFile(recordPath, metadata, 0640))
		preOffset := metadata[:len(metadata)-16]
		require.NoError(t, os.WriteFile(recordPath, preOffset, 0640))
		require.Error(t, uprobe("stop").Run(), "stop accepted pre-offset metadata")
		unchanged, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Equal(t, preOffset, unchanged, "unsupported record was mutated")
		require.NoError(t, os.WriteFile(recordPath, metadata, 0640))
		older := bytes.Clone(metadata)
		clear(older[241:337])
		copy(older[241:337], "older-offset-runtime")
		require.NoError(t, os.WriteFile(recordPath, older, 0640))
		movedRuntime := runtimeDirectory + ".reader-test-" + strconv.Itoa(os.Getpid())
		require.NoError(t, os.Rename(runtimeDirectory, movedRuntime))
		defer func() {
			if _, err := os.Stat(movedRuntime); err == nil {
				os.Rename(movedRuntime, runtimeDirectory)
			}
		}()
		require.Equal(t, sample(t, report(), true), sample(t, report(), true))
		require.NoError(t, os.Rename(movedRuntime, runtimeDirectory))
		require.Error(t, uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace").Run(), "mutation accepted incompatible resident runtime")
		after, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Equal(t, older, after, "runtime refusal mutated metadata")
		require.NoError(t, os.WriteFile(recordPath, metadata, 0640))
		setup()
		batch("5")
		second := sample(t, report(), true)
		if second != first+13 {
			t.Fatalf("reuse reset counters: %g", second)
		}
		reader := uprobe("report", "--format", "prometheus")
		require.NoError(t, reader.Start())
		time.Sleep(2 * time.Millisecond)
		reader.Process.Kill()
		reader.Wait()
		batch("5")
		require.False(t, sample(t, report(), true) != second+10, "reader death reset counters")
		fmt.Fprintln(input, "30")
		for range 4 {
			sample(t, report(), false)
		}
		require.True(t, scanner.Scan() && scanner.Text() == "done", "concurrent workload failed")
		require.Equal(t, second+70, sample(t, report(), true), "completed workload did not converge")
		segment, err := os.Stat("/dev/shm/" + name)
		require.NoError(t, err)
		run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace")
		current, err := os.Stat("/dev/shm/" + name)
		require.NoError(t, err)
		require.True(t, os.SameFile(segment, current), "replacement changed the mapped inode")
		batch("2")
		require.Equal(t, float64(4), sample(t, report(), true), "replacement retained old counters")
		previous, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		binary.NativeEndian.PutUint32(previous[60:64], 3)
		require.NoError(t, os.WriteFile(recordPath, previous, 0640))
		require.Error(t, uprobe("report").Run(), "previous schema accepted as current statistics")
		require.Error(t, uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid)).Run(), "previous probes reused")
		run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace")
		batch("2")
		require.Equal(t, float64(4), sample(t, report(), true), "schema upgrade retained old counters")
	}
	run("stop")
	if os.Getenv("YANET_UPROBE_SMOKE") != "" {
		return
	}
	run("stop")
	require.Error(t, uprobe("report").Run(), "report accepted stopped session")
	batch("2")
	relativeSetup := uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid), "--debug-file", filepath.Base(command.Path))
	relativeSetup.Dir = filepath.Dir(command.Path)
	if output, err := relativeSetup.CombinedOutput(); err != nil {
		t.Fatalf("relative debug setup: %v %s", err, output)
	}
	batch("2")
	require.False(t, sample(t, report(), true) != 4, "fresh setup retained old counters")
	run("stop")
	setup()
	batch("total")
	maxima := map[string]float64{}
	for _, line := range strings.Split(report(), "\n") {
		if !strings.Contains(line, `site="cp_lock_test_second@`) {
			continue
		}
		metric, _, _ := strings.Cut(line, "{")
		if metric != "cp_lock_wait_max_seconds" && metric != "cp_lock_hold_max_seconds" && metric != "cp_lock_total_max_seconds" {
			continue
		}
		_, number, _ := strings.Cut(line, "} ")
		value, err := strconv.ParseFloat(number, 64)
		require.NoError(t, err)
		maxima[metric] = value
	}
	require.Len(t, maxima, 3, "per-call maxima missing")
	waitMaximumSeconds := maxima["cp_lock_wait_max_seconds"]
	holdMaximumSeconds := maxima["cp_lock_hold_max_seconds"]
	totalMaximumSeconds := maxima["cp_lock_total_max_seconds"]
	require.GreaterOrEqual(t, waitMaximumSeconds, 0.010)
	require.GreaterOrEqual(t, holdMaximumSeconds, 0.100)
	require.GreaterOrEqual(t, totalMaximumSeconds, max(waitMaximumSeconds, holdMaximumSeconds))
	require.Less(t, totalMaximumSeconds, waitMaximumSeconds+holdMaximumSeconds, "total added maxima from different calls")
	run("stop")

	// A hold over a second with callers spinning on it must stand out
	// against millisecond baseline holds, as the 7.8 s incident hold would have.
	value := func(output, series string) float64 {
		t.Helper()
		for _, line := range strings.Split(output, "\n") {
			if number, found := strings.CutPrefix(line, series+" "); found {
				parsed, err := strconv.ParseFloat(number, 64)
				require.NoError(t, err)
				return parsed
			}
		}
		t.Fatalf("series %s missing", series)
		return 0
	}
	siteValue := func(output, metric, site string) float64 {
		t.Helper()
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(line, metric+"{") && strings.Contains(line, `site="`+site+"@") {
				_, number, _ := strings.Cut(line, "} ")
				parsed, err := strconv.ParseFloat(number, 64)
				require.NoError(t, err)
				return parsed
			}
		}
		t.Fatalf("%s for %s missing", metric, site)
		return 0
	}
	setup()
	batch("5")
	idleThreads := value(report(), "cp_lock_process_threads")
	batch("contend")
	contended := time.Now()
	waiters := value(report(), "cp_lock_waiters")
	for deadline := contended.Add(5 * time.Second); waiters != workload.ContentionWaiters && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		waiters = value(report(), "cp_lock_waiters")
	}
	require.EqualValues(t, workload.ContentionWaiters, waiters, "waiter gauge missed spinning callers")
	// The holder and waiters are extra threads; Go keeps its idle threads.
	require.GreaterOrEqual(t, value(report(), "cp_lock_process_threads"), idleThreads+workload.ContentionWaiters+1, "thread gauge missed contending threads")
	// Every waiter is spinning by now, so its wait outlasts the remaining hold.
	time.Sleep(max(time.Until(contended.Add(1200*time.Millisecond)), time.Second))
	batch("release")
	contention := report()
	require.Zero(t, value(contention, "cp_lock_waiters"), "waiter gauge leaked after release")
	require.EqualValues(t, workload.ContentionWaiters, siteValue(contention, "cp_lock_acquisitions_total", "cp_lock_test_wait"))
	require.GreaterOrEqual(t, siteValue(contention, "cp_lock_wait_max_seconds", "cp_lock_test_wait"), 1.0)
	longHold := siteValue(contention, "cp_lock_hold_max_seconds", "cp_lock_test_hold")
	require.GreaterOrEqual(t, longHold, 1.2)
	require.Greater(t, longHold, 10*siteValue(contention, "cp_lock_hold_max_seconds", "cp_lock_test_first"), "long hold indistinguishable from baseline")
	for _, line := range strings.Split(contention, "\n") {
		if !strings.HasPrefix(line, "cp_lock_hold_seconds_bucket{") || !strings.Contains(line, `site="cp_lock_test_hold@`) {
			continue
		}
		_, boundary, _ := strings.Cut(line, `le="`)
		boundary, number, _ := strings.Cut(boundary, `"} `)
		if boundary != "+Inf" {
			bound, err := strconv.ParseFloat(boundary, 64)
			require.NoError(t, err)
			if bound >= 1.0 {
				continue
			}
		}
		expected := "0"
		if boundary == "+Inf" {
			expected = "1"
		}
		require.Equal(t, expected, number, "long hold landed in a sub-second bucket: %s", line)
	}
	run("stop")
	if os.Getenv("YANET_UPROBE_SMOKE") == "" {
		for _, signal := range []os.Signal{os.Interrupt, os.Kill} {
			setup()
			batch("2")
			command.Process.Signal(signal)
			command.Wait()
			command, input, scanner = child(t)
			setup()
			batch("2")
			require.False(t, sample(t, report(), true) != 4, "restart did not reset owned shm")
			run("stop")
		}
		setup()
		failingHelper := t.TempDir()
		require.NoError(t, os.Symlink(runtimeDirectory, filepath.Join(failingHelper, "bpftime")))
		require.NoError(t, os.WriteFile(filepath.Join(failingHelper, "setup"), []byte("#!/bin/sh\nexit 1\n"), 0755))
		failedStop := uprobe("stop")
		failedStop.Env = append(os.Environ(), "YANET_UPROBE_HELPER_DIR="+failingHelper)
		require.Error(t, failedStop.Run(), "reset failure was accepted")
		setup()
		batch("2")
		require.Equal(t, float64(4), sample(t, report(), true), "partial stop was reused instead of recovered")
		run("stop")
		setup()
		batch("2")
		backup := command.Path + ".original"
		require.NoError(t, os.Link(command.Path, backup))
		replacement, err := os.ReadFile("/bin/false")
		require.NoError(t, err)
		temporary := command.Path + ".replacement"
		require.NoError(t, os.WriteFile(temporary, replacement, 0755))
		require.NoError(t, os.Rename(temporary, command.Path))
		setup()
		require.Equal(t, float64(4), sample(t, report(), true), "replaced disk image prevented live reuse")
		require.Error(t, uprobe("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace").Run(), "replacement accepted missing original disk image")
		require.NoError(t, os.Rename(backup, command.Path))
		run("stop")
	}
}
