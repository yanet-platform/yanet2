#include "../filter.h"
#include "../ipfw.h"
#include "attribute.h"
#include "utils.h"

#include <assert.h>
#include <netinet/in.h>
#include <time.h>

#include <rte_ip.h>
#include <rte_mbuf_core.h>
#include <rte_tcp.h>
#include <rte_udp.h>

////////////////////////////////////////////////////////////////////////////////

#define MAX_IP 16
#define MAX_PORT 256
#define MEMORY (1 << 24)
#define PACKETS 10000

////////////////////////////////////////////////////////////////////////////////

struct packet *
gen_packets(size_t count) {
	struct packet *packets = malloc(sizeof(struct packet) * count);
	int g = 3;
	for (size_t i = 0; i < count; ++i) {
		g += 13 * 17;
		g %= MAX_IP;
		int src_ip = g + 1;

		g += 13 * 17;
		g %= MAX_IP;
		int dst_ip = g + 1;

		uint16_t src_port = (123 * i + 17) % MAX_PORT;
		uint16_t dst_port = (127 * i + 121) % MAX_PORT;

		packets[i] = make_packet(
			ip(src_ip, 1, 1, 5),
			ip(dst_ip, 2, 3, 1),
			src_port,
			dst_port,
			IPPROTO_UDP,
			0,
			0
		);
	}
	return packets;
}

////////////////////////////////////////////////////////////////////////////////

