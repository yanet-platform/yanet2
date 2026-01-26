package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/c2h5oh/datasize"
	"github.com/yanet-platform/yanet2/common/go/logging"
	dataplane "github.com/yanet-platform/yanet2/lib/utils/go"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sys/unix"
)

var TotalMemory int = 1 << 31
var CpMemory int = 1 << 30
var AgentMemory int = CpMemory - 1<<27

var BalancerName string = "balancer0"

// generate packets and run handlers
type workerInfo struct {
	idx   int
	tid   int
	info  string
	isErr bool
}

func workerRoutine(bench *Bench, wg *sync.WaitGroup, readyWg *sync.WaitGroup, info chan workerInfo, start chan struct{}, idx int, packetList []dataplane.PacketList) {
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
		return
	}

	// set priority
	if err := unix.Setpriority(unix.PRIO_PROCESS, tid, -20); err != nil {
		sendError(fmt.Sprintf("failed to set priority: %s", err))
		return
	}

	sendMsg(fmt.Sprintf("pinned to CPU %d with priority %d", idx, -20))
	readyWg.Done()

	<-start

	if err := bench.HandlePackets(idx, packetList); err != nil {
		msg := fmt.Sprintf("failed to handle packets: %s", err)
		sendError(msg)
	} else {
		sendMsg("successfully handled packets")
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
	agent, err := balancer.NewBalancerAgent(bench.SharedMemory(), datasize.ByteSize(AgentMemory), logger)
	if err != nil {
		return fmt.Errorf("failed to create new balancer agent: %s", err)
	}

	// todo: add config
	if err := agent.NewBalancerManager(BalancerName, nil); err != nil {
		return fmt.Errorf("failed to create new balancer manager: %s", err)
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
	info := make(chan workerInfo, 10*config.Workers)
	var readyWg sync.WaitGroup
	var wg sync.WaitGroup
	wg.Add(config.Workers)
	readyWg.Add(config.Workers)

	generator := Generator{}

	for worker := range config.Workers {
		packetLists, err := bench.MakePacketLists(config.BatchesPerWorker)
		if err != nil {
			return fmt.Errorf("failed to create packet lists: %s", err)
		}
		for idx := range packetLists {
			packets := generator.generateWorkerPackets()
			if err := bench.InitPacketList(&packetLists[idx], packets...); err != nil {
				return fmt.Errorf("failed to init packet list at index %d: %s", idx, err)
			}
		}

		go workerRoutine(bench, &wg, &readyWg, info, start, worker, packetLists)
	}

	go func() {
		readyWg.Wait()
		fmt.Printf("All workers are ready\nPress any key to start...\n")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		close(start)
	}()

	go func() {
		for info := range info {
			logger.Infow("tid", info.tid, "worker", info.idx, "info", info.info)
		}
	}()

	logger.Infow("done")

	return nil
}
