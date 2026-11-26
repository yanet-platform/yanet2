//go:build linux && (amd64 || arm64)

package nativetest

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

const (
	// HelperEnv names a helper binary to run in place of the one the
	// build directory holds.
	HelperEnv = "YANET_NATIVE_DATAPLANE"
	// KeepEnv names a directory where each dataplane's storage is a new
	// file, kept after the test for inspection, in place of anonymous
	// memory.
	KeepEnv = "YANET_NATIVE_DATAPLANE_KEEP"
	// MemfdName is the name the kernel shows for anonymous storage.
	MemfdName = "yanet-native-dataplane"
)

// defaultReplyTimeout bounds the wait for any reply of the helper.
const defaultReplyTimeout = 10 * time.Second

// helperBuildPath is the helper's place in the module's build directory.
const helperBuildPath = "build/tests/native/dataplane-helper"

// shadowGranule is the bytes one shadow byte of AddressSanitizer stands
// for.
const shadowGranule = 8

// reportTimeout bounds the wait for a helper that is writing a sanitizer
// report to finish it and exit; symbolizing a report takes seconds.
const reportTimeout = 60 * time.Second

// reportStart matches the first line of a sanitizer report.
var reportStart = regexp.MustCompile(`==[0-9]+==ERROR: |: runtime error: `)

// sanitizerOptions make any report of the helper's sanitizers halt it and
// end it with a nonzero exit status or a signal, whatever the inherited
// options say: they are appended to those, and the last value of an option
// wins.
var sanitizerOptions = []struct {
	Name  string
	Value string
}{
	{Name: "ASAN_OPTIONS", Value: "halt_on_error=1:detect_leaks=1:exitcode=1"},
	{Name: "UBSAN_OPTIONS", Value: "halt_on_error=1:print_stacktrace=1:exitcode=1"},
	{Name: "LSAN_OPTIONS", Value: "detect_leaks=1:exitcode=1"},
}

// Config is the dataplane a helper runs.
type Config struct {
	// Workers is the worker count; zero means one.
	Workers int
	// CPMemory and DPMemory size the control-plane and dataplane zones;
	// zero means 64 MB and 4 MB.
	CPMemory uint64
	DPMemory uint64
	// Devices names the dataplane's devices, in index order.
	Devices []string
	// Modules, DeviceTypes and ObjectTypes are the types the dataplane
	// loads.
	Modules     []string
	DeviceTypes []string
	ObjectTypes []string
	// ReplyTimeout bounds the wait for each reply of the helper; zero
	// means 10 s. A helper that does not answer in time is killed.
	ReplyTimeout time.Duration
	// Fault makes the helper fail on purpose, for the client's own tests;
	// the fault constants of the protocol say how and when.
	Fault helperproto.Fault
}

// RoundResult holds the frames a round put out and dropped.
type RoundResult struct {
	Output [][]byte
	Drop   [][]byte
}

var (
	// ErrRefused marks a request the helper answered with an error; the
	// error carries the helper's text.
	ErrRefused = errors.New("dataplane helper refused the request")
	// ErrRedZoneMismatch marks a helper whose blocks carry other red zones
	// than this process's.
	ErrRedZoneMismatch = errors.New("dataplane helper red zone differs")
)

// HelperError is a failure of the helper process: what broke, how the
// helper ended and what it wrote to its error output.
type HelperError struct {
	Err error
	// State is how the helper ended, nil while it runs.
	State *os.ProcessState
	// Stderr is the helper's error output.
	Stderr string
}

// Error describes the failure, the helper's end and its error output.
func (m *HelperError) Error() string {
	end := "running"
	if m.State != nil {
		end = m.State.String()
	}
	text := fmt.Sprintf("dataplane helper: %v (%s)", m.Err, end)
	if m.Stderr != "" {
		text += "\nhelper stderr:\n" + m.Stderr
	}
	return text
}

