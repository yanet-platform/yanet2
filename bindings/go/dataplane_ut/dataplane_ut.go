package dataplaneut

/*
#cgo CFLAGS: -I../../../
#cgo CFLAGS: -I../../../lib
#cgo CFLAGS: -I../../../build/subprojects/dpdk/lib
#cgo CFLAGS: -I../../../build/lib/dataplane_ut

// Library search paths — modules, devices, and support libs.
#cgo LDFLAGS: -L../../../build/modules/blackhole/dataplane
#cgo LDFLAGS: -L../../../build/modules/decap/dataplane
#cgo LDFLAGS: -L../../../build/modules/dscp/dataplane
#cgo LDFLAGS: -L../../../build/modules/acl/dataplane
#cgo LDFLAGS: -L../../../build/modules/fwstate/dataplane
#cgo LDFLAGS: -L../../../build/modules/forward/dataplane
#cgo LDFLAGS: -L../../../build/modules/mirror/dataplane
#cgo LDFLAGS: -L../../../build/modules/route/dataplane
#cgo LDFLAGS: -L../../../build/modules/nat64/dataplane
#cgo LDFLAGS: -L../../../build/modules/pdump/dataplane
#cgo LDFLAGS: -L../../../build/devices/plain/dataplane
#cgo LDFLAGS: -L../../../build/devices/vlan/dataplane
#cgo LDFLAGS: -L../../../build/lib/dataplane_ut
#cgo LDFLAGS: -L../../../build/lib/dataplane/pipeline
#cgo LDFLAGS: -L../../../build/lib/dataplane/module
#cgo LDFLAGS: -L../../../build/lib/dataplane/worker
#cgo LDFLAGS: -L../../../build/lib/dataplane/config
#cgo LDFLAGS: -L../../../build/lib/dataplane/packet
#cgo LDFLAGS: -L../../../build/lib/logging
#cgo LDFLAGS: -L../../../build/lib/controlplane/agent
#cgo LDFLAGS: -L../../../build/lib/controlplane/config
#cgo LDFLAGS: -L../../../build/lib/counters
#cgo LDFLAGS: -L../../../build/lib/errors
#cgo LDFLAGS: -L../../../build/lib/filter
#cgo LDFLAGS: -L../../../build/lib/fwstate
#cgo LDFLAGS: -L../../../build/lib/utils

// Link all static libs inside a group so the linker resolves circular
// references between them (fwstate->acl, worker->pipeline, etc.).
// fwstate depends on acl — acl must come first inside the group.
#cgo LDFLAGS: -Wl,--start-group
#cgo LDFLAGS: -lblackhole_dp -ldecap_dp -ldscp_dp -lacl_dp -lfwstate_dp -lforward_dp -lmirror_dp -lroute_dp -lnat64_dp -lpdump_dp
#cgo LDFLAGS: -lplain_dp -lvlan_dp
#cgo LDFLAGS: -ldataplane_ut -lpipeline -lmodule -lworker_dp -lconfig_dp -lpacket
#cgo LDFLAGS: -llogging -lagent -lconfig_cp -lcounters -lerrors -lfilter_compiler -lfwstate -llib_utils
#cgo LDFLAGS: -Wl,--end-group

#cgo LDFLAGS: -lnuma
#cgo LDFLAGS: -ldl

#cgo LDFLAGS: -Wl,-E

#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane/packet/packet.h"

// dataplane_ut_set_hashes writes hashes[i] into the hash field of the
// i-th packet in list, stopping at count or the end of the list,
// whichever comes first.
static void
dataplane_ut_set_hashes(
	struct packet_list *list, const uint32_t *hashes, size_t count
) {
	struct packet *p = list->first;
	for (size_t i = 0; i < count && p != NULL; i++) {
		p->hash = hashes[i];
		p = p->next;
	}
}

// alloc_cptr_array copies n pointers from src (Go-backed) into a fresh
// C-heap array and returns it.  The caller frees with free_cptr_array.
static const char **
alloc_cptr_array(char **src, size_t n) {
	const char **dst = (const char **)malloc(n * sizeof(char *));
	if (dst == NULL) {
		return NULL;
	}
	for (size_t i = 0; i < n; i++) {
		dst[i] = src[i];
	}
	return dst;
}

static void
free_cptr_array(const char **arr) {
	free(arr);
}

void
keep_refs(void **ptrs) {
	extern struct module *new_module_blackhole(void);
	extern struct module *new_module_decap(void);
	extern struct module *new_module_dscp(void);
	extern struct module *new_module_acl(void);
	extern struct module *new_module_fwstate(void);
	extern struct module *new_module_forward(void);
	extern struct module *new_module_mirror(void);
	extern struct module *new_module_route(void);
	extern struct module *new_module_nat64(void);
	extern struct module *new_module_pdump(void);

	extern struct device *new_device_plain(void);
	extern struct device *new_device_vlan(void);

	static void *funcs[] = {
		new_module_blackhole,
		new_module_decap,
		new_module_dscp,
		new_module_acl,
		new_module_fwstate,
		new_module_forward,
		new_module_mirror,
		new_module_route,
		new_module_nat64,
		new_module_pdump,

		new_device_plain,
		new_device_vlan,
	};

	*ptrs = funcs;
}
*/
import "C"
import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/gopacket/gopacket"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// Config holds parameters for constructing a Harness.
//
// WorkerCount must be >= 1.
// Devices, Modules, and DevicesToLoad may be empty.
type Config struct {
	CPMemory      uint64
	DPMemory      uint64
	WorkerCount   uint64
	Devices       []string
	Modules       []string
	DevicesToLoad []string
}

