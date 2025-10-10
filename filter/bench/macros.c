#include "../filter.h"
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

#define MAX_IP 32
#define MAX_PORT 512
#define MEMORY (1 << 28)
#define PACKETS 1000000

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

		packets[i] = make_packet4(
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
main() {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(MEMORY);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

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

			uint8_t *src_ip = ip(i + 1, 0, 0, 0);
			uint8_t *dst_ip = ip(j + 1, 0, 0, 0);

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

	// declare filter
	FILTER_DECLARE(
		sign,
		&attribute_net4_src,
		&attribute_net4_dst,
		&attribute_port_src,
		&attribute_port_dst
	);

	clock_t init_time = clock();

	struct filter filter;
	res = FILTER_INIT(
		&filter, sign, rules, MAX_IP * MAX_IP, &memory_context
	);
	assert(res == 0);
	double filter_init_time =
		(double)((clock() - init_time)) / CLOCKS_PER_SEC;
	printf("Filter init time: %.4f seconds\n", filter_init_time);

	struct packet *packets = gen_packets(PACKETS);

	clock_t filter_query_start_time = clock();
	for (size_t i = 0; i < PACKETS; ++i) {
		uint32_t *actions;
		uint32_t actions_count;
		FILTER_QUERY(
			&filter, sign, &packets[i], &actions, &actions_count
		);
	}
	double query_time =
		(double)((clock() - filter_query_start_time)) / CLOCKS_PER_SEC;
	printf("Filter summary query time: %.4f seconds (%.2f "
	       "mp/s)\n",
	       query_time,
	       (double)PACKETS / query_time / 1e6);

	puts("OK");

	FILTER_FREE(&filter, sign);

	free(memory);

	for (size_t i = 0; i < PACKETS; ++i) {
		free_packet(&packets[i]);
	}
	free(packets);

	return 0;
}
