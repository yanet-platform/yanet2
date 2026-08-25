/*
 * Proof-of-concept benchmark for a byte-stride LPM with a hashed top level.
 *
 * The container is `struct lpm_hash` from common/lpm_hash.h: an 8-byte
 * lpm trie plus a hash that maps the leading hops key bytes to the trie
 * page number they lead to. A hit skips the first hops dependent page
 * loads and walks the rest from the stored page; a miss falls back to the
 * root walk, which terminates within hops loads because the miss itself
 * proves a flagged slot sits on the prefix path.
 *
 * Motivation: common/lpm.h walks one dependent pointer chase per key byte,
 * so an 8-byte lookup (lpm8_lookup) costs up to 8 dependent cache-line
 * accesses. The net6 classifier (lib/filter/classifiers/net6.h) runs two
 * such lookups per IPv6 address, one per 64-bit half, and the production
 * acl capture averages 5.4..7.0 loads per half. Offloading the first hops
 * bytes to one hash probe cuts the dependent chain by 44..57% at hops 6
 * with 345..3204 entries per trie.
 *
 * This is a POC harness only. It is not wired into the filter pipeline. It
 * builds a plain full LPM (control, holds every prefix) alongside the
 * hybrid, asserts lpm_hash == full for a large key sample, then A/B-times
 * both scalar lookup paths over a recycled probe set.
 *
 * Timing uses __rdtsc (the instruction rte_rdtsc wraps on x86) plus
 * clock_gettime(CLOCK_MONOTONIC); both run the same recycled loop.
 * cycles/lookup is frequency-independent; ns/lookup is the human-readable
 * headline.
 */

#include "common/lpm.h"
#include "common/lpm_hash.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/rng.h"
#include "lib/logging/log.h"

#include <x86intrin.h>

#include <inttypes.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define BENCH_KEY_SIZE 8
#define BENCH_HOPS_DEFAULT LPM_HASH_HOPS_DEFAULT

// Compile-time corruption toggle for the sanity check. When defined to 1,
// the page numbers of adjacent occupied hash slots are swapped after the
// build, so lookups for those prefixes start the walk on a foreign page and
// the correctness loop MUST report divergence. Proves the assertion can
// actually fail. The production lpm_hash (common/lpm_hash.h) has no corrupt
// path of its own; the bench injects the fault by editing slots directly.
#ifndef HYBRID_BENCH_CORRUPT
#define HYBRID_BENCH_CORRUPT 0
#endif

struct prefix_spec {
	uint8_t from[BENCH_KEY_SIZE];
	uint8_t to[BENCH_KEY_SIZE];
	uint32_t value;
	uint8_t prefix_len;
	bool is_long;
};

// Read 8 big-endian bytes into a uint64 and the reverse.
//
// These are bench-local helpers for probe generation; they are NOT used by
// the lpm_hash storage path. lpm_hash_insert and lpm_hash_lookup take raw
// 8-byte keys, hash and compare them in place, and never convert to or from
// uint64 (see common/lpm_hash.h).
static inline uint64_t
be_read(const uint8_t *p) {
	uint64_t value = 0;
	for (uint8_t idx = 0; idx < 8; ++idx) {
		value = (value << 8) | p[idx];
	}
	return value;
}

static inline void
be_write(uint64_t value, uint8_t *p) {
	for (uint8_t idx = 0; idx < 8; ++idx) {
		p[7 - idx] = (uint8_t)(value >> (8 * idx));
	}
}

static inline uint64_t
rdtsc_read(void) {
	unsigned int aux;
	return __rdtscp(&aux);
}

