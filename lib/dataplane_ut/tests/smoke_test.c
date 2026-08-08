#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"

#include <rte_mbuf.h>

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

		dataplane_ut_free(ut);
	}

	return 0;
}
