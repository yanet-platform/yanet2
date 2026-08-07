package framework

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
)

const (
	baselineSnapshotName    = "baseline"
	baselineTemplateVersion = "v2"
)

// baselineTemplatePath returns the versioned overlay path for a baseline
// snapshot. Bumping the version invalidates locally cached templates when
// their guest filesystem layout changes.
func baselineTemplatePath(qemuImage, baselineTag string) string {
	return SnapshotImagePath(qemuImage, baselineTag+"-"+baselineTemplateVersion)
}

// DataplaneOptions customizes the baseline dataplane configuration a Harness
// boots with.
//
// PluginDir and Modules drive the dataplane's runtime plugin loader: when
// PluginDir is set the dataplane scans it for module .so plugins and loads
// each name listed in Modules. Leaving both empty produces a configuration
// that relies solely on the modules statically linked into the dataplane
// binary.
type DataplaneOptions struct {
	PluginDir string
	Modules   []string
}

// DataplaneConfig returns the baseline dataplane YAML used by functional
// tests, optionally requesting runtime module plugins via opts.
func DataplaneConfig(opts DataplaneOptions) string {
	plugins := ""
	cpMemory := "134217728"
	if opts.PluginDir != "" {
		plugins = "  plugin_dir: " + opts.PluginDir + "\n"
		if len(opts.Modules) > 0 {
			plugins += "  modules:\n"
			for _, module := range opts.Modules {
				plugins += "    - " + module + "\n"
			}
		}
		cpMemory = "167772160"
	}

	// cp_memory defaults to 128 MiB, matching the built-in main pool used
	// before this configuration was extracted into a builder.
	//
	// The plugin-loading configuration bumps it to 160 MiB to carry headroom
	// for a standalone module control plane's agent, alongside the ephemeral
	// per-update agents the gateway's builtin function and pipeline services
	// attach.
	return `
dataplane:
  storage: /dev/hugepages/yanet
  dpdk_memory: 128
  loglevel: trace
` + plugins + `  instances:
    - dp_memory: 100663296
      cp_memory: ` + cpMemory + `
      numa_id: 0
  devices:
    - port_name: 01:00.0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
          num_mbufs: 2048
    - port_name: virtio_user_kni0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
          num_mbufs: 2048
  connections:
    - src_device: 01:00.0
      dst_device: virtio_user_kni0
    - src_device: virtio_user_kni0
      dst_device: 01:00.0
`
}

// DefaultControlplaneConfig returns the baseline controlplane YAML used by
// functional tests.
func DefaultControlplaneConfig() string {
	return `
logging:
  level: debug

gateway:
  server:
    endpoint: "0.0.0.0:8080"
  auth:
    disabled: true

modules:
  route:
    link_map:
      kni0: 01:00.0
    memory_requirements: 8MB
  route-mpls:
    memory_requirements: 8MB
  decap:
    memory_requirements: 8MB
  dscp:
    memory_requirements: 8MB
  forward:
    memory_requirements: 8MB
  nat64:
    memory_requirements: 8MB
  pdump:
    memory_requirements: 8MB
  acl:
    memory_requirements: 16MB

devices:
  plain:
    memory_requirements: 8MB
  vlan:
    memory_requirements: 8MB
`
}

// DefaultForwardConfig returns the baseline forward.yaml used by functional
// tests.
func DefaultForwardConfig() string {
	return `
rules:
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - ` + VMIPv4Host + `/32
      - ` + VMIPv6Host + `/64
      - "ff02::0/16"
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_pass
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - "0.0.0.0/0"
      - "::/0"
    mode: None
    devices:
      - 01:00.0
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - virtio_user_kni0
`
}

// DefaultRouteConfig returns the baseline route0.yaml used by functional
// tests.
func DefaultRouteConfig() string {
	return `
entries:
  - prefix: "0.0.0.0/0"
    nexthops:
      - dst_mac: "` + SrcMAC + `"
        src_mac: "` + DstMAC + `"
        device: "01:00.0"
  - prefix: "::/0"
    nexthops:
      - dst_mac: "` + SrcMAC + `"
        src_mac: "` + DstMAC + `"
        device: "01:00.0"
`
}

