//go:build cp_lock_bpftime

package main

import (
	"bufio"
	"debug/dwarf"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// controlFixture acknowledges one request from the test process.
func controlFixture(t *testing.T, pid int) <-chan string {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: fmt.Sprintf("@bpftime-agent-%d", pid), Net: "unix"})
	require.NoError(t, err)
	var group errgroup.Group
	requests := make(chan string, 1)
	t.Cleanup(func() {
		defer listener.Close()
		listener.SetDeadline(time.Now().Add(time.Second))
		require.NoError(t, group.Wait())
	})
	group.Go(func() error {
		connection, err := listener.AcceptUnix()
		if err != nil {
			return err
		}
		defer connection.Close()
		request, err := io.ReadAll(connection)
		if err != nil {
			return err
		}
		requests <- string(request)
		// A rejected peer closes before sending a request.
		_, _ = io.WriteString(connection, "ok\n")
		return nil
	})
	return requests
}

// Test_Control_PeerIdentity verifies that acknowledgements belong to the target.
func Test_Control_PeerIdentity(t *testing.T) {
	for _, tc := range []struct {
		Name string
		PID  int
	}{
		{"target peer", os.Getpid()},
		{"foreign peer", os.Getpid() + 100000000},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			requests := controlFixture(t, tc.PID)
			acknowledged, err := control(tc.PID, "refresh")
			if tc.PID == os.Getpid() {
				require.NoError(t, err)
				require.True(t, acknowledged)
				require.Equal(t, "refresh", <-requests)
			} else {
				require.ErrorContains(t, err, "peer PID")
				require.False(t, acknowledged)
				require.Empty(t, <-requests, "foreign peer received a mutating request")
			}
		})
	}
}

// Test_OpenRecord_MonitoringUID verifies read access across nonroot operators.
func Test_OpenRecord_MonitoringUID(t *testing.T) {
	if path := os.Getenv("CP_LOCK_MONITOR_RECORD"); path != "" {
		file, err := openRecord(path, "report")
		require.NoError(t, err)
		file.Close()
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("separate operator and monitoring UIDs require root")
	}
	file, err := os.CreateTemp("/tmp", "cp-lock-monitor-*")
	require.NoError(t, err)
	file.Close()
	t.Cleanup(func() { os.Remove(file.Name()) })
	require.NoError(t, os.Chmod(file.Name(), 0644))
	require.NoError(t, os.Chown(file.Name(), 65533, -1))
	command := exec.Command("/proc/self/exe", "-test.run=^Test_OpenRecord_MonitoringUID$")
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	command.Env = append(os.Environ(), "CP_LOCK_MONITOR_RECORD="+file.Name())
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

// Test_OpenRecord_Trust verifies that unsafe existing files are never trusted.
func Test_OpenRecord_Trust(t *testing.T) {
	for _, name := range []string{"new", "existing", "writable", "symlink", "hardlink", "fifo", "foreign owner"} {
		for _, command := range []string{"setup", "report", "stop"} {
			t.Run(name+"/"+command, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "record")
				if name != "new" && name != "fifo" {
					require.NoError(t, os.WriteFile(path, []byte("retained"), 0640))
				}
				switch name {
				case "writable":
					require.NoError(t, os.Chmod(path, 0666))
				case "symlink":
					require.NoError(t, os.Rename(path, path+".target"))
					require.NoError(t, os.Symlink(path+".target", path))
				case "hardlink":
					require.NoError(t, os.Link(path, path+".link"))
				case "fifo":
					require.NoError(t, syscall.Mkfifo(path, 0600))
				case "foreign owner":
					if os.Geteuid() != 0 {
						t.Skip("changing file owner requires root")
					}
					require.NoError(t, os.Chown(path, 65534, -1))
				}
				file, err := openRecord(path, command)
				if name == "new" && command != "setup" {
					require.ErrorIs(t, err, os.ErrNotExist)
					return
				}
				if name == "new" || name == "existing" {
					require.NoError(t, err)
					defer file.Close()
					contents, err := io.ReadAll(file)
					require.NoError(t, err)
					if name == "existing" {
						require.Equal(t, "retained", string(contents))
					}
				} else {
					require.Error(t, err)
					require.Nil(t, file)
				}
			})
		}
	}
}

// symbolFixture builds mixed Go/C debug data independently of test-binary stripping.
func symbolFixture(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "fixture.go")
	require.NoError(t, os.WriteFile(source, []byte(`package main
/*
int nativeCaller(void) { return 42; }
*/
import "C"
//go:noinline
func goCaller() int { return int(C.nativeCaller()) }
func main() { println(goCaller()) }
`), 0600))
	image := filepath.Join(directory, "image")
	output, err := exec.Command("go", "build", "-ldflags=-s=false -w=false", "-o", image, source).CombinedOutput()
	require.NoError(t, err, "%s", output)
	debug := image + ".debug"
	output, err = exec.Command("objcopy", "--only-keep-debug", image, debug).CombinedOutput()
	require.NoError(t, err, "%s", output)
	return image, debug
}

