#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>

FILTER_COMPILER_DECLARE(sign_net4, net4_src, net4_dst);
FILTER_QUERY_DECLARE(sign_net4, net4_src, net4_dst);

static void
query_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(filter, sign_net4, &p, &actions, &actions_count);
	assert(actions_count >= 1);
	assert(actions[0] == expected);
	free_packet(&p);
}

static void
query_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	uint32_t *actions;
	uint32_t actions_count;
	FILTER_QUERY(filter, sign_net4, &p, &actions, &actions_count);
	assert(actions_count == 0);
	free_packet(&p);
}

int
main() {
	log_enable_name("debug");

	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 24); // 16MB
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	builder_add_net4_src(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	builder_add_net4_dst(
		&builder1, ip(192, 255, 168, 0), ip(255, 255, 255, 0)
	);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_net4, &action1, 1, &memory_context);
	assert(res == 0);

	query_and_expect_action(
		&filter, ip(192, 255, 168, 1), ip(192, 255, 168, 10), 1
	);

	// no action because src ip mismatch
	query_and_expect_no_action(
		&filter, ip(195, 255, 168, 1), ip(192, 255, 168, 10)
	);

	// no action because dst ip mismatch
	query_and_expect_no_action(
		&filter, ip(192, 255, 168, 10), ip(195, 255, 168, 1)
	);

	FILTER_FREE(&filter, sign_net4);

	free(memory);

	LOG(INFO, "OK!");
	return 0;
}