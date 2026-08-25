#include "common/test_assert.h"

#include "lib/dataplane/packet/packet.h"

static int
test_redirect_initializes_remaining_budget(void) {
	struct packet packet = {0};
	TEST_ASSERT(
		packet_recirc_try_redirect(&packet, 16),
		"first redirect must succeed"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_remaining,
		15,
		"first redirect must consume one total credit"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_initialized,
		1,
		"first redirect must initialize recirculation state"
	);
	return TEST_SUCCESS;
}

static int
test_total_budget_stops_at_limit(void) {
	struct packet packet = {0};
	for (uint16_t idx = 0; idx < 5; ++idx) {
		TEST_ASSERT(
			packet_recirc_try_redirect(&packet, 5),
			"redirect within the total limit must succeed"
		);
	}
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, 5),
		"redirect after the total limit must fail"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_remaining,
		0,
		"total failure must preserve the exhausted budget"
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
	}
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, PACKET_RECIRC_LIMIT_MAX),
		"redirect after maximum total limit must fail"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_remaining,
		0,
		"failed redirect must preserve exhausted total budget"
	);
	return TEST_SUCCESS;
}

static int
test_total_exhaustion_preserves_state(void) {
	struct packet packet = {
		.recirc_remaining = 0,
		.recirc_initialized = 1,
	};
	TEST_ASSERT(
		!packet_recirc_try_redirect(&packet, 64),
		"redirect after total exhaustion must fail"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_remaining,
		0,
		"total exhaustion must preserve remaining budget"
	);
	TEST_ASSERT_EQUAL(
		packet.recirc_initialized,
		1,
		"total exhaustion must preserve initialization state"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	size_t failed = 0;
	if (test_redirect_initializes_remaining_budget() != TEST_SUCCESS) {
		++failed;
	}
	if (test_total_budget_stops_at_limit() != TEST_SUCCESS) {
		++failed;
	}
	if (test_maximum_total_budget() != TEST_SUCCESS) {
		++failed;
	}
	if (test_total_exhaustion_preserves_state() != TEST_SUCCESS) {
		++failed;
	}
	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