// symbolAddress locates a function's first instruction in the debug fixture.
func symbolAddress(t *testing.T, path, name string) uint64 {
	t.Helper()
	image, err := elf.Open(path)
	require.NoError(t, err)
	defer image.Close()
	symbols, err := image.Symbols()
	require.NoError(t, err)
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol.Value
		}
	}
	t.Fatalf("missing fixture symbol: %s", name)
	return 0
}

// Test_SourceLine_MixedDebug verifies that C and Go lines survive split debug packaging.
func Test_SourceLine_MixedDebug(t *testing.T) {
	image, debug := symbolFixture(t)
	cache := map[string]*dwarf.Data{}
	for _, path := range []string{image, debug} {
		for _, tc := range []struct {
			Name string
			Line int
		}{
			{"nativeCaller", 3},
			{"main.goCaller", 7},
		} {
			t.Run(filepath.Base(path)+"/"+tc.Name, func(t *testing.T) {
				line, err := sourceLine(path, symbolAddress(t, debug, tc.Name), cache)
				require.NoError(t, err)
				require.Equal(t, "fixture.go:"+fmt.Sprint(tc.Line), filepath.Base(line))
				_, err = sourceLine(path, 0, cache)
				require.Error(t, err, "unmapped address acquired a source line")
			})
		}
	}
}

// Test_Generation_ReadFailure verifies that only confirmed exit permits reclaim.
func Test_Generation_ReadFailure(t *testing.T) {
	cases := []struct {
		Name      string
		ReadError error
		Gone      bool
		Reject    bool
	}{
		{"permission denied remains live", os.ErrPermission, false, true},
		{"missing stat without ESRCH remains unknown", os.ErrNotExist, false, true},
		{"ESRCH verifies that external exit", os.ErrNotExist, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			value, err := parseStartTime(nil, tc.ReadError, tc.Gone)
			if value != 0 || (err != nil) != tc.Reject {
				t.Fatalf("generation=%d error=%v", value, err)
			}
			if tc.Reject && !errors.Is(err, tc.ReadError) {
				t.Fatal("read failure lost")
			}
		})
	}
}

// Test_Generation_StatParsing verifies that malformed records fail and process-name delimiters preserve generation.
func Test_Generation_StatParsing(t *testing.T) {
	fields := append([]string{"S"}, strings.Fields(strings.Repeat("0 ", 18))...)
	fields = append(fields, "12345")
	cases := []struct {
		Name, Stat string
		Want       uint64
		Reject     bool
	}{
		{"spaces and closing parentheses in comm", "99 (fake name)) " + strings.Join(fields, " "), 12345, false},
		{"truncated stat", "99 (fake) S", 0, true},
		{"missing comm delimiter", "bad", 0, true},
		{"zombie retains generation until task group exits", "99 (fake) Z " + strings.Join(fields[1:], " "), 12345, false},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			value, err := parseStartTime([]byte(tc.Stat), nil, false)
			if value != tc.Want || (err != nil) != tc.Reject {
				t.Fatal(fmt.Sprintf("generation=%d error=%v", value, err))
			}
		})
	}
}

// Test_MapFields_PathSpacing verifies that mapped image names remain exact.
func Test_MapFields_PathSpacing(t *testing.T) {
	fields, path := mapFields("01-02 r-xp 00 08:01 99     /test/image  with\ttab")
	if fields[0] != "01-02" || fields[2] != "00" || path != "/test/image  with\ttab" {
		t.Fatalf("fields=%v path=%q", fields, path)
	}
}

// Test_BuildID_NoteDiscovery verifies that renamed note sections retain build identity.
func Test_BuildID_NoteDiscovery(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "image")
	notes := filepath.Join(t.TempDir(), "notes")
	run := func(arguments ...string) {
		t.Helper()
		if output, err := exec.Command("objcopy", arguments...).CombinedOutput(); err != nil {
			t.Fatalf("objcopy: %v %s", err, output)
		}
	}
	run("--dump-section", ".note.gnu.build-id="+notes, os.Args[0], fixture)
	original, err := os.ReadFile(notes)
	if err != nil || len(original) < 16 {
		t.Fatalf("build-id fixture: %v", err)
	}
	expected := hex.EncodeToString(original[16:])
	run("--rename-section", ".note.gnu.build-id=.note.prototype", fixture)
	prefix := make([]byte, 16)
	binary.LittleEndian.PutUint32(prefix, 4)
	copy(prefix[12:], "XYZ\x00")
	if err := os.WriteFile(notes, append(prefix, original...), 0600); err != nil {
		t.Fatal(err)
	}
	run("--update-section", ".note.prototype="+notes, fixture)
	identity, err := buildID(fixture)
	if err != nil || identity != expected {
		t.Fatalf("build-id=%q expected=%q error=%v", identity, expected, err)
	}
	if err := os.WriteFile(notes, []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	run("--update-section", ".note.prototype="+notes, fixture)
	if _, err := buildID(fixture); err == nil {
		t.Fatal("truncated GNU note accepted")
	}
}

