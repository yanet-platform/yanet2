# YANET Functional Testing Framework

Framework for functional testing of YANET in an isolated environment using QEMU **without SSH dependencies** - all communication happens through serial console with autologin.

## Overview

The framework allows:
- Running YANET (Data Plane and Control Plane) in an isolated QEMU VM
- Configuring modules through serial console (without SSH)
- Sending and receiving packets through TCP socket connections with QEMU
- Checking packet processing correctness in a real network environment
- Automatic system login through cloud-init

## Key Features

- **No SSH**: All communication through serial console with autologin
- **Cross-platform**: Works on macOS and Linux
- **Socket Networking**: Real packet testing through QEMU socket networking
- **PTY-based**: Reliable communication through PTY devices

## Requirements

- Go 1.21 or newer
- QEMU (qemu-system-x86_64)
- genisoimage (Linux) or hdiutil (macOS)
- wget
- YANET dependencies for building and running

## Structure

```
tests/functional/
├── framework/           # Main framework components
│   ├── qemu.go         # QEMU VM management
│   ├── cli.go          # CLI interaction
│   ├── socket_client.go # TCP socket client for QEMU communication
│   ├── packet_simulator.go # Packet simulator for testing
│   ├── sock_dev.go     # Network device utilities
│   ├── tap_macos.go    # TAP interface for macOS (legacy)
│   └── framework.go    # Main framework code
├── acl_test.go         # ACL module tests
├── nat64_test.go       # NAT64 module tests
├── forward_test.go     # Forward module tests
├── decap_test.go       # Decap module tests
├── simple_test.go      # Simple basic tests
├── Makefile            # Build and run commands
├── cloud-init/            # Cloud-init configuration with autologin
└── README.md           # Documentation
```

## Usage

### Quick Start

```bash
# 1. Check dependencies
make check-deps

# 2. Prepare test environment with autologin
make prepare-vm

# 3. Run all tests
make test
```

### Running Tests

```bash
# Run all functional tests
make test

# Run enhanced decap tests
make test-enhanced

# Run basic framework tests
make test-basic

# Run specific test
make test-run TEST=TestDecapEnhancedWithYANET

# Run from project root directory
make test-functional
just test-functional

# Run in Docker (recommended)
just dtest-functional

# Run with increased timeout
go test -v -timeout 10m ./...
```

### Debugging and Diagnostics

```bash
# Show help for all commands
make help

# Show QEMU command without running
make debug-dry-run

# Run VM in debug mode with serial console
make debug-vm
# Use Ctrl+A, X to exit QEMU

# Full cleanup (including downloaded images)
make clean-all
```

### Writing Tests

1. Create a new `*_test.go` file
2. Import necessary packages:
```go
import (
    "testing"
    "github.com/yanet-platform/yanet2/tests/functional/framework"
    "github.com/stretchr/testify/require"
)
```

3. Create test function:
```go
func TestExample(t *testing.T) {
    // Framework initialization (without SSH parameters)
    fw, err := framework.New(&framework.Config{
        QEMUImage: "yanet-test.qcow2",
    })
    require.NoError(t, err)
    defer fw.Stop()

    // Start test environment
    require.NoError(t, fw.Start())

    // Start and configure YANET
    require.NoError(t, fw.StartYANET())

    // Configure decap module
    prefixes := []string{"2001:db8::2/128", "10.0.0.2/32"}
    require.NoError(t, fw.ConfigureDecapModule(prefixes))

    // Create test packet
    packet := createDecapPacket("ip6ip4")

    // Send packet and capture response
    response, err := fw.SendPacketAndCapture(0, 1, packet, 5*time.Second)
    if err != nil {
        t.Logf("Packet capture failed (expected without YANET): %v", err)
    } else {
        t.Logf("Captured response: %d bytes", len(response))
    }

    // Get statistics
    stats, err := fw.GetYANETStats()
    require.NoError(t, err)
    t.Logf("Collected %d categories of statistics", len(stats))
}
```

## Debugging

### QEMU Logs

QEMU VM logs are available in the `yanet-test-vm.log` file in the test working directory.

### VM Access

VM uses autologin through serial console - SSH is not required:

```bash
# Run VM in debug mode with serial console output
make debug-vm

# Logs are available in files:
# - qemu_debug.log - QEMU startup and configuration
# - test_output*.log - test execution
# - /tmp/yanet-test-vm/ - VM working directory
```

### Network Traffic Monitoring

To view packets passing through socket connections, you can use standard network monitoring tools:

```bash
# Monitor TCP connections
netstat -an | grep :9001

# View traffic through tcpdump (if available)
tcpdump -i lo port 9001
```

## Limitations

1. Each test runs in a separate VM for isolation
2. Test startup time is increased due to VM startup overhead
3. Requires sufficient resources to run QEMU

## Network Architecture

The framework uses **real TCP socket connections** for communication with QEMU VM:

- **QEMU starts** with parameter `-netdev socket,listen=:9001`
- **Tests connect** to this port through TCP socket client
- **Packets are transmitted** as raw bytes through socket connections
- **Packet processing** happens in the real YANET network stack

This provides:
- ✅ **Real network environment** without emulation
- ✅ **Cross-platform compatibility** (works on Linux, macOS, Windows)
- ✅ **Easy debugging** through standard network tools
- ✅ **Reliability** thanks to proven TCP protocols

## Build Dependencies

**Important:** Functional tests require pre-built YANET components:

```bash
# Build necessary components before testing
meson compile -C build          # Builds dataplane and modules
make controlplane              # Builds controlplane agents
```

Tests use **QEMU 9P filesystem** for access to built binary files:
- `build/` directory is mounted in VM as shared filesystem
- VM runs real YANET processes from built binaries
- This ensures full end-to-end testing

## Future Development

1. Add support for parallel test execution
2. Improve VM readiness waiting mechanism
3. Add VM snapshot support for faster tests
4. Expand test suite for load balancer
5. Add network performance metrics