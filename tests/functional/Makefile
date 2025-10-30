# Test configuration
QEMU_IMAGE := yanet-test.qcow2
UBUNTU_IMAGE := ubuntu-24.04-minimal-cloudimg-amd64.img
UBUNTU_URL := https://cloud-images.ubuntu.com/minimal/releases/noble/release/$(UBUNTU_IMAGE)
CLOUD_INIT_AUTOLOGIN_ISO := cloud-init-autologin.iso
QEMU_AUTOINIT := -drive file=$(CLOUD_INIT_AUTOLOGIN_ISO),if=virtio,format=raw
# Detect OS
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
	MKISOFS := hdiutil makehybrid -iso -joliet -default-volume-name cidata -o
	QEMU_ACCEL :=
else
	MKISOFS := mkisofs -output
	# Check if KVM is available
	KVM_AVAILABLE := $(shell [ -r /dev/kvm ] && echo "yes" || echo "no")
	ifeq ($(KVM_AVAILABLE),yes)
		QEMU_ACCEL := -enable-kvm
	else
		QEMU_ACCEL :=
	endif
endif

.PHONY: all test clean prepare-vm check-deps test-enhanced test-basic debug-vm debug-dry-run test-login

# Default target
all: test



# Prepare test environment with autologin
prepare-vm: check-deps $(QEMU_IMAGE)
	@echo "Test environment ready"

# Install dependencies
check-deps:
	@echo "check dependencies..."
ifeq ($(UNAME_S),Darwin)
	@which hdiutil > /dev/null || (echo "Error: hdiutil not found" && exit 1)
else
	@which mkisofs > /dev/null || (echo "Please install mkisofs package" && exit 1)
endif
	@which qemu-img > /dev/null || (echo "Please install QEMU" && exit 1)
	@which qemu-system-x86_64 > /dev/null || (echo "Please install QEMU" && exit 1)
	@which go > /dev/null || (echo "Please install Go" && exit 1)

# Run all functional tests
test: prepare-vm
	@echo "Running all functional tests..."
	go test -v ./...

# Run specific test
test-run:
ifdef TEST
	go test -v -run "$(TEST)" ./...
endif

# Clean test artifacts
clean:
	@echo "Cleaning test artifacts..."
	rm -f $(QEMU_IMAGE)
	rm -f $(CLOUD_INIT_AUTOLOGIN_ISO)
	rm -f qemu_debug.log qemu.log test_output*.log test_run.log
	rm -rf /tmp/yanet-test-vm
	go clean -testcache
	@echo "Clean completed"

# Clean everything including downloaded images
clean-all: clean
	@echo "Cleaning all artifacts including downloaded images..."
	rm -f $(UBUNTU_IMAGE)
	@echo "Full clean completed"

# Create QEMU image
$(QEMU_IMAGE): $(UBUNTU_IMAGE) $(CLOUD_INIT_AUTOLOGIN_ISO)
	@echo "Creating QEMU image..."
	cp $(UBUNTU_IMAGE) $(QEMU_IMAGE)
	qemu-img resize $(QEMU_IMAGE) 10G
	# Start QEMU with cloud-init
	$(call run_qemu,$(QEMU_AUTOINIT))

# Run QEMU image with full configuration
# Internal function to run QEMU with common configuration
define run_qemu
	# Find project root directory (contains meson.build and build directory)
	$(eval PROJECT_ROOT := $(shell pwd | sed 's|/tests/functional||'))
	$(eval BUILD_DIR := $(PROJECT_ROOT)/build)
	$(eval TARGET_DIR := $(PROJECT_ROOT)/target)

	# Create target directory if it doesn't exist
	mkdir -p $(TARGET_DIR)

	# Start QEMU with configuration
	qemu-system-x86_64 \
		-name yanet-test-debug \
		-machine q35,kernel-irqchip=split \
		-cpu max \
		-smp 4 \
		-m 8G $(QEMU_ACCEL) \
		-device intel-iommu,intremap=on,device-iotlb=on \
		-device ioh3420,id=pcie.1,chassis=1 \
		-device ioh3420,id=pcie.2,chassis=2 \
		-drive file=$(QEMU_IMAGE),if=virtio,format=qcow2 $(1) \
		-netdev user,id=net0 \
		-device virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0 \
		-netdev stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_0.sock \
		-device virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-netdev stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_1.sock \
		-device virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-fsdev local,id=fsdev0,path=/tmp,security_model=none \
		-device virtio-9p-pci,fsdev=fsdev0,mount_tag=binaries \
		-fsdev local,id=fsdev1,path=/tmp,security_model=none \
		-device virtio-9p-pci,fsdev=fsdev1,mount_tag=config \
		-fsdev local,id=fsdev2,path=$(BUILD_DIR),security_model=none \
		-device virtio-9p-pci,fsdev=fsdev2,mount_tag=build \
		-fsdev local,id=fsdev3,path=$(TARGET_DIR),security_model=none \
		-device virtio-9p-pci,fsdev=fsdev3,mount_tag=target \
		-fsdev local,id=fsdev4,path=$(PROJECT_ROOT),security_model=none \
		-device virtio-9p-pci,fsdev=fsdev4,mount_tag=yanet2 \
		-serial mon:stdio \
		-no-reboot \
		-display none
endef

# Create autologin cloud-init ISO (main ISO with autologin)
$(CLOUD_INIT_AUTOLOGIN_ISO): cloud-init-user-data.yaml meta-data
	@echo "Creating cloud-init ISO with autologin..."
	@rm -f $(CLOUD_INIT_AUTOLOGIN_ISO)
	@mkdir -p cloud-init-tmp
	@cp cloud-init-user-data.yaml cloud-init-tmp/user-data
	@cp meta-data cloud-init-tmp/meta-data
ifeq ($(UNAME_S),Darwin)
	$(MKISOFS) $(CLOUD_INIT_AUTOLOGIN_ISO) cloud-init-tmp
else
	$(MKISOFS) $(CLOUD_INIT_AUTOLOGIN_ISO) -volid cidata -joliet -rock cloud-init-tmp/user-data cloud-init-tmp/meta-data
endif
	@rm -rf cloud-init-tmp

# Download Ubuntu cloud image
$(UBUNTU_IMAGE):
	@echo "Downloading Ubuntu cloud image..."
	wget -c $(UBUNTU_URL)

# Debug targets
debug-vm: $(QEMU_IMAGE)
	@echo "Starting VM in debug mode with passwordless login..."
	@echo "VM will start with serial console output and automatic login"
	@echo "Use Ctrl+A, X to exit QEMU"
	@echo ""

	# Start QEMU in debug mode (no cloud-init)
	$(call run_qemu,)

# Show help
help:
	@echo "YANET Functional Tests Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  all              - Run all tests (default)"
	@echo "  test             - Run all functional tests"
	@echo "  test-run TEST=X  - Run specific test by name"
	@echo ""
	@echo "  prepare-vm       - Prepare VM environment with autologin"
	@echo "  check-deps       - Check and install dependencies"
	@echo ""
	@echo "  clean            - Clean test artifacts"
	@echo "  clean-all        - Clean everything including downloaded images"
	@echo ""
	@echo "  debug-vm         - Start VM in debug mode with serial console"
	@echo "  help             - Show this help"
	@echo ""
	@echo "Configuration:"
	@echo "  QEMU_IMAGE: $(QEMU_IMAGE)"
	@echo "  UBUNTU_IMAGE: $(UBUNTU_IMAGE)"
	@echo "  CLOUD_INIT_AUTOLOGIN_ISO: $(CLOUD_INIT_AUTOLOGIN_ISO)"
	@echo ""

