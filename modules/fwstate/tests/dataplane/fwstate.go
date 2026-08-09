package fwstate

//#cgo CFLAGS: -I../../../.. -I../../../../lib -I../../../../common
//#cgo LDFLAGS: -Wl,--allow-multiple-definition
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/objects -lfwstate_objects
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/fwstate -lfwstate
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
/*
#include <harness.h>

#include "common/container_of.h"
#include "common/memory.h"
#include "lib/fwstate/fwtable.h"
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/objects/fwstate_map_object.h"

// fwstate_test_config_head_v4 resolves the v4 head fwmap from a standalone
// fwstate-map object's table.
static inline fwmap_t *
fwstate_test_head_from_object(struct cp_object *cp_object) {
	fwtable_t *table = fwstate_map_object_table(cp_object);
	return ADDR_OF(&table->head);
}

// fwstate_map_from_cp_object upcasts a cp_object pointer to its enclosing
// fwstate_map_object. cp_object is the first field, so the cast preserves
// the address.
static inline struct fwstate_map_object *
fwstate_map_from_cp_object(struct cp_object *cp_object) {
	return (struct fwstate_map_object *)cp_object;
}

// Mock cp_object_init for tests: mirrors harness.c's cp_module_init mock
// by skipping dp_config_lookup_object (the stub agent has no dp_config).
// Sets dp_object_idx=0 so object_link_get_address can resolve links.
int
cp_object_init(
	struct cp_object *self,
	struct agent *agent,
	const char *object_type,
	const char *name,
	yanet_error **err
) {
	(void)err;
	memset(self, 0, sizeof(struct cp_object));
	self->dp_object_idx = 0;
	strtcpy(self->type, object_type, sizeof(self->type));
	strtcpy(self->name, name, sizeof(self->name));
	memory_context_init_from(
		&self->memory_context, &agent->memory_context, name
	);
	registry_item_init(&self->config_item);
	SET_OFFSET_OF(&self->agent, agent);
	if (counter_registry_init(
		    &self->counter_registry, &self->memory_context, 0
	    )) {
		return -1;
	}
	if (counter_registry_init(
		    &self->link_counter_registry, &self->memory_context, 0
	    )) {
		return -1;
	}
	return 0;
}

// Mock cp_object_fini for tests: mirrors harness.c's cp_module_fini mock.
void
cp_object_fini(struct cp_object *self) {
	counter_registry_fini(&self->link_counter_registry);
	counter_registry_fini(&self->counter_registry);
}

// fwstate_test_handle_packets_with_objects wires a minimal module_ectx
// with cp_module, counter_storage, and two object_links referencing the v4
// and v6 cp_objects, then invokes fwstate_handle_packets.
//
// The v4 link must be at index 0 and the v6 link at index 1, matching the
// order fwstate_module_config_set declares them (v4 first, v6 second).
//
// On-stack ectx plus heap-allocated link storage: the relative-pointer
// math (SET_OFFSET_OF / ADDR_OF) is valid across stack and heap within a
// single address space. The link storage is heap-allocated to outlive the
// ectx setup; the caller frees it via fwstate_test_free_object_links.
static inline void
fwstate_test_handle_packets_with_objects(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct cp_object *v4_object,
	struct cp_object *v6_object,
	struct packet_front *packet_front
) {
	struct object_ectx *oe_v4 =
		(struct object_ectx *)calloc(1, sizeof(struct object_ectx));
	struct object_ectx *oe_v6 =
		(struct object_ectx *)calloc(1, sizeof(struct object_ectx));
	SET_OFFSET_OF(&oe_v4->cp_object, v4_object);
	SET_OFFSET_OF(&oe_v6->cp_object, v6_object);

	struct module_object_link_ectx *links =
		(struct module_object_link_ectx *)calloc(
			2, sizeof(struct module_object_link_ectx)
		);
	SET_OFFSET_OF(&links[0].object_ectx, oe_v4);
	SET_OFFSET_OF(&links[1].object_ectx, oe_v6);

	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	SET_OFFSET_OF(&module_ectx.counter_storage, counter_storage);
	module_ectx.object_link_count = 2;
	SET_OFFSET_OF(&module_ectx.object_links, links);

	fwstate_handle_packets(dp_worker, &module_ectx, packet_front);

	free(links);
	free(oe_v4);
	free(oe_v6);
}
*/
import "C"

