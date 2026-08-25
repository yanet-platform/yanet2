/*
 * Benchmark of the four net6 half-tries (src hi/lo, dst hi/lo) filled by the
 * real filter compiler from a production-scale prefix list.
 *
 * Input is one or two files of "addr/plen" lines: the first fills the src
 * tries, an optional second fills the dst tries (default: same list). One
 * rule carries every prefix in both directions, so filter_init compiles all
 * four lpm tries through collect_net6_range exactly as a config apply
 * would.
 *
 * Probes are whole IPv6 addresses: a configurable fraction is drawn inside a
 * random prefix (uniform host bits), the rest uniform over the whole space,
 * so both top-hash hits and root-fallback misses are exercised.
 *
 * Correctness gate: after warming the memos, every replayed verdict must
 * equal the plain lpm8 walks plus the combine fetch before timing runs.
 *
 * Timing compares, over the same probe set, the four scalar half-lookups
 * per address through the plain tries (lpm8_lookup, the acl dataplane
 * walk) against the memoized query path: memo probe first, and on a miss
 * the walks plus the comb fetch, inserting the verdict.
 *
 * ns/lookup is per address (four trie walks); ns/classify adds the comb
 * fetches. rdtsc cycles are frequency-independent, CLOCK_MONOTONIC ns the
 * human headline, medians over --rounds runs.
 */

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/rng.h"
#include "lib/filter/classifiers/net6.h"
#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/logging/log.h"

#include <x86intrin.h>

#include <arpa/inet.h>
#include <inttypes.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#define QUAD_RULES_ARENA_SIZE ((size_t)16 << 30)

FILTER_COMPILER_DECLARE(bench_net6_quad_compile, net6_src, net6_dst);

// Accumulator for the timed loops. Volatile so the compiler cannot fold a
// lookup loop into nothing when its only consumer is a discarded value.
static volatile uint32_t quad_sink;

struct quad_args {
	const char *src_file;
	const char *dst_file;
	const char *pcap_file;
	size_t num_probes;
	double hit_fraction;
	uint32_t rounds;
	uint64_t seed;
	bool with_comb;
};

static void
print_help(const char *argv0) {
	fprintf(stderr,
		"Usage: %s src_prefixes.txt [dst_prefixes.txt] [options]\n"
		"\n"
		"  --probes N        whole-address probes (default 1048576)\n"
		"  --hit-fraction F  probes drawn inside a prefix, 0..1 "
		"(default 0.8)\n"
		"  --rounds N        timed rounds, median reported (default "
		"11)\n"
		"  --seed S          PRNG seed, hex (default time-based)\n"
		"  --no-comb         skip the combine fetch timing\n"
		"  --pcap FILE       take probe addresses from IPv6 packets in "
		"a\n"
		"                    classic pcap (overrides --probes and\n"
		"                    --hit-fraction)\n",
		argv0);
}

static int
parse_args(int argc, char **argv, struct quad_args *args) {
	*args = (struct quad_args){
		.src_file = NULL,
		.dst_file = NULL,
		.pcap_file = NULL,
		.num_probes = 1u << 20,
		.hit_fraction = 0.8,
		.rounds = 11,
		.seed = 0,
		.with_comb = true,
	};

	int positional = 0;
	for (int i = 1; i < argc; ++i) {
		const char *a = argv[i];
		if (strcmp(a, "-h") == 0 || strcmp(a, "--help") == 0) {
			print_help(argv[0]);
			return 1;
		} else if (strcmp(a, "--probes") == 0 && i + 1 < argc) {
			args->num_probes =
				(size_t)strtoull(argv[++i], NULL, 10);
		} else if (strcmp(a, "--hit-fraction") == 0 && i + 1 < argc) {
			args->hit_fraction = strtod(argv[++i], NULL);
		} else if (strcmp(a, "--rounds") == 0 && i + 1 < argc) {
			args->rounds = (uint32_t)strtoul(argv[++i], NULL, 10);
		} else if (strcmp(a, "--seed") == 0 && i + 1 < argc) {
			args->seed = strtoull(argv[++i], NULL, 16);
		} else if (strcmp(a, "--no-comb") == 0) {
			args->with_comb = false;
		} else if (strcmp(a, "--pcap") == 0 && i + 1 < argc) {
			args->pcap_file = argv[++i];
		} else if (a[0] == '-') {
			fprintf(stderr, "unknown arg: %s\n", a);
			print_help(argv[0]);
			return -1;
		} else if (positional == 0) {
			args->src_file = a;
			++positional;
		} else if (positional == 1) {
			args->dst_file = a;
			++positional;
		} else {
			fprintf(stderr, "unexpected argument: %s\n", a);
			return -1;
		}
	}

	if (args->src_file == NULL) {
		print_help(argv[0]);
		return -1;
	}
	if (args->dst_file == NULL) {
		args->dst_file = args->src_file;
	}
	if (args->num_probes == 0 || args->rounds == 0) {
		fprintf(stderr, "counts must be > 0\n");
		return -1;
	}
	if (args->hit_fraction < 0.0) {
		args->hit_fraction = 0.0;
	}
	if (args->hit_fraction > 1.0) {
		args->hit_fraction = 1.0;
	}
	return 0;
}

