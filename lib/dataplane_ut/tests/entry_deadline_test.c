// verifies the device-entry deadline scheduler rules: every entry is
// armed with deadline zero when the generation applies, every
// invocation — packet-driven or not — consumes the armed deadline, an
// arm only ever decreases the deadline and the generation's cached
// earliest deadline, the due sweep dispatches exactly the entries whose
// deadline has arrived, and the executing entry is observable from
// inside a module handler.

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "common/test_assert.h"
#include "devices/plain/api/controlplane.h"
#include "devices/trafgen/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/schedule.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/stages.h"

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include <stdio.h>
#include <string.h>

// What the probe module's handler observed during its last invocation.
// Written from the packet path, read by the assertions after a stage.
struct deadline_probe_observation {
	const struct device_entry_ectx *entry;
	const struct module_ectx *module;
	unsigned calls;
};
static struct deadline_probe_observation deadline_probe_observed;

// A pass-through module whose handler records the executing entry the
// generation context exposes, so the test observes the current-entry
// contract from inside a real dispatch.
static void
deadline_probe_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	deadline_probe_observed = (struct deadline_probe_observation){
		.entry = module_ectx_current_entry(module_ectx),
		.module = module_ectx,
		.calls = deadline_probe_observed.calls + 1,
	};
	packet_front_pass(packet_front);
}

static void
deadline_probe_module_commit(
	struct dp_config *dp_config, struct cp_module *cp_module
) {
	(void)dp_config;
	(void)cp_module;
}

// Resolved by the module loader from the test binary itself; the
// constructor must be dynamically exported for that.
__attribute__((visibility("default"))) struct module *
new_module_deadline_probe(void) {
	struct module *module =
		(struct module *)calloc(1, sizeof(struct module));
	if (module == NULL) {
		return NULL;
	}
	snprintf(module->name, sizeof(module->name), "%s", "deadline_probe");
	module->handler = deadline_probe_handle_packets;
	module->commit_handler = deadline_probe_module_commit;
	return module;
}

// The probe module's control-plane counterpart: a bare cp_module with
// no configuration of its own.
struct deadline_probe_config {
	struct cp_module cp_module;
};

// Builds a one-device generation whose input pipeline is the probe
// module, so the input entry's dispatch reaches a handler that
// observes the scheduler state.
static int
deadline_test_harness(struct dataplane_ut **ut_out, struct agent **agent_out) {
	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"deadline_probe"};
	const char *devs_to_load[] = {"plain"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = module_names,
		.module_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "entry-deadline", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct deadline_probe_config *probe = (struct deadline_probe_config *)
		memory_balloc(&agent->memory_context, sizeof(*probe));
	TEST_ASSERT_NOT_NULL(probe, "probe config allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_module_init(
			&probe->cp_module,
			agent,
			"deadline_probe",
			"probe",
			&err
		),
		"probe module init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *modules[] = {&probe->cp_module};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 1, modules, &err),
		"module update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const char *chain_types[] = {"deadline_probe"};
	const char *chain_names[] = {"probe"};
	struct cp_chain_config *chain =
		cp_chain_config_create("chain", 1, chain_types, chain_names);
	TEST_ASSERT_NOT_NULL(chain, "cp_chain_config_create failed");
	struct cp_function_config *function =
		cp_function_config_create("function", 1);
	TEST_ASSERT_NOT_NULL(function, "cp_function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(function, 0, chain, 1),
		"cp_function_config_set_chain failed"
	);
	struct cp_function_config *functions[] = {function};
	TEST_ASSERT_SUCCESS(
		agent_update_functions(agent, 1, functions, &err),
		"function update failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_function_config_free(function);

	struct cp_pipeline_config *input_pipeline =
		cp_pipeline_config_create("input", 1);
	TEST_ASSERT_NOT_NULL(
		input_pipeline, "input pipeline allocation failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(input_pipeline, 0, "function"),
		"input pipeline setup failed"
	);
	struct cp_pipeline_config *output_pipeline =
		cp_pipeline_config_create("output", 0);
	TEST_ASSERT_NOT_NULL(
		output_pipeline, "output pipeline allocation failed"
	);
	struct cp_pipeline_config *pipelines[] = {
		input_pipeline, output_pipeline
	};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 2, pipelines, &err),
		"pipeline update failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(input_pipeline);
	cp_pipeline_config_free(output_pipeline);

	struct cp_device_plain_config *device_config =
		cp_device_plain_config_new("dev0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(
		device_config, "cp_device_plain_config_new failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_input_pipeline(
			device_config, 0, "input", 1
		),
		"input device pipeline setup failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_plain_config_set_output_pipeline(
			device_config, 0, "output", 1
		),
		"output device pipeline setup failed"
	);
	struct cp_device *device =
		cp_device_plain_new(agent, device_config, &err);
	cp_device_plain_config_free(device_config);
	TEST_ASSERT_NOT_NULL(device, "cp_device_plain_new failed");
	struct cp_device *devices[] = {device};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(agent, 1, devices, &err),
		"device update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	*ut_out = ut;
	*agent_out = agent;
	return TEST_SUCCESS;
}

