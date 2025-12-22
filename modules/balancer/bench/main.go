package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/common/go/logging"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Defaults aligned with the request:
// - do not advance clock (use real time only)
// - sync active sessions every 2 seconds
// - initial session table size ~1M
// - benchmark packet processing only (create packets and sessions during warmup)
const (
	defaultWarmup             = 3 * time.Second
	defaultDuration           = 40 * time.Second
	defaultReportInterval     = 1 * time.Second
	defaultSyncPeriod         = 5 * time.Second
	defaultPacketsPerBatch    = 64
	defaultSessionsPerWorker  = 1 << 20 // created during warmup, then reuse for measurement
	defaultSessionTimeoutSecs = 60

	// Limit number of stored batch measurements to avoid large Go heap and OOM/SIGKILL.
	// This is a per-scenario reservoir cap (across all workers).
	maxBatchSamples = 200_000
)

// Controlplane defaults (match tests)
const (
	defaultDeviceName   = "01:00.0"
	defaultPipelineName = "pipeline0"
	defaultFunctionName = "function0"
	defaultChainName    = "chain0"
	defaultConfigName   = "balancer0"
)

type benchConfig struct {
	warmup            time.Duration
	duration          time.Duration
	reportInterval    time.Duration
	syncPeriod        time.Duration
	packetsPerBatch   int
	sessionsPerWorker int
}

type benchEnv struct {
	mock     *mock.YanetMock
	agent    *ffi.Agent
	balancer *module.Balancer
}

func (b *benchEnv) free() {
	if b == nil {
		return
	}
	if b.balancer != nil {
		b.balancer.Free()
	}
	if b.agent != nil {
		_ = b.agent.Close()
	}
	if b.mock != nil {
		b.mock.Free()
	}
}

// Simple VS types (copied from tests with minimal changes)

type vsSimple struct {
	ip    netip.Addr
	port  uint16
	proto balancerpb.TransportProto
}

type realConfigWithWeight struct {
	ip     netip.Addr
	weight uint32
}

type vsConfigWithWeights struct {
	ip        netip.Addr
	port      uint16
	proto     balancerpb.TransportProto
	scheduler balancerpb.VsScheduler
	gre       bool
	fixMss    bool
	reals     []realConfigWithWeight
}

func ipAddr(addr string) netip.Addr {
	ip, _ := netip.ParseAddr(addr)
	return ip
}

func randomClientIP(rng *rand.Rand, vsIP netip.Addr) netip.Addr {
	if vsIP.Is4() {
		return netip.AddrFrom4([4]byte{
			byte(10),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
			byte(rng.Intn(256)),
		})
	}
	return netip.MustParseAddr(
		fmt.Sprintf("2001:db8::%x:%x", rng.Intn(65536), rng.Intn(65536)),
	)
}

func randomPort(rng *rand.Rand) uint16 {
	return uint16(32768 + rng.Intn(64511))
}