// Unwrap returns what broke.
func (m *HelperError) Unwrap() error {
	return m.Err
}

// Dataplane is the dataplane test harness run by a helper process on
// storage this process maps.
//
// The helper only runs the harness: this process allocates in the
// control-plane zone and publishes, and the helper delivers what it
// publishes and runs rounds. A failure of the helper fails the test.
type Dataplane struct {
	t       testing.TB
	timeout time.Duration
	command *exec.Cmd
	// requests and replies are this process's ends of the helper's input
	// and output.
	requests *os.File
	replies  *os.File
	// encoder and decoder speak the protocol on those ends.
	encoder *gob.Encoder
	decoder *gob.Decoder
	// stderr is the helper's error output, complete once it exited.
	stderr output
	// exited closes when the helper exited and was waited for; the
	// waiter's error is then a failure of that wait other than the
	// helper's exit status.
	exited chan struct{}
	waiter errgroup.Group
	memory []byte
	// cpStart is the storage offset of the control-plane zone.
	cpStart uint64
	storage *zone.Storage
	// abandoned keeps the storage mapped past the end of the test.
	abandoned atomic.Bool

	// mu orders the requests, one at a time.
	mu sync.Mutex
	// failure is the first failure of the helper or of a delivery; every
	// later request returns it and the test fails with it at its end. It
	// is read without the lock, which a request in flight may hold.
	failure atomic.Pointer[error]
}

// output is the error output of a running helper.
type output struct {
	mu   sync.Mutex
	data bytes.Buffer
}

// Write appends the helper's output.
func (m *output) Write(data []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.Write(data)
}

// String returns the output so far.
func (m *output) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.String()
}

// NewDataplane starts a helper running a dataplane of the configuration
// and maps its storage; both go when the test ends.
//
// The helper is the build directory's, found from the module root, or the
// one the environment names. A helper that is missing, does not start or
// lays its blocks out with other red zones than this test fails the test.
func NewDataplane(t testing.TB, config Config) *Dataplane {
	t.Helper()
	path, err := helperPath()
	if err != nil {
		t.Fatalf("find the dataplane helper: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%v", fmt.Errorf("dataplane helper: %w; build it with `meson compile -C build` or `make go-test-prepare`", err))
	}
	if config.Workers == 0 {
		config.Workers = 1
	}
	if config.CPMemory == 0 {
		config.CPMemory = uint64(64 * datasize.MB)
	}
	if config.DPMemory == 0 {
		config.DPMemory = uint64(4 * datasize.MB)
	}
	if config.ReplyTimeout == 0 {
		config.ReplyTimeout = defaultReplyTimeout
	}
	size := config.CPMemory + config.DPMemory
	// The shadow is read a granule at a time from the control-plane zone
	// on.
	if config.DPMemory%shadowGranule != 0 {
		t.Fatalf("a dataplane zone of %d bytes is not a multiple of the %d-byte shadow granule", config.DPMemory, shadowGranule)
	}

	file, kept, err := openStorage(int64(size))
	if err != nil {
		t.Fatalf("dataplane storage: %v", err)
	}
	// The helper and the mapping hold the storage from here on.
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close the dataplane storage: %v", err)
		}
	}()
	if kept != "" {
		t.Logf("dataplane storage kept in %s", kept)
	}

	m := &Dataplane{t: t, timeout: config.ReplyTimeout, exited: make(chan struct{})}
	if err := m.start(path, file); err != nil {
		t.Fatalf("start the dataplane helper %s: %v", path, err)
	}
	t.Cleanup(m.close)

	ready, err := m.exchange("start", helperproto.Options{
		Workers:     config.Workers,
		CPMemory:    config.CPMemory,
		DPMemory:    config.DPMemory,
		Devices:     config.Devices,
		Modules:     config.Modules,
		DeviceTypes: config.DeviceTypes,
		ObjectTypes: config.ObjectTypes,
		Fault:       config.Fault,
	})
	if err != nil {
		// A helper that refused to start exits; its end and its log
		// tell why.
		t.Fatalf("%v", m.record(m.ended(errors.Join(err, m.waitExit()))))
	}
	if uintptr(ready.RedZone) != shmalloc.RedZone {
		t.Fatalf("%v", fmt.Errorf(
			"%w: the helper's is %d bytes and this test's %d; build both with AddressSanitizer or both without",
			ErrRedZoneMismatch, ready.RedZone, shmalloc.RedZone,
		))
	}
	memory, err := unix.Mmap(int(file.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatalf("map the dataplane storage: %v", err)
	}
	m.memory = memory
	storage, err := zone.FromMemory(memory)
	if err != nil {
		t.Fatalf("read the dataplane storage: %v", err)
	}
	m.storage = storage
	m.cpStart = config.DPMemory
	// A fresh mapping holds every block addressable; the free ones are
	// marked before this process takes any.
	if err := markFreeBlocks(storage.Instances[0].Cp, memory); err != nil {
		t.Fatalf("mark the dataplane storage: %v", err)
	}
	return m
}