import (
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

// fwstateTestModule bundles a fwstate sync config with the two standalone
// named fwstate-map objects (one v4, one v6) that own its fwtables.
//
// The sync config links the objects by name via fwstate_module_config_set.
// The objects themselves are owned and freed solely by mapObjectV4 /
// mapObjectV6. Freeing the sync config never touches fwtable memory.
type fwstateTestModule struct {
	syncConfig  *C.struct_cp_module
	mapObjectV4 *C.struct_cp_object
	mapObjectV6 *C.struct_cp_object
	storage     *C.struct_counter_storage
}

// fwstateModuleConfig creates two standalone named fwstate-map objects (one
// v4, one v6) with 1024-entry tables, then creates a fwstate sync config
// that references them by name, applies the default sync settings, and
// spawns a per-worker counter storage.
//
// The returned storage is reused across HandlePackets calls so counter
// values accumulate. The caller must call Free to release all four
// objects (storage, sync config, v4 map, v6 map).
func fwstateModuleConfig(memCtx testutils.MemoryContext) *fwstateTestModule {
	agent := (*C.struct_agent)(C.memory_balloc(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		C.sizeof_struct_agent,
	))
	if agent == nil {
		panic("failed to allocate agent")
	}

	cStubAgent := C.CString("stub agent")
	defer C.free(unsafe.Pointer(cStubAgent))

	C.memory_context_init_from(
		&agent.memory_context,
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		cStubAgent,
	)

	cTypeV4 := C.CString(C.FWSTATE_MAP_V4_OBJECT_TYPE)
	defer C.free(unsafe.Pointer(cTypeV4))
	cMapNameV4 := C.CString("test-map-v4")
	defer C.free(unsafe.Pointer(cMapNameV4))

	var cMapErrV4 *C.yanet_error
	mapObjectV4 := C.fwstate_map_object_config_new(
		agent, cTypeV4, cMapNameV4, C.FWTABLE_KIND_V4, &cMapErrV4,
	)
	if mapObjectV4 == nil {
		panic(fmt.Sprintf("failed to initialize fwstate-map v4 object: %v", cerrors.FromC(unsafe.Pointer(cMapErrV4))))
	}

	rc := C.fwstate_map_object_create_map(
		C.fwstate_map_from_cp_object(mapObjectV4),
		C.uint32_t(1024),
		C.uint32_t(64),
		C.uint16_t(1),
	)
	if rc != 0 {
		panic(fmt.Sprintf("failed to create fwstate-map v4 table: rc=%d", rc))
	}

	cTypeV6 := C.CString(C.FWSTATE_MAP_V6_OBJECT_TYPE)
	defer C.free(unsafe.Pointer(cTypeV6))
	cMapNameV6 := C.CString("test-map-v6")
	defer C.free(unsafe.Pointer(cMapNameV6))

	var cMapErrV6 *C.yanet_error
	mapObjectV6 := C.fwstate_map_object_config_new(
		agent, cTypeV6, cMapNameV6, C.FWTABLE_KIND_V6, &cMapErrV6,
	)
	if mapObjectV6 == nil {
		panic(fmt.Sprintf("failed to initialize fwstate-map v6 object: %v", cerrors.FromC(unsafe.Pointer(cMapErrV6))))
	}

	rc = C.fwstate_map_object_create_map(
		C.fwstate_map_from_cp_object(mapObjectV6),
		C.uint32_t(1024),
		C.uint32_t(64),
		C.uint16_t(1),
	)
	if rc != 0 {
		panic(fmt.Sprintf("failed to create fwstate-map v6 table: rc=%d", rc))
	}

	cName := C.CString("test")
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	syncModule := C.fwstate_module_config_new(agent, cName, &cErr)
	if syncModule == nil {
		panic(fmt.Sprintf("failed to initialize fwstate module config: %v", cerrors.FromC(unsafe.Pointer(cErr))))
	}

	var cSync C.struct_fwstate_sync_config
	multicastAddr := [16]C.uint8_t{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	for i := range 16 {
		cSync.dst_addr_multicast[i] = multicastAddr[i]
	}
	cSync.port_multicast = C.uint16_t(0x0f27) // 9999 in network byte order
	cSync.timeouts.tcp_syn_ack = C.uint64_t(120e9)
	cSync.timeouts.tcp_syn = C.uint64_t(120e9)
	cSync.timeouts.tcp_fin = C.uint64_t(120e9)
	cSync.timeouts.tcp = C.uint64_t(120e9)
	cSync.timeouts.udp = C.uint64_t(30e9)
	cSync.timeouts.default_ = C.uint64_t(16e9)

	cFw4Name := C.CString("test-map-v4")
	defer C.free(unsafe.Pointer(cFw4Name))
	cFw6Name := C.CString("test-map-v6")
	defer C.free(unsafe.Pointer(cFw6Name))

	var cSetErr *C.yanet_error
	if C.fwstate_module_config_set(syncModule, cFw4Name, cFw6Name, &cSync, &cSetErr) != 0 {
		panic(fmt.Sprintf("failed to set fwstate module config: %v", cerrors.FromC(unsafe.Pointer(cSetErr))))
	}

	storage := C.fwstate_test_counter_storage_setup(syncModule)
	if storage == nil {
		panic("failed to spawn counter storage")
	}

	return &fwstateTestModule{
		syncConfig:  syncModule,
		mapObjectV4: mapObjectV4,
		mapObjectV6: mapObjectV6,
		storage:     storage,
	}
}

// SyncConfig returns the fwstate sync config cp_module pointer, used by
// state-inspection helpers (GetStateValue, CheckStateExists, etc.).
func (m *fwstateTestModule) SyncConfig() *C.struct_cp_module {
	return m.syncConfig
}

// MapObjectV4 returns the v4 fwstate-map object pointer.
func (m *fwstateTestModule) MapObjectV4() *C.struct_cp_object {
	return m.mapObjectV4
}

// MapObjectV6 returns the v6 fwstate-map object pointer.
func (m *fwstateTestModule) MapObjectV6() *C.struct_cp_object {
	return m.mapObjectV6
}

// HandlePackets runs the fwstate dataplane handler over the given packets
// using the sync config, the map objects, and its pre-spawned counter
// storage.
func (m *fwstateTestModule) HandlePackets(packets ...gopacket.Packet) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}

	dpWorker := &C.struct_dp_worker{
		idx:          0,
		current_time: C.clock_get_time_ns(nil),
	}
	C.fwstate_test_handle_packets_with_objects(
		dpWorker, m.syncConfig, m.storage,
		m.mapObjectV4, m.mapObjectV6,
		(*C.struct_packet_front)(unsafe.Pointer(pf)),
	)
	result := pf.Payload()
	return &result, nil
}

