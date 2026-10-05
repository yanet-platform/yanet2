package framework

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Host environment variables that size the functional-test VM.
//
// Unset variables keep the defaults, so suites that set none of them boot
// the VM they always booted.
const (
	// EnvVMCPUs is the number of vCPUs of the VM.
	EnvVMCPUs = "YANET_VM_CPUS"
	// EnvVMMemory is the RAM of the VM in QEMU's -m syntax, e.g. "16G".
	EnvVMMemory = "YANET_VM_MEMORY"
)

const (
	defaultVMCPUs   = 2
	defaultVMMemory = "1G"
	maxVMCPUs       = 256
)

var qemuMemoryPattern = regexp.MustCompile(`^([1-9][0-9]*)([MG])$`)

// VMSize is the size of the functional-test VM.
type VMSize struct {
	CPUs   int
	Memory string
}

// DefaultVMSize returns the size the harness uses when no override is set.
func DefaultVMSize() VMSize {
	return VMSize{CPUs: defaultVMCPUs, Memory: defaultVMMemory}
}

// VMSizeFromEnv reads the VM size overrides from the host environment.
//
// A malformed value is an error naming its variable rather than a silent
// fallback, so a typo never boots a VM of the wrong size.
func VMSizeFromEnv() (VMSize, error) {
	return vmSizeFrom(os.LookupEnv)
}

func vmSizeFrom(lookup func(string) (string, bool)) (VMSize, error) {
	size := DefaultVMSize()
	if raw, ok := lookup(EnvVMCPUs); ok && raw != "" {
		cpus, err := strconv.Atoi(raw)
		if err != nil || cpus < 1 || cpus > maxVMCPUs {
			return VMSize{}, fmt.Errorf("%s=%q: want an integer from 1 to %d", EnvVMCPUs, raw, maxVMCPUs)
		}
		size.CPUs = cpus
	}
	if raw, ok := lookup(EnvVMMemory); ok && raw != "" {
		if !qemuMemoryPattern.MatchString(raw) {
			return VMSize{}, fmt.Errorf("%s=%q: want a size such as 1024M or 16G", EnvVMMemory, raw)
		}
		size.Memory = raw
	}
	return size, nil
}

// IsDefault reports whether the size changes nothing the harness boots.
func (m VMSize) IsDefault() bool {
	return m == DefaultVMSize()
}

// machineSuffix names the QEMU machine shape when it differs from the
// default.
//
// A saved VM snapshot only loads into a machine with the vCPUs and RAM it
// was taken on, so snapshot templates of other shapes are kept apart.
func (m VMSize) machineSuffix() string {
	if m.IsDefault() {
		return ""
	}
	return fmt.Sprintf("-c%d-m%s", m.CPUs, m.Memory)
}

// fingerprint renders the override for the baseline snapshot fingerprint;
// the default size renders empty so default fingerprints stay unchanged.
func (m VMSize) fingerprint() string {
	if m.IsDefault() {
		return ""
	}
	return strings.Join([]string{"cpus", strconv.Itoa(m.CPUs), "memory", m.Memory}, "\x00")
}

// machineArgs is the QEMU machine part of the command line for a VM of
// size: name, vCPUs, RAM and the chipset.
func machineArgs(vmName string, size VMSize) []string {
	return []string{
		"-name", vmName,
		"-smp", strconv.Itoa(size.CPUs),
		"-m", size.Memory,
		"-machine", "q35,kernel-irqchip=split",
		"-cpu", "max",
		"-device", "intel-iommu,intremap=on,device-iotlb=on",
		"-device", "ioh3420,id=pcie.1,chassis=1",
		"-device", "ioh3420,id=pcie.2,chassis=2",
	}
}
