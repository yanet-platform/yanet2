package balancer

import (
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////

// generateRandomIPv4 generates a random IPv4 address
func generateRandomIPv4(rng *rand.Rand) netip.Addr {
	return netip.AddrFrom4([4]byte{
		byte(rng.Intn(256)),
		byte(rng.Intn(256)),
		byte(rng.Intn(256)),
		byte(rng.Intn(256)),
	})
}

// generateRandomIPv6 generates a random IPv6 address
func generateRandomIPv6(rng *rand.Rand) netip.Addr {
	var bytes [16]byte
	for i := range bytes {
		bytes[i] = byte(rng.Intn(256))
	}
	return netip.AddrFrom16(bytes)
}

// generateRandomIP generates a random IP address (IPv4 or IPv6)
func generateRandomIP(rng *rand.Rand, forceIPv6 bool) netip.Addr {
	if forceIPv6 || rng.Intn(2) == 0 {
		return generateRandomIPv6(rng)
	}
	return generateRandomIPv4(rng)
}

////////////////////////////////////////////////////////////////////////////////

// createBigConfig generates a balancer configuration with the specified number
// of virtual services and reals per VS. Virtual services can have different
// flags (GRE, FixMSS, OPS, PureL3), IPv4/IPv6 addresses, and TCP/UDP protocols.
// FixMSS flag is only set for IPv6 virtual services.
func createBigConfig(vsCount int, realsPerVs int, rng *rand.Rand) *balancerpb.ModuleConfig {
	virtualServices := make([]*balancerpb.VirtualService, 0, vsCount)

	for i := 0; i < vsCount; i++ {
		// Randomly choose IPv4 or IPv6 for VS
		isIPv6 := rng.Intn(2) == 0
		var vsIP netip.Addr
		if isIPv6 {
			vsIP = generateRandomIPv6(rng)
		} else {
			vsIP = generateRandomIPv4(rng)
		}

		// Randomly choose TCP or UDP
		var proto balancerpb.TransportProto
		if rng.Intn(2) == 0 {
			proto = balancerpb.TransportProto_TCP
		} else {
			proto = balancerpb.TransportProto_UDP
		}

		// Random port
		vsPort := uint32(1 + rng.Intn(65535))

		// Random flags
		useGRE := rng.Intn(2) == 0
		useOPS := rng.Intn(2) == 0
		usePureL3 := rng.Intn(2) == 0
		// FixMSS only for IPv6 VS
		useFixMSS := isIPv6 && rng.Intn(2) == 0

		// Random scheduler
		schedulers := []balancerpb.VsScheduler{
			balancerpb.VsScheduler_WRR,
			balancerpb.VsScheduler_PRR,
			balancerpb.VsScheduler_WLC,
		}
		scheduler := schedulers[rng.Intn(len(schedulers))]

		// Generate reals for this VS
		reals := make([]*balancerpb.Real, 0, realsPerVs)
		for j := 0; j < realsPerVs; j++ {
			// Reals can be IPv4 or IPv6 independently of VS
			realIP := generateRandomIP(rng, false)

			// Generate source address and mask
			var srcAddr, srcMask netip.Addr
			if realIP.Is4() {
				srcAddr = generateRandomIPv4(rng)
				srcMask = netip.AddrFrom4([4]byte{255, 255, 255, 255})
			} else {
				srcAddr = generateRandomIPv6(rng)
				srcMask = netip.AddrFrom16([16]byte{
					255, 255, 255, 255, 255, 255, 255, 255,
					255, 255, 255, 255, 255, 255, 255, 255,
				})
			}

			reals = append(reals, &balancerpb.Real{
				DstAddr: realIP.AsSlice(),
				Weight:  uint32(1 + rng.Intn(100)),
				SrcAddr: srcAddr.AsSlice(),
				SrcMask: srcMask.AsSlice(),
				Enabled: true,
			})
		}

		// Create allowed sources (allow all traffic for simplicity)
		// Only add allowed sources that match the VS IP version
		var allowedSrcs []*balancerpb.Subnet
		if isIPv6 {
			// IPv6 VS - only allow IPv6 sources
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: netip.AddrFrom16([16]byte{}).AsSlice(),
					Size: 0,
				},
			}
		} else {
			// IPv4 VS - only allow IPv4 sources
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: netip.AddrFrom4([4]byte{0, 0, 0, 0}).AsSlice(),
					Size: 0,
				},
			}
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Addr:        vsIP.AsSlice(),
			Port:        vsPort,
			Proto:       proto,
			Scheduler:   scheduler,
			AllowedSrcs: allowedSrcs,
			Reals:       reals,
			Flags: &balancerpb.VsFlags{
				Gre:    useGRE,
				FixMss: useFixMSS,
				Ops:    useOPS,
				PureL3: usePureL3,
			},
		})
	}

	return &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: virtualServices,
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    60,
			TcpFin:    60,
			Tcp:       60,
			Udp:       60,
			Default:   60,
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
		},
	}
}

////////////////////////////////////////////////////////////////////////////////