// The worker's generation context after the published generation has
// been picked up by at least one round preparation.
static int
deadline_test_context(
	struct dataplane_ut *ut, struct config_gen_ectx **context_out
) {
	struct yanet_shm *shm = dataplane_ut_shm(ut);
	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	struct dp_worker *worker = ADDR_OF(workers);
	struct config_gen_ectx *context =
		ATOMIC_ADDR_OF(&worker->config_gen_ectx);
	TEST_ASSERT_NOT_NULL(
		context, "worker has not picked up a generation yet"
	);
	*context_out = context;
	return TEST_SUCCESS;
}

// One parsed IPv4-over-Ethernet packet, the minimum the probe module's
// pass-through dispatch carries without a drop before it.
static int
deadline_test_packet(struct dataplane_ut *ut, struct packet_list *input) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(
		mbuf, sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv4_hdr)
	);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, mbuf->data_len);
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr));
	ipv4->dst_addr = rte_cpu_to_be_32(UINT32_C(0xc0000201));
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");
	packet_list_add(input, packet);
	return TEST_SUCCESS;
}

static int
entry_deadline_test(void) {
	struct agent *agent = NULL;
	struct dataplane_ut *ut = NULL;
	TEST_ASSERT_SUCCESS(
		deadline_test_harness(&ut, &agent), "harness setup failed"
	);

	const uint64_t now = 1000 * 1000 * 1000;
	dataplane_ut_set_time_ns(ut, now);

	// The first round preparation builds the worklists and arms the
	// whole generation with deadline zero: every entry must fire right
	// after the generation applies.
	TEST_ASSERT_SUCCESS(
		dataplane_ut_begin_round(ut, 0), "begin_round failed"
	);
	struct config_gen_ectx *context = NULL;
	TEST_ASSERT_SUCCESS(
		deadline_test_context(ut, &context), "context lookup failed"
	);
	struct device_ectx *device = config_gen_ectx_get_device(context, 0);
	TEST_ASSERT_NOT_NULL(device, "generation has no device 0");
	struct device_entry_ectx *input_entry = device->abs_input_pipelines;
	struct device_entry_ectx *output_entry = device->abs_output_pipelines;
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		0L,
		"the input entry must be armed with deadline zero at apply"
	);
	TEST_ASSERT_EQUAL(
		(long)output_entry->deadline_ns,
		0L,
		"the output entry must be armed with deadline zero at apply"
	);
	TEST_ASSERT_EQUAL(
		(long)context->nearest_deadline_ns,
		0L,
		"the cached earliest deadline must be zero at apply"
	);

	// A packet-driven invocation far before the deadline consumes the
	// armed deadline, and the handler observes exactly the executing
	// entry while it runs.
	device_entry_ectx_arm(context, input_entry, now + 100000);
	struct packet_list input;
	packet_list_init(&input);
	TEST_ASSERT_SUCCESS(
		deadline_test_packet(ut, &input), "test packet build failed"
	);
	struct dataplane_ut_stage stage = {
		.worker_idx = 0,
		.device_id = 0,
		.output = false,
	};
	struct dataplane_ut_round_result result;
	TEST_ASSERT_SUCCESS(
		dataplane_ut_run_stage(ut, &stage, &input, &result),
		"run_stage failed"
	);
	dataplane_ut_round_result_free(&result);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"an invocation must consume the armed deadline"
	);
	TEST_ASSERT(
		deadline_probe_observed.entry == input_entry,
		"the handler must observe the executing entry during dispatch"
	);
	TEST_ASSERT_NOT_NULL(
		deadline_probe_observed.module,
		"the handler must observe its own module context"
	);
	TEST_ASSERT_NULL(
		context->current_entry,
		"the executing entry must be restored after the invocation"
	);

	// From the never sentinel an arm only decreases: two later arms
	// must not postpone the earliest one, and the cached earliest
	// deadline follows — here still zero, the output entry's
	// build-time arm.
	device_entry_ectx_arm(context, input_entry, now + 1000);
	device_entry_ectx_arm(context, input_entry, now + 500);
	device_entry_ectx_arm(context, input_entry, now + 900);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + 500),
		"an arm must never raise the deadline"
	);
	TEST_ASSERT_EQUAL(
		(long)context->nearest_deadline_ns,
		0L,
		"the cached earliest deadline must keep the earliest arm"
	);

	// A full round past every armed deadline fires the remaining
	// entries and leaves nothing armed.
	dataplane_ut_set_time_ns(ut, now + 2000);
	struct packet_list round_input;
	packet_list_init(&round_input);
	dataplane_ut_run(ut, 0, NULL, &round_input, &result);
	dataplane_ut_round_result_free(&result);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"a due invocation must consume the re-armed deadline"
	);
	TEST_ASSERT_EQUAL(
		(long)output_entry->deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"a due invocation must consume the build-time deadline"
	);
	TEST_ASSERT_EQUAL(
		(long)context->nearest_deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"the sweep must recompute the cache when every deadline fired"
	);

	// The sweep itself is pinned in isolation, where the force-poll
	// cannot mask a broken predicate: a full round runs every entry
	// anyway, so the arm-due stage drives only the sweep's movement and
	// leaves the dispatch to a later round.
	device_entry_ectx_arm(context, input_entry, now + 3000);

	// Before the deadline the sweep must move nothing and keep the
	// cache.
	dataplane_ut_set_time_ns(ut, now + 2500);
	TEST_ASSERT_SUCCESS(dataplane_ut_arm_due(ut, 0), "arm_due failed");
	TEST_ASSERT_EQUAL(
		(long)input_entry->schedule_list,
		(long)device_entry_schedule_home,
		"a future deadline must not dispatch its entry"
	);
	TEST_ASSERT_EQUAL(
		(long)context->nearest_deadline_ns,
		(long)(now + 3000),
		"a future deadline must keep the cached earliest"
	);

	// At exactly the armed deadline the sweep moves exactly the due
	// entry onto the ready list — without consuming its deadline, which
	// only the invocation does — and recomputes the cache from the
	// survivors.
	dataplane_ut_set_time_ns(ut, now + 3000);
	TEST_ASSERT_SUCCESS(dataplane_ut_arm_due(ut, 0), "arm_due failed");
	TEST_ASSERT_EQUAL(
		(long)input_entry->schedule_list,
		(long)device_entry_schedule_ready,
		"a due deadline must dispatch its entry"
	);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + 3000),
		"the sweep must not consume the deadline it dispatches"
	);
	TEST_ASSERT_EQUAL(
		(long)output_entry->schedule_list,
		(long)device_entry_schedule_home,
		"an entry without an arm must stay home"
	);
	TEST_ASSERT_EQUAL(
		(long)context->nearest_deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"the sweep must recompute the cache from the survivors"
	);

	// A stage may also run the dispatched entry directly: the direct
	// invocation replaces the queued run, runs the handler exactly
	// once, and consumes the deadline.
	deadline_probe_observed = (struct deadline_probe_observation){0};
	packet_list_init(&input);
	TEST_ASSERT_SUCCESS(
		deadline_test_packet(ut, &input), "test packet build failed"
	);
	TEST_ASSERT_SUCCESS(
		dataplane_ut_run_stage(ut, &stage, &input, &result),
		"run_stage failed"
	);
	dataplane_ut_round_result_free(&result);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)DEVICE_ENTRY_DEADLINE_NEVER,
		"the dispatched entry must consume its deadline on invocation"
	);
	TEST_ASSERT(
		deadline_probe_observed.entry == input_entry,
		"the queued entry must run through the stage"
	);
	TEST_ASSERT_EQUAL(
		(long)deadline_probe_observed.calls,
		1L,
		"the queued entry must run exactly once through the stage"
	);

	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