// HarnessConfig describes the VM pool a functional-test package boots.
//
// PoolName names the pool's VM instances and logging scope. BaselineTag
// scopes the cached baseline template overlay so pools that boot different
// YANET configurations never collide on one cached snapshot file. QEMUImage,
// when empty, defaults to the shared functional-test image resolved from the
// project root. The YAML fields default to the Default*/DataplaneConfig
// builders when left empty.
type HarnessConfig struct {
	PoolName         string
	PoolSize         int
	BaselineTag      string
	QEMUImage        string
	Dataplane        string
	Controlplane     string
	Forward          string
	Route            string
	EnableSSHForward bool
	Prepare          func(*TestFramework) error
	AfterStart       func(*TestFramework) error
	ProfileReady     func(*TestFramework) error
	FingerprintFiles []string
	SkipCommonConfig bool
	ForceStop        bool
}

// Harness owns a booted, baseline-configured VM pool shared by the tests of
// one functional-test package, together with the YANET configuration used to
// restore a VM to that baseline.
type Harness struct {
	pool             *VMPool
	dataplane        string
	controlplane     string
	afterStart       func(*TestFramework) error
	profileReady     func(*TestFramework) error
	skipCommonConfig bool
}

// Pool returns the underlying VM pool.
func (m *Harness) Pool() *VMPool {
	return m.pool
}

// Shutdown stops every VM in the pool.
func (m *Harness) Shutdown() error {
	return m.pool.Shutdown()
}

// resolveQEMUImage returns the configured QEMU image, or the shared
// functional-test image resolved from the project root when unset.
func resolveQEMUImage(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if env := os.Getenv("YANET_QEMU_IMAGE"); env != "" {
		return env, nil
	}
	root, err := findProjectRoot()
	if err != nil {
		return "", fmt.Errorf("failed to locate project root for QEMU image: %w", err)
	}
	return filepath.Join(root, "tests", "functional", "yanet-test.qcow2"), nil
}

// newHarnessLogger builds the logger a harness runs with, mirroring the
// verbosity split the functional tests rely on: quiet by default, verbose to
// test.log when debugging is enabled.
func newHarnessLogger() (*zap.SugaredLogger, func(), error) {
	config := zap.NewDevelopmentConfig()
	if !IsDebugEnabled() {
		config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	} else {
		config.OutputPaths = []string{"test.log"}
		config.ErrorOutputPaths = []string{"stderr", "test.log"}
	}
	logger, err := config.Build()
	if err != nil {
		return nil, nil, err
	}
	return logger.Sugar(), func() { _ = logger.Sync() }, nil
}

