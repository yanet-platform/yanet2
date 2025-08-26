#include "attribute.h"
#include "def.h"
#include "filter.h"
#include "gen.h"
#include "util.h"
#include "utils.h"
#include <assert.h>
#include <netinet/in.h>
#include <time.h>

// #define RULES_COUNT 289
#define RULES_COUNT 3000
// #define PACKETS 1000000
#define PACKETS 10000000
#define MEMORY ((size_t)1ull << 30)

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;
	// Generate rules
	struct filter_rule_holder rule_holder;
	generate_rules(&rule_holder, RULES_COUNT);

	// Init filter and memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	void *memory = malloc(MEMORY);
	block_allocator_put_arena(&allocator, memory, MEMORY);

	struct memory_context memory_context;
	int ret = memory_context_init(&memory_context, "bench", &allocator);
	assert(ret == 0);

	puts("filter init...");

	FILTER_DECLARE(
		sign,
		&attribute_proto,
		&attribute_net4_src,
		&attribute_net4_dst,
		&attribute_port_src,
		&attribute_port_dst
	);

	struct filter filter;
	clock_t tp = clock();
	FILTER_INIT(
		&filter,
		sign,
		rule_holder.rules,
		RULES_COUNT,
		&memory_context,
		&ret
	);
	assert(ret == 0);
	double filter_init_time = (double)(clock() - tp) / CLOCKS_PER_SEC;
	printf("Filter init time: %.2fs\n", filter_init_time);

	puts("dpdk init...");

	// Init dpdk rte
	ret = dpdk_init(argc, argv);
	if (ret != 0) {
		printf("failed to init dpdk (error code %d)\n", ret);
		return 1;
	}

	puts("dpdk acl init...");

	// Init dpdk acl
	struct dpdk_acl dpdk_acl;

	tp = clock();
	ret = dpdk_acl_init(&dpdk_acl, rule_holder.rules, RULES_COUNT);
	double dpdk_init_time = (double)(clock() - tp) / CLOCKS_PER_SEC;
	printf("DPDK ACL init time: %.2fs\n", dpdk_init_time);

	if (ret != 0) {
		printf("failed to init dpdk acl (error code %d)\n", ret);
		return 1;
	}

	struct packet *packets = malloc(PACKETS * sizeof(struct packet));
	for (size_t i = 0; i < PACKETS; ++i) {
		packets[i] = make_packet(
			ip(0xff, 0xff, i & 0xff, 1),
			ip(0xff, 0xff, (i + 1) & 0xff, 1),
			i & 127,
			(i * 17) & 255,
			IPPROTO_UDP,
			0,
			0
		);
	}

	// query dpdk acl
	uint32_t h = 0;
	tp = clock();
	for (size_t i = 0; i < PACKETS; ++i) {
		uint32_t action = dpdk_acl_classify(&dpdk_acl, &packets[i]);
		(void)action;
#ifdef DPDK_ACL_STRESS
		h ^= action;
#endif
	}
	double dpdk_classify_time = (double)(clock() - tp) / CLOCKS_PER_SEC;
	printf("DPDK ACL classify time: %.2fs\n", dpdk_classify_time);
	printf("DPDK ACL perfomance: %.2f MP/s\n",
	       (double)PACKETS / 1e6 / dpdk_classify_time);

	// query filter
	tp = clock();
	uint32_t *actions;
	uint32_t count;

	for (size_t i = 0; i < PACKETS; ++i) {
		FILTER_QUERY(&filter, sign, &packets[i], &actions, &count);
#ifdef STRESS
		if (count > 0) {
			assert(count == 1);
			h ^= actions[0];
		}
#endif
	}

	double filter_classify_time = (double)(clock() - tp) / CLOCKS_PER_SEC;
	printf("Filter classify time: %.2fs\n", filter_classify_time);
	printf("Filter perfomance: %.2f MP/s\n",
	       (double)PACKETS / 1e6 / filter_classify_time);

	assert(h == 0);

	for (size_t i = 0; i < PACKETS; ++i) {
		free_packet(&packets[i]);
	}
	free(packets);

	puts("OK");

	free(memory);

	return 0;
}