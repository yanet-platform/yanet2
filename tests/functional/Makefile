# Exported image paths remain literal during parse-time host detection.
ifneq ("$(value YANET_QEMU_IMAGE)","")
override YANET_QEMU_IMAGE := $(value YANET_QEMU_IMAGE)
export YANET_QEMU_IMAGE
endif

# Test configuration
QEMU_IMAGE := yanet-test.qcow2
BOOTED_TEMPLATE := yanet-test-booted-v3.qcow2

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
	# QEMU needs both read and write access to initialize KVM.
	KVM_AVAILABLE := $(shell [ -e /dev/kvm ] && (exec 3<>/dev/kvm) 2>/dev/null && echo "yes" || echo "no")
	ifeq ($(KVM_AVAILABLE),yes)
		QEMU_ACCEL := -enable-kvm
	else
		QEMU_ACCEL :=
	endif
endif

.PHONY: all test test-main test-run clean clean-all prepare-vm check-deps check-image test-enhanced test-basic debug-vm debug-vm-ssh debug-dry-run test-login

# Default target
all: test



# Preparation creates only the local base or validates an external input.
#
# Runtime tests create missing snapshot templates beside the selected image.
# Local-mode cleanup forces local template recreation; Make retains shared caches.
ifneq ("$(value YANET_QEMU_IMAGE)","")
prepare-vm: check-image
test-run: check-image

