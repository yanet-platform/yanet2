#!/bin/sh

# Help function
show_help() {
    cat << EOF
Usage: $0 [options] <image-file>

Options:
    --help          Show this help message
    GUI=1           Start with GUI enabled
    SMP=N           Set number of CPU cores (default: 4)
    FORCE_VERSION=X Use specific kernel version

Example:
    $0 noble-server-cloudimg-amd64.img
    GUI=1 $0 noble-server-cloudimg-amd64.img
    SMP=8 $0 noble-server-cloudimg-amd64.img
EOF
    exit 0
}

# Check arguments
if [ "$1" = "--help" ] || [ "$1" = "-h" ]; then
    show_help
fi

if [ -z "$1" ]; then
    echo "Error: Image file not specified"
    show_help
fi

if [ ! -f "$1" ]; then
    echo "Error: Image file '$1' not found"
    exit 1
fi

# Detect OS
OS_TYPE=$(uname -s)

# Common parameters
VERSION="6.14.0-061400-generic"
VERSION="${FORCE_VERSION:-$VERSION}"
KERNEL="-kernel shared/kernels/vmlinuz-$VERSION"
INITRD="-initrd shared/kernels/initrd.img-$VERSION"
APPEND='-append "root=/dev/vda console=ttyS0 intel-iommu=on iommu=pt"'

MACHINE="-cpu max -machine q35,kernel-irqchip=split"
VIOMMU="-device intel-iommu,intremap=on,device-iotlb=on -device ioh3420,id=pcie.1,chassis=1"
VIRTIO_PCI="-device virtio-net-pci,bus=pcie.1,netdev=net0,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,mac=02:DC:00:CC:CC:CC,mq=on,vectors=10"
ROOT="-drive format=raw,file=$1,if=virtio"

SCRIPT_DIR="$(dirname -- "${BASH_SOURCE[0]}")"
SHARE="-fsdev local,id=fs1,path=$SCRIPT_DIR/shared,security_model=none -device virtio-9p-pci,fsdev=fs1,mount_tag=share_fs1 \
       -fsdev local,id=fs2,path=$SCRIPT_DIR/yanet2,security_model=none -device virtio-9p-pci,fsdev=fs2,mount_tag=yanet2"
HEADLESS="-serial mon:stdio ${GUI:- -display none}"

# OS-specific configuration
if [ "$OS_TYPE" = "Darwin" ]; then
    # macOS configuration
    NETWORK=" -netdev vmnet-host,id=n1 -device virtio-net-pci,mac=AA:BB:CC:DD:CA:B1,netdev=n1,mq=on,vectors=6"
	VIRTIO_NIC="-netdev vmnet-host,id=net0"
	KVM_CMD="qemu-system-x86_64"
else
    # Linux configuration
	NETWORK=" -netdev tap,ifname=${TAP:-tap0},id=n1,script=no,downscript=no,queues=2 -device virtio-net-pci,mac=AA:BB:CC:DD:CA:B1,netdev=n1,mq=on,vectors=6"
	VIRTIO_NIC="-netdev tap,ifname=tap1,id=net0,vhostforce=on,script=no,downscript=no,queues=4"
	KVM_CMD="kvm"
fi

NETWORK="$NETWORK -nic user,mac=AA:BB:CC:DD:CA:B0,hostfwd=tcp:127.0.0.1:10022-:22"
KVM="$KVM_CMD $MACHINE $VIOMMU $VIRTIO_PCI $VIRTIO_NIC -smp ${SMP:-4} -m 10G $KERNEL $INITRD $APPEND $ROOT $SHARE $NETWORK $HEADLESS"
# Execute QEMU
echo "Starting QEMU with command:"
echo "$KVM"
eval "$KVM"