// SetupHarness prepares a baseline VM pool for a functional-test package.
//
// It ensures the baseline template overlay exists (bootstrapping it from the
// booted template when missing), starts the pool, waits for readiness, and
// pauses idle VM CPUs. The returned cleanup releases the harness logger; the
// caller owns Shutdown of the returned Harness.
func SetupHarness(config HarnessConfig) (_ *Harness, cleanup func(), err error) {
	logger, syncLog, err := newHarnessLogger()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build harness logger: %w", err)
	}
	defer func() {
		if err != nil {
			syncLog()
		}
	}()

	qemuImage, err := resolveQEMUImage(config.QEMUImage)
	if err != nil {
		return nil, nil, err
	}

	// A custom YAML must not reuse the shared "baseline" cache silently.
	customConfig := config.Dataplane != "" || config.Controlplane != "" ||
		config.Forward != "" || config.Route != "" || config.Prepare != nil ||
		config.AfterStart != nil || config.ProfileReady != nil ||
		config.SkipCommonConfig ||
		len(config.FingerprintFiles) != 0
	if config.BaselineTag == "" && customConfig {
		return nil, nil, fmt.Errorf("failed to set up harness: a custom YAML configuration requires a non-empty BaselineTag so it does not reuse the shared baseline cache")
	}

	dataplane := config.Dataplane
	if dataplane == "" {
		dataplane = DataplaneConfig(DataplaneOptions{})
	}
	controlplane := config.Controlplane
	if controlplane == "" {
		controlplane = DefaultControlplaneConfig()
	}
	forward := config.Forward
	if forward == "" {
		forward = DefaultForwardConfig()
	}
	route := config.Route
	if route == "" {
		route = DefaultRouteConfig()
	}

	baselineTag := config.BaselineTag
	if baselineTag == "" {
		baselineTag = baselineSnapshotName
	}

	bootedTemplate := BootedImagePath(qemuImage)
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to locate project root for baseline: %w", err)
	}
	fingerprint, err := baselineFingerprint(projectRoot, qemuImage, dataplane, controlplane, forward, route, config.FingerprintFiles, config.SkipCommonConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("fingerprint baseline: %w", err)
	}
	baselineTemplate := baselineTemplatePath(qemuImage, baselineTag+"-"+fingerprint[:16])

	baseline := &baselineSetup{
		dataplane:        dataplane,
		controlplane:     controlplane,
		forward:          forward,
		route:            route,
		poolName:         config.PoolName,
		fingerprint:      fingerprint,
		log:              logger,
		prepare:          config.Prepare,
		afterStart:       config.AfterStart,
		profileReady:     config.ProfileReady,
		skipCommonConfig: config.SkipCommonConfig,
		forceStop:        config.ForceStop,
	}
	if err = baseline.ensureTemplate(qemuImage, bootedTemplate, baselineTemplate, baselineTag); err != nil {
		return nil, nil, fmt.Errorf("failed to prepare baseline template: %w", err)
	}
	MarkBaselineSaved()

	poolSize := config.PoolSize
	if poolSize < 1 {
		poolSize = PoolSize()
	}
	logger.Infof("Starting VM pool %q with size %d (baseline template: %s)",
		config.PoolName, poolSize, baselineTemplate)

	pool, err := NewVMPool(
		poolSize, config.PoolName, qemuImage,
		bootedTemplate, baselineTemplate, baselineSnapshotName, config.EnableSSHForward, logger,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create VM pool: %w", err)
	}
	if config.ForceStop {
		pool.ForceStop()
	}

	defer func() {
		if err != nil {
			_ = pool.Shutdown()
		}
	}()

	if err = pool.StartAll(); err != nil {
		return nil, nil, fmt.Errorf("failed to start VM pool: %w", err)
	}
	if err = pool.WaitAllReady(VMReadyTimeout()); err != nil {
		return nil, nil, fmt.Errorf("failed to wait for VM pool readiness: %w", err)
	}

	// Pause all VM CPUs now that baseline snapshots are saved. Idle VMs would
	// otherwise keep DPDK's busy-poll loop running and consume host CPU,
	// starving the active VM's packet processing. Each VM resumes when
	// RestoreSnapshot calls loadvm+cont.
	pool.StopAllCPU()

	harness := &Harness{
		pool:             pool,
		dataplane:        dataplane,
		controlplane:     controlplane,
		afterStart:       config.AfterStart,
		profileReady:     config.ProfileReady,
		skipCommonConfig: config.SkipCommonConfig,
	}
	return harness, syncLog, nil
}

// WithBootedVM acquires a pooled VM, restores it to a working YANET baseline,
// runs fn, then returns the VM to the pool.
func (m *Harness) WithBootedVM(t *testing.T, fn func(fw *TestFramework)) {
	t.Helper()
	base := m.pool.Acquire()
	t.Cleanup(func() {
		m.pool.Release(base)
	})
	fw := base.ForTest(t)
	m.RestoreBooted(t, fw)
	fn(fw)
}

// RestoreBooted restores fw to a working YANET state.
//
// It tries the fast path (baseline snapshot with YANET already running)
// first, and falls back to the slow path (preyanet snapshot plus a fresh
// StartYANET) only when the baseline restore fails.
func (m *Harness) RestoreBooted(t *testing.T, fw *TestFramework) {
	t.Helper()
	if err := m.Restore(fw); err != nil {
		t.Fatalf("failed to restore VM to a working YANET baseline: %v", err)
	}
}