// Test_Setup_MissingResidentSegment verifies that rebinding is refused for a live agent's mapping.
func Test_Setup_MissingResidentSegment(t *testing.T) {
	generation, err := startTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, active := range []int32{0, 1, 2} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing")
			m := Coordinator{Path: path, Record: Record{Magic: recordMagic, PID: int32(os.Getpid()), Start: generation, Active: active}}
			copyText(m.Record.Boot[:], readLine("/proc/sys/kernel/random/boot_id"))
			before := m.Record
			defer func() {
				failure := recover()
				if failure == nil || !strings.Contains(fmt.Sprint(failure), "operator-managed CP exit") {
					t.Fatalf("missing segment: %v", failure)
				}
				if m.Record != before {
					t.Fatal("refusal changed metadata")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("refusal created segment: %v", err)
				}
			}()
			m.setup(os.Getpid(), true, "")
		})
	}
}

// Test_Runtime_ResidentMismatch verifies that an incompatible session cannot be detached.
func Test_Runtime_ResidentMismatch(t *testing.T) {
	generation, err := startTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	m := Coordinator{Record: Record{Magic: recordMagic, PID: int32(os.Getpid()), Start: generation, Active: 1}}
	copyText(m.Record.Boot[:], readLine("/proc/sys/kernel/random/boot_id"))
	copyText(m.Record.Runtime[:], "older-unsupported-runtime")
	before := m.Record
	defer func() {
		failure := recover()
		if failure == nil || !strings.Contains(fmt.Sprint(failure), "resident runtime identity mismatch") || m.Record != before {
			t.Fatalf("resident mismatch: %v", failure)
		}
	}()
	m.residentCompatible()
}

// Test_Runtime_EnvironmentRefusal verifies that kernel and alternate VM modes are refused.
func Test_Runtime_EnvironmentRefusal(t *testing.T) {
	for _, name := range []string{"BPFTIME_RUN_WITH_KERNEL", "BPFTIME_DISABLE_JIT", "BPFTIME_VM_NAME"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "1")
			defer func() {
				if failure := recover(); failure == nil || !strings.Contains(fmt.Sprint(failure), name) {
					t.Fatalf("override accepted: %v", failure)
				}
			}()
			runtimeEnvironment()
		})
	}
}

// Test_Generation_ZombieLeader verifies that surviving threads retain ownership.
func Test_Generation_ZombieLeader(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "threads.c")
	image := strings.TrimSuffix(fixture, ".c")
	if err := os.WriteFile(fixture, []byte(`#include <pthread.h>
#include <unistd.h>
static void *waiter(void *unused) { (void)unused; for (;;) { pause(); } return NULL; }
int main(void) { pthread_t thread; pthread_create(&thread, NULL, waiter, NULL); pthread_exit(NULL); }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(environment("CC", "cc"), "-pthread", fixture, "-o", image).CombinedOutput(); err != nil {
		t.Fatalf("thread fixture: %v %s", err, output)
	}
	command := exec.Command(image)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	for range 100 {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", command.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), ") Z ") {
			generation, err := startTime(command.Process.Pid)
			if err != nil || generation == 0 {
				t.Fatalf("surviving task group classified as exited: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("leader did not become a zombie")
}

// Test_Runtime_TargetEnvironment verifies that targets cannot choose another segment or backend.
func Test_Runtime_TargetEnvironment(t *testing.T) {
	for _, tc := range []struct{ Environment, Message string }{
		{"BPFTIME_GLOBAL_SHM_NAME=another-cp-lock-segment", "shared-memory name differs"},
		{"BPFTIME_RUN_WITH_KERNEL=1", "BPFTIME_RUN_WITH_KERNEL"},
		{"BPFTIME_VM_NAME=ubpf", "BPFTIME_VM_NAME"},
	} {
		t.Run(tc.Environment, func(t *testing.T) {
			command := exec.Command("/bin/sh", "-c", "echo ready; kill -STOP $$")
			command.Env = append(os.Environ(), "BPFTIME_GLOBAL_SHM_NAME=our-cp-lock-segment", tc.Environment)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { command.Process.Kill(); command.Wait() })
			scanner := bufio.NewScanner(output)
			if !scanner.Scan() || scanner.Text() != "ready" {
				t.Fatal("target did not become ready")
			}
			defer func() {
				if failure := recover(); failure == nil || !strings.Contains(fmt.Sprint(failure), tc.Message) {
					t.Fatalf("target runtime mismatch: %v", failure)
				}
			}()
			targetRuntime(command.Process.Pid, "our-cp-lock-segment", false)
		})
	}
}
