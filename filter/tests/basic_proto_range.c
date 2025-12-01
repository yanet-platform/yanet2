#include "rule.h"
#include "utils.h"

#include "attribute.h"
#include "common/memory_block.h"
#include "filter.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_tcp.h>

#include <assert.h>

#include <lib/logging/log.h>

void
query_tcp_packet(struct filter *filter, uint16_t flags, uint32_t expected) {
	struct packet packet = make_packet4(
		ip(0, 0, 0, 0), ip(0, 0, 0, 0), 0, 0, IPPROTO_TCP, flags, 0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
}

void
query_udp_packet(struct filter *filter, uint32_t expected) {
	struct packet packet = make_packet4(
		ip(0, 0, 0, 0), ip(0, 0, 0, 0), 0, 0, IPPROTO_UDP, 0, 0
	);
	query_filter_and_expect_action(filter, &packet, expected);
	free_packet(&packet);
}

////////////////////////////////////////////////////////////////////////////////

void
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
	builder_add_proto_range(&b1, IPPROTO_TCP << 8, IPPROTO_TCP << 8);
	struct filter_rule r1 = build_rule(&b1, 1);

	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_proto_range(&b2, IPPROTO_UDP << 8, IPPROTO_UDP << 8);
	struct filter_rule r2 = build_rule(&b2, 2);

	struct filter_rule rules[2] = {r1, r2};

	const struct filter_attribute *attrs[1] = {&attribute_proto_range};

	struct filter filter;

	LOG(INFO, "filter init...");
	res = filter_init(&filter, attrs, 1, rules, 2, &memory_context);
	assert(res == 0);

	LOG(INFO, "query tcp packet...");
	query_tcp_packet(&filter, 0, 1);

	LOG(INFO, "query udp packet...");
	query_udp_packet(&filter, 2);

	filter_free(&filter);
}

void setup_filter_with_tcp_rules(
    struct filter *filter,
    struct memory_context *memory_context
) {
    struct filter_rule_builder b1, b2, b3, b4;

    builder_init(&b1);
    builder_add_proto_range(&b1, (IPPROTO_TCP << 8) | RTE_TCP_SYN_FLAG, (IPPROTO_TCP << 8) | RTE_TCP_SYN_FLAG);
    struct filter_rule r1 = build_rule(&b1, 1);

    builder_init(&b2);
    builder_add_proto_range(&b2, (IPPROTO_TCP << 8) | RTE_TCP_ACK_FLAG, (IPPROTO_TCP << 8) | RTE_TCP_ACK_FLAG);
    struct filter_rule r2 = build_rule(&b2, 2);

    builder_init(&b3);
    builder_add_proto_range(&b3, (IPPROTO_TCP << 8) | RTE_TCP_FIN_FLAG, (IPPROTO_TCP << 8) | RTE_TCP_FIN_FLAG);
    struct filter_rule r3 = build_rule(&b3, 3);

    builder_init(&b4);
    builder_add_proto_range(&b4, (IPPROTO_TCP << 8) | (RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG), (IPPROTO_TCP << 8) | (RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG));
    struct filter_rule r4 = build_rule(&b4, 4);

    struct filter_rule rules[] = {r1, r2, r3, r4};
    const size_t rule_count = sizeof(rules) / sizeof(rules[0]);

    const struct filter_attribute *attrs[1] = {&attribute_proto_range};

    int res = filter_init(filter, attrs, 1, rules, rule_count, memory_context);
    assert(res == 0);
}

void test_tcp_flags(struct filter *filter) {
    LOG(INFO, "Testing TCP packets with different flags...");

	LOG(DEBUG, "Testing TCP SYN flag");
    struct packet syn_packet = make_packet4(
        ip(192, 168, 1, 1), ip(192, 168, 1, 2),
        1234, 80,
        IPPROTO_TCP,
        RTE_TCP_SYN_FLAG,
        0
    );
    query_filter_and_expect_action(filter, &syn_packet, 1);
    free_packet(&syn_packet);

	LOG(DEBUG, "Testing TCP ACK flag");
    struct packet ack_packet = make_packet4(
        ip(192, 168, 1, 1), ip(192, 168, 1, 2),
        1234, 80,
        IPPROTO_TCP,
        RTE_TCP_ACK_FLAG,
        0
    );
    query_filter_and_expect_action(filter, &ack_packet, 2);
    free_packet(&ack_packet);

	LOG(DEBUG, "Testing TCP FIN flag");
    struct packet fin_packet = make_packet4(
        ip(192, 168, 1, 1), ip(192, 168, 1, 2),
        1234, 80,
        IPPROTO_TCP,
        RTE_TCP_FIN_FLAG,
        0
    );
    query_filter_and_expect_action(filter, &fin_packet, 3);
    free_packet(&fin_packet);

	LOG(DEBUG, "Testing TCP SYN+ACK flag");
    struct packet synack_packet = make_packet4(
        ip(192, 168, 1, 1), ip(192, 168, 1, 2),
        1234, 80,
        IPPROTO_TCP,
        (RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG),
        0
    );
    query_filter_and_expect_action(filter, &synack_packet, 4);
    free_packet(&synack_packet);
}

void test_proto_extended(void *memory) {
    // init memory
    struct block_allocator allocator;
    block_allocator_init(&allocator);
    block_allocator_put_arena(&allocator, memory, 1 << 24);

    struct memory_context memory_context;
    int res = memory_context_init(&memory_context, "test", &allocator);
    assert(res == 0);

    struct filter filter;

    LOG(INFO, "Setting up filter with TCP/ICMP rules...");
    setup_filter_with_tcp_rules(&filter, &memory_context);

    LOG(INFO, "Running TCP flags tests...");
    test_tcp_flags(&filter);

    LOG(INFO, "Freeing filter...");
    filter_free(&filter);

    LOG(INFO, "All TCP/ICMP tests passed.");
}

int main() {
    log_enable_name("debug");

    void *memory = malloc(1 << 24);
    assert(memory != NULL);

    test_proto_1(memory);
    test_proto_extended(memory);

    free(memory);

    LOG(INFO, "passed");
    return 0;
}
