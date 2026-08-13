package fwstate

//#cgo CFLAGS: -I../../../.. -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../build/objects/fwstate/api -lfwstate_objects
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/fwstate -lfwstate
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
/*
#include <harness.h>
*/
import "C"
import (
	"encoding/binary"
	"fmt"
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
	// Allocate the stand-in agent through the harness constructor, which zeroes
	// the whole structure, not just the memory context, and wires the object
	// registry the harness's own link-name lookups resolve against.
	//
	// Every module construction now walks the agent's parked-list head, and an
	// unzeroed head reads as stale bytes rather than a valid empty list.
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
	obj4 := C.fwstate_test_map_object_new(agent, C.bool(false), cName4)
	if obj4 == nil {
		panic("failed to create fwstate-map v4 object")
	}
	if rc := C.fwstate_test_register_object(agent, obj4); rc != 0 {
		panic("failed to register fwstate-map v4 object")
	}

	cName6 := C.CString("fw6")
	defer C.free(unsafe.Pointer(cName6))
	obj6 := C.fwstate_test_map_object_new(agent, C.bool(true), cName6)
	if obj6 == nil {
		panic("failed to create fwstate-map v6 object")
	}
	if rc := C.fwstate_test_register_object(agent, obj6); rc != 0 {
		panic("failed to register fwstate-map v6 object")
	}

	// Configure sync settings and link both map objects by name.
	// Multicast IPv6 address: ff02::1
	var syncCfg C.struct_fwstate_sync_config
	multicastAddr := [16]C.uint8_t{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	for i := range 16 {
		syncCfg.dst_addr_multicast[i] = multicastAddr[i]
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

// SetSyncTCPTimeouts overrides the TCP established (tcp) and teardown
// (tcp_fin) timeouts so a test can distinguish an established refresh from a
// shorter-TTL state transition.
func SetSyncTCPTimeouts(cpModule *C.struct_cp_module, tcp, tcpFin uint64) {
	m := (*C.struct_fwstate_module_config)(unsafe.Pointer(cpModule))
	m.sync_config.timeouts.tcp = C.uint64_t(tcp)
	m.sync_config.timeouts.tcp_fin = C.uint64_t(tcpFin)
}

func fwstateHandlePackets(cpModule *C.struct_cp_module, storage *C.struct_counter_storage, packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}

	// Create a dummy dp_worker
	dpWorker := &C.struct_dp_worker{
		idx:          0,
		current_time: C.clock_get_time_ns(nil),
	}
	C.test_fwstate_handle_packets(dpWorker, cpModule, storage, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := pf.Payload()
	return &result, nil
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