// Restore restores fw to a working YANET baseline without depending on the
// testing package. Lab tools use this method to get the same fast-path and
// fallback behavior as functional tests.
//
// The fast path differs from the older RestoreBooted contract: it adopts the
// running config first, then restores the "baseline" snapshot and resets
// connections. When no profileReady hook is set it waits for datapath
// readiness; functional tests that pass no hooks see the same net behavior as
// before. The fallback additionally runs the afterStart/profileReady hooks
// after a fresh StartYANET. Both hook paths are no-ops for nil hooks.
func (m *Harness) Restore(fw *TestFramework) error {
	fw.AdoptRunningConfig(m.dataplane, m.controlplane)
	if err := fw.RestoreClean("baseline"); err == nil {
		fw.ResetConnections()
		if m.profileReady != nil {
			if err := m.profileReady(fw); err == nil {
				return nil
			} else {
				log.Printf("harness: fast-path profileReady failed, falling back: %v", err)
			}
		} else if err := fw.WaitForDatapathReady(15 * time.Second); err == nil {
			return nil
		} else {
			log.Printf("harness: fast-path datapath ready failed, falling back: %v", err)
		}
	} else {
		log.Printf("harness: fast-path baseline restore failed, falling back: %v", err)
	}

	if err := fw.RestoreClean("preyanet"); err != nil {
		return fmt.Errorf("restore VM to preyanet: %w", err)
	}
	if err := fw.StartYANET(m.dataplane, m.controlplane); err != nil {
		return fmt.Errorf("start YANET: %w", err)
	}
	if !m.skipCommonConfig {
		if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
			return fmt.Errorf("configure YANET: %w", err)
		}
	}

	fw.ResetConnections()

	const dpTimeout = 15 * time.Second
	if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
		if err := fw.RestartYANET(); err != nil {
			return fmt.Errorf("restart YANET: %w", err)
		}
		fw.ResetConnections()
		if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
			return fmt.Errorf("wait for dataplane after restart: %w", err)
		}
	}
	return runProfileHooks(fw, m.afterStart, m.profileReady)
}

func runProfileHooks(fw *TestFramework, afterStart, profileReady func(*TestFramework) error) error {
	if afterStart != nil {
		if err := afterStart(fw); err != nil {
			return fmt.Errorf("start profile: %w", err)
		}
	}
	if profileReady != nil {
		if err := profileReady(fw); err != nil {
			return fmt.Errorf("wait for profile readiness: %w", err)
		}
	}
	return nil
}

// baselineSetup captures the YANET configuration used while baking a baseline
// template overlay.
type baselineSetup struct {
	dataplane        string
	controlplane     string
	forward          string
	route            string
	poolName         string
	fingerprint      string
	log              *zap.SugaredLogger
	prepare          func(*TestFramework) error
	afterStart       func(*TestFramework) error
	profileReady     func(*TestFramework) error
	skipCommonConfig bool
	forceStop        bool
}

