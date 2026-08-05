package main

import (
	"crypto/sha256"
	"encoding/base64"
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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

const defaultSession = "default"

type request struct {
	Action   string   `json:"action"`
	Argv     []string `json:"argv,omitempty"`
	Manifest string   `json:"manifest,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Columns  int      `json:"columns,omitempty"`
}

type response struct {
	OK      bool           `json:"ok"`
	Output  string         `json:"output,omitempty"`
	Error   string         `json:"error,omitempty"`
	Report  *lab.RunReport `json:"report,omitempty"`
	SSHPort int            `json:"ssh_port,omitempty"`
}

type application struct {
	session string
	json    bool
}

type supervisor struct {
	operationMutex sync.Mutex
	serialMutex    sync.Mutex
	serial         net.Conn
	stopping       bool
}

type sessionRuntime struct {
	Ready     chan struct{}
	State     *supervisor
	Framework *framework.TestFramework
	Restore   func() error
	Shutdown  func() error
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

func (m *supervisor) Shutdown(fn func() error) error {
	m.serialMutex.Lock()
	m.stopping = true
	if m.serial != nil {
		_ = m.serial.Close()
	}
	m.serialMutex.Unlock()
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	return fn()
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
			_ = json.NewEncoder(os.Stderr).Encode(response{Error: err.Error()})
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
	root := &cobra.Command{Use: "yanet-lab", Short: "Operate a reusable local YANET2 QEMU lab", SilenceUsage: true}
	root.PersistentFlags().StringVar(&m.session, "session", defaultSession, "lab session name")
	root.PersistentFlags().BoolVar(&m.json, "json", false, "emit machine-readable JSON")
	root.AddCommand(
		&cobra.Command{Use: "doctor", Short: "Check host prerequisites", RunE: func(*cobra.Command, []string) error { return m.doctor() }},
		&cobra.Command{Use: "up", Short: "Start or reuse the lab VM", RunE: func(*cobra.Command, []string) error { return m.up() }},
		&cobra.Command{Use: "status", Short: "Show lab and YANET readiness", RunE: func(*cobra.Command, []string) error { return m.simple("status", nil) }},
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
			for _, value := range buffer[:count] {
				if value == 0x1d {
					return nil
				}
				if _, writeErr := destination.Write([]byte{value}); writeErr != nil {
					return writeErr
				}
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
			names := []string{"forward-route", "decap", "nat64"}
			if m.json {
				return json.NewEncoder(os.Stdout).Encode(names)
			}
			for _, name := range names {
				fmt.Println(name)
			}
			return nil
		}},
		&cobra.Command{Use: "run NAME", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
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
	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	checks := []check{}
	for _, name := range []string{"go", "just", "qemu-system-x86_64", "qemu-img", "ssh", "ssh-keygen"} {
		path, lookErr := exec.LookPath(name)
		checks = append(checks, check{Name: name, OK: lookErr == nil, Detail: path})
	}
	image := os.Getenv("YANET_QEMU_IMAGE")
	if image == "" {
		image = filepath.Join(root, "tests", "functional", "yanet-test.qcow2")
	}
	_, imageErr := os.Stat(image)
	checks = append(checks, check{Name: "qemu-image", OK: imageErr == nil, Detail: image})
	if runtime.GOOS == "linux" {
		_, kvmErr := os.Stat("/dev/kvm")
		checks = append(checks, check{Name: "kvm-optional", OK: kvmErr == nil, Detail: "/dev/kvm"})
	}
	all := true
	for _, item := range checks {
		if !item.OK && item.Name != "kvm-optional" {
			all = false
		}
	}
	if m.json {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": all, "checks": checks})
	} else {
		for _, item := range checks {
			marker := "ok"
			if !item.OK {
				marker = "missing"
			}
			fmt.Printf("%-8s %-18s %s\n", marker, item.Name, item.Detail)
		}
	}
	if !all {
		return errors.New("required lab prerequisites are missing")
	}
	return nil
}

func (m *application) up() error {
	if response, err := m.call(request{Action: "status"}); err == nil {
		return m.printResponse(response)
	}
	dir, _, err := sessionPaths(m.session)
	if err != nil {
		return err
	}
	if err := ensureSessionDirectory(dir); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := openPrivateFile(filepath.Join(dir, "supervisor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return err
	}
	command := exec.Command(executable, "--session", m.session, "serve")
	command.Stdout, command.Stderr, command.Stdin = logFile, logFile, nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	deadline := time.Now().Add(startupTimeout())
	lastStatusError := ""
	for time.Now().Before(deadline) {
		if response, callErr := m.call(request{Action: "status"}); callErr == nil {
			if response.OK {
				return m.printResponse(response)
			}
			lastStatusError = response.Error
		}
		select {
		case processErr := <-exited:
			return fmt.Errorf("lab supervisor exited during startup: %w; see %s", processErr, filepath.Join(dir, "supervisor.log"))
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
	return startupFailure(lastStatusError, filepath.Join(dir, "supervisor.log"))
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

func (m *application) ensureUp() error {
	if response, err := m.call(request{Action: "status"}); err == nil && response.OK {
		return nil
	}
	return m.up()
}

func (m *application) simple(action string, argv []string) error {
	response, err := m.call(request{Action: action, Argv: argv})
	if err != nil {
		return err
	}
	return m.printResponse(response)
}

func (m *application) simpleManifest(path string) error {
	response, err := m.call(request{Action: "manifest", Manifest: path})
	if err != nil {
		return err
	}
	return m.printResponse(response)
}

func (m *application) call(value request) (*response, error) {
	connection, err := m.sessionConnection()
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(value); err != nil {
		return nil, err
	}
	var reply response
	if err := json.NewDecoder(connection).Decode(&reply); err != nil {
		return nil, err
	}
	return &reply, nil
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
		_ = json.NewEncoder(os.Stdout).Encode(value)
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
	runtime := &sessionRuntime{Ready: make(chan struct{}), State: &supervisor{}}
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
		PoolName:         "lab-" + m.session,
		BaselineTag:      "lab-operators",
		EnableSSHForward: true,
		Prepare:          lab.PrepareOperators,
		AfterStart:       lab.StartOperators,
		ProfileReady:     lab.CheckOperators,
		FingerprintFiles: lab.OperatorFingerprintFiles(),
	})
	if err != nil {
		return err
	}
	defer cleanup()
	var shutdownOnce sync.Once
	var shutdownErr error
	shutdown := func() error {
		shutdownOnce.Do(func() { shutdownErr = harness.Shutdown() })
		return shutdownErr
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
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stopping)
	go func() {
		<-stopping
		runtime.State.CloseSerial()
		_ = listener.Close()
	}()
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
	select {
	case <-runtime.Ready:
		handleConnection(connection, runtime.Framework, dir, runtime.State, runtime.Restore, runtime.Shutdown, stop)
	default:
		defer connection.Close()
		var value request
		if err := json.NewDecoder(connection).Decode(&value); err != nil {
			_ = json.NewEncoder(connection).Encode(response{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(connection).Encode(response{Error: "lab is starting"})
	}
}

func handleConnection(connection net.Conn, fw *framework.TestFramework, dir string, state *supervisor, restore func() error, shutdown func() error, stop func()) {
	defer connection.Close()
	var value request
	if err := json.NewDecoder(connection).Decode(&value); err != nil {
		_ = json.NewEncoder(connection).Encode(response{Error: err.Error()})
		return
	}
	reply := response{OK: true}
	if value.Action == "serial" {
		if !state.TrySerial(connection) {
			reply.OK = false
			reply.Error = "lab is busy"
			_ = json.NewEncoder(connection).Encode(reply)
			return
		}
		defer state.ReleaseSerial(connection)
	} else if value.Action != "down" {
		if !state.TryOperation() {
			reply.OK = false
			reply.Error = "lab is busy"
			_ = json.NewEncoder(connection).Encode(reply)
			return
		}
		defer state.ReleaseOperation()
	}
	switch value.Action {
	case "status":
		output, err := fw.ExecuteCommand("pgrep -f '[y]anet-dataplane' >/dev/null && pgrep -f '[y]anet-controlplane' >/dev/null")
		if err == nil {
			err = lab.CheckOperators(fw)
		}
		if err == nil {
			output = "VM: running; YANET: ready; operators: ready"
		}
		reply.Output = output
		setError(&reply, err)
	case "exec":
		output, err := fw.ExecuteCommand(shellJoin(value.Argv))
		reply.Output = output
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
		err := restore()
		setError(&reply, err)
		if err == nil {
			reply.Output = "baseline restored"
		}
	case "manifest":
		report := lab.RunManifest(fw, value.Manifest)
		reply.Report = &report
		reply.OK = report.Success
		if !report.Success {
			reply.Error = "manifest run failed"
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
		if err := state.Shutdown(shutdown); err != nil {
			setError(&reply, err)
		} else {
			reply.Output = "lab stopped"
		}
		_ = json.NewEncoder(connection).Encode(reply)
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
	command := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path)
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("generate lab SSH key: %w: %s", err, output)
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
	if err := writeGuestShellFile(fw, "/root/.ssh/authorized_keys", publicKey); err != nil {
		return err
	}
	if err := writeGuestShellFile(fw, "/tmp/yanet/lab.bashrc", []byte(rcFile)); err != nil {
		return err
	}
	if _, err := fw.ExecuteCommand("chmod 700 /root/.ssh; chmod 600 /root/.ssh/authorized_keys; chmod 600 /tmp/yanet/lab.bashrc; service ssh start"); err != nil {
		return fmt.Errorf("start guest SSH service: %w", err)
	}
	return nil
}

func writeGuestShellFile(fw *framework.TestFramework, path string, contents []byte) error {
	encoded := base64.StdEncoding.EncodeToString(contents)
	command := fmt.Sprintf("mkdir -p /root/.ssh /tmp/yanet; printf '%%s' '%s' | base64 -d > %s", encoded, path)
	if _, err := fw.ExecuteCommand(command); err != nil {
		return fmt.Errorf("write guest shell file %s: %w", path, err)
	}
	return nil
}

func streamSerial(connection net.Conn, fw *framework.TestFramework, rows, columns int) {
	if _, err := io.ReadFull(connection, make([]byte, 1)); err != nil {
		return
	}
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
	defer release()
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

func setError(reply *response, err error) {
	if err != nil {
		reply.OK = false
		reply.Error = err.Error()
	}
}

func writeReport(dir string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, "report-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
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

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func sessionPaths(name string) (string, string, error) {
	if !validSessionName(name) {
		return "", "", errors.New("invalid session name")
	}
	root, err := projectRoot()
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
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
	digest := sha256.Sum256([]byte(root))
	base := filepath.Join("/tmp", fmt.Sprintf("yanet2-lab-%d", os.Getuid()), fmt.Sprintf("%x", digest[:6]), name)
	return base, filepath.Join(base, "supervisor.sock"), nil
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

var sessionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validSessionName(name string) bool {
	return name != "." && name != ".." && sessionNamePattern.MatchString(name)
}

func projectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not inside the YANET2 repository")
		}
		dir = parent
	}
}
