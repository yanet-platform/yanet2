// Package main implements a high-performance benchmark tool for the YANET balancer module.
// It generates synthetic traffic, measures packet processing throughput (MPpS), and provides
// detailed statistics on balancer performance including per-worker metrics and load distribution.
package main

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/logging"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	dataplane "github.com/yanet-platform/yanet2/lib/utils/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sys/unix"
)

var (
	PacketsMemory int = (1 << 32) + (1 << 30)
	TotalMemory   int = CpMemory + PacketsMemory
	CpMemory      int = (1 << 33)
	AgentMemory   int = CpMemory - (1 << 27)
)

var BalancerName string = "balancer0"

// generate packets and run handlers
type workerInfo struct {
	idx   int
	tid   int
	info  string
	isErr bool
}

// worker performance metrics
type workerPerf struct {
	idx      int
	packets  int
	duration time.Duration
	mpps     float64
}

func workerRoutine(
	bench *Bench,
	wg *sync.WaitGroup,
	readyWg *sync.WaitGroup,
	info chan workerInfo,
	perf chan workerPerf,
	start chan struct{},
	idx int,
	packetList []dataplane.PacketList,
	totalPackets int,
) {
	defer wg.Done()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid := unix.Gettid()

	sendMsg := func(msg string) {
		info <- workerInfo{idx: idx, tid: tid, info: msg, isErr: false}
	}

	sendError := func(msg string) {
		info <- workerInfo{idx: idx, tid: tid, info: msg, isErr: true}
	}

	// pin
	var set unix.CPUSet
	set.Zero()
	set.Set(idx)
	if err := unix.SchedSetaffinity(0, &set); err != nil {
		sendError(fmt.Sprintf("failed to set affinity: %s", err))
		readyWg.Done()
		return
	}

	// set priority
	if err := unix.Setpriority(unix.PRIO_PROCESS, tid, -20); err != nil {
		sendError(fmt.Sprintf("failed to set priority: %s", err))
		readyWg.Done()
		return
	}

	sendMsg(fmt.Sprintf("pinned to CPU %d with priority %d", idx, -20))
	readyWg.Done()

	<-start

	startTime := time.Now()

	if err := bench.HandlePackets(idx, packetList); err != nil {
		msg := fmt.Sprintf("failed to handle packets: %s", err)
		sendError(msg)
	} else {
		elapsed := time.Since(startTime)
		mpps := float64(totalPackets) / elapsed.Seconds() / 1e6
		sendMsg(fmt.Sprintf("successfully handled %d packets in %s (%.2f MPpS)", totalPackets, elapsed, mpps))
		// Send performance metrics
		perf <- workerPerf{
			idx:      idx,
			packets:  totalPackets,
			duration: elapsed,
			mpps:     mpps,
		}
	}
}

func enableAllReals(bal *balancer.BalancerManager) error {
	var updates []*balancerpb.RealUpdate
	enableTrue := true
	balancerConfig := bal.Config()

	for _, vs := range balancerConfig.PacketHandler.Vs {
		for _, real := range vs.Reals {
			updates = append(updates, &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs:   vs.Id,
					Real: real.Id,
				},
				Enable: &enableTrue,
			})
		}
	}

	// update reals
	if _, err := bal.UpdateReals(updates, false); err != nil {
		return fmt.Errorf("failed to enable reals: %s", err)
	}

	return nil
}

