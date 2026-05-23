package route_test

//#cgo CFLAGS: -I../../../../ -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/lib/testutils/cp -ltestutils_cp
//#cgo LDFLAGS: -L../../../../build/modules/route/api -lroute_cp
//#cgo LDFLAGS: -L../../../../build/modules/route/dataplane -lroute_dp
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/pipeline -lpipeline
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/module -lmodule
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
//#cgo LDFLAGS: -Wl,--export-dynamic -ldl
/*
#include <stdlib.h>
#include "dataplane/module/module.h"
#include "dataplane/pipeline/econtext.h"
#include "lib/dataplane/module/packet_front.h"

struct module *new_module_route(void);

static void
run_route(
	struct cp_module *cp_module, uint64_t *mc_index, uint64_t mc_index_size,
	struct packet_front *pf, const uint32_t *hashes, uint64_t packet_count
) {
	if (hashes != NULL) {
		struct packet *p = pf->input.first;
		for (uint64_t idx = 0; idx < packet_count && p != NULL; idx++) {
			p->hash = hashes[idx];
			p = p->next;
		}
	}
	struct module *mod = new_module_route();
	struct module_ectx ectx = {};
	SET_OFFSET_OF(&ectx.cp_module, cp_module);
	SET_OFFSET_OF(&ectx.mc_index, mc_index);
	ectx.mc_index_size = mc_index_size;
	mod->handler(NULL, &ectx, pf);
	free(mod);
	packet_list_concat(&pf->output, &pf->pending_output);
	packet_list_init(&pf->pending_output);
}
*/
import "C"
import (
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/dataplane"
	testcp "github.com/yanet-platform/yanet2/common/go/testutils/cp"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

const (
	routeCPSize  = 16 * 1024 * 1024
	routeDPSize  = 4 * 1024 * 1024
	routeMemSize = 2 * 1024 * 1024
)

// FIBNexthop is a test-domain nexthop descriptor.
//
// Device is the interface name passed on the wire. Registration order
// determines the mc_index slot: the first unique device gets slot 1, the
// second gets slot 2, and so on (slot 0 is the empty-name sentinel).
type FIBNexthop struct {
	DstMAC net.HardwareAddr
	SrcMAC net.HardwareAddr
	Device string
}

// FIBEntry is a test-domain FIB prefix with associated nexthops.
type FIBEntry struct {
	Prefix   netip.Prefix
	Nexthops []FIBNexthop
}

// setupRouteBackend stands up the Go controlplane backend against an
// in-process test shm.
//
// Returns the backend. All shm and agent resources are cleaned up via
// t.Cleanup in LIFO order (shm.Close runs last).
func setupRouteBackend(t *testing.T) route.Backend {
	t.Helper()

	shm, err := testcp.NewSHM(routeCPSize, routeDPSize, []string{"route"})
	require.NoError(t, err)
	// Register Close first so it executes last (LIFO teardown).
	t.Cleanup(func() { _ = shm.Close() })

	ffiShm := ffi.NewSharedMemoryFromRaw(shm.RawPtr())
	// No Detach in cleanup: ffi.SharedMemory.Detach calls yanet_shm_detach
	// which expects a process-level shm fd. The in-process arena is owned
	// by shm.Close above.

	agent, err := ffiShm.AgentAttach("route", 0, routeMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return route.NewBackend(agent)
}

// applyFIB pushes the given entries via the backend and returns the cp_module
// pointer for the named config, ready for routeHandlePackets.
func applyFIB(t *testing.T, backend route.Backend, name string, entries []FIBEntry) unsafe.Pointer {
	t.Helper()

	pbEntries := make([]*routepb.FIBEntry, 0, len(entries))
	for _, e := range entries {
		nexthops := make([]*routepb.FIBNexthop, 0, len(e.Nexthops))
		for _, nh := range e.Nexthops {
			nexthops = append(nexthops, &routepb.FIBNexthop{
				DstMac: commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac: commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device: nh.Device,
			})
		}
		pbEntries = append(pbEntries, &routepb.FIBEntry{
			Prefix:   e.Prefix.String(),
			Nexthops: nexthops,
		})
	}

	handle, err := backend.UpdateModule(name, pbEntries)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	mc, ok := handle.(*croute.ModuleConfig)
	require.True(t, ok, "handle is not *croute.ModuleConfig")

	return mc.CPModulePtr()
}

// routeHandlePackets invokes the route handler for the given packets.
//
// mcIndex[0] maps the empty-name sentinel device (unused by routing);
// mcIndex[1..N] map the devices registered in the order nexthops were first
// seen by the backend.
func routeHandlePackets(
	mc unsafe.Pointer,
	mcIndex []uint64,
	packets ...gopacket.Packet,
) (*dataplane.PacketFrontPayload, error) {
	return routeHandlePacketsWithHashes(mc, mcIndex, nil, packets...)
}

// routeHandlePacketsWithHashes is like routeHandlePackets but also accepts a
// per-packet hash slice that controls ECMP bucket selection. If hashes is nil
// the handler uses whatever hash value is already set on each packet (zero in
// the test environment).
func routeHandlePacketsWithHashes(
	mc unsafe.Pointer,
	mcIndex []uint64,
	hashes []uint32,
	packets ...gopacket.Packet,
) (*dataplane.PacketFrontPayload, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	pf, err := dataplane.NewPacketFrontFromPackets(&pinner, packets...)
	if err != nil {
		return nil, fmt.Errorf("failed to create packet front: %w", err)
	}

	var mcPtr *C.uint64_t
	if len(mcIndex) > 0 {
		pinner.Pin(&mcIndex[0])
		mcPtr = (*C.uint64_t)(unsafe.Pointer(&mcIndex[0]))
	}

	var hashPtr *C.uint32_t
	if len(hashes) > 0 {
		chashes := make([]C.uint32_t, len(hashes))
		for idx, h := range hashes {
			chashes[idx] = C.uint32_t(h)
		}
		pinner.Pin(&chashes[0])
		hashPtr = &chashes[0]
	}

	C.run_route(
		(*C.struct_cp_module)(mc),
		mcPtr,
		C.uint64_t(len(mcIndex)),
		(*C.struct_packet_front)(unsafe.Pointer(pf)),
		hashPtr,
		C.uint64_t(len(packets)),
	)

	payload := pf.Payload()
	return &payload, nil
}

// mcIndexFor builds the mc_index lookup table from a list of encoded
// output device IDs in registration order.
//
// Slot 0 is reserved for the empty-name sentinel that cp_module_init
// installs in every cp_module; callers pass only real-device IDs and
// the helper prepends the sentinel automatically. Slot 1 corresponds
// to the first device registered via UpdateFIB, slot 2 to the second,
// in the order nexthops were first seen by the backend.
func mcIndexFor(devices ...uint64) []uint64 {
	return append([]uint64{0}, devices...)
}
