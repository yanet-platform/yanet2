package balancer

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
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
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

func (session *fullSessionKey) Vs() vsKey {
	return vsKey{ip: session.vsIP, port: session.vsPort, proto: session.proto}
}

func (k fullSessionKey) String() string {
	return fmt.Sprintf("[%s:%d -> %s:%d/%v]",
		k.clientIP, k.clientPort, k.vsIP, k.vsPort, k.proto)
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

func fullSessionKeyFromInputPacket(packet *framework.PacketInfo) (*fullSessionKey, error) {
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
	if len(states) == 0 {
		return workerState{
			id:           -1,
			rng:          nil,
			sessions:     []fullSessionKey{},
			sessionReals: map[fullSessionKey]netip.Addr{},
			stats:        workerStats{},
		}
	}

	aggregate := workerState{
		id:           -1, // Aggregate has no specific worker ID
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
		aggregate.stats.sessions += state.stats.sessions
		aggregate.stats.outputPackets += state.stats.outputPackets
		aggregate.stats.droppedPackets += state.stats.droppedPackets
	}

	return aggregate
}

// workerStats tracks statistics for a worker
type workerStats struct {
	totalPackets   int
	sessions       int
	outputPackets  int
	droppedPackets int
}

// multithreadTestConfig holds test configuration
type multithreadTestConfig struct {
	numWorkers       int
	batchesPerWorker int
	packetsPerBatch  int
	syncPeriod       time.Duration // How often to call sync (e.g., 50ms)
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
		// VS1: TCP IPv4, WRR scheduler, 10 IPv4 reals
		{
			ip:        IpAddr("10.1.1.1"),
			port:      80,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS2: UDP IPv4, PRR scheduler, 10 IPv4 reals
		{
			ip:        IpAddr("10.1.2.1"),
			port:      5353,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_PRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS3: TCP IPv6, WRR scheduler, 10 IPv6 reals
		{
			ip:        IpAddr("2001:db8::1"),
			port:      443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
			gre:       true,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS4: UDP IPv6, PRR scheduler, 10 IPv6 reals
		{
			ip:        IpAddr("2001:db8::2"),
			port:      8080,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_PRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		// VS5: TCP IPv4, WRR scheduler, 10 mixed IPv4/IPv6 reals
		{
			ip:        IpAddr("10.1.3.1"),
			port:      8443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
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
				realIP = IpAddr(fmt.Sprintf("10.2.1.%d", j+1))
			case 1:
				realIP = IpAddr(fmt.Sprintf("10.2.2.%d", j+1))
			case 2:
				realIP = IpAddr(fmt.Sprintf("2001:db8:2::%x", j+1))
			case 3:
				realIP = IpAddr(fmt.Sprintf("2001:db8:3::%x", j+1))
			case 4:
				// Mixed IPv4/IPv6
				if j < 5 {
					realIP = IpAddr(fmt.Sprintf("10.2.4.%d", j+1))
				} else {
					realIP = IpAddr(fmt.Sprintf("2001:db8:4::%x", j-4))
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
) *balancerpb.ModuleConfig {
	virtualServices := make([]*balancerpb.VirtualService, 0, len(vsConfigs))

	for _, vsConf := range vsConfigs {
		// Build allowed sources based on VS IP version
		var allowedSrcs []*balancerpb.Subnet
		if vsConf.ip.Is4() {
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: IpAddr("10.0.0.0").AsSlice(),
					Size: 8,
				},
			}
		} else {
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: IpAddr("2001:db8::").AsSlice(),
					Size: 32,
				},
			}
		}

		// Build reals
		reals := make([]*balancerpb.Real, 0, len(vsConf.reals))
		for _, realConf := range vsConf.reals {
			var srcMask []byte
			if realConf.ip.Is4() {
				srcMask = IpAddr("255.255.255.255").AsSlice()
			} else {
				srcMask = IpAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").AsSlice()
			}

			reals = append(reals, &balancerpb.Real{
				DstAddr: realConf.ip.AsSlice(),
				Weight:  realConf.weight,
				SrcAddr: realConf.ip.AsSlice(),
				SrcMask: srcMask,
				Enabled: true,
			})
		}

		virtualServices = append(virtualServices, &balancerpb.VirtualService{
			Addr:        vsConf.ip.AsSlice(),
			Port:        uint32(vsConf.port),
			Proto:       vsConf.proto,
			AllowedSrcs: allowedSrcs,
			Scheduler:   vsConf.scheduler,
			Flags: &balancerpb.VsFlags{
				Gre:    vsConf.gre,
				FixMss: vsConf.fixMss,
				Ops:    false,
				PureL3: false,
			},
			Reals: reals,
		})
	}

	return &balancerpb.ModuleConfig{
		SourceAddressV4: IpAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: IpAddr("fe80::5").AsSlice(),
		VirtualServices: virtualServices,
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: uint32(sessionTimeout),
			TcpSyn:    uint32(sessionTimeout),
			TcpFin:    uint32(sessionTimeout),
			Tcp:       uint32(sessionTimeout),
			Udp:       uint32(sessionTimeout),
			Default:   uint32(sessionTimeout),
		},
		Wlc: &balancerpb.WlcConfig{
			WlcPower:      10,
			MaxRealWeight: 1000,
			UpdatePeriod:  durationpb.New(0),
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

// extendSessionTableRoutine periodically calls sync to allow session table resizing
func extendSessionTableRoutine(
	mock *mock.YanetMock,
	balancer *module.Balancer,
	done chan struct{},
	wg *sync.WaitGroup,
	config *multithreadTestConfig,
) {
	defer wg.Done()

	ticker := time.NewTicker(config.syncPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			err := balancer.SyncActiveSessionsAndWlcAndResizeTableOnDemand(
				mock.CurrentTime(),
			)
			if err != nil {
				return
			}
		}
	}
}

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
				fmt.Printf("%v", outPkt.RawData)
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
				sendError("failed to get session key from dropped packet: %w", err)
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
	balancer *module.Balancer,
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
	stateInfo := balancer.GetStateInfo(currentTime)

	// Get config stats (from Info)
	configStats := balancer.GetConfigStats(
		defaultDeviceName,
		defaultPipelineName,
		defaultFunctionName,
		defaultChainName,
	)

	// Validate VS counters match
	require.Equal(t, len(stateInfo.VsInfo), len(configStats.Vs),
		"VS count mismatch between state and config")

	summaryOverflowCnt := uint64(0)
	for i := range stateInfo.VsInfo {
		vsState := &stateInfo.VsInfo[i]
		vsConfig := &configStats.Vs[i]

		vs := vsKey{
			ip:    vsState.VsIdentifier.Ip,
			port:  vsState.VsIdentifier.Port,
			proto: balancerpb.TransportProto(vsState.VsIdentifier.Proto),
		}

		expectedSessions := vsSessionCount[vs]

		assert.Equal(t, uint(expectedSessions), vsState.ActiveSessions.Value,
			"[VS %s]: active session count mismatch between state and workers", vs.String())

		assert.Equal(t, vsState.Stats, vsConfig.Stats,
			"[VS %s]: stats mismatch between state and config", vs.String())

		summaryOverflowCnt += vsState.Stats.SessionTableOverflow

	}

	// Validate Real counters match
	require.Equal(t, len(stateInfo.RealInfo), len(configStats.Reals),
		"Real count mismatch between state and config")

	for i := range stateInfo.RealInfo {
		realState := &stateInfo.RealInfo[i]
		realConfig := &configStats.Reals[i]

		assert.Equal(t, realState.Stats, realConfig.Stats,
			"[real %s]: stats mismatch between state and config", realState.RealIdentifier.Ip)

		expectedSessions := realSessionCount[realState.RealIdentifier.Ip]

		assert.Equal(t, uint(expectedSessions), realState.ActiveSessions.Value,
			"[real %s]: active session count mismatch", realState.RealIdentifier.Ip)
	}

	// Validate module-level counters
	assert.Equal(t, stateInfo.Module, configStats.Module,
		"Module stats mismatch between state and config")

	// Validate invariants
	assert.Equal(t, stateInfo.Module.Common.IncomingPackets, uint64(aggregate.stats.totalPackets))
	assert.Equal(t, stateInfo.Module.Common.OutgoingPackets, uint64(aggregate.stats.outputPackets))
	assert.Equal(t, stateInfo.Module.L4.IncomingPackets, uint64(aggregate.stats.totalPackets))
	assert.Equal(t, stateInfo.Module.L4.OutgoingPackets, uint64(aggregate.stats.outputPackets))
	assert.Equal(t, stateInfo.Module.L4.SelectRealFailed, uint64(aggregate.stats.droppedPackets))
	assert.Equal(t, summaryOverflowCnt, uint64(aggregate.stats.droppedPackets))
}

// validateFinalSessions checks session count
func validateFinalSessions(
	t *testing.T,
	balancer *module.Balancer,
	currentTime time.Time,
	aggregate *workerState,
) {
	// Get sessions info from balancer
	sessionsInfo, err := balancer.GetSessionsInfo(currentTime)
	require.NoError(t, err, "failed to get sessions info")

	// Log session counts
	t.Logf("Aggregate tracked sessions: %d", len(aggregate.sessions))
	t.Logf("Balancer active sessions: %d", sessionsInfo.SessionsCount)

	// Verify session count matches
	assert.Equal(t, len(aggregate.sessions), int(sessionsInfo.SessionsCount),
		"session count mismatch between aggregate and balancer")

	// Build a map of expected sessions from aggregate
	expectedSessions := make(map[fullSessionKey]netip.Addr)
	for _, session := range aggregate.sessions {
		expectedSessions[session] = aggregate.sessionReals[session]
	}

	// Verify each balancer session exists in our tracked sessions
	for i, session := range sessionsInfo.Sessions {
		// Build session key from balancer session info
		sessionKey := fullSessionKey{
			clientIP:   session.ClientAddr,
			clientPort: session.ClientPort,
			vsIP:       session.Real.Vs.Ip,
			vsPort:     session.Real.Vs.Port,
			proto:      balancerpb.TransportProto(session.Real.Vs.Proto),
		}

		// Check if this session was tracked
		expectedReal, found := expectedSessions[sessionKey]
		if !found {
			t.Errorf("Session %d: balancer has session %v that was not tracked by workers",
				i, sessionKey)
			continue
		}

		// Verify the real server matches
		if expectedReal != session.Real.Ip {
			t.Errorf("Session %d: real server mismatch for %v: expected=%v, got=%v",
				i, sessionKey, expectedReal, session.Real.Ip)
		}

		// Remove from expected map (to detect sessions we tracked but balancer doesn't have)
		delete(expectedSessions, sessionKey)
	}

	// Check for sessions we tracked but balancer doesn't have
	if len(expectedSessions) > 0 {
		t.Errorf("Workers tracked %d sessions that are not in balancer:", len(expectedSessions))
		for sessionKey, realIP := range expectedSessions {
			t.Errorf("  - %v -> real %v", sessionKey, realIP)
		}
	}

	t.Logf("Session validation completed successfully")
}

////////////////////////////////////////////////////////////////////////////////

// runMultithreadedTest executes the multithreaded test
func runMultithreadedTest(t *testing.T, config *multithreadTestConfig) {
	// Generate VS configurations with random real weights
	vsConfigs := generateVSConfigs()

	sessionTimeout := 60
	moduleConfig := buildModuleConfig(vsConfigs, sessionTimeout)

	stateConfig := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      4096, // 4K
		SessionTableScanPeriod:    durationpb.New(0),
		SessionTableMaxLoadFactor: 0.05,
	}

	// Setup test
	setup, err := SetupTest(&TestConfig{
		moduleConfig: moduleConfig,
		stateConfig:  stateConfig,
		mock: &mock.YanetMockConfig{
			AgentsMemory: datasize.MB * 64,
			Workers:      uint64(config.numWorkers),
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

	mock := setup.mock
	balancer := setup.balancer

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
	done := make(chan struct{})
	errors := make(chan error, config.numWorkers+1)

	var workersWg sync.WaitGroup
	var syncWg sync.WaitGroup

	// Launch sync goroutine
	syncWg.Add(1)
	go extendSessionTableRoutine(mock, balancer, done, &syncWg, config)

	// Launch worker goroutines
	workersWg.Add(config.numWorkers)
	wStates := make([]workerState, config.numWorkers)
	for i := 0; i < config.numWorkers; i++ {
		go workerRoutine(
			i, config, mock,
			vsSimpleList, &workersWg, errors, &wStates[i],
		)
	}

	// Listen for errors
	go func() {
		// Wait for all workers to complete
		workersWg.Wait()

		// Signal sync goroutine to stop
		close(done)

		// Wait for sync goroutine
		syncWg.Wait()

		close(errors)
	}()

	// List for errors
	for err := range errors {
		t.Error(err)
	}

	t.Log("all worker routines completed")

	// Perform final validations

	t.Run("Validate_Workers_Stats", func(t *testing.T) {
		for worker := range wStates {
			stats := wStates[worker].stats
			dropRate := float64(stats.droppedPackets) / float64(stats.totalPackets) * 100.0
			t.Logf(
				"worker %d: sessions=%d, totalPackets=%d, output=%d, dropped=%d, dropRate=%.2f%%",
				worker,
				stats.sessions,
				stats.totalPackets,
				stats.outputPackets,
				stats.droppedPackets,
				dropRate,
			)
			assert.Less(t, dropRate, 20.0, "worker %d: too big drop rate", worker)
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
	capacity := balancer.GetModuleConfigState().SessionTableCapacity()
	t.Logf("Final session table capacity: %d", capacity)
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
				numWorkers:       tc.numWorkers,
				batchesPerWorker: 100,
				packetsPerBatch:  1024 / tc.numWorkers,
				syncPeriod:       5 * time.Millisecond,
			}

			runMultithreadedTest(t, config)
		})
	}
}
