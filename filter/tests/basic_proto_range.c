#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>

FILTER_COMPILER_DECLARE(sign_proto_range, proto_range);
FILTER_QUERY_DECLARE(sign_proto_range, proto_range);

static void
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_TCP, flags);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&packet);
}

static void
query_udp_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_proto_range, &packet_ptr, &actions, 1);
	assert(actions->count >= 1);
	assert(ADDR_OF(&actions->values)[0] == expected);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

static void
test_proto_1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_proto_range(
		&b1, 256 * IPPROTO_TCP, 256 * IPPROTO_TCP + 255
	);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(
		&b2, 256 * IPPROTO_UDP, 256 * IPPROTO_UDP + 255
	);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule rules[2] = {r1, r2};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = FILTER_INIT(&filter, sign_proto_range, rules, 2, &memory_context);
	assert(res == 0);

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 1);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 2);

	FILTER_FREE(&filter, sign_proto_range);
}

int
main() {
	log_enable_name("debug");

	void *memory = malloc(1 << 24);

	test_proto_1(memory);

	free(memory);

	LOG(INFO, "passed");

	return 0;
}
