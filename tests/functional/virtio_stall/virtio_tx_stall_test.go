package virtiostall

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

const floodPacketCount = 65536

// virtioTXQueueLen must match the tx_queue_len (and rx_queue_len) baked into
// the virtio worker config by the surgery in TestMain below.
const virtioTXQueueLen = 32

var testHarness *framework.Harness

func TestMain(m *testing.M) {
	dataplane := framework.DataplaneConfig(framework.DataplaneOptions{})
	// The phy worker's mempool must cover its rx ring plus the bounded
	// pending occupancy of the phy->virtio relay: packets still in the pipe
	// and the small accepted-but-unreclaimed tail. Keep enough headroom for
	// that plus the post-flood marker packet.
	dataplane = strings.Replace(dataplane, "num_mbufs: 2048", "num_mbufs: 4608", 1)
	if !strings.Contains(dataplane, "num_mbufs: 4608") {
		fmt.Fprintf(os.Stderr, "failed to set up virtio stall harness: num_mbufs replacement did not take effect, dataplane config template drifted\n")
		os.Exit(1)
	}
	dataplane = strings.Replace(
		dataplane,
		"port_name: virtio_user_kni0\n      mac_addr: 52:54:00:6b:ff:a5\n      mtu: 7000\n      max_lro_packet_size: 7200\n      rss_hash: 0\n      workers:\n        - core_id: 0\n          instance_id: 0\n          rx_queue_len: 1024\n          tx_queue_len: 1024",
		"port_name: virtio_user_kni0\n      mac_addr: 52:54:00:6b:ff:a5\n      mtu: 7000\n      max_lro_packet_size: 7200\n      rss_hash: 0\n      workers:\n        - core_id: 0\n          instance_id: 0\n          rx_queue_len: 32\n          tx_queue_len: 32",
		1,
	)
	if !strings.Contains(dataplane, "rx_queue_len: 32\n          tx_queue_len: 32") {
		fmt.Fprintf(os.Stderr, "failed to set up virtio stall harness: virtio worker queue length replacement did not take effect, dataplane config template drifted\n")
		os.Exit(1)
	}

	harness, cleanup, err := framework.SetupHarness(framework.HarnessConfig{
		PoolName: "virtio-stall",
		// Bump this tag whenever the dataplane/CLI binaries or the config
		// surgery above change: a cached baseline snapshot bakes in the
		// binaries copied at bake time and is otherwise reused stale.
		BaselineTag: "virtio-stall-v3",
		Dataplane:   dataplane,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up virtio stall harness: %v\n", err)
		os.Exit(1)
	}
	testHarness = harness

	code := m.Run()
	if err := harness.Shutdown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to shut down virtio stall harness: %v\n", err)
		code = 1
	}
	cleanup()
	os.Exit(code)
}

type workerCounter struct {
	WorkerIdx       uint32 `json:"worker_idx"`
	DeviceID        uint32 `json:"device_id"`
	RXPackets       uint64 `json:"rx_packets"`
	TXPackets       uint64 `json:"tx_packets"`
	RemoteRXPackets uint64 `json:"remote_rx_packets"`
	RemoteTXPackets uint64 `json:"remote_tx_packets"`
	LocalTXDrops    uint64 `json:"local_tx_drops"`
	RemoteTXDrops   uint64 `json:"remote_tx_drops"`
	Drops           uint64 `json:"drops"`
	RemoteTXPending uint64 `json:"remote_tx_pending"`
}

type workerCountersResponse struct {
	Workers []workerCounter `json:"workers"`
}

// tryReadWorkerCounters fetches the current worker counter snapshot without
// failing the test on error, so it is safe to poll from inside
// require.Eventually.
func tryReadWorkerCounters(fw *framework.TestFramework) (workerCountersResponse, error) {
	output, err := fw.ExecuteCommand(
		fw.Paths.CLI("yanet-cli-counters") + " workers --format json",
	)
	if err != nil {
		return workerCountersResponse{}, err
	}

	var response workerCountersResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return workerCountersResponse{}, err
	}
	if len(response.Workers) != 2 {
		return workerCountersResponse{}, fmt.Errorf(
			"unexpected worker count: %d", len(response.Workers),
		)
	}
	return response, nil
}

// readWorkerCounters polls tryReadWorkerCounters until it succeeds. The
// serial-console capture can interleave terminal control sequences with the
// JSON payload, so a single read may fail to parse transiently.
func readWorkerCounters(t *testing.T, fw *framework.TestFramework) workerCountersResponse {
	t.Helper()

	var response workerCountersResponse
	require.Eventually(t, func() bool {
		var err error
		response, err = tryReadWorkerCounters(fw)
		return err == nil
	}, 5*time.Second, 250*time.Millisecond, "failed to read worker counters")

	return response
}