// helperPath returns the helper the environment names or the one in the
// build directory of the module holding the working directory.
func helperPath() (string, error) {
	if path := os.Getenv(HelperEnv); path != "" {
		return path, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, helperBuildPath), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no module root above the working directory; set %s", HelperEnv)
		}
		dir = parent
	}
}

// openStorage returns zero-filled storage of the size: anonymous memory,
// or a new file the caller is to name in the test log when the
// environment names a directory to keep it in.
func openStorage(size int64) (*os.File, string, error) {
	var file *os.File
	kept := ""
	if dir := os.Getenv(KeepEnv); dir != "" {
		created, err := os.CreateTemp(dir, MemfdName+"-*")
		if err != nil {
			return nil, "", err
		}
		file, kept = created, created.Name()
	} else {
		fd, err := unix.MemfdCreate(MemfdName, unix.MFD_CLOEXEC)
		if err != nil {
			return nil, "", fmt.Errorf("create anonymous storage: %w", err)
		}
		file = os.NewFile(uintptr(fd), "memfd:"+MemfdName)
	}
	if err := file.Truncate(size); err != nil {
		return nil, "", errors.Join(err, file.Close())
	}
	return file, kept, nil
}

// start starts the helper on the storage, its descriptor 3, with pipes for
// its input and output.
func (m *Dataplane) start(path string, storage *os.File) error {
	requestsEnd, requests, err := os.Pipe()
	if err != nil {
		return err
	}
	replies, repliesEnd, err := os.Pipe()
	if err != nil {
		return errors.Join(err, requestsEnd.Close(), requests.Close())
	}
	command := exec.Command(path)
	command.Stdin = requestsEnd
	command.Stdout = repliesEnd
	command.Stderr = &m.stderr
	command.ExtraFiles = []*os.File{storage}
	command.Env = helperEnviron()
	// The helper dies with the thread that starts it and leads a process
	// group of its own, so a kill reaches a symbolizer it started too.
	//
	// The parent-death signal follows that thread, which may end before
	// this process does; the end of the helper's input when this process
	// exits is the backstop. The wait gives up on the helper's error output
	// a while after it exited, keeping what arrived, should a process it
	// started still hold that output.
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	command.WaitDelay = m.timeout
	err = command.Start()
	// The helper holds its own copies of its ends.
	err = errors.Join(err, requestsEnd.Close(), repliesEnd.Close())
	if err != nil {
		if command.Process != nil {
			err = errors.Join(err, command.Process.Kill())
		}
		return errors.Join(err, requests.Close(), replies.Close())
	}
	m.command, m.requests, m.replies = command, requests, replies
	m.encoder, m.decoder = gob.NewEncoder(requests), gob.NewDecoder(replies)
	m.waiter.Go(func() error {
		defer close(m.exited)
		// The exit status is the process state's; only another failure
		// of the wait is kept.
		err := command.Wait()
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil
		}
		return err
	})
	return nil
}

