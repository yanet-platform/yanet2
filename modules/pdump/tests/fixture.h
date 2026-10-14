#pragma once

/*
 * Shared harness for modules/pdump/tests/capture_test.c and pdump_bench.c.
 *
 * Both link new_module_pdump() straight from its static library: the
 * module's own control-plane API cannot link into either binary, since its
 * cgo exports and DPDK stubs need a Go host process. This builds the
 * config, the object link and the eBPF filter by hand instead, the way
 * modules/fwstate/fuzzing/fwstate.c builds a module_ectx with a
 * hand-linked object.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/test_assert.h"

#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/errors/errors.h"

#include "modules/pdump/dataplane/config.h"

struct agent;
struct cp_object;
struct dataplane_ut;
struct module;
struct packet;
struct ring_worker;
struct rte_bpf;

// Worker clock a built fixture stamps into every captured record.
#define PDUMP_TEST_TIMESTAMP 123456789ULL

// Compile the classic "ip" eBPF filter into a ready-to-run program.
//
// Accepts every Ethernet frame whose ethertype is IPv4 and rejects every
// other frame, independent of the IP header's own validity. Mirrors the
// pointer layout modules/pdump/api/controlplane.c's filter setter builds
// for a configured filter -- duplicated here because that API cannot link
// into either binary. Returns NULL and leaves err set on failure; the
// caller frees a non-NULL result with free().
struct rte_bpf *
pdump_test_compile_accept_ip_filter(uint32_t snaplen, yanet_error **err);

// Free every packet left on a front's input, output and drop lists.
void
pdump_test_free_front(struct packet_front *pf);

// Build one Ethernet frame mbuf with an incrementing byte pattern.
//
// A captured prefix can be checked against the source bytes, and the "ip"
// filter sees exactly the ethertype asked for. Returns NULL on mbuf
// exhaustion.
struct packet *
pdump_test_build_packet(struct dataplane_ut *ut, bool ipv4, uint16_t total_len);

// Stand-in configuration generation with one execution context per worker,
// so the module's context commit handler finds its worker the way it does
// in a published generation.
struct pdump_test_gen {
	struct cp_config_gen cp_config_gen;
	struct config_gen_ectx *ectxs;
	struct config_gen_ectx **ectx_ptrs;
};

// Allocate gen's per-worker execution contexts.
//
// Returns TEST_SUCCESS, or TEST_FAILED on allocation failure. The caller
// still owns gen's storage and must call pdump_test_gen_destroy exactly
// once either way.
int
pdump_test_gen_init(struct pdump_test_gen *gen, size_t worker_count);

void
pdump_test_gen_destroy(struct pdump_test_gen *gen);

// Execution context for the given worker, or NULL past worker_count.
struct config_gen_ectx *
pdump_test_gen_worker_ectx(struct pdump_test_gen *gen, uint64_t worker_idx);

// One pdump capture harness: a fresh agent, a ring object, and a
// hand-linked module_ectx resolving to it.
//
// Stands in for what the control plane and the ectx build normally wire up
// for a published generation, since neither runs for a config built by
// hand. module, bpf and gen are borrowed from the caller and must outlive
// the fixture; pdump_test_fixture_destroy does not release them.
struct pdump_test_fixture {
	struct agent *agent;
	struct cp_object *ring_object;
	struct ring_worker *ring;
	uint8_t *ring_data;
	struct pdump_module_config config;
	struct module_ectx module_ectx;
	void *prepared;
	struct module_object_link_ectx link;
	struct object_ectx ring_object_ectx;
	struct dp_worker dp_worker;

	struct module *module;
	struct pdump_test_gen *gen;
};

struct pdump_test_fixture_params {
	const char *agent_name;
	const char *ring_name;
	uint32_t capacity;
	uint32_t publish_batch;
	uint32_t snaplen;
	uint64_t worker_idx;

	// Borrowed: the fixture resolves against these but does not own or
	// free them.
	struct module *module;
	struct rte_bpf *bpf;
	struct pdump_test_gen *gen;
};

// Build a fixture and commit it once, as publishing a generation does.
//
// It attaches the agent, creates the ring object and wires the module
// config to the ring and the filter. Returns TEST_SUCCESS or TEST_FAILED;
// either way the caller must call pdump_test_fixture_destroy exactly once.
int
pdump_test_fixture_build(
	struct dataplane_ut *ut,
	const struct pdump_test_fixture_params *params,
	struct pdump_test_fixture *fx
);

// Run the module's context commit handler for fx's worker again.
//
// A test that changes the config's link calls this, as a new generation
// would.
void
pdump_test_fixture_commit(struct pdump_test_fixture *fx);

// Release everything pdump_test_fixture_build allocated. NULL-field-safe:
// a fixture that failed partway through build may be destroyed too.
void
pdump_test_fixture_destroy(struct pdump_test_fixture *fx);