func balancerConfig(config *BenchConfig) *balancerpb.BalancerConfig {
	// Create virtual services based on config
	var virtualServices []*balancerpb.VirtualService

	rng := rand.New(rand.NewPCG(1, 2))

	// Helper function to create a VS with reals
	createVS := func(addr netip.Addr, port uint32, proto balancerpb.TransportProto) *balancerpb.VirtualService {
		// Determine flags based on probabilities
		flags := &balancerpb.VsFlags{
			Gre:    rng.Float32() < config.GreProb,
			FixMss: rng.Float32() < config.FixMSSProb,
			PureL3: rng.Float32() < config.PureL3Prob,
			Ops:    rng.Float32() < config.OpsProb,
			Wlc:    false,
		}

		// If PureL3 is enabled, port must be 0
		if flags.PureL3 {
			port = 0
		}

		// Create reals for this VS
		reals := make([]*balancerpb.Real, 0, config.Ipv4Reals+config.Ipv6Reals)
		for i := 0; i < config.Ipv4Reals+config.Ipv6Reals; i++ {
			var realAddr netip.Addr
			if i < config.Ipv4Reals {
				// Generate IPv4 real address (10.0.0.0/8 range)
				realAddr = netip.AddrFrom4(
					[4]byte{10, 0, byte(i / 256), byte(i % 256)},
				)
			} else {
				// Generate IPv6 real address (fd00::/8 range)
				realAddr = netip.AddrFrom16([16]byte{0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(i / 256), byte(i % 256)})
			}

			// Create source address and mask (preserve original source)
			var srcAddr, srcMask []byte
			if addr.Is4() {
				srcAddr = []byte{0, 0, 0, 0}
				srcMask = []byte{0, 0, 0, 0}
			} else {
				srcAddr = make([]byte, 16)
				srcMask = make([]byte, 16)
			}

			reals = append(reals, &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip:   &balancerpb.Addr{Bytes: realAddr.AsSlice()},
					Port: 0,
				},
				Weight:  1,
				SrcAddr: &balancerpb.Addr{Bytes: srcAddr},
				SrcMask: &balancerpb.Addr{Bytes: srcMask},
			})
		}

		scheduler := balancerpb.VsScheduler_SOURCE_HASH
		if rng.Float32() < config.RoundRobinProb {
			scheduler = balancerpb.VsScheduler_ROUND_ROBIN
		}

		allowedSrc := make([]*balancerpb.AllowedSources, 0, config.AllowedSrcPerVs)
		for i := 0; i < config.AllowedSrcPerVs; i++ {
			if addr.Is4() {
				allowedSrc = append(allowedSrc, &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{
							Bytes: []byte{byte(i / 256), byte(i % 256), 5, 5},
						},
						Mask: &balancerpb.Addr{
							Bytes: []byte{255, 255, 255, 255},
						},
					}},
				})
			} else {
				allowedSrc = append(allowedSrc, &balancerpb.AllowedSources{
					Nets: []*balancerpb.Net{{
						Addr: &balancerpb.Addr{
							Bytes: []byte{byte(i / 256), byte(i % 256), 0, 2, 3, 0, 0, 29, 0, 43, 0, 16, 0, 0, 0, 0},
						},
						Mask: &balancerpb.Addr{
							Bytes: []byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255},
						},
					}},
				})
			}
		}

		peers := make([]*balancerpb.Addr, 0, 2)
		for i := range 2 {
			peers = append(
				peers,
				&balancerpb.Addr{
					Bytes: []byte{byte(i / 256), byte(i % 256), 10, 11},
				},
			)
		}

		return &balancerpb.VirtualService{
			Id: &balancerpb.VsIdentifier{
				Addr:  &balancerpb.Addr{Bytes: addr.AsSlice()},
				Port:  port,
				Proto: proto,
			},
			Scheduler:   scheduler,
			AllowedSrcs: allowedSrc,
			Reals:       reals,
			Flags:       flags,
			Peers:       peers,
		}
	}

	// Generate TCP IPv4 virtual services
	for i := 0; i < config.TCPIPv4VS; i++ {
		addr := netip.AddrFrom4([4]byte{192, 168, byte(i / 256), byte(i % 256)})
		virtualServices = append(
			virtualServices,
			createVS(addr, 80, balancerpb.TransportProto_TCP),
		)
	}

	// Generate TCP IPv6 virtual services
	for i := 0; i < config.TCPIPv6VS; i++ {
		addr := netip.AddrFrom16(
			[16]byte{
				0x20,
				0x01,
				0x0d,
				0xb8,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				byte(i / 256),
				byte(i % 256),
			},
		)
		virtualServices = append(
			virtualServices,
			createVS(addr, 80, balancerpb.TransportProto_TCP),
		)
	}

	// Generate UDP IPv4 virtual services
	for i := 0; i < config.UDPIPv4VS; i++ {
		addr := netip.AddrFrom4([4]byte{172, 16, byte(i / 256), byte(i % 256)})
		virtualServices = append(
			virtualServices,
			createVS(addr, 53, balancerpb.TransportProto_UDP),
		)
	}

	// Generate UDP IPv6 virtual services
	for i := 0; i < config.UDPIPv6Vs; i++ {
		addr := netip.AddrFrom16(
			[16]byte{
				0xfc,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				0,
				byte(i / 256),
				byte(i % 256),
			},
		)
		virtualServices = append(
			virtualServices,
			createVS(addr, 53, balancerpb.TransportProto_UDP),
		)
	}

	// Session timeouts (in seconds)
	sessionTimeouts := &balancerpb.SessionsTimeouts{
		TcpSynAck: 60,
		TcpSyn:    120,
		TcpFin:    120,
		Tcp:       3600,
		Udp:       300,
		Default:   300,
	}

	// Source addresses for encapsulation
	sourceV4 := netip.AddrFrom4([4]byte{10, 255, 255, 254})
	sourceV6 := netip.AddrFrom16(
		[16]byte{0xfd, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
	)

	// Packet handler configuration
	packetHandler := &balancerpb.PacketHandlerConfig{
		Vs:               virtualServices,
		SourceAddressV4:  &balancerpb.Addr{Bytes: sourceV4.AsSlice()},
		SourceAddressV6:  &balancerpb.Addr{Bytes: sourceV6.AsSlice()},
		DecapAddresses:   []*balancerpb.Addr{},
		SessionsTimeouts: sessionTimeouts,
	}

	// State configuration
	capacity := uint64(
		config.SessionTableCapacity,
	)

	stateConfig := &balancerpb.StateConfig{
		SessionTableCapacity:      &capacity,
		SessionTableMaxLoadFactor: nil,
		RefreshPeriod:             nil,
		Wlc:                       nil,
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: packetHandler,
		State:         stateConfig,
	}
}

