#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/dataplane_ut/mempool.h"

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE
#include <errno.h>
#include <string.h>

#include "common/strutils.h"
#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/packet/dscp.h"
#include "modules/decap/api/controlplane.h"
#include "modules/dscp/api/controlplane.h"
#include "modules/forward/api/controlplane.h"
#endif

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE
#include <rte_ether.h>
#include <rte_gre.h>
#include <rte_ip.h>
#endif
#include <rte_mbuf.h>

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE
// Verifies that run_rounds restores routing metadata and packet payload after
// modules mutate both while a bounded self-loop runs on every round.
static int
run_round_restore_test(void) {
	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"dscp", "forward"};
	const char *devs_to_load[] = {"plain"};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = module_names,
		.module_count = 2,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");
	yanet_error *err = NULL;
	struct agent *agent =
		agent_attach(shm, 0, "smoke-round-restore", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *dscp = dscp_module_config_new(agent, "mark", &err);
	TEST_ASSERT_NOT_NULL(dscp, "dscp_module_config_new failed");
	uint8_t first_addr[4] = {0, 0, 0, 0};
	uint8_t last_addr[4] = {255, 255, 255, 255};
	TEST_ASSERT_SUCCESS(
		dscp_module_config_add_prefix_v4(dscp, first_addr, last_addr),
		"dscp prefix setup failed"
	);
	TEST_ASSERT_SUCCESS(
		dscp_module_config_set_dscp_marking(dscp, DSCP_MARK_ALWAYS, 42),
		"dscp marking setup failed"
	);

	struct cp_module *forward =
		forward_module_config_init(agent, "loop", &err);
	TEST_ASSERT_NOT_NULL(forward, "forward_module_config_init failed");
	struct forward_rule forward_rule = {0};
	strtcpy(forward_rule.target, "dev0", sizeof(forward_rule.target));
	strtcpy(forward_rule.counter, "loop", sizeof(forward_rule.counter));
	forward_rule.mode = FORWARD_MODE_IN;
	TEST_ASSERT_SUCCESS(
		forward_module_config_update(forward, &forward_rule, 1, &err),
		"forward rule setup failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_module *modules[] = {dscp, forward};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 2, modules, &err),
		"module update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const char *chain_types[] = {"dscp", "forward"};
	const char *chain_names[] = {"mark", "loop"};
	struct cp_chain_config *chain =
		cp_chain_config_create("chain", 2, chain_types, chain_names);
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
	// The live generation still references the device, so the destroy
	// fails with EAGAIN and the handle stays dangling until agent_detach;
	// free the allocated error chain instead of leaking it.
	yanet_error *device_err = NULL;
	cp_device_plain_free(device, &device_err);
	yanet_error_free(device_err);

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	struct dp_worker *dp_worker = ADDR_OF(workers);
	size_t outstanding = test_mempool_outstanding(dp_worker->rx_mempool);
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
	uint8_t original_tos = ipv4->type_of_service;
	uint16_t original_checksum = ipv4->hdr_checksum;

	struct packet_list input;
	packet_list_init(&input);
	packet_list_add(&input, packet);
	struct dataplane_ut_round_result first_result;
	dataplane_ut_run(ut, 0, &input, &first_result);
	TEST_ASSERT_EQUAL(
		(long)(packet_list_count(&first_result.output) +
		       packet_list_count(&first_result.drop)),
		1L,
		"configured pipeline must process the packet"
	);
	TEST_ASSERT_EQUAL(
		(long)ipv4->type_of_service,
		(long)(42 << DSCP_MARK_SHIFT),
		"dscp module must mutate the packet payload"
	);
	packet_list_concat(&input, &first_result.output);
	packet_list_concat(&input, &first_result.drop);
	ipv4->type_of_service = original_tos;
	ipv4->hdr_checksum = original_checksum;
	packet->rx_device_id = 0;
	packet->tx_device_id = 0;
	packet->recirc_remaining = 0;
	packet->recirc_initialized = 0;
	TEST_ASSERT_SUCCESS(
		dataplane_ut_run_rounds(ut, 0, &input, 3, 1),
		"run_rounds failed"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&input),
		1L,
		"run_rounds must restore the fixed packet set"
	);
	TEST_ASSERT_EQUAL(
		(long)input.first->recirc_remaining,
		0L,
		"run_rounds must restore recirculation remaining budget"
	);
	TEST_ASSERT_EQUAL(
		(long)input.first->recirc_initialized,
		0L,
		"run_rounds must restore recirculation initialization state"
	);
	TEST_ASSERT_EQUAL(
		(long)input.first->rx_device_id,
		0L,
		"run_rounds must restore rx device id"
	);
	TEST_ASSERT_EQUAL(
		(long)input.first->tx_device_id,
		0L,
		"run_rounds must restore tx device id"
	);
	TEST_ASSERT_EQUAL(
		(long)ipv4->type_of_service,
		(long)original_tos,
		"run_rounds must restore the packet payload"
	);
	TEST_ASSERT_EQUAL(
		(long)test_mempool_outstanding(dp_worker->rx_mempool),
		(long)(outstanding + 1),
		"run_rounds must not change outstanding packet allocations"
	);

	packet = packet_list_pop(&input);
	rte_pktmbuf_free(packet_to_mbuf(packet));
	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}

