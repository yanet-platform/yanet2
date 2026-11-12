/*
 * Pins the frozen LPM to the page-tree LPM as the reference.
 *
 * Every scenario applies the same inserts to both and requires the same
 * value for every probe key: range ends and their neighbours, the first and
 * last keys and random keys, for every supported key size. The page tree
 * answers 0xffffffff where the frozen LPM answers its invalid value.
 */

#include "common/fast_lpm_build.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define ARENA_SIZE ((size_t)1 << 30)

static const uint8_t key_sizes[] = {4, 8, 12, 16};

static uint64_t rng_state = 0x9e3779b97f4a7c15ull;

static uint64_t
rng(void) {
	uint64_t x = rng_state;
	x ^= x << 13;
	x ^= x >> 7;
	x ^= x << 17;
	return rng_state = x;
}

// An insert of a scenario; keys are big-endian integers.
struct op {
	fast_lpm_u128 from;
	fast_lpm_u128 to;
	uint32_t value;
};

// A memory context over a fresh arena; the arena outlives the context.
struct env {
	void *arena;
	struct block_allocator ba;
	struct memory_context mctx;
};

static int
env_init(struct env *env) {
	env->arena = malloc(ARENA_SIZE);
	if (env->arena == NULL) {
		return -1;
	}
	block_allocator_init(&env->ba);
	block_allocator_put_arena(&env->ba, env->arena, ARENA_SIZE);
	memory_context_init(&env->mctx, "fast_lpm_test", &env->ba);
	return 0;
}

static void
env_free(struct env *env) {
	free(env->arena);
}

static fast_lpm_u128
key_max(uint8_t key_size) {
	return fast_lpm_key_max(key_size);
}

/*
 * Builds both LPMs from the inserts and compares every probe.
 *
 * Returns the number of mismatches, or -1 when a build fails.
 */
static long
compare(struct env *env,
	uint8_t key_size,
	const struct op *ops,
	size_t count,
	size_t random_probes) {
	long mismatches = -1;
	struct lpm reference;
	struct fast_lpm lpm;
	if (lpm_init(&reference, &env->mctx, "reference")) {
		return -1;
	}
	if (fast_lpm_init(&lpm, &env->mctx, "fast", key_size)) {
		fast_lpm_free(&lpm);
		lpm_free(&reference);
		return -1;
	}
	uint8_t from[16], to[16];
	for (size_t idx = 0; idx < count; ++idx) {
		fast_lpm_u128_to_key(key_size, ops[idx].from, from);
		fast_lpm_u128_to_key(key_size, ops[idx].to, to);
		if (lpm_insert(
			    &reference, key_size, from, to, ops[idx].value
		    )) {
			perror("reference insert");
			goto out;
		}
		if (fast_lpm_insert(&lpm, from, to, ops[idx].value)) {
			perror("insert");
			goto out;
		}
	}
	if (fast_lpm_build(&lpm)) {
		perror("build");
		goto out;
	}

	mismatches = 0;
	fast_lpm_u128 max = key_max(key_size);
	size_t probes = 4 * count + 2 + random_probes;
	for (size_t idx = 0; idx < probes; ++idx) {
		fast_lpm_u128 key;
		if (idx < 4 * count) {
			const struct op *op = &ops[idx / 4];
			switch (idx % 4) {
			case 0:
				key = op->from;
				break;
			case 1:
				key = op->from - 1;
				break;
			case 2:
				key = op->to;
				break;
			default:
				key = op->to + 1;
				break;
			}
		} else if (idx == 4 * count) {
			key = 0;
		} else if (idx == 4 * count + 1) {
			key = max;
		} else {
			// Near a random insert, so probes land in nodes.
			key = ((fast_lpm_u128)rng() << 64 | rng());
			if (count && rng() % 2) {
				key = ops[rng() % count].from + rng() % 512 -
				      256;
			}
		}
		key &= max;
		uint8_t bytes[16];
		fast_lpm_u128_to_key(key_size, key, bytes);
		uint32_t want = lpm_lookup(&reference, key_size, bytes);
		if (want == LPM_VALUE_INVALID) {
			want = FAST_LPM_VALUE_INVALID;
		}
		uint32_t got;
		switch (key_size) {
		case 4:
			got = fast_lpm_lookup(&lpm, 4, bytes);
			break;
		case 8:
			got = fast_lpm_lookup(&lpm, 8, bytes);
			break;
		case 12:
			got = fast_lpm_lookup(&lpm, 12, bytes);
			break;
		default:
			got = fast_lpm_lookup(&lpm, 16, bytes);
			break;
		}
		if (got != want) {
			if (mismatches < 5) {
				fprintf(stderr,
					"key size %u probe %zu: want %u got "
					"%u\n",
					key_size,
					idx,
					want,
					got);
			}
			mismatches++;
		}
	}
	// Both walks yield the same ranges in the same order.
	uint8_t first[16], last[16];
	memset(first, 0x00, sizeof(first));
	memset(last, 0xff, sizeof(last));
	struct lpm_iter want_it;
	struct fast_lpm_iter got_it;
	lpm_iter_init(&want_it, &reference, key_size, first, last);
	fast_lpm_iter_init(&got_it, &lpm);
	for (;;) {
		bool want_more = lpm_iter_next(&want_it);
		bool got_more = fast_lpm_iter_next(&got_it);
		if (want_more != got_more) {
			fprintf(stderr, "walks end apart\n");
			mismatches++;
			break;
		}
		if (!want_more) {
			break;
		}
		if (memcmp(want_it.cur_from, got_it.cur_from, key_size) ||
		    memcmp(want_it.cur_to, got_it.cur_to, key_size) ||
		    want_it.cur_value != got_it.cur_value) {
			fprintf(stderr, "walks differ\n");
			mismatches++;
			break;
		}
	}

out:
	fast_lpm_free(&lpm);
	lpm_free(&reference);
	return mismatches;
}