// InsertNewLayer inserts a new layer into both the v4 and v6 fwtable
// chains of the standalone fwstate-maps.
func (m *fwstateTestModule) InsertNewLayer() {
	for _, mapObject := range []*C.struct_cp_object{m.mapObjectV4, m.mapObjectV6} {
		rc := C.fwstate_map_object_insert_layer(
			C.fwstate_map_from_cp_object(mapObject),
			C.uint32_t(1024),
			C.uint32_t(64),
			C.uint16_t(1),
		)
		if rc != 0 {
			panic(fmt.Sprintf("failed to insert new layer: rc=%d", rc))
		}
	}
}

// TrimStaleLayers trims stale layers from both the v4 and v6 fwtable
// chains of the standalone fwstate-maps.
//
// Trimmed layers are tracked in the fwtable stale chain and freed on the
// next trim call, so this function has nothing to free.
func (m *fwstateTestModule) TrimStaleLayers(now uint64) {
	for _, mapObject := range []*C.struct_cp_object{m.mapObjectV4, m.mapObjectV6} {
		rc := C.fwstate_map_object_trim_stale_layers(
			C.fwstate_map_from_cp_object(mapObject),
			C.uint64_t(now),
		)
		if rc != 0 {
			panic(fmt.Sprintf("failed to trim stale layers: rc=%d", rc))
		}
	}
}

