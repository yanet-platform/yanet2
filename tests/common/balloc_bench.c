#include "common/memory.h"
#include "common/memory_block.h"
#include "lib/logging/log.h"

#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/time.h>
#include <unistd.h>

#include "common/rng.h"

#ifndef PRIu64
#define PRIu64 "llu"
#endif

static uint64_t
get_time_us(void) {
	struct timeval tv;
	gettimeofday(&tv, NULL);
	return (uint64_t)tv.tv_sec * 1000000ULL + (uint64_t)tv.tv_usec;
}

static inline uint64_t
rng_next_bounded(uint64_t *r, uint64_t bound_inclusive) {
	/* returns [0..bound_inclusive] */
	if (bound_inclusive == 0)
		return 0;
	return rng_next(r) % (bound_inclusive + 1);
}

struct bench_config {
	uint64_t ops; /* number of churn iterations (each = free + alloc) */
	size_t working_set; /* number of live allocations kept */
	size_t arena_mb;    /* size of the arena in MiB fed to allocator */
	size_t min_pool;    /* min pool index (0 => 8 bytes) */
	size_t max_pool;    /* max pool index (13 => 64 KiB) */
	uint64_t seed;	    /* PRNG seed (0 => use time) */
};

#define DEFAULT_OPS (10000000ULL)
#define DEFAULT_WORKING_SET (2048u)
#define DEFAULT_ARENA_MB (64u)
#define DEFAULT_MIN_POOL (0u)
#define DEFAULT_MAX_POOL (13u)

/* Compute request size that maps to a pool index.
   Follow tests' convention: req = block_size - 2*ASAN_RED_ZONE (or 1) */
static inline size_t
pool_req_size(struct block_allocator *ba, size_t pool_idx) {
	size_t block_size = block_allocator_pool_size(ba, pool_idx);
	size_t rz2 = (size_t)ASAN_RED_ZONE * 2;
	return (block_size > rz2) ? (block_size - rz2) : 1;
}

static void
print_help(const char *argv0) {
	fprintf(stderr,
		"Usage: %s [--ops N] [--working-set N] [--arena-mb M] "
		"[--min-pool I] [--max-pool J] [--seed S]\n"
		"\n"
		"  --ops           Number of churn iterations (default: %llu)\n"
		"  --working-set   Live allocation count kept (default: %u)\n"
		"  --arena-mb      Arena size in MiB (default: %u)\n"
		"  --min-pool      Minimum pool index (default: %u)\n"
		"  --max-pool      Maximum pool index (default: %u)\n"
		"  --seed          PRNG seed (default: time-based)\n"
		"\n"
		"Pools map to block_size = 1 << (3 + pool_idx); pool 0 = 8 "
		"bytes, pool 13 = 64 KiB\n",
		argv0,
		(unsigned long long)DEFAULT_OPS,
		(unsigned)DEFAULT_WORKING_SET,
		(unsigned)DEFAULT_ARENA_MB,
		(unsigned)DEFAULT_MIN_POOL,
		(unsigned)DEFAULT_MAX_POOL);
}

static void
clamp_config(struct bench_config *cfg) {
	if (cfg->max_pool >= MEMORY_BLOCK_ALLOCATOR_EXP)
		cfg->max_pool = MEMORY_BLOCK_ALLOCATOR_EXP - 1;
	if (cfg->min_pool > cfg->max_pool)
		cfg->min_pool = cfg->max_pool;
	if (cfg->working_set == 0)
		cfg->working_set = 1;
	if (cfg->arena_mb == 0)
		cfg->arena_mb = 1;
}

