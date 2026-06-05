package fwstate_test

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// Memory sizes for the fwstate functional harness.
const (
	fwsCPSize  = 64 * datasize.MB
	fwsDPSize  = 4 * datasize.MB
	fwsMemSize = 16 * datasize.MB
)

// syncMulticastAddr is the IPv6 multicast destination configured on the fwstate module.
var syncMulticastAddr = net.IP{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}

// syncPort is the UDP destination port for fwstate sync packets (host byte order).
const syncPort = 9999

// setupFWStateHarness builds a dataplane harness with the fwstate module and
// a plain device, attaches a control-plane agent, and wires a minimal pipeline.
// The returned agent and module config are ready for UpdateModules.
func setupFWStateHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(fwsCPSize),
		DPMemory:      uint64(fwsDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"fwstate"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwstate-test", 0, fwsMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return h, agent
}

// configureFWState creates and publishes a fwstate module config with sync
// enabled for syncMulticastAddr:syncPort, one worker, 1024-entry maps.
func configureFWState(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	modCfg, err := cfwstate.NewModuleConfig(agent, name)
	require.NoError(t, err)
	t.Cleanup(modCfg.Free)

	modCfg.SetSyncConfig(cfwstate.SyncConfig{
		DstAddrMulticast: [16]byte{
			0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01,
		},
		PortMulticast: syncPort,
		TcpSynAck:     uint64(120e9),
		TcpSyn:        uint64(120e9),
		TcpFin:        uint64(120e9),
		Tcp:           uint64(120e9),
		Udp:           uint64(30e9),
		Default:       uint64(16e9),
	})

	require.NoError(t, modCfg.CreateMaps(cfwstate.MapConfig{
		IndexSize:        1024,
		ExtraBucketCount: 64,
	}, 1))

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{modCfg.AsFFIModule()}))
}

// wireFWSPipeline wires chain[fwstate:name] into a pipeline bound to port0.
func wireFWSPipeline(t *testing.T, agent *ffi.Agent, name string) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: name,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: name + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "fwstate", Name: name},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      name,
		Functions: []string{name},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: name, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

// buildSyncPacketLayers returns the gopacket layers for a fwstate sync packet:
// Ethernet (VLAN) -> 802.1Q (IPv6) -> IPv6 (UDP) -> UDP -> syncFrame payload.
//
// srcIP6 is the IPv6 source address. An all-zero source marks an internal
// originator; any non-zero source is external.
func buildSyncPacketLayers(srcIP6 net.IP, syncFrame []byte) []gopacket.SerializableLayer {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		DstMAC:       net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}, // multicast
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := &layers.Dot1Q{
		Type: layers.EthernetTypeIPv6,
	}
	// IPv6 payload_len = sizeof(UDP header) + sizeof(sync frame) = 8 + 56 = 64.
	ip6 := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      srcIP6,
		DstIP:      syncMulticastAddr,
	}
	udp := &layers.UDP{
		SrcPort: layers.UDPPort(12345),
		DstPort: layers.UDPPort(syncPort),
	}
	_ = udp.SetNetworkLayerForChecksum(ip6)
	payload := gopacket.Payload(syncFrame)
	return []gopacket.SerializableLayer{eth, dot1q, ip6, udp, payload}
}

// buildSyncFrame builds a 56-byte fw_state_sync_frame with the given addresses.
// addr_type=6 indicates an IPv6 state entry.
func buildSyncFrame(srcIP6, dstIP6 net.IP) []byte {
	// fw_state_sync_frame layout (56 bytes, packed):
	//   dst_ip      uint32  [0:4]
	//   src_ip      uint32  [4:8]
	//   dst_port    uint16  [8:10]
	//   src_port    uint16  [10:12]
	//   fib         uint8   [12]
	//   proto       uint8   [13]
	//   flags       uint8   [14]   (union, 1 byte)
	//   addr_type   uint8   [15]
	//   dst_ip6     [16]byte [16:32]
	//   src_ip6     [16]byte [32:48]
	//   flow_id6    uint32  [48:52]
	//   extra       uint32  [52:56]
	frame := make([]byte, 56)
	frame[13] = uint8(layers.IPProtocolUDP) // proto
	frame[15] = 6                           // addr_type = IPv6
	copy(frame[16:32], dstIP6.To16())
	copy(frame[32:48], srcIP6.To16())
	return frame
}