// Result of one pipeline round.
type Result struct {
	Output []*framework.PacketInfo
	Drop   []*framework.PacketInfo
}

// RawResult holds the raw first-segment bytes of each output and drop packet
// from one pipeline round, without gopacket parsing.
//
// Use this when the packets under test cannot be decoded by gopacket (e.g.
// malformed or multi-segment packets that are passed through as-is).
type RawResult struct {
	Output [][]byte
	Drop   [][]byte
}

// Harness is a handle to an in-process dataplane harness.
//
// Never move this object in memory after construction — the underlying C
// struct holds self-referential pointers.
type Harness struct {
	ptr *C.struct_dataplane_ut
	// Registered topology device count; device ids passed to bench runs
	// must stay below it. An empty Devices config still exposes the
	// implicit device slot zero.
	deviceCount int
}

// NewHarness constructs a Harness from cfg.
// Free must be called when the test is done.
func NewHarness(cfg Config) (*Harness, error) {
	// Convert Go string slices to C string arrays. The *C.char values are
	// C-heap strings; toCStringArray returns Go-backed pointer slices, so
	// we copy the pointers into C-heap arrays to satisfy the CGO rule that
	// no Go pointer may appear inside a value passed to C.
	cDevices, freeDevices := toCStringArray(cfg.Devices)
	defer freeDevices()

	cModules, freeModules := toCStringArray(cfg.Modules)
	defer freeModules()

	cDevicesToLoad, freeDevicesToLoad := toCStringArray(cfg.DevicesToLoad)
	defer freeDevicesToLoad()

	cCfg := C.struct_dataplane_ut_config{
		cp_memory:             C.size_t(cfg.CPMemory),
		dp_memory:             C.size_t(cfg.DPMemory),
		worker_count:          C.size_t(cfg.WorkerCount),
		device_count:          C.size_t(len(cfg.Devices)),
		module_count:          C.size_t(len(cfg.Modules)),
		devices_to_load_count: C.size_t(len(cfg.DevicesToLoad)),
	}

	// Assign C-heap copies of the pointer arrays so the C struct contains
	// no Go pointers, satisfying the CGO pointer-passing rules.
	if len(cfg.Devices) > 0 {
		cArr := C.alloc_cptr_array(&cDevices[0], C.size_t(len(cDevices)))
		defer C.free_cptr_array(cArr)
		cCfg.devices = cArr
	}
	if len(cfg.Modules) > 0 {
		cArr := C.alloc_cptr_array(&cModules[0], C.size_t(len(cModules)))
		defer C.free_cptr_array(cArr)
		cCfg.modules = cArr
	}
	if len(cfg.DevicesToLoad) > 0 {
		cArr := C.alloc_cptr_array(&cDevicesToLoad[0], C.size_t(len(cDevicesToLoad)))
		defer C.free_cptr_array(cArr)
		cCfg.devices_to_load = cArr
	}

	ptr := C.dataplane_ut_new(&cCfg)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create dataplane harness: dataplane_ut_new returned NULL")
	}

	deviceCount := len(cfg.Devices)
	if deviceCount == 0 {
		deviceCount = 1
	}
	return &Harness{ptr: ptr, deviceCount: deviceCount}, nil
}

// Free tears down the harness. Nil-safe.
func (m *Harness) Free() {
	C.dataplane_ut_free(m.ptr)
	m.ptr = nil
}

// SharedMemory returns the shared-memory handle backing this harness.
func (m *Harness) SharedMemory() *ffi.SharedMemory {
	shm := C.dataplane_ut_shm(m.ptr)
	return ffi.NewSharedMemoryFromRaw(unsafe.Pointer(shm))
}

// SetCurrentTime installs a deterministic wall-clock value used by the next
// HandlePackets call.
func (m *Harness) SetCurrentTime(t time.Time) {
	C.dataplane_ut_set_time_ns(m.ptr, C.uint64_t(t.UnixNano()))
}

