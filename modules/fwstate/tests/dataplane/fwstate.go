package fwstate

//#cgo CFLAGS: -I../../../.. -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/lib/dataplane_ut -ldataplane_ut
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../build/lib/statemap -lstatemap
//#cgo LDFLAGS: -L../../../../build/objects/fwstate/api -lfwstate_objects
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/pipeline -lpipeline
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/worker -lworker_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/fwstate -lfwstate
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
//#cgo LDFLAGS: -lnuma
/*
#include <harness.h>
*/
import "C"
import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/testutils"
)

// fwstateModuleConfig creates a fwstate module config linked, by name, to two
// fwstate-map objects registered in the harness agent's object registry, and
// spawns a per-worker counter storage that is reused across
// fwstateHandlePackets calls so that counter values accumulate. The returned
// storage must be freed with [fwstateCounterStorageFree] (the caller owns it).
func fwstateModuleConfig(memCtx testutils.MemoryContext) (*C.struct_cp_module, *C.struct_counter_storage) {
	return fwstateModuleConfigWithStashSize(memCtx, 0)
}

// fwstateModuleConfigWithStashSize is fwstateModuleConfig with maps whose
// per-worker stash buffer is stashSize bytes, zero selecting the default.
func fwstateModuleConfigWithStashSize(memCtx testutils.MemoryContext, stashSize uint64) (*C.struct_cp_module, *C.struct_counter_storage) {
	// Allocate the stand-in agent through the harness constructor, which zeroes
	// the whole structure, not just the memory context, and wires the object
	// registry the harness's own link-name lookups resolve against.
	//
	// The dangling-free protocol and the loaded counts all read agent fields
	// an unzeroed structure would leave as stale bytes.
	cStubAgent := C.CString("stub agent")
	defer C.free(unsafe.Pointer(cStubAgent))

	agent := C.fwstate_test_agent_new(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		cStubAgent,
	)
	if agent == nil {
		panic("failed to allocate agent")
	}

	// Use the proper API to create the module config
	cName := C.CString("test")
	defer C.free(unsafe.Pointer(cName))

	// Create the map objects, give each a first table layer, and register
	// them so the module construction can resolve the names.
	cName4 := C.CString("fw4")
	defer C.free(unsafe.Pointer(cName4))
	obj4 := C.fwstate_test_map_object_new(agent, C.bool(false), cName4, C.uint64_t(stashSize))
	if obj4 == nil {
		panic("failed to create fwstate-map v4 object")
	}
	if rc := C.fwstate_test_register_object(agent, obj4); rc != 0 {
		panic("failed to register fwstate-map v4 object")
	}

	cName6 := C.CString("fw6")
	defer C.free(unsafe.Pointer(cName6))
	obj6 := C.fwstate_test_map_object_new(agent, C.bool(true), cName6, C.uint64_t(stashSize))
	if obj6 == nil {
		panic("failed to create fwstate-map v6 object")
	}
	if rc := C.fwstate_test_register_object(agent, obj6); rc != 0 {
		panic("failed to register fwstate-map v6 object")
	}

	// Configure sync settings and link both map objects by name.
	// Multicast IPv6 address: ff02::1
	var syncCfg C.struct_fwstate_sync_config
	syncCfg.dst_ether.addr[0] = 0x33
	syncCfg.dst_ether.addr[1] = 0x33
	syncCfg.dst_ether.addr[5] = 0x01
	multicastAddr := [16]C.uint8_t{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	for idx := range 16 {
		syncCfg.dst_addr_multicast[idx] = multicastAddr[idx]
	}
	syncCfg.port_multicast = C.uint16_t(0x0f27) // 9999 in network byte order

	// Set timeouts (in nanoseconds)
	syncCfg.timeouts.tcp_syn_ack = C.uint64_t(120e9)
	syncCfg.timeouts.tcp_syn = C.uint64_t(120e9)
	syncCfg.timeouts.tcp_fin = C.uint64_t(120e9)
	syncCfg.timeouts.tcp = C.uint64_t(120e9)
	syncCfg.timeouts.udp = C.uint64_t(30e9)
	syncCfg.timeouts.default_ = C.uint64_t(16e9)

	var cErr *C.yanet_error
	cpModule := C.fwstate_module_config_new(
		agent,
		cName,
		&syncCfg,
		cName4,
		cName6,
		&cErr,
	)
	if cpModule == nil {
		panic(fmt.Sprintf("failed to initialize fwstate module config: %v", cerrors.FromC(unsafe.Pointer(cErr))))
	}
	C.fwstate_test_prepared_reset(cpModule)

	// Link the counter registry and spawn a per-worker counter storage once,
	// so counter values accumulate across fwstateHandlePackets calls.
	storage := C.fwstate_test_counter_storage_setup(cpModule)
	if storage == nil {
		panic("failed to spawn counter storage")
	}

	return cpModule, storage
}

// fwstateCounterStorageFree frees a counter storage returned by
// fwstateModuleConfig. Safe to call with nil.
func fwstateCounterStorageFree(storage *C.struct_counter_storage) {
	C.fwstate_test_counter_storage_free(storage)
}

// SetSyncSuppressTimeout sets the sync suppression window (nanoseconds) on a
// module config produced by fwstateModuleConfig. Zero disables suppression.
func SetSyncSuppressTimeout(cpModule *C.struct_cp_module, ns uint64) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.sync_suppress_timeout = C.uint64_t(ns)
}

