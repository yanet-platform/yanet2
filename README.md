# YANET

> **Note:** This project is currently in active development.

YANET is a high-performance modular software router built on DPDK (Data Plane Development Kit) that provides exceptional packet processing capabilities. It's designed to be a versatile network solution that functions as a router, decapsulator, firewall, L3 load balancer, and NAT device, all in one platform.

## 🌐 Key Features

### 🚀 High Performance

- DPDK-accelerated packet processing bypasses the kernel networking stack for maximum throughput.
- Achieves near-hardware performance with the flexibility of software.
- NUMA-aware resource management for optimal multi-socket performance.
- Optimized memory management with huge pages support.

### 🧩 Modular Architecture

- Module system allows enabling only required networking functions.
- Clean separation between control plane and data plane components.
- Flexible pipeline configuration for customized packet processing flows.
- Add or remove functionality without affecting the entire system.

### 🛡️ Safe Initialization Process

- Controlled system initialization with failsafe mechanisms.
- BGP route announcement control based on system health.

## 🏗️ Architecture

YANET follows a modular architecture with clear separation between control and data planes:

### Data Plane (C & DPDK)

- Implements fast-path packet processing.
- Uses DPDK for direct hardware access and kernel bypass.
- Applies routing decisions, filtering, and packet transformations.
- Achieves high packet throughput with minimal latency.

### Control Plane (Go)

- Exposes management API.
- Handles configuration and management functions.
- Manages routing tables and policy.
- ACL compilation (TDB).

## 🛠️ Technologies

YANET employs a multi-language approach to leverage the strengths of different programming languages:

- **C** - Data plane and performance-critical components.
- **Go** - Control plane and orchestration components.
- **Rust** - CLI tools.
- **DPDK** - Kernel-bypass networking for high-performance packet processing.
- **gRPC** - Communication framework for inter-component messaging.
- **Protocol Buffers** - Efficient binary serialization format. Protobuf API compatibility policy: see [docs/proto-compatibility.md](docs/proto-compatibility.md).
- **Meson** - Build system for core components.

## 🔧 Building & Requirements

### System Requirements

- Linux-based operating system (Ubuntu 22.04+ or equivalent recommended).
- Hardware supporting DPDK (Mellanox NICs recommended).
- At least 64GB RAM recommended for production.

### Dependencies

- Go 1.21+.
- Rust 1.88+.
- Protobuf compiler 3.0+.
- Meson 0.61+.
- Ninja build system.
- GCC/Clang.

### Nix development shell

On a machine with Nix installed, the repository provides the build tools and
libraries except Rust, which is managed separately with `rustup`:

Install Nix with `sh <(curl -L https://nixos.org/nix/install) --daemon` if it is not already available.
See the [official Nix installation documentation](https://nixos.org/download/) for platform-specific options.

```bash
nix develop
git submodule update --init --recursive
meson setup build
```

`flake.lock` pins the environment. The flake does not select a Nix store path:
each machine uses its own Nix configuration, so a standard `/nix` store and an
administrator-configured external-disk store both work without repository
changes.

QEMU is included in the development shell. KVM is optional acceleration; the
functional-test harness falls back to QEMU TCG when `/dev/kvm` is unavailable.
To use KVM on Linux, add the current user to its group and log in again:

```bash
sudo usermod -aG kvm "$(id -un)"
test -r /dev/kvm && test -w /dev/kvm
```

The host or VM platform must expose `/dev/kvm`; group membership cannot create
the device. Functional tests configure hugepages inside the guest. Host
hugepages are needed only when running the dataplane directly on the host.

On x86-64, build the release artifacts with `make all`, then run
`make test-functional`. The test harness stages Nix-built executables so they
run with the guest system loader; the guest does not need `/nix/store`.
Functional tests from aarch64 hosts are not yet supported.

See the [Nix development workflow](docs/nix-develop-workflow.md) for everyday
commands and store cleanup behaviour.

### Build Instructions

1. Clone the repository:
   ```bash
   git clone https://github.com/yanet-platform/yanet2.git
   cd yanet2
   git submodule update --init
   ```

2. Configure and build with Meson:
   ```bash
   meson setup build
   meson compile -C build
   ```

3. Build CLI tools:
   ```bash
   cargo build --release
   ```

4. Install:
   ```bash
   meson install -C build
   ```

### Testing

```bash
meson test -C build
```

### Running in a Virtual Environment

YANET includes QEMU virtualization support for development and testing without physical hardware:

1. Configure QEMU VM with virtual network interfaces.
2. Set up shared folder for code access.
3. Follow detailed instructions in the documentation.

## 📄 License

YANET2 is licensed under the Apache License, Version 2.0.

## 🔗 Additional Resources

- [Documentation](docs/)
- [Contributing Guidelines](CONTRIBUTING.md)