// Parses one "addr/plen" line into a normalized (masked) net6.
static int
parse_net6_line(const char *line, struct net6 *out) {
	char buf[256];
	snprintf(buf, sizeof(buf), "%s", line);
	char *slash = strchr(buf, '/');
	if (!slash) {
		return -1;
	}
	*slash = 0;
	int plen = atoi(slash + 1);
	if (plen < 0 || plen > 128) {
		return -1;
	}

	struct in6_addr in6;
	if (inet_pton(AF_INET6, buf, &in6) != 1) {
		return -1;
	}

	memcpy(out->addr, in6.s6_addr, NET6_LEN);
	memset(out->mask, 0, NET6_LEN);
	for (int i = 0; i < plen; ++i) {
		out->mask[i / 8] |= 0x80 >> (i % 8);
	}
	for (int i = 0; i < NET6_LEN; ++i) {
		out->addr[i] &= out->mask[i];
	}
	return 0;
}

static struct net6 *
load_prefixes(const char *path, size_t *count_out) {
	FILE *f = fopen(path, "r");
	if (f == NULL) {
		fprintf(stderr, "cannot open %s\n", path);
		return NULL;
	}

	size_t cap = 1024;
	size_t count = 0;
	struct net6 *prefixes = malloc(cap * sizeof(*prefixes));
	char line[256];
	while (fgets(line, sizeof(line), f)) {
		line[strcspn(line, "\r\n")] = 0;
		if (line[0] == 0) {
			continue;
		}
		if (count == cap) {
			cap *= 2;
			prefixes = realloc(prefixes, cap * sizeof(*prefixes));
			if (prefixes == NULL) {
				fclose(f);
				return NULL;
			}
		}
		if (parse_net6_line(line, &prefixes[count]) != 0) {
			fprintf(stderr, "skip unparseable line: %s\n", line);
			continue;
		}
		++count;
	}
	fclose(f);

	*count_out = count;
	return prefixes;
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

// Classic-pcap link types and ethertypes used by the address extractor.
#define QUAD_PCAP_SNAPLEN_MAX 262144

// Reads IPv6 (src, dst) address pairs from a classic pcap file, in packet
// order. Ethernet link type with any number of 802.1Q/802.1ad tags; packets
// of other families are skipped. Returns 0 on success.
static int
load_pcap_addrs(
	const char *path,
	uint8_t (**src_out)[16],
	uint8_t (**dst_out)[16],
	size_t *count_out
) {
	FILE *f = fopen(path, "rb");
	if (f == NULL) {
		fprintf(stderr, "cannot open %s\n", path);
		return -1;
	}

	uint8_t ghdr[24];
	if (fread(ghdr, 1, 24, f) != 24) {
		fprintf(stderr, "%s: short global header\n", path);
		fclose(f);
		return -1;
	}

	uint32_t magic = (uint32_t)ghdr[0] << 24 | (uint32_t)ghdr[1] << 16 |
			 (uint32_t)ghdr[2] << 8 | ghdr[3];
	bool le;
	if (magic == 0xa1b2c3d4 || magic == 0xa1b23c4d) {
		le = false;
	} else if (magic == 0xd4c3b2a1 || magic == 0x4d3cb2a1) {
		le = true;
	} else {
		fprintf(stderr, "%s: not a classic pcap file\n", path);
		fclose(f);
		return -1;
	}

	static uint8_t pkt[QUAD_PCAP_SNAPLEN_MAX];
	size_t cap = 4096;
	size_t count = 0;
	uint8_t(*src)[16] = malloc(cap * 16);
	uint8_t(*dst)[16] = malloc(cap * 16);
	if (!src || !dst) {
		fclose(f);
		return -1;
	}

	uint8_t phdr[16];
	for (;;) {
		if (fread(phdr, 1, 16, f) != 16) {
			break;
		}
		uint32_t caplen =
			le ? (uint32_t)phdr[8] | (uint32_t)phdr[9] << 8 |
					(uint32_t)phdr[10] << 16 |
					(uint32_t)phdr[11] << 24
			   : (uint32_t)phdr[8] << 24 | (uint32_t)phdr[9] << 16 |
					(uint32_t)phdr[10] << 8 | phdr[11];
		if (caplen > sizeof(pkt)) {
			fprintf(stderr,
				"%s: packet exceeds snaplen buffer\n",
				path);
			break;
		}
		if (fread(pkt, 1, caplen, f) != caplen) {
			break;
		}
		if (caplen < 14) {
			continue;
		}

		uint32_t et = (uint32_t)pkt[12] << 8 | pkt[13];
		size_t off = 14;
		while ((et == 0x8100 || et == 0x88a8) && caplen >= off + 4) {
			et = (uint32_t)pkt[off + 2] << 8 | pkt[off + 3];
			off += 4;
		}
		if (et != 0x86dd || caplen < off + 40) {
			continue;
		}

		if (count == cap) {
			cap *= 2;
			src = realloc(src, cap * 16);
			dst = realloc(dst, cap * 16);
			if (!src || !dst) {
				fclose(f);
				return -1;
			}
		}
		memcpy(src[count], pkt + off + 8, 16);
		memcpy(dst[count], pkt + off + 24, 16);
		++count;
	}
	fclose(f);

	if (count == 0) {
		fprintf(stderr, "%s: no IPv6 packets found\n", path);
		return -1;
	}

	*src_out = src;
	*dst_out = dst;
	*count_out = count;
	return 0;
}

static int
cmp_u32(const void *a, const void *b) {
	uint32_t ua = *(const uint32_t *)a;
	uint32_t ub = *(const uint32_t *)b;
	return (ua > ub) - (ua < ub);
}

// Prints the distinct-class count and the hottest classes of one replayed
// half-lookup, weighted per packet.
static void
print_class_stats(const char *label, const uint32_t *values, size_t count) {
	uint32_t *sorted = malloc(count * sizeof(*sorted));
	uint32_t *run_v = malloc(count * sizeof(*run_v));
	size_t *run_c = malloc(count * sizeof(*run_c));
	if (!sorted || !run_v || !run_c) {
		free(sorted);
		free(run_v);
		free(run_c);
		return;
	}
	memcpy(sorted, values, count * sizeof(*sorted));
	qsort(sorted, count, sizeof(*sorted), cmp_u32);

	size_t distinct = 0;
	for (size_t idx = 0; idx < count;) {
		size_t run = 1;
		while (idx + run < count && sorted[idx + run] == sorted[idx]) {
			++run;
		}
		run_v[distinct] = sorted[idx];
		run_c[distinct] = run;
		++distinct;
		idx += run;
	}

	// Top-3 runs by selection; ties resolved by first appearance.
	uint32_t top_v[3] = {0, 0, 0};
	size_t top_c[3] = {0, 0, 0};
	for (int place = 0; place < 3; ++place) {
		int best = -1;
		for (size_t idx = 0; idx < distinct; ++idx) {
			if (run_c[idx] == 0) {
				continue;
			}
			if (best < 0 || run_c[idx] > run_c[best]) {
				best = (int)idx;
			}
		}
		if (best < 0) {
			break;
		}
		top_v[place] = run_v[best];
		top_c[place] = run_c[best];
		run_c[best] = 0;
	}

	printf("  %-8s classes hit: %zu; hottest:", label, distinct);
	for (int place = 0; place < 3; ++place) {
		if (top_c[place] > 0) {
			printf("  %u(%.1f%%)",
			       top_v[place],
			       100.0 * (double)top_c[place] / (double)count);
		}
	}
	printf("\n");
	free(sorted);
	free(run_v);
	free(run_c);
}

// Draws one whole probe address: under a random prefix (uniform host bits)
// with probability hit_fraction, uniform over the full space otherwise.
static void
gen_probe_addr(
	const struct net6 *prefixes,
	size_t prefix_count,
	double hit_fraction,
	uint64_t *rng,
	uint8_t addr[NET6_LEN]
) {
	double roll = (double)(rng_next(rng) >> 11) / (double)(1ULL << 53);
	if (roll < hit_fraction && prefix_count) {
		const struct net6 *spec =
			&prefixes[rng_next(rng) % prefix_count];
		memcpy(addr, spec->addr, NET6_LEN);
		for (int idx = 0; idx < NET6_LEN; ++idx) {
			addr[idx] |= (uint8_t)rng_next(rng) & ~spec->mask[idx];
		}
	} else {
		for (int idx = 0; idx < NET6_LEN; ++idx) {
			addr[idx] = (uint8_t)rng_next(rng);
		}
	}
}

static int
cmp_u64(const void *a, const void *b) {
	uint64_t ua = *(const uint64_t *)a;
	uint64_t ub = *(const uint64_t *)b;
	return (ua > ub) - (ua < ub);
}

static uint64_t
median_u64(uint64_t *values, uint32_t count) {
	qsort(values, count, sizeof(*values), cmp_u64);
	return values[count / 2];
}

// Times the four scalar plain half-lookups per address (the pre-hash walk).
static uint64_t
time_plain_quad_scalar(
	struct net6_classifier *src,
	struct net6_classifier *dst,
	const uint8_t (*src_addrs)[16],
	const uint8_t (*dst_addrs)[16],
	size_t count
) {
	uint64_t tsc_start = rdtsc_read();

	for (size_t idx = 0; idx < count; ++idx) {
		uint32_t acc = 0;
		acc ^= lpm8_lookup(&src->hi, src_addrs[idx]);
		acc ^= lpm8_lookup(&src->lo, src_addrs[idx] + 8);
		acc ^= lpm8_lookup(&dst->hi, dst_addrs[idx]);
		acc ^= lpm8_lookup(&dst->lo, dst_addrs[idx] + 8);
		quad_sink ^= acc;
	}

	return rdtsc_read() - tsc_start;
}

// Times the full memoized classification per address: memo probe first,
// and on a miss the two hashed-top walks plus the comb fetch, inserting
// the verdict. This is the query-path shape with a warm memo.
static uint64_t
time_memo_quad(
	struct net6_classifier *src,
	struct net6_classifier *dst,
	const uint8_t (*src_addrs)[16],
	const uint8_t (*dst_addrs)[16],
	size_t count
) {
	uint64_t tsc_start = rdtsc_read();

	for (size_t idx = 0; idx < count; ++idx) {
		uint32_t verdict;
		if (!net6_memo_lookup(&src->memo, src_addrs[idx], &verdict)) {
			uint32_t hi = lpm8_lookup(&src->hi, src_addrs[idx]);
			uint32_t lo = lpm8_lookup(&src->lo, src_addrs[idx] + 8);
			verdict = value_table_get(&src->comb, hi, lo);
			net6_memo_insert(&src->memo, src_addrs[idx], verdict);
		}
		quad_sink ^= verdict;
		if (!net6_memo_lookup(&dst->memo, dst_addrs[idx], &verdict)) {
			uint32_t hi = lpm8_lookup(&dst->hi, dst_addrs[idx]);
			uint32_t lo = lpm8_lookup(&dst->lo, dst_addrs[idx] + 8);
			verdict = value_table_get(&dst->comb, hi, lo);
			net6_memo_insert(&dst->memo, dst_addrs[idx], verdict);
		}
		quad_sink ^= verdict;
	}

	return rdtsc_read() - tsc_start;
}

static void
print_trie_stats(const char *label, const struct lpm *lpm) {
	printf("  %-8s pages=%zu\n", label, lpm->page_count);
}

int
main(int argc, char **argv) {
	struct quad_args args;
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

	size_t src_count = 0;
	struct net6 *src_prefixes = load_prefixes(args.src_file, &src_count);
	if (src_prefixes == NULL || src_count == 0) {
		fprintf(stderr,
			"no src prefixes loaded from %s\n",
			args.src_file);
		return 1;
	}
	size_t dst_count = 0;
	struct net6 *dst_prefixes = load_prefixes(args.dst_file, &dst_count);
	if (dst_prefixes == NULL || dst_count == 0) {
		fprintf(stderr,
			"no dst prefixes loaded from %s\n",
			args.dst_file);
		return 1;
	}

	LOG(INFO,
	    "bench_net6_quad: src_prefixes=%zu (%s) dst_prefixes=%zu (%s) "
	    "probes=%zu hit_fraction=%.2f pcap=%s rounds=%u seed=0x%016" PRIx64,
	    src_count,
	    args.src_file,
	    dst_count,
	    args.dst_file,
	    args.num_probes,
	    args.hit_fraction,
	    args.pcap_file ? args.pcap_file : "-",
	    args.rounds,
	    args.seed);

	// One rule carrying every prefix in both directions fills all four
	// tries through the production compile path.
	struct filter_rule rule = {0};
	rule.net6.srcs = src_prefixes;
	rule.net6.src_count = (uint32_t)src_count;
	rule.net6.dsts = dst_prefixes;
	rule.net6.dst_count = (uint32_t)dst_count;
	const struct filter_rule *rule_ptrs[1] = {&rule};

	void *arena = malloc(QUAD_RULES_ARENA_SIZE);
	if (arena == NULL) {
		fprintf(stderr,
			"arena malloc failed (%zu bytes)\n",
			QUAD_RULES_ARENA_SIZE);
		return 1;
	}

	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, QUAD_RULES_ARENA_SIZE);

	struct memory_context mctx;
	if (memory_context_init(&mctx, "bench_net6_quad", &allocator) != 0) {
		fprintf(stderr, "memory_context_init failed\n");
		return 1;
	}

	struct filter filter;
	uint64_t build_ns_start = (uint64_t)clock();
	struct timespec build_ts_start;
	clock_gettime(CLOCK_MONOTONIC, &build_ts_start);
	if (filter_init(
		    &filter,
		    bench_net6_quad_compile,
		    rule_ptrs,
		    1,
		    &mctx,
		    "bench_net6_quad",
		    NULL
	    ) != 0) {
		fprintf(stderr, "filter_init failed (arena exhausted?)\n");
		return 1;
	}
	struct timespec build_ts_end;
	clock_gettime(CLOCK_MONOTONIC, &build_ts_end);
	uint64_t build_ms =
		((uint64_t)(build_ts_end.tv_sec - build_ts_start.tv_sec) *
			 1000ULL +
		 (uint64_t)(build_ts_end.tv_nsec - build_ts_start.tv_nsec) /
			 1000000ULL);
	(void)build_ns_start;

	// The signature declares net6_src first, net6_dst second, so their
	// classifier payloads sit on leaves lookup_count + 0 and + 1.
	const uint64_t lookup_count = bench_net6_quad_compile->lookup_count;
	struct net6_classifier *src_cls = (struct net6_classifier *)ADDR_OF(
		&filter.v[lookup_count + 0].data
	);
	struct net6_classifier *dst_cls = (struct net6_classifier *)ADDR_OF(
		&filter.v[lookup_count + 1].data
	);
	if (src_cls == NULL || dst_cls == NULL) {
		fprintf(stderr, "classifier payloads missing on the leaves\n");
		return 1;
	}

	printf("compiled in %llu ms; comb dims src=%ux%u dst=%ux%u\n",
	       (unsigned long long)build_ms,
	       src_cls->comb.v_dim,
	       src_cls->comb.h_dim,
	       dst_cls->comb.v_dim,
	       dst_cls->comb.h_dim);

	// Probe set: whole addresses, split into the four half-key planes the
	// acl dataplane packs.
	uint8_t(*src_addrs)[16] = malloc(args.num_probes * 16);
	uint8_t(*dst_addrs)[16] = malloc(args.num_probes * 16);
	uint8_t(*src_hi_keys)[8] = malloc(args.num_probes * 8);
	uint8_t(*src_lo_keys)[8] = malloc(args.num_probes * 8);
	uint8_t(*dst_hi_keys)[8] = malloc(args.num_probes * 8);
	uint8_t(*dst_lo_keys)[8] = malloc(args.num_probes * 8);
	uint32_t *src_hi = malloc(args.num_probes * sizeof(uint32_t));
	uint32_t *src_lo = malloc(args.num_probes * sizeof(uint32_t));
	uint32_t *dst_hi = malloc(args.num_probes * sizeof(uint32_t));
	uint32_t *dst_lo = malloc(args.num_probes * sizeof(uint32_t));
	uint32_t *ref_hi = malloc(args.num_probes * sizeof(uint32_t));
	uint32_t *ref_lo = malloc(args.num_probes * sizeof(uint32_t));
	if (!src_addrs || !dst_addrs || !src_hi_keys || !src_lo_keys ||
	    !dst_hi_keys || !dst_lo_keys || !src_hi || !src_lo || !dst_hi ||
	    !dst_lo || !ref_hi || !ref_lo) {
		fprintf(stderr, "probe allocation failed\n");
		return 1;
	}

	if (args.pcap_file != NULL) {
		uint8_t(*pcap_src)[16] = NULL;
		uint8_t(*pcap_dst)[16] = NULL;
		size_t pkt_count = 0;
		if (load_pcap_addrs(
			    args.pcap_file, &pcap_src, &pcap_dst, &pkt_count
		    ) != 0) {
			return 1;
		}
		LOG(INFO,
		    "bench_net6_quad: pcap %s yielded %zu IPv6 packets",
		    args.pcap_file,
		    pkt_count);
		if (pkt_count > args.num_probes) {
			pkt_count = args.num_probes;
		}
		args.num_probes = pkt_count;
		for (size_t idx = 0; idx < args.num_probes; ++idx) {
			memcpy(src_addrs[idx], pcap_src[idx], 16);
			memcpy(dst_addrs[idx], pcap_dst[idx], 16);
		}
		free(pcap_src);
		free(pcap_dst);
	} else {
		for (size_t idx = 0; idx < args.num_probes; ++idx) {
			gen_probe_addr(
				src_prefixes,
				src_count,
				args.hit_fraction,
				&rng,
				src_addrs[idx]
			);
			gen_probe_addr(
				dst_prefixes,
				dst_count,
				args.hit_fraction,
				&rng,
				dst_addrs[idx]
			);
		}
	}
	for (size_t idx = 0; idx < args.num_probes; ++idx) {
		memcpy(src_hi_keys[idx], src_addrs[idx], 8);
		memcpy(src_lo_keys[idx], src_addrs[idx] + 8, 8);
		memcpy(dst_hi_keys[idx], dst_addrs[idx], 8);
		memcpy(dst_lo_keys[idx], dst_addrs[idx] + 8, 8);
	}

	// Correctness gate: warm the memos once (which fills them through
	// the compute path), then require every memo hit to equal the plain
	// walks plus the combine fetch. A miss is valid direct-mapped
	// behavior — a later address evicted the slot — and merely
	// recomputes at run time, so only a hit carrying a wrong verdict
	// fails the gate. The per-half class results of the plain walks
	// feed the replay stats below.
	{
		(void)time_memo_quad(
			src_cls, dst_cls, src_addrs, dst_addrs, args.num_probes
		);

		struct net6_classifier *cls[2] = {src_cls, dst_cls};
		const uint8_t(*addrs[2])[16] = {src_addrs, dst_addrs};
		const uint8_t(*key_planes[2][2]
		)[8] = {{src_hi_keys, src_lo_keys}, {dst_hi_keys, dst_lo_keys}};
		uint32_t *outs[2][2] = {{src_hi, src_lo}, {dst_hi, dst_lo}};
		const char *names[2] = {"src", "dst"};
		for (int dir = 0; dir < 2; ++dir) {
			for (size_t idx = 0; idx < args.num_probes; ++idx) {
				uint32_t hi = lpm8_lookup(
					&cls[dir]->hi, key_planes[dir][0][idx]
				);
				uint32_t lo = lpm8_lookup(
					&cls[dir]->lo, key_planes[dir][1][idx]
				);
				uint32_t expected = value_table_get(
					&cls[dir]->comb, hi, lo
				);
				uint32_t verdict = 0;
				if (net6_memo_lookup(
					    &cls[dir]->memo,
					    addrs[dir][idx],
					    &verdict
				    ) &&
				    verdict != expected) {
					fprintf(stderr,
						"%s memo mismatch at %zu: "
						"memo=%u expected=%u\n",
						names[dir],
						idx,
						verdict,
						expected);
					return 2;
				}
				outs[dir][0][idx] = hi;
				outs[dir][1][idx] = lo;
			}
		}
	}

	printf("trie fill:\n");
	print_trie_stats("src_hi", &src_cls->hi);
	print_trie_stats("src_lo", &src_cls->lo);
	print_trie_stats("dst_hi", &dst_cls->hi);
	print_trie_stats("dst_lo", &dst_cls->lo);

	printf("replay class hits (%zu addresses):\n", args.num_probes);
	print_class_stats("src_hi", src_hi, args.num_probes);
	print_class_stats("src_lo", src_lo, args.num_probes);
	print_class_stats("dst_hi", dst_hi, args.num_probes);
	print_class_stats("dst_lo", dst_lo, args.num_probes);

	uint64_t tsc_hz = calibrate_tsc_hz();
	LOG(INFO,
	    "bench_net6_quad: tsc_hz ~= %" PRIu64 " (%.2f GHz)",
	    tsc_hz,
	    (double)tsc_hz / 1e9);

	uint64_t *plain_cyc = calloc(args.rounds, sizeof(uint64_t));
	uint64_t *plain_ns = calloc(args.rounds, sizeof(uint64_t));
	uint64_t *memo_cyc = calloc(args.rounds, sizeof(uint64_t));
	uint64_t *memo_ns = calloc(args.rounds, sizeof(uint64_t));
	if (!plain_cyc || !plain_ns || !memo_cyc || !memo_ns) {
		fprintf(stderr, "timing arrays alloc failed\n");
		return 1;
	}

	for (uint32_t round = 0; round < args.rounds; ++round) {
		struct timespec ts_start;
		struct timespec ts_end;

		clock_gettime(CLOCK_MONOTONIC, &ts_start);
		plain_cyc[round] = time_plain_quad_scalar(
			src_cls, dst_cls, src_addrs, dst_addrs, args.num_probes
		);
		clock_gettime(CLOCK_MONOTONIC, &ts_end);
		plain_ns[round] = (uint64_t)(ts_end.tv_sec - ts_start.tv_sec) *
					  1000000000ULL +
				  (uint64_t)(ts_end.tv_nsec - ts_start.tv_nsec);

		clock_gettime(CLOCK_MONOTONIC, &ts_start);
		memo_cyc[round] = time_memo_quad(
			src_cls, dst_cls, src_addrs, dst_addrs, args.num_probes
		);
		clock_gettime(CLOCK_MONOTONIC, &ts_end);
		memo_ns[round] = (uint64_t)(ts_end.tv_sec - ts_start.tv_sec) *
					 1000000000ULL +
				 (uint64_t)(ts_end.tv_nsec - ts_start.tv_nsec);
	}

	double per = (double)args.num_probes;
	double plain_ns_per = (double)median_u64(plain_ns, args.rounds) / per;
	double plain_cyc_per = (double)median_u64(plain_cyc, args.rounds) / per;
	double memo_ns_per = (double)median_u64(memo_ns, args.rounds) / per;
	double memo_cyc_per = (double)median_u64(memo_cyc, args.rounds) / per;

	printf("\n");
	printf("=== four-trie scalar address lookup: %s ===\n",
	       args.with_comb ? "tries only (comb not in scalar helpers)"
			      : "tries only");
	printf("                  ns/address   cycles/address\n");
	printf("plain  walks only  %10.2f     %10.1f\n",
	       plain_ns_per,
	       plain_cyc_per);
	printf("memo   full query  %10.2f     %10.1f\n",
	       memo_ns_per,
	       memo_cyc_per);
	printf("\n");
	printf("speedup memo vs plain walks: %+.1f%% (ns)\n",
	       plain_ns_per > 0.0
		       ? (plain_ns_per - memo_ns_per) / plain_ns_per * 100.0
		       : 0.0);

	free(plain_cyc);
	free(plain_ns);
	free(memo_cyc);
	free(memo_ns);
	free(src_addrs);
	free(dst_addrs);
	free(src_hi_keys);
	free(src_lo_keys);
	free(dst_hi_keys);
	free(dst_lo_keys);
	free(src_hi);
	free(src_lo);
	free(dst_hi);
	free(dst_lo);
	free(ref_hi);
	free(ref_lo);

	filter_free(&filter, bench_net6_quad_compile);
	memory_context_fini(&mctx);
	free(src_prefixes);
	free(dst_prefixes);
	free(arena);

	return 0;
}
