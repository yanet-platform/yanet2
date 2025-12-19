package balancer

import (
	"fmt"
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
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
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
func createBigConfig(
	vsCount int,
	realsPerVs int,
	rng *rand.Rand,
) *balancerpb.ModuleConfig {
	virtualServices := make([]*balancerpb.VirtualService, 0, vsCount)

	for range vsCount {
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

func (key *vsKey) String() string {
	return fmt.Sprintf("%s:%d/%s", key.ip, key.port, key.proto)
}

func vsKeyFromPacket(
	t *testing.T,
	packet *framework.PacketInfo,
) (*vsKey, *netip.Addr) {
	t.Helper()
	if !packet.IsTunneled {
		t.Errorf("Output packet is not tunneled")
		return nil, nil
	}

	if packet.InnerPacket == nil {
		t.Errorf("Output packet has no inner packet")
		return nil, nil
	}

	innerPkt := packet.InnerPacket

	// Get destination IP of the tunneled packet (should be a real)
	realIP, ok := netip.AddrFromSlice(packet.DstIP)
	if !ok {
		t.Errorf("Invalid real IP in output packet")
		return nil, nil
	}

	// Get inner packet details
	_, ok = netip.AddrFromSlice(innerPkt.SrcIP)
	if !ok {
		t.Errorf("Invalid inner src IP")
		return nil, nil
	}

	innerDstIP, ok := netip.AddrFromSlice(innerPkt.DstIP)
	if !ok {
		t.Errorf("Invalid inner dst IP")
		return nil, nil
	}

	dstPort := packet.DstPort

	// Determine protocol from inner packet
	transportProto, ok := innerPkt.GetTransportProtocol()
	if !ok {
		t.Errorf("Unable to determine transport protocol from inner packet")
		return nil, nil
	}

	var proto balancerpb.TransportProto
	switch transportProto {
	case layers.IPProtocolTCP:
		proto = balancerpb.TransportProto_TCP
	case layers.IPProtocolUDP:
		proto = balancerpb.TransportProto_UDP
	default:
		t.Errorf("Unknown transport protocol: %v", transportProto)
		return nil, nil
	}

	// Find the VS this packet was sent to
	key := vsKey{
		ip:    innerDstIP,
		port:  dstPort,
		proto: proto,
	}

	return &key, &realIP
}

// vsInfo contains information about a virtual service for validation
type vsInfo struct {
	realAddrs    map[netip.Addr]bool
	enabledReals map[netip.Addr]bool
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
			realAddrs:    make(map[netip.Addr]bool),
			enabledReals: make(map[netip.Addr]bool),
		}

		for _, real := range vs.Reals {
			realIP, ok := netip.AddrFromSlice(real.DstAddr)
			if ok {
				info.realAddrs[realIP] = true
				info.enabledReals[realIP] = real.Enabled
			}
		}

		vsMap[key] = info
	}

	return vsMap
}

////////////////////////////////////////////////////////////////////////////////

// createRealUpdate creates a RealUpdate for enabling/disabling a real
func createRealUpdate(
	vs *balancerpb.VirtualService,
	real *balancerpb.Real,
	enable bool,
) lib.RealUpdate {
	vsIP, _ := netip.AddrFromSlice(vs.Addr)
	realIP, _ := netip.AddrFromSlice(real.DstAddr)

	var proto lib.Proto
	if vs.Proto == balancerpb.TransportProto_TCP {
		proto = lib.ProtoTcp
	} else {
		proto = lib.ProtoUdp
	}

	return lib.RealUpdate{
		Real: lib.RealIdentifier{
			Vs: lib.VsIdentifier{
				Ip:    vsIP,
				Port:  uint16(vs.Port),
				Proto: proto,
			},
			Ip: realIP,
		},
		Weight: uint16(real.Weight),
		Enable: enable,
	}
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

	// Create initial config: 10 VS with 50 reals each
	initialConfig := createBigConfig(10, 50, rng)

	// Common setup for both phases
	setup, err := SetupTest(&TestConfig{
		moduleConfig: initialConfig,
		stateConfig:  stateConfig,
		mock: &mock.YanetMockConfig{
			CpMemory: datasize.GB * 2,
			DpMemory: datasize.MB * 512,
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

	t.Logf(
		"Setup initial config with 10 VS and 50 reals per VS (total 500 reals), elapsed on balancer creation: %v",
		setup.stats.balancerCreationTime,
	)

	// Set initial time
	setup.mock.SetCurrentTime(time.Unix(0, 0))

	// Phase 1: Test with 10 VS and 50 reals each
	t.Run("Phase1_10VS_50Reals", func(t *testing.T) {
		testPacketSending(t, setup, initialConfig, rng, 10, 10000)
	})

	// Phase 2: Update to 20 VS with 20 reals each
	t.Run("Phase2_100VS_50Reals", func(t *testing.T) {
		newConfig := createBigConfig(20, 20, rng)

		updateStart := time.Now()
		err := setup.balancer.Update(newConfig, stateConfig)
		require.NoError(t, err)

		t.Logf(
			"Setup config to 20 VS and 20 reals per VS (total 400 reals), elapsed on config update: %v",
			time.Since(updateStart),
		)

		testPacketSending(t, setup, newConfig, rng, 10, 10000)
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

	// Extract VS information for packet generation
	virtualServices := config.VirtualServices

	for batch := range numBatches {
		// Disable random half of reals before batch
		var disableUpdates []lib.RealUpdate
		var enableUpdates []lib.RealUpdate
		disabledCount := 0

		for _, vs := range config.VirtualServices {
			// Randomly select half of reals to disable
			numToDisable := len(vs.Reals) / 2
			indices := rng.Perm(len(vs.Reals))

			for i, real := range vs.Reals {
				shouldDisable := false
				for j := 0; j < numToDisable; j++ {
					if indices[j] == i {
						shouldDisable = true
						break
					}
				}

				if shouldDisable {
					disableUpdates = append(
						disableUpdates,
						createRealUpdate(vs, real, false),
					)
					disabledCount++
				} else {
					enableUpdates = append(enableUpdates, createRealUpdate(vs, real, true))
				}
			}
		}

		// Apply disable updates and measure time
		disableStartTime := time.Now()
		err := setup.balancer.UpdateReals(disableUpdates, false)
		require.NoError(t, err)
		disableDuration := time.Since(disableStartTime)

		// Rebuild VS maps with updated enabled state
		vsMap := buildVSMaps(config)

		t.Logf(
			"Batch %d/%d: Disabled %d reals in %v",
			batch+1,
			numBatches,
			disabledCount,
			disableDuration,
		)

		packets := make([]gopacket.Packet, 0, packetsPerBatch)

		// Generate packets to existing VS (90% of packets)
		existingVSPackets := packetsPerBatch * 9 / 10
		for range existingVSPackets {
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
			// Use ephemeral port range, avoiding well-known ports like 4789 (VXLAN)
			// Range: 32768-61000 to avoid protocol detection issues
			srcPort := uint16(32768 + rng.Intn(28232))

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

			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		// Generate packets to non-existent VS (10% of packets)
		nonExistentPackets := packetsPerBatch - existingVSPackets
		for range nonExistentPackets {
			// Generate a random IP that's unlikely to match any VS
			nonExistentIP := netip.AddrFrom4([4]byte{
				byte(200 + rng.Intn(55)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
				byte(rng.Intn(256)),
			})
			nonExistentPort := uint16(60000 + rng.Intn(5535))

			srcIP := generateRandomIPv4(rng)
			// Use ephemeral port range, avoiding well-known ports
			srcPort := uint16(32768 + rng.Intn(28232))

			packetLayers := MakeUDPPacket(
				srcIP,
				srcPort,
				nonExistentIP,
				nonExistentPort,
			)

			packets = append(
				packets,
				xpacket.LayersToPacket(t, packetLayers...),
			)
		}

		totalPackets += len(packets)

		// Send packets
		handleStartTime := time.Now()
		result, err := setup.mock.HandlePackets(packets...)
		require.NoError(t, err)
		handleDuration := time.Since(handleStartTime)

		totalOutput += len(result.Output)
		totalDrop += len(result.Drop)

		assert.Equal(
			t,
			existingVSPackets,
			len(result.Output),
			"all packets should be tunneled",
		)

		// Validate output packets
		for _, outPkt := range result.Output {
			// Check packet is tunneled
			key, realIP := vsKeyFromPacket(t, outPkt)
			if key == nil || realIP == nil {
				continue
			}

			vsInfo, exists := vsMap[*key]
			if !exists {
				// all packets to non existent VS should be dropped
				t.Errorf(
					"Packet tunneled to non-existent VS %s:%d",
					key.ip,
					key.port,
				)
				continue
			}

			// Check that the real IP is in the VS's real list
			if !vsInfo.realAddrs[*realIP] {
				t.Errorf(
					"Packet tunneled to real %s which is not in VS %s:%d reals",
					*realIP,
					key.ip,
					key.port,
				)
				continue
			}

			// Check that the real is enabled
			if !vsInfo.enabledReals[*realIP] {
				t.Errorf("Packet tunneled to DISABLED real %s in VS %s:%d",
					*realIP, key.ip, key.port)
				continue
			}

			// All checks passed
			correctPackets++
		}

		// Validate drop packets
		for _, dropPkt := range result.Drop {
			// Check packet is tunneled
			if !dropPkt.IsTunneled {
				correctPackets += 1
			} else {
				t.Errorf("Drop packet should not be tunneled")
			}
		}

		// Calculate RPS (requests per second)
		rps := float64(len(packets)) / handleDuration.Seconds()

		// Log progress
		t.Logf(
			"Batch %d/%d: Sent %d packets, Output=%d, Drop=%d, Correct packets: %d/%d, HandleTime=%v, RPS=%.0f",
			batch+1,
			numBatches,
			len(packets),
			len(result.Output),
			len(result.Drop),
			correctPackets,
			totalPackets,
			handleDuration,
			rps,
		)

		// Re-enable all reals after batch and measure time
		enableStartTime := time.Now()
		err = setup.balancer.UpdateReals(enableUpdates, false)
		require.NoError(t, err)
		enableDuration := time.Since(enableStartTime)

		t.Logf(
			"Batch %d/%d: Re-enabled all reals in %v",
			batch+1,
			numBatches,
			enableDuration,
		)

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
	correctRate := float64(correctPackets) / float64(totalPackets) * 100

	t.Logf(
		"Final statistics: Total=%d, Output=%d (%.2f%%), Drop=%d (%.2f%%), Correct=%d (%.2f%% of total)",
		totalPackets,
		totalOutput,
		outputRate,
		totalDrop,
		dropRate,
		correctPackets,
		correctRate,
	)

	// Get final state info
	stateInfo := setup.balancer.GetStateInfo(setup.mock.CurrentTime())
	t.Logf("Active sessions: %d", stateInfo.ActiveSessions.Value)
	t.Logf("Session table capacity: %d",
		setup.balancer.GetModuleConfigState().SessionTableCapacity())

	// Validate state info consistency
	ValidateStateInfo(
		t,
		stateInfo,
		setup.balancer.GetModuleConfig().VirtualServices,
	)
}