// ClearSyncDestination leaves both emission endpoints disabled for a no-sync
// configuration.
func ClearSyncDestination(cpModule *C.struct_cp_module) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.port_multicast = 0
	m.sync_config.dst_addr_multicast = [16]C.uint8_t{}
	m.sync_config.port_unicast = 0
	m.sync_config.dst_addr_unicast = [16]C.uint8_t{}
}

// SetSyncUnicastDestination sets a unicast emission endpoint for a test.
func SetSyncUnicastDestination(cpModule *C.struct_cp_module) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.dst_addr_unicast = [16]C.uint8_t{
		0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3,
	}
	// 10000 in network byte order.
	m.sync_config.port_unicast = C.uint16_t(0x1027)
}

// SetSyncTCPTimeouts overrides the TCP established (tcp) and teardown
// (tcp_fin) timeouts so a test can distinguish an established refresh from a
// shorter-TTL state transition.
func SetSyncTCPTimeouts(cpModule *C.struct_cp_module, tcp, tcpFin uint64) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.timeouts.tcp = C.uint64_t(tcp)
	m.sync_config.timeouts.tcp_fin = C.uint64_t(tcpFin)
}

// SetSyncMTU sets the sync MTU on a module config produced by
// fwstateModuleConfig. Zero selects the default.
func SetSyncMTU(cpModule *C.struct_cp_module, mtu uint16) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.sync_mtu = C.uint16_t(mtu)
}

// localEvent is one stashed sync record: the frame bytes and the device
// ids ACL would capture from the original packet.
type localEvent struct {
	frame      []byte
	rxDeviceID uint16
	txDeviceID uint16
}

// roundResult is what one handler call left in the packet front, with the
// output device ids kept alongside the raw output.
type roundResult struct {
	dataplane.PacketFrontPayload
	OutputData []dataplane.PacketData
}

// testWorker returns the harness worker of the given index.
func testWorker(workerIdx uint16) *C.struct_dp_worker {
	worker := C.fwstate_test_worker(C.uint16_t(workerIdx))
	if worker == nil {
		panic(fmt.Sprintf("no harness worker %d", workerIdx))
	}
	return worker
}

// nextRound starts a new round on the harness worker.
func nextRound(worker *C.struct_dp_worker) {
	C.fwstate_test_next_round(worker)
}

