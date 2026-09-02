package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

const (
	baselineSnapshotName    = "baseline"
	baselineTemplateVersion = "v7"
)

// baselineTemplatePath returns the versioned overlay path for a baseline
// snapshot. Bumping the version invalidates locally cached templates when
// any captured guest state stops matching what this harness configures.
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
	PluginDir         string
	Modules           []string
	PacketRecircLimit uint16
}

// DataplaneConfig returns the baseline dataplane YAML used by functional
// tests, optionally requesting runtime module plugins via opts.
func DataplaneConfig(opts DataplaneOptions) string {
	plugins := ""
	cpMemory := "128 MiB"
	packetRecircLimit := ""
	if opts.PacketRecircLimit != 0 {
		packetRecircLimit = fmt.Sprintf(
			"  packet_recirc_limit: %d\n", opts.PacketRecircLimit,
		)
	}
	if opts.PluginDir != "" {
		plugins = "  plugin_dir: " + opts.PluginDir + "\n"
		if len(opts.Modules) > 0 {
			plugins += "  modules:\n"
			for _, module := range opts.Modules {
				plugins += "    - " + module + "\n"
			}
		}
		cpMemory = "160 MiB"
	}

	// The cp_memory value defaults to 128 MiB, matching the built-in main pool.
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
` + packetRecircLimit + plugins + `  instances:
    - dp_memory: 96 MiB
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
  instance_id: 0
  server:
    endpoint: "0.0.0.0:8080"
  auth:
    disabled: true

modules:
  route:
    instance_id: 0
    memory_requirements: 8MB
  route-mpls:
    instance_id: 0
    memory_requirements: 8MB
  decap:
    instance_id: 0
    memory_requirements: 8MB
  dscp:
    instance_id: 0
    memory_requirements: 8MB
  forward:
    instance_id: 0
    memory_requirements: 8MB
  nat64:
    instance_id: 0
    memory_requirements: 8MB
  pdump:
    instance_id: 0
    memory_requirements: 8MB
  acl:
    instance_id: 0
    memory_requirements: 16MB
  fwstate:
    instance_id: 0
    memory_requirements: 16MB
  mirror:
    instance_id: 0
    memory_requirements: 8MB
  blackhole:
    instance_id: 0
    memory_requirements: 8MB

devices:
  plain:
    instance_id: 0
    memory_requirements: 8MB
  vlan:
    instance_id: 0
    memory_requirements: 8MB
`
}

// DefaultForwardConfig returns the baseline forward.yaml used by functional
// tests.
func DefaultForwardConfig() string {
	return `
rules:
  - action:
      target: virtio_user_kni0
      mode: OUT
      counter: to_virtio_user_kni0
    devices:
      - name: 01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
    sources4:
      - "0.0.0.0/0"
    sources6:
      - "::/0"
    destinations4:
      - ` + VMIPv4Host + `/32
    destinations6:
      - ` + VMIPv6Host + `/64
      - "ff02::0/16"
  - action:
      target: 01:00.0
      mode: NONE
      counter: to_pass
    devices:
      - name: 01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
    sources4:
      - "0.0.0.0/0"
    sources6:
      - "::/0"
    destinations4:
      - "0.0.0.0/0"
    destinations6:
      - "::/0"
  - action:
      target: virtio_user_kni0
      mode: OUT
      counter: to_virtio_user_kni0
    devices:
      - name: 01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
  - action:
      target: 01:00.0
      mode: OUT
      counter: to_01:00.0
    devices:
      - name: virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
`
}

// DefaultRouteConfig returns the baseline route0.yaml used by functional
// tests.
func DefaultRouteConfig() string {
	v4Start, v4End := MustPrefixRange("0.0.0.0/0")
	v6Start, v6End := MustPrefixRange("::/0")

	return fmt.Sprintf(`
entries:
  - range:
      start: %q
      end: %q
    nexthops:
      - dst_mac: %q
        src_mac: %q
        device: "01:00.0"
  - range:
      start: %q
      end: %q
    nexthops:
      - dst_mac: %q
        src_mac: %q
        device: "01:00.0"
`, v4Start, v4End, SrcMAC, DstMAC, v6Start, v6End, SrcMAC, DstMAC)
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
	PoolName     string
	BaselineTag  string
	QEMUImage    string
	Dataplane    string
	Controlplane string
	Forward      string
	Route        string
}

// Harness owns a booted, baseline-configured VM pool shared by the tests of
// one functional-test package, together with the YANET configuration used to
// restore a VM to that baseline.
type Harness struct {
	pool         *VMPool
	dataplane    string
	controlplane string
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
		config.Forward != "" || config.Route != ""
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
	baselineTemplate := baselineTemplatePath(qemuImage, baselineTag)

	baseline := &baselineSetup{
		dataplane:    dataplane,
		controlplane: controlplane,
		forward:      forward,
		route:        route,
		poolName:     config.PoolName,
		log:          logger,
	}
	if err = baseline.ensureTemplate(qemuImage, bootedTemplate, baselineTemplate); err != nil {
		return nil, nil, fmt.Errorf("failed to prepare baseline template: %w", err)
	}
	MarkBaselineSaved()

	logger.Infof("Starting VM pool %q with size %d (baseline template: %s)",
		config.PoolName, PoolSize(), baselineTemplate)

	pool, err := NewVMPool(
		PoolSize(), config.PoolName, qemuImage,
		bootedTemplate, baselineTemplate, baselineSnapshotName, logger,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create VM pool: %w", err)
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
		pool:         pool,
		dataplane:    dataplane,
		controlplane: controlplane,
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

	fw.AdoptRunningConfig(m.dataplane, m.controlplane)

	err := fw.RestoreAndReconnect("baseline")
	if err == nil {
		return
	}

	t.Logf("baseline restore failed, falling back to preyanet + fresh StartYANET: %v", err)

	if err := fw.RestoreClean("preyanet"); err != nil {
		t.Fatalf("failed to restore VM to preyanet: %v", err)
	}
	if err := fw.StartYANET(m.dataplane, m.controlplane); err != nil {
		t.Fatalf("failed to start YANET: %v", err)
	}
	if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
		t.Fatalf("failed to configure YANET: %v", err)
	}

	fw.ResetConnections()

	const dpTimeout = 15 * time.Second
	if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
		t.Logf("dataplane not ready after %v, restarting YANET...", dpTimeout)
		if restartErr := fw.RestartYANET(); restartErr != nil {
			t.Fatalf("YANET restart failed: %v", restartErr)
		}
		fw.ResetConnections()
		if err := fw.WaitForDatapathReady(dpTimeout); err != nil {
			t.Fatalf("dataplane not ready after preyanet restore + restart: %v", err)
		}
	}
}

// baselineSetup captures the YANET configuration used while baking a baseline
// template overlay.
type baselineSetup struct {
	dataplane    string
	controlplane string
	forward      string
	route        string
	poolName     string
	log          *zap.SugaredLogger
}

// ensureTemplate makes sure baselineTemplate holds a "baseline" snapshot,
// bootstrapping it from the booted template when the cache is cold.
func (m *baselineSetup) ensureTemplate(qemuImage, bootedTemplate, baselineTemplate string) error {
	if OverlayHasSnapshot(baselineTemplate, baselineSnapshotName) {
		m.log.Infof("Using cached baseline template: %s", baselineTemplate)
		return nil
	}

	m.log.Infof("Baseline template %s not found; bootstrapping from booted template", baselineTemplate)

	prepPool, err := NewVMPool(1, "baseline-prep-"+m.poolName, qemuImage, bootedTemplate, "", "", m.log)
	if err != nil {
		return fmt.Errorf("failed to create baseline prep pool: %w", err)
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
	if err := m.configure(prepFW); err != nil {
		m.dumpMemoryDiagnostics(prepFW)
		return fmt.Errorf("failed to configure baseline: %w", err)
	}
	if err := prepFW.SaveSnapshotKeepUnmounted(baselineSnapshotName); err != nil {
		return fmt.Errorf("failed to save baseline snapshot: %w", err)
	}
	m.log.Info("Baseline snapshot saved successfully")

	if err := prepFW.ExportCurrentOverlay(baselineTemplate); err != nil {
		return fmt.Errorf("failed to export baseline template: %w", err)
	}
	if !OverlayHasSnapshot(baselineTemplate, baselineSnapshotName) {
		return fmt.Errorf("exported baseline template %s is missing snapshot %q", baselineTemplate, baselineSnapshotName)
	}

	m.log.Infof("Baseline template cached at %s", baselineTemplate)
	return nil
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

	if _, err := fw.ExecuteCommands(fw.CommonConfigCommands()...); err != nil {
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
	outputs, errs := fw.ExecuteCommandsSeparately(diagCmds...)
	for idx, cmd := range diagCmds {
		if errs[idx] != nil {
			m.log.Errorf("MEMORY DIAG: %s failed: %v", cmd, errs[idx])
			continue
		}
		m.log.Infof("MEMORY DIAG: %s\n%s\n---", cmd, outputs[idx])
	}
}
