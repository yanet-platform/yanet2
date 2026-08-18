package ffi

//#cgo CFLAGS: -I../../
//#include "api/counter.h"
import "C"

import "fmt"

const (
	singleValueIdx = 0
	packetsIdx     = 0
	bytesIdx       = 1
)

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
		}
	}

	return result, nil
}
