package ffi

//#cgo CFLAGS: -I../../
//#include "api/counter.h"
import "C"

import "fmt"

// maxUint32 bounds the pool gauges: both live in the dataplane as
// mempool object counts, whose native width is 32 bits.
const maxUint32 = uint64(1<<32 - 1)

const (
	singleValueIdx = 0
	packetsIdx     = 0
	bytesIdx       = 1
)

// WorkerRxMempool is the occupancy of one worker's RX packet pool, in
// mempool objects. In-use objects are the difference Capacity minus
// Available, which counts mbufs held by NIC descriptors and in flight.
type WorkerRxMempool struct {
	Capacity  uint32
	Available uint32
}

type WorkerCounter struct {
	WorkerIdx       uint32
	CoreID          uint32
	DeviceID        uint32
	QueueID         uint32
	MaxBurstSize    uint32
	RxBursts        []uint64
	Iterations      uint64
	RxPackets       uint64
	RxBytes         uint64
	TxPackets       uint64
	TxBytes         uint64
	RemoteRxPackets uint64
	RemoteTxPackets uint64
	LocalTxDrops    uint64
	RemoteTxDrops   uint64
	Disposed        uint64
	RxMempool       *WorkerRxMempool
}

func (m *DPConfig) WorkerCounters() ([]WorkerCounter, error) {
	rawCounters := m.RawWorkerCounters()
	if rawCounters == nil {
		return nil, fmt.Errorf("failed to get worker counters")
	}

	counterSet, err := NewCounterSet(rawCounters)
	if err != nil {
		return nil, fmt.Errorf("failed to index worker counters: %w", err)
	}

	rxBursts := counterSet.Lookup("rx_bursts", 0).Require()
	iterations := counterSet.Lookup("iterations", 1).Require()
	rx := counterSet.Lookup("rx", 2).Require()
	tx := counterSet.Lookup("tx", 2).Require()
	remoteRx := counterSet.Lookup("remote_rx", 2).Require()
	remoteTx := counterSet.Lookup("remote_tx", 2).Require()
	localTxDrops := counterSet.Lookup("local_tx_drops", 1).Require()
	remoteTxDrops := counterSet.Lookup("remote_tx_drops", 1).Require()
	drops := counterSet.Lookup("drops", 1).Require()
	rxMempoolCapacity := counterSet.Lookup("rx_mempool_capacity", 1).Require()
	rxMempoolAvailable := counterSet.Lookup("rx_mempool_available", 1).Require()

	if err := counterSet.Err(); err != nil {
		return nil, fmt.Errorf("failed to resolve worker counters: %w", err)
	}

	workers := counterSet.Instances()
	result := make([]WorkerCounter, workers)
	for idx := range workers {
		var metadata C.struct_worker_counter_metadata
		if C.yanet_get_worker_counter_metadata(m.ptr, C.uint64_t(idx), &metadata) != 0 {
			return nil, fmt.Errorf(
				"failed to get metadata for worker at index %d",
				idx,
			)
		}

		pool, err := workerRxMempool(
			rxMempoolCapacity.Value(idx, singleValueIdx),
			rxMempoolAvailable.Value(idx, singleValueIdx),
		)
		if err != nil {
			return nil, fmt.Errorf("worker %d: %w", idx, err)
		}

		result[idx] = WorkerCounter{
			WorkerIdx:       uint32(idx),
			CoreID:          uint32(metadata.core_id),
			DeviceID:        uint32(metadata.device_id),
			QueueID:         uint32(metadata.queue_id),
			MaxBurstSize:    uint32(metadata.rx_burst_size),
			RxBursts:        rxBursts.InstanceValues(idx),
			Iterations:      iterations.Value(idx, singleValueIdx),
			RxPackets:       rx.Value(idx, packetsIdx),
			RxBytes:         rx.Value(idx, bytesIdx),
			TxPackets:       tx.Value(idx, packetsIdx),
			TxBytes:         tx.Value(idx, bytesIdx),
			RemoteRxPackets: remoteRx.Value(idx, packetsIdx),
			RemoteTxPackets: remoteTx.Value(idx, packetsIdx),
			LocalTxDrops:    localTxDrops.Value(idx, singleValueIdx),
			RemoteTxDrops:   remoteTxDrops.Value(idx, singleValueIdx),
			Disposed:        drops.Value(idx, singleValueIdx),
			RxMempool:       pool,
		}
	}

	return result, nil
}

// workerRxMempool validates one worker's pool gauge pair. A snapshot no
// healthy dataplane would publish is an error rather than a clamped or
// zero-valued pool, so monitoring sees the failure instead of a
// plausible value.
func workerRxMempool(capacity, available uint64) (*WorkerRxMempool, error) {
	if capacity == 0 || capacity > maxUint32 {
		return nil, fmt.Errorf(
			"invalid rx mempool capacity %d", capacity,
		)
	}
	if available > capacity {
		return nil, fmt.Errorf(
			"rx mempool availability %d exceeds capacity %d",
			available,
			capacity,
		)
	}

	return &WorkerRxMempool{
		Capacity:  uint32(capacity),
		Available: uint32(available),
	}, nil
}