// Free releases the counter storage, the sync config, and both standalone
// named fwstate-map objects that own the fwtables.
func (m *fwstateTestModule) Free() {
	if m.storage != nil {
		C.fwstate_test_counter_storage_free(m.storage)
		m.storage = nil
	}
	if m.syncConfig != nil {
		C.fwstate_module_config_free(m.syncConfig)
		m.syncConfig = nil
	}
	if m.mapObjectV4 != nil {
		C.fwstate_map_object_config_free(m.mapObjectV4)
		m.mapObjectV4 = nil
	}
	if m.mapObjectV6 != nil {
		C.fwstate_map_object_config_free(m.mapObjectV6)
		m.mapObjectV6 = nil
	}
}

// SyncFrameOption is a functional option for createSyncFrame
type SyncFrameOption func(*C.struct_fw_state_sync_frame)

// WithFrameFlags sets raw flags byte in the sync frame.
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

	framePtr := (*C.struct_fw_state_sync_frame)(unsafe.Pointer(&syncFrame[0]))

	framePtr.proto = C.uint8_t(proto)
	framePtr.addr_type = C.uint8_t(addrType)
	framePtr.src_port = C.uint16_t(srcPort)
	framePtr.dst_port = C.uint16_t(dstPort)

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

// resolveHead resolves the head fwmap from the appropriate map object for
// the given address family.
func resolveHead(mapObject *C.struct_cp_object) *C.fwmap_t {
	if mapObject == nil {
		return nil
	}
	return C.fwstate_test_head_from_object(mapObject)
}

// GetStateValue reads the raw fw_state_value for a given 5-tuple via layermap_get_value_and_deadline.
// Returns Found=false if the state does not exist.
func GetStateValue(
	mapObject *C.struct_cp_object,
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

	var fwmap *C.fwmap_t
	var keyPtr unsafe.Pointer

	var pinner runtime.Pinner
	defer pinner.Unpin()

	if srcIP.Is6() && dstIP.Is6() {
		fwmap = resolveHead(mapObject)
		key6 := C.struct_fw6_state_key{}
		key6.hdr.proto = C.uint16_t(proto)
		key6.hdr.src_port = C.uint16_t(srcPort)
		key6.hdr.dst_port = C.uint16_t(dstPort)
		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		copy(unsafe.Slice((*byte)(&key6.src_addr[0]), 16), srcBytes[:])
		copy(unsafe.Slice((*byte)(&key6.dst_addr[0]), 16), dstBytes[:])
		pinner.Pin(&key6)
		keyPtr = unsafe.Pointer(&key6)
	} else {
		fwmap = resolveHead(mapObject)
		key4 := C.struct_fw4_state_key{}
		key4.hdr.proto = C.uint16_t(proto)
		key4.hdr.src_port = C.uint16_t(srcPort)
		key4.hdr.dst_port = C.uint16_t(dstPort)
		srcBytes := srcIP.As4()
		dstBytes := dstIP.As4()
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&key4.src_addr)), 4), srcBytes[:])
		copy(unsafe.Slice((*byte)(unsafe.Pointer(&key4.dst_addr)), 4), dstBytes[:])
		pinner.Pin(&key4)
		keyPtr = unsafe.Pointer(&key4)
	}

	var value unsafe.Pointer
	var deadline C.uint64_t
	var valueFromStale C.bool
	ret := C.layermap_get_value_and_deadline(fwmap, 0, keyPtr, &value, nil, &deadline, &valueFromStale)
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

