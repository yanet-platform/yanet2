package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// VMPool manages a pool of QEMU virtual machines for parallel test execution.
// Each VM slot holds a long-lived QEMUManager + framework instance. Pool slots
// are started from a pre-prepared template overlay (typically baseline, falling
// back to booted) and remain running between tests. Per-test isolation is
// achieved via RestoreBooted(), which does a fast loadvm+reconnect without
// restarting the QEMU process.
//
// Pool size is controlled by YANET_VM_POOL_SIZE (default 1).
type VMPool struct {
	vms                  []*poolEntry
	available            chan int // channel of available slot indices
	size                 int
	bootedTemplate       string // path to the canonical booted template overlay
	templateOverlay      string // preferred startup template overlay for pool VMs
	templateSnapshotName string // snapshot loaded from templateOverlay
	log                  *zap.SugaredLogger
}

type poolEntry struct {
	manager *QEMUManager
	fw      *TestFramework
}

type poolResult struct {
	idx int
	err error
}

// runWithRecovery runs fn in the current goroutine, sending the result to ch.
// If fn panics, the panic is recovered and reported as an error result.
func (p *VMPool) runWithRecovery(idx int, ch chan<- poolResult, fn func() error) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Errorf("pool goroutine %d recovered panic: %v", idx, r)
			ch <- poolResult{idx: idx, err: fmt.Errorf("panic: %v", r)}
		}
	}()
	ch <- poolResult{idx: idx, err: fn()}
}