const (
	DeviceName   = "01:00.0"
	PipelineName = "pipeline0"
	FunctionName = "function0"
	ChainName    = "chain0"
)

func setupYanet(shm *yanet.SharedMemory) error {
	// Attach bootstrap agent to configure the controlplane
	bootstrap, err := shm.AgentReattach("bootstrap", 0, 1<<20)
	if err != nil {
		return fmt.Errorf("failed to attach to bootstrap agent: %w", err)
	}

	// Update function configuration
	{
		functionConfig := yanet.FunctionConfig{
			Name: FunctionName,
			Chains: []yanet.FunctionChainConfig{
				{
					Weight: 1,
					Chain: yanet.ChainConfig{
						Name: ChainName,
						Modules: []yanet.ChainModuleConfig{
							{
								Type: "balancer",
								Name: BalancerName,
							},
						},
					},
				},
			},
		}

		if err := bootstrap.UpdateFunction(functionConfig); err != nil {
			return fmt.Errorf("failed to update function: %w", err)
		}
	}

	// Update pipelines
	{
		inputPipelineConfig := yanet.PipelineConfig{
			Name:      PipelineName,
			Functions: []string{FunctionName},
		}

		dummyPipelineConfig := yanet.PipelineConfig{
			Name:      "dummy",
			Functions: []string{},
		}

		if err := bootstrap.UpdatePipeline(inputPipelineConfig); err != nil {
			return fmt.Errorf("failed to update pipeline: %w", err)
		}

		if err := bootstrap.UpdatePipeline(dummyPipelineConfig); err != nil {
			return fmt.Errorf("failed to update pipeline: %w", err)
		}
	}

	// Update devices
	{
		deviceConfig := yanet.DeviceConfig{
			Name: DeviceName,
			Input: []yanet.DevicePipelineConfig{
				{
					Name:   PipelineName,
					Weight: 1,
				},
			},
			Output: []yanet.DevicePipelineConfig{
				{
					Name:   "dummy",
					Weight: 1,
				},
			},
		}

		if err := bootstrap.UpdatePlainDevices([]yanet.DeviceConfig{deviceConfig}); err != nil {
			return fmt.Errorf("failed to update devices: %w", err)
		}
	}

	return nil
}