// A prefix of the given length over the address.
static struct op
prefix(uint8_t key_size, fast_lpm_u128 addr, unsigned len, uint32_t value) {
	unsigned bits = 8 * key_size;
	fast_lpm_u128 host = len == 0
				     ? key_max(key_size)
				     : (((fast_lpm_u128)1 << (bits - len)) - 1);
	if (len == bits) {
		host = 0;
	}
	addr &= key_max(key_size) & ~host;
	return (struct op){addr, addr | host, value};
}

// A key from a few dense regions: near zero, near the end, a fixed top.
static fast_lpm_u128
dense_key(uint8_t key_size) {
	fast_lpm_u128 max = key_max(key_size);
	fast_lpm_u128 low = (fast_lpm_u128)rng() << 64 | rng();
	switch (rng() % 4) {
	case 0:
		return low & 0xffffff;
	case 1:
		return max - (low & 0xffffff);
	case 2:
		return (max & ~(max >> 3)) | (low & (max >> 3) & 0xffffffffff);
	default:
		return low & max;
	}
}

static uint32_t
random_value(void) {
	// Values with the top bits set, as classify marks rows with bit 30.
	uint32_t value = (uint32_t)rng() % 64;
	if (rng() % 4 == 0) {
		value |= 0x40000000u;
	}
	return value;
}

static int
check(const char *name, long mismatches) {
	if (mismatches != 0) {
		fprintf(stderr, "%s: %ld mismatches\n", name, mismatches);
		return -1;
	}
	return 0;
}

// Prefixes in random order nest and repeat; the latest insert wins.
static int
test_random_prefixes(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		for (int round = 0; round < 200; ++round) {
			size_t count = 1 + rng() % 300;
			struct op ops[300];
			for (size_t idx = 0; idx < count; ++idx) {
				unsigned len = rng() % (8 * key_size + 1);
				ops[idx] =
					prefix(key_size,
					       dense_key(key_size),
					       len,
					       random_value());
			}
			if (check("random prefixes",
				  compare(env, key_size, ops, count, 2000))) {
				return -1;
			}
		}
	}
	return 0;
}

// Overlapping ranges that are not prefixes take the general sweep.
static int
test_random_ranges(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		for (int round = 0; round < 200; ++round) {
			size_t count = 1 + rng() % 300;
			struct op ops[300];
			for (size_t idx = 0; idx < count; ++idx) {
				fast_lpm_u128 a = dense_key(key_size);
				fast_lpm_u128 b = a + rng() % 100000;
				if (b < a || b > key_max(key_size)) {
					b = key_max(key_size);
				}
				ops[idx] = (struct op){a, b, random_value()};
			}
			if (check("random ranges",
				  compare(env, key_size, ops, count, 2000))) {
				return -1;
			}
		}
	}
	return 0;
}

