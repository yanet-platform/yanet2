package framework

import (
	"strings"
	"testing"
)

func TestQEMUHardwareArgsUsesTestTopology(t *testing.T) {
	arguments := strings.Join(qemuHardwareArgs("test", []string{"/tmp/one", "/tmp/two"}), " ")
	for _, expected := range []string{
		"-machine q35,kernel-irqchip=split",
		"intel-iommu,intremap=on,device-iotlb=on",
		"ioh3420,id=pcie.1,chassis=1",
		"ioh3420,id=pcie.2,chassis=2",
		"virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0",
		"stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/one",
		"stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/two",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("QEMU arguments do not contain %q: %s", expected, arguments)
		}
	}
}