func Run(config *BenchConfig) error {
	bench, err := NewBench(config.Workers, TotalMemory, CpMemory)
	if err != nil {
		return fmt.Errorf("failed to create new bench: %s", err)
	}
	defer bench.Free()

	logLevel := zapcore.InfoLevel
	logger, _, _ := logging.Init(&logging.Config{
		Level: logLevel,
	})
	agent, err := balancer.NewBalancerAgent(
		bench.SharedMemory(),
		datasize.ByteSize(AgentMemory),
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create new balancer agent: %s", err)
	}

	balancerConfig := balancerConfig(config)
	if err := agent.NewBalancerManager(BalancerName, balancerConfig); err != nil {
		return fmt.Errorf("failed to create new balancer manager: %s", err)
	}

	if err := setupYanet(bench.SharedMemory()); err != nil {
		return fmt.Errorf("failed to setup yanet: %s", err)
	}

	// enable all reals
	bal, err := agent.BalancerManager(BalancerName)
	if err != nil {
		panic("balancer manager is incorrect")
	}

	if err := enableAllReals(bal); err != nil {
		return fmt.Errorf("failed to enable reals: %s", err)
	}

	start := make(chan struct{})
	info := make(chan workerInfo)
	perf := make(chan workerPerf, config.Workers)
	var readyWg sync.WaitGroup
	var wg sync.WaitGroup
	wg.Add(config.Workers)
	readyWg.Add(config.Workers)

	generator := NewGenerator(config, balancerConfig)

	for worker := range config.Workers {
		packetLists, err := bench.MakePacketLists(config.BatchesPerWorker)
		if err != nil {
			return fmt.Errorf("failed to create packet lists: %s", err)
		}
		for idx := range packetLists {
			if idx%100 == 0 {
				logger.Infow(
					"generating packets",
					"worker",
					worker,
					"progress",
					fmt.Sprintf(
						"%.2f%%",
						100.0*float32(idx)/float32(len(packetLists)),
					),
				)
			}
			packets := generator.generateWorkerPackets(
				worker,
				config.PacketsPerBatch,
			)
			if err := bench.InitPacketList(&packetLists[idx], packets...); err != nil {
				return fmt.Errorf(
					"failed to init packet list at index %d: %s",
					idx,
					err,
				)
			}
		}
		logger.Infow("generated all packets", "worker", worker)

		go workerRoutine(
			bench,
			&wg,
			&readyWg,
			info,
			perf,
			start,
			worker,
			packetLists,
			config.PacketsPerBatch*config.BatchesPerWorker,
		)
	}

	// Variables to track total benchmark duration
	var benchStart time.Time
	var benchDuration time.Duration

	go func() {
		readyWg.Wait()
		fmt.Printf("All workers are ready\nPress any key to start...\n")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		fmt.Println("Benchmark started")
		benchStart = time.Now()
		close(start)
		wg.Wait()
		benchDuration = time.Since(benchStart)

		fmt.Printf("All workers are finished\n")
		close(info)
	}()

	isErr := false
	workerPerfs := make([]workerPerf, 0, config.Workers)

	for info := range info {
		if info.isErr {
			logger.Errorw(info.info, "worker", info.idx, "tid", info.tid)
			isErr = true
		} else {
			logger.Infow(info.info, "worker", info.idx, "tid", info.tid)
		}
	}

	// Collect performance metrics
	close(perf)
	for p := range perf {
		workerPerfs = append(workerPerfs, p)
	}

	logger.Infow("done")

	// Print comprehensive balancer stats
	printSeparator()
	fmt.Printf("\n")
	fmt.Printf("                         BALANCER BENCHMARK RESULTS\n")
	printSeparator()

	// Print worker performance summary
	printWorkerPerformance(workerPerfs, benchDuration, config.Workers)

	fmt.Println()

	if isErr {
		return fmt.Errorf("some workers failed")
	} else {
		return nil
	}
}

// formatNumber adds comma separators to large numbers
func formatNumber(n uint64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	str := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// printSeparator prints a separator line
func printSeparator() {
	fmt.Println("================================================================================")
}

// printWorkerPerformance prints worker performance summary
func printWorkerPerformance(workerPerfs []workerPerf, benchDuration time.Duration, numWorkers int) {
	fmt.Println("\nWORKER PERFORMANCE")
	fmt.Println("------------------")

	// Calculate total packets
	var totalPackets int

	if len(workerPerfs) > 0 {
		// Sort by worker index for consistent output
		sortedPerfs := make([]workerPerf, len(workerPerfs))
		copy(sortedPerfs, workerPerfs)
		// Simple bubble sort by idx
		for i := range sortedPerfs {
			for j := i + 1; j < len(sortedPerfs); j++ {
				if sortedPerfs[i].idx > sortedPerfs[j].idx {
					sortedPerfs[i], sortedPerfs[j] = sortedPerfs[j], sortedPerfs[i]
				}
			}
		}

		// Print per-worker stats
		for _, p := range sortedPerfs {
			fmt.Printf("Worker %d:             %s packets in %s (%.2f MPpS)\n",
				p.idx,
				formatNumber(uint64(p.packets)),
				p.duration,
				p.mpps)
			totalPackets += p.packets
		}
		fmt.Println()
	}

	// Print aggregate stats based on total benchmark duration
	if benchDuration > 0 {
		aggregateMpps := float64(totalPackets) / benchDuration.Seconds() / 1e6
		fmt.Printf("Total Duration:       %s\n", benchDuration)
		fmt.Printf("Total Packets:        %s\n", formatNumber(uint64(totalPackets)))
		fmt.Printf("Aggregate Throughput: %.2f MPpS\n", aggregateMpps)
		if numWorkers > 0 {
			avgMpps := aggregateMpps / float64(numWorkers)
			fmt.Printf("Average per Worker:   %.2f MPpS\n", avgMpps)
		}
	} else {
		fmt.Printf("Total Packets:        %s\n", formatNumber(uint64(totalPackets)))
		fmt.Println("(Benchmark duration not available)")
	}
}
