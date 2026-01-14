#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>

FILTER_COMPILER_DECLARE(sign, port_src);
FILTER_QUERY_DECLARE(sign, port_src);

static void
run_case(void) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(1 << 24); // 16MB
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// one rule: src port 1024-5016 -> action 1
	struct filter_rule_builder b;
	builder_init(&b);
	builder_add_port_src_range(&b, 1024, 5016);
	struct filter_rule r = build_rule(&b, 1);

	// init filter
	struct filter f;
	res = FILTER_INIT(&f, sign, &r, 1, &memory_context);
	assert(res == 0);

	// craft packet: UDP 4000
	struct packet p = {0};
	uint8_t sip[NET4_LEN] = {0, 0, 0, 0};
	uint8_t dip[NET4_LEN] = {0, 0, 0, 0};
	res = fill_packet_net4(&p, sip, dip, 4000, 0, IPPROTO_UDP, 0);
	assert(res == 0);

	// query via header-only API
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(&f, sign, &packet_ptr, &actions, 1);
	assert(actions->count == 1);
	assert(ADDR_OF(&actions->values)[0] == 1);

	free_packet(&p);
	FILTER_FREE(&f, sign);
	free(memory);
}

int
main() {
	log_enable_name("debug");
	run_case();
	LOG(INFO, "OK");
	return 0;
}
