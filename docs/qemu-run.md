# Running YANET in QEMU

## Introduction

This document describes the process of preparing and running YANET in a QEMU virtual machine.

Requirements:

- QEMU
- ext4fuse on macOS (optional - if not available, commands in chroot must be executed in running VM)

## Work Directory Structure

```bash
./
├── mnt/
├── noble-server-cloudimg-amd64.img
├── qemu_run.sh
├── shared/
└── yanet2/
```

## System Image Preparation

### Creating Base Image

We'll use Ubuntu 24.04 Noble cloud image:

```bash
# Download the image
wget https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.tar.gz

# Extract the image
tar xzf ubuntu-24.04-server-cloudimg-amd64.tar.gz

# Increase image size to 10GB
truncate -s10G noble-server-cloudimg-amd64.img
```

### Check and Resize on Linux

```bash
e2fsck -f noble-server-cloudimg-amd64.img
resize2fs noble-server-cloudimg-amd64.img
```

### Mount the Image

#### For Linux

```bash
mkdir -p mnt
sudo mount noble-server-cloudimg-amd64.img mnt
df -h mnt
```

#### For macOS

```bash
# Note: ext4fuse must be installed (brew install ext4fuse)
mkdir -p mnt
sudo ext4fuse noble-server-cloudimg-amd64.img mnt -o allow_other
df -h mnt
```

### System Configuration

#### Setting up fstab

```bash
sudo chroot mnt /bin/bash -c "
echo 'share_fs1 /shared 9p trans=virtio,rw,_netdev,nofail 0 0
yanet2 /yanet2 9p trans=virtio,rw,_netdev,nofail 0 0
' | tee -a /etc/fstab

# Create mount point
mkdir -p /yanet2
"
```

#### Network Configuration

The VM will have two network interfaces:

1. For SSH access and internet connectivity (DHCP)
2. For YANET testing (static IPv6)

```bash
cd mnt
sudo chroot . /usr/bin/bash -i <<EOF
# DHCP interface configuration
echo '
[Match]
MACAddress=AABB.CCDD.CAB0
[Network]
DHCP=yes
' |  tee /etc/systemd/network/00-user-net.network

# Static IPv6 configuration
echo '
[Match]
MACAddress=AABB.CCDD.CAB1
[Network]
Address=fd22:6563:20e0:ace2::2/64
Gateway=fd22:6563:20e0:ace2::1
' |  tee /etc/systemd/network/01-static-ip.network

systemctl enable systemd-networkd
EOF
```

#### SSH Access Configuration

```bash
cd mnt
sudo mkdir ./root/.ssh
# Copy your public key
sudo cp ~/.ssh/id_rsa.pub ./root/.ssh/authorized_keys
sudo chmod go-rwx -R ./root/.ssh

# For cloud-image, regenerate host keys
sudo chroot . /usr/bin/bash -c "dpkg-reconfigure openssh-server"
```

Recommended ~/.ssh/config entry:

```ssh-config
Host yanet-dev
    User root
    HostName localhost
    Port 10022
    StrictHostKeyChecking no
```

## Kernel Preparation

### Installing Kernels

Kernels can be obtained from Ubuntu Mainline: <https://kernel.ubuntu.com/mainline/>

```bash
# Download and install kernel
wget https://kernel.ubuntu.com/mainline/v6.14/amd64/linux-image-unsigned-6.14.0-061400-generic_6.14.0-061400.202503241442_amd64.deb
wget https://kernel.ubuntu.com/mainline/v6.14/amd64/linux-modules-6.14.0-061400-generic_6.14.0-061400.202503241442_amd64.deb

sudo cp -v linux-*.deb mnt/
cd mnt
sudo chroot . /usr/bin/bash -c "dpkg -i *deb"

# Copy kernel and initrd
mkdir -p ../shared/kernels
sudo cp -v boot/initrd.img-* boot/vmlinuz-* ../shared/kernels/
sudo chown -cR $USER ../shared/kernels/

# Unmount the image
cd ..
sudo umount mnt
rmdir mnt
```

## Running QEMU

The project includes a universal QEMU launch script that automatically detects the operating system and configures appropriate parameters. The script handles platform-specific differences in networking and device configuration:

- On Linux, it uses tap interfaces for networking
- On macOS, it uses vmnet-host for networking

```bash
# Start in headless mode
./qemu_run.sh noble-server-cloudimg-amd64.img

# Start with GUI
GUI=1 ./qemu_run.sh noble-server-cloudimg-amd64.img

# Override number of CPU cores
SMP=8 ./qemu_run.sh noble-server-cloudimg-amd64.img

# Force specific kernel version
FORCE_VERSION=6.8.0-57-generic ./qemu_run.sh noble-server-cloudimg-amd64.img
```

## VM Control

- `Ctrl+a x` - terminate VM
- `Ctrl+a c` - switch between VM console and QEMU monitor
- In QEMU monitor:
  - `info network` - network interfaces information
  - `quit` - shutdown VM

## First Run

After successfully starting the VM and connecting via SSH (or console), install the required development packages:

```bash
# Update package lists
apt-get update -y

# Install build tools
apt-get install -y \
    meson \
    clang \
    clang-format-19 \
    clang-tidy-19 \
    git \
    just \
    make

# Install development dependencies
apt-get install -y \
    python3-pyelftools \
    libnuma-dev \
    libpcap-dev \
    libyaml-dev \
    protobuf-compiler \
    rustup

# Install debugging tools
apt-get install -y \
    gdb \
    lldb \
    lcov

# Install Go and tools
apt-get install -y golang-go

# Install Go modules
GOBIN=/usr/local/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
GOBIN=/usr/local/bin go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Install Rust (self update disable need only for rustup installed from apt)
rustup set auto-self-update disable
rustup install stable
```

## Building and Running YANET

After installing all necessary packages, you can proceed with building YANET. The project code will be available in the `/yanet2` directory, which is automatically mounted from the host system:

```bash
cd /yanet2

# Initialize and update submodules
git submodule update --init --recursive

# Build the project
just setup build

# Build controlplane
just controlplane

# Build cli
just cli
```

This organization allows you to develop on the host system in your familiar environment while performing builds and testing in the VM through the mounted directory.

### Running YANET

To run YANET:

1. Configure hugepages:
    ```bash
    ./subprojects/dpdk/usertools/dpdk-hugepages.py --setup 4G
    ```

2. Configure network interfaces for DPDK:
    ```bash
    # Check PCI devices status
    ./subprojects/dpdk/usertools/dpdk-devbind.py --status

    # Load driver
    modprobe uio_pci_generic

    # Bind interface to DPDK driver
    ./subprojects/dpdk/usertools/dpdk-devbind.py --bind=uio_pci_generic 00:02.0
    ```

3. Start dataplane:
    ```bash
    ./build/dataplane/yanet-dataplane dataplane.yaml
    ```

4. In another terminal, start controlplane:
    ```bash
    ./build/yanet-controlplane -c controlplane.yaml
    ```

### Checking Functionality

To verify YANET operation, use yanet-cli utility:

```bash
# Inspect config
./cli/target/release/yanet-cli inspect
```
