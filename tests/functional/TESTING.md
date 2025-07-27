# YANET Functional Testing Framework

## Test Structure

After reorganization, the `tests/functional` directory contains the following tests:

### 1. Framework Test (`framework_test.go`)
**Main comprehensive test for checking all testing framework functionality.**

Includes:
- ✅ **VM Startup** - QEMU VM startup and initialization
- ✅ **Basic Commands** - Basic system command execution check
- ✅ **Filesystem Check** - Filesystem and mounting check
- ✅ **YANET Binaries** - Availability check for all YANET CLI binaries
- ✅ **Network Interfaces** - Network interfaces and socket devices check
- ✅ **PacketParser** - Packet parser functionality check
- ✅ **System Resources** - System resources check (memory, CPU, hugepages)
- ✅ **Final Statistics** - YANET statistics collection and check

### 2. PacketParser Tests
**Tests for checking packet parser functionality:**

- `packet_parser_test.go` - Comprehensive PacketParser tests
- `packet_parser_simple_test.go` - Simple tunneling tests

### 3. YANET Module Tests
**Tests for checking YANET module functionality:**

- `acl_test.go` - ACL module tests
- `forward_test.go` - Forward module tests  
- `nat64_test.go` - NAT64 module tests
- `nat64_simple_test.go` - Simple NAT64 tests

⚠️ **Note**: YANET module tests require updates for compatibility with the new framework API.

## Running Tests

### Run main framework test:
```bash
go test -v ./tests/functional -run TestFramework -timeout 5m
```

### Run PacketParser tests:
```bash
go test -v ./tests/functional -run TestPacketParser
```

### Run all tests:
```bash
go test -v ./tests/functional -timeout 5m
```

## What the framework test checks

### VM and system:
- QEMU VM startup with correct configuration
- Basic command execution (`whoami`, `pwd`, `date`, `uname`, `cat /proc/meminfo`)
- Filesystem check (`/mnt`, `/mnt/build`)
- System resources check (memory, CPU, hugepages)

### YANET components:
- CLI binaries:
  - `/mnt/target/release/yanet-cli` (main CLI)
  - `/mnt/target/release/yanet-cli-*` (module CLIs)
- Main components:
  - `/mnt/build/dataplane/yanet-dataplane`
  - `/mnt/build/controlplane/yanet-controlplane`

### Network functionality:
- Network interfaces
- Framework socket clients
- PacketParser for packet analysis

### Statistics and monitoring:
- YANET statistics collection
- Various metrics availability check
- Results logging

## Architecture

Framework test uses:
- **TestFramework** - main class for test environment management
- **QEMUManager** - QEMU VM management
- **CLIManager** - VM command execution
- **PacketParser** - network packet analysis
- **Structured logging** - detailed logging with zap

## Result

After successful framework test execution, it confirms:
1. ✅ VM starts and works correctly
2. ✅ All system commands execute
3. ✅ YANET binaries are available (or warnings are logged)
4. ✅ Network functionality works
5. ✅ PacketParser functions correctly
6. ✅ Statistics are collected correctly

This single test replaces multiple small tests and provides comprehensive checking of all testing framework functionality.