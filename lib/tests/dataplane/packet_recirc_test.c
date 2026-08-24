#include "common/test_assert.h"

#include "lib/dataplane/packet/packet.h"

static int
test_stalled_redirects_stop_at_stall_limit(void) {
	struct packet packet = {0};
	for (uint8_t idx = 0; idx < PACKET_RECIRC_STALL_LIMIT; ++idx) {
		TEST_ASSERT(
			packet_recirc_try_redirect(&packet, 16),
			"redirect within the stall limit must succeed"
		);
	}
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, 16),
		"redirect after the stall limit must fail"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_total_count,
		PACKET_RECIRC_STALL_LIMIT,
		"stalled redirects must consume total budget"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_stall_count,
		PACKET_RECIRC_STALL_LIMIT,
		"stalled redirects must consume stall budget"
	);
	return TEST_SUCCESS;
}

static int
test_progress_resets_only_stall_budget(void) {
	struct packet packet = {
		.recirc_total_count = 3,
		.recirc_stall_count = PACKET_RECIRC_STALL_LIMIT,
	};
	packet_recirc_mark_progress(&packet);
	TEST_ASSERT_EQUAL(
		packet.recirc_total_count,
		3,
		"progress must preserve total budget"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_stall_count, 0, "progress must reset stall budget"
	);
	TEST_ASSERT(
		packet_recirc_try_redirect(&packet, 16),
		"redirect after progress must succeed"
	);
	return TEST_SUCCESS;
}

static int
test_progress_cannot_bypass_total_budget(void) {
	struct packet packet = {0};
	for (uint16_t idx = 0; idx < 4; ++idx) {
		TEST_ASSERT(
			packet_recirc_try_redirect(&packet, 4),
			"redirect within total limit must succeed"
		);
		packet_recirc_mark_progress(&packet);
	}
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, 4),
		"progress must not bypass total limit"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_total_count,
		4,
		"total budget must remain exhausted"
	);
	return TEST_SUCCESS;
}

static int
test_maximum_total_budget(void) {
	struct packet packet = {0};
	for (uint16_t idx = 0; idx < PACKET_RECIRC_LIMIT_MAX; ++idx) {
		TEST_ASSERT(
			packet_recirc_try_redirect(
				&packet, PACKET_RECIRC_LIMIT_MAX
			),
			"redirect within maximum total limit must succeed"
		);
		packet_recirc_mark_progress(&packet);
	}
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, PACKET_RECIRC_LIMIT_MAX),
		"redirect after maximum total limit must fail"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_total_count,
		PACKET_RECIRC_LIMIT_MAX,
		"failed redirect must preserve the maximum total count"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	size_t failed = 0;
	if (test_stalled_redirects_stop_at_stall_limit() != TEST_SUCCESS) {
		++failed;
	}
	if (test_progress_resets_only_stall_budget() != TEST_SUCCESS) {
		++failed;
	}
	if (test_progress_cannot_bypass_total_budget() != TEST_SUCCESS) {
		++failed;
	}
	if (test_maximum_total_budget() != TEST_SUCCESS) {
		++failed;
	}
	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