// stashEvents appends pending records to the worker's stash slots in its
// current round and reports how many fit.
func stashEvents(cpModule *C.struct_cp_module, worker *C.struct_dp_worker, events ...localEvent) int {
	stashed := 0
	for _, event := range events {
		frame := (*C.struct_fw_state_sync_frame)(C.CBytes(event.frame))
		rc := C.fwstate_test_stash_push(
			cpModule, worker, frame,
			C.uint16_t(event.rxDeviceID), C.uint16_t(event.txDeviceID),
		)
		C.free(unsafe.Pointer(frame))
		if rc == 0 {
			stashed++
		}
	}
	return stashed
}

// runHandler dispatches the packets as ordinary input to the handler in
// the worker's current round, marking them as local fwstate emissions
// when flagged is set.
func runHandler(
	cpModule *C.struct_cp_module,
	storage *C.struct_counter_storage,
	worker *C.struct_dp_worker,
	flagged bool,
	packets ...gopacket.Packet,
) *roundResult {
	return runHandlerInContext(cpModule, storage, worker, 0, flagged, packets...)
}

// runHandlerInContext is runHandler through the given one of the config's
// stand-in execution contexts on the worker.
func runHandlerInContext(
	cpModule *C.struct_cp_module,
	storage *C.struct_counter_storage,
	worker *C.struct_dp_worker,
	context uint16,
	flagged bool,
	packets ...gopacket.Packet,
) *roundResult {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	var pf *dataplane.PacketFront
	if len(packets) == 0 {
		pf = dataplane.NewPacketFront(&pinner, nil, nil, nil)
	} else {
		var err error
		pf, err = dataplane.NewPacketFrontFromPackets(&pinner, packets...)
		if err != nil {
			panic(fmt.Sprintf("failed to create packet front: %v", err))
		}
	}

	cPacketFront := (*C.struct_packet_front)(unsafe.Pointer(pf))
	if flagged {
		C.fwstate_test_mark_internal(cPacketFront)
	}

	worker.current_time = C.clock_get_time_ns(nil)
	C.test_fwstate_handle_packets_in_context(worker, cpModule, storage, cPacketFront, C.uint16_t(context))
	return &roundResult{
		PacketFrontPayload: pf.Payload(),
		OutputData:         pf.OutputList().Data(),
	}
}

// syncFrameOf returns the first sync frame carried by a sync packet built
// with createSyncPacket.
func syncFrameOf(packet gopacket.Packet) []byte {
	udp, ok := packet.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if !ok {
		panic("sync packet carries no UDP layer")
	}
	return append([]byte(nil), udp.Payload[:C.sizeof_struct_fw_state_sync_frame]...)
}

// fwstateHandleLocalEvents starts a new round on worker 0, stashes the
// frame of every sync packet as ACL would, and runs the handler with no
// ordinary input.
func fwstateHandleLocalEvents(cpModule *C.struct_cp_module, storage *C.struct_counter_storage, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	worker := testWorker(0)
	nextRound(worker)
	events := make([]localEvent, 0, len(packets))
	for _, packet := range packets {
		events = append(events, localEvent{frame: syncFrameOf(packet)})
	}
	if stashed := stashEvents(cpModule, worker, events...); stashed != len(events) {
		return nil, fmt.Errorf("stashed %d of %d events", stashed, len(events))
	}
	result := runHandler(cpModule, storage, worker, false)
	return &result.PacketFrontPayload, nil
}

// fwstateHandleWirePackets dispatches packets as ordinary input in a new
// round on worker 0.
func fwstateHandleWirePackets(cpModule *C.struct_cp_module, storage *C.struct_counter_storage, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	worker := testWorker(0)
	nextRound(worker)
	result := runHandler(cpModule, storage, worker, false, packets...)
	return &result.PacketFrontPayload, nil
}