// ensureTemplate makes sure baselineTemplate holds a "baseline" snapshot,
// bootstrapping it from the booted template when the cache is cold.
func (m *baselineSetup) ensureTemplate(qemuImage, bootedTemplate, baselineTemplate, baselineTag string) error {
	lock, err := acquireBaselineLock(baselineTemplate)
	if err != nil {
		return err
	}
	defer lock.Close()
	if OverlayHasSnapshot(baselineTemplate, baselineSnapshotName) && fingerprintMatches(baselineTemplate, m.fingerprint) {
		m.log.Infof("Using cached baseline template: %s", baselineTemplate)
		return nil
	}

	m.log.Infof("Baseline template %s not found; bootstrapping from booted template", baselineTemplate)

	prepPool, err := NewVMPool(1, "baseline-prep-"+m.poolName, qemuImage, bootedTemplate, "", "", false, m.log)
	if err != nil {
		return fmt.Errorf("failed to create baseline prep pool: %w", err)
	}
	if m.forceStop {
		prepPool.ForceStop()
	}
	defer func() {
		if err := prepPool.Shutdown(); err != nil {
			m.log.Errorf("Failed to shut down baseline prep pool: %v", err)
		}
	}()

	if err := prepPool.StartAll(); err != nil {
		return fmt.Errorf("failed to start baseline prep pool: %w", err)
	}
	if err := prepPool.WaitAllReady(VMReadyTimeout()); err != nil {
		return fmt.Errorf("baseline prep pool not ready: %w", err)
	}

	prepFW := prepPool.Acquire()
	defer prepPool.Release(prepFW)

	if err := prepFW.PrepareLocalStorage(); err != nil {
		return fmt.Errorf("failed to prepare local storage: %w", err)
	}
	if m.prepare != nil {
		if err := m.prepare(prepFW); err != nil {
			return fmt.Errorf("failed to prepare profile: %w", err)
		}
	}
	if err := m.configure(prepFW); err != nil {
		return fmt.Errorf("failed to configure baseline: %w", err)
	}
	if err := prepFW.SaveSnapshotKeepUnmounted(baselineSnapshotName); err != nil {
		return fmt.Errorf("failed to save baseline snapshot: %w", err)
	}
	m.log.Info("Baseline snapshot saved successfully")

	temporary := baselineTemplate + ".tmp.qcow2"
	_ = os.Remove(temporary)
	if err := prepFW.ExportCurrentOverlay(temporary); err != nil {
		return fmt.Errorf("failed to export baseline template: %w", err)
	}
	defer os.Remove(temporary)
	if !OverlayHasSnapshot(temporary, baselineSnapshotName) {
		return fmt.Errorf("exported baseline template %s is missing snapshot %q", temporary, baselineSnapshotName)
	}
	if err := os.Rename(temporary, baselineTemplate); err != nil {
		return fmt.Errorf("replace baseline template: %w", err)
	}
	if err := writeFingerprint(baselineTemplate, m.fingerprint); err != nil {
		return fmt.Errorf("write baseline fingerprint: %w", err)
	}

	m.log.Infof("Baseline template cached at %s", baselineTemplate)

	// Prune superseded fingerprinted templates with the same baseline tag
	// to avoid unbounded overlay accumulation.
	dir := filepath.Dir(baselineTemplate)
	base := filepath.Base(baselineTemplate)
	prefix := strings.TrimSuffix(base, "-"+baselineTemplateVersion+".qcow2")
	prefix = strings.TrimSuffix(prefix, "-"+m.fingerprint[:16])
	if matches, err := filepath.Glob(filepath.Join(dir, prefix+"-"+baselineTemplateVersion+".qcow2")); err == nil {
		for _, old := range matches {
			if old != baselineTemplate {
				_ = os.Remove(old)
				_ = os.Remove(old + ".sha256")
				m.log.Infof("Pruned stale baseline template %s", old)
			}
		}
	}
	return nil
}