// Verifies that run_rounds reports -EINVAL when a module mutates mbuf
// geometry: decap strips tunnel headers with rte_pktmbuf_adj, so the second
// round's restore sees a packet whose length no longer matches the snapshot.
static int
run_rounds_geometry_mismatch_test(void) {
	const char *port_names[] = {"dev0"};
	const char *module_names[] = {"decap"};
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
		agent_attach(shm, 0, "smoke-geometry-mismatch", 8u << 20, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *decap =
		decap_module_config_new(agent, "unwrap", &err);
	TEST_ASSERT_NOT_NULL(decap, "decap_module_config_new failed");
	uint8_t first_addr[4] = {0, 0, 0, 0};
	uint8_t last_addr[4] = {255, 255, 255, 255};
	TEST_ASSERT_SUCCESS(
		decap_module_config_add_prefix_v4(decap, first_addr, last_addr),
		"decap prefix setup failed"
	);

	struct cp_module *modules[] = {decap};
	TEST_ASSERT_SUCCESS(
		agent_update_modules(agent, 1, modules, &err),
		"module update failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const char *chain_types[] = {"decap"};
	const char *chain_names[] = {"unwrap"};
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
	// Dangling until agent_detach; the EAGAIN error chain must be freed.
	yanet_error *device_err = NULL;
	cp_device_plain_free(device, &device_err);
	yanet_error_free(device_err);

	struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	struct dp_worker *dp_worker = ADDR_OF(workers);
	size_t outstanding = test_mempool_outstanding(dp_worker->rx_mempool);
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
	TEST_ASSERT_NOT_NULL(mbuf, "dataplane_ut_alloc_mbuf returned NULL");
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;

	// ether + outer IPv4 (GRE) + 4-byte GRE header + inner IPv4
	const size_t payload_len = sizeof(struct rte_ether_hdr) +
				   2 * sizeof(struct rte_ipv4_hdr) + 4;
	uint8_t *data = (uint8_t *)rte_pktmbuf_append(mbuf, payload_len);
	TEST_ASSERT_NOT_NULL(data, "packet payload allocation failed");
	memset(data, 0, payload_len);
	struct rte_ether_hdr *ether = (struct rte_ether_hdr *)data;
	ether->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *outer = (struct rte_ipv4_hdr *)(ether + 1);
	outer->version_ihl = RTE_IPV4_VHL_DEF;
	outer->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + 4 + sizeof(struct rte_ipv4_hdr)
	);
	outer->next_proto_id = IPPROTO_GRE;
	outer->dst_addr = rte_cpu_to_be_32(UINT32_C(0xc0000201));
	struct rte_gre_hdr *gre = (struct rte_gre_hdr *)(outer + 1);
	gre->proto = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	struct rte_ipv4_hdr *inner = (struct rte_ipv4_hdr *)(gre + 1);
	inner->version_ihl = RTE_IPV4_VHL_DEF;
	inner->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr));
	inner->next_proto_id = IPPROTO_TCP;
	TEST_ASSERT_SUCCESS(parse_packet(packet), "parse_packet failed");

	struct packet_list input;
	packet_list_init(&input);
	packet_list_add(&input, packet);

	int rc = dataplane_ut_run_rounds(ut, 0, &input, 3, 1);
	TEST_ASSERT_EQUAL(
		(long)rc,
		(long)-EINVAL,
		"run_rounds must reject mutated packet geometry"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&input),
		1L,
		"run_rounds must return the saved packet on failure"
	);
	TEST_ASSERT_EQUAL(
		(long)test_mempool_outstanding(dp_worker->rx_mempool),
		(long)(outstanding + 1),
		"run_rounds must not leak the mutated packet"
	);

	packet = packet_list_pop(&input);
	rte_pktmbuf_free(packet_to_mbuf(packet));
	agent_detach(agent);
	dataplane_ut_free(ut);
	return TEST_SUCCESS;
}
#endif