// fwstateHandleFlaggedPackets dispatches packets marked as emitted by an
// upstream fwstate in a new round on worker 0.
func fwstateHandleFlaggedPackets(cpModule *C.struct_cp_module, storage *C.struct_counter_storage, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	worker := testWorker(0)
	nextRound(worker)
	result := runHandler(cpModule, storage, worker, true, packets...)
	return &result.PacketFrontPayload, nil
}

// stashSlotCount reports the record count of the worker's stash slot for
// one family, or -1 without a linked map.
func stashSlotCount(cpModule *C.struct_cp_module, isIPv6 bool, workerIdx uint16) int {
	slot := C.fwstate_test_stash_slot(cpModule, C.bool(isIPv6), C.uint16_t(workerIdx))
	if slot == nil {
		return -1
	}
	return int(slot.count)
}

// stashRecordStatus reports the decision status of one record in the
// worker's stash slot for one family.
func stashRecordStatus(cpModule *C.struct_cp_module, isIPv6 bool, workerIdx uint16, idx int) uint8 {
	slot := C.fwstate_test_stash_slot(cpModule, C.bool(isIPv6), C.uint16_t(workerIdx))
	return uint8(C.fwstate_test_slot_record(slot, C.uint32_t(idx)).status)
}

// Record status values of the stash.
const (
	recordPending    = uint8(C.FWSTATE_SYNC_RECORD_PENDING)
	recordApplied    = uint8(C.FWSTATE_SYNC_RECORD_APPLIED)
	recordSuppressed = uint8(C.FWSTATE_SYNC_RECORD_SUPPRESSED)
)

// moduleCounter reads the first value of a module counter by name.
func moduleCounter(cpModule *C.struct_cp_module, storage *C.struct_counter_storage, name string) uint64 {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return uint64(C.fwstate_test_counter(cpModule, storage, cName, 0))
}

// fwstateTable resolves the module config's linked fwtable for one family,
// or nil when the family is unlinked. Resolution goes through the C harness,
// which reads the module's declared link and looks it up in the harness
// agent's object registry.
func fwstateTable(cpModule *C.struct_cp_module, isIPv6 bool) *C.fwtable_t {
	return C.fwstate_test_linked_table(cpModule, C.bool(isIPv6))
}

// fwstateKey builds the pinned C key for a 5-tuple. The pinner passed in must
// be unpinned by the caller.
func fwstateKey(pinner *runtime.Pinner, proto layers.IPProtocol, srcPort, dstPort uint16, srcIP, dstIP netip.Addr) unsafe.Pointer {
	if srcIP.Is6() && dstIP.Is6() {
		key6 := C.struct_fw6_state_key{}
		key6.hdr.proto = C.uint16_t(proto)
		key6.hdr.src_port = C.uint16_t(srcPort)
		key6.hdr.dst_port = C.uint16_t(dstPort)
		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		copy(unsafe.Slice((*byte)(&key6.src_addr[0]), 16), srcBytes[:])
		copy(unsafe.Slice((*byte)(&key6.dst_addr[0]), 16), dstBytes[:])
		pinner.Pin(&key6)
		return unsafe.Pointer(&key6)
	}

	key4 := C.struct_fw4_state_key{}
	key4.hdr.proto = C.uint16_t(proto)
	key4.hdr.src_port = C.uint16_t(srcPort)
	key4.hdr.dst_port = C.uint16_t(dstPort)
	srcBytes := srcIP.As4()
	dstBytes := dstIP.As4()
	copy(unsafe.Slice((*C.uint32_t)(&key4.src_addr), 1), []C.uint32_t{C.uint32_t(binary.LittleEndian.Uint32(srcBytes[:]))})
	copy(unsafe.Slice((*C.uint32_t)(&key4.dst_addr), 1), []C.uint32_t{C.uint32_t(binary.LittleEndian.Uint32(dstBytes[:]))})
	pinner.Pin(&key4)
	return unsafe.Pointer(&key4)
}