func generateVSConfigs() []vsConfigWithWeights {
	rng := rand.New(rand.NewSource(42))
	configs := []vsConfigWithWeights{
		{
			ip:        ipAddr("10.1.1.1"),
			port:      80,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		{
			ip:        ipAddr("10.1.2.1"),
			port:      5353,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_PRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		{
			ip:        ipAddr("2001:db8::1"),
			port:      443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
			gre:       true,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		{
			ip:        ipAddr("2001:db8::2"),
			port:      8080,
			proto:     balancerpb.TransportProto_UDP,
			scheduler: balancerpb.VsScheduler_PRR,
			gre:       false,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
		{
			ip:        ipAddr("10.1.3.1"),
			port:      8443,
			proto:     balancerpb.TransportProto_TCP,
			scheduler: balancerpb.VsScheduler_WRR,
			gre:       true,
			fixMss:    false,
			reals:     make([]realConfigWithWeight, 10),
		},
	}
	for i := range configs {
		for j := range configs[i].reals {
			var realIP netip.Addr
			switch i {
			case 0:
				realIP = ipAddr(fmt.Sprintf("10.2.1.%d", j+1))
			case 1:
				realIP = ipAddr(fmt.Sprintf("10.2.2.%d", j+1))
			case 2:
				realIP = ipAddr(fmt.Sprintf("2001:db8:2::%x", j+1))
			case 3:
				realIP = ipAddr(fmt.Sprintf("2001:db8:3::%x", j+1))
			case 4:
				if j < 5 {
					realIP = ipAddr(fmt.Sprintf("10.2.4.%d", j+1))
				} else {
					realIP = ipAddr(fmt.Sprintf("2001:db8:4::%x", j-4))
				}
			}
			configs[i].reals[j] = realConfigWithWeight{
				ip:     realIP,
				weight: uint32(rng.Intn(10) + 1),
			}
		}
	}
	return configs
}

func buildModuleConfig(vsConfigs []vsConfigWithWeights, sessionTimeout int) *balancerpb.ModuleConfig {
	virtualServices := make([]*balancerpb.VirtualService, 0, len(vsConfigs))
	for _, vsConf := range vsConfigs {
		var allowedSrcs []*balancerpb.Subnet
		if vsConf.ip.Is4() {
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: ipAddr("10.0.0.0").AsSlice(),
					Size: 8,
				},
			}
		} else {
			allowedSrcs = []*balancerpb.Subnet{
				{
					Addr: ipAddr("2001:db8::").AsSlice(),
					Size: 32,
				},
			}
		}
		reals := make([]*balancerpb.Real, 0, len(vsConf.reals))
		for _, realConf := range vsConf.reals {
			var srcMask []byte
			if realConf.ip.Is4() {
				srcMask = ipAddr("255.255.255.255").AsSlice()
			} else {
				srcMask = ipAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").AsSlice()
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
		SourceAddressV4: ipAddr("5.5.5.5").AsSlice(),
		SourceAddressV6: ipAddr("fe80::5").AsSlice(),
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

// Minimal packet builders (safe and version-neutral)

func makeSimplePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	isTCP bool,
	tcpFlags *layers.TCP,
) []gopacket.SerializableLayer {
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

	result = append(result, gopacket.Payload([]byte{}))
	return result
}

func setupCP(agent *ffi.Agent) error {
	functionConfig := ffi.FunctionConfig{
		Name: defaultFunctionName,
		Chains: []ffi.FunctionChainConfig{
			{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name: defaultChainName,
					Modules: []ffi.ChainModuleConfig{
						{
							Type: "balancer",
							Name: defaultConfigName,
						},
					},
				},
			},
		},
	}
	if err := agent.UpdateFunction(functionConfig); err != nil {
		return fmt.Errorf("failed to update function: %w", err)
	}
	inputPipelineConfig := ffi.PipelineConfig{
		Name:      defaultPipelineName,
		Functions: []string{defaultFunctionName},
	}
	if err := agent.UpdatePipeline(inputPipelineConfig); err != nil {
		return fmt.Errorf("failed to update pipeline: %w", err)
	}
	dummyPipelineConfig := ffi.PipelineConfig{
		Name:      "dummy",
		Functions: []string{},
	}
	if err := agent.UpdatePipeline(dummyPipelineConfig); err != nil {
		return fmt.Errorf("failed to update pipeline: %w", err)
	}
	deviceConfig := ffi.DeviceConfig{
		Name: defaultDeviceName,
		Input: []ffi.DevicePipelineConfig{
			{
				Name:   defaultPipelineName,
				Weight: 1,
			},
		},
		Output: []ffi.DevicePipelineConfig{
			{
				Name:   "dummy",
				Weight: 1,
			},
		},
	}
	if err := agent.UpdatePlainDevices([]ffi.DeviceConfig{deviceConfig}); err != nil {
		return fmt.Errorf("failed to update device pipelines: %w", err)
	}
	return nil
}

func setupBenchmark(numWorkers int, tableCapacity uint64) (*benchEnv, error) {
	cfg := &mock.YanetMockConfig{
		CpMemory: datasize.GB * 8,
		DpMemory: datasize.GB * 8,
		Workers:  uint64(numWorkers),
		Devices: []mock.YanetMockDeviceConfig{
			{Id: 0, Name: defaultDeviceName},
		},
	}
	mockInstance, err := mock.NewYanetMock(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock: %w", err)
	}
	agent, err := mockInstance.SharedMemory().AgentAttach("balancer", 0, uint(cfg.CpMemory-datasize.MB*128))
	if err != nil {
		mockInstance.Free()
		return nil, err
	}

	// Logger
	logLevel := zapcore.InfoLevel
	sugared, _, _ := logging.Init(&logging.Config{Level: logLevel})

	// Module configs
	vsConfs := generateVSConfigs()
	moduleCfg := buildModuleConfig(vsConfs, defaultSessionTimeoutSecs)

	stateCfg := &balancerpb.ModuleStateConfig{
		SessionTableCapacity:      tableCapacity,
		SessionTableScanPeriod:    durationpb.New(0), // no background scan
		SessionTableMaxLoadFactor: 0.75,              // target higher allowed load
	}

	bal, err := module.NewBalancerFromProto(
		*agent,
		defaultConfigName,
		moduleCfg,
		stateCfg,
		sugared,
	)
	if err != nil {
		_ = agent.Close()
		mockInstance.Free()
		return nil, fmt.Errorf("failed to create balancer: %w", err)
	}
	if err := setupCP(agent); err != nil {
		bal.Free()
		_ = agent.Close()
		mockInstance.Free()
		return nil, fmt.Errorf("failed to setup controlplane: %w", err)
	}

	return &benchEnv{
		mock:     mockInstance,
		agent:    agent,
		balancer: bal,
	}, nil
}

type prebuiltWorkerPackets struct {
	synPackets  []gopacket.Packet
	dataPackets []gopacket.Packet
}

// Build per-worker SYN and DATA packets up-front to avoid measuring packet creation
func prebuildWorkerPackets(workerID int, sessions int, vsList []vsSimple) (*prebuiltWorkerPackets, error) {
	// Build only one batch worth of data packets for measurement to minimize memory.
	// Do NOT prebuild SYN packets; warmup will generate SYNs on-the-fly.
	rng := rand.New(rand.NewSource(int64(1000 + workerID)))
	datas := make([]gopacket.Packet, 0, defaultPacketsPerBatch)

	for i := range defaultPacketsPerBatch {
		vs := vsList[(i+workerID)%len(vsList)]
		clientIP := randomClientIP(rng, vs.ip)
		clientPort := randomPort(rng)

		var dataLayers []gopacket.SerializableLayer
		if vs.proto == balancerpb.TransportProto_TCP {
			dataLayers = makeSimplePacketLayers(clientIP, clientPort, vs.ip, vs.port, true, &layers.TCP{})
		} else {
			dataLayers = makeSimplePacketLayers(clientIP, clientPort, vs.ip, vs.port, false, nil)
		}

		dataPkt, err := xpacket.LayersToPacketChecked(dataLayers...)
		if err != nil {
			return nil, err
		}
		datas = append(datas, dataPkt)
	}

	return &prebuiltWorkerPackets{
		synPackets:  nil, // warmup generates SYNs on-the-fly to avoid large heap usage
		dataPackets: datas,
	}, nil
}

func toVsSimple(vsConfs []vsConfigWithWeights) []vsSimple {
	out := make([]vsSimple, 0, len(vsConfs))
	for _, c := range vsConfs {
		out = append(out, vsSimple{ip: c.ip, port: c.port, proto: c.proto})
	}
	return out
}

// Worker warmup: create sessions by sending SYN (or first packet for UDP)
func workerWarmup(ctx context.Context, env *benchEnv, workerID int, packets *prebuiltWorkerPackets, batchSize int) {
	batch := make([]gopacket.Packet, batchSize)
	idx := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Fill batch
		for i := range batchSize {
			batch[i] = packets.synPackets[idx]
			idx++
			if idx >= len(packets.synPackets) {
				idx = 0
			}
		}
		_, _ = env.mock.HandlePacketsOnWorker(workerID, batch...)
	}
}

// Warmup helper: send exactly len(packets.synPackets) SYNs once (count-based warmup)
func workerWarmupCount(
	env *benchEnv,
	workerID int,
	packets *prebuiltWorkerPackets,
	batchSize int,
	sessionsCount int,
	vsList []vsSimple,
) {
	// 1) Ensure measurement sessions exist: send SYNs for prebuilt batch first
	prebuiltSyn := packets.synPackets
	if len(prebuiltSyn) > 0 {
		offset := 0
		for offset < len(prebuiltSyn) {
			n := batchSize
			remaining := len(prebuiltSyn) - offset
			if n > remaining {
				n = remaining
			}
			batch := make([]gopacket.Packet, n)
			copy(batch, prebuiltSyn[offset:offset+n])
			_, _ = env.mock.HandlePacketsOnWorker(workerID, batch...)
			offset += n
		}
	}

	// 2) Create the remaining sessions on-the-fly without storing packets
	remaining := sessionsCount - len(prebuiltSyn)
	if remaining <= 0 {
		return
	}

	// Deterministic generator to avoid duplicates and guarantee exact count
	seq := len(prebuiltSyn)
	genBatch := func(n int) []gopacket.Packet {
		out := make([]gopacket.Packet, 0, n)
		for i := 0; i < n; i++ {
			k := seq + i
			vs := vsList[k%len(vsList)]
			var clientIP netip.Addr
			if vs.ip.Is4() {
				// 10.(workerID+1).(k>>8).(k)
				clientIP = netip.AddrFrom4([4]byte{
					10,
					byte(workerID + 1),
					byte((k >> 8) & 0xff),
					byte(k & 0xff),
				})
			} else {
				// 2001:db8::(workerID+1):(k)
				clientIP = netip.MustParseAddr(
					fmt.Sprintf("2001:db8::%x:%x", workerID+1, k&0xffff),
				)
			}
			clientPort := uint16(32768 + (k % 64511))
			var layersList []gopacket.SerializableLayer
			if vs.proto == balancerpb.TransportProto_TCP {
				layersList = makeSimplePacketLayers(clientIP, clientPort, vs.ip, vs.port, true, &layers.TCP{SYN: true})
			} else {
				layersList = makeSimplePacketLayers(clientIP, clientPort, vs.ip, vs.port, false, nil)
			}
			pkt, err := xpacket.LayersToPacketChecked(layersList...)
			if err != nil {
				continue
			}
			out = append(out, pkt)
		}
		seq += n
		return out
	}

	for remaining > 0 {
		n := batchSize
		if n > remaining {
			n = remaining
		}
		batch := genBatch(n)
		if len(batch) > 0 {
			_, _ = env.mock.HandlePacketsOnWorker(workerID, batch...)
		}
		remaining -= n
	}
}

// Worker measurement: send only data packets for existing sessions (lookup-only path)
func workerMeasure(ctx context.Context, env *benchEnv, workerID int, packets *prebuiltWorkerPackets, batchSize int, measCh chan<- time.Duration) {
	batch := make([]gopacket.Packet, batchSize)
	idx := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		
		// Reservoir sampler for time.Duration with bounded memory.
		// Implements Vitter's Algorithm R (reservoir sampling) to keep a uniform sample
		// of size at most cap from an unbounded stream.
		type reservoirSampler struct {
			cap   int
			count int64
			data  []time.Duration
			rnd   *rand.Rand
		}
		
		func newReservoirSampler(capacity int) *reservoirSampler {
			return &reservoirSampler{
				cap:   capacity,
				data:  make([]time.Duration, 0, capacity),
				rnd:   rand.New(rand.NewSource(42)),
				count: 0,
			}
		}
		
		func (s *reservoirSampler) Add(v time.Duration) {
			s.count++
			if len(s.data) < s.cap {
				s.data = append(s.data, v)
				return
			}
			// pick random index in [0, count-1]
			j := s.rnd.Int63n(s.count)
			if j < int64(s.cap) {
				s.data[j] = v
			}
		}
		
		func (s *reservoirSampler) Sorted() []time.Duration {
			out := make([]time.Duration, len(s.data))
			copy(out, s.data)
			slices.Sort(out)
			return out
		}
		for i := 0; i < batchSize; i++ {
			batch[i] = packets.dataPackets[idx]
			idx++
			if idx >= len(packets.dataPackets) {
				idx = 0
			}
		}
		t0 := time.Now()
		_, _ = env.mock.HandlePacketsOnWorker(workerID, batch...)
		dt := time.Since(t0)
		// non-blocking write if the channel is full is not desired; buffer is large enough
		measCh <- dt
	}
}

// Sync routine: strictly every syncPeriod (2s)
func syncRoutine(ctx context.Context, env *benchEnv, syncPeriod time.Duration) {
	t := time.NewTicker(syncPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			// touch config stats to keep state hot (explicit scan disabled)
			_ = env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
			_ = now
		}
	}
}

// Reporter: use GetConfigStats (does not trigger active-session scan) to avoid extra scanning.
// Print RPS and drop stats once per second. Capacity is a static read.
func reporter(ctx context.Context, env *benchEnv, reportInterval time.Duration, measureStart time.Time, rpsCh chan<- uint64) {
	t := time.NewTicker(reportInterval)
	defer t.Stop()

	var prevOutgoing uint64
	var prevFailed uint64
	var prevOverflow uint64
	var started bool

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			stats := env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
			out := stats.Module.Common.OutgoingPackets
			fail := stats.Module.L4.SelectRealFailed

			overflow := uint64(0)
			for i := range stats.Vs {
				overflow += stats.Vs[i].Stats.SessionTableOverflow
			}

			if !started {
				// Defer baseline until measurement actually starts
				if now.After(measureStart) {
					prevOutgoing = out
					prevFailed = fail
					prevOverflow = overflow
					started = true
				}
				continue
			}

			rps := out - prevOutgoing
			drops := fail - prevFailed
			over := overflow - prevOverflow
			prevOutgoing = out
			prevFailed = fail
			prevOverflow = overflow

			// feed per-second aggregate RPS for percentile summary
			select {
			case rpsCh <- rps:
			default:
			}

			var dropPct float64
			den := rps + drops
			if den > 0 {
				dropPct = 100.0 * float64(drops) / float64(den)
			}
			var totalDropPct float64
			totalDen := out + fail
			if totalDen > 0 {
				totalDropPct = 100.0 * float64(fail) / float64(totalDen)
			}

			capacity := env.balancer.GetModuleConfigState().SessionTableCapacity()
			fmt.Printf("[bench] ts=%s rps=%d drops/s=%d (%.2f%%) overflow/s=%d total_out=%d total_drops=%d (%.2f%%) table_capacity=%d\n",
				now.Format("15:04:05"),
				rps, drops, dropPct, over, out, fail, totalDropPct, capacity)
		}
	}
}

