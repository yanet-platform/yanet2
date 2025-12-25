package converted

import (
	"os"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// Global framework instance shared across all converted tests
var globalFramework *framework.TestFramework

// TestMain initializes the test framework for converted tests
func TestMain(m *testing.M) {
	println("TestMain: Starting test framework initialization")
	code := testMainWrapper(m)
	println("TestMain: Exiting with code", code)
	os.Exit(code)
}

// testMainWrapper sets up the test environment and runs tests
func testMainWrapper(m *testing.M) (code int) {
	// Create logger
	lg := zap.NewDevelopmentConfig()
	if !framework.IsDebugEnabled() {
		lg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	} else {
		lg.OutputPaths = []string{"converted_test.log"}
		lg.ErrorOutputPaths = []string{"stderr", "converted_test.log"}
	}

	logger, err := lg.Build()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	// Get QEMU image path (relative to parent functional directory)
	qemuImage := os.Getenv("YANET_QEMU_IMAGE")
	if qemuImage == "" {
		qemuImage = "../yanet-test.qcow2"
	}

	// Create framework
	fw, err := framework.New(&framework.Config{
		Name:      "converted",
		QEMUImage: qemuImage,
	}, framework.WithLog(sugar))
	if err != nil {
		sugar.Errorf("Failed to create framework: %v", err)
		return 1
	}

	globalFramework = fw

	// Ensure cleanup
	defer func() {
		if fw != nil {
			if err := fw.Stop(); err != nil {
				sugar.Errorf("Failed to stop framework: %v", err)
				code = 12
			}
		}
	}()

	// Start framework
	if err := fw.Start(); err != nil {
		sugar.Errorf("Failed to start framework: %v", err)
		return 1
	}

	// Wait for VM to be ready (60 seconds timeout)
	if err := fw.QEMU.WaitForReady(60 * time.Second); err != nil {
		sugar.Errorf("Failed to wait for VM readiness: %v", err)
		return 1
	}

	// Start YANET with converted tests configuration (same as framework_test.go)
	dataplaneConfig := `
dataplane:
  storage: /dev/hugepages/yanet
  dpdk_memory: 1024
  loglevel: trace
  instances:
    - dp_memory: 1073741824
      cp_memory: 1342177280
      numa_id: 0
  devices:
    - port_name: 01:00.0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
    - port_name: virtio_user_kni0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      workers:
        - core_id: 0
          instance_id: 0
          rx_queue_len: 1024
          tx_queue_len: 1024
  connections:
    - src_device_id: 0
      dst_device_id: 1
    - src_device_id: 1
      dst_device_id: 0
`

	controlplaneConfig := `
logging:
  level: debug

modules:
  route:
    link_map:
      kni0: 01:00.0
`

	forwardConfig := `
rules:
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - ` + framework.VMIPv4Host + `/32
      - ` + framework.VMIPv6Host + `/64
      - "ff02::0/16"
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_pass
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
      - "0.0.0.0/0"
      - "::/0"
    dsts:
      - "0.0.0.0/0"
      - "::/0"
    mode: None
    devices:
      - 01:00.0
  - target: virtio_user_kni0
    counter: to_virtio_user_kni0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - 01:00.0
  - target: 01:00.0
    counter: to_01:00.0
    vlan_ranges:
      - from: 0
        to: 4095
    srcs:
    dsts:
    mode: Out
    devices:
      - virtio_user_kni0
`

	sugar.Info("Starting YANET (dataplane + controlplane)...")
	if err := fw.StartYANET(dataplaneConfig, controlplaneConfig); err != nil {
		sugar.Errorf("Failed to start YANET: %v", err)
		return 1
	}

	if err := fw.CreateConfigFile("forward.yaml", forwardConfig); err != nil {
		sugar.Errorf("Failed to create forward config: %v", err)
		return 1
	}

	sugar.Info("Executing common configuration commands...")
	if _, err := fw.CLI.ExecuteCommands(framework.CommonConfigCommands...); err != nil {
		sugar.Errorf("Failed to execute common configuration commands: %v", err)
		return 1
	}

	sugar.Info("Common configuration completed successfully")

	// Run tests
	code = m.Run()

	if framework.IsDebugEnabled() {
		// Copy logs from VM for debugging
		sugar.Info("Copying logs from VM...")
		debugCommands := []string{
			"cp -v /var/log/yanet-controlplane.log /mnt/build/yanet-controlplane-converted.log 2>&1 || echo 'No controlplane log found (converted tests)'",
			"cp -v /var/log/yanet-dataplane.log /mnt/build/yanet-dataplane-converted.log 2>&1 || echo 'No dataplane log found (converted tests)'",
		}
		fw.CLI.ExecuteCommands(debugCommands...)
	}

	return code
}
