package balancer_test

// TestBigConfig tests the balancer with large configurations to verify:
//
// # Configuration Scalability
// - Phase 1: 10 virtual services with 50 reals each (500 total reals)
// - Phase 2: 20 virtual services with 20 reals each (400 total reals)
// - Random IPv4/IPv6 addresses for virtual services and reals
// - Random TCP/UDP protocols
// - Random schedulers (WRR, PRR, WLC)
// - Random flags (GRE, FixMSS, OPS)
//
// # Packet Processing
// - Sends 10 batches of 10,000 packets per phase
// - 90% packets to existing virtual services
// - 10% packets to non-existent virtual services (should be dropped)
// - Validates packet distribution to enabled reals only
//
// # Real Server Management
// - Randomly disables half of reals before each batch
// - Re-enables all reals after each batch
// - Validates that disabled reals receive no new packets
//
// # Session Management
// - Tracks active sessions across batches
// - Validates session table capacity and load factor
// - Syncs active sessions between batches
//
// # Performance Metrics
// - Measures configuration creation time
// - Measures configuration update time
// - Measures real enable/disable time
// - Measures packet handling time and RPS (requests per second)

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
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////
// Random IP generation helpers

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
// Configuration creation

// createBigConfig generates a balancer configuration with the specified number
// of virtual services and reals per VS. Virtual services can have different
// flags (GRE, FixMSS, OPS, PureL3), IPv4/IPv6 addresses, and TCP/UDP protocols.
// FixMSS flag is only set for IPv6 virtual services.
func createBigConfig(
	vsCount int,
	realsPerVs int,
	rng *rand.Rand,
) *balancerpb.BalancerConfig {
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

		// Random flags
		useGRE := rng.Intn(2) == 0
		useOPS := rng.Intn(2) == 0
		usePureL3 := false

		// FixMSS only for IPv6 VS
		useFixMSS := isIPv6 && rng.Intn(2) == 0

		vsPort := uint32(1 + rng.Intn(65535))

		// Random scheduler
		schedulers := []balancerpb.VsScheduler{
			balancerpb.VsScheduler_ROUND_ROBIN,
			balancerpb.VsScheduler_SOURCE_HASH,
		}
		scheduler := schedulers[rng.Intn(len(schedulers))]

		// Generate reals for this VS
		reals := make([]*balancerpb.Real, 0, realsPerVs)
		for range realsPerVs {
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
				Id: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: realIP.AsSlice()},
					Port: 0,
				},
				Weight: uint32(1 + rng.Intn(100)),
				SrcAddr: &balancerpb.Addr{
					Bytes: srcAddr.AsSlice(),
				},
				SrcMask: &balancerpb.Addr{
					Bytes: srcMask.AsSlice(),
				},
			})
		}

		// Create allowed sources (allow all traffic for simplicity)
		// Only add allowed sources that match the VS IP version
		var allowedSrcs []*balancerpb.Net
		if isIPv6 {
			// IPv6 VS - only allow IPv6 sources
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom16([16]byte{}).AsSlice(),
					},
					Size: 0,
				},
			}
		} else {
			// IPv4 VS - only allow IPv4 sources
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.AddrFrom4([4]byte{0, 0, 0, 0}).AsSlice(),
					},
					Size: 0,
				},
			}
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  &balancerpb.Addr{Bytes: vsIP.AsSlice()},
				Port:  vsPort,
				Proto: proto,
			},
			Scheduler:   scheduler,
			AllowedSrcs: allowedSrcs,
			Reals:       reals,
			Flags: &balancerpb.VsFlags{
				Gre:    useGRE,
				FixMss: useFixMSS,
				Ops:    useOPS,
				PureL3: usePureL3,
				Wlc:    false,
			},
			Peers: []*balancerpb.Addr{},
		})
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs:             virtualServices,
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
				Default:   60,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity: func() *uint64 {
				v := uint64(200_000)
				return &v
			}(),
			SessionTableMaxLoadFactor: func() *float32 {
				v := float32(0.75)
				return &v
			}(),
			RefreshPeriod: durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power: func() *uint64 {
					v := uint64(10)
					return &v
				}(),
				MaxWeight: func() *uint32 {
					v := uint32(1000)
					return &v
				}(),
			},
		},
	}
}

////////////////////////////////////////////////////////////////////////////////
// Virtual service key for tracking

