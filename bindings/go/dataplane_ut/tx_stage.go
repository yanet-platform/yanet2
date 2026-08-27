package dataplaneut

/*
#include <stdlib.h>
#include "lib/dataplane_ut/tx_stage.h"
*/
import "C"

import (
	"fmt"
	"runtime"
)

// TxStageFixture drives the worker's remote staging path over a connection
// table of real pipes, without a NIC.
type TxStageFixture struct {
	ptr *C.struct_dataplane_ut_tx_stage

	deviceCount    int
	pipesPerDevice int
}

// TxStagePacket is one packet offered to the staging path.
type TxStagePacket struct {
	ptr *C.struct_packet
}

// NewTxStageFixture builds a connection table of deviceCount devices, each
// with pipesPerDevice pipes.
func NewTxStageFixture(deviceCount, pipesPerDevice int) (*TxStageFixture, error) {
	const maxPipes = 1 << 20

	if deviceCount < 0 || pipesPerDevice < 0 {
		return nil, fmt.Errorf(
			"negative dimensions: %d devices, %d pipes each",
			deviceCount, pipesPerDevice,
		)
	}
	if deviceCount > maxPipes || pipesPerDevice > maxPipes ||
		deviceCount*pipesPerDevice > maxPipes {
		return nil, fmt.Errorf(
			"dimensions too large: %d devices, %d pipes each",
			deviceCount, pipesPerDevice,
		)
	}

	ptr := C.dataplane_ut_tx_stage_new(
		C.uint32_t(deviceCount), C.uint32_t(pipesPerDevice),
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to build a tx stage fixture")
	}

	return &TxStageFixture{
		ptr:            ptr,
		deviceCount:    deviceCount,
		pipesPerDevice: pipesPerDevice,
	}, nil
}

// Free releases the fixture and everything it owns.
func (m *TxStageFixture) Free() {
	if m.ptr != nil {
		C.dataplane_ut_tx_stage_free(m.ptr)
		m.ptr = nil
	}
	runtime.KeepAlive(m)
}

// Packet takes a packet from the mock pool addressed to a device and carrying
// a flow hash.
func (m *TxStageFixture) Packet(txDeviceID uint16, hash uint32) (*TxStagePacket, error) {
	ptr := C.dataplane_ut_tx_stage_packet(
		m.ptr, C.uint16_t(txDeviceID), C.uint32_t(hash),
	)
	if ptr == nil {
		return nil, fmt.Errorf("mock pool exhausted")
	}

	return &TxStagePacket{ptr: ptr}, nil
}

// Offer hands one packet to the staging path.
func (m *TxStageFixture) Offer(packet *TxStagePacket) {
	C.dataplane_ut_tx_stage_offer(m.ptr, packet.ptr)
}

// Flush hands every pipe's held packets to their consumers.
func (m *TxStageFixture) Flush() {
	C.dataplane_ut_tx_stage_flush(m.ptr)
}

// checkPipe rejects coordinates outside the table before they reach C, where
// they would index the connection array unchecked.
func (m *TxStageFixture) checkPipe(device, pipe int) {
	if device < 0 || device >= m.deviceCount {
		panic(fmt.Sprintf(
			"device %d out of range [0,%d)", device, m.deviceCount,
		))
	}
	if pipe < 0 || pipe >= m.pipesPerDevice {
		panic(fmt.Sprintf(
			"pipe %d out of range [0,%d)", pipe, m.pipesPerDevice,
		))
	}
}

// Held reports how many packets a pipe is currently holding.
func (m *TxStageFixture) Held(device, pipe int) int {
	m.checkPipe(device, pipe)

	return int(C.dataplane_ut_tx_stage_held(
		m.ptr, C.uint32_t(device), C.uint32_t(pipe),
	))
}

// Placed reports how many packets a pipe has handed to its consumer.
func (m *TxStageFixture) Placed(device, pipe int) int {
	m.checkPipe(device, pipe)

	return int(C.dataplane_ut_tx_stage_placed(
		m.ptr, C.uint32_t(device), C.uint32_t(pipe),
	))
}

// TxCount reports the remote transmit counter.
func (m *TxStageFixture) TxCount() int {
	return int(C.dataplane_ut_tx_stage_tx_count(m.ptr))
}

// TxDrops reports the remote drop counter.
func (m *TxStageFixture) TxDrops() int {
	return int(C.dataplane_ut_tx_stage_tx_drops(m.ptr))
}

// Failed returns the failure list in the order packets joined it.
func (m *TxStageFixture) Failed() []*TxStagePacket {
	count := int(C.dataplane_ut_tx_stage_failed_count(m.ptr))

	out := make([]*TxStagePacket, count)
	for idx := range out {
		out[idx] = &TxStagePacket{
			ptr: C.dataplane_ut_tx_stage_failed_at(m.ptr, C.size_t(idx)),
		}
	}

	return out
}

// Same reports whether both handles refer to one packet.
func (m *TxStagePacket) Same(other *TxStagePacket) bool {
	return m.ptr == other.ptr
}