// helperEnviron returns this process's environment with the sanitizer
// options appended.
func helperEnviron() []string {
	environ := os.Environ()
	for _, option := range sanitizerOptions {
		value := option.Value
		if inherited := os.Getenv(option.Name); inherited != "" {
			value = inherited + ":" + value
		}
		environ = append(environ, option.Name+"="+value)
	}
	return environ
}

// Instance returns the dataplane's instance in this process's mapping.
func (m *Dataplane) Instance() *zone.Instance {
	return m.storage.Instances[0]
}

// Deliver hands a generation this process published to the workers, as
// the publication of a dataplane without workers of its own does; it is
// a cpconfig.Deliver.
//
// The helper runs the commit handlers and every worker holds its context
// on return. A failed delivery is kept: the next request returns it and
// the test fails with it at its end.
func (m *Dataplane) Deliver(dp *layout.DpConfig, gen *layout.CpConfigGen) {
	// The failure is kept before the lock goes, so a cleanup that takes
	// the lock finds it.
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.deliverLocked(dp, gen); err != nil {
		m.record(err)
	}
}

// deliverLocked is the delivery for a caller holding the request lock.
func (m *Dataplane) deliverLocked(dp *layout.DpConfig, gen *layout.CpConfigGen) error {
	if dp != m.Instance().Dp {
		return errors.New("deliver: the dataplane zone is not this dataplane's")
	}
	offset := uintptr(unsafe.Pointer(gen)) - uintptr(unsafe.Pointer(&m.memory[0]))
	request := &helperproto.Request{Op: helperproto.OpDeliver, Offset: uint64(offset), Marks: m.marks()}
	if _, err := m.exchangeLocked("deliver", request); err != nil {
		return fmt.Errorf("deliver generation %d: %w", gen.Gen, err)
	}
	return nil
}

// Round runs one round of the frames on a worker, all received on one
// device, and returns the frames it put out and dropped.
//
// A round without frames lets the workers acknowledge the published
// generation and publish the time.
func (m *Dataplane) Round(worker int, device uint16, frames ...[]byte) (*RoundResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reply, err := m.exchangeLocked("round", &helperproto.Request{
		Op:     helperproto.OpRound,
		Worker: worker,
		Device: device,
		Frames: frames,
		Marks:  m.marks(),
	})
	if err != nil {
		return nil, err
	}
	return &RoundResult{Output: reply.Output, Drop: reply.Drop}, nil
}

// marks returns the ranges of the control-plane zone this process's
// AddressSanitizer holds unaddressable, so the dataplane's reads of this
// process's red zones and free blocks are reported; other builds send
// none.
func (m *Dataplane) marks() []helperproto.Range {
	if !sanitized {
		return nil
	}
	return unaddressableRuns(m.memory, m.cpStart)
}

// SetTime sets the time the next rounds run at and publishes it on every
// worker.
func (m *Dataplane) SetTime(now time.Time) error {
	ns := now.UnixNano()
	if ns < 0 {
		return fmt.Errorf("time: %v precedes the epoch", now)
	}
	_, err := m.exchange("time", &helperproto.Request{Op: helperproto.OpTime, Time: ns})
	return err
}

// Abandon keeps the storage mapped past the end of the test, for a test
// that fails while a publication still runs on it; the helper is killed
// then and its end logged.
func (m *Dataplane) Abandon() {
	m.abandoned.Store(true)
}

// exchange sends the options or a request and returns the reply.
//
// An error reply is returned with the helper's text. A helper that breaks
// the exchange is killed and its failure, with its exit status and error
// output, kept and returned.
func (m *Dataplane) exchange(name string, message any) (*helperproto.Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exchangeLocked(name, message)
}

