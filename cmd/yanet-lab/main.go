package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yanet-platform/yanet2/lab"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

const defaultSession = "default"

// sunPathLimit is the platform-specific maximum length of the sockaddr_un
// path field (104 on darwin, 108 on linux). Subtract slack for the prefix
// `/tmp/yanet2-lab-<uid>/<root-digest>/` plus the trailing
// `/supervisor.sock` so the listener never fails with an opaque bind error.
const sunPathLimit = 104
const sunPathSlack = len("/supervisor.sock") + 1

// sshKeygenTimeout bounds the time we wait for `ssh-keygen` to produce a
// fresh ed25519 keypair. ssh-keygen with `-N ""` should not block past a
// second on any healthy host; the cap defends against a wedged binary
// blocking `up`/`serve` indefinitely.
const sshKeygenTimeout = 30 * time.Second

// maxSessionNameSlack covers the fixed portion of the runtime path that
// the session name sits inside: "/tmp/yanet2-lab-" (15) + uid (up to 10
// digits) + "/" + 12-char root digest + "/" = 39, rounded to 40 to leave
// a one-byte headroom under the sun_path cap.
const maxSessionNameSlack = 40

// maxSessionNameLen caps the session name length so the supervisor.sock
// path fits in sun_path regardless of the project root's absolute length.
// It is re-checked in validSessionName so any caller — including those
// that bypass provisionSession — gets the rejection up front.
const maxSessionNameLen = sunPathLimit - sunPathSlack - maxSessionNameSlack

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
	m := &application{session: defaultSession}
	root := m.command()
	if err := root.Execute(); err != nil {
		if m.json {
			_ = json.NewEncoder(os.Stderr).Encode(response{Error: err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "yanet-lab:", err)
		}
		var usageErr interface{ Usage() bool }
		if errors.As(err, &usageErr) && usageErr.Usage() {
			os.Exit(2)
		}
		os.Exit(1)
	}
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
	runtime, err := provisionSession(m.session)
	if err != nil {
		return err
	}
	// provisionSession leaves a placeholder supervisor.sock with mode 0600
	// so `up` satisfies the AC "runtime contains supervisor.sock mode
	// 0600". The forked `serve` is the only thing that removes it before
	// binding, so do not touch the placeholder here.
	dir := runtime.Dir
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
	socket, err := sessionSocket(m.session)
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
	runtime, err := provisionSession(m.session)
	if err != nil {
		return err
	}
	dir, socket := runtime.Dir, runtime.Socket
	// provisionSession writes a placeholder supervisor.sock with mode 0600
	// (the file the AC inspects). The listener below must bind a real socket
	// at the same path, so remove the placeholder before net.Listen and let
	// net.Listen recreate it as a socket inode.
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

// sessionRuntime is the owned state of a lab session: the runtime
// directory and the four files provisionSession lays down inside it.
//
// supervisor.sock starts as a regular-file placeholder (mode 0600); the
// forked Supervisor removes it before binding the real socket inode.
type sessionRuntime struct {
	Dir        string
	Socket     string
	Lock       string
	PrivateKey string
	PublicKey  string
}

// runtimeStat is the lstat seam used to validate owned paths. Production
// wires it to os.Lstat; tests override it to inject synthetic FileInfo
// values that simulate owner or mode drift without performing chown or
// chmod tricks on disk.
var runtimeStat = os.Lstat

// provisioningBase selects where the runtime directory tree is rooted.
// Production returns /tmp/yanet2-lab-<uid>; tests substitute t.TempDir().
var provisioningBase = func() string {
	return filepath.Join("/tmp", fmt.Sprintf("yanet2-lab-%d", os.Getuid()))
}

// lookupKeygen resolves the ssh-keygen binary used to provision the
// ed25519 host keypair. It defaults to exec.LookPath; tests override it
// to simulate ssh-keygen being absent from PATH.
var lookupKeygen = func() (string, error) {
	return exec.LookPath("ssh-keygen")
}

// walkStart returns the directory at which the project-root walk begins.
// Production reads os.Getwd; tests override this seam so the walk can
// be driven from a planted directory without depending on t.Chdir's
// behaviour on the host (getcwd(2) canonicalises on Linux but Go's
// $PWD fallback may not).
var walkStart = os.Getwd

// sessionLocation computes the canonical runtime paths for a session
// without touching the filesystem. Callers that only need the directory
// or socket path (for example the per-request dial in call) use this;
// callers that need to provision or validate owned files use
// provisionSession.
func sessionLocation(name string) (sessionRuntime, error) {
	if !validSessionName(name) {
		return sessionRuntime{}, errors.New("invalid session name")
	}
	root, err := cachedProjectRoot()
	if err != nil {
		return sessionRuntime{}, err
	}
	return sessionRuntimeAt(provisioningBase(), name, rootDigest(root)), nil
}

// sessionRuntimeAt is the path-only seam: it returns the canonical
// runtime paths for a given base and project-root digest, with no
// filesystem I/O. Tests use it to materialise a sessionRuntime inside
// t.TempDir() without invoking projectRoot.
//
// Callers that pass user-supplied names must call validSessionName
// first; sessionRuntimeAt does not validate the name on its own.
func sessionRuntimeAt(base, name, digest string) sessionRuntime {
	dir := filepath.Join(base, digest, name)
	return sessionRuntime{
		Dir:        dir,
		Socket:     filepath.Join(dir, "supervisor.sock"),
		Lock:       filepath.Join(dir, "supervisor.lock"),
		PrivateKey: filepath.Join(dir, "id_ed25519"),
		PublicKey:  filepath.Join(dir, "id_ed25519.pub"),
	}
}

// provisionSession provisions the runtime directory, lockfile, placeholder
// socket, and ed25519 host keypair for a named session, validating every
// owned path against type, mode, ownership, and symlink status. A second
// call on the same session is idempotent: present-and-valid paths are
// reused and the keypair is not regenerated.
func provisionSession(name string) (sessionRuntime, error) {
	rt, err := sessionLocation(name)
	if err != nil {
		return sessionRuntime{}, err
	}
	return provisionSessionAt(rt)
}

// provisionSessionAt is the I/O seam for provisionSession: it takes an
// already-resolved sessionRuntime and runs the create-or-validate flow.
// Tests call this directly with a runtime location under t.TempDir().
func provisionSessionAt(rt sessionRuntime) (sessionRuntime, error) {
	if err := ensureRuntimeDir(rt.Dir); err != nil {
		return sessionRuntime{}, err
	}
	if err := ensureOwnedFile(rt.Socket, 0o600); err != nil {
		return sessionRuntime{}, err
	}
	if err := ensureOwnedFile(rt.Lock, 0o600); err != nil {
		return sessionRuntime{}, err
	}
	if err := ensureKeypair(rt.PrivateKey, rt.PublicKey); err != nil {
		return sessionRuntime{}, err
	}
	return rt, nil
}

// rootDigest returns the first 12 hex characters of sha256 over the
// symlink-resolved project root. It is the per-root namespace key in
// the runtime path layout /tmp/yanet2-lab-<uid>/<root-digest>/<name>/.
func rootDigest(resolvedRoot string) string {
	sum := sha256.Sum256([]byte(resolvedRoot))
	return hex.EncodeToString(sum[:])[:12]
}

// ensureRuntimeDir creates the runtime directory with mode 0700 or
// validates an existing one for ownership, mode, and symlink status.
func ensureRuntimeDir(dir string) error {
	info, err := runtimeStat(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// MkdirAll applies the process umask; force the exact prescribed mode.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
		info, err = runtimeStat(dir)
		if err != nil {
			return err
		}
	}
	return validateOwnedPath(dir, info, "directory", 0o700, true)
}

// ensureOwnedFile creates a placeholder file with the prescribed mode
// or validates an existing one for ownership, mode, type, and symlink
// status.
func ensureOwnedFile(path string, mode os.FileMode) error {
	info, err := runtimeStat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		file, err := openNoFollow(path, mode)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		// OpenFile applies the process umask; force the exact prescribed mode.
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		return nil
	}
	return validateOwnedPath(path, info, "regular file", mode, false)
}

