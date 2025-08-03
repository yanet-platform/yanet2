#include "attribute.h"
#include "filter.h"
#include "utils.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdio.h>

void
test(void *memory, const struct filter_attribute *attrs[4]) {
	// init memory
	struct block_allocator allocator;
	block_allocator_init(&allocator);

	block_allocator_put_arena(&allocator, memory, 1 << 26);

	struct memory_context memory_context;
	int res = memory_context_init(&memory_context, "test", &allocator);
	assert(res == 0);

	// actions

	// a1:
	//  src_port: 100-500
	//  dst_port: 200-250
	//  net4_src: 198.233.0.0/16
	//  net4_dst: 192.0.0.0/8
	struct filter_rule_builder b1;
	builder_init(&b1);
	builder_add_port_src_range(&b1, 100, 500);
	builder_add_port_dst_range(&b1, 200, 250);
	builder_add_net4_src(&b1, ip(198, 233, 0, 0), ip(255, 255, 0, 0));
	builder_add_net4_dst(&b1, ip(192, 0, 0, 0), ip(255, 0, 0, 0));
	struct filter_rule a1 = build_rule(&b1, 1);

	// a2:
	//  src_port: 200-300
	//  dst_port: 100-300
	//  net4_src: 198.233.10.0/24
	//  net4_dst: 192.0.0.0/8
	struct filter_rule_builder b2;
	builder_init(&b2);
	builder_add_port_src_range(&b2, 200, 300);
	builder_add_port_dst_range(&b2, 100, 300);
	builder_add_net4_src(&b2, ip(198, 233, 10, 0), ip(255, 255, 255, 0));
	builder_add_net4_dst(&b2, ip(192, 0, 0, 0), ip(255, 0, 0, 0));
	struct filter_rule a2 = build_rule(&b2, 2);

	struct filter_rule actions[2] = {a1, a2};

	// build filter
	struct filter filter;
	res = filter_init(&filter, attrs, 4, actions, 2, &memory_context);
	assert(res == 0);

	// make queries

	{
		struct packet p = make_packet(
			ip(198, 233, 10, 15),
			ip(192, 1, 1, 1),
			200,
			230,
			IPPROTO_UDP,
			0,
			0
		);
		query_filter_and_expect_action(&filter, &p, 1);
		free_packet(&p);
	}

	{
		struct packet p = make_packet(
			ip(198, 233, 10, 15),
			ip(192, 1, 1, 1),
			200,
			150,
			IPPROTO_UDP,
			0,
			0
		);
		query_filter_and_expect_action(&filter, &p, 2);
		free_packet(&p);
	}

	filter_free(&filter);
}

////////////////////////////////////////////////////////////////////////////////

int
next_permutation(uint32_t *a, size_t n) {
	if (n <= 1) {
		return 0;
	}
	for (ssize_t i = n - 2; i >= 0; --i) {
		if (a[i] < a[i + 1]) {
			// find the least a[j] s.t a[j] > a[i]
			for (ssize_t j = n - 1; j > i; --j) {
				if (a[j] > a[i]) {
					// swap (a[i], a[j])
					uint32_t tmp = a[i];
					a[i] = a[j];
					a[j] = tmp;
					break;
				}
			}
			// reverse prefix
			size_t len = n - i - 1;
			for (size_t j = 0; j < len / 2; ++j) {
				// swap (a[i + j + 1, a[n - j - 1]])
				uint32_t tmp = a[i + j + 1];
				a[i + j + 1] = a[n - j - 1];
				a[n - j - 1] = tmp;
			}
			return 1;
		}
	}
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	void *memory = malloc(1 << 26); // 64MB

	uint32_t perm[4] = {0, 1, 2, 3};

	const struct filter_attribute *attrs[4] = {
		&attribute_port_src,
		&attribute_port_dst,
		&attribute_net4_src,
		&attribute_net4_dst,
	};

	uint32_t check_counter = 0;
	do {
		const struct filter_attribute *a[4];
		for (size_t i = 0; i < 4; ++i) {
			a[i] = attrs[perm[i]];
		}
		test(memory, a);
		++check_counter;
	} while (next_permutation(perm, 4));

	assert(check_counter == 24);

	puts("OK");
	printf("checked %u attribute permutations\n", check_counter);

	free(memory);

	return 0;
}