package dataplaneut

/*
#include "lib/dataplane_ut/dataplane_ut.h"
*/
import "C"

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/gopacket/gopacket"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// BenchRewriting runs b.N pipeline rounds over the packets on worker 0, as
// Bench does, but first lets the callback rewrite the frames of each round.
//
// Bench's rounds repeat the same flows, so the per-flow state a module looks
// up stays in cache; rewriting, say, source addresses and ports cycles the
// rounds through as many flows as a cold benchmark needs. The callback gets
// the round's index, counting from zero, and the frames in packet order:
// the very buffers the pipeline reads, as the previous round left them,
// valid until this call returns. It may change bytes but not move headers,
// as each round restores the metadata parsed from the packets given, the
// address hash among it. Each round is a call into C timed together with the
// callback, a cost equal for every pipeline, which an empty one cancels.
//
// The options mean what they mean to Bench. A reset undoes what the module
// did to the packets after each round, so the callback gets the frames as
// it left them, and with WithPayloadReset and WithGeometryReset together
// that holds for a module that moves headers.
func (m *Harness) BenchRewriting(
	b *testing.B,
	packets []gopacket.Packet,
	rewrite func(round int, frames [][]byte),
	options ...BenchOption,
) {
	b.Helper()

	opts := newBenchOptions()
	for _, o := range options {
		o(opts)
	}

	warnUnoptimized(b)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	devices := m.benchDevices(b, opts, len(packets))

	payloads := xpacket.PacketsGoPayload(packets...)
	data := make([]dataplane.PacketData, 0, len(payloads))
	for idx := range payloads {
		data = append(data, dataplane.PacketData{Payload: payloads[idx]})
	}

	packetList, err := dataplane.NewPacketListFromData(&pinner, data...)
	if err != nil {
		b.Fatalf("failed to build packet list: %v", err)
	}
	defer packetList.Free()

	var totalBytes int64
	frames := make([][]byte, 0, len(payloads))
	for packet := packetList.First(); packet != nil; packet = packet.Next() {
		frame := packet.Data().Payload
		frames = append(frames, frame)
		totalBytes += int64(len(frame))
	}

	var devicesPtr *C.uint16_t
	if len(devices) > 0 {
		pinner.Pin(&devices[0])
		devicesPtr = (*C.uint16_t)(&devices[0])
	}
	list := (*C.struct_packet_list)(unsafe.Pointer(packetList))

	b.ResetTimer()
	for round := range b.N {
		rewrite(round, frames)
		rc := C.dataplane_ut_run_rounds(
			m.ptr,
			C.size_t(0),
			devicesPtr,
			list,
			C.uint64_t(1),
			opts.reset(),
		)
		if rc != 0 {
			b.Fatalf("failed to run round %d: rc=%d", round, rc)
		}
	}
	b.StopTimer()

	reportThroughput(b, totalBytes, len(frames))
}