// buildSyncHeaderBytes returns the 66 on-wire bytes that precede the UDP payload
// for a fwstate sync packet (Ethernet 14 + VLAN 4 + IPv6 40 + UDP 8).
//
// payloadLen is the UDP payload length in bytes (IPv6 payload_len = 8 + payloadLen).
func buildSyncHeaderBytes(srcIP6 net.IP, payloadLen uint16) []byte {
	hdr := make([]byte, 66)

	// Ethernet header (14 bytes): dst, src, ether_type=0x8100 (VLAN)
	copy(hdr[0:6], []byte{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}) // multicast dst
	copy(hdr[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(hdr[12:14], 0x8100) // EtherType VLAN

	// 802.1Q header (4 bytes): TCI=0, ether_type=0x86DD (IPv6)
	binary.BigEndian.PutUint16(hdr[14:16], 0x0000) // TCI (VLAN 0, priority 0)
	binary.BigEndian.PutUint16(hdr[16:18], 0x86dd) // EtherType IPv6

	// IPv6 header (40 bytes) at offset 18.
	// version=6, traffic class=0, flow label=0
	hdr[18] = 0x60
	// payload_len = 8 (UDP header) + payloadLen
	ip6PayloadLen := uint16(8) + payloadLen
	binary.BigEndian.PutUint16(hdr[22:24], ip6PayloadLen)
	hdr[24] = uint8(layers.IPProtocolUDP) // next header
	hdr[25] = 64                          // hop limit
	copy(hdr[26:42], net.IPv6zero)        // src
	if len(srcIP6) > 0 {
		copy(hdr[26:42], srcIP6.To16())
	}
	copy(hdr[42:58], syncMulticastAddr.To16()) // dst

	// UDP header (8 bytes) at offset 58.
	binary.BigEndian.PutUint16(hdr[58:60], 12345)    // src port
	binary.BigEndian.PutUint16(hdr[60:62], syncPort) // dst port
	udpLen := uint16(8) + payloadLen
	binary.BigEndian.PutUint16(hdr[62:64], udpLen) // length
	binary.BigEndian.PutUint16(hdr[64:66], 0)      // checksum (zero, not verified)

	return hdr
}

// TestFWStateSyncPacketOOBGuard is a regression test for the fwstate
// sync-packet OOB read (#568).
//
// Pre-fix, fwstate accessed sync frames contiguously from segment 0 of the
// mbuf via rte_pktmbuf_mtod_offset, walking off segment 0's tight buffer when
// the payload was in segment 1. Post-fix, the rte_pktmbuf_data_len bound check
// in fwstate_sync_payload_len rejects such packets as non-contiguous-sync and
// passes them to output unchanged.
func TestFWStateSyncPacketOOBGuard(t *testing.T) {
	externalSrc := net.IP{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	internalSrc := net.IPv6zero
	syncFrame := buildSyncFrame(externalSrc, syncMulticastAddr)

	t.Run("well_formed_external", func(t *testing.T) {
		// A fully contiguous, well-formed sync packet from an external source
		// must be consumed by fwstate: it is processed and dropped (not forwarded).
		h, agent := setupFWStateHarness(t)
		configureFWState(t, agent, "test")
		wireFWSPipeline(t, agent, "test")

		pkt := xpacket.LayersToPacket(t, buildSyncPacketLayers(externalSrc, syncFrame)...)
		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Drop, 1, "external sync packet must be consumed (dropped)")
		require.Empty(t, result.Output, "external sync packet must not reach output")
	})

	t.Run("multiseg_external", func(t *testing.T) {
		// THE reproducer: headers in segment 0, sync frame in segment 1.
		//
		// The IPv6 payload_len field is set consistently (8+56=64) so parse_packet
		// accepts the packet. Post-fix, fwstate_sync_payload_len rejects it because
		// claimed_payload (56) > bytes resident in segment 0 (0 past the headers),
		// so the packet is passed to output. Pre-fix, fwstate walks off segment 0.
		h, agent := setupFWStateHarness(t)
		configureFWState(t, agent, "test")
		wireFWSPipeline(t, agent, "test")

		seg0 := buildSyncHeaderBytes(externalSrc, uint16(len(syncFrame)))
		seg1 := make([]byte, len(syncFrame))
		copy(seg1, syncFrame)

		rawResult, err := h.HandleSegmentedPackets([][]byte{seg0, seg1})
		require.NoError(t, err)
		// Post-fix: packet is rejected as non-contiguous-sync and passed through.
		require.Len(t, rawResult.Output, 1, "multi-segment external sync must be passed through post-fix")
		require.Empty(t, rawResult.Drop, "multi-segment external sync must not be dropped post-fix")
	})

	t.Run("multiseg_internal", func(t *testing.T) {
		// Same two-segment split but with an all-zero IPv6 source (internal
		// originator). Post-fix, the rte_pktmbuf_data_len bound rejects it
		// before the internal/external check, so the packet reaches output.
		h, agent := setupFWStateHarness(t)
		configureFWState(t, agent, "test")
		wireFWSPipeline(t, agent, "test")

		internalFrame := buildSyncFrame(internalSrc, syncMulticastAddr)
		seg0 := buildSyncHeaderBytes(internalSrc, uint16(len(internalFrame)))
		seg1 := make([]byte, len(internalFrame))
		copy(seg1, internalFrame)

		rawResult, err := h.HandleSegmentedPackets([][]byte{seg0, seg1})
		require.NoError(t, err)
		// Post-fix: non-contiguous packet is passed through regardless of source.
		require.Len(t, rawResult.Output, 1, "multi-segment internal sync must be passed through post-fix")
		require.Empty(t, rawResult.Drop, "multi-segment internal sync must not be dropped post-fix")
	})
}
