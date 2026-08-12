package decap_test

/*
#cgo CFLAGS: -I../../../../ -I../../../../lib
#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp

#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"
#include "dataplane/config/zone.h"

// yanet_test_pin_current_gen mirrors the pin half of the unlocked-reader
// pattern used to read counters: lock, acquire a pin on the currently
// published generation, unlock.
static struct cp_config_gen *
yanet_test_pin_current_gen(struct agent *agent) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);
	struct cp_config_gen *gen = cp_config_gen_acquire(cp_config);
	cp_config_unlock(cp_config);

	return gen;
}

// yanet_test_unpin_gen mirrors the same reader's release: lock, drop the
// pin, unlock.
static void
yanet_test_unpin_gen(struct agent *agent, struct cp_config_gen *gen) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);
	cp_config_gen_release(cp_config, gen);
	cp_config_unlock(cp_config);
}

// yanet_test_zero_worker_count clobbers the shared worker count to zero and
// returns the value it replaced.
//
// The execution-context build rejects a zero worker count outright, before
// touching the configuration arena at all, so a publish issued while this
// is in effect fails deterministically in that stage, regardless of how
// much arena memory happens to be free. The worker count is fixed once
// dataplane startup fills it in, so the caller must restore it before the
// harness does anything else that relies on it.
static uint64_t
yanet_test_zero_worker_count(struct agent *agent) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	uint64_t original = dp_config->worker_count;
	dp_config->worker_count = 0;
	return original;
}

// yanet_test_restore_worker_count reverses the worker-count override above.
static void
yanet_test_restore_worker_count(struct agent *agent, uint64_t worker_count) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);
	dp_config->worker_count = worker_count;
}
*/
import "C"

import "unsafe"

// generationPin models an unlocked counters reader's pin on an agent's
// currently published configuration generation.
//
// It is taken and dropped under the config lock exactly as a production
// counter read does, without reading any counters itself. This lives
// outside _test.go because cgo is not permitted there. Callers in this
// package use it directly.
type generationPin struct {
	agent *C.struct_agent
	gen   *C.struct_cp_config_gen
}

// pinCurrentGeneration pins the given agent's currently published
// configuration generation, deferring that generation's teardown until
// release is called.
//
// The pointer must be the unsafe.Pointer an *ffi.Agent exposes via
// AsRawPtr.
func pinCurrentGeneration(agentPtr unsafe.Pointer) *generationPin {
	agent := (*C.struct_agent)(agentPtr)
	return &generationPin{
		agent: agent,
		gen:   C.yanet_test_pin_current_gen(agent),
	}
}

// release drops the pin taken by pinCurrentGeneration.
//
// Safe to call at most once: a second call would double-release the pin.
func (m *generationPin) release() {
	C.yanet_test_unpin_gen(m.agent, m.gen)
}

// zeroWorkerCount clobbers the shared worker count to zero.
//
// A publish issued while it is in effect fails deterministically inside
// the execution-context build, independent of how much configuration
// arena memory happens to be free. Returns the value to pass to
// restoreWorkerCount once the publish under test has run. The pointer
// must be the unsafe.Pointer an *ffi.Agent exposes via AsRawPtr.
func zeroWorkerCount(agentPtr unsafe.Pointer) uint64 {
	return uint64(C.yanet_test_zero_worker_count((*C.struct_agent)(agentPtr)))
}

// restoreWorkerCount undoes zeroWorkerCount, writing the given worker count
// back to the shared configuration.
//
// The pointer must be the unsafe.Pointer an *ffi.Agent exposes via
// AsRawPtr.
func restoreWorkerCount(agentPtr unsafe.Pointer, workerCount uint64) {
	C.yanet_test_restore_worker_count((*C.struct_agent)(agentPtr), C.uint64_t(workerCount))
}
