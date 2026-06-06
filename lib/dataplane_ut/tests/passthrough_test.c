#include "common/test_assert.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane_ut/dataplane_ut.h"

#include <rte_mbuf.h>

#include <string.h>

#define PACKET_COUNT 4
#define PACKET_DATA_LEN 64

static int
run_passthrough_test(void) {
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

	uint64_t tx_device_id;
	int rc = dataplane_ut_add_passthrough_pipeline(
		ut, "01:00.0", "pt0", &tx_device_id
	);
	TEST_ASSERT_EQUAL((long)rc, 0L, "add_passthrough_pipeline failed");
	TEST_ASSERT_EQUAL((long)tx_device_id, 0L, "device 0 must have index 0");

	// Allocate PACKET_COUNT mbufs and build an input packet_list.
	struct packet_list input;
	packet_list_init(&input);

	for (int idx = 0; idx < PACKET_COUNT; ++idx) {
		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);

		// Append a small payload so data_len is non-zero.
		char *buf = rte_pktmbuf_append(mbuf, PACKET_DATA_LEN);
		TEST_ASSERT_NOT_NULL(buf, "rte_pktmbuf_append returned NULL");
		memset(buf, 0, PACKET_DATA_LEN);

		// Initialise the packet header that lives at buf_addr.
		struct packet *pkt = mbuf_to_packet(mbuf);
		memset(pkt, 0, sizeof(struct packet));
		pkt->mbuf = mbuf;
		pkt->tx_device_id = (uint16_t)tx_device_id;

		packet_list_add(&input, pkt);
	}

	// Run one pipeline round.
	struct dataplane_ut_round_result result;
	dataplane_ut_run(ut, 0, &input, &result);

	// A passthrough pipeline must forward every packet, none dropped.
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.output),
		(long)PACKET_COUNT,
		"passthrough must forward all packets"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.drop),
		0L,
		"passthrough must not drop any packet"
	);

	// Free the output mbufs returned by the pipeline.
	struct packet *pkt;
	while ((pkt = packet_list_pop(&result.output)) != NULL) {
		rte_pktmbuf_free(pkt->mbuf);
	}

	dataplane_ut_free(ut);

	return TEST_SUCCESS;
}

// Verify that a passthrough pipeline bound to the SECOND of two configured
// devices correctly routes packets whose tx_device_id equals the second
// device's topology index (1).  Before the P2 fix this would crash because
// the device was inserted at the wrong registry slot.
static int
run_passthrough_test_two_devices(void) {
	const char *port_names[] = {"01:00.0", "02:00.0"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 2,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "dataplane_ut_new returned NULL");

	// Bind the passthrough pipeline to the SECOND device ("02:00.0").
	uint64_t tx_device_id;
	int rc = dataplane_ut_add_passthrough_pipeline(
		ut, "02:00.0", "pt1", &tx_device_id
	);
	TEST_ASSERT_EQUAL((long)rc, 0L, "add_passthrough_pipeline failed");
	// The second device must report topology index 1.
	TEST_ASSERT_EQUAL(
		(long)tx_device_id, 1L, "device 02:00.0 must have index 1"
	);

	// Allocate PACKET_COUNT mbufs and address them to the second device.
	struct packet_list input;
	packet_list_init(&input);

	for (int idx = 0; idx < PACKET_COUNT; ++idx) {
		struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
		TEST_ASSERT_NOT_NULL(
			mbuf, "dataplane_ut_alloc_mbuf returned NULL"
		);

		char *buf = rte_pktmbuf_append(mbuf, PACKET_DATA_LEN);
		TEST_ASSERT_NOT_NULL(buf, "rte_pktmbuf_append returned NULL");
		memset(buf, 0, PACKET_DATA_LEN);

		struct packet *pkt = mbuf_to_packet(mbuf);
		memset(pkt, 0, sizeof(struct packet));
		pkt->mbuf = mbuf;
		pkt->tx_device_id = (uint16_t)tx_device_id;

		packet_list_add(&input, pkt);
	}

	// Run one pipeline round.
	struct dataplane_ut_round_result result;
	dataplane_ut_run(ut, 0, &input, &result);

	// All packets must exit via output, none dropped.
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.output),
		(long)PACKET_COUNT,
		"passthrough must forward all packets"
	);
	TEST_ASSERT_EQUAL(
		(long)packet_list_count(&result.drop),
		0L,
		"passthrough must not drop any packet"
	);

	struct packet *pkt;
	while ((pkt = packet_list_pop(&result.output)) != NULL) {
		rte_pktmbuf_free(pkt->mbuf);
	}

	dataplane_ut_free(ut);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	if (run_passthrough_test() != TEST_SUCCESS) {
		return 1;
	}
	if (run_passthrough_test_two_devices() != TEST_SUCCESS) {
		return 1;
	}
	return 0;
}
