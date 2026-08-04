package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

const defaultSession = "default"

type request struct {
	Action   string   `json:"action"`
	Argv     []string `json:"argv,omitempty"`
	Manifest string   `json:"manifest,omitempty"`
}

type response struct {
	OK     bool           `json:"ok"`
	Output string         `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
	Report *lab.RunReport `json:"report,omitempty"`
}

type application struct {
	session string
	json    bool
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
		m.execCommand(), m.shellCommand(), m.manifestCommand(), m.scenarioCommand(), m.serveCommand(),
	)
	return root
}

func (m *application) execCommand() *cobra.Command {
	return &cobra.Command{Use: "exec -- COMMAND [ARG...]", Short: "Execute a command in the guest", Args: cobra.MinimumNArgs(1), DisableFlagParsing: true, RunE: func(_ *cobra.Command, args []string) error {
		return m.simple("exec", args)
	}}
}

func (m *application) shellCommand() *cobra.Command {
	return &cobra.Command{Use: "shell", Short: "Open an interactive guest command shell", RunE: func(*cobra.Command, []string) error {
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("yanet-lab> ")
			if !scanner.Scan() {
				return scanner.Err()
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "exit" || line == "quit" {
				return nil
			}
			if line != "" {
				if err := m.simple("shell", []string{line}); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
		}
	}}
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
	for _, name := range []string{"go", "just", "qemu-system-x86_64", "qemu-img"} {
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
	if _, err := m.call(request{Action: "status"}); err == nil {
		return m.simple("status", nil)
	}
	dir, socket, err := sessionPaths(m.session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "supervisor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if response, callErr := m.call(request{Action: "status"}); callErr == nil {
			return m.printResponse(response)
		}
		select {
		case processErr := <-exited:
			return fmt.Errorf("lab supervisor exited during startup: %w; see %s", processErr, filepath.Join(dir, "supervisor.log"))
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("lab did not start; see %s", filepath.Join(dir, "supervisor.log"))
}

func (m *application) ensureUp() error {
	if _, err := m.call(request{Action: "status"}); err == nil {
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
	_, socket, err := sessionPaths(m.session)
	if err != nil {
		return nil, err
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return nil, fmt.Errorf("lab session %q is not running; use 'yanet-lab up'", m.session)
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

func (m *application) serve() error {
	dir, socket, err := sessionPaths(m.session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	harness, cleanup, err := framework.SetupHarness(framework.HarnessConfig{PoolName: "lab-" + m.session})
	if err != nil {
		return err
	}
	defer cleanup()
	defer harness.Shutdown()
	fw := harness.Pool().Acquire()
	defer harness.Pool().Release(fw)
	if err := harness.Restore(fw); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	_ = os.Chmod(socket, 0o600)
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stopping; _ = listener.Close() }()
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return acceptErr
		}
		stop := handleConnection(connection, fw, dir, func() error { return harness.Restore(fw) })
		if stop {
			return nil
		}
	}
}

func handleConnection(connection net.Conn, fw *framework.TestFramework, dir string, restore func() error) bool {
	defer connection.Close()
	var value request
	if err := json.NewDecoder(connection).Decode(&value); err != nil {
		_ = json.NewEncoder(connection).Encode(response{Error: err.Error()})
		return false
	}
	reply := response{OK: true}
	switch value.Action {
	case "status":
		output, err := fw.ExecuteCommand("pgrep -x yanet-dataplane >/dev/null && echo 'VM: running; YANET: ready'")
		reply.Output = output
		setError(&reply, err)
	case "exec":
		output, err := fw.ExecuteCommand(shellJoin(value.Argv))
		reply.Output = output
		setError(&reply, err)
	case "shell":
		output, err := fw.ExecuteCommand(strings.Join(value.Argv, " "))
		reply.Output = output
		setError(&reply, err)
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
		data, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile(filepath.Join(dir, "last-report.json"), data, 0o600)
	case "report":
		status, statusErr := fw.ExecuteCommand("pgrep -a yanet-dataplane; pgrep -a yanet-controlplane; ip -brief address show kni0")
		reportPath := filepath.Join(dir, "report-"+time.Now().Format("20060102-150405")+".txt")
		if writeErr := os.WriteFile(reportPath, []byte(status), 0o600); writeErr != nil {
			setError(&reply, writeErr)
		} else {
			reply.Output = reportPath
			setError(&reply, statusErr)
		}
	case "down":
		reply.Output = "lab stopped"
		_ = json.NewEncoder(connection).Encode(reply)
		return true
	default:
		reply.OK = false
		reply.Error = "unknown action: " + value.Action
	}
	_ = json.NewEncoder(connection).Encode(reply)
	return false
}

func setError(reply *response, err error) {
	if err != nil {
		reply.OK = false
		reply.Error = err.Error()
	}
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
	// Use /tmp directly: macOS TMPDIR paths are too long for Unix-domain
	// sockets once a session name is appended.
	base := filepath.Join("/tmp", fmt.Sprintf("yanet2-lab-%d", os.Getuid()), filepath.Base(root), name)
	return base, filepath.Join(base, "supervisor.sock"), nil
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