// exchangeLocked is the exchange for a caller holding the request lock.
func (m *Dataplane) exchangeLocked(name string, message any) (*helperproto.Reply, error) {
	if failure := m.failure.Load(); failure != nil {
		return nil, *failure
	}
	reply, err := m.roundTrip(message)
	if err != nil {
		return nil, m.record(m.describe(fmt.Errorf("%s: %w", name, err)))
	}
	if reply.Err != "" {
		return nil, fmt.Errorf("%s: %w: %s", name, ErrRefused, reply.Err)
	}
	return reply, nil
}

// roundTrip writes a message and reads its reply within the reply
// timeout.
func (m *Dataplane) roundTrip(message any) (*helperproto.Reply, error) {
	deadline := time.Now().Add(m.timeout)
	if err := m.requests.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err := m.encoder.Encode(message); err != nil {
		return nil, err
	}
	if err := m.replies.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	reply := &helperproto.Reply{}
	if err := m.decoder.Decode(reply); err != nil {
		return nil, fmt.Errorf("no reply: %w", err)
	}
	return reply, nil
}

// record keeps a failure unless one is kept already and returns the
// first failure kept.
func (m *Dataplane) record(err error) error {
	if m.failure.CompareAndSwap(nil, &err) {
		return err
	}
	return *m.failure.Load()
}

// describe ends the helper and returns the error with how the helper
// ended.
func (m *Dataplane) describe(err error) error {
	return m.helperError(errors.Join(err, m.stop()))
}

// helperError returns the error with how the helper ended, or nil for no
// error.
func (m *Dataplane) helperError(err error) error {
	if err == nil {
		return nil
	}
	state := m.state()
	if state != nil {
		err = errors.Join(err, m.waiter.Wait())
	}
	return &HelperError{Err: err, State: state, Stderr: m.stderr.String()}
}

// ended returns a failure with how the helper ended, unless it carries
// that already.
func (m *Dataplane) ended(err error) error {
	if _, ok := errors.AsType[*HelperError](err); ok {
		return err
	}
	return m.helperError(err)
}

// stop ends the helper; it fails only when the helper does not end.
//
// A helper that has not exited is killed, unless it is writing a
// sanitizer report: it gets the report timeout to finish the report and
// exit first.
func (m *Dataplane) stop() error {
	if m.hasExited() || m.reporting() && m.awaitExit(reportTimeout) {
		return nil
	}
	// The group outlives a helper that exited while a process it started
	// still runs.
	if err := syscall.Kill(-m.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill: %w", err)
	}
	if !m.awaitExit(m.timeout) {
		return fmt.Errorf("no exit within %v of a kill", m.timeout)
	}
	return nil
}

// waitExit waits for the helper's exit within the reply timeout and stops
// it then; it fails when the helper had to be stopped.
func (m *Dataplane) waitExit() error {
	if m.awaitExit(m.timeout) {
		return nil
	}
	return errors.Join(fmt.Errorf("no exit within %v", m.timeout), m.stop())
}

// awaitExit reports whether the helper exits within the timeout.
func (m *Dataplane) awaitExit(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-m.exited:
		return true
	case <-timer.C:
		return false
	}
}

// hasExited reports whether the helper exited.
func (m *Dataplane) hasExited() bool {
	select {
	case <-m.exited:
		return true
	default:
		return false
	}
}

// reporting reports whether the helper started a sanitizer report.
func (m *Dataplane) reporting() bool {
	return reportStart.MatchString(m.stderr.String())
}

// state returns how the helper ended, or nil while it runs.
func (m *Dataplane) state() *os.ProcessState {
	if !m.hasExited() {
		return nil
	}
	return m.command.ProcessState
}