// CurrentTime returns the mock wall-clock value currently installed.
func (m *Harness) CurrentTime() time.Time {
	ns := uint64(C.dataplane_ut_get_time_ns(m.ptr))
	return time.Unix(0, int64(ns))
}

// AdvanceTime adds d to the current mock wall-clock, stores the result, and
// returns the new time.
func (m *Harness) AdvanceTime(d time.Duration) time.Time {
	now := m.CurrentTime().Add(d)
	m.SetCurrentTime(now)
	return now
}

// HandlePackets runs packets through worker 0 for one pipeline round.
func (m *Harness) HandlePackets(packets ...gopacket.Packet) (*Result, error) {
	return m.handlePackets(0, nil, packets...)
}

// HandlePacketsOnWorker runs packets through the given worker index for one
// pipeline round.
func (m *Harness) HandlePacketsOnWorker(worker int, packets ...gopacket.Packet) (*Result, error) {
	return m.handlePackets(worker, nil, packets...)
}

// HandlePacketsWithHashes runs one pipeline round with caller-supplied per-packet hash values.
//
// hashes[i] is written into packets[i].hash before the round, giving
// modules that select outputs via hash (e.g. route ECMP) deterministic
// test-controlled values. hashes may be shorter than packets; entries
// past len(hashes) keep their original hash. Returns an error if
// len(hashes) > len(packets).
func (m *Harness) HandlePacketsWithHashes(
	hashes []uint32,
	packets ...gopacket.Packet,
) (*Result, error) {
	if len(hashes) > len(packets) {
		return nil, fmt.Errorf(
			"hashes length %d exceeds packets length %d",
			len(hashes),
			len(packets),
		)
	}
	return m.handlePackets(0, hashes, packets...)
}

// HandleSegmentedPackets runs one pipeline round on worker 0 where each input
// packet is supplied as an ordered list of byte segments, producing a chained
// multi-segment mbuf. The result is returned unparsed so callers can inspect
// packets that gopacket cannot decode.
func (m *Harness) HandleSegmentedPackets(packets ...[][]byte) (*RawResult, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	builtPackets := make([]*dataplane.Packet, 0, len(packets))
	for idx := range packets {
		pkt, err := dataplane.NewPacketFromSegments(packets[idx], 0, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to build segmented packet at index %d: %w", idx, err)
		}
		pinner.Pin(pkt)
		builtPackets = append(builtPackets, pkt)
	}

	packetList := dataplane.NewPacketList(&pinner, builtPackets)

	pinner.Pin(m)

	var result C.struct_dataplane_ut_round_result
	C.dataplane_ut_run(
		m.ptr,
		C.size_t(0),
		(*C.struct_packet_list)(unsafe.Pointer(packetList)),
		&result,
	)

	pinner.Pin(&result)

	output := (*dataplane.PacketList)(unsafe.Pointer(&result.output))
	drop := (*dataplane.PacketList)(unsafe.Pointer(&result.drop))

	rawBytes := func(list *dataplane.PacketList) [][]byte {
		data := list.Data()
		out := make([][]byte, 0, len(data))
		for idx := range data {
			b := make([]byte, len(data[idx].Payload))
			copy(b, data[idx].Payload)
			out = append(out, b)
		}
		return out
	}

	return &RawResult{
		Output: rawBytes(output),
		Drop:   rawBytes(drop),
	}, nil
}

// handlePackets is the shared implementation for all HandlePackets variants.
//
// It builds the packet list, optionally overwrites per-packet hash values,
// then runs one pipeline round on the given worker index.
func (m *Harness) handlePackets(
	worker int,
	hashes []uint32,
	packets ...gopacket.Packet,
) (*Result, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	payloads := xpacket.PacketsGoPayload(packets...)
	data := make([]dataplane.PacketData, 0, len(payloads))
	for idx := range payloads {
		data = append(data, dataplane.PacketData{
			Payload:    payloads[idx],
			TxDeviceId: 0,
			RxDeviceId: 0,
		})
	}

	packetList, err := dataplane.NewPacketListFromData(&pinner, data...)
	if err != nil {
		return nil, fmt.Errorf("failed to build packet list: %w", err)
	}

	if len(hashes) > 0 {
		pinner.Pin(&hashes[0])
		C.dataplane_ut_set_hashes(
			(*C.struct_packet_list)(unsafe.Pointer(packetList)),
			(*C.uint32_t)(unsafe.Pointer(&hashes[0])),
			C.size_t(len(hashes)),
		)
	}

	pinner.Pin(m)

	var result C.struct_dataplane_ut_round_result
	C.dataplane_ut_run(
		m.ptr,
		C.size_t(worker),
		(*C.struct_packet_list)(unsafe.Pointer(packetList)),
		&result,
	)

	pinner.Pin(&result)

	output := (*dataplane.PacketList)(unsafe.Pointer(&result.output))
	drop := (*dataplane.PacketList)(unsafe.Pointer(&result.drop))

	return &Result{
		Output: output.Info(),
		Drop:   drop.Info(),
	}, nil
}