check-image:
	@case "$$YANET_QEMU_IMAGE" in /*) ;; *) echo "YANET_QEMU_IMAGE must be absolute" >&2; exit 1;; esac
	@test -f "$$YANET_QEMU_IMAGE" && test -r "$$YANET_QEMU_IMAGE" || { echo "YANET_QEMU_IMAGE must be a readable regular file" >&2; exit 1; }
else
prepare-vm: check-deps $(QEMU_IMAGE)
endif

prepare-vm:
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
# restoreBooted tries the fast baseline snapshot first (YANET running),
# falling back to the preyanet snapshot only when baseline fails.
test: prepare-vm
	@echo "Running all functional tests..."
	go test -count=1 -v ./main/...

# Run main functional tests
test-main: prepare-vm
	@echo "Running main functional tests..."
	go test -count=1 -v -failfast ./main/...

# Run specific test
test-run:
ifdef TEST
	go test -count=1 -v -run "$(TEST)" ./...
endif

# Clean test artifacts
ifneq ("$(value YANET_QEMU_IMAGE)","")
clean-all: CLEAN_ALL_INTENT := yes
clean-all: clean
clean clean-all: export CLEAN_QEMU_IMAGE := $(QEMU_IMAGE)
clean clean-all: export CLEAN_INIT_ISO := $(CLOUD_INIT_AUTOLOGIN_ISO)
clean clean-all: export CLEAN_UBUNTU_IMAGE := $(UBUNTU_IMAGE)
clean: check-image

# Every stage preflights its operands; all-clean can finish after a prior local cleanup.
define clean_shared
	@set -eu; \
	capture() { metadata=$$("$$@" && printf .) || return; metadata=$${metadata%.}; }; \
	physical_dir() { (CDPATH= cd -P "$$1" && capture pwd -P && printf '%s' "$${metadata%?}"); }; \
	capture physical_dir .; local_dir="$$metadata"; \
	image="$$YANET_QEMU_IMAGE"; temp_dir=; protected_temp=; \
	if [ '$(1)' = ubuntu ]; then \
		if [ -L "$$CLEAN_UBUNTU_IMAGE" ] || [ "$$CLEAN_UBUNTU_IMAGE" -ef "$$image" ] || [ ! -e "$$CLEAN_UBUNTU_IMAGE" ]; then exit 0; fi; \
		set -- "$$CLEAN_UBUNTU_IMAGE"; \
	else \
		set -- "$$CLEAN_QEMU_IMAGE" yanet-test-booted*.qcow2 yanet-test-baseline*.qcow2 \
			"$$CLEAN_INIT_ISO" qemu_debug.log qemu.log test_output*.log test_run.log; \
		if [ -n '$(filter yes,$(CLEAN_ALL_INTENT))$(filter clean-all,$(MAKECMDGOALS))' ]; then set -- "$$@" "$$CLEAN_UBUNTU_IMAGE"; fi; \
		if [ -L /tmp/yanet-test-vm ] || [ /tmp/yanet-test-vm -ef "$$image" ]; then \
			protected_temp=yes; \
		elif [ -d /tmp/yanet-test-vm ]; then \
			capture physical_dir /tmp/yanet-test-vm; temp_dir="$$metadata"; \
		fi; \
	fi; \
	while :; do \
		capture physical_dir "$${image%/*}/."; parent="$$metadata"; \
		if [ "$$parent" -ef "$$local_dir" ]; then \
			echo "Refusing cleanup in the shared image directory" >&2; exit 1; \
		fi; \
		if [ -n "$$temp_dir" ]; then \
			for start in "$$parent" "$$temp_dir"; do \
				if [ "$$start" = "$$parent" ]; then boundary="$$temp_dir"; else boundary="$$parent"; fi; \
				dir="$$start"; \
				while :; do \
					if [ "$$dir" -ef "$$boundary" ]; then echo "Shared storage overlaps the VM working directory" >&2; exit 1; fi; \
					capture physical_dir "$$dir/.."; next="$$metadata"; \
					[ "$$dir" -ef "$$next" ] && break; \
					dir="$$next"; \
				done; \
			done; \
		fi; \
		case "$$image" in /tmp/yanet-test-vm/*) echo "Shared image overlaps the VM working directory" >&2; exit 1;; esac; \
		for path in "$$@" /tmp/yanet-test-vm; do \
			[ -n "$$path" ] || continue; \
			case "$$path" in /*) ;; *) path="$$local_dir/$$path";; esac; \
			if [ -L "$$path" ] || [ "$$path" -ef "$$YANET_QEMU_IMAGE" ]; then continue; fi; \
			[ -e "$$path" ] || continue; \
			capture physical_dir "$${path%/*}/."; dir="$$metadata"; \
			if [ "$$dir" -ef "$$parent" ]; then echo "Cleanup operand is in shared storage: $$path" >&2; exit 1; fi; \
		done; \
		[ -L "$$image" ] || break; \
		capture readlink -n "$$image"; link="$$metadata"; \
		case "$$link" in /*) image="$$link";; *) image="$$parent/$$link";; esac; \
	done; \
	if [ -n "$$temp_dir" ]; then \
		capture find "$$temp_dir" -exec sh -c 'image=$$1; shift; for path in "$$@"; do if [ -L "$$path" ]; then printf "%s\n" "$$path" || exit "$$?"; elif env test "$$path" -ef "$$image"; then printf "%s\n" "$$path" || exit "$$?"; else status=$$?; [ "$$status" -eq 1 ] || exit "$$status"; fi; done; exit 0' sh "$$image" {} +; protected_temp="$$metadata"; \
	fi; \
	for path in "$$@"; do \
		[ -n "$$path" ] || continue; \
		case "$$path" in /*) ;; *) path="$$local_dir/$$path";; esac; \
		if [ -L "$$path" ] || [ "$$path" -ef "$$YANET_QEMU_IMAGE" ]; then \
			echo "Preserving shared image alias: $$path"; \
		else rm -f -- "$$path"; fi; \
	done; \
	if [ '$(1)' = clean ]; then \
		if [ -n "$$protected_temp" ]; then \
			echo "Preserving VM working directory containing possible shared aliases"; \
		else rm -rf /tmp/yanet-test-vm; fi; \
	fi
endef

clean:
	$(call clean_shared,clean)
	go clean -testcache

clean-all:
	$(call clean_shared,ubuntu)
else
clean:
	@echo "Cleaning test artifacts..."
	rm -f $(QEMU_IMAGE)
	rm -f yanet-test-booted*.qcow2 yanet-test-baseline*.qcow2
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
endif

ifeq ("$(value YANET_QEMU_IMAGE)","")
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
	mkdir -p $(TARGET_DIR) /tmp/yanet-test-vm

	# Start QEMU with configuration
	qemu-system-x86_64 \
		-name yanet-test-debug \
		-machine q35,kernel-irqchip=split \
		-cpu max \
		-smp 4 \
		-m 1G $(QEMU_ACCEL) \
		-device intel-iommu,intremap=on,device-iotlb=on \
		-device ioh3420,id=pcie.1,chassis=1 \
		-device ioh3420,id=pcie.2,chassis=2 \
		-drive file=$(QEMU_IMAGE),if=virtio,format=qcow2,discard=unmap,detect-zeroes=unmap $(1) \
		-netdev user,id=net0 \
		-device virtio-net-pci,netdev=net0,mac=AA:BB:CC:DD:CA:B0 \
		-netdev stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_0.sock \
		-device virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-netdev stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_1.sock \
		-device virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-fsdev local,id=fsdev0,path=/tmp/yanet-test-vm,security_model=none \
		-device virtio-9p-pci,fsdev=fsdev0,mount_tag=logs \
		-fsdev local,id=fsdev1,path=/tmp/yanet-test-vm,security_model=none \
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

define run_qemu_ssh
	# Find project root directory (contains meson.build and build directory)
	$(eval PROJECT_ROOT := $(shell pwd | sed 's|/tests/functional||'))
	$(eval BUILD_DIR := $(PROJECT_ROOT)/build)
	$(eval TARGET_DIR := $(PROJECT_ROOT)/target)

	# Create target directory if it doesn't exist
	mkdir -p $(TARGET_DIR) /tmp/yanet-test-vm

	# Start QEMU with configuration
	qemu-system-x86_64 \
		-netdev user,id=n1,hostfwd=tcp:127.0.0.1:2222-:22 \
		-device virtio-net-pci,netdev=n1,mac=AA:BB:CC:DD:CA:B0 \
		-name yanet-test-debug \
		-machine q35,kernel-irqchip=split \
		-cpu max \
		-smp 4 \
		-m 1G $(QEMU_ACCEL) \
		-device intel-iommu,intremap=on,device-iotlb=on \
		-device ioh3420,id=pcie.1,chassis=1 \
		-device ioh3420,id=pcie.2,chassis=2 \
		-drive file=$(QEMU_IMAGE),if=virtio,format=qcow2 $(1) \
		-netdev stream,id=net1,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_0.sock \
		-device virtio-net-pci,bus=pcie.1,netdev=net1,mac=52:54:00:6b:ff:a5,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-netdev stream,id=net2,server=on,addr.type=unix,addr.path=/tmp/yanetvm_sockdev_1.sock \
		-device virtio-net-pci,bus=pcie.2,netdev=net2,mac=52:54:00:11:00:03,disable-legacy=on,disable-modern=off,iommu_platform=on,ats=on,vectors=10 \
		-fsdev local,id=fsdev0,path=/tmp/yanet-test-vm,security_model=none \
		-device virtio-9p-pci,fsdev=fsdev0,mount_tag=logs \
		-fsdev local,id=fsdev1,path=/tmp/yanet-test-vm,security_model=none \
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
endif

# Debug targets
ifneq ("$(value YANET_QEMU_IMAGE)","")
debug-vm debug-vm-ssh:
	@echo "Direct debug targets cannot use YANET_QEMU_IMAGE; unset it to use a local image" >&2; exit 1
else
debug-vm: $(QEMU_IMAGE)
	@echo "Starting VM in debug mode with passwordless login..."
	@echo "VM will start with serial console output and automatic login"
	@echo "Use Ctrl+A, X to exit QEMU"
	@echo ""

	# Start QEMU in debug mode (no cloud-init)
	$(call run_qemu,)

# Debug target ssh
debug-vm-ssh: $(QEMU_IMAGE)
	@echo "Starting VM in debug mode with passwordless login..."
	@echo "VM will start with serial console output and automatic login"
	@echo "Listens to SSH connections on port 2222"
	@echo ""
	@echo "NOTE: You need to manually start SSH server and configure authorized_keys:"
	@echo "  1. Start SSH server: systemctl start ssh"
	@echo "  2. Add your public key to /root/.ssh/authorized_keys"
	@echo "  3. Connect via SSH: ssh -p 2222 root@127.0.0.1"
	@echo ""
	@echo "Use Ctrl+A, X to exit QEMU"
	@echo ""

	# Start QEMU in debug mode (no cloud-init)
	$(call run_qemu_ssh,)
endif

# Show help
help:
	@echo "YANET Functional Tests Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  all              - Run all tests (default)"
	@echo "  test             - Run all functional tests"
	@echo "  test-main        - Run main functional tests"
	@echo "  test-run TEST=X  - Run specific test by name"
	@echo ""
	@echo "  prepare-vm       - Prepare VM environment with autologin"
	@echo "  check-deps       - Check and install dependencies"
ifneq ("$(value YANET_QEMU_IMAGE)","")
	@echo "  check-image      - Validate the existing shared input"
endif
	@echo ""
	@echo "  clean            - Clean test artifacts"
	@echo "  clean-all        - Clean everything including downloaded images"
	@echo ""
	@echo "  debug-vm         - Start VM in debug mode with serial console"
	@echo "  debug-vm-ssh     - Start VM in debug mode with serial console and SSH forwarding on port 2222"
	@echo "  help             - Show this help"
	@echo ""
	@echo "Configuration:"
	@echo "  QEMU_IMAGE: $(QEMU_IMAGE)"
	@echo "  UBUNTU_IMAGE: $(UBUNTU_IMAGE)"
	@echo "  CLOUD_INIT_AUTOLOGIN_ISO: $(CLOUD_INIT_AUTOLOGIN_ISO)"
	@printf '  YANET_QEMU_IMAGE: %s\n' "$${YANET_QEMU_IMAGE-}"
	@echo ""
	@echo "Shared input: nonempty YANET_QEMU_IMAGE requires an absolute readable regular file."
	@echo "  Preparation validates only; unset/empty keeps local image creation."
	@echo "  Direct debug targets reject shared mode; clean preserves input/aliases/neighbors"
	@echo "  and independent local cleanup, refusing unsafe overlap."
	@echo "Sandbox regression: bash test-makefile.sh (no real VM or Go execution)."
	@echo ""