// close ends the helper and unmaps the storage when the test ends, and
// fails the test with any failure of the helper or of a delivery.
//
// A helper still running is asked to quit, so a failure kept while it ran
// comes with how it ended.
func (m *Dataplane) close() {
	if m.abandoned.Load() {
		m.closeAbandoned()
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.unmap()
	failure := m.failure.Load()
	if failure == nil {
		if err := m.quit(); err != nil {
			m.t.Errorf("%v", err)
		}
		return
	}
	if !m.hasExited() {
		// The failure is the one to report; the end comes with it.
		if err := m.quit(); err != nil {
			m.t.Logf("dataplane helper did not quit cleanly: %v", err)
		}
	}
	m.t.Errorf("%v", m.ended(*failure))
}

// quit asks the helper to quit and returns the failure of an unclean end.
func (m *Dataplane) quit() error {
	reply, err := m.roundTrip(&helperproto.Request{Op: helperproto.OpQuit})
	if err != nil {
		return m.describe(fmt.Errorf("quit: %w", err))
	}
	var failures []error
	if reply.Err != "" {
		failures = append(failures, fmt.Errorf("quit: %w: %s", ErrRefused, reply.Err))
	}
	if err := m.waitExit(); err != nil {
		failures = append(failures, err)
	}
	if state := m.state(); state != nil && !state.Success() {
		failures = append(failures, errors.New("quit: the helper ended uncleanly"))
	}
	// The error builder attaches the wait's own failure.
	if m.hasExited() && m.waiter.Wait() != nil {
		failures = append(failures, errors.New("quit: the wait for the helper failed"))
	}
	return m.helperError(errors.Join(failures...))
}

// closeAbandoned kills an abandoned dataplane's helper, so a request still
// in flight fails at once, closes the pipes and keeps the storage mapped.
//
// Any failure kept before the kill fails the test. A request in flight is
// waited for to keep how it ended; its failure fails the test too unless
// it came from the kill alone. A helper that ended otherwise than by the
// kill fails it as well.
func (m *Dataplane) closeAbandoned() {
	before := m.failure.Load()
	if err := m.stop(); err != nil {
		m.t.Errorf("abandoned dataplane: %v", m.helperError(err))
	}
	// The kill ends a request in flight, as its reply timeout would.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t.Logf("abandoned dataplane helper ended: %v", m.state())
	failure := m.failure.Load()
	if failure != before && m.byKill(*failure) {
		failure = before
	}
	switch {
	case failure != nil:
		m.t.Errorf("abandoned dataplane: %v", m.ended(*failure))
	case m.hasExited() && !m.killed():
		m.t.Errorf("abandoned dataplane: %v", m.helperError(errors.New("ended before the kill")))
	}
	if err := errors.Join(m.requests.Close(), m.replies.Close()); err != nil {
		m.t.Errorf("close the abandoned dataplane: %v", err)
	}
}

// byKill reports whether a failure came from the kill alone: an exchange
// with the helper broken by its end, on a helper the kill ended.
func (m *Dataplane) byKill(err error) bool {
	if _, ok := errors.AsType[*HelperError](err); !ok || !m.killed() {
		return false
	}
	broken := []error{io.EOF, io.ErrUnexpectedEOF, syscall.EPIPE, os.ErrClosed}
	return slices.ContainsFunc(broken, func(target error) bool {
		return errors.Is(err, target)
	})
}

// killed reports whether the helper exited by the kill signal.
func (m *Dataplane) killed() bool {
	state := m.state()
	if state == nil {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

// unmap closes this process's ends of the pipes and unmaps the storage.
func (m *Dataplane) unmap() {
	err := errors.Join(m.requests.Close(), m.replies.Close())
	if m.storage != nil {
		err = errors.Join(err, m.storage.Close())
	}
	if m.memory != nil {
		// The allocator of this process poisoned the free blocks it put
		// back; a later mapping at these addresses must start clean.
		unpoison(m.memory)
		err = errors.Join(err, unix.Munmap(m.memory))
		m.memory = nil
	}
	if err != nil {
		m.t.Errorf("close the dataplane: %v", err)
	}
}
