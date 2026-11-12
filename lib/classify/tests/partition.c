// Partition test of the region-based classification library.
//
// Pins the prefix partition built from the sorted prefix ends to the
// radix range collector and range index as the oracle: the same piece
// starts, region values and region count, the same piece bounds of
// every prefix, the same exact boundary lookups, a structurally
// identical LPM and identical lookups through it. The scenarios cover
// the edge sets of every key width - empty, wildcard, single, nested,
// duplicate, full length, all-zero and all-ones prefixes - and the
// classifier measurement workloads scaled down: random IPv4 networks,
// contiguous, clustered bi-contiguous and mixed IPv6 networks over both
// address halves.

#include "lib/classify/compiler/partition.h"

#include "common/key.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/radix.h"
#include "common/range_collector.h"
#include "common/range_index.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define TEST_ARENA_SIZE ((size_t)1 << 28)

// The oracle partition: the radix range collector collected into a
// range index.
struct test_reference {
	struct range_collector collector;
	struct range_index index;
	uint8_t key_size;
};

// Builds the oracle partition of the prefixes: the radix collector
// collected into a range index.
static void
test_reference_build(
	struct memory_context *mctx,
	uint8_t key_bits,
	const struct classify_prefix *prefixes,
	uint32_t count,
	struct test_reference *ref
) {
	ref->key_size = key_bits / 8;
	assert(range_collector_init(&ref->collector, mctx) == 0);
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint8_t key[8];
		classify_key_store(prefixes[idx].from, key_bits, key);
		assert(range_collector_add(
			       &ref->collector,
			       ref->key_size,
			       key,
			       (uint8_t)prefixes[idx].len
		       ) == 0);
	}
	assert(range_index_init(&ref->index, mctx) == 0);
	assert(range_collector_collect(
		       &ref->collector, ref->key_size, &ref->index
	       ) == 0);
}

// Releases the oracle partition.
static void
test_reference_free(struct test_reference *ref) {
	range_index_free(&ref->index);
	range_collector_free(&ref->collector, ref->key_size);
}

// Walk state checking the pieces of the partition against the range
// starts of the oracle index in order.
struct test_walk_ctx {
	const struct classify_partition *partition;
	uint32_t next;
};

// Checks one oracle range start against the next piece of the partition.
static int
test_walk_piece(
	uint8_t key_size, const uint8_t *key, uint32_t index, void *data
) {
	struct test_walk_ctx *ctx = data;
	assert(index == ctx->next);
	assert(index < ctx->partition->count);
	assert(classify_key_load(key, key_size) == ctx->partition->starts[index]
	);
	++ctx->next;
	return 0;
}

// Asserts that both tries hold the same values in the same page shape,
// walking them in parallel from the roots.
static void
test_assert_lpm_page_equal(
	const struct lpm_page *first, const struct lpm_page *second
) {
	for (uint32_t slot = 0; slot < 256; ++slot) {
		const union lpm_value *first_value = first->values + slot;
		const union lpm_value *second_value = second->values + slot;
		if (first_value->value & LPM_VALUE_FLAG) {
			assert(first_value->value == second_value->value);
			continue;
		}
		assert(!(second_value->value & LPM_VALUE_FLAG));
		test_assert_lpm_page_equal(
			ADDR_OF(&first_value->page),
			ADDR_OF(&second_value->page)
		);
	}
}

// The bounds of a prefix through the oracle radix lookups.
static void
test_reference_bounds(
	const struct test_reference *ref,
	uint8_t key_bits,
	uint64_t from,
	uint64_t len,
	uint32_t *start,
	uint32_t *stop
) {
	uint8_t from_key[8];
	uint8_t to_key[8];
	classify_key_store(from, key_bits, from_key);
	classify_key_store(
		classify_prefix_to(key_bits, from, len), key_bits, to_key
	);
	filter_key_inc(ref->key_size, to_key);

	*start = radix_lookup(&ref->index.radix, ref->key_size, from_key);
	*stop = radix_lookup(&ref->index.radix, ref->key_size, to_key);
	if (*stop == 0) {
		*stop = ref->index.count;
	}
}

