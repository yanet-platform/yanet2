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

- Go 1.27.1+.
- Rust 1.99+.
- Protobuf compiler 3.0+.
- Meson 1.3+ (on Ubuntu 22.04, `pip install meson==1.3.2`).
- Ninja build system.
- CMake 3.5+.
- GCC/Clang.

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

Meson rebuilds native inputs and refreshes CGO dependency fingerprints. After
changing C code, project headers or linked archives, run `make go-test-prepare`
(equivalent to `meson compile -C build go-test-prepare`) before direct Go
commands. Use the same CGO flags as the configured build: the build writes its
preprocessor flags to `build/yanet_cgo_cppflags`, and a sanitized build also
needs its sanitizer flags. For an unsanitized build:

```bash
make go-test-prepare
CGO_CPPFLAGS="$(cat build/yanet_cgo_cppflags)" go test -count=1 ./modules/route/...
```

The root `make test`, `make test-asan` and `make bench` targets prepare these
dependencies and CGO flags automatically and reuse the Go build cache.

Custom Meson modules must append their existing protobuf targets to
`go_proto_targets` when declaring them and register CGO native dependencies in
`cgo_packages` during module configuration, before the root creates fingerprints.
`build_by_default: true` alone does not add a generator to the Go test preparation
closure.

A package's `libraries`, with the project libraries they depend on, and its
`archives`, external archives such as DPDK's linked as they are, become the link
flags of its generated `zz_yanet_cgo.go`, so no tracked Go file names a build
directory. Only `build/` in the top-level source tree writes these files
(`-Dgo_cgo_flags=enabled` makes another build directory write them). A Go target
outside this project that builds its CGO packages must depend on
`cgo_controlplane_deps`, or `cgo_test_deps` for the test packages: these run
after the generated flags and every archive they name. Before building a
revision older than the generated flags in the same checkout, remove them with
`find . -name zz_yanet_cgo.go -delete`.

### Testing

```bash
meson test -C build
```

### Running in a Virtual Environment

For repeatable local experiments, use the reusable QEMU lab:

```bash
make -C tests/functional prepare-vm
just lab doctor
just lab up
just lab scenario run forward-route
just lab down
```

Run `make all` in a Linux build environment before `just lab doctor`; the Linux
guest requires x86_64 ELF dataplane, control-plane, operator, and CLI artifacts.
Prepare the functional-test image with `make -C tests/functional prepare-vm`.
On macOS, native Mach-O outputs are not usable lab artifacts. The doctor
command also checks the QEMU image and all artifacts required by the lab
baseline.
Running `just lab` without a subcommand only prints help.

See the [YANET2 Lab guide](docs/lab.md) for manifests, packet probes,
troubleshooting, and the built-in forward/route, decap, and NAT64 scenarios.

## 📄 License

YANET2 is licensed under the Apache License, Version 2.0.

## 🔗 Additional Resources

- [Documentation](docs/)
- [Contributing Guidelines](CONTRIBUTING.md)