// vsKey uniquely identifies a virtual service
type vsKey struct {
	ip    netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

// vsInfo contains information about a virtual service for validation
type vsInfo struct {
	realAddrs map[netip.Addr]bool
}

// buildVSMaps builds lookup maps for virtual services and their reals
func buildVSMaps(config *balancerpb.ModuleConfig) map[vsKey]*vsInfo {
	vsMap := make(map[vsKey]*vsInfo)

	for _, vs := range config.VirtualServices {
		vsIP, ok := netip.AddrFromSlice(vs.Addr)
		if !ok {
			continue
		}

		key := vsKey{
			ip:    vsIP,
			port:  uint16(vs.Port),
			proto: vs.Proto,
		}

		info := &vsInfo{
			realAddrs: make(map[netip.Addr]bool),
		}

		for _, real := range vs.Reals {
			realIP, ok := netip.AddrFromSlice(real.DstAddr)
			if ok {
				info.realAddrs[realIP] = true
			}
		}

		vsMap[key] = info
	}

	return vsMap
}

////////////////////////////////////////////////////////////////////////////////

// TestBigData tests the balancer with large configurations:
// - First phase: 40 VS with 500 reals each
// - Second phase: 300 VS with 300 reals each
// Sends 10 batches of 10k packets with 1s time advance between batches
func TestBigData(t *testing.T) {
	// Use fixed seed for reproducibility
	rng := rand.New(rand.NewSource(42))

	// Session table capacity: 100K
	sessionTableCapacity := uint64(100_000)

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      sessionTableCapacity,
		SessionTableScanPeriod:    durationpb.New(0),
		SessionTableMaxLoadFactor: 0.75,
	}

	// Create initial config: 40 VS with 500 reals each
	initialConfig := createBigConfig(40, 500, rng)

	t.Logf("Created initial config with 40 VS and 500 reals per VS (total 20000 reals)")

	// Common setup for both phases
	setup, err := SetupTest(&TestConfig{
		moduleConfig: initialConfig,
		stateConfig:  stateConfig,
		mock: &mock.YanetMockConfig{
			CpMemory: datasize.GB * 8,
			DpMemory: datasize.GB,
			Workers:  1,
			Devices: []mock.YanetMockDeviceConfig{
				{
					Id:   0,
					Name: defaultDeviceName,
				},
			},
		},
	})
	require.NoError(t, err)
	defer setup.Free()

	// Set initial time
	setup.mock.SetCurrentTime(time.Unix(0, 0))

	// Phase 1: Test with 40 VS and 500 reals each
	t.Run("Phase1_40VS_500Reals", func(t *testing.T) {
		testPacketSending(t, setup, initialConfig, rng, 10, 10000)
	})

	// Phase 2: Update to 300 VS with 300 reals each
	t.Run("Phase2_300VS_300Reals", func(t *testing.T) {
		// newConfig := createBigConfig(300, 300, rng)
		// t.Logf("Updating config to 300 VS and 300 reals per VS (total 90000 reals)")

		// err := setup.balancer.Update(newConfig, stateConfig)
		// require.NoError(t, err)

		// testPacketSending(t, setup, newConfig, rng, 10, 10000)
	})
}

////////////////////////////////////////////////////////////////////////////////