// warnUnoptimizedOnce guards the non-optimized build warning so it prints
// at most once per process regardless of how many benchmarks call Bench.
var warnUnoptimizedOnce sync.Once

// benchOptions configures a Bench run.
type benchOptions struct {
	resetPayload bool
	deviceIDs    []uint16
}

func newBenchOptions() *benchOptions {
	return &benchOptions{}
}

// BenchOption customizes a Bench run.
type BenchOption func(*benchOptions)

// WithPayloadReset restores every packet payload to its captured state
// between rounds.
//
// Required for modules that rewrite packet contents in place, such as
// route decrementing TTL; costs one payload copy per packet per round.
// Only the bytes are restored, so geometry-changing modules stay out of
// scope.
func WithPayloadReset() BenchOption {
	return func(opts *benchOptions) {
		opts.resetPayload = true
	}
}

// WithDeviceIDs assigns per-packet rx/tx device ids before the run.
//
// ids[i] is written into packets[i]; packets past len(ids) keep device 0.
// The round dispatch then splits the burst into per-device fronts the way
// a real multi-device worker round does. Ids persist across bench rounds
// via the harness packet snapshot.
func WithDeviceIDs(ids []uint16) BenchOption {
	return func(opts *benchOptions) {
		opts.deviceIDs = ids
	}
}

// Bench runs b.N pipeline rounds over the given packets on worker 0,
// timing only the C-side loop.
//
// Harness, config, and packet-build setup done by the caller before this
// call is excluded from the measurement. Packet payloads are reused as
// is between rounds unless WithPayloadReset is given.
func (m *Harness) Bench(
	b *testing.B, packets []gopacket.Packet, options ...BenchOption,
) {
	b.Helper()

	opts := newBenchOptions()
	for _, o := range options {
		o(opts)
	}

	warnUnoptimizedOnce.Do(func() {
		if C.dataplane_ut_build_optimized() == 0 {
			b.Logf("WARNING: dataplane built without optimizations (or with sanitizers); benchmark numbers are NOT representative — rebuild meson with buildtype=debugoptimized or release and b_sanitize=none.")
		}
	})

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	// The round dispatch indexes per-device scheduling arrays by the
	// packet device id, so an id outside the registered topology would
	// corrupt memory on the C side.
	for idx, deviceID := range opts.deviceIDs {
		if int(deviceID) >= m.deviceCount {
			b.Fatalf(
				"device id %d at packet %d exceeds topology device count %d",
				deviceID,
				idx,
				m.deviceCount,
			)
		}
	}

	payloads := xpacket.PacketsGoPayload(packets...)
	data := make([]dataplane.PacketData, 0, len(payloads))
	for idx := range payloads {
		packetData := dataplane.PacketData{Payload: payloads[idx]}
		if idx < len(opts.deviceIDs) {
			packetData.TxDeviceId = opts.deviceIDs[idx]
			packetData.RxDeviceId = opts.deviceIDs[idx]
		}
		data = append(data, packetData)
	}

	packetList, err := dataplane.NewPacketListFromData(&pinner, data...)
	if err != nil {
		b.Fatalf("failed to build packet list: %v", err)
	}

	pinner.Pin(m)

	var totalBytes int64
	for idx := range payloads {
		totalBytes += int64(len(payloads[idx]))
	}
	numPkts := len(payloads)

	reset := C.int(0)
	if opts.resetPayload {
		reset = C.int(1)
	}

	b.ResetTimer()
	C.dataplane_ut_run_rounds(
		m.ptr,
		C.size_t(0),
		(*C.struct_packet_list)(unsafe.Pointer(packetList)),
		C.uint64_t(b.N),
		reset,
	)
	b.StopTimer()

	elapsed := b.Elapsed()
	if elapsed > 0 {
		bits := float64(totalBytes) * float64(b.N) * 8
		pkts := float64(numPkts) * float64(b.N)
		b.ReportMetric(bits/elapsed.Seconds()/1e9, "Gbit/s")
		b.ReportMetric(pkts/elapsed.Seconds()/1e6, "Mpps")
	}
}

// toCStringArray converts a Go string slice to a slice of *C.char values and
// returns a cleanup function that frees each element.
func toCStringArray(ss []string) ([]*C.char, func()) {
	out := make([]*C.char, len(ss))
	for idx, s := range ss {
		out[idx] = C.CString(s)
	}
	free := func() {
		for idx := range out {
			C.free(unsafe.Pointer(out[idx]))
		}
	}
	return out, free
}