static uint64_t
calibrate_tsc_hz(void) {
	struct timespec ts_start;
	struct timespec ts_end;
	clock_gettime(CLOCK_MONOTONIC, &ts_start);
	uint64_t tsc_start = rdtsc_read();

	// ~50 ms gate, enough for clock_gettime resolution yet short.
	struct timespec sleep_ts = {.tv_sec = 0, .tv_nsec = 50 * 1000 * 1000};
	nanosleep(&sleep_ts, NULL);

	uint64_t tsc_end = rdtsc_read();
	clock_gettime(CLOCK_MONOTONIC, &ts_end);

	uint64_t ns =
		(uint64_t)(ts_end.tv_sec - ts_start.tv_sec) * 1000000000ULL +
		(uint64_t)(ts_end.tv_nsec - ts_start.tv_nsec);
	if (ns == 0) {
		return 0;
	}
	return tsc_end * 1000000000ULL / ns - tsc_start * 1000000000ULL / ns;
}

static void
prefix_range_from_len(
	const uint8_t *base, uint8_t prefix_len, uint8_t *from, uint8_t *to
) {
	uint8_t whole = prefix_len / 8;
	uint8_t frac = prefix_len % 8;

	memcpy(from, base, BENCH_KEY_SIZE);
	memset(from + whole, 0x00, BENCH_KEY_SIZE - whole);
	if (frac) {
		uint8_t mask = (uint8_t)(0xff << (8 - frac));
		from[whole] &= mask;
	}

	memcpy(to, from, BENCH_KEY_SIZE);
	for (uint8_t idx = whole; idx < BENCH_KEY_SIZE; ++idx) {
		if (idx == whole && frac) {
			to[idx] |= (uint8_t)~(0xff << (8 - frac));
		} else {
			to[idx] = 0xff;
		}
	}
}

// Builds a prefix set with a controllable long/short split.
//
// Long prefixes are /56../64 of the half; they push pointer paths deep and
// populate the hashed top. Each gets a disjoint top-byte region. Short
// prefixes are /0../16 defaults; they terminate the walk early, so probes
// under them miss the top hash and take the (short) root fallback.
static void
generate_prefixes(
	struct prefix_spec *prefixes,
	size_t count,
	double long_fraction,
	uint64_t *rng
) {
	size_t long_count = (size_t)((double)count * long_fraction);
	if (long_count > count) {
		long_count = count;
	}

	size_t long_idx = 0;
	for (size_t i = 0; i < count; ++i) {
		struct prefix_spec *spec = &prefixes[i];
		spec->value = (uint32_t)(i + 1);

		if (long_idx < long_count) {
			// Disjoint region: carve a /56 slice out of byte 0.
			// 256 slices available, so wrap and shift down for
			// larger long_count.
			uint64_t slice = long_idx % 256ULL;
			uint8_t base[BENCH_KEY_SIZE];
			memset(base, 0, BENCH_KEY_SIZE);
			base[0] = (uint8_t)slice;
			base[1] = (uint8_t)(long_idx / 256ULL);

			// Bias toward /64-of-half (one covered address), the
			// realistic exact-match-heavy workload that makes a
			// plain LPM walk all 8 bytes. A small minority sits at
			// /56../63 so deeper pointer paths exist too.
			uint8_t len;
			uint32_t shape = (uint32_t)(rng_next(rng) % 100);
			if (shape < 85) {
				len = 64;
			} else {
				len = 56 + (uint8_t)(rng_next(rng) % 9);
			}
			prefix_range_from_len(base, len, spec->from, spec->to);
			spec->prefix_len = len;
			spec->is_long = true;
			++long_idx;
		} else {
			// Default-range prefix: /0, /8, or /16.
			uint8_t base[BENCH_KEY_SIZE];
			memset(base, 0, BENCH_KEY_SIZE);
			uint32_t pick = (uint32_t)(rng_next(rng) % 3);
			uint8_t len = (pick == 0) ? 0 : (pick == 1 ? 8 : 16);
			if (len) {
				base[0] = (uint8_t)(rng_next(rng) % 256);
			}
			prefix_range_from_len(base, len, spec->from, spec->to);
			spec->prefix_len = len;
			spec->is_long = false;
		}
	}
}

