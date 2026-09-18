// acl module benchmark, shared core path: the l2 filter plus two
// network cores (v4 and v6) with two derivations each over a dumped
// ruleset, then the v6 lookup path (both v6 filters per packet, first
// match merge) over a capture.
//
// Usage: bench_acl_core <rules.bin> <capture.pcap> [arena MiB]
//                   [results.bin]
//
// The ruleset dump comes from dump_acl_ruleset.py; with results.bin on
// both benchmarks the per packet rule indices must match the production
// path byte for byte.

#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

#include "bench_acl_common.h"
#include "bench_pcap.h"

FILTER_COMPILER_DECLARE(core_v4, device, vlan, net4_src, net4_dst);
static const struct filter_compile_attr_handlers *sign_ip4[] = {
	FILTER_ATTR_COMPILE(device),
	FILTER_ATTR_COMPILE(vlan),
	FILTER_ATTR_COMPILE(net4_src),
	FILTER_ATTR_COMPILE(net4_dst),
	FILTER_ATTR_COMPILE(ipfrag),
	FILTER_ATTR_COMPILE(proto_range),
};
static const struct filter_compile_attr_handlers *sign_ip4_port[] = {
	FILTER_ATTR_COMPILE(device),
	FILTER_ATTR_COMPILE(vlan),
	FILTER_ATTR_COMPILE(net4_src),
	FILTER_ATTR_COMPILE(net4_dst),
	FILTER_ATTR_COMPILE(proto_range),
	FILTER_ATTR_COMPILE(port_src),
	FILTER_ATTR_COMPILE(port_dst),
};

FILTER_COMPILER_DECLARE(core_v6, device, vlan, net6_src, net6_dst);
static const struct filter_compile_attr_handlers *sign_ip6[] = {
	FILTER_ATTR_COMPILE(device),
	FILTER_ATTR_COMPILE(vlan),
	FILTER_ATTR_COMPILE(net6_src),
	FILTER_ATTR_COMPILE(net6_dst),
	FILTER_ATTR_COMPILE(ipfrag),
	FILTER_ATTR_COMPILE(proto_range),
};
static const struct filter_compile_attr_handlers *sign_ip6_port[] = {
	FILTER_ATTR_COMPILE(device),
	FILTER_ATTR_COMPILE(vlan),
	FILTER_ATTR_COMPILE(net6_src),
	FILTER_ATTR_COMPILE(net6_dst),
	FILTER_ATTR_COMPILE(proto_range),
	FILTER_ATTR_COMPILE(port_src),
	FILTER_ATTR_COMPILE(port_dst),
};

FILTER_COMPILER_DECLARE(f_vlan, device, vlan);
FILTER_QUERY_DECLARE(
	q_ip6, device, vlan, net6_src, net6_dst, ipfrag, proto_range
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
	size_t arena_mb = argc > 3 ? (size_t)atol(argv[3]) : 40000;
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
	uint32_t n_v4any = bench_project(
		proj + 5 * stats.rule_count,
		all,
		stats.rule_count,
		check_has4_any
	);
	(void)n_v4any;
	(void)check_has6_any;
	printf("new acl.in: rules=%u l2=%u ip4=%u ip4_port=%u ip6=%u "
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

	struct timespec t0, t1, tall0, tall1;
	int rc = 0;

	struct filter flt_vlan;
	struct filter_core core4;
	struct filter flt_ip4, flt_ip4p;
	struct filter_core core6;
	struct filter flt_ip6, flt_ip6p;

	// The v4 and v6 union rulesets over which the cores are built.
	const struct filter_rule **v4_union =
		malloc(sizeof(*v4_union) * stats.rule_count);
	const struct filter_rule **v6_union =
		malloc(sizeof(*v6_union) * stats.rule_count);
	uint32_t n_v4u =
		bench_project(v4_union, all, stats.rule_count, check_has4_any);
	uint32_t n_v6u =
		bench_project(v6_union, all, stats.rule_count, check_has6_any);
	(void)n_v4u;
	(void)n_v6u;

	clock_gettime(CLOCK_MONOTONIC, &tall0);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_init(&flt_vlan, f_vlan, proj, stats.rule_count, &mctx);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new vlan:      %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_core_init(
		&core4, core_v4, v4_union, stats.rule_count, &mctx
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v4 core:   %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_derive_init(
		&flt_ip4, &core4, proj + stats.rule_count, sign_ip4
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6..4:    %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_derive_init(
		&flt_ip4p, &core4, proj + 2 * stats.rule_count, sign_ip4_port
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip4_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);

	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_core_init(
		&core6, core_v6, v6_union, stats.rule_count, &mctx
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new v6 core:   %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_derive_init(
		&flt_ip6, &core6, proj + 3 * stats.rule_count, sign_ip6
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6:       %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rc |= filter_derive_init(
		&flt_ip6p, &core6, proj + 4 * stats.rule_count, sign_ip6_port
	);
	clock_gettime(CLOCK_MONOTONIC, &t1);
	printf("new ip6_port:  %8.1f ms rc=%d\n", bench_ms(&t0, &t1), rc);
	clock_gettime(CLOCK_MONOTONIC, &tall1);
	printf("new compile total: %.1f ms rc=%d\n",
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
	printf("new v6 lookup: matched %u/%u (%.1f%%) fnv=%llx\n",
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
	printf("new v6 steady: %.1f ns/pkt (both filters)\n",
	       bench_ms(&t0, &t1) * 1e6 / ((double)passes * cap.count));

	filter_free(&flt_ip6p, sign_ip6_port);
	filter_free(&flt_ip6, sign_ip6);
	filter_core_free(&core6, core_v6);
	filter_free(&flt_ip4p, sign_ip4_port);
	filter_free(&flt_ip4, sign_ip4);
	filter_core_free(&core4, core_v4);
	filter_free(&flt_vlan, f_vlan);
	bench_capture_free(&cap);
	return 0;
}