// ensureKeypair creates the ed25519 host keypair via ssh-keygen when
// the private key is missing, validates ownership, mode, type, and
// symlink status of both files in the present-and-valid case, and
// recovers from a partial-write failure (private present, public
// missing) by regenerating the pair.
func ensureKeypair(privatePath, publicPath string) error {
	info, err := runtimeStat(privatePath)
	switch {
	case err == nil:
		if err := validateOwnedPath(privatePath, info, "regular file", 0o600, false); err != nil {
			return err
		}
	case os.IsNotExist(err):
		if err := generateKeypair(privatePath); err != nil {
			return err
		}
	default:
		return err
	}
	// If the private key exists but the public key does not (a partial
	// write from a killed ssh-keygen), regenerate the pair. Removing
	// the stale private forces the missing branch above.
	pubInfo, err := runtimeStat(publicPath)
	if err == nil {
		return validateOwnedPath(publicPath, pubInfo, "regular file", 0o644, false)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat public key %s: %w", publicPath, err)
	}
	if err := os.Remove(privatePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale private key %s: %w", privatePath, err)
	}
	if err := generateKeypair(privatePath); err != nil {
		return err
	}
	pubInfo, err = runtimeStat(publicPath)
	if err != nil {
		return fmt.Errorf("public key missing after regeneration: %s: %w", publicPath, err)
	}
	return validateOwnedPath(publicPath, pubInfo, "regular file", 0o644, false)
}

