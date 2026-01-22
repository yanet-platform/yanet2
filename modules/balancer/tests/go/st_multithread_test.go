package balancer_test

import (
	"fmt"
	"maps"
	"math"
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"google.golang.org/protobuf/types/known/durationpb"
)

////////////////////////////////////////////////////////////////////////////////

type fullSessionKey struct {
	clientIP   netip.Addr
	clientPort uint16
	vsIP       netip.Addr
	vsPort     uint16
	proto      balancerpb.TransportProto
}

func (session *fullSessionKey) String() string {
	return fmt.Sprintf(
		"%v:%v->%v:%v/%v",
		session.clientIP, session.clientPort,
		session.vsIP, session.vsPort, session.proto,
	)
}

func (session *fullSessionKey) Vs() vsKey {
	return vsKey{ip: session.vsIP, port: session.vsPort, proto: session.proto}
}

func fullSessionKeyFromTunPacket(
	packet *framework.PacketInfo,
) (*fullSessionKey, error) {
	if !packet.IsTunneled {
		return nil, fmt.Errorf("packet not tunneled")
	}
	if packet.InnerPacket == nil {
		return nil, fmt.Errorf("no inner packet")
	}
	proto, err := func() (balancerpb.TransportProto, error) {
		proto, ok := packet.GetTransportProtocol()
		if !ok {
			return 0, fmt.Errorf("no transport protocol in inner packet")
		}
		switch proto {
		case layers.IPProtocolTCP:
			return balancerpb.TransportProto_TCP, nil
		case layers.IPProtocolUDP:
			return balancerpb.TransportProto_UDP, nil
		default:
			return 0, fmt.Errorf(
				"incorrect inner packet protocol: %v, but protocol should be Tcp or Udp",
				proto,
			)
		}
	}()
	if err != nil {
		return nil, err
	}
	srcIP, ok := netip.AddrFromSlice(packet.InnerPacket.SrcIP)
	if !ok {
		return nil, fmt.Errorf(
			"invalid inner packet src IP: %v",
			packet.InnerPacket.SrcIP,
		)
	}
	dstIP, ok := netip.AddrFromSlice(packet.InnerPacket.DstIP)
	if !ok {
		return nil, fmt.Errorf(
			"invalid inner packet dst IP: %v",
			packet.InnerPacket.DstIP,
		)
	}
	key := fullSessionKey{
		clientIP:   srcIP,
		clientPort: packet.SrcPort,
		vsIP:       dstIP,
		vsPort:     packet.DstPort,
		proto:      proto,
	}
	return &key, nil
}

func fullSessionKeyFromInputPacket(
	packet *framework.PacketInfo,
) (*fullSessionKey, error) {
	proto, err := func() (balancerpb.TransportProto, error) {
		proto, ok := packet.GetTransportProtocol()
		if !ok {
			return 0, fmt.Errorf("no transport protocol in inner packet")
		}
		switch proto {
		case layers.IPProtocolTCP:
			return balancerpb.TransportProto_TCP, nil
		case layers.IPProtocolUDP:
			return balancerpb.TransportProto_UDP, nil
		default:
			return 0, fmt.Errorf(
				"incorrect inner packet protocol: %v, but protocol should be Tcp or Udp",
				proto,
			)
		}
	}()
	if err != nil {
		return nil, err
	}
	srcIP, ok := netip.AddrFromSlice(packet.SrcIP)
	if !ok {
		return nil, fmt.Errorf(
			"invalid src IP: %v",
			packet.SrcIP,
		)
	}
	dstIP, ok := netip.AddrFromSlice(packet.DstIP)
	if !ok {
		return nil, fmt.Errorf(
			"invalid dst IP: %v",
			packet.DstIP,
		)
	}
	key := fullSessionKey{
		clientIP:   srcIP,
		clientPort: packet.SrcPort,
		vsIP:       dstIP,
		vsPort:     packet.DstPort,
		proto:      proto,
	}
	return &key, nil
}