// Asserts that the exact boundary lookup of the key agrees with the
// oracle, a key inside a piece included.
static void
test_assert_find(
	const struct test_reference *ref,
	const struct classify_partition *partition,
	uint8_t key_bits,
	uint64_t key
) {
	uint8_t bytes[8];
	classify_key_store(key, key_bits, bytes);
	assert(classify_partition_find(partition, key) ==
	       radix_lookup(&ref->index.radix, ref->key_size, bytes));
}

// Asserts that the oracle and the partition build the same LPM with the
// values: the same page shape and the same lookups at every piece start
// and the key before it.
static void
test_assert_lpm(
	const struct test_reference *ref,
	const struct classify_partition *partition,
	struct memory_context *mctx,
	const uint32_t *values
) {
	struct lpm expected;
	struct lpm actual;
	assert(lpm_init(&expected, mctx, "expected") == 0);
	assert(lpm_init(&actual, mctx, "actual") == 0);

	assert(range_index_build_lpm_values(
		       &ref->index, ref->key_size, values, &expected
	       ) == 0);
	assert(classify_partition_build_lpm(partition, values, &actual) == 0);

	assert(expected.page_count == actual.page_count);
	test_assert_lpm_page_equal(
		lpm_page(&expected, 0), lpm_page(&actual, 0)
	);

	for (uint32_t idx = 0; idx < partition->count; ++idx) {
		uint64_t keys[2] = {
			partition->starts[idx],
			(partition->starts[idx] - 1) &
				classify_key_max(partition->key_bits),
		};
		for (uint32_t key_idx = 0; key_idx < 2; ++key_idx) {
			uint8_t key[8];
			classify_key_store(
				keys[key_idx], partition->key_bits, key
			);
			assert(lpm_lookup(&expected, ref->key_size, key) ==
			       lpm_lookup(&actual, ref->key_size, key));
		}
	}

	lpm_free(&actual);
	lpm_free(&expected);
}

// Orders prefixes by start, then by length, for qsort.
static int
test_prefix_cmp(const void *first, const void *second) {
	const struct classify_prefix *a = first;
	const struct classify_prefix *b = second;
	if (a->from != b->from) {
		return a->from < b->from ? -1 : 1;
	}
	return (a->len > b->len) - (a->len < b->len);
}

// The number of distinct prefixes of the set.
static uint32_t
test_unique_count(const struct classify_prefix *prefixes, uint32_t count) {
	if (count == 0) {
		return 0;
	}
	struct classify_prefix *sorted = malloc(count * sizeof(*sorted));
	assert(sorted != NULL);
	memcpy(sorted, prefixes, count * sizeof(*sorted));
	qsort(sorted, count, sizeof(*sorted), test_prefix_cmp);

	uint32_t unique = 1;
	for (uint32_t idx = 1; idx < count; ++idx) {
		unique += test_prefix_cmp(sorted + idx - 1, sorted + idx) != 0;
	}
	free(sorted);
	return unique;
}

/*
 * Asserts that the partition of the prefixes equals the oracle one.
 *
 * The LPM is built with the region values, and with the given values
 * when they are present, one per piece of the oracle partition.
 */
