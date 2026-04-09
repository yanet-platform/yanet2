package balancer

/*
#cgo CFLAGS: -I../../../ -I../../../filter -I../../../lib -I../../../modules/balancer/dataplane -I../../../modules/balancer/dataplane/types
#cgo LDFLAGS: -L../../../build/modules/balancer/controlplane/helpers -lbalancer_helpers
#cgo LDFLAGS: -L../../../build/filter -lfilter_compiler

#include "filter/rule.h"

#include "modules/balancer/controlplane/helpers/agent.h"
#include "modules/balancer/controlplane/helpers/balancer.h"
#include "modules/balancer/controlplane/helpers/vs.h"
#include "modules/balancer/controlplane/helpers/real.h"
#include "modules/balancer/controlplane/helpers/sessions.h"

#include "modules/balancer/dataplane/types/vs.h"
#include "modules/balancer/dataplane/types/stats.h"
#include "modules/balancer/dataplane/types/selector.h"
#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/sessions_tracker.h"
#include "modules/balancer/dataplane/types/session.h"
#include "modules/balancer/dataplane/dataplane.h"
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/relptr"
)

func errFromCode(res C.int) error {
	switch res {
	case 0:
		return nil
	case -1:
		return errNoAgentMemory
	case -2:
		return fmt.Errorf("no heap memory")
	default:
		return fmt.Errorf("unknown error code=%d", res)
	}
}

func (a *BalancerAgent) asCPtr() *C.struct_agent {
	return (*C.struct_agent)(unsafe.Pointer(a.AsYanetAgent().AsRawPtr()))
}

func (a *BalancerAgent) install(handler *PacketHandler) error {
	a.AsYanetAgent().CleanError()

	res := C.balancer_agent_install(
		a.asCPtr(),
		handler.asCPtr(),
	)
	if res != 0 {
		return a.AsYanetAgent().TakeError()
	}

	return nil
}

func (a *BalancerAgent) register(handler *PacketHandler) error {
	return errFromCode(C.balancer_agent_register(
		a.asCPtr(),
		handler.asCPtr(),
	))
}

func (a *BalancerAgent) forget(handler *PacketHandler) {
	C.balancer_agent_forget(
		a.asCPtr(),
		handler.asCPtr(),
	)
}

func (a *BalancerAgent) list() []*PacketHandler {
	count := C.size_t(0)
	handlersRaw := C.balancer_agent_list(a.asCPtr(), &count)
	if handlersRaw == nil {
		return nil
	}
	handlers := unsafe.Slice((**PacketHandler)(unsafe.Pointer(handlersRaw)), count)
	for i := range handlers {
		handlers[i] = relptr.Deref(&handlers[i])
	}
	return handlers
}

func (a *BalancerAgent) createSessionTable(capacity int) *SessionTable {
	return (*SessionTable)(unsafe.Pointer(C.balancer_agent_create_st(
		a.asCPtr(),
		C.size_t(capacity),
	)))
}

func (a *BalancerAgent) destroySessionTable(st *SessionTable) {
	C.balancer_agent_destroy_st(a.asCPtr(), st.asCPtr())
}

func (ph *PacketHandler) asCPtr() *C.struct_balancer_packet_handler {
	return (*C.struct_balancer_packet_handler)(unsafe.Pointer(ph))
}

func (ph *PacketHandler) initialSetup(agent *BalancerAgent, name string, st *SessionTable) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return errFromCode(C.balancer_initial_setup(
		agent.asCPtr(),
		ph.asCPtr(),
		cName,
		st.asCPtr(),
	))
}

func (ph *PacketHandler) registerCounters() error {
	return errFromCode(C.balancer_register_counters(ph.asCPtr()))
}

func (ph *PacketHandler) name() string {
	return C.GoString(C.balancer_name(ph.asCPtr()))
}

func (ph *PacketHandler) setIpv4DecapFilter() error {
	return errFromCode(C.balancer_set_ipv4_decap_filter(ph.asCPtr()))
}

func (ph *PacketHandler) setIpv6DecapFilter() error {
	return errFromCode(C.balancer_set_ipv6_decap_filter(ph.asCPtr()))
}

func (ph *PacketHandler) freeDecapFilters() {
	C.balancer_free_decap_filters(ph.asCPtr())
}

func (ph *PacketHandler) setIpv4VsMatcher() error {
	return errFromCode(C.balancer_set_ipv4_vs_matcher(ph.asCPtr()))
}

func (ph *PacketHandler) setIpv6VsMatcher() error {
	return errFromCode(C.balancer_set_ipv6_vs_matcher(ph.asCPtr()))
}

func (ph *PacketHandler) freeVsMatchers() {
	C.balancer_free_vs_matchers(ph.asCPtr())
}

func (vs *VS) asCPtr() *C.struct_balancer_vs {
	return (*C.struct_balancer_vs)(unsafe.Pointer(vs))
}

func (vs *VS) setACL(agent *BalancerAgent) error {
	return errFromCode(C.balancer_vs_set_acl(vs.asCPtr(), agent.asCPtr()))
}

func (vs *VS) freeACL(agent *BalancerAgent) {
	C.balancer_vs_free_acl(vs.asCPtr(), agent.asCPtr())
}

func (vs *VS) updateRealSelector(rcu *RCU, agent *BalancerAgent) error {
	return errFromCode(
		C.balancer_vs_update_real_selector(vs.asCPtr(), rcu.asCPtr(), agent.asCPtr()),
	)
}

func (vs *VS) freeRealSelector(agent *BalancerAgent) {
	C.balancer_vs_free_real_selector(vs.asCPtr(), agent.asCPtr())
}

func (vs *VS) setSessionsTracker(agent *BalancerAgent) error {
	return errFromCode(C.balancer_vs_set_session_trackers(vs.asCPtr(), agent.asCPtr()))
}

func (vs *VS) freeSessionTracker(agent *BalancerAgent) {
	C.balancer_vs_free_session_trackers(vs.asCPtr(), agent.asCPtr())
}

func (r *Real) asCPtr() *C.struct_balancer_real {
	return (*C.struct_balancer_real)(unsafe.Pointer(r))
}

func (r *Real) sessions(workers uint32) (uint64, time.Time) {
	activeSessions := C.uint64_t(0)
	lastPacketTimestamp := C.uint32_t(0)
	C.balancer_real_sessions(r.asCPtr(), C.size_t(workers), &activeSessions, &lastPacketTimestamp)
	return uint64(activeSessions), time.Unix(int64(lastPacketTimestamp), 0)
}

func (st *SessionTable) asCPtr() *C.struct_balancer_session_table {
	return (*C.struct_balancer_session_table)(unsafe.Pointer(st))
}

func (st *SessionTable) capacity() int {
	return int(C.balancer_st_capacity(st.asCPtr()))
}

func (st *SessionTable) resize(newSize int, now time.Time) error {
	return errFromCode(C.balancer_st_resize(st.asCPtr(), C.size_t(newSize), C.uint32_t(now.Unix())))
}

const bucketMaxEntries = 16

func (st *SessionTable) newSessionIter() SessionTableIter {
	var iter SessionTableIter
	C.balancer_st_iter_init(
		(*C.struct_balancer_session_table_iter)(unsafe.Pointer(&iter)),
		st.asCPtr(),
	)
	return iter
}

func (it *SessionTableIter) nextBucket(now uint32, buf []SessionEntry) int {
	var count C.int
	ret := C.balancer_st_iter_next_bucket_buf(
		(*C.struct_balancer_session_table_iter)(unsafe.Pointer(it)),
		C.uint32_t(now),
		(*C.struct_balancer_session_entry)(unsafe.Pointer(&buf[0])),
		&count,
	)
	if ret == 0 {
		return -1
	}
	return int(count)
}

func (rcu *RCU) asCPtr() *C.struct_rcu {
	return (*C.struct_rcu)(unsafe.Pointer(rcu))
}
