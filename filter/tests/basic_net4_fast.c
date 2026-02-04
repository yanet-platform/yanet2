#include "common/test_assert.h"
#include "filter/compiler.h"
#include "filter/filter.h"
#include "filter/query.h"

#include "filter/tests/helpers.h"
#include "lib/utils/packet.h"

#include "logging/log.h"
#include <assert.h>
#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

FILTER_COMPILER_DECLARE(sign_net4, net4_fast_src, net4_fast_dst);
FILTER_QUERY_DECLARE(sign_net4, net4_fast_src, net4_fast_dst);

static int
query_and_expect_action(
	struct filter *filter,
	uint8_t sip[NET4_LEN],
	uint8_t dip[NET4_LEN],
	uint32_t expected
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");

	// Check what's in the packet header
	struct rte_mbuf *mbuf = packet_to_mbuf(&p);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, p.network_header.offset
	);

	LOG(INFO,
	    "Query packet src: %d.%d.%d.%d (0x%08x), dst: %d.%d.%d.%d (0x%08x)",
	    sip[0],
	    sip[1],
	    sip[2],
	    sip[3],
	    ipv4_hdr->src_addr,
	    dip[0],
	    dip[1],
	    dip[2],
	    dip[3],
	    ipv4_hdr->dst_addr);

	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_net4, &packet_ptr, &actions, 1);

	LOG(INFO,
	    "Query result: actions->count = %lu",
	    (unsigned long)actions->count);

	free_packet(&p);
	TEST_ASSERT(actions->count >= 1, "at least one action expected");
	TEST_ASSERT_EQUAL(
		expected, ADDR_OF(&actions->values)[0], "expected mismatch"
	);
	return TEST_SUCCESS;
}

static int
query_and_expect_no_action(
	struct filter *filter, uint8_t sip[NET4_LEN], uint8_t dip[NET4_LEN]
) {
	struct packet p = {0};
	int res = fill_packet_net4(&p, sip, dip, 0, 0, IPPROTO_UDP, 0);
	TEST_ASSERT_EQUAL(res, 0, "failed to fill packet net4");
	struct packet *packet_ptr = &p;
	struct value_range *actions;
	FILTER_QUERY(filter, sign_net4, &packet_ptr, &actions, 1);
	free_packet(&p);
	TEST_ASSERT_EQUAL(actions->count, 0, "got unexpected actions");
	return TEST_SUCCESS;
}

#define MEMORY_SIZE (1 << 24)

static int
test1(void *memory) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, 1 << 24);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// action 1:
	struct filter_rule_builder builder1;
	builder_init(&builder1);
	uint8_t *src_net = ip(192, 255, 168, 0);
	uint8_t *src_mask = ip(255, 255, 255, 0);
	uint8_t *dst_net = ip(192, 255, 168, 0);
	uint8_t *dst_mask = ip(255, 255, 255, 0);

	LOG(INFO,
	    "Rule src network: %d.%d.%d.%d/%d.%d.%d.%d",
	    src_net[0],
	    src_net[1],
	    src_net[2],
	    src_net[3],
	    src_mask[0],
	    src_mask[1],
	    src_mask[2],
	    src_mask[3]);
	LOG(INFO,
	    "Rule dst network: %d.%d.%d.%d/%d.%d.%d.%d",
	    dst_net[0],
	    dst_net[1],
	    dst_net[2],
	    dst_net[3],
	    dst_mask[0],
	    dst_mask[1],
	    dst_mask[2],
	    dst_mask[3]);

	builder_add_net4_src(&builder1, src_net, src_mask);
	builder_add_net4_dst(&builder1, dst_net, dst_mask);
	struct filter_rule action1 = build_rule(&builder1, 1);

	// init filter
	struct filter filter;
	res = FILTER_INIT(&filter, sign_net4, &action1, 1, &memory_context);
	TEST_ASSERT_EQUAL(res, 0, "failed to initialize filter");

	res = query_and_expect_action(
		&filter, ip(192, 255, 168, 1), ip(192, 255, 168, 10), 1
	);
	TEST_ASSERT_SUCCESS(res, "failed to query first packet");

	// no action because src ip mismatch
	res = query_and_expect_no_action(
		&filter, ip(195, 255, 168, 1), ip(192, 255, 168, 10)
	);
	TEST_ASSERT_SUCCESS(
		res, "failed to query second packet (src_ip mismatch)"
	);

	// no action because dst ip mismatch
	query_and_expect_no_action(
		&filter, ip(192, 255, 168, 10), ip(195, 255, 168, 1)
	);
	TEST_ASSERT_SUCCESS(
		res, "failed to query second packet (dst_ip mismatch)"
	);

	FILTER_FREE(&filter, sign_net4);

	return TEST_SUCCESS;
}

int
main() {
	log_enable_name("debug");

	void *memory = malloc(1 << 24); // 16MB

	size_t failed_count = 0;

	if (test1(memory) != TEST_SUCCESS) {
		LOG(ERROR, "test 1 failed");
		++failed_count;
	}

	if (failed_count == 0) {
		LOG(INFO, "all tests passed");
	} else {
		LOG(ERROR, "failed %zu tests", failed_count);
	}

	free(memory);

	return (failed_count > 0 ? 1 : 0);
}