// A sorted partition of the key space, as a range index produces.
static int
test_sorted_partition(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		for (int round = 0; round < 50; ++round) {
			size_t count = 1 + rng() % 1000;
			fast_lpm_u128 *starts =
				malloc(count * sizeof(fast_lpm_u128));
			starts[0] = 0;
			for (size_t idx = 1; idx < count; ++idx) {
				starts[idx] = dense_key(key_size);
			}
			// Insertion sort: the counts are small.
			for (size_t i = 1; i < count; ++i) {
				for (size_t j = i;
				     j > 0 && starts[j - 1] > starts[j];
				     --j) {
					fast_lpm_u128 t = starts[j];
					starts[j] = starts[j - 1];
					starts[j - 1] = t;
				}
			}
			struct op *ops = malloc(count * sizeof(*ops));
			size_t n = 0;
			for (size_t idx = 0; idx < count; ++idx) {
				if (idx + 1 < count &&
				    starts[idx + 1] == starts[idx]) {
					continue;
				}
				fast_lpm_u128 end =
					idx + 1 < count ? starts[idx + 1] - 1
							: key_max(key_size);
				ops[n++] = (struct op
				){starts[idx], end, random_value()};
			}
			long rc = compare(env, key_size, ops, n, 2000);
			free(ops);
			free(starts);
			if (check("sorted partition", rc)) {
				return -1;
			}
		}
	}
	return 0;
}