int
query_filter_compiler(
	struct filter_compiler *filter_compiler,
	struct packet *packet,
	uint32_t *rule_count,
	uint32_t **rules
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint32_t src_net = lpm4_lookup(
		&filter_compiler->src_net4, (uint8_t *)&ipv4_hdr->src_addr
	);
	uint32_t dst_net = lpm4_lookup(
		&filter_compiler->dst_net4, (uint8_t *)&ipv4_hdr->dst_addr
	);

	uint32_t src_port = value_table_get(
		&filter_compiler->src_port4, 0, packet_src_port(packet)
	);
	uint32_t dst_port = value_table_get(
		&filter_compiler->dst_port4, 0, packet_dst_port(packet)
	);

	uint32_t net = value_table_get(
		&filter_compiler->v4_lookups.network, src_net, dst_net
	);
	uint32_t transport = value_table_get(
		&filter_compiler->v4_lookups.transport_port, src_port, dst_port
	);
	uint32_t result = value_table_get(
		&filter_compiler->v4_lookups.result, net, transport
	);

	struct value_range *range =
		ADDR_OF(&filter_compiler->v4_lookups.result_registry.ranges) +
		result;
	*rules = ADDR_OF(&range->values);
	*rule_count = range->count;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(MEMORY);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	const struct filter_attribute *attributes[4] = {
		&attribute_net4_src,
		&attribute_net4_dst,
		&attribute_port_src,
		&attribute_port_dst
	};

	// generate rules
	int g = 0;
	struct filter_rule *rules =
		malloc(sizeof(struct filter_rule) * MAX_IP * MAX_IP);
	struct filter_rule_builder *builders =
		malloc(sizeof(struct filter_rule_builder) * MAX_IP * MAX_IP);
	for (int i = 0; i < MAX_IP; ++i) {
		for (int j = 0; j < MAX_IP; ++j) {
			g += 123 * 15;
			g %= MAX_PORT;
			uint16_t src_port1 = g;

			g += 123 * 15;
			g %= MAX_PORT;
			uint16_t src_port2 = g;

			if (src_port2 < src_port1) {
				uint16_t t = src_port2;
				src_port2 = src_port1;
				src_port1 = t;
			}

			g += 123 * 15;
			g %= MAX_PORT;
			uint16_t dst_port1 = g;

			g += 123 * 15;
			g %= MAX_PORT;
			uint16_t dst_port2 = g;

			if (dst_port2 < dst_port1) {
				uint16_t t = dst_port2;
				dst_port2 = dst_port1;
				dst_port1 = t;
			}

			uint32_t src_ip = ip(i + 1, 0, 0, 0);
			uint32_t dst_ip = ip(j + 1, 0, 0, 0);

			struct filter_rule_builder *builder =
				&builders[i * MAX_IP + j];
			builder_init(builder);
			builder_add_port_src_range(
				builder, src_port1, src_port2
			);
			builder_add_port_dst_range(
				builder, dst_port1, dst_port2
			);
			builder_add_net4_src(builder, src_ip, ip(255, 0, 0, 0));
			builder_add_net4_dst(builder, dst_ip, ip(255, 0, 0, 0));
			rules[i * MAX_IP + j] = build_rule(builder, i + j);
		}
	}

	clock_t new_filter_init_start_time = clock();
	struct filter filter;
	res = filter_init(
		&filter, attributes, 4, rules, MAX_IP * MAX_IP, &memory_context
	);
	assert(res == 0);
	double filter_init_time =
		(double)((clock() - new_filter_init_start_time)) /
		CLOCKS_PER_SEC;
	printf("New filter init time: %.4f seconds\n", filter_init_time);

	// init memory for old filter
	struct block_allocator allocator1;
	block_allocator_init(&allocator1);
	void *memory1 = malloc(MEMORY);
	block_allocator_put_arena(&allocator1, memory1, MEMORY);

	struct memory_context memory_context1;
	res = memory_context_init(&memory_context1, "test_prev", &allocator1);
	assert(res == 0);

	clock_t old_filter_init_start_time = clock();
	struct filter_compiler filter_compiler;
	res = filter_compiler_init(
		&filter_compiler, &memory_context1, rules, MAX_IP * MAX_IP
	);
	assert(res == 0);
	double old_filter_init_time =
		(double)((clock() - old_filter_init_start_time)) /
		CLOCKS_PER_SEC;
	printf("Old filter init time: %.4f seconds\n", old_filter_init_time);

	struct packet *packets = gen_packets(PACKETS);

	clock_t filter_query_start_time = clock();
	uint32_t new_filter_checksum = 0;
	for (size_t i = 0; i < PACKETS; ++i) {
		uint32_t *actions;
		uint32_t actions_count;
		filter_query(&filter, &packets[i], &actions, &actions_count);
		new_filter_checksum ^= actions_count;
		for (size_t j = 0; j < actions_count; ++j) {
			new_filter_checksum ^= actions[j];
		}
	}
	double new_filter_query_time =
		(double)((clock() - filter_query_start_time)) / CLOCKS_PER_SEC;
	printf("New filter summary query time: %.4f seconds (%.2f mp/s)\n",
	       new_filter_query_time,
	       (double)PACKETS / new_filter_query_time / 1e6);

	clock_t old_filter_query_start_time = clock();
	uint32_t old_filter_checksum = 0;

	for (size_t i = 0; i < PACKETS; ++i) {
		uint32_t *actions;
		uint32_t actions_count;
		query_filter_compiler(
			&filter_compiler, &packets[i], &actions_count, &actions
		);
		old_filter_checksum ^= actions_count;
		for (size_t j = 0; j < actions_count; ++j) {
			old_filter_checksum ^= actions[j];
		}
	}
	double old_filter_query_time =
		(double)((clock() - old_filter_query_start_time)) /
		CLOCKS_PER_SEC;
	printf("Old filter summary query time: %.4f seconds (%.2f mp/s)\n",
	       old_filter_query_time,
	       (double)PACKETS / old_filter_query_time / 1e6);

	assert(old_filter_checksum == new_filter_checksum);

	puts("OK");

	filter_free(&filter);

	free(memory);
	free(memory1);

	for (size_t i = 0; i < PACKETS; ++i) {
		free_packet(&packets[i]);
	}
	free(packets);

	return 0;
}
