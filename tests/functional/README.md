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
│   ├── packet_parser.go # Packet parser for network analysis
│   ├── utils.go        # Utility functions
│   └── framework.go    # Main framework code
├── framework_test.go   # Main comprehensive framework test
├── nat64_test.go       # NAT64 module tests
├── forward_test.go     # Forward module tests
├── decap_test.go       # Decap module tests
├── decap_test.sh       # Shell script for decap testing
├── Makefile            # Build and run commands
├── cloud-init-user-data.yaml # Cloud-init configuration with autologin
├── meta-data           # Cloud-init metadata
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

# Run specific test
make test-run TEST=TestFramework

# Run with Go directly
go test -v ./...

# Run with increased timeout
go test -v -timeout 10m ./...

# Run from project root directory
just test-functional   # qemu
```

### Debugging and Diagnostics

```bash
# Show help for all commands
make help

# Run VM in debug mode with serial console
make debug-vm
# Use Ctrl+A, X to exit QEMU

# Enable debug logging for tests
export YANET_TEST_DEBUG=1
# Preserve test artifacts for debugging (don't delete after test)
export YANET_PRESERVE_ARTIFACTS=1
# Keep VM running after test for manual debugging (also enables debug logging and preserves artifacts)
export YANET_KEEP_VM_ALIVE=1
go test -v ./...

# Clean test artifacts
make clean

# Full cleanup (including downloaded images)
make clean-all
```

#### Debug Logging

By default, tests use minimal logging level (ErrorLevel).
To enable verbose debug output, set the environment variable:

```bash
# Enable verbose logging
export YANET_TEST_DEBUG=1

# Run tests with debug output
go test -v ./...

# Or in a single command
YANET_TEST_DEBUG=1 go test -v ./...
```

When `YANET_TEST_DEBUG` is set, the framework will use zap's Development
configuration with detailed output of all framework operations.

When `YANET_PRESERVE_ARTIFACTS` is set, the framework will:
- Keep the VM working directory with all configuration files
- Preserve QEMU logs and output files
- Log the location of preserved artifacts

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
    // Framework initialization
    fw, err := framework.New(&framework.Config{
        QEMUImage: "yanet-test.qcow2",
    })
    require.NoError(t, err)
    defer fw.Stop()

    // Start test environment
    require.NoError(t, fw.Start())

    // Wait for VM to be ready
    require.NoError(t, fw.WaitForReady())

    // Execute basic commands
    output, err := fw.ExecuteCommand("whoami")
    require.NoError(t, err)
    t.Logf("Current user: %s", strings.TrimSpace(output))

}
```

## Debugging

### QEMU Logs

QEMU VM logs are available in the `yanet-test-vm.log` file in the test working directory.
To access logs after test run, preserve artifacts with settings env
`export YANET_PRESERVE_ARTIFACTS=1` - logs are then available in
`/tmp/yanet-vm-<name>-<pid>-<timestamp>/` directory.

### VM Access

VM uses autologin through serial console - SSH is not required:

```bash
# Run VM in debug mode with serial console output
make debug-vm

# Logs are available in files:
# - qemu.log - QEMU startup and configuration
# - test_output*.log - test execution logs
# - /tmp/yanet-test-vm/ - VM working directory
```

When `YANET_KEEP_VM_ALIVE` is set, the framework will:
- Enable debug logging (same as `YANET_TEST_DEBUG`)
- Preserve test artifacts (same as `YANET_PRESERVE_ARTIFACTS`)
- Keep the VM process running after test completion
- Enable SSH port forwarding on a random free port for manual debugging
- Log SSH connection details and serial console socket path

To connect to a running VM's serial console when `YANET_KEEP_VM_ALIVE` is set, find the VM
process with `pgrep -af qemu` (look for the VM name), locate the working directory in the
command line, and connect using `socat - UNIX-CONNECT:/tmp/yanet-vm-<instance-id>/serial.sock`.

```bash
# Example: Find running VM and connect to serial console
pgrep -af qemu  # Find VM process and locate workdir path
# rlwrap is optional but very helpful
rlwrap socat - UNIX-CONNECT:/tmp/yanet-vm-main-1058482-1766597152887221542/serial.sock
```

### Network Traffic Monitoring

To view packets passing through Unix domain sockets, you can monitor socket files:

```bash
# Check socket files
ls -la /tmp/yanetvm_sockdev_*.sock

# Monitor socket activity
lsof /tmp/yanetvm_sockdev_*.sock
```

### Packet Dump Files

When `YANET_TEST_DEBUG=1` is enabled, the framework automatically records all socket traffic to dump files for each test:

```bash
# Enable debug mode to record packet dumps
export YANET_TEST_DEBUG=1
go test -v ./...

# Dump files are created in the VM working directory:
# /tmp/yanet-vm-<name>-<pid>-<timestamp>/<Test/SubTestName>.in.dump   # Input packets
# /tmp/yanet-vm-<name>-<pid>-<timestamp>/<Test/SubTestName>.out.dump  # Output packets
```

Each dump file contains raw socket data in the QEMU socket protocol format:
- 4-byte length prefix (big-endian)
- Packet data

#### Replaying Packets from Dump Files

To manually replay packets from a dump file to a socket:

```bash
# Find the socket path (usually in /tmp/)
ls -la /tmp/yanetvm_*_sockdev_*.sock

# Replay packets from dump file to socket
socat -u FILE:/tmp/yanet-vm-main-123-456/TestDecap.in.dump UNIX-CONNECT:/tmp/yanetvm_main_123_456_sockdev_0.sock > response.dump
```

## Limitations

1. Each test runs in a separate VM for isolation
2. Test startup time is increased due to VM startup overhead
3. Requires sufficient resources to run QEMU

## Network Architecture

The framework uses **Unix domain sockets** for communication with QEMU VM:

- **QEMU starts** with `-netdev stream` using Unix domain sockets
- **Socket devices** at `/tmp/yanetvm_sockdev_*.sock` for network interfaces
- **Packets are transmitted** as raw bytes through socket connections
- **Packet processing** happens in the real YANET network stack

This provides:
- ✅ **Real network environment** without emulation
- ✅ **High performance** through Unix domain sockets
- ✅ **Easy debugging** through standard network tools
- ✅ **Reliability** and low latency communication

## Build Dependencies

**Important:** Functional tests require pre-built YANET components:

```bash
# Build necessary components before testing
just dbuild                    # Docker build (recommended)
just build                     # Local build

# Or using meson directly
meson compile -C build          # Builds dataplane and modules
```

Tests use **QEMU 9P filesystem** for access to built binary files:
- `build/` directory is mounted in VM as shared filesystem
- `target/` directory for CLI binaries
- VM runs real YANET processes from built binaries
- This ensures full end-to-end testing

## Future Development

1. Add support for parallel test execution
2. Improve VM readiness waiting mechanism
3. Add VM snapshot support for faster tests
4. Expand test suite for load balancer
5. Add network performance metrics