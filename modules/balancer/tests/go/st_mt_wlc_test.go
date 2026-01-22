package balancer_test

import (
	"fmt"
	"math/rand"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mock "github.com/yanet-platform/yanet2/mock/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
)

////////////////////////////////////////////////////////////////////////////////

// generateVSConfigs creates 5 virtual services with random real weights
func generateVSConfigsWithWlc() []vsConfigWithWeights {
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

////////////////////////////////////////////////////////////////////////////////

// workerRoutine sends packets and validates sessions
func workerDisableRealsAwareRoutine(
	workerID int,
	config *multithreadTestConfig,
	mock *mock.YanetMock,
	virtualServices []vsSimple,
	wg *sync.WaitGroup,
	errors chan error,
	resultState *workerState,
) {
	defer wg.Done()

	realMismatches := 0

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
					realMismatches += 1
					state.sessionReals[*sessionKey] = realIP
				}
				outputActiveSessions[*sessionKey] = true
			} else { // created new session
				state.sessionReals[*sessionKey] = realIP
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
				realMismatches += 1
			}
		}
		for _, touched := range outputActiveSessions {
			if !touched {
				realMismatches += 1
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

	if realMismatches > 10 {
		errors <- fmt.Errorf("worker %d: too many real mismatches: %d (max is 10)", workerID, realMismatches)
	}

	state.stats.sessions = len(state.sessions)

	*resultState = *state
}

////////////////////////////////////////////////////////////////////////////////

// refreshRoutine periodically calls sync to allow session table resizing
func refreshRoutine(
	mock *mock.YanetMock,
	balancer *balancer.BalancerManager,
	done chan struct{},
	config *multithreadTestConfig,
	errors chan error,
) {
	ticker := time.NewTicker(config.extendSessionTablePeriod)
	defer ticker.Stop()

	iter := 0

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if iter <= 5 {
				// disable one real for every virtual service
				config := balancer.Config()
				updates := []*balancerpb.RealUpdate{}
				enableTrue := true
				enableFalse := false
				for _, vs := range config.PacketHandler.Vs {
					if iter < 5 {
						updates = append(updates, &balancerpb.RealUpdate{
							RealId: &balancerpb.RealIdentifier{
								Real: vs.Reals[iter].Id,
								Vs:   vs.Id,
							},
							Weight: nil,
							Enable: &enableFalse,
						})
					}
					if iter > 0 {
						updates = append(updates, &balancerpb.RealUpdate{
							RealId: &balancerpb.RealIdentifier{
								Real: vs.Reals[iter-1].Id,
								Vs:   vs.Id,
							},
							Weight: nil,
							Enable: &enableTrue,
						})
					}
				}
				madeUpdates, err := balancer.UpdateReals(updates, false)
				if err != nil {
					errors <- err
				}
				if madeUpdates != len(updates) {
					errors <- fmt.Errorf("in refresh routine: expected %d updates, got %d", len(updates), madeUpdates)
				}
				iter += 1
			}
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

// runMultithreadedWlcTestMultithreadedTest executes the multithreaded test
func runMultithreadedWlcTest(t *testing.T, config *multithreadTestConfig) {
	// Generate VS configurations with random real weights
	vsConfigs := generateVSConfigsWithWlc()

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
		go workerDisableRealsAwareRoutine(
			i, config, mock,
			vsSimpleList, &wg, errors, &wStates[i],
		)
	}

	done := make(chan struct{}, 1)

	// Start extend session table routine
	go func() {
		refreshRoutine(mock, balancer, done, config, errors)
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

	// Log final statistics
	capacity := balancer.Config().State.SessionTableCapacity
	t.Logf("Final session table capacity: %d", *capacity)
}

////////////////////////////////////////////////////////////////////////////////

// TestMultithreadedWlcSessionTable tests session table with multiple workers
func TestMultithreadedWlcSessionTable(t *testing.T) {
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
				extendSessionTablePeriod: 150 * time.Millisecond,
			}

			runMultithreadedWlcTest(t, config)
		})
	}
}
