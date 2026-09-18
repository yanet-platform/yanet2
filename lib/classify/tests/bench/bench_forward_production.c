// forward module benchmark, production path: the three lib/filter
// compiles of forward_module_config_update over a dumped ruleset, then
// the lookup path (vlan plus the family filter per packet, first match
// merge) over a capture.
//
// Usage: bench_forward_production <rules.bin> <capture.pcap> [arena MiB]
//                            [results.bin]
//
// The ruleset dump comes from dump_acl_ruleset.py - the rule shape is
// shared with the acl module; the projections follow the forward module
// checks. With results.bin on both benchmarks the per packet rule
// indices must match byte for byte.

#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "bench_acl_common.h"
#include "bench_pcap.h"

FILTER_COMPILER_DECLARE(FWD_FILTER_VLAN_TAG, device, vlan);
FILTER_QUERY_DECLARE(q_fwd_vlan, device, vlan);

FILTER_COMPILER_DECLARE(FWD_FILTER_IP4_TAG, device, vlan, net4_src, net4_dst);
FILTER_QUERY_DECLARE(q_fwd_ip4, device, vlan, net4_src, net4_dst);

FILTER_COMPILER_DECLARE(FWD_FILTER_IP6_TAG, device, vlan, net6_src, net6_dst);
FILTER_QUERY_DECLARE(q_fwd_ip6, device, vlan, net6_src, net6_dst);

#define BATCH 64

static uint32_t
merge_first(uint32_t a, uint32_t b) {
	if (a == FILTER_RULE_INVALID) {
		return b;
	}
	if (b == FILTER_RULE_INVALID) {
		return a;
	}
	return a < b ? a : b;
}

int
main(int argc, char **argv) {
	if (argc < 3) {
		fprintf(stderr,
			"usage: %s <rules.bin> <capture.pcap> [arena MiB] "
			"[results.bin]\n",
			argv[0]);
		return 1;
	}
	const char *rule_path = argv[1];
	const char *pcap_path = argv[2];
	size_t arena_mb = argc > 3 ? (size_t)atol(argv[3]) : 40000;
	const char *result_path = argc > 4 ? argv[4] : NULL;

	struct filter_rule *rules;
	const struct filter_rule **all;
	struct bench_stats stats;
	if (bench_load_full(rule_path, &rules, &all, &stats)) {
		return 1;
	}

	const struct filter_rule **proj =
		malloc(sizeof(*proj) * stats.rule_count * 3);
	uint32_t n_l2 = bench_project(proj, all, stats.rule_count, check_l2);
	uint32_t n_ip4 = bench_project(
		proj + stats.rule_count, all, stats.rule_count, check_has4_any
	);
	uint32_t n_ip6 = bench_project(
		proj + 2 * stats.rule_count,
		all,
		stats.rule_count,
		check_has6_any
	);
	printf("old forward acl.in: rules=%u l2=%u ip4=%u ip6=%u\n",
	       stats.rule_count,
	       n_l2,
	       n_ip4,
	       n_ip6);

	void *arena = malloc(arena_mb << 20);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_mb << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "bench", &allocator);

	struct filter flt_vlan, flt_ip4, flt_ip6;
	struct timespec t0, t1, tall0, tall1;
	int rc = 0;

	clock_gettime(CLOCK_MONOTONIC, &tall0);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_vlan,
		FWD_FILTER_VLAN_TAG,
		proj,
		stats.rule_count,
		&mctx,
		"vlan",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old vlan: %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip4,
		FWD_FILTER_IP4_TAG,
		proj + stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip4",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip4:  %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip6,
		FWD_FILTER_IP6_TAG,
		proj + 2 * stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip6",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip6:  %10.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &tall1);
	printf("old compile total: %.1f ms rc=%d\n",
	       bench_ms(&tall0, &tall1),
	       rc);
	if (rc) {
		return 1;
	}

	struct bench_capture cap;
	if (bench_pcap_load(pcap_path, &cap)) {
		return 1;
	}

	uint32_t *rv = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *rf = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *merged = malloc(sizeof(uint32_t) * cap.count);

	for (uint32_t off = 0; off < cap.count; off += BATCH) {
		uint32_t n = cap.count - off < BATCH ? cap.count - off : BATCH;
		filter_query(
			&flt_vlan, q_fwd_vlan, cap.ptrs + off, rv + off, n
		);
		filter_query(&flt_ip6, q_fwd_ip6, cap.ptrs + off, rf + off, n);
	}
	uint32_t matched = 0;
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		merged[idx] = merge_first(rv[idx], rf[idx]);
		matched += merged[idx] != FILTER_RULE_INVALID;
	}
	printf("old v6 lookup: matched %u/%u (%.1f%%) fnv=%llx\n",
	       matched,
	       cap.count,
	       100.0 * matched / cap.count,
	       (unsigned long long)bench_fnv1a(merged, cap.count));
	if (result_path != NULL) {
		FILE *rfp = fopen(result_path, "wb");
		fwrite(merged, sizeof(uint32_t), cap.count, rfp);
		fclose(rfp);
	}

	const uint32_t passes = 50;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	for (uint32_t pass = 0; pass < passes; ++pass) {
		for (uint32_t off = 0; off < cap.count; off += BATCH) {
			uint32_t n = cap.count - off < BATCH ? cap.count - off
							     : BATCH;
			filter_query(
				&flt_vlan,
				q_fwd_vlan,
				cap.ptrs + off,
				rv + off,
				n
			);
			filter_query(
				&flt_ip6, q_fwd_ip6, cap.ptrs + off, rf + off, n
			);
		}
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old v6 steady: %.1f ns/pkt (vlan plus ip6)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

	(void)flt_ip4;
	(void)q_fwd_ip4;
	filter_free(&flt_ip6, FWD_FILTER_IP6_TAG);
	filter_free(&flt_ip4, FWD_FILTER_IP4_TAG);
	filter_free(&flt_vlan, FWD_FILTER_VLAN_TAG);
	(void)check_ip4;
	(void)check_ip4_port;
	(void)check_ip6;
	(void)check_ip6_port;
	bench_capture_free(&cap);
	return 0;
}