// PoolSize returns the desired VM pool size from the environment.
func PoolSize() int {
	s := os.Getenv("YANET_VM_POOL_SIZE")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// NewVMPool creates a pool of size VMs. The bootedTemplate overlay is the
// canonical fallback source for pool slots. If templateOverlay/templateSnapshotName
// are provided and valid, StartAll prefers them. Otherwise it falls back to the
// booted template and bootstraps it if needed.
//
// VMs are not started yet - call StartAll after creating the pool.
func NewVMPool(size int, baseName string, qemuImage string, bootedTemplate string, templateOverlay string, templateSnapshotName string, log *zap.SugaredLogger) (_ *VMPool, err error) {
	if size < 1 {
		size = 1
	}

	pool := &VMPool{
		vms:                  make([]*poolEntry, size),
		available:            make(chan int, size),
		size:                 size,
		bootedTemplate:       bootedTemplate,
		templateOverlay:      templateOverlay,
		templateSnapshotName: templateSnapshotName,
		log:                  log.Named("VMPool"),
	}
	defer func() {
		if err == nil {
			return
		}
		for _, entry := range pool.vms {
			if entry == nil || entry.fw == nil {
				continue
			}
			_ = entry.fw.Stop()
		}
	}()

	for i := range size {
		name := baseName
		if size > 1 {
			name = fmt.Sprintf("%s-%d", baseName, i)
		}
		qemu, qemuErr := NewQEMUManager(name, qemuImage, log)
		if qemuErr != nil {
			err = qemuErr
			return nil, fmt.Errorf("failed to create QEMU manager for pool slot %d: %w", i, err)
		}

		fw := &TestFramework{
			qemu:  qemu,
			log:   log.Named(name),
			Paths: DefaultGuestPaths(),
			socketClients: &socketClientsCache{
				clients: make(map[int]*SocketClient),
			},
			PacketParser: NewPacketParser(),
		}
		cli, cliErr := NewCLIManager(qemu, CLIWithLog(log))
		if cliErr != nil {
			err = cliErr
			return nil, fmt.Errorf("failed to create CLI manager for pool slot %d: %w", i, err)
		}
		fw.cli = cli

		pool.vms[i] = &poolEntry{manager: qemu, fw: fw}
	}

	return pool, nil
}

// Size returns the number of VM slots in the pool.
func (p *VMPool) Size() int {
	return p.size
}

// StartAll starts all VM slots. It prefers the configured template overlay when
// available and otherwise falls back to the cached booted template.
//
//   - Fast path: preferred template or booted template already exists -> copy it
//     to each slot and boot via -loadvm <snapshot>.
//   - Slow path: booted template missing -> boot VM0 from scratch, save the
//     booted snapshot, cache it as the template, then restart all slots from it.
//
// After StartAll all slots are added to the available channel.
func (p *VMPool) StartAll() error {
	if p.templateOverlay != "" && p.templateSnapshotName != "" && OverlayHasSnapshot(p.templateOverlay, p.templateSnapshotName) {
		p.log.Infof("Preferred template found at %s; starting all slots from %q", p.templateOverlay, p.templateSnapshotName)
		return p.startAllFromTemplate(p.templateOverlay, p.templateSnapshotName)
	}

	if _, err := os.Stat(p.bootedTemplate); err == nil && HasBootedSnapshot(p.bootedTemplate) {
		p.log.Infof("Booted template found at %s; starting all slots from it", p.bootedTemplate)
		return p.startAllFromTemplate(p.bootedTemplate, BootedSnapshotName)
	}

	// Slow path: bootstrap from a cold boot.
	p.log.Warnf("Booted template not found at %s; bootstrapping (run 'make prepare-vm' to avoid this delay)", p.bootedTemplate)
	return p.bootstrapTemplate()
}

// validateBootedTemplate runs 3 sequential loadvm+smoke cycles on a temporary VM
// to verify that the booted template is stable before using it for all pool slots.
func (p *VMPool) validateBootedTemplate() error {
	vm0ImagePath := p.vms[0].manager.ImagePath

	valMgr, err := NewQEMUManager("validate-booted", vm0ImagePath, p.log)
	if err != nil {
		return fmt.Errorf("failed to create validation manager: %w", err)
	}
	valMgr.TemplateOverlay = p.bootedTemplate
	valMgr.TemplateSnapshotName = BootedSnapshotName

	valFW := &TestFramework{
		qemu: valMgr,
		log:  p.log.Named("validate-booted"),
		socketClients: &socketClientsCache{
			clients: make(map[int]*SocketClient),
		},
		PacketParser: NewPacketParser(),
	}
	cli, err := NewCLIManager(valMgr, CLIWithLog(p.log))
	if err != nil {
		return fmt.Errorf("failed to create validation CLI: %w", err)
	}
	valFW.cli = cli

	p.log.Infof("Starting validation VM from booted template...")
	if _, err := valFW.Start(); err != nil {
		return fmt.Errorf("validation VM start failed: %w", err)
	}
	defer valFW.Stop() //nolint:errcheck

	if err := valMgr.WaitForReady(60 * time.Second); err != nil {
		return fmt.Errorf("validation VM not ready after start: %w", err)
	}

	for i := 1; i <= 3; i++ {
		p.log.Infof("Validation cycle %d/3...", i)

		// Unmount 9P before loadvm (required by QEMU).
		if err := valFW.Unmount9P(); err != nil {
			p.log.Debugf("validation unmount 9P (non-fatal): %v", err)
		}

		if err := valMgr.RestoreBooted(); err != nil {
			return fmt.Errorf("cycle %d: restore failed: %w", i, err)
		}

		// Remount 9P and run a simple smoke command.
		if err := valFW.Mount9P(); err != nil {
			p.log.Debugf("validation mount 9P (non-fatal): %v", err)
		}

		if _, err := valFW.ExecuteCommand("echo ok"); err != nil {
			return fmt.Errorf("cycle %d: smoke command failed: %w", i, err)
		}
		p.log.Infof("Validation cycle %d/3 passed", i)
	}

	return nil
}

// startAllFromTemplate starts all slots from the given cached template.
func (p *VMPool) startAllFromTemplate(templateOverlay string, snapshotName string) error {
	// Point every slot at the template so Start() copies it.
	for _, entry := range p.vms {
		entry.manager.TemplateOverlay = templateOverlay
		entry.manager.TemplateSnapshotName = snapshotName
	}

	ch := make(chan poolResult, len(p.vms))
	for i, entry := range p.vms {
		fw := entry.fw
		go p.runWithRecovery(i, ch, func() error {
			p.log.Infof("Starting VM %d/%d (%s) from template %q...", i+1, p.size, fw.qemu.Name, snapshotName)
			_, err := fw.Start()
			return err
		})
	}
	var firstErr error
	for range p.vms {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to start VM %d: %w", r.idx, r.err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	for i := range p.vms {
		p.available <- i
	}
	return nil
}

// bootstrapTemplate cold-boots VM0, saves the booted snapshot, caches the
// overlay as the canonical template, then restarts all slots from the template.
func (p *VMPool) bootstrapTemplate() error {
	vm0 := p.vms[0]
	p.log.Infof("Cold-booting VM0 (%s) to create booted template...", vm0.manager.Name)

	if _, err := vm0.fw.Start(); err != nil {
		return fmt.Errorf("failed to start VM0 for bootstrap: %w", err)
	}
	if err := vm0.manager.WaitForReady(120 * time.Second); err != nil {
		_ = vm0.fw.Stop()
		return fmt.Errorf("VM0 not ready during bootstrap: %w", err)
	}

	// Unmount 9P so savevm can proceed (QEMU blocks savevm with VirtFS active).
	vm0.manager.Ninepmounted.Store(true)
	if err := vm0.fw.Unmount9P(); err != nil {
		p.log.Warnf("Failed to unmount 9P before snapshot (non-fatal): %v", err)
	}

	// Save the booted snapshot and get the overlay path.
	overlayPath, err := vm0.manager.SaveBootedOverlay()
	if err != nil {
		_ = vm0.fw.Stop()
		return fmt.Errorf("failed to save booted snapshot: %w", err)
	}

	// Cache the overlay as the canonical template before stopping VM0
	// (Stop() removes WorkDir which contains the overlay).
	if err := os.MkdirAll(filepath.Dir(p.bootedTemplate), 0755); err != nil {
		p.log.Warnf("Failed to create template cache dir: %v", err)
	} else if err := copyFile(overlayPath, p.bootedTemplate); err != nil {
		p.log.Warnf("Failed to cache booted template: %v", err)
		if rerr := os.Remove(p.bootedTemplate); rerr != nil && !os.IsNotExist(rerr) {
			p.log.Warnf("Failed to remove stale booted template %s: %v", p.bootedTemplate, rerr)
		}
	} else {
		p.log.Infof("Booted template cached at %s", p.bootedTemplate)
	}

	// Stop VM0 (overlay is now cached; WorkDir will be cleaned up).
	if err := vm0.fw.Stop(); err != nil {
		p.log.Warnf("Failed to stop VM0 after bootstrap: %v", err)
	}

	// Validate the cached template with 3 restore cycles before using it.
	if _, err := os.Stat(p.bootedTemplate); err == nil {
		p.log.Infof("Validating booted template with 3 restore cycles...")
		if err := p.validateBootedTemplate(); err != nil {
			p.log.Warnf("Booted template validation failed: %v; recreating...", err)
			if rerr := os.Remove(p.bootedTemplate); rerr != nil && !os.IsNotExist(rerr) {
				p.log.Warnf("Failed to remove stale booted template %s: %v", p.bootedTemplate, rerr)
			}
			// Fall through to cold boot fallback below.
		} else {
			p.log.Infof("Booted template validation passed")
			return p.startAllFromTemplate(p.bootedTemplate, BootedSnapshotName)
		}
	}

	// Template caching failed — fall back to cold boot for all slots.
	p.log.Warn("Template caching failed; starting all slots with cold boot")
	ch := make(chan poolResult, len(p.vms))
	for i, entry := range p.vms {
		fw := entry.fw
		go p.runWithRecovery(i, ch, func() error {
			_, err := fw.Start()
			return err
		})
	}
	var firstErr error
	for range p.vms {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("failed to start VM %d: %w", r.idx, r.err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	for i := range p.vms {
		p.available <- i
	}
	return nil
}

// WaitAllReady waits for every VM in the pool to reach the shell prompt.
func (p *VMPool) WaitAllReady(timeout time.Duration) error {
	ch := make(chan poolResult, len(p.vms))
	for i, entry := range p.vms {
		mgr := entry.manager
		go p.runWithRecovery(i, ch, func() error {
			return mgr.WaitForReady(timeout)
		})
	}
	var firstErr error
	for range p.vms {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("VM %d failed to become ready: %w", r.idx, r.err)
		}
	}
	return firstErr
}

// ForEachParallel calls fn for each VM's framework instance in parallel.
func (p *VMPool) ForEachParallel(fn func(idx int, fw *TestFramework) error) error {
	ch := make(chan poolResult, len(p.vms))
	for i, entry := range p.vms {
		fw := entry.fw
		go p.runWithRecovery(i, ch, func() error {
			return fn(i, fw)
		})
	}
	var firstErr error
	for range p.vms {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("ForEachParallel failed on VM %d: %w", r.idx, r.err)
		}
	}
	return firstErr
}

// StopAllCPU pauses every VM's CPU via the QEMU monitor. Useful after initial
// setup to avoid idle DPDK busy-polling consuming host CPU.
func (p *VMPool) StopAllCPU() {
	for i, entry := range p.vms {
		if _, err := entry.manager.SendMonitorCommand("stop"); err != nil {
			p.log.Warnf("VM %d stop: %v (non-fatal)", i, err)
		} else {
			p.log.Debugf("VM %d CPU paused", i)
		}
	}
}

// Acquire blocks until a VM slot is available and returns its framework
// instance. The caller MUST call Release when done.
func (p *VMPool) Acquire() *TestFramework {
	idx := <-p.available
	p.log.Debugf("Acquired VM slot %d", idx)
	return p.vms[idx].fw
}

// Release returns a VM slot back to the pool.
func (p *VMPool) Release(fw *TestFramework) {
	for i, e := range p.vms {
		if e.fw == fw {
			p.log.Debugf("Released VM slot %d", i)
			p.available <- i
			return
		}
	}
	p.log.Error("Release called with unknown framework")
}

// Shutdown stops all VM slots in the pool.
func (p *VMPool) Shutdown() error {
	var errs []error
	for i, entry := range p.vms {
		if err := entry.fw.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop VM %d: %w", i, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("pool shutdown errors: %v", errs)
	}
	return nil
}