// workerState holds per-worker state
type workerState struct {
	id           int
	rng          *rand.Rand
	sessions     []fullSessionKey
	sessionReals map[fullSessionKey]netip.Addr // mapping from session to selected real
	stats        workerStats
}

func aggregateWorkerStates(states []workerState) workerState {
	aggregate := workerState{
		id:           -1,
		rng:          nil,
		sessions:     []fullSessionKey{},
		sessionReals: map[fullSessionKey]netip.Addr{},
		stats:        workerStats{},
	}

	// Aggregate all sessions and session-to-real mappings
	for _, state := range states {
		aggregate.sessions = append(aggregate.sessions, state.sessions...)
		maps.Copy(aggregate.sessionReals, state.sessionReals)

		// Aggregate statistics
		aggregate.stats.totalPackets += state.stats.totalPackets
		aggregate.stats.outputPackets += state.stats.outputPackets
		aggregate.stats.droppedPackets += state.stats.droppedPackets
		aggregate.stats.sessions += state.stats.sessions
	}

	return aggregate
}

// workerStats tracks statistics for a worker
type workerStats struct {
	totalPackets   int
	outputPackets  int
	droppedPackets int
	sessions       int
}

// multithreadTestConfig holds test configuration
type multithreadTestConfig struct {
	numWorkers               int
	batchesPerWorker         int
	packetsPerBatch          int
	extendSessionTablePeriod time.Duration
}

