package nat64_test

//#cgo CFLAGS: -I../../../.. -I../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/nat64/dataplane -lnat64_dp
//#cgo LDFLAGS: -L../../../../build/modules/nat64/api -lnat64_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
/*
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "modules/nat64/api/nat64cp.h"
#include "modules/nat64/dataplane/config.h"
#include "lib/dataplane/pipeline/econtext.h"

// cp_module_init and cp_module_fini stand in for the real implementations,
// whose dependency chain pulls in registries this harness has no use for.
//
// These symbols must resolve at link time because the module's create/free
// entry points reference them, though the tests below never call those
// entry points. cp_module_init deliberately returns failure instead of a
// no-op body, so a future caller that does reach it fails loudly instead of
// continuing on an uninitialized module.
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
) {
	(void)cp_module, (void)agent, (void)module_type, (void)module_name,
		(void)err;
	return -1;
}

void
cp_module_fini(struct cp_module *cp_module) {
	(void)cp_module;
}

// cp_module_try_destroy is no longer header-only, so this harness supplies
// a stand-in. The stub agent above has no real configuration zone, so this
// mock reproduces the production check without the config lock the real
// implementation takes around it.
int
cp_module_try_destroy(struct cp_module *cp_module, yanet_error **err) {
	(void)err;
	if (cp_module->config_item.refcnt != 0) {
		errno = EAGAIN;
		return -1;
	}
	return 0;
}

// nat64dp.c registers an RTE log type at load time and logs through the
// DPDK EAL, which this harness does not link. Stand in with no-op bodies.
int
rte_log_register_type_and_pick_level(const char *name, uint32_t level_def) {
	(void)name, (void)level_def;
	return 0;
}

int
rte_log_set_level(uint32_t logtype, uint32_t level) {
	(void)logtype, (void)level;
	return 0;
}

int
rte_log(uint32_t level, uint32_t logtype, const char *format, ...) {
	(void)level, (void)logtype, (void)format;
	return 0;
}

void
nat64_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

void
test_nat64_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	nat64_handle_packets(dp_worker, &module_ectx, packet_front);
}
*/
import "C"
import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/testutils"
)

// nat64Mapping is one IPv4-IPv6 address mapping to seed into a test module
// config.
type nat64Mapping struct {
	IP4 uint32
	IP6 [16]byte
}

// nat64ModuleConfig builds a nat64 module config backed by memCtx, with a
// single 96-bit prefix and the given mappings all bound to it.
func nat64ModuleConfig(prefix [12]byte, mappings []nat64Mapping, memCtx testutils.MemoryContext) *C.struct_nat64_module_config {
	// Allocated from memCtx rather than the Go heap: the config's LPM and
	// array fields hold offsets relative to its own address, so it must
	// live at the address the memory context's own allocator gave it.
	m := (*C.struct_nat64_module_config)(C.memory_balloc(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		C.sizeof_struct_nat64_module_config,
	))
	if m == nil {
		panic("failed to allocate nat64 module config")
	}
	// The memory_balloc function does not zero its block. The cp_module_init
	// call is stubbed to fail above, so nothing else zeroes the cp_module fields
	// this test harness never sets.
	C.memset(unsafe.Pointer(m), 0, C.sizeof_struct_nat64_module_config)

	cName := C.CString("nat64_test")
	defer C.free(unsafe.Pointer(cName))
	C.memory_context_init_from(
		&m.cp_module.memory_context,
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		cName,
	)

	if C.nat64_module_config_data_init(m, &m.cp_module.memory_context) != 0 {
		panic("failed to init nat64 module config data")
	}

	cPrefix := (*C.uint8_t)(unsafe.Pointer(&prefix[0]))
	if C.nat64_module_config_add_prefix(&m.cp_module, cPrefix) < 0 {
		panic("failed to add nat64 prefix")
	}

	for idx := range mappings {
		cIP6 := (*C.uint8_t)(unsafe.Pointer(&mappings[idx].IP6[0]))
		if C.nat64_module_config_add_mapping(&m.cp_module, C.uint32_t(mappings[idx].IP4), cIP6, 0) < 0 {
			panic(fmt.Sprintf("failed to add nat64 mapping at index %d", idx))
		}
	}

	return m
}

// nat64HandlePackets runs the nat64 dataplane handler over payload against
// mc and returns the resulting output/input/drop lists.
func nat64HandlePackets(t *testing.T, mc *C.struct_nat64_module_config, payload [][]byte) dataplane.PacketFrontPayload {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPayload(&pinner, payload)
	require.NoError(t, err, "failed to create packet front")
	C.test_nat64_handle_packets(nil, &mc.cp_module, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	return pf.Payload()
}

// nat64MalformedPackets returns the module's cumulative count of packets
// dropped for a malformed embedded ICMP error payload.
func nat64MalformedPackets(mc *C.struct_nat64_module_config) uint64 {
	return uint64(mc.stats.malformed_packets)
}