// SyncFrameOption is a functional option for createSyncFrame
type SyncFrameOption func(*C.struct_fw_state_sync_frame)

// WithFrameFlags sets raw flags byte in the sync frame.
// `flags` is a union exposed to cgo as [1]byte, so we write through it directly.
func WithFrameFlags(flags uint8) SyncFrameOption {
	return func(f *C.struct_fw_state_sync_frame) {
		*(*uint8)(unsafe.Pointer(&f.flags[0])) = flags
	}
}

// WithFrameFib sets fib (direction marker: 0 = forward, 1 = backward)
func WithFrameFib(fib uint8) SyncFrameOption {
	return func(f *C.struct_fw_state_sync_frame) {
		f.fib = C.uint8_t(fib)
	}
}

// createSyncFrame creates a properly formatted fw_state_sync_frame
func createSyncFrame(proto layers.IPProtocol, addrType uint8, srcPort uint16, dstPort uint16, dstIP6, srcIP6 []byte, opts ...SyncFrameOption) []byte {
	syncFrame := make([]byte, C.sizeof_struct_fw_state_sync_frame)

	// Use unsafe pointer to treat the byte slice as a C struct
	framePtr := (*C.struct_fw_state_sync_frame)(unsafe.Pointer(&syncFrame[0]))

	framePtr.proto = C.uint8_t(proto)
	framePtr.addr_type = C.uint8_t(addrType)
	framePtr.src_port = C.uint16_t(srcPort)
	framePtr.dst_port = C.uint16_t(dstPort)

	// Copy IPv6 addresses if provided
	if len(dstIP6) == 16 {
		for i := range 16 {
			framePtr.dst_ip6[i] = C.uint8_t(dstIP6[i])
		}
	}
	if len(srcIP6) == 16 {
		for i := range 16 {
			framePtr.src_ip6[i] = C.uint8_t(srcIP6[i])
		}
	}

	for _, opt := range opts {
		opt(framePtr)
	}

	return syncFrame
}

// StateValueSnapshot is a Go-side snapshot of a fw_state_value entry.
type StateValueSnapshot struct {
	Found           bool
	External        bool
	FlagsRaw        uint8
	CreatedAt       uint64
	UpdatedAt       uint64
	PacketsForward  uint64
	PacketsBackward uint64
	Deadline        uint64
}

