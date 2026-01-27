package main

import (
	"fmt"
	"math/rand/v2"
	"net/netip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	dataplane "github.com/yanet-platform/yanet2/lib/utils/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
)

type session struct {
	clientIp   netip.Addr
	clientPort uint16
	vsIp       netip.Addr
	vsPort     uint16
	proto      balancerpb.TransportProto
}

type Generator struct {
	bench     *BenchConfig
	generated int
	rand      *rand.Rand
	balancer  *balancerpb.BalancerConfig
	sessions  []session // Per-worker session storage
	worker    int
}

func NewGenerator(
	bench *BenchConfig,
	balancer *balancerpb.BalancerConfig,
) *Generator {
	return &Generator{
		bench:     bench,
		balancer:  balancer,
		generated: 0,
		sessions:  []session{},
		rand:      rand.New(rand.NewPCG(3, 5)),
		worker:    -1,
	}
}

// getAllVirtualServices returns all virtual services from the balancer config
func (ctx *Generator) getAllVirtualServices() []*balancerpb.VirtualService {
	return ctx.balancer.PacketHandler.Vs
}

// selectRandomVS selects a random virtual service
func (ctx *Generator) selectRandomVS() *balancerpb.VirtualService {
	vsList := ctx.getAllVirtualServices()
	if len(vsList) == 0 {
		return nil
	}
	idx := ctx.rand.IntN(len(vsList))
	return vsList[idx]
}

// generateRandomIPInNetwork generates a random IP address within the given network
func (ctx *Generator) generateRandomIPInNetwork(
	netAddr netip.Addr,
	prefixLen uint32,
) netip.Addr {
	if netAddr.Is4() {
		// IPv4
		addrBytes := netAddr.As4()
		// Generate random bits for the host part
		hostBits := 32 - prefixLen
		if hostBits > 0 {
			// Generate random host part
			for i := range hostBits {
				byteIdx := 3 - (i / 8)
				bitIdx := i % 8
				if ctx.rand.IntN(2) == 1 {
					addrBytes[byteIdx] |= (1 << bitIdx)
				}
			}
		}
		return netip.AddrFrom4(addrBytes)
	} else {
		// IPv6
		addrBytes := netAddr.As16()
		// Generate random bits for the host part
		hostBits := 128 - prefixLen
		if hostBits > 0 {
			// Generate random host part
			for i := range hostBits {
				byteIdx := 15 - (i / 8)
				bitIdx := i % 8
				if ctx.rand.IntN(2) == 1 {
					addrBytes[byteIdx] |= (1 << bitIdx)
				}
			}
		}
		return netip.AddrFrom16(addrBytes)
	}
}

// generateClientIP generates a client IP address for the given virtual service
func (ctx *Generator) generateClientIP(
	vs *balancerpb.VirtualService,
) netip.Addr {
	vsAddr, ok := netip.AddrFromSlice(vs.Id.Addr.Bytes)
	if !ok {
		panic("invalid VS address")
	}

	// If VS has allowed sources, pick a random one and generate IP within that network
	if len(vs.AllowedSrcs) > 0 {
		idx := ctx.rand.IntN(len(vs.AllowedSrcs))
		allowedNet := vs.AllowedSrcs[idx]
		netAddr, ok := netip.AddrFromSlice(allowedNet.Addr.Bytes)
		if !ok {
			panic("invalid allowed source address")
		}
		return ctx.generateRandomIPInNetwork(netAddr, allowedNet.Size)
	}

	// Otherwise generate random IP in appropriate range
	if vsAddr.Is4() {
		// Generate IPv4: 10.x.x.x
		return netip.AddrFrom4([4]byte{
			10,
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
		})
	} else {
		// Generate IPv6: fd00::x:x:x:x
		return netip.AddrFrom16([16]byte{
			0xfd, 0x00, 0, 0, 0, 0, 0, 0,
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
			byte(ctx.rand.IntN(256)),
		})
	}
}

// generateClientPort generates a random ephemeral port
func (ctx *Generator) generateClientPort() uint16 {
	// Ephemeral port range: 32768-65535
	return uint16(ctx.rand.IntN(65535-32768+1) + 32768)
}

// createNewSession creates a new session for a random virtual service
func (ctx *Generator) createNewSession() session {
	vs := ctx.selectRandomVS()
	if vs == nil {
		panic("no virtual services configured")
	}

	vsIp, ok := netip.AddrFromSlice(vs.Id.Addr.Bytes)
	if !ok {
		panic("invalid VS address in session creation")
	}

	return session{
		clientIp:   ctx.generateClientIP(vs),
		clientPort: ctx.generateClientPort(),
		vsIp:       vsIp,
		vsPort:     uint16(vs.Id.Port),
		proto:      vs.Id.Proto,
	}
}

// createPacketForSession creates a packet for the given session
func (ctx *Generator) createPacketForSession(s session) dataplane.PacketData {
	var packetLayers []gopacket.SerializableLayer

	if s.proto == balancerpb.TransportProto_TCP {
		// Use utility function from utils/packet.go
		tcp := &layers.TCP{SYN: true}
		packetLayers = utils.MakeTCPPacket(
			s.clientIp,
			s.clientPort,
			s.vsIp,
			s.vsPort,
			tcp,
		)
	} else {
		// UDP packet
		packetLayers = utils.MakeUDPPacket(
			s.clientIp,
			s.clientPort,
			s.vsIp,
			s.vsPort,
		)
	}

	// Serialize packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	if err := gopacket.SerializeLayers(buf, opts, packetLayers...); err != nil {
		panic(fmt.Sprintf("failed to serialize packet: %v", err))
	}

	packet := gopacket.NewPacket(
		buf.Bytes(),
		layers.LayerTypeEthernet,
		gopacket.Default,
	)

	// Handle MSS for TCP packets
	if s.proto == balancerpb.TransportProto_TCP && ctx.bench.mss > 0 {
		modifiedPacket, err := utils.InsertOrUpdateMSS(
			packet,
			uint16(ctx.bench.mss),
		)
		if err != nil {
			panic(fmt.Sprintf("failed to insert MSS: %v", err))
		}
		packet = *modifiedPacket
	}

	return dataplane.PacketData{
		Data:       packet.Data(),
		TxDeviceId: 0,
		RxDeviceId: 0,
	}
}

// generateWorkerPackets generates packets for a worker based on the bench config
func (ctx *Generator) generateWorkerPackets(
	worker int,
	count int,
) []dataplane.PacketData {
	packets := make([]dataplane.PacketData, 0, ctx.bench.PacketsPerBatch)

	if worker != ctx.worker {
		// new worker
		ctx.sessions = []session{}
		ctx.worker = worker
	}

	for i := 0; i < count; i++ {
		var s session

		// Decide: new session or reuse?
		if ctx.rand.Float32() < ctx.bench.NewSessionProb ||
			len(ctx.sessions) == 0 {
			// Create new session
			s = ctx.createNewSession()
			ctx.sessions = append(ctx.sessions, s)
		} else {
			// Reuse random existing session
			idx := ctx.rand.IntN(len(ctx.sessions))
			s = ctx.sessions[idx]
		}

		// Generate packet for this session
		packetData := ctx.createPacketForSession(s)
		packets = append(packets, packetData)

		ctx.generated++
	}

	return packets
}