// Builds a recycled probe set biased so that roughly long_fraction of probes
// hit a long (deep) prefix and the rest hit only a short default.
static void
generate_probes(
	uint8_t (*probes)[BENCH_KEY_SIZE],
	size_t count,
	const struct prefix_spec *prefixes,
	size_t prefix_count,
	double long_fraction,
	uint64_t *rng
) {
	size_t long_count = 0;
	for (size_t i = 0; i < prefix_count; ++i) {
		if (prefixes[i].is_long) {
			++long_count;
		}
	}

	for (size_t i = 0; i < count; ++i) {
		double roll =
			(double)(rng_next(rng) >> 11) / (double)(1ULL << 53);
		const struct prefix_spec *spec = NULL;
		if (roll < long_fraction && long_count) {
			size_t idx = (size_t)(rng_next(rng) % prefix_count);
			while (!prefixes[idx].is_long) {
				idx = (idx + 1) % prefix_count;
			}
			spec = &prefixes[idx];
		} else {
			size_t idx = (size_t)(rng_next(rng) % prefix_count);
			while (prefixes[idx].is_long) {
				idx = (idx + 1) % prefix_count;
			}
			spec = &prefixes[idx];
		}

		uint64_t base = be_read(spec->from);
		uint64_t last = be_read(spec->to);
		uint64_t addr;
		if (base == 0 && last == UINT64_MAX) {
			// Full 64-bit range (a /0 default): any value is valid
			// and span+1 would overflow to 0, so pick directly.
			addr = rng_next(rng);
		} else {
			uint64_t span = last - base;
			uint64_t off =
				(span == 0) ? 0 : (rng_next(rng) % (span + 1));
			addr = base + off;
		}
		be_write(addr, probes[i]);
	}
}

// Asserts lpm_hash == full for every key in [keys]. Returns the number of
// mismatches. A mismatch is a real bug, not a benchmark artifact.
static uint64_t
verify_against_full(
	const struct lpm_hash *lpm_hash,
	const struct lpm *full,
	const uint8_t (*keys)[BENCH_KEY_SIZE],
	size_t count
) {
	uint64_t mismatches = 0;
	for (size_t i = 0; i < count; ++i) {
		uint32_t full_value = lpm8_lookup(full, keys[i]);
		uint32_t lpm_hash_value = lpm_hash_lookup(lpm_hash, keys[i]);
		if (full_value != lpm_hash_value) {
			if (mismatches < 8) {
				fprintf(stderr,
					"mismatch at %zu: full=%u "
					"lpm_hash=%u\n",
					i,
					full_value,
					lpm_hash_value);
			}
			++mismatches;
		}
	}
	return mismatches;
}

struct bench_args {
	size_t num_prefixes;
	double long_fraction;
	uint32_t hops;
	size_t num_probes;
	uint32_t rounds;
	uint64_t seed;
};

static void
print_help(const char *argv0) {
	fprintf(stderr,
		"Usage: %s [options]\n"
		"\n"
		"  --prefixes N       total prefix count (default 10000)\n"
		"  --long-fraction F  fraction of prefixes that are long /\n"
		"                     deep, 0..1 (default 0.8)\n"
		"  --hops N           hashed top height, 0..7 (default %u)\n"
		"  --probes N         recycled probe set size (default "
		"1048576)\n"
		"  --rounds N         timed rounds, median reported (default "
		"11)\n"
		"  --seed S           PRNG seed, hex (default time-based)\n",
		argv0,
		BENCH_HOPS_DEFAULT);
}