// GetStateValue reads the raw fw_state_value for a given 5-tuple via a
// fwtable lookup across all layers. Returns Found=false if the state does
// not exist.
func GetStateValue(
	cpModule *C.struct_cp_module,
	proto layers.IPProtocol,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) StateValueSnapshot {
	srcIP, err1 := netip.ParseAddr(srcAddr)
	dstIP, err2 := netip.ParseAddr(dstAddr)
	if err1 != nil || err2 != nil {
		return StateValueSnapshot{}
	}

	table := fwstateTable(cpModule, srcIP.Is6() && dstIP.Is6())
	if table == nil {
		return StateValueSnapshot{}
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()
	keyPtr := fwstateKey(&pinner, proto, srcPort, dstPort, srcIP, dstIP)

	var value unsafe.Pointer
	var deadline C.uint64_t
	var valueFromStale C.bool
	// now=0 so the deadline check inside the lookup always passes
	ret := C.fwtable_lookup_with_deadline(table, 0, keyPtr, &value, nil, &deadline, &valueFromStale)
	if ret < 0 || value == nil {
		return StateValueSnapshot{Found: false}
	}

	v := (*C.struct_fw_state_value)(value)
	return StateValueSnapshot{
		Found:           true,
		External:        bool(v.external),
		FlagsRaw:        *(*uint8)(unsafe.Pointer(&v.flags[0])),
		CreatedAt:       uint64(v.created_at),
		UpdatedAt:       uint64(v.updated_at),
		PacketsForward:  uint64(v.packets_forward),
		PacketsBackward: uint64(v.packets_backward),
		Deadline:        uint64(deadline),
	}
}

// CheckStateExists checks if a state exists in the linked fwtable
func CheckStateExists(
	cpModule *C.struct_cp_module,
	proto layers.IPProtocol,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) bool {
	srcIP, err1 := netip.ParseAddr(srcAddr)
	dstIP, err2 := netip.ParseAddr(dstAddr)
	if err1 != nil || err2 != nil {
		return false
	}

	table := fwstateTable(cpModule, srcIP.Is6() && dstIP.Is6())
	if table == nil {
		return false
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()
	keyPtr := fwstateKey(&pinner, proto, srcPort, dstPort, srcIP, dstIP)

	// now=0 so the deadline check inside the lookup always passes
	var value unsafe.Pointer
	var valueFromStale C.bool
	ret := C.fwtable_lookup(table, 0, keyPtr, &value, nil, &valueFromStale)
	return ret >= 0
}

// InsertNewLayer appends a new layer to both linked map objects via the C
// harness helper.
func InsertNewLayer(cpModule *C.struct_cp_module) {
	rc := C.fwstate_test_insert_new_layer(cpModule)
	if rc != 0 {
		panic(fmt.Sprintf("failed to insert new layer: rc=%d", rc))
	}
}

// GetStateDeadline returns the deadline of a state entry
func GetStateDeadline(
	cpModule *C.struct_cp_module,
	proto layers.IPProtocol,
	srcPort uint16,
	dstPort uint16,
	srcAddr string,
	dstAddr string,
) uint64 {
	srcIP, err1 := netip.ParseAddr(srcAddr)
	dstIP, err2 := netip.ParseAddr(dstAddr)
	if err1 != nil || err2 != nil {
		return 0
	}

	table := fwstateTable(cpModule, srcIP.Is6() && dstIP.Is6())
	if table == nil {
		return 0
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()
	keyPtr := fwstateKey(&pinner, proto, srcPort, dstPort, srcIP, dstIP)

	var value unsafe.Pointer
	var deadline C.uint64_t
	var valueFromStale C.bool
	ret := C.fwtable_lookup_with_deadline(table, 0, keyPtr, &value, nil, &deadline, &valueFromStale)
	if ret < 0 {
		return 0
	}
	return uint64(deadline)
}

// GetLayerCount returns the number of layers in both IPv4 and IPv6 tables
func GetLayerCount(cpModule *C.struct_cp_module) (uint32, uint32) {
	fwmap4 := C.fwstate_test_table_layer(cpModule, C.bool(false), C.uint32_t(0))
	layerCount4 := uint32(C.fwmap_layer_count(fwmap4))

	fwmap6 := C.fwstate_test_table_layer(cpModule, C.bool(true), C.uint32_t(0))
	layerCount6 := uint32(C.fwmap_layer_count(fwmap6))

	return layerCount4, layerCount6
}

// GetCurrentTime returns current time in nanoseconds
func GetCurrentTime() uint64 {
	return uint64(C.clock_get_time_ns(nil))
}

// TrimStaleLayers trims stale layers from both linked map objects.
func TrimStaleLayers(cpModule *C.struct_cp_module, now uint64) error {
	rc := C.fwstate_test_trim_stale_layers(cpModule, C.uint64_t(now))
	if rc != 0 {
		return fmt.Errorf("failed to trim stale layers: rc=%d", rc)
	}
	return nil
}

// fwstateSiblingModuleConfig creates a second module config on the agent of
// cpModule, linked to the same two map objects and emitting only to the
// unicast endpoint [2001:db8::3]:10000, with its own counter storage.
func fwstateSiblingModuleConfig(cpModule *C.struct_cp_module, name string) (*C.struct_cp_module, *C.struct_counter_storage) {
	agent := (*C.struct_agent)(C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&cpModule.agent))))

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	cName4 := C.CString("fw4")
	defer C.free(unsafe.Pointer(cName4))
	cName6 := C.CString("fw6")
	defer C.free(unsafe.Pointer(cName6))

	source := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	syncCfg := source.sync_config

	var cErr *C.yanet_error
	sibling := C.fwstate_module_config_new(
		agent,
		cName,
		&syncCfg,
		cName4,
		cName6,
		&cErr,
	)
	if sibling == nil {
		panic(fmt.Sprintf("failed to initialize sibling fwstate module config: %v", cerrors.FromC(unsafe.Pointer(cErr))))
	}
	C.fwstate_test_prepared_reset(sibling)
	ClearSyncDestination(sibling)
	SetSyncUnicastDestination(sibling)

	storage := C.fwstate_test_counter_storage_setup(sibling)
	if storage == nil {
		panic("failed to spawn sibling counter storage")
	}
	return sibling, storage
}

