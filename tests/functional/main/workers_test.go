package functional

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// workerAssignmentSnapshot mirrors one assignment of the workers counters
// view: the polled device and queue with the pair's rx service weight.
type workerAssignmentSnapshot struct {
	DeviceID uint32 `json:"device_id"`
	QueueID  uint32 `json:"queue_id"`
	Weight   uint32 `json:"weight"`
}

// workerCounterSnapshot mirrors one worker of the workers counters view.
type workerCounterSnapshot struct {
	WorkerIdx   uint32                     `json:"worker_idx"`
	RxPackets   uint64                     `json:"rx_packets"`
	RxBursts    []uint64                   `json:"rx_bursts"`
	Assignments []workerAssignmentSnapshot `json:"assignments"`
}

// workerCounters reads the workers counters view of the running dataplane.
func workerCounters(t *testing.T, fw *framework.TestFramework) []workerCounterSnapshot {
	t.Helper()

	output, err := fw.ExecuteCommand(
		fw.Paths.CLI("yanet-cli-counters") + " workers --format json",
	)
	require.NoError(t, err)

	var response struct {
		Workers []workerCounterSnapshot `json:"workers"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &response))
	return response.Workers
}

// nonEmptyBursts sums the polls that returned at least one packet.
func nonEmptyBursts(worker workerCounterSnapshot) uint64 {
	var total uint64
	for _, count := range worker.RxBursts[1:] {
		total += count
	}
	return total
}

// multiAssignmentDataplaneConfig serves the default harness devices from
// one worker.
//
// The first device declares its queue through a queues list and the tap
// through queue_count with device defaults, so both declaration shapes
// boot; the worker's assignments carry unequal rx service weights.
func multiAssignmentDataplaneConfig() string {
	return `
dataplane:
  storage: /dev/hugepages/yanet
  dpdk_memory: 128
  loglevel: trace
  instances:
    - dp_memory: 96 MiB
      cp_memory: 128 MiB
      numa_id: 0
  devices:
    - port_name: 01:00.0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      queues:
        - rx_queue_len: 1024
          tx_queue_len: 1024
          num_mbufs: 2048
    - port_name: virtio_user_kni0
      mac_addr: 52:54:00:6b:ff:a5
      mtu: 7000
      max_lro_packet_size: 7200
      rss_hash: 0
      default_queue:
        rx_queue_len: 1024
        tx_queue_len: 1024
        num_mbufs: 2048
      queue_count: 1
  workers:
    - core_id: 0
      instance_id: 0
      assignments:
        - device: 01:00.0
          queue: 0
          weight: 3
        - device: virtio_user_kni0
          queue: 0
          weight: 1
  connections:
    - src_device: 01:00.0
      dst_device: virtio_user_kni0
    - src_device: virtio_user_kni0
      dst_device: 01:00.0
`
}

// Test_Worker_MultiAssignment verifies that one worker serves queues of
// several devices with their configured rx service weights.
//
// The default boot keeps one single-assignment worker per queue. The test
// then switches the dataplane to one worker holding both queues, forwards
// echo traffic whose requests and replies each enter through a different
// assignment, and reads the assignments, weights and rx counters back
// through the workers counters view.
func Test_Worker_MultiAssignment(t *testing.T) {
	withBootedVM(t, func(fw *framework.TestFramework) {
		defaultWorkers := workerCounters(t, fw)
		require.Len(t, defaultWorkers, 2, "the default config serves each queue from its own worker")
		for _, worker := range defaultWorkers {
			require.Len(t, worker.Assignments, 1, "worker %d", worker.WorkerIdx)
			require.Equal(t, uint32(1), worker.Assignments[0].Weight, "worker %d", worker.WorkerIdx)
		}

		fw.AdoptRunningConfig(multiAssignmentDataplaneConfig(), framework.DefaultControlplaneConfig())
		require.NoError(t, fw.RestartYANET())
		require.NoError(t, fw.WaitForDatapathReady(15*time.Second))

		switched := workerCounters(t, fw)
		require.Len(t, switched, 1, "one worker serves both queues")
		require.Equal(
			t,
			[]workerAssignmentSnapshot{
				{DeviceID: 0, QueueID: 0, Weight: 3},
				{DeviceID: 1, QueueID: 0, Weight: 1},
			},
			switched[0].Assignments,
		)

		// A train of echo requests enters the weight-3 ingress queue; the
		// kernel's replies enter through the tap's assignment, so every
		// echo crosses the worker twice.
		const train = 40
		packet := createICMPPacket(
			net.ParseIP(framework.VMIPv4Gateway),
			net.ParseIP(framework.VMIPv4Host),
			[]byte("multi-assignment worker"),
		)

		client, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, client.Connect())

		requests := make([][]byte, train)
		for idx := range requests {
			requests[idx] = packet
		}
		require.NoError(t, client.SendPackets(requests, ""))

		replies := 0
		deadline := time.Now().Add(10 * time.Second)
		for replies < train && time.Now().Before(deadline) {
			reply, err := client.ReceivePacket(time.Until(deadline), "")
			if err != nil {
				break
			}
			if isEchoReply(reply) {
				replies++
			}
		}
		require.Equal(t, train, replies, "every request of the train is answered")

		served := workerCounters(t, fw)
		require.Len(t, served, 1)
		require.GreaterOrEqual(
			t,
			served[0].RxPackets-switched[0].RxPackets,
			uint64(2*train),
			"requests and replies both arrive through the one worker",
		)
		require.Greater(
			t,
			nonEmptyBursts(served[0]),
			nonEmptyBursts(switched[0]),
			"the rx burst histogram records the served polls",
		)
	})
}

// Whether the frame is an ICMP echo reply of the host.
func isEchoReply(data []byte) bool {
	packet := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.NoCopy)
	icmp, ok := packet.Layer(layers.LayerTypeICMPv4).(*layers.ICMPv4)
	if !ok || icmp == nil {
		return false
	}
	ipv4, ok := packet.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	return ok && ipv4 != nil &&
		icmp.TypeCode.Type() == layers.ICMPv4TypeEchoReply &&
		ipv4.SrcIP.Equal(net.ParseIP(framework.VMIPv4Host))
}