static int
parse_args(int argc, char **argv, struct bench_args *args) {
	*args = (struct bench_args){
		.num_prefixes = 10000,
		.long_fraction = 0.8,
		.hops = BENCH_HOPS_DEFAULT,
		.num_probes = 1u << 20,
		.rounds = 11,
		.seed = 0,
	};

	for (int i = 1; i < argc; ++i) {
		const char *a = argv[i];
		if (strcmp(a, "-h") == 0 || strcmp(a, "--help") == 0) {
			print_help(argv[0]);
			return 1;
		} else if (strcmp(a, "--prefixes") == 0 && i + 1 < argc) {
			args->num_prefixes =
				(size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(a, "--long-fraction") == 0 && i + 1 < argc) {
			args->long_fraction = strtod(argv[++i], NULL);
		} else if (strcmp(a, "--hops") == 0 && i + 1 < argc) {
			args->hops = (uint32_t)strtoul(argv[++i], NULL, 10);
		} else if (strcmp(a, "--probes") == 0 && i + 1 < argc) {
			args->num_probes =
				(size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(a, "--rounds") == 0 && i + 1 < argc) {
			args->rounds = (uint32_t)strtoul(argv[++i], NULL, 10);
		} else if (strcmp(a, "--seed") == 0 && i + 1 < argc) {
			args->seed = strtoull(argv[++i], NULL, 16);
		} else {
			fprintf(stderr, "unknown arg: %s\n", a);
			print_help(argv[0]);
			return -1;
		}
	}

	if (args->num_prefixes == 0 || args->num_probes == 0 ||
	    args->rounds == 0) {
		fprintf(stderr, "counts must be > 0\n");
		return -1;
	}
	if (args->hops > 7) {
		fprintf(stderr, "hops must be 0..7\n");
		return -1;
	}
	if (args->long_fraction < 0.0) {
		args->long_fraction = 0.0;
	}
	if (args->long_fraction > 1.0) {
		args->long_fraction = 1.0;
	}
	return 0;
}

static int
cmp_u64(const void *a, const void *b) {
	uint64_t ua = *(const uint64_t *)a;
	uint64_t ub = *(const uint64_t *)b;
	return (ua > ub) - (ua < ub);
}

// Broadest prefix first. lpm.h resolves overlapping inserts last-writer-wins
// at each covered leaf, so inserting least-specific first makes the more
// specific prefix survive and the lookup behave as real longest-prefix-match.
static int
cmp_prefix_broad_first(const void *a, const void *b) {
	const struct prefix_spec *pa = a;
	const struct prefix_spec *pb = b;
	return (int)pa->prefix_len - (int)pb->prefix_len;
}

static uint64_t
median_u64(uint64_t *values, uint32_t count) {
	qsort(values, count, sizeof(*values), cmp_u64);
	return values[count / 2];
}

int
main(int argc, char **argv) {
	struct bench_args args;
	int parsed = parse_args(argc, argv, &args);
	if (parsed != 0) {
		return (parsed > 0) ? 0 : 1;
	}

	log_enable_name("info");

	if (args.seed == 0) {
		struct timespec ts;
		clock_gettime(CLOCK_MONOTONIC, &ts);
		args.seed = ((uint64_t)ts.tv_sec << 20) ^ (uint64_t)ts.tv_nsec ^
			    0x9e3779b97f4a7c15ULL;
	}
	uint64_t rng = args.seed;

	LOG(INFO,
	    "lpm_hash_hybrid_bench: prefixes=%zu long_fraction=%.2f "
	    "hops=%u probes=%zu rounds=%u seed=0x%016" PRIx64,
	    args.num_prefixes,
	    args.long_fraction,
	    args.hops,
	    args.num_probes,
	    args.rounds,
	    args.seed);

	uint64_t tsc_hz = calibrate_tsc_hz();
	LOG(INFO,
	    "lpm_hash_hybrid_bench: tsc_hz ~= %" PRIu64
	    " (%.2f GHz, self-calibrated)",
	    tsc_hz,
	    (double)tsc_hz / 1e9);

	size_t arena_size =
		args.num_prefixes * 32ULL * 1024ULL + 64ULL * 1024ULL * 1024ULL;
	if (arena_size < 128ULL * 1024ULL * 1024ULL) {
		arena_size = 128ULL * 1024ULL * 1024ULL;
	}
	void *arena = malloc(arena_size);
	if (arena == NULL) {
		fprintf(stderr, "arena malloc failed (%zu bytes)\n", arena_size
		);
		return 1;
	}

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_size);

	struct memory_context bench_ctx;
	if (memory_context_init(&bench_ctx, "lpm_hash_bench", &allocator) !=
	    0) {
		fprintf(stderr, "memory_context_init failed\n");
		free(arena);
		return 1;
	}

	struct prefix_spec *prefixes = (struct prefix_spec *)calloc(
		args.num_prefixes, sizeof(struct prefix_spec)
	);
	if (prefixes == NULL) {
		fprintf(stderr, "prefixes calloc failed\n");
		return 1;
	}
	generate_prefixes(
		prefixes, args.num_prefixes, args.long_fraction, &rng
	);

	// Reorder so broadest prefixes are inserted first (see comparator).
	qsort(prefixes,
	      args.num_prefixes,
	      sizeof(*prefixes),
	      cmp_prefix_broad_first);

	size_t long_count = 0;
	size_t short_count = 0;
	for (size_t i = 0; i < args.num_prefixes; ++i) {
		if (prefixes[i].is_long) {
			++long_count;
		} else {
			++short_count;
		}
	}

	// Full control LPM: every prefix.
	struct lpm full;
	if (lpm_init(&full, &bench_ctx, "bench_full") != 0) {
		fprintf(stderr, "full lpm_init failed\n");
		return 1;
	}
	for (size_t i = 0; i < args.num_prefixes; ++i) {
		if (lpm8_insert(
			    &full,
			    prefixes[i].from,
			    prefixes[i].to,
			    prefixes[i].value
		    ) != 0) {
			fprintf(stderr,
				"full lpm8_insert failed at %zu (arena "
				"full?)\n",
				i);
			return 1;
		}
	}

	// lpm_hash: same prefixes into its trie, then the top table is
	// derived from the finished trie.
	struct lpm_hash lh;
	if (lpm_hash_init(
		    &lh, &bench_ctx, (uint8_t)args.hops, "lpm_hash_bench"
	    ) != 0) {
		fprintf(stderr, "lpm_hash_init failed\n");
		return 1;
	}
	for (size_t i = 0; i < args.num_prefixes; ++i) {
		if (lpm_hash_insert(
			    &lh,
			    prefixes[i].from,
			    prefixes[i].to,
			    prefixes[i].value
		    ) != 0) {
			fprintf(stderr, "lpm_hash_insert failed at %zu\n", i);
			return 1;
		}
	}
	if (lpm_hash_build_top(&lh) != 0) {
		fprintf(stderr, "lpm_hash_build_top failed\n");
		return 1;
	}
	LOG(INFO,
	    "lpm_hash_hybrid_bench: long_prefixes=%zu short_prefixes=%zu "
	    "top_entries=%u lpm_pages=%zu",
	    long_count,
	    short_count,
	    lh.index.size,
	    lh.lpm.page_count);

#if HYBRID_BENCH_CORRUPT
	// Inject a deliberate fault so the correctness check has something to
	// catch. The production lpm_hash has no corrupt path, so swap the page
	// numbers of adjacent occupied slots directly: lookups for the swapped
	// prefixes start the walk on a foreign page and disagree with the full
	// LPM. Swaps keep every page number valid, so no out-of-range page is
	// dereferenced.
	{
		struct lpm_hash_slot *slots = ADDR_OF(&lh.index.slots);
		for (uint32_t pos = 0; pos + 1 < lh.index.capacity; ++pos) {
			if (slots[pos].value == LPM_VALUE_INVALID ||
			    slots[pos + 1].value == LPM_VALUE_INVALID) {
				continue;
			}
			uint32_t tmp = slots[pos].value;
			slots[pos].value = slots[pos + 1].value;
			slots[pos + 1].value = tmp;
			++pos;
		}
	}
#endif

	uint8_t(*probes)[BENCH_KEY_SIZE] = (uint8_t(*)[BENCH_KEY_SIZE]
	)malloc(args.num_probes * BENCH_KEY_SIZE);
	if (probes == NULL) {
		fprintf(stderr, "probes malloc failed\n");
		return 1;
	}
	generate_probes(
		probes,
		args.num_probes,
		prefixes,
		args.num_prefixes,
		args.long_fraction,
		&rng
	);

	// Correctness: the biased probes and a deterministic whole-space grid.
	uint64_t probe_mismatch =
		verify_against_full(&lh, &full, probes, args.num_probes);

	size_t grid_count = 1u << 16;
	uint8_t(*grid)[BENCH_KEY_SIZE] =
		(uint8_t(*)[BENCH_KEY_SIZE])malloc(grid_count * BENCH_KEY_SIZE);
	if (grid == NULL) {
		fprintf(stderr, "grid malloc failed\n");
		return 1;
	}
	for (size_t i = 0; i < grid_count; ++i) {
		uint64_t addr = (i * 0x9e3779b97f4a7c15ULL);
		be_write(addr, grid[i]);
	}
	uint64_t grid_mismatch =
		verify_against_full(&lh, &full, grid, grid_count);
	free(grid);

#if HYBRID_BENCH_CORRUPT
	if (probe_mismatch == 0 && grid_mismatch == 0) {
		fprintf(stderr,
			"CORRUPT MODE: correctness check did NOT detect the "
			"injected fault (assertion is vacuous)\n");
		return 2;
	}
	LOG(INFO,
	    "lpm_hash_hybrid_bench: CORRUPT mode detected %llu injected "
	    "mismatches (assertion is live), skipping timing.",
	    (unsigned long long)(probe_mismatch + grid_mismatch));
	lpm_hash_fini(&lh);
	lpm_free(&full);
	free(probes);
	free(prefixes);
	memory_context_fini(&bench_ctx);
	free(arena);
	return 0;
#else
	if (probe_mismatch || grid_mismatch) {
		fprintf(stderr,
			"correctness FAILED: probe mismatches=%llu "
			"grid mismatches=%llu\n",
			(unsigned long long)probe_mismatch,
			(unsigned long long)grid_mismatch);
		return 2;
	}
#endif

	// Measure top-hit rate over the probe set for interpretation.
	//
	// A hit is a successful value_slot_index probe on the zero-padded
	// leading-hops bytes (no root fallback), reported by
	// value_slot_index_lookup as a value other than LPM_VALUE_INVALID.
	uint64_t hash_hits = 0;
	volatile uint32_t sink = 0;
	for (size_t i = 0; i < args.num_probes; ++i) {
		uint8_t top_key[8];
		lpm_hash_top_key(&lh, probes[i], top_key);
		uint32_t hit = value_slot_index_lookup(&lh.index, top_key);
		if (hit != LPM_VALUE_INVALID) {
			++hash_hits;
		}
		sink ^= hit;
	}
	double hash_hit_rate = (double)hash_hits / (double)args.num_probes;

	uint64_t *plain_ns = (uint64_t *)calloc(args.rounds, sizeof(uint64_t));
	uint64_t *plain_cyc = (uint64_t *)calloc(args.rounds, sizeof(uint64_t));
	uint64_t *lpm_hash_ns =
		(uint64_t *)calloc(args.rounds, sizeof(uint64_t));
	uint64_t *lpm_hash_cyc =
		(uint64_t *)calloc(args.rounds, sizeof(uint64_t));
	if (!plain_ns || !plain_cyc || !lpm_hash_ns || !lpm_hash_cyc) {
		fprintf(stderr, "timing arrays alloc failed\n");
		return 1;
	}

	for (uint32_t round = 0; round < args.rounds; ++round) {
		struct timespec ts_start;
		struct timespec ts_end;
		uint32_t acc = 0;

		clock_gettime(CLOCK_MONOTONIC, &ts_start);
		uint64_t tsc_start = rdtsc_read();
		for (size_t i = 0; i < args.num_probes; ++i) {
			acc ^= lpm8_lookup(&full, probes[i]);
		}
		uint64_t tsc_end = rdtsc_read();
		clock_gettime(CLOCK_MONOTONIC, &ts_end);
		sink ^= acc;

		plain_ns[round] = (uint64_t)(ts_end.tv_sec - ts_start.tv_sec) *
					  1000000000ULL +
				  (uint64_t)(ts_end.tv_nsec - ts_start.tv_nsec);
		plain_cyc[round] = tsc_end - tsc_start;
	}

	for (uint32_t round = 0; round < args.rounds; ++round) {
		struct timespec ts_start;
		struct timespec ts_end;
		uint32_t acc = 0;

		clock_gettime(CLOCK_MONOTONIC, &ts_start);
		uint64_t tsc_start = rdtsc_read();
		for (size_t i = 0; i < args.num_probes; ++i) {
			acc ^= lpm_hash_lookup(&lh, probes[i]);
		}
		uint64_t tsc_end = rdtsc_read();
		clock_gettime(CLOCK_MONOTONIC, &ts_end);
		sink ^= acc;

		lpm_hash_ns[round] =
			(uint64_t)(ts_end.tv_sec - ts_start.tv_sec) *
				1000000000ULL +
			(uint64_t)(ts_end.tv_nsec - ts_start.tv_nsec);
		lpm_hash_cyc[round] = tsc_end - tsc_start;
	}

	(void)sink;

	uint64_t plain_ns_med = median_u64(plain_ns, args.rounds);
	uint64_t lpm_hash_ns_med = median_u64(lpm_hash_ns, args.rounds);
	uint64_t plain_cyc_med = median_u64(plain_cyc, args.rounds);
	uint64_t lpm_hash_cyc_med = median_u64(lpm_hash_cyc, args.rounds);

	double plain_ns_per = (double)plain_ns_med / (double)args.num_probes;
	double lpm_hash_ns_per =
		(double)lpm_hash_ns_med / (double)args.num_probes;
	double plain_cyc_per = (double)plain_cyc_med / (double)args.num_probes;
	double lpm_hash_cyc_per =
		(double)lpm_hash_cyc_med / (double)args.num_probes;

	double speedup = 0.0;
	if (lpm_hash_ns_per > 0.0) {
		speedup =
			(plain_ns_per - lpm_hash_ns_per) / plain_ns_per * 100.0;
	}

	printf("\n");
	printf("=== lpm_hash (hashed top, hops=%u) vs plain 8-byte LPM ===\n",
	       args.hops);
	printf("prefixes=%zu  long_fraction=%.2f  rounds=%u\n",
	       args.num_prefixes,
	       args.long_fraction,
	       args.rounds);
	printf("long=%zu short=%zu top_entries=%u top_hit_rate=%.3f\n",
	       long_count,
	       short_count,
	       lh.index.size,
	       hash_hit_rate);
	printf("hash_table_bytes=%" PRIu64 " full_lpm_bytes=%lu "
	       "lpm_hash_lpm_bytes=%lu\n",
	       (uint64_t)lh.index.capacity * sizeof(struct lpm_hash_slot),
	       lpm_memory_usage(&full),
	       lpm_memory_usage(&lh.lpm));
	printf("\n");
	printf("                    ns/lookup   cycles/lookup\n");
	printf("plain  scalar       %8.2f      %8.1f\n",
	       plain_ns_per,
	       plain_cyc_per);
	printf("hash   scalar       %8.2f      %8.1f\n",
	       lpm_hash_ns_per,
	       lpm_hash_cyc_per);
	printf("\n");
	printf("speedup scalar vs plain (ns/lookup): %+.1f%%\n", speedup);

	free(plain_ns);
	free(plain_cyc);
	free(lpm_hash_ns);
	free(lpm_hash_cyc);
	lpm_hash_fini(&lh);
	lpm_free(&full);
	free(probes);
	free(prefixes);
	memory_context_fini(&bench_ctx);
	free(arena);

	return 0;
}