// vsSimple holds simplified VS info for packet generation
type vsSimple struct {
	ip    netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

// vsConfigWithWeights holds VS configuration with real weights
type vsConfigWithWeights struct {
	ip        netip.Addr
	port      uint16
	proto     balancerpb.TransportProto
	scheduler balancerpb.VsScheduler
	gre       bool
	fixMss    bool
	reals     []realConfigWithWeight
}

// realConfigWithWeight holds real configuration with weight
type realConfigWithWeight struct {
	ip     netip.Addr
	weight uint32
}

////////////////////////////////////////////////////////////////////////////////

// randomClientIP generates a random client IP based on VS IP version
func randomClientIP(rng *rand.Rand, vsIP netip.Addr) netip.Addr {
	if vsIP.Is4() {
		return netip.AddrFrom4([4]byte{
			byte(10),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
	}
	// IPv6
	return netip.MustParseAddr(
		fmt.Sprintf("2001:db8::%x:%x", rng.Intn(65536), rng.Intn(65536)),
	)
}

// randomPort generates a random port
func randomPort(rng *rand.Rand) uint16 {
	return uint16(32768 + rng.Intn(64511))
}

////////////////////////////////////////////////////////////////////////////////

// generateVSConfigs creates 5 virtual services with random real weights
func generateVSConfigs() []vsConfigWithWeights {
	rng := rand.New(rand.NewSource(42))

	configs := []vsConfigWithWeights{
		// VS1: TCP IPv4, RR scheduler, 10 IPv4 reals
		{
			ip:        netip.MustParseAddr("10.1.1.1"),
			port:      80,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS2: UDP IPv4, RR scheduler, 10 IPv4 reals
		{
			ip:        netip.MustParseAddr("10.1.2.1"),
			port:      5353,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS3: TCP IPv6, RR scheduler, 10 IPv6 reals
		{
			ip:        netip.MustParseAddr("2001:db8::1"),
			port:      443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       true,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS4: UDP IPv6, RR scheduler, 10 IPv6 reals
		{
			ip:        netip.MustParseAddr("2001:db8::2"),
			port:      8080,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS5: TCP IPv4, RR scheduler, 10 mixed IPv4/IPv6 reals
		{
			ip:        netip.MustParseAddr("10.1.3.1"),
			port:      8443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			gre:       true,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
	}

	// Generate real IPs and random weights
	for i := range configs {
		for j := range configs[i].reals {
			var realIP netip.Addr
			switch i {
			case 0:
				realIP = netip.MustParseAddr(fmt.Sprintf("10.2.1.%d", j+1))
			case 1:
				realIP = netip.MustParseAddr(fmt.Sprintf("10.2.2.%d", j+1))
			case 2:
				realIP = netip.MustParseAddr(fmt.Sprintf("2001:db8:2::%x", j+1))
			case 3:
				realIP = netip.MustParseAddr(fmt.Sprintf("2001:db8:3::%x", j+1))
			case 4:
				// Mixed IPv4/IPv6
				if j < 5 {
					realIP = netip.MustParseAddr(fmt.Sprintf("10.2.4.%d", j+1))
				} else {
					realIP = netip.MustParseAddr(fmt.Sprintf("2001:db8:4::%x", j-4))
				}
			}

			configs[i].reals[j] = realConfigWithWeight{
				ip:     realIP,
				weight: uint32(rng.Intn(10) + 1), // Random weight 1-10
			}
		}
	}

	return configs
}

// buildModuleConfig creates balancer module config from VS configs
func buildModuleConfig(
	vsConfigs []vsConfigWithWeights,
	sessionTimeout int,
	capacity uint64,
	maxLoadFactor float32,
) *balancerpb.BalancerConfig {
	virtualServices := make([]*balancerpb.VirtualService, 0, len(vsConfigs))

	for _, vsConf := range vsConfigs {
		// Build allowed sources based on VS IP version
		var allowedSrcs []*balancerpb.Net
		if vsConf.ip.Is4() {
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.0").AsSlice(),
					},
					Size: 8,
				},
			}
		} else {
			allowedSrcs = []*balancerpb.Net{
				{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("2001:db8::").AsSlice(),
					},
					Size: 32,
				},
			}
		}

		// Build reals
		reals := make([]*balancerpb.Real, 0, len(vsConf.reals))
		for _, realConf := range vsConf.reals {
			var srcMask []byte
			if realConf.ip.Is4() {
				srcMask = netip.MustParseAddr("255.255.255.255").AsSlice()
			} else {
				srcMask = netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").AsSlice()
			}

			reals = append(reals, &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip: &balancerpb.Addr{
						Bytes: realConf.ip.AsSlice(),
					},
					Port: 0,
				},
				Weight: realConf.weight,
				SrcAddr: &balancerpb.Addr{
					Bytes: realConf.ip.AsSlice(),
				},
				SrcMask: &balancerpb.Addr{
					Bytes: srcMask,
				},
			})
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr: &balancerpb.Addr{
					Bytes: vsConf.ip.AsSlice(),
				},
				Port:  uint32(vsConf.port),
				Proto: vsConf.proto,
			},
			AllowedSrcs: allowedSrcs,
			Scheduler:   vsConf.scheduler,
			Flags: &balancerpb.VsFlags{
				Gre:    vsConf.gre,
				FixMss: vsConf.fixMss,
				Ops:    false,
				PureL3: false,
				Wlc:    false,
			},
			Reals: reals,
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
				TcpSynAck: uint32(sessionTimeout),
				TcpSyn:    uint32(sessionTimeout),
				TcpFin:    uint32(sessionTimeout),
				Tcp:       uint32(sessionTimeout),
				Udp:       uint32(sessionTimeout),
				Default:   uint32(sessionTimeout),
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &capacity,
			SessionTableMaxLoadFactor: &maxLoadFactor,
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

////////////////////////////////////////////////////////////////////////////////

// makeSimplePacketLayers creates packet layers with minimal payload to avoid protocol detection issues
func makeSimplePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	isTCP bool,
	tcpFlags *layers.TCP,
) []gopacket.SerializableLayer {
	// Ensure both addresses are the same IP version
	if srcIP.Is4() != dstIP.Is4() {
		panic(fmt.Sprintf("IP version mismatch: src=%v dst=%v", srcIP, dstIP))
	}

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if srcIP.Is4() {
		ethernetType = layers.EthernetTypeIPv4
		if isTCP {
			ip = &layers.IPv4{
				Version:  4,
				IHL:      5,
				TTL:      64,
				Protocol: layers.IPProtocolTCP,
				SrcIP:    src,
				DstIP:    dst,
			}
		} else {
			ip = &layers.IPv4{
				Version:  4,
				IHL:      5,
				TTL:      64,
				Protocol: layers.IPProtocolUDP,
				SrcIP:    src,
				DstIP:    dst,
			}
		}
	} else {
		if isTCP {
			ip = &layers.IPv6{
				Version:    6,
				NextHeader: layers.IPProtocolTCP,
				HopLimit:   64,
				SrcIP:      src,
				DstIP:      dst,
			}
		} else {
			ip = &layers.IPv6{
				Version:    6,
				NextHeader: layers.IPProtocolUDP,
				HopLimit:   64,
				SrcIP:      src,
				DstIP:      dst,
			}
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: ethernetType,
	}

	var result []gopacket.SerializableLayer
	result = append(result, eth, ip.(gopacket.SerializableLayer))

	if isTCP {
		tcp := tcpFlags
		tcp.SrcPort = layers.TCPPort(srcPort)
		tcp.DstPort = layers.TCPPort(dstPort)
		tcp.SetNetworkLayerForChecksum(ip)
		result = append(result, tcp)
	} else {
		udp := &layers.UDP{
			SrcPort: layers.UDPPort(srcPort),
			DstPort: layers.UDPPort(dstPort),
		}
		udp.SetNetworkLayerForChecksum(ip)
		result = append(result, udp)
	}

	// Use empty payload to avoid protocol detection issues
	result = append(result, gopacket.Payload([]byte{}))
	return result
}

// generateNewSessionPacket creates a packet for a new session
func generateNewSessionPacket(
	state *workerState,
	virtualServices []vsSimple,
) (gopacket.Packet, *fullSessionKey, error) {
	// Randomly select a virtual service
	vs := virtualServices[state.rng.Intn(len(virtualServices))]

	// Generate random client IP based on VS IP version
	clientIP := randomClientIP(state.rng, vs.ip)
	clientPort := randomPort(state.rng)

	session := &fullSessionKey{
		clientIP:   clientIP,
		clientPort: clientPort,
		vsIP:       vs.ip,
		vsPort:     vs.port,
		proto:      vs.proto,
	}

	var packetLayers []gopacket.SerializableLayer
	if vs.proto == balancerpb.TransportProto_TCP {
		packetLayers = makeSimplePacketLayers(
			clientIP, clientPort, vs.ip, vs.port,
			true, &layers.TCP{SYN: true},
		)
	} else {
		packetLayers = makeSimplePacketLayers(
			clientIP, clientPort, vs.ip, vs.port,
			false, nil,
		)
	}

	packet, err := xpacket.LayersToPacketChecked(packetLayers...)
	if err != nil {
		return nil, nil, err
	}
	return packet, session, nil
}

// generateExistingSessionPacket creates a packet for an existing session
func generateExistingSessionPacket(
	state *workerState,
) (gopacket.Packet, *fullSessionKey, error) {
	// Randomly select an existing session
	idx := state.rng.Intn(len(state.sessions))
	session := &state.sessions[idx]

	// Create packet for session
	var packetLayers []gopacket.SerializableLayer
	if session.proto == balancerpb.TransportProto_TCP {
		// TCP
		packetLayers = makeSimplePacketLayers(
			session.clientIP, session.clientPort,
			session.vsIP, session.vsPort,
			true, &layers.TCP{},
		)
	} else {
		// UDP
		packetLayers = makeSimplePacketLayers(
			session.clientIP, session.clientPort,
			session.vsIP, session.vsPort,
			false, nil,
		)
	}

	packet, err := xpacket.LayersToPacketChecked(packetLayers...)
	if err != nil {
		return nil, nil, err
	}
	return packet, session, nil
}

////////////////////////////////////////////////////////////////////////////////

// workerRoutine sends packets and validates sessions
func workerRoutine(
	workerID int,
	config *multithreadTestConfig,
	mock *mock.YanetMock,
	virtualServices []vsSimple,
	wg *sync.WaitGroup,
	errors chan error,
	resultState *workerState,
) {
	defer wg.Done()

	state := &workerState{
		id:           workerID,
		rng:          rand.New(rand.NewSource(int64(workerID + 1000))),
		sessions:     []fullSessionKey{},
		sessionReals: map[fullSessionKey]netip.Addr{},
		stats:        workerStats{},
	}

	for batch := range config.batchesPerWorker {
		outputActiveSessions := map[fullSessionKey]bool{}
		packets := make([]gopacket.Packet, 0, config.packetsPerBatch)

		sendError := func(format string, a ...any) {
			errors <- fmt.Errorf("worker %d: batch %d: %w", workerID, batch, fmt.Errorf(format, a...))
		}

		for range config.packetsPerBatch {
			if state.rng.Intn(10) < 5 || len(state.sessions) == 0 {
				// new session
				packet, _, err := generateNewSessionPacket(
					state,
					virtualServices,
				)
				if err != nil {
					sendError("failed to generate new session packet: %w", err)
					continue
				}
				packets = append(packets, packet)
			} else {
				packet, key, err := generateExistingSessionPacket(state)
				if err != nil {
					sendError("failed to generate existing session packet: %w", err)
					continue
				}
				packets = append(packets, packet)
				outputActiveSessions[*key] = false
			}
		}

		result, err := mock.HandlePacketsOnWorker(workerID, packets...)
		if err != nil {
			sendError("failed to handle packets: %w", err)
			continue
		}
		output, drop := result.Output, result.Drop
		for _, outPkt := range output {
			sessionKey, err := fullSessionKeyFromTunPacket(outPkt)
			if err != nil {
				sendError("failed to get session key for out packet: %w", err)
				continue
			}
			realIP, ok := netip.AddrFromSlice(outPkt.DstIP)
			if !ok {
				sendError(
					"failed to get real ip for out packet (dstIP=%v)",
					outPkt.DstIP,
				)
				continue
			}
			if expectedRealIP, ok := state.sessionReals[*sessionKey]; ok {
				if expectedRealIP != realIP {
					sendError(
						"real ip mismatch for session %v: expected=%v, got=%v",
						sessionKey,
						expectedRealIP,
						realIP,
					)
					continue
				}
				outputActiveSessions[*sessionKey] = true
			} else { // created new session
				state.sessionReals[*sessionKey] = realIP
				state.sessions = append(state.sessions, *sessionKey)
			}
		}
		for _, dropPkt := range drop {
			key, err := fullSessionKeyFromInputPacket(dropPkt)
			if err != nil {
				sendError(
					"failed to get session key from dropped packet: %w",
					err,
				)
				continue
			}
			if _, ok := outputActiveSessions[*key]; ok {
				expectedReal := state.sessionReals[*key]
				sendError("dropped active session %v [real %v]",
					key, expectedReal,
				)
			}
		}
		for sessionKey, touched := range outputActiveSessions {
			if !touched {
				sendError(
					"active session %v not in output [real %v]",
					sessionKey,
					state.sessionReals[sessionKey],
				)
			}
		}
		if config.packetsPerBatch != len(drop)+len(output) {
			sendError(
				"summary packet mismatch: expected=%d, got=%d",
				config.packetsPerBatch,
				len(drop)+len(output),
			)
		}
		state.stats.droppedPackets += len(drop)
		state.stats.outputPackets += len(output)
		state.stats.totalPackets += config.packetsPerBatch
	}

	state.stats.sessions = len(state.sessions)

	*resultState = *state
}

////////////////////////////////////////////////////////////////////////////////

// validateWeightDistribution checks packet distribution matches real weights
func validateWeightDistribution(
	t *testing.T,
	vsConfigs []vsConfigWithWeights,
	aggregate *workerState,
) {
	realSessionCount := make(map[netip.Addr]int)
	for _, session := range aggregate.sessions {
		realIP := aggregate.sessionReals[session]
		realSessionCount[realIP] += 1
	}

	for _, vsConfig := range vsConfigs {
		// Calculate total weight and total packets
		totalWeight := uint32(0)
		totalSessions := 0

		for idx := range vsConfig.reals {
			real := &vsConfig.reals[idx]
			totalSessions += realSessionCount[real.ip]
			totalWeight += real.weight
		}

		for idx := range vsConfig.reals {
			real := &vsConfig.reals[idx]
			sessions := realSessionCount[real.ip]
			expectedRatio := float64(real.weight) / float64(totalWeight)
			actualRatio := float64(sessions) / float64(totalSessions)

			deviation := math.Abs(actualRatio-expectedRatio) / expectedRatio

			assert.Less(
				t,
				deviation,
				0.3,
				"VS %s:%d/%s Real %s: sessions distribution deviates too much from weight: expected=%.2f, actual=%.2f, deviation=%.2f",
				vsConfig.ip,
				vsConfig.port,
				vsConfig.proto,
				real.ip,
				expectedRatio,
				actualRatio,
				deviation,
			)
		}
	}
}

// validateCounters checks stats counters match info counters
func validateCounters(
	t *testing.T,
	balancer *balancer.BalancerManager,
	mock *mock.YanetMock,
	aggregate *workerState,
) {
	// Count sessions per real from aggregate
	realSessionCount := make(map[netip.Addr]int)
	vsSessionCount := make(map[vsKey]int)
	for _, session := range aggregate.sessions {
		realIP := aggregate.sessionReals[session]
		realSessionCount[realIP] += 1
		vsSessionCount[session.Vs()] += 1
	}

	currentTime := mock.CurrentTime()

	// Get state info (from stats)
	stateInfo, err := balancer.Info(currentTime)
	require.NoError(t, err)

	// Get config stats (from Info)
	ref := &balancerpb.PacketHandlerRef{
		Device:   &utils.DeviceName,
		Pipeline: &utils.PipelineName,
		Function: &utils.FunctionName,
		Chain:    &utils.ChainName,
	}
	configStats, err := balancer.Stats(ref)
	require.NoError(t, err)

	// Validate VS counters match
	require.Equal(t, len(stateInfo.Vs), len(configStats.Vs),
		"VS count mismatch between state and config")

	summaryOverflowCnt := uint64(0)
	for i := range stateInfo.Vs {
		vsState := stateInfo.Vs[i]
		vsConfig := configStats.Vs[i]

		vsAddr, _ := netip.AddrFromSlice(vsState.Id.Addr.Bytes)
		vs := vsKey{
			ip:    vsAddr,
			port:  uint16(vsState.Id.Port),
			proto: vsState.Id.Proto,
		}

		expectedSessions := vsSessionCount[vs]

		assert.Equal(
			t,
			uint64(expectedSessions),
			vsState.ActiveSessions,
			"[VS %s]: active session count mismatch between state and workers",
			vs.String(),
		)

		summaryOverflowCnt += vsConfig.Stats.SessionTableOverflow

	}

	// Validate Real counters match
	for i := range stateInfo.Vs {
		vsState := stateInfo.Vs[i]
		vsConfig := configStats.Vs[i]

		require.Equal(t, len(vsState.Reals), len(vsConfig.Reals),
			"Real count mismatch between state and config for VS %d", i)

		for j := range vsState.Reals {
			realState := vsState.Reals[j]

			realIP, _ := netip.AddrFromSlice(realState.Id.Real.Ip.Bytes)
			expectedSessions := realSessionCount[realIP]

			assert.Equal(t, uint64(expectedSessions), realState.ActiveSessions,
				"[real %s]: active session count mismatch", realIP)
		}
	}

	// Validate invariants
	assert.Equal(
		t,
		configStats.Common.IncomingPackets,
		uint64(aggregate.stats.totalPackets),
	)
	assert.Equal(
		t,
		configStats.Common.OutgoingPackets,
		uint64(aggregate.stats.outputPackets),
	)
	assert.Equal(
		t,
		configStats.L4.IncomingPackets,
		uint64(aggregate.stats.totalPackets),
	)
	assert.Equal(
		t,
		configStats.L4.OutgoingPackets,
		uint64(aggregate.stats.outputPackets),
	)
	assert.Equal(
		t,
		configStats.L4.SelectRealFailed,
		uint64(aggregate.stats.droppedPackets),
	)
	assert.Equal(t, summaryOverflowCnt, uint64(aggregate.stats.droppedPackets))
}

// validateFinalSessions checks session count
func validateFinalSessions(
	t *testing.T,
	balancer *balancer.BalancerManager,
	currentTime time.Time,
	aggregate *workerState,
) {
	// Get sessions info from balancer
	sessionsInfo, err := balancer.Sessions(currentTime)
	require.NoError(t, err, "failed to get sessions info")

	// Log session counts
	t.Logf("Aggregate tracked sessions: %d", len(aggregate.sessions))
	t.Logf("Balancer active sessions: %d", len(sessionsInfo))

	// Verify session count matches
	assert.Equal(t, len(aggregate.sessions), len(sessionsInfo),
		"session count mismatch between aggregate and balancer")

	// Build a map of expected sessions from aggregate
	expectedSessions := make(map[fullSessionKey]netip.Addr)
	for _, session := range aggregate.sessions {
		expectedSessions[session] = aggregate.sessionReals[session]
	}

	// Verify each balancer session exists in our tracked sessions
	for i, session := range sessionsInfo {
		// Build session key from balancer session info
		clientAddr, _ := netip.AddrFromSlice(session.ClientAddr.Bytes)
		vsAddr, _ := netip.AddrFromSlice(session.RealId.Vs.Addr.Bytes)
		realIP, _ := netip.AddrFromSlice(session.RealId.Real.Ip.Bytes)

		sessionKey := fullSessionKey{
			clientIP:   clientAddr,
			clientPort: uint16(session.ClientPort),
			vsIP:       vsAddr,
			vsPort:     uint16(session.RealId.Vs.Port),
			proto:      session.RealId.Vs.Proto,
		}

		// Check if this session was tracked
		expectedReal, found := expectedSessions[sessionKey]
		if !found {
			t.Errorf(
				"Session %d: balancer has session %s that was not tracked by workers",
				i,
				sessionKey.String(),
			)
			continue
		}

		// Verify the real server matches
		if expectedReal != realIP {
			t.Errorf(
				"Session %d: real server mismatch for %s: expected=%v, got=%v",
				i,
				sessionKey.String(),
				expectedReal,
				realIP,
			)
		}

		// Remove from expected map (to detect sessions we tracked but balancer doesn't have)
		delete(expectedSessions, sessionKey)
	}

	// Check for sessions we tracked but balancer doesn't have
	if len(expectedSessions) > 0 {
		t.Errorf(
			"Workers tracked %d sessions that are not in balancer:",
			len(expectedSessions),
		)
		for sessionKey, realIP := range expectedSessions {
			t.Errorf("  - %s -> real %v", sessionKey.String(), realIP)
		}
	}

	t.Logf("Session validation completed successfully")
}

////////////////////////////////////////////////////////////////////////////////

// extendSessionTableRoutine periodically calls sync to allow session table resizing
func extendSessionTableRoutine(
	mock *mock.YanetMock,
	balancer *balancer.BalancerManager,
	done chan struct{},
	config *multithreadTestConfig,
	errors chan error,
) {
	ticker := time.NewTicker(config.extendSessionTablePeriod)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			err := balancer.Refresh(
				mock.CurrentTime(),
			)
			if err != nil {
				errors <- err
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

// runMultithreadedTest executes the multithreaded test
func runMultithreadedTest(t *testing.T, config *multithreadTestConfig) {
	// Generate VS configurations with random real weights
	vsConfigs := generateVSConfigs()

	sessionTimeout := 60

	// Calculate expected sessions to set initial capacity
	// Total packets = numWorkers * batchesPerWorker * packetsPerBatch
	// New session probability is 50%
	totalPackets := config.numWorkers * config.batchesPerWorker * config.packetsPerBatch
	expectedSessions := uint64(totalPackets / 2)
	initialCapacity := 3 * expectedSessions / 2
	maxLoadFactor := float32(0.5)

	moduleConfig := buildModuleConfig(
		vsConfigs,
		sessionTimeout,
		initialCapacity,
		maxLoadFactor,
	)

	// Setup test
	mockConfig := utils.SingleWorkerMockConfig(datasize.MB*512, datasize.MB*4)
	mockConfig.Workers = uint64(config.numWorkers)

	setup, err := utils.Make(&utils.TestConfig{
		Mock:     mockConfig,
		Balancer: moduleConfig,
		AgentMemory: func() *datasize.ByteSize {
			memory := 256 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer setup.Free()

	mock := setup.Mock
	balancer := setup.Balancer

	// Enable all reals
	utils.EnableAllReals(t, setup)

	// Set initial time
	mock.SetCurrentTime(time.Unix(0, 0))

	// Create simplified VS list for packet generation
	vsSimpleList := make([]vsSimple, 0, len(vsConfigs))
	for _, vsConf := range vsConfigs {
		vsSimpleList = append(vsSimpleList, vsSimple{
			ip:    vsConf.ip,
			port:  vsConf.port,
			proto: vsConf.proto,
		})
	}

	// Create channels and wait groups
	errors := make(chan error, config.numWorkers+1)

	var wg sync.WaitGroup

	// Launch worker goroutines
	wg.Add(config.numWorkers)
	wStates := make([]workerState, config.numWorkers)
	for i := 0; i < config.numWorkers; i++ {
		go workerRoutine(
			i, config, mock,
			vsSimpleList, &wg, errors, &wStates[i],
		)
	}

	done := make(chan struct{}, 1)

	// Start extend session table routine
	go func() {
		extendSessionTableRoutine(mock, balancer, done, config, errors)
	}()

	// Listen for errors
	wg.Wait()

	// Stop extend session table routine
	done <- struct{}{}

	close(errors)

	// List for errors
	for err := range errors {
		t.Error(err)
	}

	t.Log("all worker routines completed")

	// Perform final validations

	t.Run("Validate_Workers_Stats", func(t *testing.T) {
		for worker := range wStates {
			stats := wStates[worker].stats
			dropRate := float64(
				stats.droppedPackets,
			) / float64(
				stats.totalPackets,
			) * 100.0
			t.Logf(
				"worker %d: sessions=%d, totalPackets=%d, output=%d, dropped=%d, dropRate=%.2f%%",
				worker,
				stats.sessions,
				stats.totalPackets,
				stats.outputPackets,
				stats.droppedPackets,
				dropRate,
			)
			assert.Less(
				t,
				dropRate,
				20.0,
				"worker %d: too big drop rate",
				worker,
			)
		}
	})

	currentTime := mock.CurrentTime()

	// Aggregate worker states
	aggregate := aggregateWorkerStates(wStates)
	if len(aggregate.sessions) != len(aggregate.sessionReals) {
		panic("invariant violation, maybe bad seed")
	}

	t.Run("Validate_Counters", func(t *testing.T) {
		validateCounters(t, balancer, mock, &aggregate)
	})

	t.Run("Validate_Final_Sessions", func(t *testing.T) {
		validateFinalSessions(t, balancer, currentTime, &aggregate)
	})

	t.Run("Validate_Weight_Distribution", func(t *testing.T) {
		validateWeightDistribution(t, vsConfigs, &aggregate)
	})

	// Log final statistics
	capacity := balancer.Config().State.SessionTableCapacity
	t.Logf("Final session table capacity: %d", *capacity)
}

////////////////////////////////////////////////////////////////////////////////

// TestMultithreadedSessionTable tests session table with multiple workers
func TestMultithreadedSessionTable(t *testing.T) {
	testCases := []struct {
		name       string
		numWorkers int
	}{
		{"SingleWorker", 1},
		{"TwoWorkers", 2},
		{"FourWorkers", 4},
		{"EightWorkers", 8},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &multithreadTestConfig{
				numWorkers:               tc.numWorkers,
				batchesPerWorker:         100,
				packetsPerBatch:          1024 / tc.numWorkers,
				extendSessionTablePeriod: 50 * time.Millisecond,
			}

			runMultithreadedTest(t, config)
		})
	}
}
