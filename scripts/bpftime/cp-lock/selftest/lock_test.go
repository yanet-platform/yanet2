//go:build cp_lock_bpftime

package selftest_test

import (
	"bufio"
	"bytes"
	"context"
	"debug/elf"
	_ "embed"
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

	workload "github.com/yanet-platform/yanet2/scripts/bpftime/cp-lock/selftest"
)

//go:embed lock.go
var lockSource string

// Test_Workload_Child verifies that every critical section stays on one C thread.
func Test_Workload_Child(t *testing.T) {
	if os.Getenv("CP_LOCK_CHILD") == "" {
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

// child starts only a workload owned by this test and returns its command channel.
func child(t *testing.T) (*exec.Cmd, io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "workload")
	data, err := os.ReadFile(os.Args[0])
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(executable, data, 0755))
	if os.Getenv("CP_LOCK_SMOKE") != "" {
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
	command.Env = append(os.Environ(), "CP_LOCK_CHILD=1")
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

// sample rejects incoherent counters, maxima and every cumulative histogram row.
func sample(t *testing.T, output string) float64 {
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
		if histogram[labels] != count || maxima[labels] > sums[labels] || buckets[labels] != 64 {
			t.Fatalf("incoherent row %s", labels)
		}
		require.False(t, (strings.Contains(labels, "cp_lock_test_first") || strings.Contains(labels, "cp_lock_test_second")) && count > 0 && sums[labels] < count*0.001, "real critical section hold bound lost")
		if (strings.Contains(labels, "cp_lock_test_first") || strings.Contains(labels, "cp_lock_test_second")) && count > 0 {
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

// Test_Session_ContinuousRealLock verifies that counters survive reader death and CP survives explicit detach.
func Test_Session_ContinuousRealLock(t *testing.T) {
	helper := os.Getenv("CP_LOCK_HELPER_DIR")
	tool := filepath.Join(helper, "yanet-cp-lock")
	if helper == "" {
		tool = "/usr/bin/yanet-cp-lock"
	}
	if _, err := os.Stat(tool); err != nil {
		if os.Getenv("CP_LOCK_SMOKE") != "" {
			t.Fatal(err)
		}
		t.Skip("build opt-in cp-lock before running self-check")
	}
	runtimeDirectory := filepath.Join(helper, "bpftime")
	if helper == "" {
		runtimeDirectory = "/usr/lib/yanet2/cp-lock/bpftime"
	}
	if _, err := os.Stat(filepath.Join(runtimeDirectory, "libbpftime-agent.so")); err != nil {
		if os.Getenv("CP_LOCK_SMOKE") != "" {
			t.Fatal("installed smoke requires the packaged runtime: ", err)
		}
		t.Skip("prepare the prebuilt bpftime release before self-check")
	}
	if os.Getenv("CP_LOCK_SMOKE") != "" {
		installedSymbols(t)
	}
	t.Setenv("BPFTIME_GLOBAL_SHM_NAME", fmt.Sprintf("cp_lock_test_%d", os.Getpid()))
	name := os.Getenv("BPFTIME_GLOBAL_SHM_NAME")
	t.Cleanup(func() { os.Remove("/dev/shm/" + name); os.Remove("/dev/shm/" + name + ".cp-lock") })
	command, input, scanner := child(t)
	run := func(arguments ...string) string {
		t.Helper()
		output, err := exec.Command(tool, arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
		return string(output)
	}
	setup := func() { run("setup", "--pid", strconv.Itoa(command.Process.Pid)) }
	batch := func(count string) {
		fmt.Fprintln(input, count)
		require.True(t, scanner.Scan() && scanner.Text() == "done", "workload failed")
	}
	report := func() string { return run("report", "--format", "prometheus") }
	setup()
	batch("5")
	first := sample(t, report())
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
	require.Equal(t, first+1, sample(t, report()), "successful trylock did not count acquisition")
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
	if os.Getenv("CP_LOCK_SMOKE") == "" {
		// Offset readers do not consult installed runtime files.
		recordPath := "/dev/shm/" + name + ".cp-lock"
		metadata, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Error(t, exec.Command(tool, "setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace", "--debug-file", "/nonexistent-cp-lock-symbols").Run())
		unchangedReplacement, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Equal(t, metadata, unchangedReplacement, "invalid replacement mutated ownership")
		require.Equal(t, first+3, sample(t, report()), "invalid replacement cleared counters")
		require.NoError(t, os.WriteFile(recordPath, nil, 0640))
		require.Error(t, exec.Command(tool, "stop").Run(), "empty ownership record accepted foreign shm")
		refused, err := exec.Command(tool, "setup", "--pid", strconv.Itoa(command.Process.Pid)).CombinedOutput()
		require.Error(t, err, "unowned resident agent accepted")
		require.Contains(t, string(refused), "unowned resident bpftime agent")
		require.NoError(t, os.WriteFile(recordPath, metadata, 0640))
		preOffset := metadata[:len(metadata)-16]
		require.NoError(t, os.WriteFile(recordPath, preOffset, 0640))
		require.Error(t, exec.Command(tool, "stop").Run(), "stop accepted pre-offset metadata")
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
		require.Equal(t, sample(t, report()), sample(t, report()))
		require.NoError(t, os.Rename(movedRuntime, runtimeDirectory))
		require.Error(t, exec.Command(tool, "setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace").Run(), "mutation accepted incompatible resident runtime")
		after, err := os.ReadFile(recordPath)
		require.NoError(t, err)
		require.Equal(t, older, after, "runtime refusal mutated metadata")
		require.NoError(t, os.WriteFile(recordPath, metadata, 0640))
		setup()
		batch("5")
		second := sample(t, report())
		if second != first+13 {
			t.Fatalf("reuse reset counters: %g", second)
		}
		reader := exec.Command(tool, "report", "--format", "prometheus")
		require.NoError(t, reader.Start())
		time.Sleep(2 * time.Millisecond)
		reader.Process.Kill()
		reader.Wait()
		batch("5")
		require.False(t, sample(t, report()) != second+10, "reader death reset counters")
		fmt.Fprintln(input, "30")
		for range 4 {
			sample(t, report())
		}
		require.True(t, scanner.Scan() && scanner.Text() == "done", "concurrent workload failed")
		segment, err := os.Stat("/dev/shm/" + name)
		require.NoError(t, err)
		run("setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace")
		current, err := os.Stat("/dev/shm/" + name)
		require.NoError(t, err)
		require.True(t, os.SameFile(segment, current), "replacement changed the mapped inode")
		batch("2")
		require.Equal(t, float64(4), sample(t, report()), "replacement retained old counters")

	}
	run("stop")
	if os.Getenv("CP_LOCK_SMOKE") != "" {
		return
	}
	run("stop")
	require.Error(t, exec.Command(tool, "report").Run(), "report accepted stopped session")
	batch("2")
	relativeSetup := exec.Command(tool, "setup", "--pid", strconv.Itoa(command.Process.Pid), "--debug-file", filepath.Base(command.Path))
	relativeSetup.Dir = filepath.Dir(command.Path)
	if output, err := relativeSetup.CombinedOutput(); err != nil {
		t.Fatalf("relative debug setup: %v %s", err, output)
	}
	batch("2")
	require.False(t, sample(t, report()) != 4, "fresh setup retained old counters")
	run("stop")
	if os.Getenv("CP_LOCK_SMOKE") == "" {
		for _, signal := range []os.Signal{os.Interrupt, os.Kill} {
			setup()
			batch("2")
			command.Process.Signal(signal)
			command.Wait()
			command, input, scanner = child(t)
			setup()
			batch("2")
			require.False(t, sample(t, report()) != 4, "restart did not reset owned shm")
			run("stop")
		}
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
		require.Equal(t, float64(4), sample(t, report()), "replaced disk image prevented live reuse")
		require.Error(t, exec.Command(tool, "setup", "--pid", strconv.Itoa(command.Process.Pid), "--replace").Run(), "replacement accepted missing original disk image")
		require.NoError(t, os.Rename(backup, command.Path))
		run("stop")
	}
}

// Test_Launch_PinnedImage verifies that initial exec survives atomic path replacement.
func Test_Launch_PinnedImage(t *testing.T) {
	helper := os.Getenv("CP_LOCK_HELPER_DIR")
	if helper == "" {
		t.Skip("build opt-in cp-lock before launch check")
	}
	path := filepath.Join(t.TempDir(), "launch-image")
	original, err := os.ReadFile(os.Args[0])
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, original, 0755))
	image, err := os.Open(path)
	require.NoError(t, err)
	defer image.Close()
	gate, release, err := os.Pipe()
	require.NoError(t, err)
	defer gate.Close()
	defer release.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(helper, "yanet-cp-lock"), "__launch", "", path, "-test.run=^Test_Workload_Child$")
	command.ExtraFiles = []*os.File{gate, image}
	command.Env = append(os.Environ(), "CP_LOCK_CHILD=1")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	require.NoError(t, command.Start())
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	replacement := filepath.Join(t.TempDir(), "replacement")
	other, err := os.ReadFile("/bin/false")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(replacement, other, 0755))
	require.NoError(t, os.Rename(replacement, path))
	_, err = release.Write([]byte{'x'})
	require.NoError(t, err)
	release.Close()
	require.NoError(t, command.Wait(), "%s", output.String())
	require.Contains(t, output.String(), "ready\n", "replacement executed instead of pinned workload")
}