// v6Event builds a stashed IPv6 TCP event from 2001:db8::1 to 2001:db8::2
// whose source port tells events apart.
func v6Event(srcPort uint16, rxDeviceID, txDeviceID uint16) localEvent {
	return localEvent{
		frame: createSyncFrame(
			layers.IPProtocolTCP, 6, srcPort, 80,
			net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1"),
		),
		rxDeviceID: rxDeviceID,
		txDeviceID: txDeviceID,
	}
}

// v4Event builds a stashed IPv4 UDP event whose source port tells events
// apart.
func v4Event(srcPort uint16) localEvent {
	return localEvent{frame: createSyncFrame(layers.IPProtocolUDP, 4, srcPort, 53, nil, nil)}
}

// syncPayload returns the destination and UDP payload of an emitted sync
// packet.
func syncPayload(raw []byte) (net.IP, uint16, []byte) {
	packet := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
	ip6, ok := packet.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	if !ok {
		panic("emitted sync packet carries no IPv6 layer")
	}
	udp, ok := packet.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if !ok {
		panic("emitted sync packet carries no UDP layer")
	}
	return ip6.DstIP, uint16(udp.DstPort), append([]byte(nil), udp.Payload...)
}

// frameSize is the wire size of one sync frame.
const frameSize = int(C.sizeof_struct_fw_state_sync_frame)

// stashDefaultSize is the stash bytes each harness map has per worker.
const stashDefaultSize = int(C.FWSTATE_STASH_DEFAULT_SIZE)

// syncRecordSize is the size of one stashed record.
const syncRecordSize = int(C.sizeof_struct_fwstate_sync_record)

// stashSlotAddr returns the address of the worker's stash slot for one
// family.
func stashSlotAddr(cpModule *C.struct_cp_module, isIPv6 bool, workerIdx uint16) uintptr {
	return uintptr(unsafe.Pointer(C.fwstate_test_stash_slot(cpModule, C.bool(isIPv6), C.uint16_t(workerIdx))))
}

// stashRecordsAddr returns the address of the worker's records array for
// one family.
func stashRecordsAddr(cpModule *C.struct_cp_module, isIPv6 bool, workerIdx uint16) uintptr {
	slot := C.fwstate_test_stash_slot(cpModule, C.bool(isIPv6), C.uint16_t(workerIdx))
	return uintptr(unsafe.Pointer(C.fwstate_test_slot_record(slot, 0)))
}

// poolOutstanding reports how many mock-pool mbufs the harness workers
// hold outside the pool.
func poolOutstanding(worker *C.struct_dp_worker) uint64 {
	return uint64(C.fwstate_test_pool_outstanding(worker))
}

// limitPool lets the harness mock pool hand out only extra more mbufs than
// it has outstanding now, until unlimitPool restores the default.
func limitPool(worker *C.struct_dp_worker, extra uint32) {
	C.fwstate_test_pool_limit(worker, C.uint32_t(extra))
}

// unlimitPool restores the default capacity of the harness mock pool.
func unlimitPool(worker *C.struct_dp_worker) {
	C.fwstate_test_pool_unlimit(worker)
}