// The edges of the key space and of the insert order.
static int
test_edges(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		unsigned bits = 8 * key_size;
		fast_lpm_u128 max = key_max(key_size);
		struct op ops[4];

		if (check("no inserts", compare(env, key_size, ops, 0, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, 0, 0, 3);
		ops[1] = prefix(key_size, max, bits, 5);
		if (check("default route then last key",
			  compare(env, key_size, ops, 2, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, max, bits, 5);
		ops[1] = prefix(key_size, 0, 0, 3);
		if (check("last key hidden by a later default route",
			  compare(env, key_size, ops, 2, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, 0, bits, 7);
		if (check("first key alone",
			  compare(env, key_size, ops, 1, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, max, 8, 1);
		ops[1] = prefix(key_size, max, 8, 9);
		if (check("duplicate prefix, the later wins",
			  compare(env, key_size, ops, 2, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, max >> 1, 16, 11);
		ops[1] = prefix(key_size, max >> 1, 8, 13);
		ops[2] = prefix(key_size, max >> 1, 24, 15);
		if (check("nested prefix before and after its parent",
			  compare(env, key_size, ops, 3, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, 0, 1, 17);
		ops[1] = prefix(key_size, max, 1, 17);
		if (check("halves of one value merge",
			  compare(env, key_size, ops, 2, 100))) {
			return -1;
		}
		ops[0] = (struct op){5, 4, 19};
		if (check("empty range", compare(env, key_size, ops, 1, 100))) {
			return -1;
		}
		ops[0] = prefix(key_size, 0, 0, FAST_LPM_VALUE_INVALID - 1);
		if (check("largest value",
			  compare(env, key_size, ops, 1, 100))) {
			return -1;
		}
	}
	return 0;
}

/*
 * Boundaries in one last-level subtree: four fit a bucket, five need a
 * full node; above the last 8 key bytes a bucket holds two.
 */
static int
test_bucket_capacity(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		for (unsigned shift = 0; shift < 8 * key_size; shift += 8) {
			for (size_t bounds = 1; bounds <= 6; ++bounds) {
				struct op ops[6];
				fast_lpm_u128 base = (fast_lpm_u128)0x42
						     << shift;
				for (size_t idx = 0; idx < bounds; ++idx) {
					fast_lpm_u128 at =
						base +
						((fast_lpm_u128)(2 * idx + 1)
						 << shift);
					ops[idx] = (struct op
					){at, at, (uint32_t)idx + 1};
				}
				if (check("bucket capacity",
					  compare(env,
						  key_size,
						  ops,
						  bounds,
						  100))) {
					return -1;
				}
			}
		}
	}
	return 0;
}

// Values at or above the invalid value are refused.
static int
test_value_limit(struct env *env) {
	struct fast_lpm lpm;
	if (fast_lpm_init(&lpm, &env->mctx, "lpm", 4)) {
		return -1;
	}
	uint8_t key[4] = {0};
	errno = 0;
	int rc = fast_lpm_insert(&lpm, key, key, FAST_LPM_VALUE_INVALID);
	int err = errno;
	fast_lpm_free(&lpm);
	if (rc != -1 || err != EINVAL) {
		fprintf(stderr, "invalid value accepted\n");
		return -1;
	}
	return 0;
}

// Key sizes other than 4, 8, 12 and 16 bytes are refused.
static int
test_key_size_limit(struct env *env) {
	struct fast_lpm lpm;
	errno = 0;
	int rc = fast_lpm_init(&lpm, &env->mctx, "lpm", 6);
	int err = errno;
	fast_lpm_free(&lpm);
	if (rc != -1 || err != EINVAL) {
		fprintf(stderr, "key size 6 accepted\n");
		return -1;
	}
	return 0;
}

// An LPM that was never built maps every key to the invalid value.
static int
test_unbuilt_lookup(struct env *env) {
	struct fast_lpm lpm;
	if (fast_lpm_init(&lpm, &env->mctx, "lpm", 16)) {
		return -1;
	}
	uint8_t key[16];
	memset(key, 0x5a, sizeof(key));
	if (fast_lpm_insert(&lpm, key, key, 1)) {
		fast_lpm_free(&lpm);
		return -1;
	}
	uint32_t value = fast_lpm_lookup(&lpm, 16, key);
	fast_lpm_free(&lpm);
	if (value != FAST_LPM_VALUE_INVALID) {
		fprintf(stderr, "staged insert visible before the build\n");
		return -1;
	}
	return 0;
}

// A built LPM refuses further inserts and builds.
static int
test_frozen(struct env *env) {
	struct fast_lpm lpm;
	if (fast_lpm_init(&lpm, &env->mctx, "lpm", 4) || fast_lpm_build(&lpm)) {
		fast_lpm_free(&lpm);
		return -1;
	}
	uint8_t key[4] = {0};
	errno = 0;
	int insert_rc = fast_lpm_insert(&lpm, key, key, 1);
	int insert_err = errno;
	errno = 0;
	int build_rc = fast_lpm_build(&lpm);
	int build_err = errno;
	fast_lpm_free(&lpm);
	if (insert_rc != -1 || insert_err != EBUSY || build_rc != -1 ||
	    build_err != EBUSY) {
		fprintf(stderr, "built LPM accepted a change\n");
		return -1;
	}
	return 0;
}

// A build out of memory keeps the previous trie and frees cleanly.
static int
test_build_out_of_memory(void) {
	size_t arena_size = 256 << 10;
	void *arena = aligned_alloc(arena_size, arena_size);
	if (arena == NULL) {
		return -1;
	}
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, arena_size);
	struct memory_context mctx;
	memory_context_init(&mctx, "small", &ba);

	struct fast_lpm lpm;
	int rc = -1;
	if (fast_lpm_init(&lpm, &mctx, "lpm", 4)) {
		goto out;
	}
	// Three host routes under every first byte need a full node each:
	// 256 KiB of nodes, while the staged inserts fit.
	for (uint32_t idx = 0; idx < 3 * 256; ++idx) {
		uint8_t key[4] = {
			(uint8_t)(idx / 3), 0, 1, (uint8_t)(1 + 4 * (idx % 3))
		};
		if (fast_lpm_insert(&lpm, key, key, idx % 3 + 1)) {
			fprintf(stderr, "staging ran out of memory\n");
			goto out;
		}
	}
	errno = 0;
	if (fast_lpm_build(&lpm) != -1 || errno != ENOMEM) {
		fprintf(stderr, "build fit a 256 KiB arena\n");
		goto out;
	}
	uint8_t probe[4] = {7, 0, 1, 1};
	if (fast_lpm_lookup(&lpm, 4, probe) != FAST_LPM_VALUE_INVALID) {
		fprintf(stderr, "failed build changed the trie\n");
		goto out;
	}
	rc = 0;
out:
	fast_lpm_free(&lpm);
	free(arena);
	return rc;
}

// A trie over several memory blocks keeps every link within reach.
static int
test_many_blocks(struct env *env) {
	for (size_t ks = 0; ks < sizeof(key_sizes); ++ks) {
		uint8_t key_size = key_sizes[ks];
		size_t count = 20000;
		struct op *ops = malloc(count * sizeof(*ops));
		for (size_t idx = 0; idx < count; ++idx) {
			fast_lpm_u128 addr = (fast_lpm_u128)rng() << 64 | rng();
			ops[idx] =
				prefix(key_size,
				       addr,
				       8 * key_size / 2 +
					       rng() % (4 * key_size),
				       random_value());
		}
		long rc = compare(env, key_size, ops, count, 100000);
		free(ops);
		if (check("many blocks", rc)) {
			return -1;
		}
	}
	return 0;
}

int
main(void) {
	struct env env;
	if (env_init(&env)) {
		return 1;
	}
	int rc = test_edges(&env) || test_bucket_capacity(&env) ||
		 test_value_limit(&env) || test_key_size_limit(&env) ||
		 test_unbuilt_lookup(&env) || test_frozen(&env) ||
		 test_build_out_of_memory() || test_random_prefixes(&env) ||
		 test_random_ranges(&env) || test_sorted_partition(&env) ||
		 test_many_blocks(&env);
	env_free(&env);
	if (rc) {
		return 1;
	}
	printf("fast_lpm: ok\n");
	return 0;
}
