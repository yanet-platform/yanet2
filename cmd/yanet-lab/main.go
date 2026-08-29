package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

const defaultSession = "default"

const labBusyError = "lab is busy"

var errLabBusy = errors.New(labBusyError)

const (
	statusReady    = "READY"
	statusNotReady = "NOT_READY"
)

const (
	supervisorRequestTimeout  = 5 * time.Second
	supervisorStatusTimeout   = 45 * time.Second
	supervisorExecTimeout     = 5 * time.Minute
	supervisorManifestTimeout = 10 * time.Minute
	supervisorResetTimeout    = 2 * time.Minute
	supervisorShutdownTimeout = 2 * time.Minute
	maxRequestSize            = 1 << 20 // 1 MiB
)

// sunPathLimit is the platform-specific maximum length of the sockaddr_un
// path field (104 on darwin, 108 on linux). Subtract slack for the prefix
// `/tmp/yanet2-lab-<uid>/<root-digest>/` plus the trailing
// `/supervisor.sock` so the listener never fails with an opaque bind error.
const sunPathLimit = 104
const sunPathSlack = len("/supervisor.sock") + 1

// sshKeygenTimeout bounds the time we wait for ssh-keygen to produce a
// fresh ed25519 keypair. ssh-keygen with -N "" should not block past a
// second on any healthy host; the cap defends against a wedged binary
// blocking up/serve indefinitely.
const sshKeygenTimeout = 30 * time.Second

// maxSessionNameSlack covers the fixed portion of the runtime path that
// the session name sits inside: "/tmp/yanet2-lab-" (15) + uid (up to 10
// digits) + "/" + 12-char root digest + "/" = 39, rounded to 40 to leave
// a one-byte headroom under the sun_path cap.
const maxSessionNameSlack = 40

// maxSessionNameLen caps the session name length so the supervisor.sock
// path fits in sun_path regardless of the project root's absolute length.
// It is re-checked in validSessionName so any caller — including those
// that bypass ensureSessionDirectory — gets the rejection up front.
const maxSessionNameLen = sunPathLimit - sunPathSlack - maxSessionNameSlack

// supervisorProtocolVersion is stamped on every Supervisor reply so a stale
// Supervisor process forked by an older CLI is detected and replaced.
// Version 3 folded the pre-ready busy reply into the single-flight contract:
// version 2 supervisors reply `lab is starting` pre-ready, which newer CLIs
// treat as stale. Main-line code is pre-release, so the wire may break freely.
const supervisorProtocolVersion = 3

// cliProtocolVersion is the version of the CLI JSON envelope itself. It is
// independent from the Supervisor protocol and travels in its own field so
// the two contracts can evolve separately (AD-8, FR-18).
const cliProtocolVersion = 1