// Verifies the trafgen generator re-arms its input entry on every run,
// so it keeps sourcing traffic once the worker's force-poll is gone.
static int
trafgen_rearm_test(void) {
	const char *devs_to_load[] = {"trafgen"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "trafgen-rearm", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_pipeline_config *input_pipeline =
		cp_pipeline_config_create("input", 0);
	TEST_ASSERT_NOT_NULL(
		input_pipeline, "input pipeline allocation failed"
	);
	struct cp_pipeline_config *output_pipeline =
		cp_pipeline_config_create("output", 0);
	TEST_ASSERT_NOT_NULL(
		output_pipeline, "output pipeline allocation failed"
	);
	struct cp_pipeline_config *pipelines[] = {
		input_pipeline, output_pipeline
	};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 2, pipelines, &err),
		"pipeline update failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(input_pipeline);
	cp_pipeline_config_free(output_pipeline);

	struct cp_device_trafgen_config *device_config =
		cp_device_trafgen_config_new("gen0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(
		device_config, "cp_device_trafgen_config_new failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_config_set_input_pipeline(
			device_config, 0, "input", 1
		),
		"input device pipeline setup failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_config_set_output_pipeline(
			device_config, 0, "output", 1
		),
		"output device pipeline setup failed"
	);
	struct cp_device *device =
		cp_device_trafgen_new(agent, device_config, &err);
	cp_device_trafgen_config_free(device_config);
	TEST_ASSERT_NOT_NULL(device, "cp_device_trafgen_new failed");

	// One parseable frame, replayed at 1000 pps by a single worker: a
	// packet's worth of bucket credit is exactly one millisecond.
	uint8_t
		frame[sizeof(struct rte_ether_hdr) +
		      sizeof(struct rte_ipv4_hdr)];
	memset(frame, 0, sizeof(frame));
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)frame;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr));
	ipv4->dst_addr = rte_cpu_to_be_32(UINT32_C(0xc0000201));
	uint32_t lengths[1] = {sizeof(frame)};
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_set_frames(
			device, frame, sizeof(frame), lengths, 1, &err
		),
		"frame upload failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_set_rate(device, 1000), "rate setup failed"
	);

	struct cp_device *devices[] = {device};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(agent, 1, devices, &err),
		"device update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const uint64_t now = 1000 * 1000 * 1000;
	dataplane_ut_set_time_ns(ut, now);
	TEST_ASSERT_SUCCESS(
		dataplane_ut_begin_round(ut, 0), "begin_round failed"
	);
	struct config_gen_ectx *context = NULL;
	TEST_ASSERT_SUCCESS(
		deadline_test_context(ut, &context), "context lookup failed"
	);
	struct device_ectx *device_ectx =
		config_gen_ectx_get_device(context, 0);
	struct device_entry_ectx *input_entry =
		device_ectx->abs_input_pipelines;

	// The first run anchors the pacer clock and emits nothing, but must
	// still arm the entry for one packet interval of bucket time.
	struct packet_list round_input;
	packet_list_init(&round_input);
	struct dataplane_ut_round_result result;
	dataplane_ut_run(ut, 0, NULL, &round_input, &result);
	dataplane_ut_round_result_free(&result);
	const uint64_t interval_ns = 1000 * 1000;
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + interval_ns),
		"the generator must re-arm its entry for one packet interval"
	);

	// The re-arm drives the sweep: before the deadline nothing moves,
	// at the deadline the entry is dispatched.
	dataplane_ut_set_time_ns(ut, now + interval_ns / 2);
	TEST_ASSERT_SUCCESS(dataplane_ut_arm_due(ut, 0), "arm_due failed");
	TEST_ASSERT_EQUAL(
		(long)input_entry->schedule_list,
		(long)device_entry_schedule_home,
		"the generator's arm must not fire before its deadline"
	);
	dataplane_ut_set_time_ns(ut, now + interval_ns);
	TEST_ASSERT_SUCCESS(dataplane_ut_arm_due(ut, 0), "arm_due failed");
	TEST_ASSERT_EQUAL(
		(long)input_entry->schedule_list,
		(long)device_entry_schedule_ready,
		"the generator's arm must dispatch its entry at the deadline"
	);

	// The dispatched run emits from the accumulated credit and re-arms
	// again relative to its own now. Two full intervals passed since
	// the pacer anchored, so the bucket releases two packets.
	dataplane_ut_set_time_ns(ut, now + 2 * interval_ns);
	dataplane_ut_run(ut, 0, NULL, &round_input, &result);
	TEST_ASSERT_EQUAL(
		(long)(packet_list_count(&result.output) +
		       packet_list_count(&result.drop)),
		2L,
		"the dispatched run must emit the accumulated packets"
	);
	dataplane_ut_round_result_free(&result);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + 3 * interval_ns),
		"the emitting run must re-arm relative to its own time"
	);

	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