func createTaggedSilentUDPv6Packet() []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 411,
		Type:           layers.EthernetTypeIPv6,
	}
	ipv6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		// The default forward rule sends fe80::/64 to kni0. This address is
		// deliberately not local, so Linux silently discards the packet.
		DstIP: net.ParseIP("fe80::dead:beef"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	if err := udp.SetNetworkLayerForChecksum(&ipv6); err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(
		buf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		&eth,
		&vlan,
		&ipv6,
		&udp,
		gopacket.Payload(make([]byte, 64)),
	); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func createMarkerPacket(seq uint16) []byte {
	return framework.CreateICMPv4EchoPacket(
		net.ParseIP(framework.VMIPv4Gateway),
		net.ParseIP(framework.VMIPv4Host),
		1,
		seq,
		[]byte("virtio tx recovery marker"),
	)
}

func requireMarkerReply(t *testing.T, fw *framework.TestFramework, seq uint16) {
	t.Helper()
	_, reply, err := fw.SendPacketAndParse(0, 0, createMarkerPacket(seq), time.Second)
	require.NoError(t, err, "physical -> virtio -> kernel did not recover")
	require.NotNil(t, reply)
	require.Equal(t, framework.VMIPv4Host, reply.SrcIP.String())
	require.Equal(t, framework.VMIPv4Gateway, reply.DstIP.String())
}

func TestVirtioTXRecoversAfterOverload(t *testing.T) {
	testHarness.WithBootedVM(t, func(fw *framework.TestFramework) {
		activeConfig, err := fw.ExecuteCommand("sed -n '/port_name: virtio_user_kni0/,+12p' /tmp/yanet/config/dataplane.yaml")
		require.NoError(t, err)
		t.Logf("active virtio config:\n%s", activeConfig)
		requireMarkerReply(t, fw, 1)
		before := readWorkerCounters(t, fw)

		// Keep the DPDK workers running while temporarily starving the
		// vhost-net completion thread. This deterministically fills the virtio
		// TX ring and makes rte_eth_tx_burst() return a partial write.
		hogStatus, err := fw.ExecuteCommand(`vhost_tid=$(ps -eLo tid,comm | awk '$2 ~ /^vhost-/ {print $1; exit}'); test -n "$vhost_tid"; taskset -pc 1 "$vhost_tid"; nohup taskset -c 1 chrt -f 90 sh -c 'while :; do :; done' >/dev/null 2>&1 & echo $! >/tmp/yanet-vhost-hog.pid; sleep 0.2; ps -o cls=,rtprio=,psr=,comm= -p $(cat /tmp/yanet-vhost-hog.pid); taskset -pc "$vhost_tid"`)
		require.NoError(t, err)
		t.Logf("vhost starvation setup:\n%s", hogStatus)
		require.Contains(t, hogStatus, "FF")
		defer func() {
			_, _ = fw.ExecuteCommand("kill -9 $(cat /tmp/yanet-vhost-hog.pid) 2>/dev/null; rm -f /tmp/yanet-vhost-hog.pid")
		}()

		client, err := fw.GetSocketClient(0)
		require.NoError(t, err)
		require.NoError(t, framework.WithTimeout(30*time.Second)(client))
		require.NoError(t, client.Connect())

		packet := createTaggedSilentUDPv6Packet()
		packets := make([][]byte, floodPacketCount)
		for idx := range packets {
			packets[idx] = packet
		}
		require.NoError(t, client.SendPackets(packets, ""))

		// Read the counters while the hog still starves the vhost-net
		// completion thread, so the pending FIFOs and drop counters reflect
		// the backpressure the flood created.
		afterFlood := readWorkerCounters(t, fw)
		t.Logf("workers before: %+v", before.Workers)
		t.Logf("workers after flood: %+v", afterFlood.Workers)
		require.Greater(t, afterFlood.Workers[0].RemoteTXPending, uint64(0),
			"phy worker did not accumulate pending remote-tx packets under backpressure")
		require.Greater(t, afterFlood.Workers[0].RemoteTXDrops, before.Workers[0].RemoteTXDrops,
			"test did not create inter-worker backpressure")

		_, err = fw.ExecuteCommand("kill -9 $(cat /tmp/yanet-vhost-hog.pid) 2>/dev/null; rm -f /tmp/yanet-vhost-hog.pid")
		require.NoError(t, err)

		// Give vhost-net time to complete accepted descriptors. The dataplane
		// must then make forward progress without another flood or a restart.
		time.Sleep(500 * time.Millisecond)

		require.Eventually(t, func() bool {
			_, reply, markerErr := fw.SendPacketAndParse(
				0, 0, createMarkerPacket(2), 200*time.Millisecond,
			)
			return markerErr == nil && reply != nil
		}, 3*time.Second, 250*time.Millisecond, "virtio TX remained stalled after overload")

		afterRecovery := readWorkerCounters(t, fw)
		t.Logf("workers after recovery: %+v", afterRecovery.Workers)
		require.Greater(t, afterRecovery.Workers[1].TXPackets, afterFlood.Workers[1].TXPackets,
			"virtio worker did not transmit any further packets once the hog was gone")

		// Without a virtio tx_done_cleanup op, the accepted-but-uncleaned tail
		// of the last tx burst stays pinned in the producer's pending FIFO
		// until the next nonzero burst frees it. At quiescence that leaves a
		// small residual bounded by the consumer's TX ring depth, never a
		// wedge, so the FIFO drains down to virtioTXQueueLen, not to zero.
		require.Eventually(t, func() bool {
			response, drainErr := tryReadWorkerCounters(fw)
			if drainErr != nil {
				return false
			}
			return response.Workers[0].RemoteTXPending <= virtioTXQueueLen
		}, 3*time.Second, 250*time.Millisecond, "phy worker's remote-tx pending FIFO did not drain to the bounded residual")
	})
}
