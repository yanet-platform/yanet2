package dataplaneut

/*
#include "lib/dataplane_ut/stages.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
)

// Stage selects one published device entry within an opened worker round.
type Stage struct {
	Worker      int
	DeviceID    uint16
	Output      bool
	PacketFlags uint16
}

// StageResult owns copied packet bytes and observations taken before disposal.
type StageResult struct {
	RawResult
	OutstandingMbufs uint64
	OutputFlags      []uint16
}

// BeginRound advances a worker without polling its device entries.
//
// Stages may run repeatedly until another round or configuration install
// invalidates this round. Every call releases all native packet ownership.
func (m *Harness) BeginRound(worker int) error {
	if err := m.validateWorkerIdx(worker); err != nil {
		return err
	}
	if status := C.dataplane_ut_begin_round(m.ptr, C.size_t(worker)); status != 0 {
		return fmt.Errorf("failed to begin worker %d round: %d", worker, status)
	}
	return nil
}

// SetAllocationBudget bounds additional live allocations in the real pool.
//
// Zero forces allocation failure; minus one restores the normal capacity.
func (m *Harness) SetAllocationBudget(budget int) error {
	if status := C.dataplane_ut_set_allocation_budget(m.ptr, C.int64_t(budget)); status != 0 {
		return fmt.Errorf("invalid allocation budget %d", budget)
	}
	return nil
}

// RunStage executes a published device entry and its scheduled redirects.
//
// Inputs and emissions are disposed before return, including on failure.
// Returned bytes and flags are independent copies; the occupancy observation
// records the pool state while the result still owned its emitted packets.
func (m *Harness) RunStage(stage Stage, packets ...[]byte) (*StageResult, error) {
	if err := m.validateWorkerIdx(stage.Worker); err != nil {
		return nil, err
	}
	var pinner runtime.Pinner
	defer pinner.Unpin()
	var input C.struct_dataplane_ut_round_result
	pinner.Pin(&input)
	defer C.dataplane_ut_round_result_free(&input)
	for idx, payload := range packets {
		if len(payload) == 0 {
			return nil, fmt.Errorf("packet %d has an empty payload", idx)
		}
		pinner.Pin(&payload[0])
		if status := C.dataplane_ut_stage_input_append(&input.output, unsafe.Pointer(&payload[0]), C.size_t(len(payload))); status != 0 {
			return nil, fmt.Errorf("build stage packet %d: %d", idx, status)
		}
	}
	cStage := C.struct_dataplane_ut_stage{
		worker_idx: C.size_t(stage.Worker), device_id: C.uint16_t(stage.DeviceID),
		output: C.bool(stage.Output), packet_flags: C.uint16_t(stage.PacketFlags),
	}
	var result C.struct_dataplane_ut_round_result
	pinner.Pin(&result)
	pinner.Pin(&cStage)
	status := C.dataplane_ut_run_stage(m.ptr, &cStage,
		&input.output, &result,
	)
	defer C.dataplane_ut_round_result_free(&result)
	if status != 0 {
		return nil, fmt.Errorf("run stage: %d", status)
	}
	copyPayloads := func(list *C.struct_packet_list) [][]byte {
		data := (*dataplane.PacketList)(unsafe.Pointer(list)).Data()
		payloads := make([][]byte, len(data))
		for idx := range data {
			payloads[idx] = append([]byte(nil), data[idx].Payload...)
		}
		return payloads
	}
	output := &StageResult{
		Output:           copyPayloads(&result.output),
		Drop:             copyPayloads(&result.drop),
		OutstandingMbufs: m.OutstandingMbufs(),
	}
	for packet := result.output.first; packet != nil; packet = packet.next {
		output.OutputFlags = append(output.OutputFlags, uint16(packet.flags))
	}
	return output, nil
}