// Verifies that result cleanup returns packets to their owning DPDK pool.
static int
run_round_result_foreign_pool_test(void) {
	struct rte_mempool *pool = test_mempool_create();
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(pool);
	TEST_ASSERT_NOT_NULL(mbuf, "foreign-pool mbuf allocation failed");

	struct packet *packet = mbuf_to_packet(mbuf);
	*packet = (struct packet){.mbuf = mbuf};

	struct dataplane_ut_round_result result;
	packet_list_init(&result.output);
	packet_list_init(&result.drop);
	packet_list_add(&result.output, packet);
	dataplane_ut_round_result_free(&result);

	size_t outstanding = test_mempool_outstanding(pool);
	test_mempool_free(pool);
	TEST_ASSERT_EQUAL(
		(long)outstanding,
		0L,
		"result cleanup must return a packet to its owning pool"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");
	TEST_ASSERT_SUCCESS(
		run_round_result_foreign_pool_test(),
		"foreign-pool result cleanup test failed"
	);

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");

	dataplane_ut_free(ut);

	{
		struct dataplane_ut *ut = dataplane_ut_new(&cfg);
		TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");
		shm = dataplane_ut_shm(ut);
		TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");

		// Time setter / getter round-trip.
		dataplane_ut_set_time_ns(ut, 12345ULL);
		TEST_ASSERT_EQUAL(
			(long)dataplane_ut_get_time_ns(ut),
			(long)12345ULL,
			"get_time_ns must echo set_time_ns"
		);

		struct dp_config *dp_config = yanet_shm_dp_config(shm, 0);
		TEST_ASSERT_EQUAL(
			dp_config->packet_recirc_limit,
			PACKET_RECIRC_LIMIT_DEFAULT,
			"test storage must use the default recirculation limit"
		);
		struct dp_worker **workers = ADDR_OF(&dp_config->workers);
		struct dp_worker *dp_worker = ADDR_OF(workers);
		struct cp_config *cp_config = ADDR_OF(&dp_config->cp_config);
		struct cp_config_gen *cp_config_gen =
			ADDR_OF(&cp_config->cp_config_gen);
		uint64_t iterations = *dp_worker->iterations;

		// The harness round uses shared preparation for mock time,
		// generation publication and iteration count.
		struct packet_list empty;
		packet_list_init(&empty);
		struct dataplane_ut_round_result result;
		dataplane_ut_run(ut, 0, &empty, &result);
		TEST_ASSERT_EQUAL(
			(long)dp_worker->current_time,
			(long)12345ULL,
			"mock time must reach the worker round"
		);
		TEST_ASSERT_EQUAL(
			(long)*dp_worker->iterations,
			(long)(iterations + 1),
			"one valid harness round must increment iterations once"
		);
		uint64_t published_gen =
			__atomic_load_n(&dp_worker->gen, __ATOMIC_ACQUIRE);
		TEST_ASSERT_EQUAL(
			(long)published_gen,
			(long)cp_config_gen->gen,
			"harness round must publish its generation"
		);

		// Mbuf factory yields a usable mbuf.
		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);
		rte_pktmbuf_free(mbuf);

		// Empty-input round must complete cleanly and yield empty
		// output and drop lists.
		dataplane_ut_run(ut, 0, &empty, &result);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&result.output),
			0L,
			"empty input must yield empty output"
		);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&result.drop),
			0L,
			"empty input must yield empty drop"
		);

		dataplane_ut_free(ut);
	}

	// NULL-safe free must not crash.
	dataplane_ut_free(NULL);

	// Input validation: NULL cfg must return NULL, not crash.
	struct dataplane_ut *bad = dataplane_ut_new(NULL);
	TEST_ASSERT_NULL(bad, "dataplane_ut_new(NULL) should return NULL");

	// Optional recirculation limits accept the production range and reject
	// invalid values before a harness is published.
	{
		struct dataplane_ut_config custom_cfg = cfg;
		custom_cfg.packet_recirc_limit = 37;
		struct dataplane_ut *custom = dataplane_ut_new(&custom_cfg);
		TEST_ASSERT_NOT_NULL(
			custom, "custom recirculation limit rejected"
		);
		struct dp_config *custom_dp_config =
			yanet_shm_dp_config(dataplane_ut_shm(custom), 0);
		TEST_ASSERT_EQUAL(
			custom_dp_config->packet_recirc_limit,
			37,
			"custom recirculation limit must reach runtime config"
		);
		dataplane_ut_free(custom);

		custom_cfg.packet_recirc_limit = 3;
		TEST_ASSERT_NULL(
			dataplane_ut_new(&custom_cfg),
			"recirculation limit below the accepted range must fail"
		);
		custom_cfg.packet_recirc_limit = 257;
		TEST_ASSERT_NULL(
			dataplane_ut_new(&custom_cfg),
			"recirculation limit above the accepted range must fail"
		);
	}

	// Input validation: a worker bound to a device_id outside the
	// configured topology must return NULL, not crash.
	{
		struct dataplane_ut_worker_spec out_of_range_workers[] = {
			{.device_id = 1, .queue_id = 0},
		};
		struct dataplane_ut_config bad_cfg = cfg;
		bad_cfg.workers = out_of_range_workers;

		struct dataplane_ut *bad_worker = dataplane_ut_new(&bad_cfg);
		TEST_ASSERT_NULL(
			bad_worker,
			"dataplane_ut_new with an out-of-range worker "
			"device_id should return NULL"
		);
	}

	// dataplane_ut_run with an out-of-range worker_idx must still leave
	// input drained and result well-formed, so a caller can inspect and
	// free result->output/result->drop exactly as on any other path.
	{
		struct dataplane_ut *ut = dataplane_ut_new(&cfg);
		TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");
		struct dp_config *dp_config =
			yanet_shm_dp_config(dataplane_ut_shm(ut), 0);
		struct dp_worker **workers = ADDR_OF(&dp_config->workers);
		struct dp_worker *dp_worker = ADDR_OF(workers);
		uint64_t iterations = *dp_worker->iterations;

		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);

		struct packet *packet = mbuf_to_packet(mbuf);
		memset(packet, 0, sizeof(*packet));
		packet->mbuf = mbuf;

		struct packet_list input;
		packet_list_init(&input);
		packet_list_add(&input, packet);

		struct dataplane_ut_round_result result;
		dataplane_ut_run(ut, cfg.worker_count + 1, &input, &result);

		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&input),
			0L,
			"an out-of-range worker_idx must still drain input"
		);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&result.output),
			0L,
			"an out-of-range worker_idx must yield empty output"
		);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&result.drop),
			1L,
			"an out-of-range worker_idx must drop the input packet"
		);
		TEST_ASSERT_EQUAL(
			(long)*dp_worker->iterations,
			(long)iterations,
			"an out-of-range worker_idx must not prepare a round"
		);

		struct packet *dropped;
		while ((dropped = packet_list_pop(&result.drop)) != NULL) {
			rte_pktmbuf_free(packet_to_mbuf(dropped));
		}

		// Recycled rounds must restore packet routing state after every
		// run.
		struct rte_mbuf *round_mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			round_mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);
		struct packet *round_packet = mbuf_to_packet(round_mbuf);
		memset(round_packet, 0, sizeof(*round_packet));
		round_packet->mbuf = round_mbuf;
		round_packet->recirc_remaining = 61;
		round_packet->recirc_initialized = 1;

		struct packet_list rounds_input;
		packet_list_init(&rounds_input);
		packet_list_add(&rounds_input, round_packet);
		TEST_ASSERT_SUCCESS(
			dataplane_ut_run_rounds(ut, 0, &rounds_input, 5, 0),
			"run_rounds failed"
		);
		TEST_ASSERT_EQUAL(
			(long)packet_list_count(&rounds_input),
			1L,
			"run_rounds must restore the fixed packet set"
		);
		TEST_ASSERT_EQUAL(
			(long)rounds_input.first->recirc_remaining,
			61L,
			"run_rounds must restore recirculation remaining budget"
		);
		TEST_ASSERT_EQUAL(
			(long)rounds_input.first->recirc_initialized,
			1L,
			"run_rounds must restore recirculation initialization "
			"state"
		);
		round_packet = packet_list_pop(&rounds_input);
		rte_pktmbuf_free(packet_to_mbuf(round_packet));

		dataplane_ut_free(ut);
	}

#ifdef YANET_DATAPLANE_UT_CONTROLPLANE
	TEST_ASSERT_SUCCESS(
		run_round_restore_test(),
		"run_rounds mutation and restore test failed"
	);
	TEST_ASSERT_SUCCESS(
		run_rounds_geometry_mismatch_test(),
		"run_rounds geometry rejection test failed"
	);
#endif

	return 0;
}