static void
test_check_partition(
	struct memory_context *mctx,
	uint8_t key_bits,
	const struct classify_prefix *prefixes,
	uint32_t count,
	const uint32_t *lpm_values
) {
	size_t balloc_size = mctx->balloc_size;
	size_t bfree_size = mctx->bfree_size;

	struct test_reference ref;
	test_reference_build(mctx, key_bits, prefixes, count, &ref);

	struct classify_prefixes input = {NULL, 0};
	for (uint32_t idx = 0; idx < count; ++idx) {
		assert(classify_prefixes_add(
			       mctx,
			       &input,
			       prefixes[idx].from,
			       prefixes[idx].len
		       ) == 0);
	}
	struct classify_partition partition;
	assert(classify_partition_build(mctx, key_bits, &input, &partition) == 0
	);
	classify_prefixes_free(mctx, &input);

	assert(partition.count == ref.index.count);
	// Repeated prefixes do not grow the piece arrays.
	assert(partition.capacity <= 2 * test_unique_count(prefixes, count) + 1
	);
	assert(partition.region_count == ref.collector.count);
	assert(partition.region_count == ref.index.max_value + 1);
	assert(memcmp(partition.values,
		      ADDR_OF(&ref.index.values),
		      partition.count * sizeof(uint32_t)) == 0);
	struct test_walk_ctx walk_ctx = {&partition, 0};
	assert(radix_walk(
		       &ref.index.radix,
		       ref.key_size,
		       test_walk_piece,
		       &walk_ctx
	       ) == 0);
	assert(walk_ctx.next == partition.count);

	// The wildcard covers every piece of the domain.
	uint32_t start;
	uint32_t stop;
	classify_partition_bounds(
		&partition, 0, classify_key_max(key_bits), &start, &stop
	);
	assert(start == 0 && stop == partition.count);

	for (uint32_t idx = 0; idx < count; ++idx) {
		uint64_t from = prefixes[idx].from;
		uint64_t len = prefixes[idx].len;
		uint32_t expected_start;
		uint32_t expected_stop;
		test_reference_bounds(
			&ref,
			key_bits,
			from,
			len,
			&expected_start,
			&expected_stop
		);
		classify_partition_bounds(
			&partition,
			from,
			classify_prefix_to(key_bits, from, len),
			&start,
			&stop
		);
		assert(start == expected_start);
		assert(stop == expected_stop);

		test_assert_find(&ref, &partition, key_bits, from);
		test_assert_find(
			&ref,
			&partition,
			key_bits,
			(classify_prefix_to(key_bits, from, len) + 1) &
				classify_key_max(key_bits)
		);
		test_assert_find(
			&ref,
			&partition,
			key_bits,
			(from + 1) & classify_key_max(key_bits)
		);
	}

	test_assert_lpm(&ref, &partition, mctx, partition.values);
	if (lpm_values != NULL) {
		test_assert_lpm(&ref, &partition, mctx, lpm_values);
	}

	classify_partition_free(mctx, &partition);
	test_reference_free(&ref);

	assert(mctx->balloc_size - balloc_size == mctx->bfree_size - bfree_size
	);
}

// Checks the prefixes of the given lengths at the given starts in every
// key width; a start is given in the 64 bit width and narrowed to the
// leading bits of the width under test.
static void
test_check_widths(
	struct memory_context *mctx,
	const struct classify_prefix *prefixes,
	uint32_t count
) {
	const uint8_t widths[] = {16, 32, 64};
	for (uint32_t width_idx = 0; width_idx < 3; ++width_idx) {
		uint8_t key_bits = widths[width_idx];
		struct classify_prefix narrowed[16];
		assert(count <= 16);
		for (uint32_t idx = 0; idx < count; ++idx) {
			uint64_t len = prefixes[idx].len;
			if (len > key_bits) {
				len = key_bits;
			}
			uint64_t from = prefixes[idx].from >> (64 - key_bits);
			uint64_t host = classify_prefix_to(key_bits, 0, len);
			narrowed[idx] = (struct classify_prefix){
				from & ~host,
				len,
			};
		}
		test_check_partition(mctx, key_bits, narrowed, count, NULL);
	}
}

// Returns the next value of a fixed seed generator.
static uint64_t
test_rand(uint64_t *state) {
	uint64_t value = *state;
	value ^= value << 13;
	value ^= value >> 7;
	value ^= value << 17;
	*state = value;
	return value;
}

// The 64 bit mask of the given number of leading bits.
static uint64_t
test_mask64(uint64_t len) {
	return len == 0 ? 0 : ~(uint64_t)0 << (64 - len);
}

#define TEST_HIGH (UINT64_C(0x8000000000000000))
#define TEST_ONES (~UINT64_C(0))

// Verifies that a domain without prefixes, or with the wildcard alone,
// is one piece of one region.
static void
run_empty_set_test(struct memory_context *mctx) {
	test_check_widths(mctx, NULL, 0);

	const struct classify_prefix wildcard[] = {{0, 0}};
	test_check_widths(mctx, wildcard, 1);
}