static int
parse_args(int argc, char **argv, struct bench_config *cfg) {
	*cfg = (struct bench_config){
		.ops = DEFAULT_OPS,
		.working_set = DEFAULT_WORKING_SET,
		.arena_mb = DEFAULT_ARENA_MB,
		.min_pool = DEFAULT_MIN_POOL,
		.max_pool = DEFAULT_MAX_POOL,
		.seed = 0,
	};

	for (int i = 1; i < argc; ++i) {
		if (strcmp(argv[i], "--help") == 0 ||
		    strcmp(argv[i], "-h") == 0) {
			print_help(argv[0]);
			return 1;
		} else if (strcmp(argv[i], "--ops") == 0 && i + 1 < argc) {
			cfg->ops = strtoull(argv[++i], NULL, 10);
		} else if (strcmp(argv[i], "--working-set") == 0 &&
			   i + 1 < argc) {
			cfg->working_set =
				(size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(argv[i], "--arena-mb") == 0 && i + 1 < argc) {
			cfg->arena_mb = (size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(argv[i], "--min-pool") == 0 && i + 1 < argc) {
			cfg->min_pool = (size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(argv[i], "--max-pool") == 0 && i + 1 < argc) {
			cfg->max_pool = (size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(argv[i], "--seed") == 0 && i + 1 < argc) {
			cfg->seed = strtoull(argv[++i], NULL, 16);
			if (cfg->seed == 0) {
				/* allow decimal too */
				cfg->seed = strtoull(argv[i], NULL, 10);
			}
		} else {
			fprintf(stderr,
				"Unknown or incomplete arg: %s\n",
				argv[i]);
			print_help(argv[0]);
			return -1;
		}
	}

	clamp_config(cfg);
	return 0;
}

int
main(int argc, char **argv) {
	struct bench_config cfg;
	int par = parse_args(argc, argv, &cfg);
	if (par != 0) {
		return (par > 0) ? 0 : 1;
	}

	log_enable_name("info");

	LOG(INFO, "balloc_bench: preparing allocator...");

	/* 1) Prepare allocator with a single contiguous arena */
	size_t arena_size = cfg.arena_mb * 1024ULL * 1024ULL;
	void *arena = malloc(arena_size);
	if (!arena) {
		LOG(ERROR, "failed to malloc arena of %zu MiB", cfg.arena_mb);
		return 1;
	}

	struct block_allocator ba;
	if (block_allocator_init(&ba) != 0) {
		LOG(ERROR, "block_allocator_init failed");
		free(arena);
		return 1;
	}
	block_allocator_put_arena(&ba, arena, arena_size);

	struct memory_context mctx;
	if (memory_context_init(&mctx, "balloc.bench", &ba) != 0) {
		LOG(ERROR, "memory_context_init failed");
		free(arena);
		return 1;
	}

	/* 2) Working-set storage */
	void **slots = (void **)calloc(cfg.working_set, sizeof(void *));
	size_t *sizes = (size_t *)calloc(cfg.working_set, sizeof(size_t));
	if (!slots || !sizes) {
		LOG(ERROR, "failed to allocate working-set tables");
		free(slots);
		free(sizes);
		free(arena);
		return 1;
	}

	/* 3) RNG */
	if (cfg.seed == 0) {
		cfg.seed = ((uint64_t)get_time_us() ^ 0x9e3779b97f4a7c15ULL) +
			   (uint64_t)(uintptr_t)slots;
	}
	uint64_t rng = cfg.seed;

	LOG(INFO,
	    "balloc_bench: ops=%" PRIu64
	    " working_set=%zu arena_mb=%zu min_pool=%zu max_pool=%zu "
	    "seed=0x%016" PRIx64,
	    cfg.ops,
	    cfg.working_set,
	    cfg.arena_mb,
	    cfg.min_pool,
	    cfg.max_pool,
	    cfg.seed);

	/* 4) Fill working set to steady-state occupancy */
	uint64_t init_fail = 0;
	for (size_t i = 0; i < cfg.working_set; ++i) {
		/* pick a random pool in [min..max] */
		size_t pool_span = cfg.max_pool - cfg.min_pool;
		size_t pool_idx = cfg.min_pool +
				  (size_t)rng_next_bounded(&rng, pool_span);

		void *p = NULL;
		size_t req = 0;

		/* fallback towards smaller pools on failure */
		for (size_t attempt = 0;
		     pool_idx + 1 > 0 && attempt < MEMORY_BLOCK_ALLOCATOR_EXP;
		     ++attempt) {
			req = pool_req_size(&ba, pool_idx);
			p = memory_balloc(&mctx, req);
			if (p != NULL)
				break;
			if (pool_idx == cfg.min_pool)
				break;
			pool_idx--;
		}
		if (p == NULL) {
			init_fail++;
			/* leave empty, will be filled during churn or remain
			 * empty */
		} else {
			slots[i] = p;
			sizes[i] = req;
		}
	}
	if (init_fail) {
		LOG(WARN,
		    "initial fill: %" PRIu64
		    " allocations failed (will continue)",
		    init_fail);
	}

	/* 5) Churn loop: constant occupancy by free then alloc in same slot */
	LOG(INFO, "balloc_bench: running...");
	uint64_t start_us = get_time_us();

	uint64_t alloc_fail = 0;

	for (uint64_t it = 0; it < cfg.ops; ++it) {
		size_t idx = (size_t)(rng_next(&rng) % cfg.working_set);

		/* free old */
		if (slots[idx] != NULL) {
			memory_bfree(&mctx, slots[idx], sizes[idx]);
			slots[idx] = NULL;
			sizes[idx] = 0;
		}

		/* allocate new with random pool; fallback to smaller */
		size_t pool_span = cfg.max_pool - cfg.min_pool;
		size_t pool_idx = cfg.min_pool +
				  (size_t)rng_next_bounded(&rng, pool_span);

		void *p = NULL;
		size_t req = 0;

		for (size_t attempt = 0;
		     pool_idx + 1 > 0 && attempt < MEMORY_BLOCK_ALLOCATOR_EXP;
		     ++attempt) {
			req = pool_req_size(&ba, pool_idx);
			p = memory_balloc(&mctx, req);
			if (p != NULL)
				break;
			if (pool_idx == cfg.min_pool)
				break;
			pool_idx--;
		}

		if (p != NULL) {
			slots[idx] = p;
			sizes[idx] = req;
		} else {
			alloc_fail++;
			/* leave slot empty; future iterations will try again */
		}
	}

	uint64_t end_us = get_time_us();
	double elapsed_s = (double)(end_us - start_us) / 1e6;

	/* 6) Stats */
	double ops_per_s =
		(elapsed_s > 0.0) ? (double)cfg.ops / elapsed_s : 0.0;

	/* occupancy stats */
	size_t live = 0;
	size_t live_bytes = 0;
	for (size_t i = 0; i < cfg.working_set; ++i) {
		if (slots[i] != NULL) {
			live++;
			live_bytes += sizes[i];
		}
	}
	LOG(INFO,
	    "balloc_bench: elapsed=%.3f s; throughput=%.2f Mops/s",
	    elapsed_s,
	    ops_per_s / 1e6);
	LOG(INFO,
	    "balloc_bench: live=%zu/%zu (%.1f%%), live_bytes=%.2f MiB, "
	    "alloc_fail=%" PRIu64,
	    live,
	    cfg.working_set,
	    100.0 * (double)live / (double)cfg.working_set,
	    (double)live_bytes / (1024.0 * 1024.0),
	    alloc_fail);

	/* 7) Cleanup: free live blocks, teardown */
	for (size_t i = 0; i < cfg.working_set; ++i) {
		if (slots[i] != NULL)
			memory_bfree(&mctx, slots[i], sizes[i]);
	}

	free(slots);
	free(sizes);
	free(arena);

	return 0;
}