#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>

FILTER_COMPILER_DECLARE(sign_vlan, vlan);
FILTER_QUERY_DECLARE(sign_vlan, vlan);

////////////////////////////////////////////////////////////////////////////////

static void
query_packet(struct filter *filter, uint16_t vlan, uint32_t expected) {
	struct packet packet = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	int res = fill_packet_net4(&packet, sip, dip, 0, 0, IPPROTO_UDP, 0);
	assert(res == 0);
	packet.vlan = vlan;

	struct packet *packet_ptr = &packet;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_vlan, &packet_ptr, &actions, 1);
	assert(actions->count == 1);
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
	builder_set_vlan(&b1, 10);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_set_vlan(&b2, 20);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule_builder b3;
	builder_init(&b3);
	builder_set_vlan(&b3, 30);
	struct filter_rule r3 = build_rule(&b3, 3);

	struct filter_rule rules[3] = {r1, r2, r3};

	struct filter filter;
	res = FILTER_INIT(&filter, sign_vlan, rules, 3, &memory_context);
	assert(res == 0);

	query_packet(&filter, 10, 1);
	query_packet(&filter, 20, 2);
	query_packet(&filter, 30, 3);

	FILTER_FREE(&filter, sign_vlan);
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");
	void *memory = malloc(1 << 24);

	test_proto_1(memory);

	free(memory);

	LOG(INFO, "OK");

	return 0;
}