func acquireBaselineLock(baselineTemplate string) (*os.File, error) {
	lock, err := os.OpenFile(baselineTemplate+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func baselineFingerprint(projectRoot, qemuImage, dataplane, controlplane, forward, route string, extraFiles []string, skipCommonConfig bool) (string, error) {
	hash := sha256.New()
	for _, value := range []string{"dataplane", dataplane, "controlplane", controlplane, "forward", forward, "route", route, "skipCommonConfig", strconv.FormatBool(skipCommonConfig)} {
		_, _ = io.WriteString(hash, value)
		_, _ = io.WriteString(hash, "\x00")
	}

	image, err := os.Stat(qemuImage)
	if err != nil {
		return "", err
	}
	imagePath, err := filepath.EvalSymlinks(qemuImage)
	if err != nil {
		return "", err
	}
	_, _ = io.WriteString(hash, imagePath)
	_, _ = io.WriteString(hash, fmt.Sprintf("\x00%d\x00%d", image.Size(), image.ModTime().UnixNano()))

	paths := []string{
		filepath.Join(projectRoot, "build", "dataplane", "yanet-dataplane"),
		filepath.Join(projectRoot, "build", "controlplane", "yanet-controlplane"),
		filepath.Join(projectRoot, "subprojects", "dpdk", "usertools", "dpdk-devbind.py"),
	}
	for _, path := range extraFiles {
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectRoot, path)
		}
		paths = append(paths, path)
	}
	for _, name := range CLIBinaryNames {
		paths = append(paths, filepath.Join(projectRoot, "target", "release", name))
	}
	plugins, err := filepath.Glob(filepath.Join(projectRoot, "build", "modules", "*", "dataplane", "*_dp_plugin.so"))
	if err != nil {
		return "", err
	}
	paths = append(paths, plugins...)
	sort.Strings(paths)
	for _, path := range paths {
		if err := statFingerprint(hash, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// statFingerprint records path, size, and mtime for O(1) fingerprint cost.
// Meson/cargo rebuilds update both size and mtime, so fingerprints change
// naturally. A manual same-size same-mtime replacement (for example `cp -p`
// of a binary) is not detected — rerun `meson compile` or delete the cached
// template to force re-baselining.
func statFingerprint(hash io.Writer, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano())
	return nil
}

func fingerprintMatches(baselineTemplate, want string) bool {
	data, err := os.ReadFile(baselineTemplate + ".sha256")
	return err == nil && string(data) == want
}

func writeFingerprint(baselineTemplate, value string) error {
	temporary := baselineTemplate + ".sha256.tmp"
	if err := os.WriteFile(temporary, []byte(value), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, baselineTemplate+".sha256")
}

// configure writes the baseline config files, captures a "preyanet" fallback
// snapshot, then starts YANET and applies the common runtime configuration.
func (m *baselineSetup) configure(fw *TestFramework) error {
	// Write config files BEFORE starting YANET so the "preyanet" snapshot
	// captures them on disk without a running dataplane.
	if err := fw.CreateForwardConfig(m.forward); err != nil {
		return err
	}
	if err := fw.createGuestFile(fw.Paths.ConfigDir+"/route0.yaml", m.route); err != nil {
		return err
	}

	// Save "preyanet" snapshot: OS booted, binaries copied to /tmp/yanet,
	// config files written, 9P unmounted, no YANET running. Used as the
	// fallback source when baseline restore fails.
	if err := fw.SaveSnapshotKeepUnmounted("preyanet"); err != nil {
		return err
	}
	m.log.Info("Pre-yanet snapshot saved")

	// Remount 9P before starting YANET so host-backed logs remain available.
	if err := fw.Mount9P(); err != nil {
		return err
	}

	if err := fw.StartYANET(m.dataplane, m.controlplane); err != nil {
		return err
	}

	m.dumpMemoryDiagnostics(fw)

	if !m.skipCommonConfig {
		if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
			return err
		}
	}
	if err := runProfileHooks(fw, m.afterStart, m.profileReady); err != nil {
		return err
	}

	return nil
}

// dumpMemoryDiagnostics logs guest memory and YANET process state, aiding
// diagnosis of allocation failures during baseline setup.
func (m *baselineSetup) dumpMemoryDiagnostics(fw *TestFramework) {
	diagCmds := []string{
		"echo '=== HUGEPAGES ===' && cat /proc/meminfo | grep -i huge",
		"echo '=== FREE ===' && free -h",
		"echo '=== PROCESS MEMORY ===' && ps aux | grep yanet",
		"echo '=== HUGEPAGE FILE ===' && ls -lh /dev/hugepages/yanet",
		"echo '=== DATAPLANE LOG ===' && cat /tmp/yanet/logs/yanet-dataplane.log",
		"echo '=== CONTROLPLANE LOG ===' && cat /tmp/yanet/logs/yanet-controlplane.log",
	}
	outputs, err := fw.ExecuteCommands(diagCmds...)
	if err != nil {
		m.log.Errorf("MEMORY DIAG: error collecting diagnostics: %v", err)
		return
	}
	for idx, cmd := range diagCmds {
		m.log.Infof("MEMORY DIAG: %s\n%s\n---", cmd, outputs[idx])
	}
}