// Verifies that a single prefix splits the domain around itself.
static void
run_single_prefix_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{UINT64_C(0x0a00000000000000), 8},
	};
	test_check_widths(mctx, prefixes, 1);
}

// Verifies that prefixes sharing a start nest by length, whatever order
// they arrive in.
static void
run_nested_same_start_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{UINT64_C(0x0a00000000000000), 12},
		{UINT64_C(0x0a00000000000000), 4},
		{UINT64_C(0x0a00000000000000), 8},
	};
	test_check_widths(mctx, prefixes, 3);
}

// Verifies that a nested prefix ending where its parent ends starts a
// fresh region for the rest of the parent.
static void
run_equal_end_nesting_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{UINT64_C(0x0a00000000000000), 8},
		{UINT64_C(0x0a80000000000000), 9},
		{UINT64_C(0x0ac0000000000000), 10},
	};
	test_check_widths(mctx, prefixes, 3);
}

// Verifies that repeated prefixes split the domain like one.
static void
run_duplicates_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{UINT64_C(0x0a00000000000000), 8},
		{UINT64_C(0x0b00000000000000), 8},
		{UINT64_C(0x0a00000000000000), 8},
		{UINT64_C(0x0a00000000000000), 8},
		{UINT64_C(0x0b00000000000000), 8},
	};
	test_check_widths(mctx, prefixes, 5);
}

// Verifies the full length prefixes at the all-zero and the all-ones
// keys and inside the domain, and a prefix ending at the last key, whose
// end wraps past the domain.
static void
run_full_length_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{0, 64},
		{TEST_ONES, 64},
		{UINT64_C(0x0a0b0c0d0e0f1011), 64},
		{TEST_HIGH, 1},
		{0, 1},
	};
	test_check_widths(mctx, prefixes, 5);

	const struct classify_prefix last_key[] = {{TEST_ONES, 64}};
	test_check_widths(mctx, last_key, 1);
}

// Verifies the partition of random IPv4 networks of the measurement
// workload, /16 to /32.
static void
run_net4_workload_test(struct memory_context *mctx) {
	const uint32_t count = 5000;
	struct classify_prefix *prefixes = malloc(count * sizeof(*prefixes));
	assert(prefixes != NULL);

	uint64_t state = 1;
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint64_t len = 16 + test_rand(&state) % 17;
		uint64_t from = (test_rand(&state) & 0xffffffff) &
				(test_mask64(len) >> 32);
		prefixes[idx] = (struct classify_prefix){from, len};
	}
	test_check_partition(mctx, 32, prefixes, count, NULL);

	free(prefixes);
}

enum test_net6_shape {
	TEST_NET6_CONTIGUOUS,
	TEST_NET6_CLUSTERED,
	TEST_NET6_MIX,
};

// Fills the high and low half prefixes of the IPv6 measurement
// workloads: contiguous networks /32 to /64 under 2000::/3 with a
// wildcard low half, bi-contiguous networks /32 to /56 under one of 64
// parents with the low half one of 256 fixed prefixes, or each network
// of either shape.
static void
test_net6_workload(
	enum test_net6_shape shape,
	uint32_t count,
	struct classify_prefix *hi,
	struct classify_prefix *lo
) {
	uint64_t state = 7;
	uint64_t parents[64];
	for (uint32_t idx = 0; idx < 64; ++idx) {
		parents[idx] = (UINT64_C(0x20000000) |
				(test_rand(&state) & 0x1fffffff))
			       << 32;
	}
	struct classify_prefix pool[256];
	for (uint32_t idx = 0; idx < 256; ++idx) {
		uint64_t len = 8 + test_rand(&state) % 57;
		pool[idx] = (struct classify_prefix){
			test_rand(&state) & test_mask64(len),
			len,
		};
	}

	for (uint32_t idx = 0; idx < count; ++idx) {
		int clustered =
			shape == TEST_NET6_CLUSTERED ||
			(shape == TEST_NET6_MIX && (test_rand(&state) & 1));
		uint64_t addr = test_rand(&state);
		if (clustered) {
			uint64_t len = 32 + test_rand(&state) % 25;
			addr = parents[test_rand(&state) % 64] |
			       (addr & 0xffffffff);
			hi[idx] = (struct classify_prefix
			){addr & test_mask64(len), len};
			lo[idx] = pool[test_rand(&state) % 256];
			continue;
		}

		uint64_t len = 32 + test_rand(&state) % 33;
		addr = ((UINT64_C(0x20000000) | (test_rand(&state) & 0x1fffffff)
			)
			<< 32) |
		       (addr & 0xffffffff);
		hi[idx] =
			(struct classify_prefix){addr & test_mask64(len), len};
		lo[idx] = (struct classify_prefix){0, 0};
	}
}