// CheckStateExists checks if a state exists in the fwmap
func CheckStateExists(
	mapObject *C.struct_cp_object,
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

	var fwmap *C.fwmap_t
	var keyPtr unsafe.Pointer

	var pinner runtime.Pinner
	defer pinner.Unpin()

	if srcIP.Is6() && dstIP.Is6() {
		fwmap = resolveHead(mapObject)
		key6 := C.struct_fw6_state_key{}

		key6.hdr.proto = C.uint16_t(proto)
		key6.hdr.src_port = C.uint16_t(srcPort)
		key6.hdr.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		srcAddrSlice := unsafe.Slice((*byte)(&key6.src_addr[0]), 16)
		dstAddrSlice := unsafe.Slice((*byte)(&key6.dst_addr[0]), 16)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key6)
		keyPtr = unsafe.Pointer(&key6)

	} else {
		fwmap = resolveHead(mapObject)
		key4 := C.struct_fw4_state_key{}

		key4.hdr.proto = C.uint16_t(proto)
		key4.hdr.src_port = C.uint16_t(srcPort)
		key4.hdr.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As4()
		dstBytes := dstIP.As4()
		srcAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.src_addr)), 4)
		dstAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.dst_addr)), 4)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key4)
		keyPtr = unsafe.Pointer(&key4)
	}

	var value unsafe.Pointer
	now := C.uint64_t(0)
	var valueFromStale C.bool
	ret := C.layermap_get(fwmap, now, keyPtr, &value, nil, &valueFromStale)
	return ret >= 0
}

// GetStateDeadline returns the deadline of a state entry
func GetStateDeadline(
	mapObject *C.struct_cp_object,
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

	var fwmap *C.fwmap_t
	var keyPtr unsafe.Pointer

	var pinner runtime.Pinner
	defer pinner.Unpin()

	if srcIP.Is6() && dstIP.Is6() {
		fwmap = resolveHead(mapObject)
		key6 := C.struct_fw6_state_key{}
		key6.hdr.proto = C.uint16_t(proto)
		key6.hdr.src_port = C.uint16_t(srcPort)
		key6.hdr.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As16()
		dstBytes := dstIP.As16()
		srcAddrSlice := unsafe.Slice((*byte)(&key6.src_addr[0]), 16)
		dstAddrSlice := unsafe.Slice((*byte)(&key6.dst_addr[0]), 16)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key6)
		keyPtr = unsafe.Pointer(&key6)
	} else {
		fwmap = resolveHead(mapObject)
		key4 := C.struct_fw4_state_key{}
		key4.hdr.proto = C.uint16_t(proto)
		key4.hdr.src_port = C.uint16_t(srcPort)
		key4.hdr.dst_port = C.uint16_t(dstPort)

		srcBytes := srcIP.As4()
		dstBytes := dstIP.As4()
		srcAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.src_addr)), 4)
		dstAddrSlice := unsafe.Slice((*byte)(unsafe.Pointer(&key4.dst_addr)), 4)
		copy(srcAddrSlice, srcBytes[:])
		copy(dstAddrSlice, dstBytes[:])

		pinner.Pin(&key4)
		keyPtr = unsafe.Pointer(&key4)
	}

	var value unsafe.Pointer
	var deadline C.uint64_t
	var valueFromStale C.bool
	now := C.uint64_t(C.clock_get_time_ns(nil))
	ret := C.layermap_get_value_and_deadline(fwmap, now*0, keyPtr, &value, nil, &deadline, &valueFromStale)
	if ret < 0 {
		return 0
	}
	return uint64(deadline)
}

// GetLayerCount returns the number of layers in the fwtable owned by the
// given map object.
func GetLayerCount(mapObject *C.struct_cp_object) uint32 {
	fwmap := resolveHead(mapObject)
	return uint32(C.fwmap_layer_count(fwmap))
}

// GetCurrentTime returns current time in nanoseconds
func GetCurrentTime() uint64 {
	return uint64(C.clock_get_time_ns(nil))
}
