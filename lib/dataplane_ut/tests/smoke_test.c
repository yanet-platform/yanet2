#include "common/test_assert.h"
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

		// Time setter / getter round-trip.
		dataplane_ut_set_time_ns(ut, 12345ULL);
		TEST_ASSERT_EQUAL(
			(long)dataplane_ut_get_time_ns(ut),
			(long)12345ULL,
			"get_time_ns must echo set_time_ns"
		);

		// Mbuf factory yields a usable mbuf.
		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);
		rte_pktmbuf_free(mbuf);

		// Empty-input round must complete cleanly and yield empty
		// output and drop lists.
		struct packet_list empty;
		packet_list_init(&empty);
		struct dataplane_ut_round_result result;
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

	return 0;
}