// Checks both half partitions of an IPv6 workload.
static void
test_check_net6_workload(
	struct memory_context *mctx, enum test_net6_shape shape
) {
	const uint32_t count = 3000;
	struct classify_prefix *hi = malloc(count * sizeof(*hi));
	struct classify_prefix *lo = malloc(count * sizeof(*lo));
	assert(hi != NULL && lo != NULL);

	test_net6_workload(shape, count, hi, lo);
	test_check_partition(mctx, 64, hi, count, NULL);
	test_check_partition(mctx, 64, lo, count, NULL);

	free(lo);
	free(hi);
}

// Verifies the partitions of contiguous IPv6 networks.
static void
run_net6_contiguous_workload_test(struct memory_context *mctx) {
	test_check_net6_workload(mctx, TEST_NET6_CONTIGUOUS);
}

// Verifies the partitions of clustered bi-contiguous IPv6 networks.
static void
run_net6_bicontiguous_workload_test(struct memory_context *mctx) {
	test_check_net6_workload(mctx, TEST_NET6_CLUSTERED);
}

// Verifies the partitions of a mix of contiguous and bi-contiguous IPv6
// networks.
static void
run_net6_mix_workload_test(struct memory_context *mctx) {
	test_check_net6_workload(mctx, TEST_NET6_MIX);
}

// Verifies that a piece carrying the invalid LPM value is left out of
// the trie.
static void
run_lpm_invalid_values_test(struct memory_context *mctx) {
	const struct classify_prefix prefixes[] = {
		{0x0a000000, 8},
		{0x0a800000, 9},
		{0xc0a80000, 16},
		{0xffffffff, 32},
	};

	// Seven pieces: the oracle split of the prefixes above.
	const uint32_t values[] = {
		LPM_VALUE_INVALID,
		1,
		LPM_VALUE_INVALID,
		3,
		4,
		LPM_VALUE_INVALID,
		LPM_VALUE_INVALID,
	};
	test_check_partition(mctx, 32, prefixes, 4, values);
}

typedef void (*scenario_func)(struct memory_context *);

// Runs the scenario over a fresh poisoned arena, so a value a build
// leaves unwritten never reads as a valid one.
static void
run_scenario(scenario_func scenario) {
	void *memory = malloc(TEST_ARENA_SIZE);
	assert(memory != NULL);
	memset(memory, 0xa5, TEST_ARENA_SIZE);

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, memory, TEST_ARENA_SIZE);

	struct memory_context mctx;
	assert(memory_context_init(&mctx, "test", &allocator) == 0);

	scenario(&mctx);

	memory_context_fini(&mctx);
	free(memory);
}

int
main(void) {
	run_scenario(run_empty_set_test);
	run_scenario(run_single_prefix_test);
	run_scenario(run_nested_same_start_test);
	run_scenario(run_equal_end_nesting_test);
	run_scenario(run_duplicates_test);
	run_scenario(run_full_length_test);
	run_scenario(run_net4_workload_test);
	run_scenario(run_net6_contiguous_workload_test);
	run_scenario(run_net6_bicontiguous_workload_test);
	run_scenario(run_net6_mix_workload_test);
	run_scenario(run_lpm_invalid_values_test);

	return 0;
}
