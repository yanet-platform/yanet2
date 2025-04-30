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

- Multi-stage configuration process orchestrated by the Coordinator component.
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

### Key Components

1. **Module System** - Pluggable modules (route, acl, balancer, etc.) providing specific network functions.
2. **Pipeline System** - Configurable packet processing chains.
3. **Coordinator** - Orchestrates multi-stage system configuration.
4. **Announcer (TDB)** - Monitors system health and controls BGP announcements.
5. **CLI Tools** - Management interfaces with consistent command structure.
6. **Web UI (TDB)** - Browser-based management interface.

## 🛠️ Technologies

YANET employs a multi-language approach to leverage the strengths of different programming languages:

- **C** - Data plane and performance-critical components.
- **Go** - Control plane and orchestration components.
- **Rust** - CLI tools.
- **DPDK** - Kernel-bypass networking for high-performance packet processing.
- **gRPC** - Communication framework for inter-component messaging.
- **Protocol Buffers** - Efficient binary serialization format.
- **Meson** - Build system for core components.

## 🔧 Building & Requirements

### System Requirements

- Linux-based operating system (Ubuntu 22.04+ or equivalent recommended).
- Hardware supporting DPDK (Mellanox NICs recommended).
- At least 64GB RAM recommended for production.

### Dependencies

- Go 1.21+.
- Rust 1.84+.
- Protobuf compiler 3.0+.
- Meson 0.61+.
- Ninja build system.
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
