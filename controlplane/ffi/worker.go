package ffi

//#cgo CFLAGS: -I../../
//#include "api/counter.h"
import "C"

import "fmt"

const (
	workerCounterSingleValueIdx  = C.uint64_t(0)
	workerCounterPacketsValueIdx = C.uint64_t(0)
	workerCounterBytesValueIdx   = C.uint64_t(1)
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
	Drops           uint64
}

func (m *DPConfig) WorkerCounters() ([]WorkerCounter, error) {
	counters := C.yanet_get_worker_counters(m.ptr)
	if counters == nil {
		return nil, fmt.Errorf("failed to get worker counters")
	}
	defer C.yanet_counter_handle_list_free(counters)

	counterByName := map[string]*C.struct_counter_handle{}
	for idx := range counters.count {
		handle := C.yanet_get_counter(counters, idx)
		if handle == nil {
			continue
		}
		counterByName[C.GoString(&handle.name[0])] = handle
	}

	rxBurstsHandle := counterByName["rx_bursts"]
	iterationsHandle := counterByName["iterations"]
	rxHandle := counterByName["rx"]
	txHandle := counterByName["tx"]
	remoteRxHandle := counterByName["remote_rx"]
	remoteTxHandle := counterByName["remote_tx"]
	localTxDropsHandle := counterByName["local_tx_drops"]
	remoteTxDropsHandle := counterByName["remote_tx_drops"]
	dropsHandle := counterByName["drops"]

	workerCount := counters.instance_count
	result := make([]WorkerCounter, workerCount)
	for idx := range workerCount {
		var metadata C.struct_worker_counter_metadata
		if C.yanet_get_worker_counter_metadata(m.ptr, idx, &metadata) != 0 {
			return nil, fmt.Errorf(
				"failed to get worker counter metadata for worker %d",
				uint64(idx),
			)
		}

		worker := WorkerCounter{
			WorkerIdx: uint32(idx),
			Iterations: uint64(C.yanet_get_counter_value(
				iterationsHandle.values,
				workerCounterSingleValueIdx,
				idx,
			)),
			RxPackets: uint64(C.yanet_get_counter_value(
				rxHandle.values,
				workerCounterPacketsValueIdx,
				idx,
			)),
			RxBytes: uint64(C.yanet_get_counter_value(
				rxHandle.values,
				workerCounterBytesValueIdx,
				idx,
			)),
			TxPackets: uint64(C.yanet_get_counter_value(
				txHandle.values,
				workerCounterPacketsValueIdx,
				idx,
			)),
			TxBytes: uint64(C.yanet_get_counter_value(
				txHandle.values,
				workerCounterBytesValueIdx,
				idx,
			)),
			RemoteRxPackets: uint64(C.yanet_get_counter_value(
				remoteRxHandle.values,
				workerCounterPacketsValueIdx,
				idx,
			)),
			RemoteTxPackets: uint64(C.yanet_get_counter_value(
				remoteTxHandle.values,
				workerCounterPacketsValueIdx,
				idx,
			)),
			LocalTxDrops: uint64(C.yanet_get_counter_value(
				localTxDropsHandle.values,
				workerCounterSingleValueIdx,
				idx,
			)),
			RemoteTxDrops: uint64(C.yanet_get_counter_value(
				remoteTxDropsHandle.values,
				workerCounterSingleValueIdx,
				idx,
			)),
			Drops: uint64(C.yanet_get_counter_value(
				dropsHandle.values,
				workerCounterSingleValueIdx,
				idx,
			)),
		}

		rxBurstSize := uint32(metadata.rx_burst_size) + 1
		worker.RxBursts = make([]uint64, rxBurstSize)
		for burstIdx := C.uint64_t(0); burstIdx < rxBurstsHandle.size; burstIdx++ {
			worker.RxBursts[burstIdx] = uint64(C.yanet_get_counter_value(
				rxBurstsHandle.values,
				burstIdx,
				idx,
			))
		}

		if rxBurstSize > 0 {
			worker.MaxBurstSize = rxBurstSize - 1
		}

		worker.CoreID = uint32(metadata.core_id)
		worker.DeviceID = uint32(metadata.device_id)
		worker.QueueID = uint32(metadata.queue_id)
		worker.MaxBurstSize = uint32(metadata.rx_burst_size)

		result[idx] = worker
	}

	return result, nil
}