// generateKeypair runs ssh-keygen with a bounded context and re-validates
// the freshly written files. The re-validation defends against a
// hostile or buggy ssh-keygen that produces a key with the wrong mode
// or owner.
func generateKeypair(privatePath string) error {
	keygen, lookErr := lookupKeygen()
	if lookErr != nil {
		return errors.New("ssh-keygen not found in PATH; install OpenSSH client tools")
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshKeygenTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, keygen, "-t", "ed25519", "-N", "", "-f", privatePath)
	if out, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("ssh-keygen failed: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	info, err := runtimeStat(privatePath)
	if err != nil {
		return fmt.Errorf("stat private key after ssh-keygen: %w", err)
	}
	if err := validateOwnedPath(privatePath, info, "regular file", 0o600, false); err != nil {
		return err
	}
	return nil
}

// validateOwnedPath rejects any drift from the expected type, mode,
// ownership, or symlink status of an owned runtime path.
func validateOwnedPath(path string, info os.FileInfo, wantType string, wantMode os.FileMode, wantDir bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("session runtime path is a symlink: %s", path)
	}
	if info.IsDir() != wantDir {
		return fmt.Errorf("session runtime path has wrong type, want %s: %s", wantType, path)
	}
	if info.Mode().Perm() != wantMode {
		return fmt.Errorf("session runtime path has wrong mode %#o, want %#o: %s", info.Mode().Perm(), wantMode, path)
	}
	if uid := ownerUID(info); uid != os.Getuid() {
		return fmt.Errorf("session runtime path expected owner uid %d, found uid %d: %s", os.Getuid(), uid, path)
	}
	return nil
}

// ownerUID returns the owning uid recorded in info's platform-specific
// Stat_t. On non-Unix systems it returns -1 so validateOwnedPath
// rejects the path.
func ownerUID(info os.FileInfo) int {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid)
	}
	return -1
}

// openNoFollow opens or creates path with the given mode and refuses
// to follow symlinks. The combination of an upstream Lstat in
// runtimeStat and O_NOFOLLOW here defeats the symlink swap window
// between check and create.
func openNoFollow(path string, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
}

func sessionPaths(name string) (string, string, error) {
	rt, err := sessionLocation(name)
	if err != nil {
		return "", "", err
	}
	return rt.Dir, rt.Socket, nil
}

// sessionSocket returns the Unix-domain socket path for a session
// without re-walking the project root on every call. It exists so the
// per-request dial in call() does not pay for EvalSymlinks on every
// invocation.
func sessionSocket(name string) (string, error) {
	rt, err := sessionLocation(name)
	if err != nil {
		return "", err
	}
	return rt.Socket, nil
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

func projectRoot() (string, error) {
	return cachedProjectRoot()
}

// cachedProjectRoot caches the symlink-resolved project root for the
// lifetime of the process. Re-resolving on every dial would walk the
// directory tree from CWD to go.mod, then EvalSymlinks it, on every
// supervisor request.
var (
	projectRootOnce  sync.Once
	projectRootValue string
	projectRootErr   error
)

func cachedProjectRoot() (string, error) {
	projectRootOnce.Do(func() {
		dir, err := walkStart()
		if err != nil {
			projectRootErr = err
			return
		}
		for {
			if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
				// EvalSymlinks defeats intermediate-path symlinks that resolve
				// across the canonical root boundary (for example a symlinked
				// parent directory that lands the walk path under /tmp while the
				// canonical root lives elsewhere). A mismatch means an attacker
				// could swap files outside the rooted tree we actually trust.
				resolved, evalErr := filepath.EvalSymlinks(dir)
				if evalErr != nil {
					projectRootErr = evalErr
					return
				}
				if resolved != dir {
					projectRootErr = errors.New("project root is not canonical; resolve symlinks before running")
					return
				}
				projectRootValue = resolved
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				projectRootErr = errors.New("not inside the YANET2 repository")
				return
			}
			dir = parent
		}
	})
	return projectRootValue, projectRootErr
}

// resetProjectRootCache clears the cached project root. Tests call it
// after t.Chdir or after overriding walkStart to ensure the next
// projectRoot call walks the new cwd.
//
// Contract: callers MUST NOT invoke resetProjectRootCache concurrently
// with projectRoot(). The cache fields are unsynchronized; concurrent
// access can yield a torn read of projectRootValue or panic when the
// sync.Once reassignment races with an in-flight Do. Production never
// calls this function; tests call it only during setup or teardown,
// before goroutines begin or after they have joined.
func resetProjectRootCache() {
	projectRootOnce = sync.Once{}
	projectRootValue = ""
	projectRootErr = nil
}