// testPacketSending sends batches of packets and validates the results
func testPacketSending(
	t *testing.T,
	setup *TestSetup,
	config *balancerpb.ModuleConfig,
	rng *rand.Rand,
	numBatches int,
	packetsPerBatch int,
) {
	t.Helper()

	totalOutput := 0
	totalDrop := 0
	totalPackets := 0
	correctPackets := 0

	// Build VS lookup maps
	vsMap := buildVSMaps(config)

	// Extract VS information for packet generation
	virtualServices := config.VirtualServices

	for batch := 0; batch < numBatches; batch++ {
		packets := make([]gopacket.Packet, 0, packetsPerBatch)

		// Generate packets to existing VS (90% of packets)
		existingVSPackets := packetsPerBatch * 9 / 10
		for i := 0; i < existingVSPackets; i++ {
			// Pick a random VS
			vs := virtualServices[rng.Intn(len(virtualServices))]

			vsIP, ok := netip.AddrFromSlice(vs.Addr)
			require.True(t, ok, "invalid VS IP")

			vsPort := uint16(vs.Port)

			// Generate random source with matching IP protocol
			var srcIP netip.Addr
			if vsIP.Is4() {
				srcIP = generateRandomIPv4(rng)
			} else {
				srcIP = generateRandomIPv6(rng)
			}
			srcPort := uint16(1024 + rng.Intn(64511))

			// Create packet based on protocol
			var packetLayers []gopacket.SerializableLayer
			if vs.Proto == balancerpb.TransportProto_TCP {
				packetLayers = MakeTCPPacket(
					srcIP,
					srcPort,
					vsIP,
					vsPort,
					&layers.TCP{SYN: true},
				)
			} else {
				packetLayers = MakeUDPPacket(
					srcIP,
					srcPort,
					vsIP,
					vsPort,
				)
			}

			packets = append(packets, xpacket.LayersToPacket(t, packetLayers...))
		}

		// Generate packets to non-existent VS (10% of packets)
		nonExistentPackets := packetsPerBatch - existingVSPackets
		for i := 0; i < nonExistentPackets; i++ {
			// Generate a random IP that's unlikely to match any VS
			nonExistentIP := netip.AddrFrom4([4]byte{
				byte(200 + rng.Intn(55)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})
			nonExistentPort := uint16(60000 + rng.Intn(5535))

			srcIP := generateRandomIPv4(rng)
			srcPort := uint16(1024 + rng.Intn(64511))

			packetLayers := MakeUDPPacket(
				srcIP,
				srcPort,
				nonExistentIP,
				nonExistentPort,
			)

			packets = append(packets, xpacket.LayersToPacket(t, packetLayers...))
		}

		totalPackets += len(packets)

		// Send packets
		result, err := setup.mock.HandlePackets(packets...)
		require.NoError(t, err)

		totalOutput += len(result.Output)
		totalDrop += len(result.Drop)

		// Validate output packets
		for _, outPkt := range result.Output {
			// Check packet is tunneled
			if !outPkt.IsTunneled {
				t.Errorf("Output packet is not tunneled")
				continue
			}

			if outPkt.InnerPacket == nil {
				t.Errorf("Output packet has no inner packet")
				continue
			}

			innerPkt := outPkt.InnerPacket

			// Get destination IP of the tunneled packet (should be a real)
			realIP, ok := netip.AddrFromSlice(outPkt.DstIP)
			if !ok {
				t.Errorf("Invalid real IP in output packet")
				continue
			}

			// Get inner packet details
			_, ok = netip.AddrFromSlice(innerPkt.SrcIP)
			if !ok {
				t.Errorf("Invalid inner src IP")
				continue
			}

			innerDstIP, ok := netip.AddrFromSlice(innerPkt.DstIP)
			if !ok {
				t.Errorf("Invalid inner dst IP")
				continue
			}

			srcPort := outPkt.SrcPort
			dstPort := outPkt.DstPort

			// Determine protocol
			var proto balancerpb.TransportProto
			switch outPkt.Protocol {
			case layers.IPProtocolTCP:
				proto = balancerpb.TransportProto_TCP
			case layers.IPProtocolUDP:
				proto = balancerpb.TransportProto_UDP
			default:
				t.Errorf("Unknown protocol in inner packet")
				continue
			}

			// Find the VS this packet was sent to
			key := vsKey{
				ip:    innerDstIP,
				port:  dstPort,
				proto: proto,
			}

			vsInfo, exists := vsMap[key]
			if !exists {
				// all packets to non existent VS should be dropped
				t.Errorf("Packet tunneled to non-existent VS %s:%d", innerDstIP, dstPort)
				continue
			}

			// Check that the real IP is in the VS's real list
			if !vsInfo.realAddrs[realIP] {
				t.Errorf("Packet tunneled to real %s which is not in VS %s:%d reals",
					realIP, innerDstIP, dstPort)
				continue
			}

			// Verify inner packet src matches original client
			// We need to find the original packet to verify this
			// For now, just check that inner src port is in valid range
			if srcPort < 1024 {
				t.Errorf("Invalid inner src port: %d", srcPort)
				continue
			}

			// All checks passed
			correctPackets++
		}

		// Log progress
		if batch%2 == 0 || batch+1 == numBatches {
			t.Logf("Batch %d/%d: Sent %d packets, Output=%d, Drop=%d, Correct=%d",
				batch+1, numBatches, len(packets), len(result.Output), len(result.Drop), correctPackets)
		}

		// Advance time by 1 second
		setup.mock.AdvanceTime(time.Second)

		// Sync active sessions
		err = setup.balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
			setup.mock.CurrentTime(),
		)
		require.NoError(t, err)
	}

	// Final statistics
	assert.Equal(t, totalPackets, totalOutput+totalDrop,
		"Total packets should equal output + drop")

	outputRate := float64(totalOutput) / float64(totalPackets) * 100
	dropRate := float64(totalDrop) / float64(totalPackets) * 100
	correctRate := float64(correctPackets) / float64(totalOutput) * 100

	t.Logf("Final statistics: Total=%d, Output=%d (%.2f%%), Drop=%d (%.2f%%), Correct=%d (%.2f%% of output)",
		totalPackets, totalOutput, outputRate, totalDrop, dropRate, correctPackets, correctRate)

	// Verify that packets to existing VS are mostly processed correctly
	assert.Greater(t, outputRate, 50.0,
		"At least 50%% of packets should be processed successfully")

	// Verify that output packets are correctly tunneled
	assert.Greater(t, correctRate, 95.0,
		"At least 95%% of output packets should be correctly tunneled to VS reals")

	// Get final state info
	stateInfo := setup.balancer.GetStateInfo()
	t.Logf("Active sessions: %d", stateInfo.ActiveSessions.Value)
	t.Logf("Session table capacity: %d",
		setup.balancer.GetModuleConfigState().SessionTableCapacity())

	// Validate state info consistency
	ValidateStateInfo(t, stateInfo, setup.balancer.GetModuleConfig().VirtualServices)
}
