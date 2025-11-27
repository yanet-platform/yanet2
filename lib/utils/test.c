#include <assert.h>
#include <netinet/in.h>

#include "common/test_assert.h"
#include "dataplane/packet/packet.h"
#include "logging/log.h"
#include "packet.h"

////////////////////////////////////////////////////////////////////////////////

static int
test_convert_packet() {
	struct packet packet;
	uint8_t src[] = {192, 166, 22, 33};
	uint8_t dst[] = {10, 101, 12, 3};

	int res = fill_packet(
		&packet, src, dst, 1010, 3032, IPPROTO_TCP, IPPROTO_IP, 0
	);
	TEST_ASSERT_EQUAL(0, res, "failed to fill packet");

	// get raw packet data

	struct packet_data pdata = packet_data(&packet);

	// fill packet with provided data

	struct packet packet1;
	res = fill_packet_from_data(&packet1, &pdata);
	TEST_ASSERT_EQUAL(0, res, "failed to fill packet from raw data");

	// free packets

	free_packet(&packet1);
	free_packet(&packet);

	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");

	LOG(INFO, "test convert packet...");
	TEST_ASSERT_SUCCESS(
		test_convert_packet(), "test convert packet failed"
	);

	LOG(INFO, "all tests successfully passed");

	return 0;
}