// Drives the Rust-implemented rblackhole module end to end through the
// in-process dataplane: a C control-plane config is published over
// shared memory, worker rounds run the real pipeline, and the module's
// prefix-match drop decisions are asserted on the round results.

#include "api/agent.h"
#include "common/test_assert.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/mempool.h"

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/strutils.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/packet/data.h"
#include "modules/rblackhole/api/controlplane.h"

// Builds the plain-device topology with one rblackhole chain and runs
// one packet through it, reporting the result lists through `outcome`.
struct round_outcome {
	uint64_t output_count;
	uint64_t drop_count;
};

static int
run_one(const uint8_t dst_addr[4], struct round_outcome *outcome) {
	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"rblackhole"};
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
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "rust-module-test", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *rblackhole =
		rblackhole_module_config_new(agent, "bh", &err);
	TEST_ASSERT_NOT_NULL(rblackhole, "rblackhole_module_config_new failed");
	// Blackhole 192.0.2.0/24.
	uint8_t first_addr[4] = {192, 0, 2, 0};
	uint8_t last_addr[4] = {192, 0, 2, 255};
	TEST_ASSERT_SUCCESS(
		rblackhole_module_config_add_prefix_v4(
			rblackhole, first_addr, last_addr
		),
		"rblackhole prefix setup failed"
	);
	// Passed packets are routed to the linked device's output entry, so
	// the config must own one.
	uint64_t device_idx = 0;
	TEST_ASSERT_SUCCESS(
		rblackhole_module_config_add_device(
			rblackhole, "dev0", &device_idx, &err
		),
		"rblackhole device link failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *modules[] = {rblackhole};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 1, modules, &err),
		"module update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const char *chain_types[] = {"rblackhole"};
	const char *chain_names[] = {"bh"};
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
		"function update failed"
	);
	cp_function_config_free(function);

	struct cp_pipeline_config *input_pipeline =
		cp_pipeline_config_create("input", 1);
	TEST_ASSERT_NOT_NULL(input_pipeline, "input pipeline alloc failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(input_pipeline, 0, "function"),
		"input device pipeline setup failed"
	);
	struct cp_chain_config *wire_chain =
		cp_chain_config_create("wire", 0, NULL, NULL);
	TEST_ASSERT_NOT_NULL(wire_chain, "cp_chain_config_create failed");
	struct cp_function_config *output_function =
		cp_function_config_create("output_function", 1);
	TEST_ASSERT_NOT_NULL(
		output_function, "cp_function_config_create failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(output_function, 0, wire_chain, 1),
		"cp_function_config_set_chain failed"
	);
	struct cp_function_config *output_functions[] = {output_function};
	TEST_ASSERT_SUCCESS(
		agent_update_functions(agent, 1, output_functions, &err),
		"output function update failed"
	);
	cp_function_config_free(output_function);

	struct cp_pipeline_config *output_pipeline =
		cp_pipeline_config_create("output", 1);
	TEST_ASSERT_NOT_NULL(output_pipeline, "output pipeline alloc failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(
			output_pipeline, 0, "output_function"
		),
		"output device pipeline setup failed"
	);
	struct cp_pipeline_config *pipelines[] = {
		input_pipeline, output_pipeline
	};
	TEST_ASSERT_SUCCESS(
		agent_update_pipelines(agent, 2, pipelines, &err),
		"pipeline update failed"
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
		"device update failed"
	);
	yanet_error *device_err = NULL;
	cp_device_plain_free(device, &device_err);
	yanet_error_free(device_err);

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
	// dst_addr is already network order; a byte copy keeps it so.
	memcpy(&ipv4->dst_addr, dst_addr, 4);
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");

	struct packet_list input;
	packet_list_init(&input);
	packet_list_add(&input, packet);
	struct dataplane_ut_round_result result;
	dataplane_ut_run(ut, 0, &input, &result);
	outcome->output_count = packet_list_count(&result.output);
	outcome->drop_count = packet_list_count(&result.drop);
	dataplane_ut_round_result_free(&result);

	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

static int
rust_module_round_test(void) {
	struct round_outcome dropped;
	uint8_t dropped_dst[4] = {192, 0, 2, 1};
	TEST_ASSERT_SUCCESS(
		run_one(dropped_dst, &dropped), "blackholed round failed"
	);
	TEST_ASSERT_EQUAL(
		(long)dropped.output_count,
		0L,
		"a packet inside the blackhole prefix must not pass"
	);
	TEST_ASSERT_EQUAL(
		(long)dropped.drop_count,
		1L,
		"a packet inside the blackhole prefix must be dropped"
	);

	struct round_outcome passed;
	uint8_t passed_dst[4] = {198, 51, 100, 7};
	TEST_ASSERT_SUCCESS(
		run_one(passed_dst, &passed), "passed round failed"
	);
	TEST_ASSERT_EQUAL(
		(long)passed.output_count,
		1L,
		"a packet outside the blackhole prefix must pass"
	);
	TEST_ASSERT_EQUAL(
		(long)passed.drop_count,
		0L,
		"a packet outside the blackhole prefix must not be dropped"
	);

	return TEST_SUCCESS;
}
#endif

int
main(void) {
	log_enable_name("debug");

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE
	TEST_ASSERT_SUCCESS(
		rust_module_round_test(), "rblackhole round test failed"
	);
#endif
	return TEST_SUCCESS;
}
