// acl module benchmark, production path: the five lib/filter compiles
// of acl_module_compile_rules over a dumped ruleset, then the v6
// lookup path (both v6 filters per packet, first match merge) over a
// capture.
//
// Usage: bench_acl_production <rules.bin> <capture.pcap> [arena MiB]
//                        [results.bin]
//
// The ruleset dump comes from dump_acl_ruleset.py; the optional
// results.bin holds the per packet rule indices for cross checking
// against the core based benchmark.

#include "lib/filter/compiler.h"
#include "lib/filter/filter.h"
#include "lib/filter/query.h"

#include "bench_acl_common.h"
#include "bench_pcap.h"

FILTER_COMPILER_DECLARE(f_vlan, device, vlan);
FILTER_QUERY_DECLARE(q_vlan, device, vlan);

FILTER_COMPILER_DECLARE(
	f_ip4, device, vlan, net4_src, net4_dst, ip_frag, proto_range
);
FILTER_QUERY_DECLARE(
	q_ip4, device, vlan, net4_src, net4_dst, ip_frag, proto_range
);

FILTER_COMPILER_DECLARE(
	f_ip4_port,
	device,
	vlan,
	net4_src,
	net4_dst,
	proto_range,
	port_src,
	port_dst
);
FILTER_QUERY_DECLARE(
	q_ip4_port,
	device,
	vlan,
	net4_src,
	net4_dst,
	proto_range,
	port_src,
	port_dst
);

FILTER_COMPILER_DECLARE(
	f_ip6, device, vlan, net6_src, net6_dst, ip_frag, proto_range
);
FILTER_QUERY_DECLARE(
	q_ip6, device, vlan, net6_src, net6_dst, ip_frag, proto_range
);

FILTER_COMPILER_DECLARE(
	f_ip6_port,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);
FILTER_QUERY_DECLARE(
	q_ip6_port,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);

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
	size_t arena_mb = argc > 3 ? (size_t)atol(argv[3]) : 56000;
	const char *result_path = argc > 4 ? argv[4] : NULL;

	struct filter_rule *rules;
	const struct filter_rule **all;
	struct bench_stats stats;
	if (bench_load_full(rule_path, &rules, &all, &stats)) {
		return 1;
	}

	const struct filter_rule **proj =
		malloc(sizeof(*proj) * stats.rule_count * 6);
	uint32_t n_l2 = bench_project(proj, all, stats.rule_count, check_l2);
	uint32_t n_ip4 = bench_project(
		proj + stats.rule_count, all, stats.rule_count, check_ip4
	);
	uint32_t n_ip4p = bench_project(
		proj + 2 * stats.rule_count,
		all,
		stats.rule_count,
		check_ip4_port
	);
	uint32_t n_ip6 = bench_project(
		proj + 3 * stats.rule_count, all, stats.rule_count, check_ip6
	);
	uint32_t n_ip6p = bench_project(
		proj + 4 * stats.rule_count,
		all,
		stats.rule_count,
		check_ip6_port
	);
	printf("old acl.in: rules=%u l2=%u ip4=%u ip4_port=%u ip6=%u "
	       "ip6_port=%u\n",
	       stats.rule_count,
	       n_l2,
	       n_ip4,
	       n_ip4p,
	       n_ip6,
	       n_ip6p);

	void *arena = malloc(arena_mb << 20);
	struct block_allocator allocator;
	block_allocator_init(&allocator);
	block_allocator_put_arena(&allocator, arena, arena_mb << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "bench", &allocator);

	struct filter flt_vlan, flt_ip4, flt_ip4p, flt_ip6, flt_ip6p;
	struct timespec t0, t1, tall0, tall1;
	int rc = 0;

	clock_gettime(CLOCK_MONOTONIC, &tall0);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_vlan, f_vlan, proj, stats.rule_count, &mctx, "vlan", NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old vlan:      %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip4,
		f_ip4,
		proj + stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip4",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip4:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip4p,
		f_ip4_port,
		proj + 2 * stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip4_port",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip4_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip6,
		f_ip6,
		proj + 3 * stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip6",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip6:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(
		&flt_ip6p,
		f_ip6_port,
		proj + 4 * stats.rule_count,
		stats.rule_count,
		&mctx,
		"ip6_port",
		NULL
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old ip6_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
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

	uint32_t *r6 = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *r6p = malloc(sizeof(uint32_t) * cap.count);
	uint32_t *merged = malloc(sizeof(uint32_t) * cap.count);

	for (uint32_t off = 0; off < cap.count; off += BATCH) {
		uint32_t n = cap.count - off < BATCH ? cap.count - off : BATCH;
		filter_query(&flt_ip6, q_ip6, cap.ptrs + off, r6 + off, n);
		filter_query(
			&flt_ip6p, q_ip6_port, cap.ptrs + off, r6p + off, n
		);
	}
	uint32_t matched = 0;
	for (uint32_t idx = 0; idx < cap.count; ++idx) {
		merged[idx] = merge_first(r6[idx], r6p[idx]);
		matched += merged[idx] != FILTER_RULE_INVALID;
	}
	printf("old v6 lookup: matched %u/%u (%.1f%%) fnv=%llx\n",
	       matched,
	       cap.count,
	       100.0 * matched / cap.count,
	       (unsigned long long)bench_fnv1a(merged, cap.count));
	if (result_path != NULL) {
		FILE *rf = fopen(result_path, "wb");
		fwrite(merged, sizeof(uint32_t), cap.count, rf);
		fclose(rf);
	}

	const uint32_t passes = 50;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	for (uint32_t pass = 0; pass < passes; ++pass) {
		for (uint32_t off = 0; off < cap.count; off += BATCH) {
			uint32_t n = cap.count - off < BATCH ? cap.count - off
							     : BATCH;
			filter_query(
				&flt_ip6, q_ip6, cap.ptrs + off, r6 + off, n
			);
			filter_query(
				&flt_ip6p,
				q_ip6_port,
				cap.ptrs + off,
				r6p + off,
				n
			);
		}
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("old v6 steady: %.1f ns/pkt (both filters)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

	filter_free(&flt_ip6p, f_ip6_port);
	filter_free(&flt_ip6, f_ip6);
	filter_free(&flt_ip4p, f_ip4_port);
	filter_free(&flt_ip4, f_ip4);
	filter_free(&flt_vlan, f_vlan);
	(void)q_vlan;
	(void)q_ip4;
	(void)q_ip4_port;
	(void)n_l2;
	(void)check_has4_any;
	(void)check_has6_any;
	bench_capture_free(&cap);
	return 0;
}