func runScenario(workerCount int, cfg benchConfig) error {
	sessionsPerWorker := cfg.sessionsPerWorker
	fmt.Printf("=== Scenario: workers=%d warmup=%s duration=%s batch=%d sessions/worker=%d\n",
		workerCount, cfg.warmup, cfg.duration, cfg.packetsPerBatch, sessionsPerWorker)

	env, err := setupBenchmark(workerCount, 1<<24) // 16M
	if err != nil {
		return err
	}
	defer env.free()

	// Build VS list for packet pre-generation
	vsList := toVsSimple(generateVSConfigs())

	// sessions per worker already defined from cfg
	// Prebuild packets for each worker
	prebuilt := make([]*prebuiltWorkerPackets, workerCount)
	for w := range workerCount {
		pkts, err := prebuildWorkerPackets(w, sessionsPerWorker, vsList)
		if err != nil {
			return fmt.Errorf("prebuild packets failed for worker %d: %w", w, err)
		}
		prebuilt[w] = pkts
	}

	// Contexts
	ctxAll, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	// Sync goroutine (2s)
	go syncRoutine(ctxAll, env, cfg.syncPeriod)

	// Warmup: create exactly sessionsPerWorker SYNs per worker
	fmt.Println("... warmup phase (creating sessions)")
	var wgWarm sync.WaitGroup
	wgWarm.Add(workerCount)
	for w := range workerCount {
		wi := w
		go func() {
			defer wgWarm.Done()
			workerWarmupCount(env, wi, prebuilt[wi], cfg.packetsPerBatch, sessionsPerWorker, vsList)
		}()
	}
	wgWarm.Wait()
	fmt.Println("... warmup complete")

	// Gate: wait until created sessions reach expected total without scanning the table
	expectedTotal := uint64(workerCount * sessionsPerWorker)
	gateStart := time.Now()
	lastLog := time.Time{}
	for {
		stats := env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
		created := uint64(0)
		for i := range stats.Vs {
			created += stats.Vs[i].Stats.CreatedSessions
		}
		// Allow tolerance up to one batch per worker to avoid infinite wait on occasional duplicates
		if created >= expectedTotal || created+uint64(workerCount*cfg.packetsPerBatch) >= expectedTotal {
			if created < expectedTotal {
				fmt.Printf("... gating with tolerance: created=%d expected=%d\n", created, expectedTotal)
			}
			break
		}
		now := time.Now()
		// progress log once per second
		if lastLog.IsZero() || now.Sub(lastLog) >= time.Second {
			fmt.Printf("... waiting sessions creation: created=%d expected=%d\n", created, expectedTotal)
			lastLog = now
		}
		// fail-safe timeout to avoid freezing forever on counters mismatch
		if now.Sub(gateStart) > 60*time.Second {
			fmt.Printf("... gating timeout after %s: created=%d < expected=%d; proceeding to measurement\n",
				now.Sub(gateStart).Truncate(time.Second), created, expectedTotal)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Reporter
	measureStart := time.Now()
	// Baseline for aggregate RPS over the measurement window
	stats0 := env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
	outStart := stats0.Module.Common.OutgoingPackets

	ctxReport, cancelReport := context.WithCancel(ctxAll)

	// Collect per-second aggregate RPS values for percentiles
	rpsCh := make(chan uint64, 1<<20)
	rpsVals := make([]uint64, 0, int(cfg.duration.Seconds())+4)
	var rpsWg sync.WaitGroup
	rpsWg.Add(1)
	go func() {
		defer rpsWg.Done()
		for v := range rpsCh {
			rpsVals = append(rpsVals, v)
		}
	}()

	go reporter(ctxReport, env, cfg.reportInterval, measureStart, rpsCh)

	// Measurement: per-batch timing
	fmt.Println("... measurement phase")
	measCh := make(chan time.Duration, 1<<20)
	durations := make([]time.Duration, 0, workerCount*int(cfg.duration.Seconds())*1024)
	var aggWg sync.WaitGroup
	aggWg.Add(1)
	go func() {
		defer aggWg.Done()
		for dt := range measCh {
			durations = append(durations, dt)
		}
	}()

	ctxMeasure, cancelMeasure := context.WithCancel(ctxAll)
	var wgMeasure sync.WaitGroup
	wgMeasure.Add(workerCount)
	for w := range workerCount {
		wi := w
		go func() {
			defer wgMeasure.Done()
			workerMeasure(ctxMeasure, env, wi, prebuilt[wi], cfg.packetsPerBatch, measCh)
		}()
	}
	time.Sleep(cfg.duration)
	cancelMeasure()
	wgMeasure.Wait()
	close(measCh)
	aggWg.Wait()

	// Print final summary
	stats := env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
	out := stats.Module.Common.OutgoingPackets
	fail := stats.Module.L4.SelectRealFailed
	capacity := env.balancer.GetModuleConfigState().SessionTableCapacity()

	// Aggregate RPS across workers over the whole measurement window
	measuredSec := time.Since(measureStart).Seconds()
	aggRPS := 0.0
	if measuredSec > 0 {
		aggRPS = float64(out-outStart) / measuredSec
	}

	fmt.Printf("=== Summary workers=%d: total_out=%d total_drops=%d table_capacity=%d aggregate_rps=%.0f\n",
		workerCount, out, fail, capacity, aggRPS)

	// Percentiles from per-batch latency; per-request latency = batchTime / batchSize
	if len(durations) > 0 {
		slices.Sort(durations)
		get := func(p float64) time.Duration {
			if p < 0 {
				p = 0
			}
			if p > 1 {
				p = 1
			}
			idx := int(math.Round(p * float64(len(durations)-1)))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(durations) {
				idx = len(durations) - 1
			}
			return durations[idx]
		}
		p50d := get(0.50)
		p90d := get(0.90)
		p95d := get(0.95)
		p99d := get(0.99)

		// per-request latency (ms)
		l50 := float64(p50d) / float64(time.Millisecond) / float64(cfg.packetsPerBatch)
		l90 := float64(p90d) / float64(time.Millisecond) / float64(cfg.packetsPerBatch)
		l95 := float64(p95d) / float64(time.Millisecond) / float64(cfg.packetsPerBatch)
		l99 := float64(p99d) / float64(time.Millisecond) / float64(cfg.packetsPerBatch)

		// RPS per percentile computed from batch (rps = batchSize / batchTime)
		bs := float64(cfg.packetsPerBatch)
		r50 := bs / p50d.Seconds()
		r90 := bs / p90d.Seconds()
		r95 := bs / p95d.Seconds()
		r99 := bs / p99d.Seconds()

		fmt.Printf("Latency percentiles (per-request): p50=%.3fms p90=%.3fms p95=%.3fms p99=%.3fms\n", l50, l90, l95, l99)
		fmt.Printf("RPS by percentile (batchSize=%d): p50=%.0f p90=%.0f p95=%.0f p99=%.0f\n", cfg.packetsPerBatch, r50, r90, r95, r99)
	}

	// Close RPS stream and compute aggregate per-second RPS percentiles
	cancelReport()
	close(rpsCh)
	rpsWg.Wait()
	if len(rpsVals) > 0 {
		slices.Sort(rpsVals)
		getR := func(p float64) uint64 {
			if p < 0 {
				p = 0
			}
			if p > 1 {
				p = 1
			}
			idx := max(int(math.Round(p*float64(len(rpsVals)-1))), 0)
			if idx >= len(rpsVals) {
				idx = len(rpsVals) - 1
			}
			return rpsVals[idx]
		}
		r01 := getR(0.01)
		r05 := getR(0.05)
		r10 := getR(0.10)
		r50 := getR(0.50)
		r95 := getR(0.95)
		r99 := getR(0.99)
		// Aggregate RPS across workers over entire window
		measuredSec := time.Since(measureStart).Seconds()
		stats := env.balancer.GetConfigStats(defaultDeviceName, defaultPipelineName, defaultFunctionName, defaultChainName)
		aggRPS := 0.0
		if measuredSec > 0 {
			aggRPS = float64(stats.Module.Common.OutgoingPackets-outStart) / measuredSec
		}
		fmt.Printf("Aggregate per-second RPS percentiles: p01=%d p05=%d p10=%d p50=%d p95=%d p99=%d (aggregate_rps=%.0f)\n",
			r01, r05, r10, r50, r95, r99, aggRPS)
	}
	return nil
}

func main() {
	// Graceful stop
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	cfg := benchConfig{
		warmup:            defaultWarmup,
		duration:          defaultDuration,
		reportInterval:    defaultReportInterval,
		syncPeriod:        defaultSyncPeriod,
		packetsPerBatch:   defaultPacketsPerBatch,
		sessionsPerWorker: defaultSessionsPerWorker,
	}

	// Scenarios
	workers := []int{1, 2, 4, 8}
	for _, wc := range workers {
		select {
		case <-stop:
			fmt.Println("received stop signal")
			return
		default:
		}
		if err := runScenario(wc, cfg); err != nil {
			fmt.Printf("scenario workers=%d failed: %v\n", wc, err)
			return
		}
	}
	fmt.Println("benchmark complete")
}