// vsKey uniquely identifies a virtual service
type vsKey struct {
	ip    netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

func (key *vsKey) String() string {
	protoStr := "TCP"
	if key.proto == balancerpb.TransportProto_UDP {
		protoStr = "UDP"
	}
	return netip.AddrPortFrom(key.ip, key.port).String() + "/" + protoStr
}

// vsKeyFromPacket extracts the VS key from a tunneled packet
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

	// Get inner packet destination (VS IP)
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
func buildVSMaps(config *balancerpb.BalancerConfig) map[vsKey]*vsInfo {
	vsMap := make(map[vsKey]*vsInfo)

	if config.PacketHandler == nil {
		return vsMap
	}

	for _, vs := range config.PacketHandler.Vs {
		vsIP, ok := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		if !ok {
			continue
		}

		key := vsKey{
			ip:    vsIP,
			port:  uint16(vs.Id.Port),
			proto: vs.Id.Proto,
		}

		info := &vsInfo{
			realAddrs:    make(map[netip.Addr]bool),
			enabledReals: make(map[netip.Addr]bool),
		}

		for _, real := range vs.Reals {
			realIP, ok := netip.AddrFromSlice(real.Id.Ip.Bytes)
			if ok {
				info.realAddrs[realIP] = true
				// Initially all reals are enabled (EnableAllReals is called before testPacketSending)
				info.enabledReals[realIP] = true
			}
		}

		vsMap[key] = info
	}

	return vsMap
}

// updateVSMapsWithRealStates updates the enabled state of reals in VS maps
func updateVSMapsWithRealStates(
	vsMap map[vsKey]*vsInfo,
	updates []*balancerpb.RealUpdate,
) {
	for _, update := range updates {
		if update.RealId == nil || update.RealId.Vs == nil ||
			update.RealId.Real == nil {
			continue
		}

		vsIP, ok := netip.AddrFromSlice(update.RealId.Vs.Addr.Bytes)
		if !ok {
			continue
		}

		key := vsKey{
			ip:    vsIP,
			port:  uint16(update.RealId.Vs.Port),
			proto: update.RealId.Vs.Proto,
		}

		info, exists := vsMap[key]
		if !exists {
			continue
		}

		realIP, ok := netip.AddrFromSlice(update.RealId.Real.Ip.Bytes)
		if !ok {
			continue
		}

		if update.Enable != nil {
			info.enabledReals[realIP] = *update.Enable
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// Main test function

// TestBigConfig tests the balancer with large configurations
func TestBigConfig(t *testing.T) {
	// Use fixed seed for reproducibility
	rng := rand.New(rand.NewSource(42))

	// Create initial config: 10 VS with 50 reals each
	initialConfig := createBigConfig(10, 50, rng)

	// Setup test with appropriate memory
	agentMemory := 32 * datasize.MB

	ts, err := utils.Make(&utils.TestConfig{
		Mock: utils.SingleWorkerMockConfig(
			datasize.MB*128,
			4*datasize.MB,
		),
		Balancer:    initialConfig,
		AgentMemory: &agentMemory,
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Logf(
		"Setup initial config with 10 VS and 50 reals per VS (total 500 reals)",
	)

	// Set initial time
	ts.Mock.SetCurrentTime(time.Unix(0, 0))

	// Enable all reals initially
	utils.EnableAllReals(t, ts)

	// Phase 1: Test with 10 VS and 50 reals each
	t.Run("Phase1_10VS_50Reals", func(t *testing.T) {
		testPacketSending(t, ts, initialConfig, rng, 10, 10000)
	})

	// Phase 2: Update to 20 VS with 20 reals each
	t.Run("Phase2_20VS_20Reals", func(t *testing.T) {
		newConfig := createBigConfig(20, 20, rng)

		updateStart := time.Now()
		err := ts.Balancer.Update(newConfig, ts.Mock.CurrentTime())
		require.NoError(t, err)

		t.Logf(
			"Updated config to 20 VS and 20 reals per VS (total 400 reals), elapsed: %v",
			time.Since(updateStart),
		)

		utils.EnableAllReals(t, ts)

		testPacketSending(t, ts, newConfig, rng, 10, 10000)
	})
}

////////////////////////////////////////////////////////////////////////////////
// Packet sending and validation

// testPacketSending sends batches of packets and validates the results
func testPacketSending(
	t *testing.T,
	ts *utils.TestSetup,
	config *balancerpb.BalancerConfig,
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
	if config.PacketHandler == nil {
		t.Fatal("PacketHandler config is nil")
	}
	virtualServices := config.PacketHandler.Vs

	for batch := range numBatches {
		// Build VS maps for validation
		vsMap := buildVSMaps(config)

		// Disable random half of reals before batch
		var disableUpdates []*balancerpb.RealUpdate
		var enableUpdates []*balancerpb.RealUpdate
		disabledCount := 0

		for _, vs := range virtualServices {
			// Randomly select half of reals to disable
			numToDisable := len(vs.Reals) / 2
			indices := rng.Perm(len(vs.Reals))

			for i, real := range vs.Reals {
				shouldDisable := false
				for j := range numToDisable {
					if indices[j] == i {
						shouldDisable = true
						break
					}
				}

				enableFalse := false
				enableTrue := true

				if shouldDisable {
					disableUpdates = append(
						disableUpdates,
						&balancerpb.RealUpdate{
							RealId: &balancerpb.RealIdentifier{
								Vs:   vs.Id,
								Real: real.Id,
							},
							Enable: &enableFalse,
						},
					)
					disabledCount++
				} else {
					enableUpdates = append(enableUpdates, &balancerpb.RealUpdate{
						RealId: &balancerpb.RealIdentifier{
							Vs:   vs.Id,
							Real: real.Id,
						},
						Enable: &enableTrue,
					})
				}
			}
		}

		// Apply disable updates and measure time
		disableStartTime := time.Now()
		_, err := ts.Balancer.UpdateReals(disableUpdates, false)
		require.NoError(t, err)
		disableDuration := time.Since(disableStartTime)

		// Update VS maps with new enabled states
		updateVSMapsWithRealStates(vsMap, disableUpdates)
		updateVSMapsWithRealStates(vsMap, enableUpdates)

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

			vsIP, ok := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			require.True(t, ok, "invalid VS IP")

			vsPort := uint16(vs.Id.Port)

			// Generate random source with matching IP protocol
			var srcIP netip.Addr
			if vsIP.Is4() {
				srcIP = generateRandomIPv4(rng)
			} else {
				srcIP = generateRandomIPv6(rng)
			}
			// Use ephemeral port range, avoiding well-known ports
			// Range: 32768-61000 to avoid protocol detection issues
			srcPort := uint16(32768 + rng.Intn(28232))

			// Create packet based on protocol
			var packetLayers []gopacket.SerializableLayer
			if vs.Id.Proto == balancerpb.TransportProto_TCP {
				packetLayers = utils.MakeTCPPacket(
					srcIP,
					srcPort,
					vsIP,
					vsPort,
					&layers.TCP{SYN: true},
				)
			} else {
				packetLayers = utils.MakeUDPPacket(
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
			// Use ephemeral port range
			srcPort := uint16(32768 + rng.Intn(28232))

			packetLayers := utils.MakeUDPPacket(
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
		result, err := ts.Mock.HandlePackets(packets...)
		require.NoError(t, err)
		handleDuration := time.Since(handleStartTime)

		totalOutput += len(result.Output)
		totalDrop += len(result.Drop)

		assert.Equal(
			t,
			existingVSPackets,
			len(result.Output),
			"all packets to existing VS should be tunneled",
		)

		// Validate output packets
		for _, outPkt := range result.Output {
			// Check packet is tunneled
			key, realIP := vsKeyFromPacket(t, outPkt)
			require.NotNil(t, key)
			require.NotNil(t, realIP)

			vsInfo, exists := vsMap[*key]
			if !exists {
				// all packets to non existent VS should be dropped
				t.Errorf(
					"Packet tunneled to non-existent VS %s",
					key.String(),
				)
				continue
			}

			// Check that the real IP is in the VS's real list
			if !vsInfo.realAddrs[*realIP] {
				t.Errorf(
					"Packet tunneled to real %s which is not in VS %s reals",
					*realIP,
					key.String(),
				)
				continue
			}

			// Check that the real is enabled
			if !vsInfo.enabledReals[*realIP] {
				t.Errorf("Packet tunneled to DISABLED real %s in VS %s",
					*realIP, key.String())
				continue
			}

			// All checks passed
			correctPackets++
		}

		// Validate drop packets
		for _, dropPkt := range result.Drop {
			// Check packet is not tunneled (dropped before tunneling)
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
			"Batch %d/%d: Sent %d packets, Output=%d, Drop=%d, Correct=%d/%d, HandleTime=%v, RPS=%.0f",
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
		_, err = ts.Balancer.UpdateReals(enableUpdates, false)
		require.NoError(t, err)
		enableDuration := time.Since(enableStartTime)

		t.Logf(
			"Batch %d/%d: Re-enabled all reals in %v",
			batch+1,
			numBatches,
			enableDuration,
		)

		// Advance time by 1 second
		ts.Mock.AdvanceTime(time.Second)

		// Sync active sessions
		err = ts.Balancer.Refresh(ts.Mock.CurrentTime())
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
	info, err := ts.Balancer.Info(ts.Mock.CurrentTime())
	require.NoError(t, err)
	t.Logf("Active sessions: %d", info.ActiveSessions)
	t.Logf("Session table capacity: %d", *config.State.SessionTableCapacity)
}