type request struct {
	Action   string   `json:"action"`
	Argv     []string `json:"argv,omitempty"`
	Manifest string   `json:"manifest,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Columns  int      `json:"columns,omitempty"`
}

// response is the wire shape shared by the CLI envelope and every Supervisor
// reply.
//
// Two protocol version fields travel together so the CLI envelope contract
// (Protocol) and the Supervisor protocol contract (SupervisorProtocolVersion)
// can evolve independently per AD-8 / FR-21.
//
// The CLI side sets Protocol=cliProtocolVersion in JSON output (printResponse);
// the non-JSON CLI branch leaves it zero. In either case the Supervisor's
// SupervisorProtocolVersion is forwarded untouched. The Supervisor side never
// sets Protocol; it stamps SupervisorProtocolVersion=supervisorProtocolVersion
// on every reply, including the pre-ready and decode-error paths.
type response struct {
	OK      bool           `json:"ok"`
	Output  string         `json:"output,omitempty"`
	Error   string         `json:"error,omitempty"`
	Report  *lab.RunReport `json:"report,omitempty"`
	SSHPort int            `json:"ssh_port,omitempty"`
	// Protocol is the CLI JSON envelope version. Set to cliProtocolVersion
	// by the CLI before printing a response in --json mode.
	Protocol int `json:"protocol,omitempty"`
	// SupervisorProtocolVersion is the Supervisor protocol version the
	// Supervisor stamps on every reply.
	SupervisorProtocolVersion int `json:"supervisorProtocolVersion"`
	// Status is the overall Operator Profile verdict for status replies:
	// READY when every scope is ready, NOT_READY otherwise.
	Status string `json:"status,omitempty"`
	// Scopes lists every AD-11 scope's state and reason for status replies.
	Scopes []lab.ScopeResult `json:"scopes,omitempty"`
}

type application struct {
	session string
	json    bool
}

type supervisor struct {
	operationMutex sync.Mutex
	serialMutex    sync.Mutex
	shutdownMutex  sync.Mutex
	serial         net.Conn
	stopping       bool
}

type sessionRuntime struct {
	Ready       chan struct{}
	State       *supervisor
	Framework   *framework.TestFramework
	Restore     func() error
	Shutdown    func() error
	Interrupted *atomic.Bool
}

func (m *supervisor) TryOperation() bool {
	m.serialMutex.Lock()
	defer m.serialMutex.Unlock()
	return !m.stopping && m.operationMutex.TryLock()
}

func (m *supervisor) ReleaseOperation() {
	m.operationMutex.Unlock()
}

func (m *supervisor) TrySerial(connection net.Conn) bool {
	m.serialMutex.Lock()
	defer m.serialMutex.Unlock()
	if m.stopping || !m.operationMutex.TryLock() {
		return false
	}
	m.serial = connection
	return true
}

func (m *supervisor) ReleaseSerial(connection net.Conn) {
	m.serialMutex.Lock()
	if m.serial == connection {
		m.serial = nil
	}
	m.serialMutex.Unlock()
	m.operationMutex.Unlock()
}

func (m *supervisor) CloseSerial() {
	m.serialMutex.Lock()
	defer m.serialMutex.Unlock()
	if m.serial != nil {
		_ = m.serial.Close()
	}
}

// Stop flags the supervisor as stopping and closes an active serial session.
//
// TryOperation and TrySerial reject new work after Stop returns. Stop does not
// wait for an operation already holding operationMutex.
func (m *supervisor) Stop() {
	m.serialMutex.Lock()
	defer m.serialMutex.Unlock()
	m.stopping = true
	if m.serial != nil {
		_ = m.serial.Close()
	}
}

func (m *supervisor) waitForOperation(fn func() error) error {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	return fn()
}

func (m *supervisor) shutdown(beforeWait func(), fn func() error, result func(error)) error {
	m.shutdownMutex.Lock()
	defer m.shutdownMutex.Unlock()
	m.Stop()
	if beforeWait != nil {
		beforeWait()
	}
	err := m.waitForOperation(fn)
	if result != nil {
		result(err)
	}
	return err
}

func (m *supervisor) Shutdown(fn func() error) error {
	return m.shutdown(nil, fn, nil)
}

// ShutdownWithBeforeWait stops the supervisor after running a pre-wait hook.
//
// The hook runs after new work is rejected and before the active operation is
// awaited.
func (m *supervisor) ShutdownWithBeforeWait(beforeWait func(), fn func() error) error {
	return m.shutdown(beforeWait, fn, nil)
}

// ShutdownWithBeforeWaitAndResult stops the supervisor and runs a result hook.
//
// The result hook runs while shutdowns are serialized and after the active
// operation and resource cleanup have completed.
func (m *supervisor) ShutdownWithBeforeWaitAndResult(beforeWait func(), fn func() error, result func(error)) error {
	return m.shutdown(beforeWait, fn, result)
}

func main() {
	os.Exit(newApplication().Run())
}

func newApplication() *application {
	return &application{session: defaultSession}
}

// Run executes the lab command and returns its process exit code.
func (m *application) Run() int {
	root := m.command()
	if err := root.Execute(); err != nil {
		if m.json {
			_ = json.NewEncoder(os.Stderr).Encode(response{Error: err.Error(), Protocol: cliProtocolVersion})
		} else {
			fmt.Fprintln(os.Stderr, "yanet-lab:", err)
		}
		var usageErr interface{ Usage() bool }
		if errors.As(err, &usageErr) && usageErr.Usage() {
			return 2
		}
		return 1
	}
	return 0
}

func (m *application) command() *cobra.Command {
	root := &cobra.Command{
		Use:          "yanet-lab",
		Short:        "Operate a reusable local YANET2 QEMU lab",
		Long:         "Use 'yanet-lab up' to start the lab, then 'status', 'reset', 'scenario', or 'down'.",
		RunE:         func(command *cobra.Command, _ []string) error { return command.Help() },
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&m.session, "session", defaultSession, "lab session name")
	root.PersistentFlags().BoolVar(&m.json, "json", false, "emit machine-readable JSON")
	root.AddCommand(
		&cobra.Command{Use: "doctor", Short: "Check host prerequisites", RunE: func(*cobra.Command, []string) error { return m.doctor() }},
		m.upCommand(),
		m.statusCommand(),
		&cobra.Command{Use: "reset", Short: "Restore the baseline snapshot", RunE: func(*cobra.Command, []string) error { return m.simple("reset", nil) }},
		&cobra.Command{Use: "report", Short: "Collect an inspect and readiness report", RunE: func(*cobra.Command, []string) error { return m.simple("report", nil) }},
		&cobra.Command{Use: "down", Short: "Stop the lab VM", RunE: func(*cobra.Command, []string) error { return m.simple("down", nil) }},
		m.execCommand(), m.shellCommand(), m.serialCommand(), m.manifestCommand(), m.scenarioCommand(), m.serveCommand(),
	)
	return root
}

func (m *application) execCommand() *cobra.Command {
	return &cobra.Command{Use: "exec -- COMMAND [ARG...]", Short: "Execute a command in the guest", Args: cobra.MinimumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		return m.simple("exec", args)
	}}
}

// upCommand wires the optional positional session name used in the recipe
// invocations (`just lab up my-session`) on top of the --session flag.
func (m *application) upCommand() *cobra.Command {
	return &cobra.Command{Use: "up [SESSION]", Short: "Start or reuse the lab VM", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		m.selectSession(args)
		return m.up()
	}}
}

// statusCommand wires the optional positional session name used in the recipe
// invocations (`just lab status my-session`) on top of the --session flag,
// matching the up command.
func (m *application) statusCommand() *cobra.Command {
	return &cobra.Command{Use: "status [SESSION]", Short: "Show lab and YANET readiness", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		m.selectSession(args)
		return m.status()
	}}
}

// selectSession applies an optional positional session name to the running
// application, leaving the --session flag value intact when no positional is
// given. Extracted so the assignment is testable without spawning serve.
func (m *application) selectSession(args []string) {
	if len(args) == 1 {
		m.session = args[0]
	}
}

func (m *application) shellCommand() *cobra.Command {
	return &cobra.Command{Use: "shell", Short: "Open an interactive guest command shell", RunE: func(*cobra.Command, []string) error {
		response, err := m.call(request{Action: "shell"})
		if err != nil {
			return err
		}
		if !response.OK {
			return errors.New(response.Error)
		}
		dir, _, err := sessionPaths(m.session)
		if err != nil {
			return err
		}
		command := exec.Command("ssh", "-tt", "-i", filepath.Join(dir, "id_ed25519"), "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "PasswordAuthentication=no", "-p", fmt.Sprint(response.SSHPort), "root@127.0.0.1", "bash", "--rcfile", "/tmp/yanet/lab.bashrc", "-i")
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Run()
	}}
}

func (m *application) serialCommand() *cobra.Command {
	return &cobra.Command{Use: "serial", Short: "Attach to the guest ttyS0 console", RunE: func(*cobra.Command, []string) error {
		rows, columns := 24, 80
		if width, height, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			rows, columns = serialDimensions(width, height)
		}
		connection, err := m.sessionConnection()
		if err != nil {
			return err
		}
		defer connection.Close()
		if err := json.NewEncoder(connection).Encode(request{Action: "serial", Rows: rows, Columns: columns}); err != nil {
			return err
		}
		var response response
		if err := json.NewDecoder(connection).Decode(&response); err != nil {
			return err
		}
		if !response.OK {
			return errors.New(response.Error)
		}
		if _, err := connection.Write([]byte{1}); err != nil {
			return err
		}
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("set terminal raw mode: %w", err)
		}
		defer term.Restore(int(os.Stdin.Fd()), state)
		fmt.Fprintln(os.Stderr, "Detach with Ctrl-].")
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(os.Stdout, connection)
			close(done)
		}()
		if err := copySerialInput(connection, os.Stdin); err != nil {
			return err
		}
		_ = connection.Close()
		<-done
		return nil
	}}
}

func serialDimensions(width, height int) (int, int) {
	return height, width
}

func copySerialInput(destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 1024)
	for {
		count, err := source.Read(buffer)
		if count > 0 {
			for idx, value := range buffer[:count] {
				if value == 0x1d {
					if idx > 0 {
						if _, writeErr := destination.Write(buffer[:idx]); writeErr != nil {
							return writeErr
						}
					}
					return nil
				}
			}
			if _, writeErr := destination.Write(buffer[:count]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (m *application) manifestCommand() *cobra.Command {
	command := &cobra.Command{Use: "manifest", Short: "Validate or run a lab manifest"}
	command.AddCommand(
		&cobra.Command{Use: "validate PATH", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			manifest, err := lab.LoadManifest(args[0])
			if err != nil {
				return err
			}
			return m.printValue(manifest)
		}},
		&cobra.Command{Use: "run PATH", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			if _, err := lab.LoadManifest(path); err != nil {
				return err
			}
			if err := m.ensureUp(); err != nil {
				return err
			}
			return m.simpleManifest(path)
		}},
	)
	return command
}

func (m *application) scenarioCommand() *cobra.Command {
	command := &cobra.Command{Use: "scenario", Short: "List or run built-in guided scenarios"}
	command.AddCommand(
		&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			matches, err := filepath.Glob(filepath.Join(root, "lab", "scenarios", "*", "manifest.yaml"))
			if err != nil {
				return err
			}
			names := make([]string, 0, len(matches))
			for _, m := range matches {
				names = append(names, filepath.Base(filepath.Dir(m)))
			}
			sort.Strings(names)
			if m.json {
				return json.NewEncoder(os.Stdout).Encode(names)
			}
			for _, name := range names {
				fmt.Println(name)
			}
			return nil
		}},
		&cobra.Command{Use: "run NAME", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if !validSessionName(args[0]) {
				return fmt.Errorf("invalid scenario name: %q", args[0])
			}
			root, err := projectRoot()
			if err != nil {
				return err
			}
			path := filepath.Join(root, "lab", "scenarios", args[0], "manifest.yaml")
			if _, err := lab.LoadManifest(path); err != nil {
				return err
			}
			if err := m.ensureUp(); err != nil {
				return err
			}
			return m.simpleManifest(path)
		}},
	)
	return command
}

func (m *application) serveCommand() *cobra.Command {
	command := &cobra.Command{Use: "serve", Hidden: true, RunE: func(*cobra.Command, []string) error { return m.serve() }}
	return command
}

func (m *application) doctor() error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	image := os.Getenv("YANET_QEMU_IMAGE")
	if image == "" {
		image = filepath.Join(root, "tests", "functional", "yanet-test.qcow2")
	}
	report := collectDoctorReport(doctorConfig{
		Root:         root,
		Image:        image,
		LookPath:     exec.LookPath,
		ImageCheck:   validateQEMUImage,
		Platform:     runtime.GOOS,
		KVMAvailable: framework.KVMAvailable,
	})
	if m.json {
		_ = json.NewEncoder(os.Stdout).Encode(report)
	} else {
		for _, check := range report.Checks {
			fmt.Printf("%-8s %-52s %s\n", strings.ToUpper(check.Status), check.Name, check.Reason)
		}
	}
	if !report.OK {
		failed := make([]string, 0)
		for _, check := range report.Checks {
			if check.Status == doctorStatusFailed {
				failed = append(failed, check.Name)
			}
		}
		return fmt.Errorf("required lab prerequisites are missing: %s", strings.Join(failed, ", "))
	}
	return nil
}

const (
	doctorStatusOK      = "ok"
	doctorStatusFailed  = "failed"
	doctorStatusWarning = "warning"
)

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

type doctorConfig struct {
	Root         string
	Image        string
	LookPath     func(string) (string, error)
	ImageCheck   func(string) error
	Platform     string
	KVMAvailable func() bool
}

func collectDoctorReport(config doctorConfig) doctorReport {
	checks := make([]doctorCheck, 0)
	for _, name := range []string{"go", "just", "qemu-system-x86_64", "qemu-img", "ssh", "ssh-keygen"} {
		path, err := config.LookPath(name)
		if err != nil {
			checks = append(checks, doctorCheck{
				Name:   "tool:" + name,
				Status: doctorStatusFailed,
				Reason: "not found in PATH",
			})
			continue
		}
		checks = append(checks, doctorCheck{
			Name:   "tool:" + name,
			Status: doctorStatusOK,
			Reason: "found at " + path,
		})
	}

	imageCheck := config.ImageCheck
	if imageCheck == nil {
		imageCheck = readableRegularFile
	}
	if err := imageCheck(config.Image); err != nil {
		checks = append(checks, doctorCheck{
			Name:   "qemu-image",
			Status: doctorStatusFailed,
			Reason: fmt.Sprintf("%s: %v", config.Image, err),
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:   "qemu-image",
			Status: doctorStatusOK,
			Reason: config.Image,
		})
	}

	for _, path := range lab.RequiredArtifacts(config.Root) {
		relative, err := filepath.Rel(config.Root, path)
		if err != nil {
			relative = path
		}
		name := "artifact:" + filepath.ToSlash(relative)
		if err := readableExecutableFile(path); err != nil {
			checks = append(checks, doctorCheck{
				Name:   name,
				Status: doctorStatusFailed,
				Reason: fmt.Sprintf("%v; candidate source: %s", err, artifactSource(relative)),
			})
			continue
		}
		checks = append(checks, doctorCheck{
			Name:   name,
			Status: doctorStatusOK,
			Reason: "usable; candidate source: " + artifactSource(relative),
		})
	}

	checks = append(checks, doctorAccelerationCheck(config.Platform, config.KVMAvailable))
	report := doctorReport{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == doctorStatusFailed {
			report.OK = false
			break
		}
	}
	return report
}

func readableRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("missing or unreadable file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if info.Size() == 0 {
		return errors.New("file is empty")
	}
	if info.Mode().Perm()&0o444 == 0 {
		return errors.New("file has no read permission")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}
	return file.Close()
}

func validateQEMUImage(path string) error {
	if err := readableRegularFile(path); err != nil {
		return err
	}
	output, err := exec.Command("qemu-img", "info", "--output=json", path).CombinedOutput()
	if err != nil {
		diagnostics := strings.TrimSpace(lab.TruncateOutput(string(output)))
		if diagnostics == "" {
			return fmt.Errorf("qemu-img cannot read image: %w", err)
		}
		return fmt.Errorf("qemu-img cannot read image: %w: %s", err, diagnostics)
	}
	var info struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return fmt.Errorf("qemu-img returned invalid metadata: %w", err)
	}
	if info.Format != "qcow2" {
		return fmt.Errorf("image format is %q, want qcow2", info.Format)
	}
	return nil
}

func readableExecutableFile(path string) error {
	if err := readableRegularFile(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot inspect file: %w", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("file is not executable")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot inspect executable: %w", err)
	}
	defer file.Close()
	if filepath.Ext(path) == ".py" {
		line, err := bufio.NewReaderSize(file, 256).ReadSlice('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("cannot inspect shebang: %w", err)
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if !bytes.HasPrefix(line, []byte("#!")) {
			return errors.New("file is not a shebang script")
		}
		if strings.TrimSpace(string(line[2:])) == "" {
			return errors.New("shebang has no interpreter")
		}
		return nil
	}
	binary, err := elf.NewFile(file)
	if err != nil {
		return fmt.Errorf("file is not an ELF executable: %w", err)
	}
	if binary.Class != elf.ELFCLASS64 {
		return errors.New("file is not a 64-bit ELF executable")
	}
	if binary.Machine != elf.EM_X86_64 {
		return errors.New("file is not an x86_64 ELF executable")
	}
	if binary.Data != elf.ELFDATA2LSB {
		return errors.New("file is not a little-endian ELF executable")
	}
	if binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN {
		return errors.New("file is not a runnable ELF executable")
	}
	return nil
}

func artifactSource(relative string) string {
	relative = filepath.ToSlash(relative)
	switch {
	case strings.HasPrefix(relative, "build/operators/"):
		return "build/operators/"
	case strings.HasPrefix(relative, "target/release/"):
		return "target/release/"
	default:
		return relative
	}
}

func doctorAccelerationCheck(platform string, kvmAvailable func() bool) doctorCheck {
	if platform != "linux" {
		return doctorCheck{
			Name:   "kvm",
			Status: doctorStatusWarning,
			Reason: "KVM unavailable; using TCG fallback",
		}
	}
	if !kvmAvailable() {
		return doctorCheck{
			Name:   "kvm",
			Status: doctorStatusWarning,
			Reason: "KVM unavailable; using TCG fallback",
		}
	}
	return doctorCheck{Name: "kvm", Status: doctorStatusOK, Reason: "available"}
}

func (m *application) up() error {
	dir, _, err := sessionPaths(m.session)
	if err != nil {
		return err
	}
	if err := ensureSessionDirectory(dir); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "supervisor.log")
	if err := shutdownMarkerCheck(dir); err != nil {
		return err
	}
	reuse, replacedFrom, err := m.shutdownStaleSupervisor()
	if err != nil {
		return err
	}
	if reuse != nil {
		if !reuse.OK {
			if reuse.Error == labBusyError {
				return errLabBusy
			}
			return sessionUnhealthy(reuse.Error, logPath)
		}
		return m.printResponse(reuse)
	}
	if err := probeSessionLock(dir); err != nil {
		if errors.Is(err, errLabBusy) {
			return errLabBusy
		}
		return sessionUnhealthy(err.Error(), logPath)
	}
	// Re-check the shutdown marker immediately before spawning serve so a
	// concurrent `down` that wrote the marker between the initial check and
	// the spawn still blocks `up` instead of clobbering the failed session.
	if err := shutdownMarkerCheck(dir); err != nil {
		return err
	}
	response, err := serveRunner(m, logPath)
	if err != nil {
		return err
	}
	annotateReplacement(response, replacedFrom)
	return m.printResponse(response)
}

// sessionUnhealthy wraps the exact AC2 error form: a non-zero prefix naming
// the reason and the supervisor log path the developer should inspect.
func sessionUnhealthy(reason, logPath string) error {
	return fmt.Errorf("session unhealthy: %s; see %s", reason, logPath)
}

// probeSessionLock reports whether another supervisor process still holds the
// session lock. up uses it to refuse clobbering a locked-but-silent session
// (AC2 "locked by a stale Supervisor") without spawning a serve process that
// would just die on flock.
func probeSessionLock(directory string) error {
	path := filepath.Join(directory, "supervisor.lock")
	lock, err := openPrivateFile(path, os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errLabBusy
		}
		return fmt.Errorf("supervisor lock is held by another process: %w", err)
	}
	return nil
}

// noteProtocolReplacement records in the up response that a Supervisor with a
// stale protocol version was torn down and replaced. A negative staleVersion
// (the noStaleTeardown sentinel) is a no-op so a direct caller cannot emit a
// bogus "replaced stale supervisor (protocol -1)" prefix.
func noteProtocolReplacement(output string, staleVersion int) string {
	if staleVersion < 0 {
		return output
	}
	prefix := fmt.Sprintf("replaced stale supervisor (protocol %d)", staleVersion)
	if output == "" {
		return prefix
	}
	return prefix + "; " + output
}

// annotateReplacement prepends the stale-protocol replacement note to the
// fresh Supervisor's status reply when up tore down a stale Supervisor first.
// Extracted so the no-op sentinel path is testable without a live Supervisor.
func annotateReplacement(response *response, replacedFrom int) {
	if replacedFrom >= 0 {
		response.Output = noteProtocolReplacement(response.Output, replacedFrom)
	}
}

// serveRunner forks the serve subprocess for up and returns its first OK
// status response. The package var lets tests substitute a fake runner so the
// stale-protocol replacement wire-up can be exercised without spawning QEMU.
var serveRunner = func(m *application, logPath string) (*response, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logFile, err := openPrivateFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, "--session", m.session, "serve")
	command.Stdout, command.Stderr, command.Stdin = logFile, logFile, nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	_ = logFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	deadline := time.Now().Add(startupTimeout())
	lastStatusError := ""
	for time.Now().Before(deadline) {
		if response, callErr := m.call(request{Action: "status"}); callErr == nil {
			if response.OK {
				return response, nil
			}
			lastStatusError = startupStatusError(lastStatusError, response.Error)
		}
		select {
		case processErr := <-exited:
			directory, _, pathErr := sessionPaths(m.session)
			return nil, exitedDuringStartupError(directory, pathErr, processErr, logPath)
		case <-time.After(250 * time.Millisecond):
		}
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-exited
	}
	return nil, startupFailure(lastStatusError, logPath)
}

func startupTimeout() time.Duration {
	return 2*framework.VMReadyTimeout() + 5*time.Minute
}

func startupFailure(lastStatusError, logPath string) error {
	if lastStatusError == "" {
		return fmt.Errorf("lab did not start; see %s", logPath)
	}
	return fmt.Errorf("lab did not start: %s; see %s", lastStatusError, logPath)
}

// startupStatusError folds a non-busy status reply into the startup poll's
// last-seen error. The pre-ready Supervisor answers every non-down action
// with `lab is busy` while it is still booting, so that reply is expected
// during startup and must not become the failure message a timeout reports.
func startupStatusError(lastStatusError, statusError string) string {
	if statusError == labBusyError {
		return lastStatusError
	}
	return statusError
}

// exitedDuringStartupError reports a forked serve child that exited before
// answering OK. When the session lock is still held by a competing Supervisor
// the child lost the flock race and the exact busy contract applies; otherwise
// the wrapped error names the child's own failure. directoryErr guards the
// probe so an unresolvable session path still yields the generic error.
func exitedDuringStartupError(directory string, directoryErr error, processErr error, logPath string) error {
	if directoryErr == nil && errors.Is(probeSessionLock(directory), errLabBusy) {
		return errLabBusy
	}
	return fmt.Errorf("lab supervisor exited during startup: %w; see %s", processErr, logPath)
}

func (m *application) ensureUp() error {
	return m.up()
}

type staleSupervisorDecision int

const (
	staleSupervisorAbsent staleSupervisorDecision = iota
	staleSupervisorCurrent
	staleSupervisorStale
)

func classifyStaleSupervisor(resp *response, callErr error) staleSupervisorDecision {
	if callErr != nil {
		return staleSupervisorAbsent
	}
	if resp.SupervisorProtocolVersion == supervisorProtocolVersion {
		return staleSupervisorCurrent
	}
	return staleSupervisorStale
}

// noStaleTeardown is the replacedFrom sentinel returned by shutdownStaleSupervisor
// when no stale Supervisor was torn down. 0 is a legitimate observed stale
// version (an old Supervisor that did not stamp supervisorProtocolVersion), so
// the sentinel is distinct.
const noStaleTeardown = -1

// shutdownStaleSupervisor checks for a running lab supervisor. If one exists
// with the current protocol version it returns the status response. If a stale
// (wrong-version) supervisor is running, it sends "down" and polls until the
// socket goes away, then reports the observed stale version so up can annotate
// the replacement in its JSON output (AC4).
func (m *application) shutdownStaleSupervisor() (*response, int, error) {
	resp, err := m.call(request{Action: "status"})
	switch classifyStaleSupervisor(resp, err) {
	case staleSupervisorAbsent:
		return nil, noStaleTeardown, nil
	case staleSupervisorCurrent:
		return resp, noStaleTeardown, nil
	}
	staleVersion := resp.SupervisorProtocolVersion
	staleResponse, staleErr := m.call(request{Action: "down"})
	if staleErr != nil {
		return nil, noStaleTeardown, fmt.Errorf("stale lab supervisor is running; stop it before starting a new one: %w", staleErr)
	}
	if !staleResponse.OK {
		return nil, noStaleTeardown, fmt.Errorf("stale lab supervisor refused shutdown: %s", staleResponse.Error)
	}
	deadline := time.Now().Add(supervisorRequestTimeout)
	for time.Now().Before(deadline) {
		if _, callErr := m.call(request{Action: "status"}); callErr != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, callErr := m.call(request{Action: "status"}); callErr == nil {
		return nil, noStaleTeardown, fmt.Errorf("stale lab supervisor did not shut down within %s; stop it manually", supervisorRequestTimeout)
	}
	return nil, staleVersion, nil
}

func (m *application) simple(action string, argv []string) error {
	response, err := m.call(request{Action: action, Argv: argv})
	if err != nil {
		return err
	}
	return m.printResponse(response)
}

// status runs the read-only Operator Profile status check and renders it.
//
// A Supervisor reply whose protocol version differs from the current one is a
// stale Supervisor: status reports the observed version and returns a protocol
// mismatch error without calling down or changing any Lab state. A reply whose
// overall status is missing is derived from the reported scopes, and a failed
// reply never renders a ready verdict, so the CLI can contradict neither the
// Supervisor nor its own fail-closed default.
func (m *application) status() error {
	reply, err := m.call(request{Action: "status"})
	if err != nil {
		return err
	}
	if reply.SupervisorProtocolVersion != supervisorProtocolVersion {
		reply.Status = statusNotReady
		reply.OK = false
		reply.Error = fmt.Sprintf("protocol mismatch: supervisor protocol %d, want %d", reply.SupervisorProtocolVersion, supervisorProtocolVersion)
		return m.printResponse(reply)
	}
	if reply.Scopes == nil {
		reply.Scopes = []lab.ScopeResult{}
	}
	// Re-derive the verdict from the reported scopes so a Supervisor reply can
	// never force a ready verdict the scopes contradict.
	reply.Status = statusVerdict(reply.Scopes)
	if !reply.OK {
		reply.Status = statusNotReady
	}
	if reply.Status == statusNotReady && reply.Error == "" {
		reply.OK = false
		reply.Error = operatorProfileNotReadyError(reply.Scopes)
	}
	return m.printResponse(reply)
}

// statusVerdict reduces per-scope state to the overall READY/NOT_READY
// verdict. It fails closed: the report must cover every AD-11 scope exactly
// once (any order), every scope must be a known AD-11 name, and every scope
// must be ready. A missing, duplicated, unknown, or non-ready scope is
// NOT_READY, so an incomplete or forged report can never be mistaken for a
// healthy profile.
func statusVerdict(scopes []lab.ScopeResult) string {
	expected := lab.ScopeNames()
	if len(scopes) != len(expected) {
		return statusNotReady
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, duplicate := seen[scope.Name]; duplicate {
			return statusNotReady
		}
		seen[scope.Name] = struct{}{}
		if !slices.Contains(expected, scope.Name) || scope.State != lab.StateReady {
			return statusNotReady
		}
	}
	return statusReady
}

// failedScopeNames returns the AD-11 scope names that a status result does not
// report as ready: it walks the expected scope table and names every expected
// scope that is missing from the result or present but not ready. Duplicated
// result entries collapse into one, and extra unknown names carried by the
// result are not iterated here.
func failedScopeNames(scopes []lab.ScopeResult) []string {
	byName := make(map[string]lab.ScopeResult, len(scopes))
	for _, scope := range scopes {
		byName[scope.Name] = scope
	}
	failed := make([]string, 0, len(lab.ScopeNames()))
	for _, name := range lab.ScopeNames() {
		scope, present := byName[name]
		if !present || scope.State != lab.StateReady {
			failed = append(failed, name)
		}
	}
	return failed
}

// operatorProfileNotReadyError renders the shared NOT_READY error message used
// by both the Supervisor status handler and the CLI fallback, so scripts
// grepping the message observe one shape. The message names the failing scopes
// when any are known and stays a bare message otherwise.
func operatorProfileNotReadyError(scopes []lab.ScopeResult) string {
	names := failedScopeNames(scopes)
	if len(names) == 0 {
		return "operator profile is not ready"
	}
	return "operator profile is not ready: " + strings.Join(names, ", ")
}

// checkOperatorStatus gathers the per-scope Operator Profile status from the
// live guest. The package var lets tests substitute a fake runner so the
// status request handler can be exercised without a QEMU guest.
var checkOperatorStatus = lab.CheckStatus

func (m *application) simpleManifest(path string) error {
	response, err := m.call(request{Action: "manifest", Manifest: path})
	if err != nil {
		return err
	}
	return m.printResponse(response)
}

func (m *application) call(value request) (*response, error) {
	return callSupervisor(m, value)
}

// callSupervisor issues a request to the named session's Supervisor over its
// Unix-domain socket. The function lives at package scope so tests can
// substitute a fake without standing up a real socket. Tests must not call
// t.Parallel() while this var is stubbed because the package-level binding
// is shared.
var callSupervisor = func(m *application, value request) (*response, error) {
	connection, err := m.sessionConnection()
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(value); err != nil {
		return nil, err
	}
	var reply response
	if err := connection.SetReadDeadline(time.Now().Add(requestTimeout(value.Action))); err != nil {
		return nil, err
	}
	defer connection.SetReadDeadline(time.Time{})
	if err := json.NewDecoder(connection).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

var executeSupervisorCommand = (*framework.TestFramework).ExecuteCommandWithTimeout

func requestTimeout(action string) time.Duration {
	switch action {
	case "manifest":
		return supervisorManifestTimeout + 30*time.Second
	case "exec":
		return supervisorExecTimeout + 30*time.Second
	case "reset":
		return supervisorResetTimeout
	case "down":
		return supervisorShutdownTimeout
	// status runs every Operator Profile probe in one guest command bounded
	// by the command runner, so the CLI deadline must exceed that bound
	// instead of the protocol handshake budget.
	case "status":
		return supervisorStatusTimeout
	default:
		return supervisorRequestTimeout
	}
}

func (m *application) sessionConnection() (net.Conn, error) {
	dir, socket, err := sessionPaths(m.session)
	if err != nil {
		return nil, err
	}
	if err := validateSessionDirectory(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("lab session %q is not running; use 'yanet-lab up'", m.session)
		}
		return nil, err
	}
	if err := validateSessionSocket(socket); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("lab session %q is not running; use 'yanet-lab up'", m.session)
		}
		return nil, err
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return nil, fmt.Errorf("lab session %q is not running; use 'yanet-lab up'", m.session)
	}
	return connection, nil
}

func (m *application) printResponse(value *response) error {
	if m.json {
		encoded := *value
		encoded.Protocol = cliProtocolVersion
		_ = json.NewEncoder(os.Stdout).Encode(&encoded)
	} else if value.Report != nil {
		for _, result := range value.Report.Results {
			marker := "PASS"
			if !result.Success {
				marker = "FAIL"
			}
			fmt.Printf("%s %-8s %s\n", marker, result.Kind, result.Name)
			if result.Output != "" {
				fmt.Println(result.Output)
			}
			if result.Error != "" {
				fmt.Println(result.Error)
			}
		}
	} else if value.Scopes != nil {
		if value.Status != "" {
			fmt.Println(value.Status)
		}
		if value.Output != "" {
			fmt.Println(value.Output)
		}
		for _, scope := range value.Scopes {
			if scope.State != lab.StateReady {
				fmt.Printf("%s: %s\n", scope.Name, scope.Reason)
			}
		}
	} else if value.Output != "" {
		fmt.Println(value.Output)
	}
	if !value.OK {
		return errors.New(value.Error)
	}
	return nil
}

func (m *application) printValue(value any) error {
	if m.json {
		return json.NewEncoder(os.Stdout).Encode(value)
	}
	fmt.Println("manifest is valid")
	return nil
}

func (m *application) serve() (err error) {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	dir, socket, err := sessionPaths(m.session)
	if err != nil {
		return err
	}
	if err := ensureSessionDirectory(dir); err != nil {
		return err
	}
	lock, err := acquireSessionLock(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	_ = os.Remove(socket)
	keyPath, err := ensureSSHKey(dir)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	startupInterrupted := atomic.Bool{}
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}, Interrupted: &startupInterrupted}
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopping)
	go func() {
		<-stopping
		handleTerminationSignal(runtime, func() { _ = listener.Close() })
	}()
	acceptErrors := make(chan error, 1)
	var handlers errgroup.Group
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				acceptErrors <- acceptErr
				return
			}
			handlers.Go(func() error {
				handleRuntimeConnection(connection, dir, runtime, func() { _ = listener.Close() })
				return nil
			})
		}
	}()
	harness, cleanup, err := framework.SetupHarness(framework.HarnessConfig{
		PoolName:         "lab-" + filepath.Base(filepath.Dir(dir)) + "-" + m.session,
		PoolSize:         1,
		BaselineTag:      "lab-operators",
		ProjectRoot:      root,
		EnableSSHForward: true,
		ForceStop:        true,
		Prepare:          lab.PrepareOperators,
		AfterStart:       lab.StartOperators,
		ProfileReady:     lab.CheckOperators,
		FingerprintFiles: lab.OperatorFingerprintFiles(),
	})
	if err != nil {
		return err
	}
	defer cleanup()
	var shutdownMutex sync.Mutex
	var shutdownDone bool
	shutdown := func() error {
		shutdownMutex.Lock()
		defer shutdownMutex.Unlock()
		if shutdownDone {
			return nil
		}
		err := harness.Shutdown()
		if err == nil {
			shutdownDone = true
		}
		return err
	}
	if startupInterrupted.Load() {
		_ = shutdown()
		return fmt.Errorf("lab startup interrupted")
	}
	defer func() {
		if shutdownErr := shutdown(); err == nil && shutdownErr != nil {
			err = fmt.Errorf("shut down lab: %w", shutdownErr)
		}
	}()
	fw := harness.Pool().Acquire()
	defer harness.Pool().Release(fw)
	if err := harness.Restore(fw); err != nil {
		return err
	}
	if err := setupGuestShell(fw, keyPath); err != nil {
		return err
	}
	restore := func() error {
		if err := harness.Restore(fw); err != nil {
			return err
		}
		return setupGuestShell(fw, keyPath)
	}
	runtime.Framework = fw
	runtime.Restore = restore
	runtime.Shutdown = shutdown
	close(runtime.Ready)
	acceptErr := <-acceptErrors
	runtime.State.CloseSerial()
	_ = handlers.Wait()
	if errors.Is(acceptErr, net.ErrClosed) {
		return nil
	}
	return acceptErr
}

func handleRuntimeConnection(connection net.Conn, dir string, runtime *sessionRuntime, stop func()) {
	defer connection.Close()
	select {
	case <-runtime.Ready:
		handleConnection(connection, runtime.Framework, dir, runtime.State, runtime.Restore, runtime.Shutdown, stop)
	default:
		var value request
		if err := decodeRequest(connection, &value); err != nil {
			_ = json.NewEncoder(connection).Encode(response{Error: err.Error(), SupervisorProtocolVersion: supervisorProtocolVersion})
			return
		}
		if value.Action == "down" {
			if runtime.Interrupted != nil {
				runtime.Interrupted.Store(true)
			}
			_ = json.NewEncoder(connection).Encode(response{OK: true, Output: "lab stopped", SupervisorProtocolVersion: supervisorProtocolVersion})
			stop()
			return
		}
		_ = json.NewEncoder(connection).Encode(response{Error: labBusyError, SupervisorProtocolVersion: supervisorProtocolVersion})
	}
}

func handleTerminationSignal(runtime *sessionRuntime, closeListener func()) {
	if runtime.Interrupted != nil {
		runtime.Interrupted.Store(true)
	}
	runtime.State.ShutdownWithBeforeWait(func() {
		closeListener()
		select {
		case <-runtime.Ready:
			if runtime.Framework != nil {
				runtime.Framework.AbortGuestSerial()
			}
		default:
		}
	}, func() error { return nil })
}

func handleConnection(connection net.Conn, fw *framework.TestFramework, dir string, state *supervisor, restore func() error, shutdown func() error, stop func()) {
	defer connection.Close()
	var value request
	if err := decodeRequest(connection, &value); err != nil {
		_ = json.NewEncoder(connection).Encode(response{Error: err.Error(), SupervisorProtocolVersion: supervisorProtocolVersion})
		return
	}
	reply := response{OK: true, SupervisorProtocolVersion: supervisorProtocolVersion}
	if value.Action == "serial" {
		if !state.TrySerial(connection) {
			reply.OK = false
			reply.Error = errLabBusy.Error()
			_ = json.NewEncoder(connection).Encode(reply)
			return
		}
		defer state.ReleaseSerial(connection)
	} else if value.Action != "down" {
		if !state.TryOperation() {
			reply.OK = false
			reply.Error = errLabBusy.Error()
			_ = json.NewEncoder(connection).Encode(reply)
			return
		}
		defer state.ReleaseOperation()
	}
	switch value.Action {
	case "status":
		results, checkErr := checkOperatorStatus(fw)
		reply.Scopes = results
		reply.Status = statusVerdict(results)
		if checkErr != nil {
			reply.Status = statusNotReady
			setError(&reply, checkErr)
		} else if reply.Status == statusNotReady {
			setError(&reply, errors.New(operatorProfileNotReadyError(results)))
		}
	case "exec":
		output, err := executeSupervisorCommand(fw, lab.ShellJoin(value.Argv), supervisorExecTimeout)
		reply.Output = lab.TruncateOutput(output)
		setError(&reply, err)
	case "shell":
		reply.SSHPort = fw.SSHPort()
		if reply.SSHPort == 0 {
			setError(&reply, errors.New("guest SSH forwarding is unavailable"))
		}
	case "serial":
		if err := json.NewEncoder(connection).Encode(reply); err != nil {
			return
		}
		streamSerial(connection, fw, value.Rows, value.Columns)
		return
	case "reset":
		if !baselineReadyProbe() {
			setError(&reply, errors.New("baseline snapshot missing; run up again"))
			break
		}
		err := restore()
		setError(&reply, err)
		if err == nil {
			reply.Output = "baseline restored"
		}
	case "manifest":
		manifestDone := make(chan lab.RunReport, 1)
		go func() { manifestDone <- lab.RunManifest(fw, value.Manifest) }()
		timer := time.NewTimer(supervisorManifestTimeout)
		var report lab.RunReport
		select {
		case report = <-manifestDone:
		case <-timer.C:
			fw.AbortGuestSerial()
			report = <-manifestDone
			if err := fw.RestartGuestSerial(); err != nil {
				report.Results = append(report.Results, lab.Result{
					Name:  "serial-restart",
					Kind:  "error",
					Error: "failed to restart serial after timeout: " + err.Error(),
				})
			}
			report.Success = false
			report.Results = append(report.Results, lab.Result{
				Name:  "server-timeout",
				Kind:  "timeout",
				Error: "manifest exceeded server-side timeout (" + supervisorManifestTimeout.String() + ")",
			})
			reply.OK = false
			reply.Error = "manifest exceeded server-side timeout"
		}
		timer.Stop()
		reply.Report = &report
		if reply.OK {
			reply.OK = report.Success
			if !report.Success {
				reply.Error = "manifest run failed"
			}
		}
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			setError(&reply, marshalErr)
		} else {
			setError(&reply, writeFile(filepath.Join(dir, "last-report.json"), data))
		}
	case "report":
		status, statusErr := fw.ExecuteCommand("pgrep -a yanet-dataplane; pgrep -a yanet-controlplane; ip -brief address show kni0")
		reportPath, writeErr := writeReport(dir, []byte(status))
		if writeErr != nil {
			setError(&reply, writeErr)
		} else {
			reply.Output = reportPath
			setError(&reply, statusErr)
		}
	case "down":
		reply.Output = "lab stopped"
		_ = json.NewEncoder(connection).Encode(reply)
		_ = state.ShutdownWithBeforeWaitAndResult(func() {
			if fw != nil {
				fw.AbortGuestSerial()
			}
		}, shutdown, func(shutdownErr error) {
			if shutdownErr == nil {
				if removeErr := os.Remove(filepath.Join(dir, shutdownMarkerName)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					fmt.Fprintf(os.Stderr, "remove shutdown marker: %v\n", removeErr)
				}
			} else if markerErr := writeShutdownMarker(dir, "down", shutdownErr.Error()); markerErr != nil {
				fmt.Fprintf(os.Stderr, "write shutdown marker: %v\n", markerErr)
			}
		})
		stop()
		return
	default:
		reply.OK = false
		reply.Error = "unknown action: " + value.Action
	}
	_ = json.NewEncoder(connection).Encode(reply)
}

func ensureSSHKey(dir string) (string, error) {
	path := filepath.Join(dir, "id_ed25519")
	if _, err := os.Lstat(path); err == nil {
		if err := validatePrivateFile(path, 0o600); err != nil {
			return "", err
		}
		if err := validatePublicFile(path + ".pub"); err != nil {
			return "", fmt.Errorf("invalid SSH public key: %w", err)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Lstat(path + ".pub"); err == nil {
		return "", fmt.Errorf("SSH private key missing while public key exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	command, err := sshKeygenCommand(path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshKeygenTimeout)
	defer cancel()
	command = exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	if output, runErr := command.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("generate lab SSH key: %w: %s", runErr, output)
	}
	if err := validatePrivateFile(path, 0o600); err != nil {
		return "", err
	}
	if err := validatePublicFile(path + ".pub"); err != nil {
		return "", err
	}
	return path, nil
}

func setupGuestShell(fw *framework.TestFramework, keyPath string) error {
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return fmt.Errorf("read lab SSH public key: %w", err)
	}
	rcFile := `if [ -f /root/.bashrc ]; then
  . /root/.bashrc
fi
export PATH=/tmp/yanet/cli:$PATH
for command in /tmp/yanet/cli/yanet-cli*; do
  [ -x "$command" ] || continue
  source <(COMPLETE=bash "$command")
done
printf '\nYANET lab: CLI=/tmp/yanet/cli config=/tmp/yanet/config logs=/tmp/yanet/logs build=/tmp/yanet/build\n'
printf 'Host controls: just lab reset | just lab down\n\n'
`
	if err := fw.WriteGuestFile("/root/.ssh/authorized_keys", string(publicKey)); err != nil {
		return err
	}
	if err := fw.WriteGuestFile("/tmp/yanet/lab.bashrc", rcFile); err != nil {
		return err
	}
	if _, err := fw.ExecuteCommand("chmod 700 /root/.ssh; chmod 600 /root/.ssh/authorized_keys; chmod 600 /tmp/yanet/lab.bashrc; service ssh start"); err != nil {
		return fmt.Errorf("start guest SSH service: %w", err)
	}
	return nil
}

func streamSerial(connection net.Conn, fw *framework.TestFramework, rows, columns int) {
	if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}
	if _, err := io.ReadFull(connection, make([]byte, 1)); err != nil {
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	if rows < 1 {
		rows = 24
	}
	if columns < 1 {
		columns = 80
	}
	serial, release, err := fw.AttachSerial()
	if err != nil {
		return
	}
	defer func() {
		if err := release(); err != nil {
			fmt.Fprintf(os.Stderr, "release serial: %v\n", err)
		}
	}()
	if _, err := fmt.Fprintf(serial, "stty rows %d columns %d; exec bash --rcfile /tmp/yanet/lab.bashrc -i\n", rows, columns); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(connection, serial)
		close(done)
	}()
	_, _ = io.Copy(serial, connection)
	_ = serial.Close()
	<-done
}

func decodeRequest(connection net.Conn, value *request) error {
	if err := connection.SetReadDeadline(time.Now().Add(supervisorRequestTimeout)); err != nil {
		return err
	}
	defer connection.SetReadDeadline(time.Time{})
	return json.NewDecoder(io.LimitReader(connection, maxRequestSize)).Decode(value)
}

func setError(reply *response, err error) {
	if err != nil {
		reply.OK = false
		reply.Error = err.Error()
	}
}

func writeReport(dir string, data []byte) (string, error) {
	path := filepath.Join(dir, "last-report.txt")
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func writeFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// writeShutdownMarker records a failed down in dir so the next up can block
// until an operator issues an explicit retry. The JSON shape is the exact
// schema checkShutdownMarker requires: status "FAILED" plus the non-empty
// step and reason naming what to clean up. The atomic rename + 0600 mode
// come from writeFile so the marker reader cannot observe a half-written
// file via openPrivateFile's NOFOLLOW + mode checks.
func writeShutdownMarker(dir, step, reason string) error {
	data, err := json.Marshal(map[string]string{"status": "FAILED", "step": step, "reason": reason})
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, shutdownMarkerName), data)
}

func acquireSessionLock(dir string) (*os.File, error) {
	lock, err := openPrivateFile(filepath.Join(dir, "supervisor.lock"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lab session is already running: %w", err)
	}
	return lock, nil
}

func sessionPaths(name string) (string, string, error) {
	if !validSessionName(name) {
		return "", "", errors.New("invalid session name")
	}
	root, err := projectRoot()
	if err != nil {
		return "", "", err
	}
	return sessionPathsForRoot(root, name)
}

func sessionPathsForRoot(root, name string) (string, string, error) {
	if !validSessionName(name) {
		return "", "", errors.New("invalid session name")
	}
	// Use /tmp directly: macOS TMPDIR paths are too long for Unix-domain
	// sockets once a session name is appended.
	base := filepath.Join("/tmp", fmt.Sprintf("yanet2-lab-%d", os.Getuid()), rootDigest(root), name)
	return base, filepath.Join(base, "supervisor.sock"), nil
}

// rootDigest returns the first 12 hex characters of sha256 over the
// symlink-resolved project root. It is the per-root namespace key in
// the runtime path layout /tmp/yanet2-lab-<uid>/<root-digest>/<name>/.
func rootDigest(resolvedRoot string) string {
	sum := sha256.Sum256([]byte(resolvedRoot))
	return fmt.Sprintf("%x", sum[:6])
}

func ensureSessionDirectory(dir string) error {
	for _, path := range []string{filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		if err := ensurePrivateDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func validateSessionDirectory(dir string) error {
	for _, path := range []string{filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		if err := validatePrivateDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validatePrivateDirectory(path)
}

func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe lab path %s: expected a directory", path)
	}
	if err := validateOwner(path, info); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("unsafe lab path %s: mode is %o, want 700", path, info.Mode().Perm())
	}
	return nil
}

func validateSessionSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe lab path %s: expected a Unix socket", path)
	}
	if err := validateOwner(path, info); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("unsafe lab path %s: mode is %o, want 600", path, info.Mode().Perm())
	}
	return nil
}

func validatePrivateFile(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe lab path %s: expected a regular file", path)
	}
	if err := validateOwner(path, info); err != nil {
		return err
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("unsafe lab path %s: mode is %o, want %o", path, info.Mode().Perm(), mode)
	}
	return nil
}

func validatePublicFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe lab path %s: expected a regular file", path)
	}
	if err := validateOwner(path, info); err != nil {
		return err
	}
	if info.Mode().Perm() != 0o644 {
		return fmt.Errorf("unsafe lab path %s: mode is %o, want 644", path, info.Mode().Perm())
	}
	return nil
}

func validateOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("unsafe lab path %s: not owned by uid %d", path, os.Getuid())
	}
	return nil
}

func openPrivateFile(path string, flags int) (*os.File, error) {
	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateFile(path, 0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// shutdownMarkerName is the file a failed down writes atomically into the
// session runtime directory. Its presence blocks the next up until an
// explicit successful down retry removes it (AC5/AC6).
const shutdownMarkerName = "shutdown-failed.json"

// maxShutdownMarkerBytes bounds how much of the marker file checkShutdownMarker
// reads; any well-formed marker is tiny.
const maxShutdownMarkerBytes = 4096

// shutdownMarkerCheck rejects up when a previous down left its marker in the
// session runtime directory. It is a package var so tests can stub the second
// invocation inside up() and exercise the TOCTOU re-check race without
// standing up a real marker-writing sibling process.
var shutdownMarkerCheck = checkShutdownMarker

// baselineReadyProbe answers whether harness.Restore's baseline fast-path can
// succeed. It is a package var so tests can stub the reset pre-check without
// provisioning a real baseline snapshot.
var baselineReadyProbe = framework.HasBaselineSnapshot

// checkShutdownMarker rejects up when a previous down left its marker in the
// session runtime directory. A valid marker names the failing step and reason
// and is reported back to the caller; any deviation from the exact
// mode-0600 regular file containing exactly the string fields
// status="FAILED", non-empty step and reason is treated as an unknown shutdown
// state so cleanup stays with an explicit down retry.
func checkShutdownMarker(directory string) error {
	path := filepath.Join(directory, shutdownMarkerName)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return unknownShutdownState(fmt.Errorf("inspect marker: %w", err))
	}
	file, err := openPrivateFile(path, os.O_RDONLY)
	if err != nil {
		return unknownShutdownState(err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxShutdownMarkerBytes+1))
	if err != nil {
		return unknownShutdownState(fmt.Errorf("read marker: %w", err))
	}
	if len(data) > maxShutdownMarkerBytes {
		return unknownShutdownState(fmt.Errorf("marker exceeds %d bytes", maxShutdownMarkerBytes))
	}
	fields := map[string]any{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return unknownShutdownState(fmt.Errorf("marker is not a JSON object: %w", err))
	}
	if len(fields) != 3 {
		return unknownShutdownState(fmt.Errorf("marker has %d fields, want exactly status, step, reason", len(fields)))
	}
	for _, key := range []string{"status", "step", "reason"} {
		value, present := fields[key]
		if !present {
			return unknownShutdownState(fmt.Errorf("marker missing field %q", key))
		}
		stringValue, ok := value.(string)
		if !ok {
			return unknownShutdownState(fmt.Errorf("marker field %q is not a string", key))
		}
		if stringValue == "" {
			return unknownShutdownState(fmt.Errorf("marker field %q is empty", key))
		}
	}
	if status := fields["status"].(string); status != "FAILED" {
		return unknownShutdownState(fmt.Errorf("marker status is %q, want FAILED", status))
	}
	step := fields["step"].(string)
	reason := fields["reason"].(string)
	return fmt.Errorf("previous down failed at step %q: %s; run 'yanet-lab down' to retry cleanup", step, reason)
}

// unknownShutdownState formats the uniform error checkShutdownMarker returns
// for every deviation from the contract (AC6).
func unknownShutdownState(err error) error {
	return fmt.Errorf("shutdown state unknown: %w; run 'yanet-lab down' to retry cleanup", err)
}

var sessionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validSessionName(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	if len(name) > maxSessionNameLen {
		return false
	}
	return sessionNamePattern.MatchString(name)
}

// lookupKeygen resolves the ssh-keygen binary. Tests override it to
// exercise the not-in-PATH branch deterministically without depending
// on the host's actual PATH.
var lookupKeygen = func() (string, error) {
	return exec.LookPath("ssh-keygen")
}

// sshKeygenCommand constructs the ssh-keygen command for the runtime's
// private key. It surfaces a clean error when ssh-keygen is missing
// rather than the raw exec.LookPath / exit-status strings.
func sshKeygenCommand(privatePath string) (*exec.Cmd, error) {
	binary, err := lookupKeygen()
	if err != nil {
		return nil, errors.New("ssh-keygen not found in PATH; install OpenSSH client tools")
	}
	return exec.Command(binary, "-q", "-t", "ed25519", "-N", "", "-f", privatePath), nil
}

type projectRootState struct {
	sync.Once
	root string
	err  error
}

func (m *projectRootState) Resolve() (string, error) {
	m.Do(func() {
		cwd, err := os.Getwd()
		if err != nil {
			m.err = err
			return
		}
		m.root, m.err = resolveProjectRoot(cwd)
	})
	return m.root, m.err
}

var projectRootCache projectRootState

func projectRoot() (string, error) {
	return projectRootCache.Resolve()
}

func resolveProjectRoot(start string) (string, error) {
	logicalStart, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	canonicalStart, err := filepath.EvalSymlinks(logicalStart)
	if err != nil {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}
	logicalWalk := walkToGoMod(logicalStart)
	if logicalWalk.Symlink {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}
	if !logicalWalk.Found {
		canonicalWalk := walkToGoMod(canonicalStart)
		if canonicalWalk.Symlink {
			return "", errors.New("project root is not canonical; resolve symlinks before running")
		}
		if len(logicalWalk.SymlinkPaths) != 0 {
			return "", errors.New("project root is not canonical; resolve symlinks before running")
		}
		if !canonicalWalk.Found {
			return "", fmt.Errorf("cannot find go.mod walking up from %s", start)
		}
		return canonicalWalk.Root, nil
	}

	canonicalRoot, err := filepath.EvalSymlinks(logicalWalk.Root)
	if err != nil {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}
	if symlinkLeavesRoot(logicalWalk.SymlinkPaths, filepath.Dir(logicalWalk.Root), canonicalRoot) {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}
	canonicalWalk := walkToGoMod(canonicalStart)
	if canonicalWalk.Symlink || !canonicalWalk.Found || canonicalWalk.Root != canonicalRoot {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}

	resolvedGoMod, err := filepath.EvalSymlinks(filepath.Join(logicalWalk.Root, "go.mod"))
	if err != nil || resolvedGoMod != filepath.Join(canonicalRoot, "go.mod") {
		return "", errors.New("project root is not canonical; resolve symlinks before running")
	}
	return canonicalRoot, nil
}

type goModWalk struct {
	Root         string
	Found        bool
	Symlink      bool
	SymlinkPaths []string
}

func walkToGoMod(start string) goModWalk {
	dir := start
	var symlinkPaths []string
	for {
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			symlinkPaths = append(symlinkPaths, dir)
			dir = filepath.Dir(dir)
			continue
		}
		marker := filepath.Join(dir, "go.mod")
		if info, err := os.Lstat(marker); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return goModWalk{Root: dir, Symlink: true, SymlinkPaths: symlinkPaths}
			}
			if info.Mode().IsRegular() {
				symlinkPaths = append(symlinkPaths, symlinkComponents(dir)...)
				return goModWalk{Root: dir, Found: true, SymlinkPaths: symlinkPaths}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return goModWalk{SymlinkPaths: symlinkPaths}
		}
		dir = parent
	}
}

func symlinkComponents(path string) []string {
	var paths []string
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			paths = append(paths, current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return paths
		}
	}
}

func symlinkLeavesRoot(paths []string, logicalRoot, canonicalRoot string) bool {
	for _, path := range paths {
		if !pathWithin(logicalRoot, path) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !pathWithin(canonicalRoot, resolved) {
			if err == nil && isSystemPrefixAlias(path, resolved) {
				continue
			}
			return true
		}
	}
	return false
}

func isSystemPrefixAlias(path, resolved string) bool {
	if filepath.Dir(path) != string(filepath.Separator) {
		return false
	}
	return resolved == filepath.Join(string(filepath.Separator)+"private", path)
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