// Verifies that a non-divisor packet rate rounds the wakeup up, so the
// credit for each packet is payable at the wake that fires for it.
static int
trafgen_remainder_test(void) {
	const char *devs_to_load[] = {"trafgen"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "trafgen-remainder", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_pipeline_config *input_pipeline =
		cp_pipeline_config_create("input", 0);
	TEST_ASSERT_NOT_NULL(
		input_pipeline, "input pipeline allocation failed"
	);
	struct cp_pipeline_config *output_pipeline =
		cp_pipeline_config_create("output", 0);
	TEST_ASSERT_NOT_NULL(
		output_pipeline, "output pipeline allocation failed"
	);
	struct cp_pipeline_config *pipelines[] = {
		input_pipeline, output_pipeline
	};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 2, pipelines, &err),
		"pipeline update failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(input_pipeline);
	cp_pipeline_config_free(output_pipeline);

	struct cp_device_trafgen_config *device_config =
		cp_device_trafgen_config_new("gen0", 1, 1, &err);
	TEST_ASSERT_NOT_NULL(
		device_config, "cp_device_trafgen_config_new failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_config_set_input_pipeline(
			device_config, 0, "input", 1
		),
		"input device pipeline setup failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_config_set_output_pipeline(
			device_config, 0, "output", 1
		),
		"output device pipeline setup failed"
	);
	struct cp_device *device =
		cp_device_trafgen_new(agent, device_config, &err);
	cp_device_trafgen_config_free(device_config);
	TEST_ASSERT_NOT_NULL(device, "cp_device_trafgen_new failed");

	uint8_t
		frame[sizeof(struct rte_ether_hdr) +
		      sizeof(struct rte_ipv4_hdr)];
	memset(frame, 0, sizeof(frame));
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)frame;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *ipv4 = (struct rte_ipv4_hdr *)(ether + 1);
	ipv4->version_ihl = RTE_IPV4_VHL_DEF;
	ipv4->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr));
	ipv4->dst_addr = rte_cpu_to_be_32(UINT32_C(0xc0000201));
	uint32_t lengths[1] = {sizeof(frame)};
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_set_frames(
			device, frame, sizeof(frame), lengths, 1, &err
		),
		"frame upload failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	// Three packets per second: one packet costs exactly one billion
	// credit, so a full interval of 333333333 ns accrues 999999999 —
	// one short of a packet.
	TEST_ASSERT_SUCCESS(
		cp_device_trafgen_set_rate(device, 3), "rate setup failed"
	);

	struct cp_device *devices[] = {device};
	TEST_ASSERT_SUCCESS(
		agent_update_devices(agent, 1, devices, &err),
		"device update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const uint64_t now = 1000 * 1000 * 1000;
	dataplane_ut_set_time_ns(ut, now);
	TEST_ASSERT_SUCCESS(
		dataplane_ut_begin_round(ut, 0), "begin_round failed"
	);
	struct config_gen_ectx *context = NULL;
	TEST_ASSERT_SUCCESS(
		deadline_test_context(ut, &context), "context lookup failed"
	);
	struct device_entry_ectx *input_entry =
		config_gen_ectx_get_device(context, 0)->abs_input_pipelines;

	// The anchor run arms one rounded-up packet interval: 333333334 ns,
	// not the truncated 333333333 that wakes a billionth short.
	struct packet_list round_input;
	packet_list_init(&round_input);
	struct dataplane_ut_round_result result;
	dataplane_ut_run(ut, 0, NULL, &round_input, &result);
	dataplane_ut_round_result_free(&result);
	const uint64_t interval_ns = 333333334;
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + interval_ns),
		"the anchor arm must round the packet interval up"
	);

	// At the armed moment the accumulated credit is exactly one packet:
	// the run emits it and arms the next wake from the leftover two
	// credit, again rounded up.
	dataplane_ut_set_time_ns(ut, now + interval_ns);
	dataplane_ut_run(ut, 0, NULL, &round_input, &result);
	TEST_ASSERT_EQUAL(
		(long)(packet_list_count(&result.output) +
		       packet_list_count(&result.drop)),
		1L,
		"the wake at the rounded-up arm must afford one packet"
	);
	dataplane_ut_round_result_free(&result);
	TEST_ASSERT_EQUAL(
		(long)input_entry->deadline_ns,
		(long)(now + 2 * interval_ns - 1),
		"the emitting run must arm the next packet from the leftover "
		"credit"
	);

	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("error");
	TEST_ASSERT_SUCCESS(
		entry_deadline_test(), "entry_deadline_test failed"
	);
	TEST_ASSERT_SUCCESS(trafgen_rearm_test(), "trafgen_rearm_test failed");
	TEST_ASSERT_SUCCESS(
		trafgen_remainder_test(), "trafgen_remainder_test failed"
	);
	return TEST_SUCCESS;
}
